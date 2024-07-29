package terraform

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/flanksource/commons/files"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

const ConfigType = "Terraform::StateFile"

type Scraper struct {
}

func (t Scraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.Terraform) > 0
}

func (t Scraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults

	for _, config := range ctx.ScrapeConfig().Spec.Terraform {
		states, err := loadStateFiles(ctx, config.State)
		if err != nil {
			results = append(results, v1.ScrapeResult{Error: err})
			continue
		}

		for _, state := range states {
			name, err := config.Name.Run(map[string]any{"path": state.Path})
			if err != nil {
				results = append(results, v1.ScrapeResult{Error: err})
				continue
			}

			results = append(results, getConfigFromState(config, name, state))
		}
	}

	return results
}

func getConfigFromState(config v1.Terraform, name string, _state StateFile) v1.ScrapeResult {
	var state State
	if err := json.Unmarshal(_state.Data, &state); err != nil {
		return v1.ScrapeResult{Error: err}
	}

	newConfig := v1.ScrapeResult{
		ID:          state.Lineage,
		ConfigID:    &state.Lineage,
		BaseScraper: config.BaseScraper,
		Name:        name,
		Type:        ConfigType,
		ConfigClass: ConfigType,
		Aliases:     []string{state.Lineage},
		Config:      _state.Data,
	}

	for _, resource := range state.Resources {
		if resource.Mode != "managed" {
			continue
		}

		switch resource.Provider {
		case `provider["registry.terraform.io/hashicorp/aws"]`:
			newConfig.RelationshipResults = append(newConfig.RelationshipResults, awsProvider(state.Lineage, resource)...)
		}
	}

	return newConfig
}

type StateFile struct {
	Data []byte
	Path string
}

func loadStateFiles(ctx api.ScrapeContext, stateSource v1.TerraformStateSource) ([]StateFile, error) {
	var states []StateFile
	if stateSource.Local != "" {
		paths, err := files.UnfoldGlobs(stateSource.Local)
		if err != nil {
			return nil, fmt.Errorf("error processing local path: %s %s", stateSource.Local, err)
		}

		for _, path := range paths {
			content, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}

			states = append(states, StateFile{
				Data: content,
				Path: path,
			})
		}
	}

	return states, nil
}

func awsProvider(externalID string, resource Resource) []v1.RelationshipResult {
	var results []v1.RelationshipResult

	for _, instance := range resource.Instances {
		switch resource.Type {
		case "aws_iam_role":
			results = append(results, v1.RelationshipResult{
				ConfigExternalID:  v1.ExternalID{ConfigType: ConfigType, ExternalID: []string{externalID}},
				RelatedExternalID: v1.ExternalID{ConfigType: v1.AWSIAMRole, ExternalID: []string{instance.Attributes["arn"].(string)}, ScraperID: "all"},
			})

		case "aws_lambda_function":
			results = append(results, v1.RelationshipResult{
				ConfigExternalID:  v1.ExternalID{ConfigType: ConfigType, ExternalID: []string{externalID}},
				RelatedExternalID: v1.ExternalID{ConfigType: v1.AWSLambdaFunction, ExternalID: []string{instance.Attributes["arn"].(string)}, ScraperID: "all"},
			})
		}
	}

	return results
}
