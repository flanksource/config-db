package github

import (
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	gogithub "github.com/google/go-github/v73/github"
)

func doctorGitHubOrganizationMembers(
	ctx api.ScrapeContext,
	client *GitHubClient,
	configName string,
	organization string,
) v1.DoctorResults {
	var results v1.DoctorResults
	for _, role := range []string{"admin", "member"} {
		role := role
		results = append(results, runGitHubDoctorProbe(githubDoctorCheck{
			config:    configName,
			resource:  organization,
			operation: "organization " + role + " members",
		}, client.authenticated, func() (*gogithub.Response, error) {
			_, response, err := client.Organizations.ListMembers(
				ctx,
				organization,
				&gogithub.ListMembersOptions{
					Role:        role,
					ListOptions: gogithub.ListOptions{PerPage: 1},
				},
			)
			return response, err
		}))
	}

	teams, response, err := client.Teams.ListTeams(
		ctx,
		organization,
		&gogithub.ListOptions{PerPage: 1},
	)
	results = append(results, githubDoctorResult(githubDoctorCheck{
		config:    configName,
		resource:  organization,
		operation: "organization teams",
	}, client.authenticated, response, err))

	if err != nil {
		return results
	}
	if len(teams) == 0 {
		return append(results,
			skippedGitHubDoctorResult(configName, organization, "team members", "no organization teams"),
			skippedGitHubDoctorResult(configName, organization, "team repositories", "no organization teams"),
		)
	}

	return append(results, doctorGitHubTeam(ctx, client, configName, organization, teams[0].GetSlug())...)
}

func doctorGitHubTeam(
	ctx api.ScrapeContext,
	client *GitHubClient,
	configName string,
	organization string,
	slug string,
) v1.DoctorResults {
	memberResult := runGitHubDoctorProbe(githubDoctorCheck{
		config:    configName,
		resource:  organization + "/" + slug,
		operation: "team members",
	}, client.authenticated, func() (*gogithub.Response, error) {
		_, response, err := client.Teams.ListTeamMembersBySlug(
			ctx,
			organization,
			slug,
			&gogithub.TeamListTeamMembersOptions{
				Role:        "all",
				ListOptions: gogithub.ListOptions{PerPage: 1},
			},
		)
		return response, err
	})

	repositoryResult := runGitHubDoctorProbe(githubDoctorCheck{
		config:    configName,
		resource:  organization + "/" + slug,
		operation: "team repositories",
	}, client.authenticated, func() (*gogithub.Response, error) {
		_, response, err := client.Teams.ListTeamReposBySlug(
			ctx,
			organization,
			slug,
			&gogithub.ListOptions{PerPage: 1},
		)
		return response, err
	})

	return v1.DoctorResults{memberResult, repositoryResult}
}
