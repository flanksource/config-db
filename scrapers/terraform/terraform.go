package terraform

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/flanksource/artifacts"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

const ConfigType = "Terraform::Module"

type Scraper struct {
}

func (t Scraper) CanScrape(spec v1.ScraperSpec) bool {
	return len(spec.Terraform) > 0
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
	conn, err := stateSource.Connection(ctx.DutyContext())
	if err != nil {
		return nil, fmt.Errorf("error getting connection from state source: %v", err)
	}

	fs, err := artifacts.GetFSForConnection(ctx.DutyContext(), *conn)
	if err != nil {
		return nil, fmt.Errorf("error getting fs: %v", err)
	}

	files, err := fs.ReadDir(stateSource.Path())
	if err != nil {
		return nil, err
	}

	var states []StateFile
	for _, fInfo := range files {
		reader, err := fs.Read(ctx, fInfo.FullPath())
		if err != nil {
			return nil, err
		}

		content, err := io.ReadAll(reader)
		if err != nil {
			return nil, err
		}

		states = append(states, StateFile{
			Data: content,
			Path: fInfo.Name(),
		})
	}

	return states, nil
}

func awsProvider(externalID string, resource Resource) []v1.RelationshipResult {
	var results []v1.RelationshipResult

	arnKeys := []string{
		"arn", "policy_arn", "function_arn", "role_arn", "kms_key_arn",
		"bucket_arn", "topic_arn", "queue_arn", "lambda_arn", "cluster_arn",
		"instance_arn", "execution_arn", "stream_arn",
	}
	for _, instance := range resource.Instances {
		var arn string
		for _, key := range arnKeys {
			_arn, ok := instance.Attributes[key]
			if ok {
				arn = _arn.(string)
				break
			}
		}

		if arn == "" {
			logger.Debugf("skipping instance as it does not have an arn: %v", instance)
			continue
		}

		results = append(results, v1.RelationshipResult{
			ConfigExternalID:  v1.ExternalID{ConfigType: ConfigType, ExternalID: []string{externalID}},
			RelatedExternalID: v1.ExternalID{ExternalID: []string{arn}, ScraperID: "all"},
		})
	}

	return results
}
