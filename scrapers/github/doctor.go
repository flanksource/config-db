package github

import (
	"fmt"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	gogithub "github.com/google/go-github/v73/github"
)

func (gh GithubScraper) Doctor(ctx api.ScrapeContext) (v1.DoctorResults, error) {
	if ctx.ScrapeConfig() == nil {
		return nil, fmt.Errorf("github doctor requires a scrape config")
	}

	var results v1.DoctorResults
	for index, config := range ctx.ScrapeConfig().Spec.GitHub {
		configName := config.Name
		if configName == "" {
			configName = fmt.Sprintf("github[%d]", index)
		}

		repositories, resolutionResults := resolveDoctorRepositories(ctx, config, configName)
		results = append(results, resolutionResults...)
		for _, repository := range repositories {
			results = append(results, doctorGitHubRepository(ctx, config, configName, repository)...)
		}
		for _, organization := range config.Organizations {
			results = append(results, doctorGitHubOrganization(ctx, config, configName, organization)...)
		}
	}

	return results, nil
}

func resolveDoctorRepositories(
	ctx api.ScrapeContext,
	config v1.GitHub,
	configName string,
) ([]v1.GitHubRepository, v1.DoctorResults) {
	var scrapeResults v1.ScrapeResults
	repositories, rateLimited := resolveRepositoryConfigs(ctx, config, &scrapeResults)
	var results v1.DoctorResults

	for _, scrapeResult := range scrapeResults {
		if scrapeResult.Error == nil {
			continue
		}
		results = append(results, v1.DoctorResult{
			Scraper:       "github",
			Config:        configName,
			Resource:      "repository selectors",
			Operation:     "resolve repositories",
			GrantEvidence: "request denied",
			Status:        v1.DoctorStatusFail,
			Message:       scrapeResult.Error.Error(),
		})
	}

	if rateLimited {
		results = append(results, v1.DoctorResult{
			Scraper:       "github",
			Config:        configName,
			Resource:      "repository selectors",
			Operation:     "resolve repositories",
			GrantEvidence: "request denied",
			Status:        v1.DoctorStatusFail,
			Message:       "GitHub API rate limit reached",
		})
	}

	if hasRepositorySelector(config.Repositories) && !rateLimited && !scrapeResults.HasErr() {
		results = append(results, v1.DoctorResult{
			Scraper:       "github",
			Config:        configName,
			Resource:      "repository selectors",
			Operation:     "resolve repositories",
			GrantEvidence: "repository listing succeeded",
			Status:        v1.DoctorStatusPass,
			Message:       fmt.Sprintf("resolved %d repositories", len(repositories)),
		})
	}

	return repositories, results
}

func hasRepositorySelector(repositories []v1.GitHubRepository) bool {
	for _, repository := range repositories {
		if isRepositorySelector(repository.Repo) {
			return true
		}
	}
	return false
}

func doctorGitHubRepository(
	ctx api.ScrapeContext,
	config v1.GitHub,
	configName string,
	repository v1.GitHubRepository,
) v1.DoctorResults {
	resource := repository.Owner + "/" + repository.Repo
	client, err := NewGitHubClient(ctx, config, repository.Owner, repository.Repo)
	if err != nil {
		return v1.DoctorResults{{
			Scraper:       "github",
			Config:        configName,
			Resource:      resource,
			Operation:     "create client",
			GrantEvidence: "credentials unavailable",
			Status:        v1.DoctorStatusFail,
			Message:       err.Error(),
		}}
	}

	repo, response, err := client.Repositories.Get(ctx, repository.Owner, repository.Repo)
	results := v1.DoctorResults{githubDoctorResult(githubDoctorCheck{
		config:    configName,
		resource:  resource,
		operation: "repository metadata",
	}, client.authenticated, response, err)}

	if config.Security {
		results = append(results, doctorGitHubRepositorySecurity(ctx, client, configName, resource, repo)...)
	}
	if config.Permissions != nil && config.Permissions.Enabled {
		results = append(results, doctorGitHubRepositoryPermissions(ctx, client, configName, resource)...)
	}

	return results
}

func doctorGitHubRepositoryPermissions(
	ctx api.ScrapeContext,
	client *GitHubClient,
	configName string,
	resource string,
) v1.DoctorResults {
	_, response, err := client.Repositories.ListCollaborators(
		ctx,
		client.owner,
		client.repo,
		&gogithub.ListCollaboratorsOptions{
			Affiliation: "all",
			ListOptions: gogithub.ListOptions{PerPage: 1},
		},
	)
	results := v1.DoctorResults{githubDoctorResult(githubDoctorCheck{
		config:    configName,
		resource:  resource,
		operation: "repository collaborators",
	}, client.authenticated, response, err)}

	_, response, err = client.Repositories.ListTeams(
		ctx,
		client.owner,
		client.repo,
		&gogithub.ListOptions{PerPage: 1},
	)
	return append(results, githubDoctorResult(githubDoctorCheck{
		config:    configName,
		resource:  resource,
		operation: "repository teams",
	}, client.authenticated, response, err))
}
