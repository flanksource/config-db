package scrapers

import (
	"context"

	"github.com/flanksource/commons/logger"
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

func RunScrapers(scraperConfigs []v1.ConfigScraper, filename, outputDir string) error {
	ctx := v1.ScrapeContext{Context: context.Background()}
	results := []v1.ScrapeResult{}
	for _, scraperConfig := range scraperConfigs {
		logger.Debugf("Scrapping %+v", scraperConfig)
		_results, err := Run(ctx, scraperConfig)
		if err != nil {
			return err
		}
		results = append(results, _results...)
	}

	for _, result := range results {
		if err := exportResource(result, filename, outputDir); err != nil {
			return err
		}
	}
	return nil
}
