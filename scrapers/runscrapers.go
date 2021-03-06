package scrapers

import (
	v1 "github.com/flanksource/confighub/api/v1"
)

// Run ...
func Run(ctx v1.ScrapeContext, manager v1.Manager, configs ...v1.ConfigScraper) ([]v1.ScrapeResult, error) {
	results := []v1.ScrapeResult{}
	for _, config := range configs {
		for _, scraper := range All {
			results = append(results, scraper.Scrape(ctx, config, manager)...)
		}
	}
	return results, nil
}
