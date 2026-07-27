package github

import (
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	gogithub "github.com/google/go-github/v73/github"
)

func doctorGitHubOrganizationApps(
	ctx api.ScrapeContext,
	client *GitHubClient,
	configName string,
	organization string,
) v1.DoctorResults {
	_, response, err := client.Organizations.ListInstallations(
		ctx,
		organization,
		&gogithub.ListOptions{PerPage: 1},
	)
	return v1.DoctorResults{githubDoctorResult(githubDoctorCheck{
		config:    configName,
		resource:  organization,
		operation: "organization app installations",
		required:  []string{"github:organization_administration=read"},
	}, client.authenticated, response, err)}
}
