package github

import (
	"fmt"
	"sync"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/flanksource/is-healthy/pkg/health"
	"github.com/google/go-github/v73/github"
)

const (
	ConfigTypeRepository = "GitHub::Repository"
)

var LastAlertScrapeTime = sync.Map{}

type GithubScraper struct{}

func (gh GithubScraper) CanScrape(spec v1.ScraperSpec) bool {
	return len(spec.GitHub) > 0
}

func (gh GithubScraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults

	for _, config := range ctx.ScrapeConfig().Spec.GitHub {
		for _, repoConfig := range config.Repositories {
			if err := ctx.Err(); err != nil {
				return results
			}

			repoFullName := fmt.Sprintf("%s/%s", repoConfig.Owner, repoConfig.Repo)
			ctx.Logger.V(2).Infof("scraping GitHub repository: %s", repoFullName)

			client, err := NewGitHubClient(ctx, config, repoConfig.Owner, repoConfig.Repo)
			if err != nil {
				results.Errorf(err, "failed to create GitHub client for %s", repoFullName)
				continue
			}

			if shouldPause, duration, err := client.ShouldPauseForRateLimit(ctx); err != nil {
				results.Errorf(err, "failed to check rate limit for %s", repoFullName)
				continue
			} else if shouldPause {
				resetAt := time.Now().Add(duration)
				return results.RateLimited(
					fmt.Sprintf("GitHub API rate limit for %s", repoFullName),
					&resetAt,
				)
			}

			repo, _, err := client.Client.Repositories.Get(ctx, repoConfig.Owner, repoConfig.Repo)
			if err != nil {
				results.Errorf(err, "failed to get repository metadata for %s", repoFullName)
				continue
			}

			configID := fmt.Sprintf("github/%s", repoFullName)

			var alerts *allAlerts
			if config.Security {
				alerts, err = scrapeSecurityAlerts(ctx, client, config, repoFullName)
				if err != nil {
					results.Errorf(err, "failed to scrape security alerts for %s", repoFullName)
					continue
				}
			}

			var scorecard *ScorecardResponse
			if config.OpenSSF {
				scorecard, err = scrapeOpenSSFScorecard(ctx, repoConfig)
				if err != nil {
					ctx.Warnf("failed to fetch OpenSSF scorecard for %s: %v", repoFullName, err)
				}
			}

			result := buildRepositoryResult(repo, repoConfig, alerts, scorecard)
			results = append(results, result)

			var openssfCheckNames map[string]bool
			if scorecard != nil {
				openssfCheckNames = make(map[string]bool, len(scorecard.Checks))
				for _, check := range scorecard.Checks {
					openssfCheckNames[check.Name] = true
				}
			}

			if alerts != nil {
				createAlertAnalyses(ctx, &results, configID, alerts, openssfCheckNames)
			}

			if scorecard != nil {
				createScorecardAnalyses(ctx, &results, configID, repoConfig, scorecard)
			}
		}
	}

	return results
}

func buildRepositoryResult(repo *github.Repository, repoConfig v1.GitHubRepository, alerts *allAlerts, scorecard *ScorecardResponse) v1.ScrapeResult {
	repoFullName := fmt.Sprintf("%s/%s", repoConfig.Owner, repoConfig.Repo)
	configID := fmt.Sprintf("github/%s", repoFullName)

	var properties []*types.Property
	properties = append(properties, &types.Property{
		Name: "URL",
		Type: "url",
		Text: repo.GetHTMLURL(),
		Links: []types.Link{
			{URL: repo.GetHTMLURL(), Type: "url"},
		},
	})

	healthStatus := health.HealthStatus{Health: health.HealthHealthy, Ready: true, Message: "No issues"}

	if alerts != nil {
		properties = append(properties, alertProperties(alerts)...)
		healthStatus = calculateAlertHealthStatus(alerts)
	}

	if scorecard != nil {
		properties = append(properties, scorecardProperties(repoConfig.Owner, repoConfig.Repo, scorecard)...)
		if alerts == nil {
			healthStatus = calculateScorecardHealthStatus(scorecard)
		}
	}

	result := v1.ScrapeResult{
		Type:        ConfigTypeRepository,
		ID:          configID,
		Name:        repoFullName,
		ConfigClass: "Repository",
		Config:      sanitizeRepository(repo),
		Tags: v1.JSONStringMap{
			"owner": repoConfig.Owner,
			"repo":  repoConfig.Repo,
		},
		CreatedAt:  repo.CreatedAt.GetTime(),
		Properties: properties,
	}

	return result.WithHealthStatus(healthStatus)
}

func alertProperties(alerts *allAlerts) []*types.Property {
	return []*types.Property{
		{Name: "Critical Alerts", Type: "number", Text: fmt.Sprintf("%d", alerts.counts.critical)},
		{Name: "High Alerts", Type: "number", Text: fmt.Sprintf("%d", alerts.counts.high)},
		{Name: "Medium Alerts", Type: "number", Text: fmt.Sprintf("%d", alerts.counts.medium)},
		{Name: "Low Alerts", Type: "number", Text: fmt.Sprintf("%d", alerts.counts.low)},
	}
}

func scorecardProperties(owner, repo string, scorecard *ScorecardResponse) []*types.Property {
	viewerURL := fmt.Sprintf("https://scorecard.dev/viewer/?uri=github.com/%s/%s", owner, repo)
	badgeURL := fmt.Sprintf("%s/projects/github.com/%s/%s/badge", OpenSSFScorecardAPIBase, owner, repo)

	passingChecks := 0
	for _, check := range scorecard.Checks {
		if check.Score >= 7 {
			passingChecks++
		}
	}

	return []*types.Property{
		{Name: "OpenSSF Score", Type: "number", Text: fmt.Sprintf("%.1f", scorecard.Score)},
		{Name: "OpenSSF Passing Checks", Type: "text", Text: fmt.Sprintf("%d/%d", passingChecks, len(scorecard.Checks))},
		{Name: "OpenSSF Badge", Type: "badge", Text: badgeURL, Links: []types.Link{{URL: badgeURL, Type: "badge"}}},
		{Name: "OpenSSF Viewer", Type: "url", Text: viewerURL, Links: []types.Link{{URL: viewerURL, Type: "url"}}},
	}
}

func createAlertAnalyses(ctx api.ScrapeContext, results *v1.ScrapeResults, configID string, alerts *allAlerts, openssfCheckNames map[string]bool) {
	for _, alert := range alerts.dependabot {
		a := results.Analysis(
			fmt.Sprintf("dependabot/%d", alert.GetNumber()),
			ConfigTypeRepository, configID,
		)
		a.AnalysisType = models.AnalysisTypeSecurity
		a.Severity = mapGitHubSeverity(alert.SecurityAdvisory.GetSeverity())
		a.Source = "GitHub Dependabot"
		a.Summary = alert.SecurityAdvisory.GetSummary()
		a.Status = alert.GetState()
		if t := alert.CreatedAt.GetTime(); t != nil {
			a.FirstObserved = t
		}
		if t := alert.UpdatedAt.GetTime(); t != nil {
			a.LastObserved = t
		}
		a.Message(alert.SecurityAdvisory.GetDescription())
		a.Analysis = map[string]any{"url": alert.GetHTMLURL()}
	}

	for _, alert := range alerts.codeScanning {
		if openssfCheckNames[alert.Rule.GetDescription()] {
			ctx.Debugf("skipping code scanning alert %d (covered by OpenSSF check %s)", alert.GetNumber(), alert.Rule.GetDescription())
			continue
		}

		a := results.Analysis(
			fmt.Sprintf("code-scanning/%d", alert.GetNumber()),
			ConfigTypeRepository, configID,
		)
		a.AnalysisType = models.AnalysisTypeSecurity
		a.Severity = mapGitHubSeverity(alert.Rule.GetSeverity())
		a.Source = "GitHub Code Scanning"
		a.Summary = alert.Rule.GetDescription()
		a.Status = alert.GetState()
		if alert.CreatedAt != nil {
			t := alert.CreatedAt.Time
			a.FirstObserved = &t
		}
		if alert.UpdatedAt != nil {
			t := alert.UpdatedAt.Time
			a.LastObserved = &t
		}
		a.Message(alert.GetMostRecentInstance().GetMessage().GetText())
		a.Analysis = map[string]any{"url": alert.GetHTMLURL()}
	}

	for _, alert := range alerts.secretScanning {
		a := results.Analysis(
			fmt.Sprintf("secret-scanning/%d", alert.GetNumber()),
			ConfigTypeRepository, configID,
		)
		a.AnalysisType = models.AnalysisTypeSecurity
		a.Severity = models.SeverityHigh
		a.Source = "GitHub Secret Scanning"
		a.Summary = fmt.Sprintf("Exposed %s secret", alert.GetSecretType())
		a.Status = alert.GetState()
		if alert.CreatedAt != nil {
			t := alert.CreatedAt.Time
			a.FirstObserved = &t
		}
		if alert.UpdatedAt != nil {
			t := alert.UpdatedAt.Time
			a.LastObserved = &t
		}
		a.Analysis = map[string]any{"url": alert.GetHTMLURL()}
	}
}

func mapGitHubSeverity(severity string) models.Severity {
	switch severity {
	case "critical":
		return models.SeverityCritical
	case "high":
		return models.SeverityHigh
	case "medium":
		return models.SeverityMedium
	case "low":
		return models.SeverityLow
	default:
		return models.SeverityInfo
	}
}
