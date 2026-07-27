package github

import (
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	gogithub "github.com/google/go-github/v73/github"
)

func doctorGitHubOrganizationSettings(
	ctx api.ScrapeContext,
	client *GitHubClient,
	configName string,
	organization string,
) v1.DoctorResults {
	actions, response, actionsErr := client.Actions.GetActionsPermissions(ctx, organization)
	results := v1.DoctorResults{githubDoctorResult(githubDoctorCheck{
		config:    configName,
		resource:  organization,
		operation: "organization actions permissions",
	}, client.authenticated, response, actionsErr)}

	if actionsErr == nil && actions.GetAllowedActions() == "selected" {
		results = append(results, runGitHubDoctorProbe(githubDoctorCheck{
			config:    configName,
			resource:  organization,
			operation: "organization allowed actions",
		}, client.authenticated, func() (*gogithub.Response, error) {
			_, response, err := client.Actions.GetActionsAllowed(ctx, organization)
			return response, err
		}))
	}

	results = append(results, doctorGitHubOrganizationRoles(ctx, client, configName, organization)...)
	return append(results, doctorGitHubCodeSecurityConfigurations(ctx, client, configName, organization)...)
}

func doctorGitHubOrganizationRulesets(
	ctx api.ScrapeContext,
	client *GitHubClient,
	configName string,
	organization string,
) v1.DoctorResult {
	return runGitHubDoctorProbe(githubDoctorCheck{
		config:    configName,
		resource:  organization,
		operation: "organization repository rulesets",
		required:  []string{"github:organization_administration=write"},
	}, client.authenticated, func() (*gogithub.Response, error) {
		_, response, err := client.Organizations.GetAllRepositoryRulesets(
			ctx,
			organization,
			&gogithub.ListOptions{PerPage: 1},
		)
		return response, err
	})
}

func doctorGitHubOrganizationRoles(
	ctx api.ScrapeContext,
	client *GitHubClient,
	configName string,
	organization string,
) v1.DoctorResults {
	roles, response, err := client.Organizations.ListRoles(ctx, organization)
	results := v1.DoctorResults{githubDoctorResult(githubDoctorCheck{
		config:    configName,
		resource:  organization,
		operation: "organization roles",
	}, client.authenticated, response, err)}

	if err != nil {
		return results
	}
	if roles == nil || len(roles.CustomRepoRoles) == 0 {
		return append(results,
			skippedGitHubDoctorResult(configName, organization, "organization role teams", "no organization roles"),
			skippedGitHubDoctorResult(configName, organization, "organization role users", "no organization roles"),
		)
	}

	roleID := roles.CustomRepoRoles[0].GetID()
	for _, assignment := range []struct {
		operation string
		probe     githubDoctorProbe
	}{
		{
			operation: "organization role teams",
			probe: func() (*gogithub.Response, error) {
				_, response, err := client.Organizations.ListTeamsAssignedToOrgRole(
					ctx, organization, roleID, &gogithub.ListOptions{PerPage: 1},
				)
				return response, err
			},
		},
		{
			operation: "organization role users",
			probe: func() (*gogithub.Response, error) {
				_, response, err := client.Organizations.ListUsersAssignedToOrgRole(
					ctx, organization, roleID, &gogithub.ListOptions{PerPage: 1},
				)
				return response, err
			},
		},
	} {
		results = append(results, runGitHubDoctorProbe(githubDoctorCheck{
			config:    configName,
			resource:  organization,
			operation: assignment.operation,
		}, client.authenticated, assignment.probe))
	}

	return results
}

func doctorGitHubCodeSecurityConfigurations(
	ctx api.ScrapeContext,
	client *GitHubClient,
	configName string,
	organization string,
) v1.DoctorResults {
	configurations, response, err := client.Organizations.GetCodeSecurityConfigurations(ctx, organization)
	results := v1.DoctorResults{githubDoctorResult(githubDoctorCheck{
		config:    configName,
		resource:  organization,
		operation: "organization code security configurations",
	}, client.authenticated, response, err)}

	if err != nil {
		return results
	}
	if len(configurations) == 0 {
		return append(results, skippedGitHubDoctorResult(
			configName,
			organization,
			"code security configuration repositories",
			"no code security configurations",
		))
	}

	return append(results, runGitHubDoctorProbe(githubDoctorCheck{
		config:    configName,
		resource:  organization,
		operation: "code security configuration repositories",
	}, client.authenticated, func() (*gogithub.Response, error) {
		_, response, err := client.Organizations.GetRepositoriesForCodeSecurityConfiguration(
			ctx,
			organization,
			configurations[0].GetID(),
		)
		return response, err
	}))
}
