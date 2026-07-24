package github

import (
	"fmt"
	"sort"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/types"
	"github.com/google/go-github/v73/github"
)

// scrapeCodeSecurityConfigurations emits a config item per organization code
// security configuration, linked to the organization and to every repository in
// the scrape it is attached to.
//
// The configuration payload is used verbatim: CodeSecurityConfiguration is a
// flat struct of enablement strings with no actor, avatar or URL-template
// fields, so there is nothing to strip.
func scrapeCodeSecurityConfigurations(ctx api.ScrapeContext, scrape *organizationScrape) v1.ScrapeResults {
	var results v1.ScrapeResults
	org := scrape.name()

	configurations, _, err := scrape.client.Client.Organizations.GetCodeSecurityConfigurations(ctx, org)
	if err != nil {
		if isOrganizationFeatureUnavailable(err) {
			ctx.Logger.V(2).Infof("skipping code security configurations for %s: %v", org, err)
			return nil
		}

		results.Errorf(err, "failed to list code security configurations for GitHub organization %s", org)
		return results
	}

	for _, configuration := range configurations {
		if configuration.GetID() == 0 {
			results.Errorf(fmt.Errorf("missing id"), "invalid code security configuration %q for GitHub organization %s",
				configuration.GetName(), org)
			continue
		}

		repositories, err := codeSecurityConfigurationRepositories(ctx, scrape, configuration.GetID())
		if err != nil {
			results.Errorf(err, "failed to list repositories for code security configuration %q of GitHub organization %s",
				configuration.GetName(), org)
			continue
		}

		results = append(results, buildCodeSecurityConfigurationResult(scrape, configuration, repositories))
	}

	return results
}

func codeSecurityConfigurationRepositories(ctx api.ScrapeContext, scrape *organizationScrape, id int64) ([]string, error) {
	attached, _, err := scrape.client.Client.Organizations.GetRepositoriesForCodeSecurityConfiguration(ctx, scrape.name(), id)
	if err != nil {
		return nil, err
	}

	var names []string
	for _, repo := range attached {
		if name := repo.GetName(); scrape.hasRepository(name) {
			names = append(names, name)
		}
	}

	sort.Strings(names)
	return names, nil
}

func buildCodeSecurityConfigurationResult(
	scrape *organizationScrape,
	configuration *github.CodeSecurityConfiguration,
	repositories []string,
) v1.ScrapeResult {
	org := scrape.name()
	externalConfigID := githubCodeSecurityConfigurationExternalID(org, configuration.GetID())

	properties := []*types.Property{
		{Name: "Enforcement", Type: "badge", Text: configuration.GetEnforcement()},
		{Name: "Repositories", Type: "number", Text: fmt.Sprintf("%d", len(repositories))},
	}
	if url := configuration.GetHTMLURL(); url != "" {
		properties = append(properties, &types.Property{
			Name:  "URL",
			Type:  "url",
			Text:  url,
			Links: []types.Link{{URL: url, Type: "url"}},
		})
	}

	relationships := []v1.RelationshipResult{{
		ConfigExternalID: v1.ExternalID{
			ConfigType: ConfigTypeOrganization,
			ExternalID: githubOrganizationExternalID(org),
			ScraperID:  "all",
		},
		RelatedExternalID: v1.ExternalID{
			ConfigType: ConfigTypeCodeSecurityConfiguration,
			ExternalID: externalConfigID,
		},
		Relationship: RelationshipGitHubOrganizationCodeSecurityConfiguration,
	}}

	for _, repo := range repositories {
		relationships = append(relationships, v1.RelationshipResult{
			ConfigExternalID: v1.ExternalID{
				ConfigType: ConfigTypeCodeSecurityConfiguration,
				ExternalID: externalConfigID,
			},
			RelatedExternalID: v1.ExternalID{
				ConfigType: ConfigTypeRepository,
				ExternalID: githubRepositoryExternalID(org, repo),
			},
			Relationship: RelationshipGitHubCodeSecurityConfigurationRepository,
		})
	}

	return v1.ScrapeResult{
		BaseScraper: scrape.spec.BaseScraper,
		Type:        ConfigTypeCodeSecurityConfiguration,
		ID:          externalConfigID,
		Name:        configuration.GetName(),
		ConfigClass: "CodeSecurityConfiguration",
		Config:      configuration,
		Tags: v1.JSONStringMap{
			"owner": org,
		},
		CreatedAt:           configuration.CreatedAt.GetTime(),
		Properties:          properties,
		RelationshipResults: relationships,
	}
}
