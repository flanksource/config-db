package exec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/flanksource/duty/query"
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
			Setup:     config.Setup,
		}

		if config.Connections != nil {
			execConfig.Connections = *config.Connections
		}

		if len(config.Query) > 0 {
			if err := runQueries(ctx, config.Query, execConfig.BaseDir); err != nil {
				results = append(results, v1.NewScrapeResult(config.BaseScraper).Errorf("running queries: %v", err))
				continue
			}
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

		parsedResults := ParseOutput(config.BaseScraper, execDetails.Stdout)
		results = append(results, parsedResults...)
	}

	return results
}

func ParseOutput(config v1.BaseScraper, stdout string) v1.ScrapeResults {
	results := v1.ScrapeResults{}

	if stdout == "" {
		return results
	}

	// Try parsing as JSON first
	var jsonData any
	if err := json.Unmarshal([]byte(stdout), &jsonData); err == nil {
		return CreateResultsFromJSON(config, jsonData)
	}

	// Try parsing as YAML
	jsonBytes, err := yaml.YAMLToJSON([]byte(stdout))
	if err == nil {
		if err := json.Unmarshal(jsonBytes, &jsonData); err == nil {
			return CreateResultsFromJSON(config, jsonData)
		}
	}

	// If parsing fails, treat as plain text and create single result
	result := v1.NewScrapeResult(config)
	return v1.ScrapeResults{result.Success(stdout)}
}

func runQueries(ctx api.ScrapeContext, queries []v1.ConfigQuery, workDir string) error {
	for _, q := range queries {
		items, err := query.FindConfigsByResourceSelector(ctx.Context, 0, q.ResourceSelector)
		if err != nil {
			return fmt.Errorf("query for %s: %w", q.Path, err)
		}

		data, err := json.Marshal(items)
		if err != nil {
			return fmt.Errorf("marshaling results for %s: %w", q.Path, err)
		}

		outPath := filepath.Join(workDir, q.Path)
		os.MkdirAll(filepath.Dir(outPath), 0755) //nolint:errcheck
		if err := os.WriteFile(outPath, data, 0644); err != nil {
			return fmt.Errorf("writing results to %s: %w", q.Path, err)
		}

		ctx.Logger.V(2).Infof("query exported %d items to %s", len(items), outPath)
	}
	return nil
}

func CreateResultsFromJSON(config v1.BaseScraper, data any) v1.ScrapeResults {
	results := v1.ScrapeResults{}

	switch typed := data.(type) {
	case []any:
		// Array of items - create one result per item
		for _, item := range typed {
			result := v1.NewScrapeResult(config)
			jsonStr, err := json.Marshal(item)
			if err != nil {
				results = append(results, result.Errorf("failed to marshal item: %v", err))
				continue
			}
			results = append(results, result.Success(string(jsonStr)))
		}
	case map[string]any:
		// Single object - create one result
		result := v1.NewScrapeResult(config)
		jsonStr, err := json.Marshal(typed)
		if err != nil {
			results = append(results, result.Errorf("failed to marshal object: %v", err))
		} else {
			results = append(results, result.Success(string(jsonStr)))
		}
	default:
		// Scalar value - create one result
		result := v1.NewScrapeResult(config)
		jsonStr, err := json.Marshal(typed)
		if err != nil {
			results = append(results, result.Errorf("failed to marshal value: %v", err))
		} else {
			results = append(results, result.Success(string(jsonStr)))
		}
	}

	return results
}
