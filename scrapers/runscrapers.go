package scrapers

import (
	v1 "github.com/flanksource/confighub/api/v1"
)

func Run(ctx v1.ScrapeContext, configs ...v1.ConfigScraper) ([]v1.ScrapeResult, error) {
	results := []v1.ScrapeResult{}
	for _, config := range configs {
		for _, scraper := range All {
			results = append(results, scraper.Scrape(ctx, config)...)
		}
	}
	return results, nil
}
