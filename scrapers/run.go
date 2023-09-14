package scrapers

import (
	"fmt"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
)

func RunScraper(ctx *v1.ScrapeContext) (v1.ScrapeResults, error) {
	results, scraperErr := Run(ctx)
	if scraperErr != nil {
		return nil, fmt.Errorf("failed to run scraper %v: %w", ctx.ScrapeConfig.Name, scraperErr)
	}

	if err := saveResults(ctx, results); err != nil {
		return nil, fmt.Errorf("failed to save results: %w", err)
	}

	return results, nil
}

func RunK8IncrementalScraper(ctx *v1.ScrapeContext, config v1.Kubernetes, resources []*v1.InvolvedObject) error {
	results, scraperErr := runK8IncrementalScraper(ctx, config, resources)
	if scraperErr != nil {
		return fmt.Errorf("failed to run scraper %v: %w", ctx.ScrapeConfig.Name, scraperErr)
	}

	if err := saveResults(ctx, results); err != nil {
		return fmt.Errorf("failed to save results: %w", err)
	}

	return nil
}

func saveResults(ctx *v1.ScrapeContext, results v1.ScrapeResults) error {
	dbErr := db.SaveResults(ctx, results)
	if dbErr != nil {
		//FIXME cache results to save to db later
		return fmt.Errorf("failed to update db: %w", dbErr)
	}

	// If error in any of the scrape results, don't delete old items
	if len(results) > 0 && !v1.ScrapeResults(results).HasErr() {
		if err := DeleteStaleConfigItems(*ctx.ScrapeConfig.GetPersistedID()); err != nil {
			return fmt.Errorf("error deleting stale config items: %w", err)
		}
	}

	logger.Debugf("Saved scrape results. name=%s", ctx.ScrapeConfig.Name)
	return nil
}
