package devops

import (
	"fmt"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/types"
)

const RepositoryType = "AzureDevops::Repository"

func repositoryExternalID(organization, project, repoID string) string {
	return fmt.Sprintf("azuredevops://%s/%s/repository/%s", organization, project, repoID)
}

func (ado AzureDevopsScraper) scrapeRepositories(
	ctx api.ScrapeContext,
	client *AzureDevopsClient,
	config v1.AzureDevops,
	project Project,
) v1.ScrapeResults {
	repos, err := client.GetRepositories(ctx, project.Name)
	if err != nil {
		var results v1.ScrapeResults
		results.Errorf(err, "failed to get repositories for %s", project.Name)
		return results
	}

	ctx.Logger.V(3).Infof("[%s] found %d repositories", project.Name, len(repos))

	var results v1.ScrapeResults
	for _, repo := range repos {
		if !collections.MatchItems(repo.Name, config.Repositories...) {
			continue
		}

		id := repositoryExternalID(config.Organization, project.Name, repo.ID)

		configData := map[string]any{
			"id":            repo.ID,
			"name":          repo.Name,
			"defaultBranch": repo.DefaultBranch,
			"remoteUrl":     repo.RemoteURL,
			"sshUrl":        repo.SSHURL,
			"size":          repo.Size,
			"isDisabled":    repo.IsDisabled,
			"isFork":        repo.IsFork,
			"project":       project.Name,
			"organization":  config.Organization,
		}

		var properties types.Properties
		if repo.WebURL != "" {
			properties = append(properties, &types.Property{
				Name:  "Source",
				Links: []types.Link{{Type: "source", URL: repo.WebURL}},
			})
		}

		results = append(results, v1.ScrapeResult{
			BaseScraper: config.BaseScraper,
			ConfigClass: "Repository",
			Config:      configData,
			Type:        RepositoryType,
			ID:          id,
			Name:        repo.Name,
			Properties:  properties,
		})
	}

	return results
}
