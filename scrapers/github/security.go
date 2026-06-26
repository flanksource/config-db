package github

import (
	"errors"
	"fmt"

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

func scrapeSecurityAlerts(ctx api.ScrapeContext, client *GitHubClient, config v1.GitHub, repoFullName string, securityFeatures []securityFeatureStatus) (*allAlerts, error) {
	alerts := &allAlerts{}

	filters := config.SecurityFilters
	stateFilter := "open"
	if len(filters.State) > 0 {
		stateFilter = filters.State[0]
	}

	opts := AlertListOptions{
		State:   stateFilter,
		PerPage: 100,
	}

	var errs []error

	if !isSecurityFeatureDisabled(securityFeatures, "dependabot-alerts") {
		dependabotAlerts, err := client.GetDependabotAlerts(ctx, opts)
		if err != nil {
			errs = append(errs, fmt.Errorf("dependabot: %w", err))
		} else {
			alerts.dependabot = dependabotAlerts
		}
	} else {
		ctx.Debugf("skipping Dependabot alerts for %s: Dependabot alerts disabled", repoFullName)
	}

	if !isSecurityFeatureDisabled(securityFeatures, "code-scanning") {
		codeScanAlerts, err := client.GetCodeScanningAlerts(ctx, opts)
		if err != nil {
			errs = append(errs, fmt.Errorf("code scanning: %w", err))
		} else {
			alerts.codeScanning = codeScanAlerts
		}
	} else {
		ctx.Debugf("skipping code scanning alerts for %s: code scanning disabled", repoFullName)
	}

	if !isSecurityFeatureDisabled(securityFeatures, "secret-scanning") {
		secretAlerts, err := client.GetSecretScanningAlerts(ctx, opts)
		if err != nil {
			errs = append(errs, fmt.Errorf("secret scanning: %w", err))
		} else {
			alerts.secretScanning = secretAlerts
		}
	} else {
		ctx.Debugf("skipping secret scanning alerts for %s: secret scanning disabled", repoFullName)
	}

	for _, alert := range alerts.dependabot {
		countAlertSeverity(&alerts.counts, alert.SecurityAdvisory.GetSeverity())
	}

	for _, alert := range alerts.codeScanning {
		countAlertSeverity(&alerts.counts, alert.Rule.GetSeverity())
	}

	// Secret scanning alerts don't have a severity field in the GitHub API,
	// so we default to counting them as high severity.
	for range alerts.secretScanning {
		alerts.counts.high++
	}

	ctx.Logger.V(2).Infof("fetched alerts for %s: dependabot=%d, code-scan=%d, secrets=%d",
		repoFullName, len(alerts.dependabot), len(alerts.codeScanning), len(alerts.secretScanning))

	return alerts, errors.Join(errs...)
}

func isSecurityFeatureDisabled(features []securityFeatureStatus, key string) bool {
	for _, feature := range features {
		if feature.Key == key {
			return !feature.Enabled
		}
	}
	return false
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
