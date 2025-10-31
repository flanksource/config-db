package openssf

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/flanksource/is-healthy/pkg/health"
)

const (
	ConfigTypeOpenSSFScorecardRepo = "OpenSSF::Scorecard::Repository"
	ScorecardCacheTTL              = 24 * time.Hour
)

// LastScorecardScrapeTime tracks the last assessment date for each repository to avoid redundant API calls
var LastScorecardScrapeTime = sync.Map{}

// OpenSSFScorecardScraper implements OpenSSF Scorecard scraping
type OpenSSFScorecardScraper struct{}

func (o OpenSSFScorecardScraper) CanScrape(spec v1.ScraperSpec) bool {
	return len(spec.OpenSSFScorecard) > 0
}

func (o OpenSSFScorecardScraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	results := v1.ScrapeResults{}

	for _, config := range ctx.ScrapeConfig().Spec.OpenSSFScorecard {
		ctx.Logger.V(2).Infof("scraping OpenSSF Scorecard for %d repositories", len(config.Repositories))
		client := NewScorecardClient(ctx)

		for _, repoConfig := range config.Repositories {
			repoFullName := fmt.Sprintf("%s/%s", repoConfig.Owner, repoConfig.Repo)
			ctx.Logger.V(2).Infof("fetching scorecard for repository: %s", repoFullName)

			if shouldSkip, reason := shouldSkipScorecardFetch(ctx, repoFullName); shouldSkip {
				ctx.Debugf("skipping %s: %s", repoFullName, reason)
				continue
			}

			scorecard, err := client.GetRepositoryScorecard(ctx, repoConfig.Owner, repoConfig.Repo)
			if err != nil {
				results.Errorf(err, "failed to scrape repository %s", repoFullName)
				continue
			}

			if config.MinScore != nil && scorecard.Score < *config.MinScore {
				ctx.Debugf("skipping %s: score %.1f below minimum %.1f",
					repoFullName, scorecard.Score, *config.MinScore)
				continue
			}

			LastScorecardScrapeTime.Store(repoFullName, time.Now())
			ctx.Logger.V(3).Infof("stored last scorecard scrape time for %s: %v", repoFullName, scorecard.Date)

			result := createRepositoryConfig(ctx, repoConfig, scorecard)
			results = append(results, result)

			createCheckAnalyses(ctx, &results, repoConfig, scorecard)

			ctx.Logger.V(2).Infof("successfully scraped %s: score %.1f/10, %d checks", repoFullName, scorecard.Score, len(scorecard.Checks))
		}
	}

	return results
}

func shouldSkipScorecardFetch(ctx api.ScrapeContext, repoFullName string) (bool, string) {
	if lastScrape, ok := LastScorecardScrapeTime.Load(repoFullName); ok {
		lastTime := lastScrape.(time.Time)
		timeSinceLastScrape := time.Since(lastTime)
		if timeSinceLastScrape < ScorecardCacheTTL {
			return true, fmt.Sprintf("last scraped %v ago (cache TTL: %v)", timeSinceLastScrape, ScorecardCacheTTL)
		}
		ctx.Logger.V(3).Infof("cache expired for %s (last scraped %v ago)", repoFullName, timeSinceLastScrape)
	}
	return false, ""
}

func createRepositoryConfig(ctx api.ScrapeContext, repoConfig v1.OpenSSFRepository, scorecard *ScorecardResponse) v1.ScrapeResult {
	repoFullName := fmt.Sprintf("%s/%s", repoConfig.Owner, repoConfig.Repo)

	ctx.Debugf("scorecard results for %s: overall score=%.1f, checks=%d", repoFullName, scorecard.Score, len(scorecard.Checks))

	healthStatus := calculateHealthStatus(scorecard)
	properties := createRepositoryProperties(repoConfig.Owner, repoConfig.Repo, scorecard)

	result := v1.ScrapeResult{
		Type:        ConfigTypeOpenSSFScorecardRepo,
		ID:          fmt.Sprintf("openssf-scorecard/%s", repoFullName),
		Name:        repoFullName,
		ConfigClass: "Security",
		Config:      scorecard,
		Tags: v1.Tags{
			{Name: "owner", Value: repoConfig.Owner},
			{Name: "repo", Value: repoConfig.Repo},
			{Name: "openssf", Value: "true"},
			{Name: "scorecard", Value: "true"},
		},
		CreatedAt:  &scorecard.Date,
		Properties: properties,
	}

	result = result.WithHealthStatus(healthStatus)

	return result
}

func createCheckAnalyses(ctx api.ScrapeContext, results *v1.ScrapeResults, repoConfig v1.OpenSSFRepository, scorecard *ScorecardResponse) {
	repoFullName := fmt.Sprintf("%s/%s", repoConfig.Owner, repoConfig.Repo)
	configID := fmt.Sprintf("openssf-scorecard/%s", repoFullName)

	ctx.Debugf("creating analysis records for %d checks in %s", len(scorecard.Checks), repoFullName)

	for _, check := range scorecard.Checks {
		compliance := GetComplianceMappings(check.Name)
		ctx.Tracef("check %s: score=%d/10, SOC2=%d, NIST=%d, CIS=%d",
			check.Name, check.Score, len(compliance.SOC2), len(compliance.NISTSSDF), len(compliance.CIS))

		analysis := results.Analysis(
			check.Name,
			ConfigTypeOpenSSFScorecardRepo,
			configID,
		)

		analysis.AnalysisType = models.AnalysisTypeSecurity
		analysis.Severity = mapCheckScoreToSeverity(check.Score)
		analysis.Source = "OpenSSF Scorecard"
		analysis.Summary = check.Reason
		analysis.FirstObserved = &scorecard.Date
		analysis.LastObserved = &scorecard.Date

		if check.Score < 10 {
			analysis.Status = "failing"
		} else {
			analysis.Status = "passing"
		}

		for _, detail := range check.Details {
			analysis.Message(detail)
		}

		analysis.Analysis = map[string]any{
			"check_name":  check.Name,
			"score":       check.Score,
			"max_score":   10,
			"reason":      check.Reason,
			"details":     check.Details,
			"documentation": map[string]string{
				"url":   check.Documentation.URL,
				"short": check.Documentation.Short,
			},
			"compliance": map[string][]string{
				"SOC 2":      compliance.SOC2,
				"NIST SSDF":  compliance.NISTSSDF,
				"CIS Controls": compliance.CIS,
			},
		}

		ctx.Tracef("created analysis for check %s (severity: %s, status: %s)",
			check.Name, analysis.Severity, analysis.Status)
	}

	ctx.Debugf("created %d analysis records for %s", len(scorecard.Checks), repoFullName)
}

func calculateHealthStatus(scorecard *ScorecardResponse) health.HealthStatus {
	status := health.HealthStatus{
		Ready: true,
	}

	criticalChecks := []string{"Code-Review", "SAST", "Token-Permissions", "Dangerous-Workflow", "Branch-Protection"}
	var failedCritical []string

	for _, check := range scorecard.Checks {
		if check.Score == 0 && containsString(criticalChecks, check.Name) {
			failedCritical = append(failedCritical, check.Name)
		}
	}

	if scorecard.Score >= 7.0 && len(failedCritical) == 0 {
		status.Health = health.HealthHealthy
		status.Message = fmt.Sprintf("Security score: %.1f/10", scorecard.Score)
	} else if scorecard.Score < 4.0 || len(failedCritical) > 0 {
		status.Health = health.HealthUnhealthy
		if len(failedCritical) > 0 {
			status.Message = fmt.Sprintf("Security score: %.1f/10, critical checks failing: %s",
				scorecard.Score, strings.Join(failedCritical, ", "))
		} else {
			status.Message = fmt.Sprintf("Security score: %.1f/10", scorecard.Score)
		}
	} else {
		status.Health = health.HealthWarning
		status.Message = fmt.Sprintf("Security score: %.1f/10", scorecard.Score)
	}

	return status
}

func createRepositoryProperties(owner, repo string, scorecard *ScorecardResponse) []*types.Property {
	badgeURL := fmt.Sprintf("%s/projects/github.com/%s/%s/badge", OpenSSFScorecardAPIBase, owner, repo)
	viewerURL := fmt.Sprintf("https://scorecard.dev/viewer/?uri=github.com/%s/%s", owner, repo)

	properties := []*types.Property{
		{
			Name: "Overall Score",
			Type: "number",
			Text: fmt.Sprintf("%.1f", scorecard.Score),
		},
		{
			Name: "Scorecard Version",
			Type: "text",
			Text: scorecard.Scorecard.Version,
		},
		{
			Name: "Commit SHA",
			Type: "text",
			Text: scorecard.Repo.Commit,
		},
		{
			Name: "Assessment Date",
			Type: "datetime",
			Text: scorecard.Date.Format("2006-01-02T15:04:05Z"),
		},
		{
			Name: "Badge",
			Type: "badge",
			Text: badgeURL,
			Links: []types.Link{
				{URL: badgeURL, Type: "badge"},
			},
		},
		{
			Name: "URL",
			Type: "url",
			Text: viewerURL,
			Links: []types.Link{
				{URL: viewerURL, Type: "url"},
			},
		},
	}

	passingChecks := 0
	for _, check := range scorecard.Checks {
		if check.Score >= 7 {
			passingChecks++
		}
	}

	properties = append(properties, &types.Property{
		Name: "Passing Checks",
		Type: "text",
		Text: fmt.Sprintf("%d/%d", passingChecks, len(scorecard.Checks)),
	})

	return properties
}

func containsString(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func scorecardCheckNameToKebab(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "-", "-"))
}

func mapCheckScoreToSeverity(score int) models.Severity {
	if score <= 3 {
		return models.SeverityCritical
	} else if score <= 6 {
		return models.SeverityHigh
	} else if score <= 9 {
		return models.SeverityMedium
	}
	return models.SeverityLow
}
