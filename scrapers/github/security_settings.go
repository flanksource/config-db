package github

import (
	"fmt"
	"strings"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/google/go-github/v73/github"
)

const securitySettingsSource = "config-db/github"

type securityFeatureStatus struct {
	Key       string
	Name      string
	Status    string
	Enabled   bool
	Severity  models.Severity
	Message   string
	Remediate string
}

func scrapeSecurityFeatureStatuses(ctx api.ScrapeContext, client *GitHubClient, repo *github.Repository, repoFullName string) []securityFeatureStatus {
	var features []securityFeatureStatus

	if enabled, err := client.GetDependabotAlertsEnabled(ctx); err != nil {
		ctx.Debugf("failed to detect Dependabot alerts status for %s: %v", repoFullName, err)
	} else {
		features = append(features, securityFeatureStatus{
			Key:       "dependabot-alerts",
			Name:      "Dependabot alerts",
			Status:    statusFromEnabled(enabled),
			Enabled:   enabled,
			Severity:  models.SeverityHigh,
			Message:   "Dependabot alerts detect vulnerable dependencies and GitHub Security Advisories for repository manifests.",
			Remediate: "Enable Dependabot alerts and the dependency graph for this repository.",
		})
	}

	securityAndAnalysis := repo.GetSecurityAndAnalysis()
	features = appendSecurityAndAnalysisStatus(features, securityFeatureStatus{
		Key:       "advanced-security",
		Name:      "GitHub Advanced Security",
		Severity:  models.SeverityMedium,
		Message:   "GitHub Advanced Security unlocks advanced code security features for the repository.",
		Remediate: "Enable GitHub Advanced Security / Code Security for this repository when available.",
	}, statusPtr(securityAndAnalysis.GetAdvancedSecurity()))
	features = appendSecurityAndAnalysisStatus(features, securityFeatureStatus{
		Key:       "dependabot-security-updates",
		Name:      "Dependabot security updates",
		Severity:  models.SeverityMedium,
		Message:   "Dependabot security updates open pull requests to upgrade vulnerable dependencies.",
		Remediate: "Enable Dependabot security updates for this repository.",
	}, statusPtr(securityAndAnalysis.GetDependabotSecurityUpdates()))
	features = appendSecurityAndAnalysisStatus(features, securityFeatureStatus{
		Key:       "secret-scanning",
		Name:      "Secret scanning",
		Severity:  models.SeverityHigh,
		Message:   "Secret scanning detects exposed tokens, API keys, and credentials committed to the repository.",
		Remediate: "Enable secret scanning for this repository.",
	}, statusPtr(securityAndAnalysis.GetSecretScanning()))
	features = appendSecurityAndAnalysisStatus(features, securityFeatureStatus{
		Key:       "secret-scanning-push-protection",
		Name:      "Secret scanning push protection",
		Severity:  models.SeverityHigh,
		Message:   "Push protection blocks supported secrets before they are pushed to the repository.",
		Remediate: "Enable secret scanning push protection for this repository.",
	}, statusPtr(securityAndAnalysis.GetSecretScanningPushProtection()))

	if configured, err := client.IsCodeScanningConfigured(ctx); err != nil {
		ctx.Debugf("failed to detect code scanning status for %s: %v", repoFullName, err)
	} else {
		features = append(features, securityFeatureStatus{
			Key:       "code-scanning",
			Name:      "Code scanning",
			Status:    statusFromEnabled(configured),
			Enabled:   configured,
			Severity:  models.SeverityMedium,
			Message:   "Code scanning surfaces SAST findings from CodeQL and SARIF-uploading tools.",
			Remediate: "Configure CodeQL or upload SARIF from a code scanning tool for this repository.",
		})
	}

	return features
}

func createSecurityFeatureStatusAnalyses(results *v1.ScrapeResults, externalConfigID string, features []securityFeatureStatus) {
	for _, feature := range features {
		a := results.Analysis(
			fmt.Sprintf("security-settings/%s", feature.Key),
			ConfigTypeRepository,
			externalConfigID,
		)
		a.ExternalAnalysisID = fmt.Sprintf("%s::security-settings/%s", v1.NormalizeExternalID(externalConfigID), feature.Key)
		a.AnalysisType = models.AnalysisTypeSecurity
		a.Source = securitySettingsSource
		a.Analyzer = "github-security-settings"
		a.Severity = feature.Severity
		a.Status = models.AnalysisStatusOpen
		a.Summary = fmt.Sprintf("%s is disabled", feature.Name)
		if feature.Enabled {
			a.Status = models.AnalysisStatusResolved
			a.Summary = fmt.Sprintf("%s is enabled", feature.Name)
		}
		a.Message(feature.Message)
		if !feature.Enabled {
			a.Message(feature.Remediate)
		}
		a.Analysis = map[string]any{
			"provider": "github",
			"signal":   "security_settings",
			"feature":  feature.Key,
			"status":   feature.Status,
			"enabled":  feature.Enabled,
		}
		a.Properties = append(a.Properties,
			&types.Property{Name: "Provider", Type: "badge", Text: "GitHub"},
			&types.Property{Name: "Feature", Type: "badge", Text: feature.Name},
			&types.Property{Name: "Status", Type: "badge", Text: feature.Status},
		)
	}
}

func appendSecurityAndAnalysisStatus(features []securityFeatureStatus, feature securityFeatureStatus, status *string) []securityFeatureStatus {
	if status == nil || *status == "" {
		return features
	}

	feature.Status = strings.ToLower(*status)
	feature.Enabled = feature.Status == "enabled"
	return append(features, feature)
}

func statusFromEnabled(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func statusPtr(status interface{ GetStatus() string }) *string {
	if status == nil {
		return nil
	}
	value := status.GetStatus()
	if value == "" {
		return nil
	}
	return &value
}
