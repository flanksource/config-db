package exec

import (
	"encoding/json"

	"github.com/flanksource/duty/shell"
	"sigs.k8s.io/yaml"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

type ExecScraper struct{}

func (e ExecScraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.Exec) > 0
}

func (e ExecScraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	results := v1.ScrapeResults{}

	for _, config := range ctx.ScrapeConfig().Spec.Exec {
		execConfig := shell.Exec{
			Script:    config.Script,
			Checkout:  config.Checkout,
			EnvVars:   config.Env,
			Artifacts: config.Artifacts,
		}

		if config.Connections != nil {
			execConfig.Connections = *config.Connections
		}

		execDetails, err := shell.Run(ctx.DutyContext(), execConfig)
		if err != nil {
			result := v1.NewScrapeResult(config.BaseScraper)
			if execDetails != nil && execDetails.Stderr != "" {
				results = append(results, result.Errorf("failed to execute script: (%s) %v", execDetails.Stderr, err))
			} else {
				results = append(results, result.Errorf("failed to execute script: %v", err))
			}

			continue
		}

		if execDetails.ExitCode != 0 {
			result := v1.NewScrapeResult(config.BaseScraper)
			results = append(results, result.Errorf("script exited with code %d: %s", execDetails.ExitCode, execDetails.Stderr))
			continue
		}

		parsedResults := parseOutput(config, execDetails.Stdout)
		results = append(results, parsedResults...)
	}

	return results
}

func parseOutput(config v1.Exec, stdout string) v1.ScrapeResults {
	results := v1.ScrapeResults{}

	if stdout == "" {
		return results
	}

	// Try parsing as JSON first
	var jsonData any
	if err := json.Unmarshal([]byte(stdout), &jsonData); err == nil {
		return createResultsFromJSON(config, jsonData)
	}

	// Try parsing as YAML
	jsonBytes, err := yaml.YAMLToJSON([]byte(stdout))
	if err == nil {
		if err := json.Unmarshal(jsonBytes, &jsonData); err == nil {
			return createResultsFromJSON(config, jsonData)
		}
	}

	// If parsing fails, treat as plain text and create single result
	result := v1.NewScrapeResult(config.BaseScraper)
	return v1.ScrapeResults{result.Success(stdout)}
}

func createResultsFromJSON(config v1.Exec, data any) v1.ScrapeResults {
	results := v1.ScrapeResults{}

	switch typed := data.(type) {
	case []any:
		// Array of items - create one result per item
		for _, item := range typed {
			result := v1.NewScrapeResult(config.BaseScraper)
			jsonStr, err := json.Marshal(item)
			if err != nil {
				results = append(results, result.Errorf("failed to marshal item: %v", err))
				continue
			}
			results = append(results, result.Success(string(jsonStr)))
		}
	case map[string]any:
		// Single object - create one result
		result := v1.NewScrapeResult(config.BaseScraper)
		jsonStr, err := json.Marshal(typed)
		if err != nil {
			results = append(results, result.Errorf("failed to marshal object: %v", err))
		} else {
			results = append(results, result.Success(string(jsonStr)))
		}
	default:
		// Scalar value - create one result
		result := v1.NewScrapeResult(config.BaseScraper)
		jsonStr, err := json.Marshal(typed)
		if err != nil {
			results = append(results, result.Errorf("failed to marshal value: %v", err))
		} else {
			results = append(results, result.Success(string(jsonStr)))
		}
	}

	return results
}
