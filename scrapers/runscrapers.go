package scrapers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers/analysis"
	"github.com/flanksource/config-db/scrapers/changes"
	"github.com/flanksource/config-db/scrapers/processors"
	"github.com/flanksource/duty/models"
)

// Run ...
func Run(ctx *v1.ScrapeContext, configs ...v1.ConfigScraper) ([]v1.ScrapeResult, error) {
	cwd, _ := os.Getwd()
	logger.Infof("Scraping files from (PWD: %s)", cwd)

	results := []v1.ScrapeResult{}
	for _, config := range configs {
		for _, scraper := range All {
			jobHistory := models.JobHistory{
				Name: fmt.Sprintf("scraper:%T", scraper),
			}
			jobHistory.Start()

			if err := db.PersistJobHistory(&jobHistory); err != nil {
				logger.Errorf("Error persisting job history: %v", err)
			}

			for _, result := range scraper.Scrape(ctx, config) {
				if result.AnalysisResult != nil {
					if rule, ok := analysis.Rules[result.AnalysisResult.Analyzer]; ok {
						result.AnalysisResult.AnalysisType = rule.Category
						result.AnalysisResult.Severity = rule.Severity
					}
				}

				result.Changes = changes.ProcessRules(result)

				if result.Config == nil && (result.AnalysisResult != nil || len(result.Changes) > 0) {
					results = append(results, result)
				} else if result.Config != nil {
					extractor, err := processors.NewExtractor(result.BaseScraper)
					if err != nil {
						logger.Errorf("failed to create extractor: %v", err)
						jobHistory.AddError(err.Error())
						continue
					}

					scraped, err := extractor.Extract(result)
					if err != nil {
						logger.Errorf("failed to extract: %v", err)
						jobHistory.AddError(err.Error())
						continue
					}

					if config.Full {
						for i := range scraped {
							changeRes, err := extractChangesFromConfig(scraped[i].Config)
							if err != nil {
								logger.Errorf("failed to extract changes from config: %v", err)
								continue
							}

							for _, cr := range changeRes {
								if cr.ExternalID == "" {
									continue
								}

								configItem, err := db.GetConfigItem(cr.ExternalType, cr.ExternalID)
								if err != nil {
									logger.Errorf("failed to get config item id: %v", err)
								} else if configItem != nil {
									cr.ConfigItemID = configItem.ID
									scraped[i].Changes = append(scraped[i].Changes, cr)
								}
							}
						}
					}

					results = append(results, scraped...)
				}

				if result.Error != nil {
					jobHistory.AddError(result.Error.Error())
				} else {
					jobHistory.IncrSuccess()
				}
			}

			jobHistory.End()
			if err := db.PersistJobHistory(&jobHistory); err != nil {
				logger.Errorf("Error persisting job history: %v", err)
			}
		}
	}

	return results, nil
}

// extractChangesFromConfig will attempt to extract changes from
// the scraped config.
func extractChangesFromConfig(config any) ([]v1.ChangeResult, error) {
	configMap, ok := config.(map[string]any)
	if !ok {
		return nil, errors.New("config is not a map")
	}

	changes, ok := configMap["changes"].([]any)
	if !ok {
		return nil, errors.New("changes is not a slice of map")
	}

	raw, err := json.Marshal(changes)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal changes: %v", err)
	}

	var changeRes []v1.ChangeResult
	if err := json.Unmarshal(raw, &changeRes); err != nil {
		return nil, fmt.Errorf("failed to unmarshal changes map into []v1.ChangeResult: %v", err)
	}

	return changeRes, nil
}
