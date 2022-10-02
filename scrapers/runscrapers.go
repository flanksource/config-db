package scrapers

import (
	"os"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/scrapers/analysis"
	"github.com/flanksource/config-db/scrapers/processors"
)

// Run ...
func Run(ctx *v1.ScrapeContext, configs ...v1.ConfigScraper) ([]v1.ScrapeResult, error) {
	cwd, _ := os.Getwd()
	logger.Infof("Scraping files from (PWD: %s)", cwd)

	results := []v1.ScrapeResult{}
	for _, config := range configs {

		for _, scraper := range All {
			for _, result := range scraper.Scrape(ctx, config) {

				if result.AnalysisResult != nil {
					if rule, ok := analysis.Rules[result.AnalysisResult.Analyzer]; ok {
						result.AnalysisResult.AnalysisType = rule.Category
						result.AnalysisResult.Severity = rule.Severity
					}
				}

				if result.Config == nil && result.AnalysisResult != nil {
					results = append(results, result)
				} else if result.Config != nil {
					extractor, err := processors.NewExtractor(result.BaseScraper)
					if err != nil {
						logger.Errorf("failed to create extractor: %v", err)
						continue
					}
					scraped, err := extractor.Extract(result)
					if err != nil {
						logger.Errorf("failed to extract: %v", err)
						continue
					}
					results = append(results, scraped...)
				}

			}
		}
	}
	return results, nil
}
