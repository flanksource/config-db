package logs

import (
	"fmt"

	"github.com/flanksource/duty/logs"
	"github.com/flanksource/duty/logs/bigquery"
	"github.com/flanksource/duty/logs/gcpcloudlogging"
	"github.com/flanksource/duty/logs/loki"
	"github.com/flanksource/duty/logs/opensearch"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

// LogResult is a copy of logs.LogResult with modified JSON struct tags.
// The omitempty tags are removed to provide a consistent JSON structure
// for template and scripting operations.
type LogResult struct {
	Metadata map[string]any  `json:"metadata"`
	Logs     []*logs.LogLine `json:"logs"`
}

type LogsScraper struct{}

func (s LogsScraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.Logs) > 0
}

func (s LogsScraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults

	for _, config := range ctx.ScrapeConfig().Spec.Logs {
		if config.Loki != nil {
			lokiResults, err := s.scrapeLoki(ctx, config)
			if err != nil {
				results = append(results, v1.NewScrapeResult(config.BaseScraper).
					SetError(fmt.Errorf("failed to scrape loki logs: %w", err)))
			} else {
				results = append(results, lokiResults...)
			}
		}

		if config.GCPCloudLogging != nil {
			gcpResults, err := s.scrapeGCPCloudLogging(ctx, config)
			if err != nil {
				results = append(results, v1.NewScrapeResult(config.BaseScraper).
					SetError(fmt.Errorf("failed to scrape GCP cloud logging: %w", err)))
			} else {
				results = append(results, gcpResults...)
			}
		}

		if config.OpenSearch != nil {
			osResults, err := s.scrapeOpenSearch(ctx, config)
			if err != nil {
				results = append(results, v1.NewScrapeResult(config.BaseScraper).
					SetError(fmt.Errorf("failed to scrape OpenSearch logs: %w", err)))
			} else {
				results = append(results, osResults...)
			}
		}

		if config.BigQuery != nil {
			bqResults, err := s.scrapeBigQuery(ctx, config)
			if err != nil {
				results = append(results, v1.NewScrapeResult(config.BaseScraper).
					SetError(fmt.Errorf("failed to scrape BigQuery logs: %w", err)))
			} else {
				results = append(results, bqResults...)
			}
		}
	}

	return results
}

func (s LogsScraper) scrapeLoki(ctx api.ScrapeContext, config v1.Logs) (v1.ScrapeResults, error) {
	if config.Loki == nil {
		return nil, nil
	}

	lokiClient := loki.New(config.Loki.Loki, config.FieldMapping)
	response, err := lokiClient.Search(ctx.DutyContext(), config.Loki.Request)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch logs from loki: %w", err)
	}

	var results v1.ScrapeResults
	results = append(results, v1.ScrapeResult{
		BaseScraper: config.BaseScraper,
		Config:      LogResult(*response),
	})

	return results, nil
}

func (s LogsScraper) scrapeGCPCloudLogging(ctx api.ScrapeContext, config v1.Logs) (v1.ScrapeResults, error) {
	if config.GCPCloudLogging == nil {
		return nil, nil
	}

	client, err := gcpcloudlogging.New(ctx.DutyContext(), config.GCPCloudLogging.GCPConnection, config.FieldMapping)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCP cloud logging client: %w", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			ctx.Errorf("failed to close GCP cloud logging client: %v", err)
		}
	}()

	response, err := client.Search(ctx.DutyContext(), config.GCPCloudLogging.Request)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch logs from GCP cloud logging: %w", err)
	}

	var results v1.ScrapeResults
	results = append(results, v1.ScrapeResult{
		BaseScraper: config.BaseScraper,
		Config:      LogResult(*response),
	})

	return results, nil
}

func (s LogsScraper) scrapeOpenSearch(ctx api.ScrapeContext, config v1.Logs) (v1.ScrapeResults, error) {
	if config.OpenSearch == nil {
		return nil, nil
	}

	client, err := opensearch.New(ctx.DutyContext(), config.OpenSearch.Backend, config.FieldMapping)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenSearch client: %w", err)
	}

	response, err := client.Search(ctx.DutyContext(), config.OpenSearch.Request)
	if err != nil {
		return nil, fmt.Errorf("failed to search logs in OpenSearch: %w", err)
	}

	var results v1.ScrapeResults
	results = append(results, v1.ScrapeResult{
		BaseScraper: config.BaseScraper,
		Config:      LogResult(*response),
	})

	return results, nil
}

func (s LogsScraper) scrapeBigQuery(ctx api.ScrapeContext, config v1.Logs) (v1.ScrapeResults, error) {
	if config.BigQuery == nil {
		return nil, nil
	}

	searcher := bigquery.New(config.BigQuery.GCPConnection, config.FieldMapping)
	defer func() {
		if err := searcher.Close(); err != nil {
			ctx.Errorf("failed to close BigQuery searcher: %v", err)
		}
	}()

	response, err := searcher.Search(ctx.DutyContext(), config.BigQuery.Request)
	if err != nil {
		return nil, fmt.Errorf("failed to search logs in BigQuery: %w", err)
	}

	var results v1.ScrapeResults
	results = append(results, v1.ScrapeResult{
		BaseScraper: config.BaseScraper,
		Config:      LogResult(*response),
	})

	return results, nil
}
