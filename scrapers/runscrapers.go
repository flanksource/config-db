package scrapers

import (
	"fmt"
	"os"
	"time"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/db/models"
	"github.com/flanksource/config-db/scrapers/analysis"
	"github.com/flanksource/config-db/scrapers/changes"
	"github.com/flanksource/config-db/scrapers/processors"
)

// Run ...
func Run(ctx *v1.ScrapeContext, configs ...v1.ConfigScraper) ([]v1.ScrapeResult, error) {
	cwd, _ := os.Getwd()
	logger.Infof("Scraping files from (PWD: %s)", cwd)

	results := []v1.ScrapeResult{}
	var histories models.JobHistories
	for _, config := range configs {
		for _, scraper := range All {
			jobHistory := models.JobHistory{
				Name:      fmt.Sprintf("scraper:%T", scraper),
				TimeStart: time.Now(),
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
						jobHistory.ErrorCount += 1
						jobHistory.Errors = append(jobHistory.Errors, err.Error())
						continue
					}

					scraped, err := extractor.Extract(result)
					if err != nil {
						logger.Errorf("failed to extract: %v", err)
						jobHistory.ErrorCount += 1
						jobHistory.Errors = append(jobHistory.Errors, err.Error())
						continue
					}

					results = append(results, scraped...)
				}
				if result.Error != nil {
					jobHistory.ErrorCount += 1
					jobHistory.Errors = append(jobHistory.Errors, result.Error.Error())
				} else {
					jobHistory.SuccessCount += 1
				}
			}
			jobHistory.TimeEnd = time.Now()
			if jobHistory.SuccessCount+jobHistory.ErrorCount > 0 {
				histories = append(histories, jobHistory)
			}
		}
	}
	if err := db.SaveJobHistories(histories); err != nil {
		logger.Errorf("Failed to save job histories: %v", err)
	}
	return results, nil
}
