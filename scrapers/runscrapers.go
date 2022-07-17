package scrapers

import (
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/scrapers/processors"
)

// Run ...
func Run(ctx v1.ScrapeContext, manager v1.Manager, configs ...v1.ConfigScraper) ([]v1.ScrapeResult, error) {
	results := []v1.ScrapeResult{}
	for _, config := range configs {

		for _, scraper := range All {
			for _, result := range scraper.Scrape(ctx, config, manager) {
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
	return results, nil
}
