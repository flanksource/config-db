package github

import (
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	gogithub "github.com/google/go-github/v73/github"
)

func doctorGitHubOrganization(
	ctx api.ScrapeContext,
	config v1.GitHub,
	configName string,
	organization v1.GitHubOrganization,
) v1.DoctorResults {
	client, err := NewGitHubClient(ctx, config, organization.Name, "")
	if err != nil {
		return v1.DoctorResults{{
			Scraper:       "github",
			Config:        configName,
			Resource:      organization.Name,
			Operation:     "create client",
			GrantEvidence: "credentials unavailable",
			Status:        v1.DoctorStatusFail,
			Message:       err.Error(),
		}}
	}

	results := v1.DoctorResults{runGitHubDoctorProbe(githubDoctorCheck{
		config:    configName,
		resource:  organization.Name,
		operation: "organization metadata",
	}, client.authenticated, func() (*gogithub.Response, error) {
		_, response, err := client.Organizations.Get(ctx, organization.Name)
		return response, err
	})}

	if organization.Settings {
		results = append(results, doctorGitHubOrganizationSettings(ctx, client, configName, organization.Name)...)
	}
	if organization.Rulesets {
		results = append(results, doctorGitHubOrganizationRulesets(ctx, client, configName, organization.Name))
	}
	if organization.Apps {
		results = append(results, doctorGitHubOrganizationApps(ctx, client, configName, organization.Name)...)
	}
	if organization.Members {
		results = append(results, doctorGitHubOrganizationMembers(ctx, client, configName, organization.Name)...)
	}

	return results
}

func skippedGitHubDoctorResult(configName, resource, operation, message string) v1.DoctorResult {
	return v1.DoctorResult{
		Scraper:       "github",
		Config:        configName,
		Resource:      resource,
		Operation:     operation,
		GrantEvidence: "no representative resource",
		Status:        v1.DoctorStatusSkip,
		Message:       message,
	}
}
