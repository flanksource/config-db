package github

import (
	"encoding/json"
	"fmt"
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

			externalConfigID := fmt.Sprintf("github/%s", repoFullName)

			var alerts *allAlerts
			if config.Security {
				alerts, err = scrapeSecurityAlerts(ctx, client, config, repoFullName)
				if err != nil {
					results.Errorf(err, "failed to scrape security alerts for %s", repoFullName)
				}
			}

			var scorecard *ScorecardResponse
			if config.OpenSSF {
				scorecard, err = scrapeOpenSSFScorecard(ctx, repoConfig)
				if err != nil {
					results.Errorf(err, "failed to fetch OpenSSF scorecard for %s", repoFullName)
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
				createAlertAnalyses(ctx, &results, externalConfigID, alerts, openssfCheckNames)
			}

			if scorecard != nil {
				createScorecardAnalyses(ctx, &results, externalConfigID, repoConfig, scorecard)
			}
		}
	}

	return results
}

func buildRepositoryResult(repo *github.Repository, repoConfig v1.GitHubRepository, alerts *allAlerts, scorecard *ScorecardResponse) v1.ScrapeResult {
	repoFullName := fmt.Sprintf("%s/%s", repoConfig.Owner, repoConfig.Repo)
	externalConfigID := fmt.Sprintf("github/%s", repoFullName)

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
		ID:          externalConfigID,
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

func createAlertAnalyses(ctx api.ScrapeContext, results *v1.ScrapeResults, externalConfigID string, alerts *allAlerts, openssfCheckNames map[string]bool) {
	for _, alert := range alerts.dependabot {
		a := results.Analysis(
			fmt.Sprintf("dependabot/%d", alert.GetNumber()),
			ConfigTypeRepository, externalConfigID,
		)
		if externalAnalysisID := alert.GetURL(); externalAnalysisID != "" {
			a.ExternalAnalysisID = externalAnalysisID
		}
		a.AnalysisType = models.AnalysisTypeSecurity
		a.Severity = mapGitHubSeverity(alert.SecurityAdvisory.GetSeverity())
		a.Source = "GitHub Dependabot"
		a.Analyzer = alert.GetDependency().GetPackage().GetEcosystem()
		a.Summary = alert.SecurityAdvisory.GetSummary()
		a.Status = alert.GetState()
		if t := alert.CreatedAt.GetTime(); t != nil {
			a.FirstObserved = t
		}
		if t := alert.UpdatedAt.GetTime(); t != nil {
			a.LastObserved = t
		}
		a.Message(alert.SecurityAdvisory.GetDescription())
		a.Analysis = alertToMap(alert)

		if htmlURL := alert.GetHTMLURL(); htmlURL != "" {
			a.Properties = append(a.Properties, &types.Property{
				Name:  "URL",
				Text:  htmlURL,
				Type:  "url",
				Links: []types.Link{{URL: htmlURL, Type: "url"}},
			})
		}
		if cve := alert.SecurityAdvisory.GetCVEID(); cve != "" {
			a.Properties = append(a.Properties, &types.Property{
				Name:  "CVE ID",
				Text:  cve,
				Type:  "badge",
				Links: []types.Link{{URL: "https://nvd.nist.gov/vuln/detail/" + cve, Type: "url"}},
			})
		}
		if ghsa := alert.SecurityAdvisory.GetGHSAID(); ghsa != "" {
			a.Properties = append(a.Properties, &types.Property{
				Name:  "GHSA ID",
				Text:  ghsa,
				Type:  "badge",
				Links: []types.Link{{URL: "https://github.com/advisories/" + ghsa, Type: "url"}},
			})
		}
		if cvss := alert.SecurityAdvisory.GetCVSS(); cvss != nil {
			if score := cvss.GetScore(); score != nil {
				scoreInt := int64(*score * 10)
				maxScore := int64(100)
				a.Properties = append(a.Properties, &types.Property{
					Name:  "CVSS Score",
					Value: &scoreInt,
					Max:   &maxScore,
					Type:  "badge",
					Color: badgeColorInverted(scoreInt, maxScore),
				})
			}
			if vector := cvss.GetVectorString(); vector != "" {
				a.Properties = append(a.Properties, &types.Property{
					Name: "CVSS Vector",
					Text: vector,
					Type: "badge",
				})
			}
		}
		if epss := alert.SecurityAdvisory.GetEPSS(); epss != nil {
			a.Properties = append(a.Properties, &types.Property{
				Name: "EPSS Score",
				Text: fmt.Sprintf("%.3f%% (%gth percentile)", epss.Percentage*100, epss.Percentile*100),
				Type: "badge",
			})
		}
		for _, cwe := range alert.SecurityAdvisory.CWEs {
			if cweID := cwe.GetCWEID(); cweID != "" {
				cweURL := fmt.Sprintf("https://cwe.mitre.org/data/definitions/%s.html", cweID[4:]) // strip "CWE-" prefix
				a.Properties = append(a.Properties, &types.Property{
					Name:  cweID,
					Text:  fmt.Sprintf("%s: %s", cweID, cwe.GetName()),
					Type:  "badge",
					Links: []types.Link{{URL: cweURL, Type: "url"}},
				})
			}
		}
		if dep := alert.GetDependency(); dep != nil {
			if pkg := dep.GetPackage(); pkg != nil {
				a.Properties = append(a.Properties, &types.Property{
					Name: "Package",
					Text: fmt.Sprintf("%s (%s)", pkg.GetName(), pkg.GetEcosystem()),
					Type: "badge",
				})
			}
			if scope := dep.GetScope(); scope != "" {
				a.Properties = append(a.Properties, &types.Property{
					Name: "Dependency Scope",
					Text: scope,
					Type: "badge",
				})
			}
		}
		if vuln := alert.GetSecurityVulnerability(); vuln != nil {
			if versionRange := vuln.GetVulnerableVersionRange(); versionRange != "" {
				a.Properties = append(a.Properties, &types.Property{
					Name: "Vulnerable Versions",
					Text: versionRange,
					Type: "badge",
				})
			}
			if patched := vuln.GetFirstPatchedVersion(); patched != nil {
				if ver := patched.GetIdentifier(); ver != "" {
					a.Properties = append(a.Properties, &types.Property{
						Name:  "Patched Version",
						Text:  ver,
						Type:  "badge",
						Color: "bg-green-100 border-green-200 text-green-800",
					})
				}
			}
		}
	}

	for _, alert := range alerts.codeScanning {
		if openssfCheckNames[alert.Rule.GetDescription()] {
			ctx.Debugf("skipping code scanning alert %d (covered by OpenSSF check %s)", alert.GetNumber(), alert.Rule.GetDescription())
			continue
		}

		a := results.Analysis(
			fmt.Sprintf("code-scanning/%d", alert.GetNumber()),
			ConfigTypeRepository, externalConfigID,
		)
		if externalAnalysisID := alert.GetURL(); externalAnalysisID != "" {
			a.ExternalAnalysisID = externalAnalysisID
		}
		a.AnalysisType = models.AnalysisTypeSecurity
		a.Severity = mapGitHubSeverity(alert.Rule.GetSeverity())
		a.Source = "GitHub Code Scanning"
		a.Analyzer = alert.Rule.GetID()
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
		a.Analysis = alertToMap(alert)

		if htmlURL := alert.GetHTMLURL(); htmlURL != "" {
			a.Properties = append(a.Properties, &types.Property{
				Name:  "URL",
				Text:  htmlURL,
				Type:  "url",
				Links: []types.Link{{URL: htmlURL, Type: "url"}},
			})
		}
		if tool := alert.GetTool(); tool != nil {
			toolText := tool.GetName()
			if ver := tool.GetVersion(); ver != "" {
				toolText = fmt.Sprintf("%s %s", toolText, ver)
			}
			a.Properties = append(a.Properties, &types.Property{
				Name: "Tool",
				Text: toolText,
				Type: "badge",
			})
		}
	}

	for _, alert := range alerts.secretScanning {
		a := results.Analysis(
			fmt.Sprintf("secret-scanning/%d", alert.GetNumber()),
			ConfigTypeRepository, externalConfigID,
		)
		if externalAnalysisID := alert.GetURL(); externalAnalysisID != "" {
			a.ExternalAnalysisID = externalAnalysisID
		}
		a.AnalysisType = models.AnalysisTypeSecurity
		a.Severity = models.SeverityHigh
		a.Source = "GitHub Secret Scanning"
		a.Analyzer = alert.GetSecretType()
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
		a.Analysis = alertToMap(alert)

		if htmlURL := alert.GetHTMLURL(); htmlURL != "" {
			a.Properties = append(a.Properties, &types.Property{
				Name:  "URL",
				Text:  htmlURL,
				Type:  "url",
				Links: []types.Link{{URL: htmlURL, Type: "url"}},
			})
		}
	}
}

// alertToMap converts any struct to a map[string]any via JSON marshaling.
func alertToMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

// badgeColor returns muted Tailwind classes for a "higher is better" ratio.
// Used for scores like OpenSSF where a high value is good.
func badgeColor(value, max int64) string {
	if max <= 0 {
		return ""
	}
	ratio := float64(value) / float64(max) * 100
	switch {
	case ratio > 80:
		return "bg-green-100 border-green-200 text-green-800"
	case ratio > 50:
		return "bg-yellow-100 border-yellow-200 text-yellow-800"
	case ratio > 30:
		return "bg-orange-100 border-orange-200 text-orange-800"
	default:
		return "bg-red-100 border-red-200 text-red-800"
	}
}

// badgeColorInverted returns muted Tailwind classes for a "lower is better" ratio.
// Used for scores like CVSS where a high value means higher risk.
func badgeColorInverted(value, max int64) string {
	if max <= 0 {
		return ""
	}
	ratio := float64(value) / float64(max) * 100
	switch {
	case ratio > 80:
		return "bg-red-100 border-red-200 text-red-800"
	case ratio > 50:
		return "bg-orange-100 border-orange-200 text-orange-800"
	case ratio > 30:
		return "bg-yellow-100 border-yellow-200 text-yellow-800"
	default:
		return "bg-green-100 border-green-200 text-green-800"
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
