package scrapers

import (
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

				if result.Costs != nil {
					gormDB := db.DefaultDB()
					var accountTotal1h, accountTotal1d, accountTotal7d, accountTotal30d float64
					for _, item := range result.Costs.LineItems {
						tx := gormDB.Exec(`UPDATE config_items SET cost_per_minute = ?, cost_total_1d = ?, cost_total_7d = ?, cost_total_30d = ? WHERE ? = ANY(external_id)`,
							item.CostPerMin,
							item.Cost1d,
							item.Cost7d,
							item.Cost30d,
							item.ExternalID,
						)

						if tx.Error != nil {
							logger.Errorf("error updating costs for config_item  (externalID=%s): %v", item.ExternalID, tx.Error)
							continue
						}

						if tx.RowsAffected == 0 {
							accountTotal1h += item.CostPerMin
							accountTotal1d += item.Cost1d
							accountTotal7d += item.Cost7d
							accountTotal30d += item.Cost30d
							continue
						}

						logger.Infof("updated cost (externalID=%s)", item.ExternalID)
					}

					err := gormDB.Exec(`UPDATE config_items SET cost_per_minute = ?, cost_total_1d = ?, cost_total_7d = ?, cost_total_30d = ? WHERE external_type = ? AND ? = ANY(external_id)`,
						accountTotal1h,
						accountTotal1d,
						accountTotal7d,
						accountTotal30d,
						result.Costs.ExternalType,
						result.Costs.ExternalID,
					).Error
					if err != nil {
						logger.Errorf("error updating costs (type=%s) (externalID=%s): %v", result.Costs.ExternalType, result.Costs.ExternalID, err)
					}

					logger.Infof("updated total cost (externalID=%s)", result.Costs.ExternalID)
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
