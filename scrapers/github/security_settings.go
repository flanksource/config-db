package github

import (
	"strings"

	"github.com/flanksource/config-db/api"
	"github.com/google/go-github/v73/github"
)

// securityFeatureStatus records whether a GitHub security feature is enabled
// for a repository. It is used to skip alert scraping for features that are
// known to be disabled, avoiding unnecessary API calls and error handling.
type securityFeatureStatus struct {
	Key     string
	Enabled bool
}

// scrapeSecurityFeatureStatuses detects the enablement of the security
// features whose alerts are scraped by scrapeSecurityAlerts. Features whose
// status cannot be determined (e.g. the token lacks the required scope) are
// left absent so that alert scraping falls back to fetching and tolerating
// 404/403 responses.
func scrapeSecurityFeatureStatuses(ctx api.ScrapeContext, client *GitHubClient, repo *github.Repository, repoFullName string) []securityFeatureStatus {
	var features []securityFeatureStatus

	if enabled, err := client.GetDependabotAlertsEnabled(ctx); err != nil {
		ctx.Debugf("failed to detect Dependabot alerts status for %s: %v", repoFullName, err)
	} else {
		features = append(features, securityFeatureStatus{
			Key:     "dependabot-alerts",
			Enabled: enabled,
		})
	}

	// Secret scanning enablement is only exposed via security_and_analysis,
	// which requires admin access on the repository. When it is unavailable
	// (non-admin token) we leave it absent and let GetSecretScanningAlerts
	// handle the disabled case via 404/403.
	if status := repo.GetSecurityAndAnalysis().GetSecretScanning().GetStatus(); status != "" {
		features = append(features, securityFeatureStatus{
			Key:     "secret-scanning",
			Enabled: strings.EqualFold(status, "enabled"),
		})
	}

	if configured, err := client.IsCodeScanningConfigured(ctx); err != nil {
		ctx.Debugf("failed to detect code scanning status for %s: %v", repoFullName, err)
	} else {
		features = append(features, securityFeatureStatus{
			Key:     "code-scanning",
			Enabled: configured,
		})
	}

	return features
}
