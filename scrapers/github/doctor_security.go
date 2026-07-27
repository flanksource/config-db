package github

import (
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	gogithub "github.com/google/go-github/v73/github"
)

func doctorGitHubRepositorySecurity(
	ctx api.ScrapeContext,
	client *GitHubClient,
	configName string,
	resource string,
	repository *gogithub.Repository,
) v1.DoctorResults {
	listOptions := gogithub.ListCursorOptions{First: 1}
	_, response, err := client.Dependabot.ListRepoAlerts(
		ctx,
		client.owner,
		client.repo,
		&gogithub.ListAlertsOptions{ListCursorOptions: listOptions},
	)
	results := v1.DoctorResults{githubDoctorResult(githubDoctorCheck{
		config:        configName,
		resource:      resource,
		operation:     "dependabot alerts",
		knownDisabled: isDependabotDoctorDisabled,
	}, client.authenticated, response, err)}

	_, response, err = client.CodeScanning.ListAlertsForRepo(
		ctx,
		client.owner,
		client.repo,
		&gogithub.AlertListOptions{ListCursorOptions: listOptions},
	)
	results = append(results, githubDoctorResult(githubDoctorCheck{
		config:        configName,
		resource:      resource,
		operation:     "code scanning alerts",
		knownDisabled: isCodeScanningDoctorDisabled,
	}, client.authenticated, response, err))

	if repository != nil && !secretScanningEnabled(repository) {
		return append(results, v1.DoctorResult{
			Scraper:       "github",
			Config:        configName,
			Resource:      resource,
			Operation:     "secret scanning alerts",
			GrantEvidence: "repository metadata",
			Status:        v1.DoctorStatusSkip,
			Message:       "secret scanning is disabled",
		})
	}

	_, response, err = client.SecretScanning.ListAlertsForRepo(
		ctx,
		client.owner,
		client.repo,
		&gogithub.SecretScanningAlertListOptions{ListCursorOptions: listOptions},
	)
	return append(results, githubDoctorResult(githubDoctorCheck{
		config:        configName,
		resource:      resource,
		operation:     "secret scanning alerts",
		knownDisabled: isSecretScanningDoctorDisabled,
	}, client.authenticated, response, err))
}

func isDependabotDoctorDisabled(err error) bool {
	return isNotFound(err) || isDependabotAlertsDisabled(err)
}

func isCodeScanningDoctorDisabled(err error) bool {
	return isNotFound(err) || isCodeScanningDisabled(err)
}

func isSecretScanningDoctorDisabled(err error) bool {
	return isNotFound(err) || isSecretScanningDisabled(err)
}
