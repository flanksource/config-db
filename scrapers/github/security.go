package github

import (
	"context"
	"fmt"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/types"
	"github.com/flanksource/is-healthy/pkg/health"
	"github.com/google/go-github/v73/github"
)

const (
	ConfigTypeGitHubSecurityRepo = "GitHub::Repository::Security"
)

// GithubSecurityScraper implements security alert scraping for GitHub repositories
type GithubSecurityScraper struct{}

func (gh GithubSecurityScraper) CanScrape(spec v1.ScraperSpec) bool {
	return len(spec.GitHubSecurity) > 0
}

func (gh GithubSecurityScraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	results := v1.ScrapeResults{}

	for _, config := range ctx.ScrapeConfig().Spec.GitHubSecurity {
		for _, repoConfig := range config.Repositories {
			client, err := NewGitHubSecurityClient(ctx, config, repoConfig.Owner, repoConfig.Repo)
			if err != nil {
				results.Errorf(err, "failed to create GitHub security client for %s/%s", repoConfig.Owner, repoConfig.Repo)
				continue
			}

			if shouldPause, duration, err := client.ShouldPauseForRateLimit(ctx); err != nil {
				results.Errorf(err, "failed to check rate limit for %s/%s", repoConfig.Owner, repoConfig.Repo)
				continue
			} else if shouldPause {
				ctx.Warnf("pausing for %v due to rate limit", duration)
				time.Sleep(duration)
			}

			result, err := scrapeRepository(ctx, client, config, repoConfig)
			if err != nil {
				results.Errorf(err, "failed to scrape repository %s/%s", repoConfig.Owner, repoConfig.Repo)
				continue
			}

			results = append(results, result)
		}
	}

	return results
}

func scrapeRepository(ctx api.ScrapeContext, client *GitHubSecurityClient, config v1.GitHubSecurity, repoConfig v1.GitHubSecurityRepository) (v1.ScrapeResult, error) {
	// FIXME: Implement full repository scraping
	repoFullName := fmt.Sprintf("%s/%s", repoConfig.Owner, repoConfig.Repo)

	// Fetch repository metadata
	repo, _, err := client.Client.Repositories.Get(ctx, repoConfig.Owner, repoConfig.Repo)
	if err != nil {
		return v1.ScrapeResult{}, fmt.Errorf("failed to get repository metadata: %w", err)
	}

	// Fetch alerts
	alerts, err := fetchAllAlerts(ctx, client, config)
	if err != nil {
		return v1.ScrapeResult{}, fmt.Errorf("failed to fetch alerts: %w", err)
	}

	// Calculate health status
	healthStatus := calculateHealthStatus(alerts)

	// Create properties
	properties := createRepositoryProperties(repo, alerts)

	result := v1.ScrapeResult{
		Type:        ConfigTypeGitHubSecurityRepo,
		ID:          fmt.Sprintf("github-security/%s", repoFullName),
		Name:        repoFullName,
		ConfigClass: "Security",
		Config:      repo,
		Tags: v1.Tags{
			{Name: "owner", Value: repoConfig.Owner},
			{Name: "repo", Value: repoConfig.Repo},
			{Name: "security", Value: "true"},
		},
		CreatedAt:  repo.CreatedAt.GetTime(),
		Properties: properties,
	}

	result = result.WithHealthStatus(healthStatus)

	return result, nil
}

type alertCounts struct {
	critical int
	high     int
	medium   int
	low      int
}

type allAlerts struct {
	dependabot     []*github.DependabotAlert
	codeScanning   []*github.Alert
	secretScanning []*github.SecretScanningAlert
	advisories     []*github.SecurityAdvisory
	counts         alertCounts
}

func fetchAllAlerts(ctx context.Context, client *GitHubSecurityClient, config v1.GitHubSecurity) (*allAlerts, error) {
	alerts := &allAlerts{}

	filters := config.Filters
	stateFilter := "open"
	if len(filters.State) > 0 {
		stateFilter = filters.State[0]
	}

	opts := AlertListOptions{
		State:   stateFilter,
		Page:    1,
		PerPage: 100,
	}

	// Fetch Dependabot alerts
	dependabotAlerts, _, err := client.GetDependabotAlerts(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get Dependabot alerts: %w", err)
	}
	alerts.dependabot = dependabotAlerts

	// Fetch code scanning alerts
	codeScanAlerts, _, err := client.GetCodeScanningAlerts(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get code scanning alerts: %w", err)
	}
	alerts.codeScanning = codeScanAlerts

	// Fetch secret scanning alerts
	secretAlerts, _, err := client.GetSecretScanningAlerts(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret scanning alerts: %w", err)
	}
	alerts.secretScanning = secretAlerts

	// Count alerts by severity
	for _, alert := range dependabotAlerts {
		countAlertSeverity(&alerts.counts, alert.SecurityAdvisory.GetSeverity())
	}
	for _, alert := range codeScanAlerts {
		countAlertSeverity(&alerts.counts, alert.Rule.GetSeverity())
	}

	return alerts, nil
}

func countAlertSeverity(counts *alertCounts, severity string) {
	switch severity {
	case "critical":
		counts.critical++
	case "high":
		counts.high++
	case "medium":
		counts.medium++
	case "low":
		counts.low++
	}
}

func calculateHealthStatus(alerts *allAlerts) health.HealthStatus {
	status := health.HealthStatus{
		Health: health.HealthHealthy,
		Ready:  true,
	}

	counts := alerts.counts

	if counts.critical > 0 {
		status.Health = health.HealthUnhealthy
		status.Message = fmt.Sprintf("%d critical alerts", counts.critical)
		if counts.high > 0 {
			status.Message += fmt.Sprintf(", %d high alerts", counts.high)
		}
	} else if counts.high >= 5 {
		status.Health = health.HealthUnhealthy
		status.Message = fmt.Sprintf("%d high severity alerts", counts.high)
	} else if counts.high > 0 {
		status.Health = health.HealthWarning
		status.Message = fmt.Sprintf("%d high alerts", counts.high)
	} else if counts.medium >= 10 {
		status.Health = health.HealthWarning
		status.Message = fmt.Sprintf("%d medium alerts", counts.medium)
	}

	if status.Health == health.HealthHealthy {
		if counts.low > 0 || counts.medium > 0 {
			status.Message = fmt.Sprintf("%d medium, %d low alerts", counts.medium, counts.low)
		} else {
			status.Message = "No security alerts"
		}
	}

	return status
}

func createRepositoryProperties(repo *github.Repository, alerts *allAlerts) []*types.Property {
	properties := []*types.Property{
		{
			Name: "URL",
			Type: "url",
			Text: repo.GetHTMLURL(),
			Links: []types.Link{
				{URL: repo.GetHTMLURL(), Type: "url"},
			},
		},
		{
			Name: "Critical Alerts",
			Type: "number",
			Text: fmt.Sprintf("%d", alerts.counts.critical),
		},
		{
			Name: "High Alerts",
			Type: "number",
			Text: fmt.Sprintf("%d", alerts.counts.high),
		},
		{
			Name: "Medium Alerts",
			Type: "number",
			Text: fmt.Sprintf("%d", alerts.counts.medium),
		},
		{
			Name: "Low Alerts",
			Type: "number",
			Text: fmt.Sprintf("%d", alerts.counts.low),
		},
	}

	return properties
}

// createConfigInsights creates ConfigInsight records for each alert
// FIXME: Implement full ConfigInsight creation with all alert details
func createConfigInsights(ctx api.ScrapeContext, configID string, alerts *allAlerts) error {
	// This will be implemented to create individual ConfigInsight records
	// for each alert type with proper mapping
	return nil
}
