package github

import (
	"fmt"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/is-healthy/pkg/health"
	"github.com/google/go-github/v73/github"
)

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
	counts         alertCounts
}

func scrapeSecurityAlerts(ctx api.ScrapeContext, client *GitHubClient, config v1.GitHub, repoFullName string) (*allAlerts, error) {
	alerts := &allAlerts{}

	filters := config.SecurityFilters
	stateFilter := "open"
	if len(filters.State) > 0 {
		stateFilter = filters.State[0]
	}

	opts := AlertListOptions{
		State:   stateFilter,
		Page:    1,
		PerPage: 100,
	}

	if lastTime, ok := LastAlertScrapeTime.Load(repoFullName); ok {
		if since, ok := lastTime.(time.Time); ok {
			opts.CreatedAt = since.Format(time.RFC3339)
			ctx.Logger.V(3).Infof("fetching alerts for %s since %v (incremental)", repoFullName, since)
		}
	}

	var maxAlertTime time.Time

	dependabotAlerts, _, err := client.GetDependabotAlerts(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get Dependabot alerts: %w", err)
	}
	alerts.dependabot = dependabotAlerts

	codeScanAlerts, _, err := client.GetCodeScanningAlerts(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get code scanning alerts: %w", err)
	}
	alerts.codeScanning = codeScanAlerts

	secretAlerts, _, err := client.GetSecretScanningAlerts(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret scanning alerts: %w", err)
	}
	alerts.secretScanning = secretAlerts

	for _, alert := range dependabotAlerts {
		countAlertSeverity(&alerts.counts, alert.SecurityAdvisory.GetSeverity())
		if alert.UpdatedAt != nil && alert.UpdatedAt.After(maxAlertTime) {
			maxAlertTime = alert.UpdatedAt.Time
		}
	}

	for _, alert := range codeScanAlerts {
		countAlertSeverity(&alerts.counts, alert.Rule.GetSeverity())
		if alert.UpdatedAt != nil && alert.UpdatedAt.After(maxAlertTime) {
			maxAlertTime = alert.UpdatedAt.Time
		}
	}

	// Secret scanning alerts don't have a severity field in the GitHub API,
	// so we default to counting them as high severity.
	for _, alert := range secretAlerts {
		alerts.counts.high++
		if alert.UpdatedAt != nil && alert.UpdatedAt.After(maxAlertTime) {
			maxAlertTime = alert.UpdatedAt.Time
		}
	}

	if !maxAlertTime.IsZero() {
		LastAlertScrapeTime.Store(repoFullName, maxAlertTime)
	}

	ctx.Logger.V(2).Infof("fetched alerts for %s: dependabot=%d, code-scan=%d, secrets=%d",
		repoFullName, len(dependabotAlerts), len(codeScanAlerts), len(secretAlerts))

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

func calculateAlertHealthStatus(alerts *allAlerts) health.HealthStatus {
	status := health.HealthStatus{Health: health.HealthHealthy, Ready: true}
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
