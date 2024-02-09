package scrapers

import (
	"fmt"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
)

type contextKey string

const (
	contextKeyScrapeStart contextKey = "scrape_start_time"
)

func RunScraper(ctx api.ScrapeContext) (v1.ScrapeResults, error) {
	ctx = ctx.WithValue(contextKeyScrapeStart, time.Now())

	results, scraperErr := Run(ctx)
	if scraperErr != nil {
		return nil, fmt.Errorf("failed to run scraper %v: %w", ctx.ScrapeConfig().Name, scraperErr)
	}

	if err := saveResults(ctx, results); err != nil {
		return nil, fmt.Errorf("failed to save results: %w", err)
	}

	return results, nil
}

func RunK8IncrementalScraper(ctx api.ScrapeContext, config v1.Kubernetes, events []v1.KubernetesEvent) error {
	ctx = ctx.WithValue(contextKeyScrapeStart, time.Now())

	results, scraperErr := runK8IncrementalScraper(ctx, config, events)
	if scraperErr != nil {
		return fmt.Errorf("failed to run scraper %v: %w", ctx.ScrapeConfig().Name, scraperErr)
	}

	if err := saveResults(ctx, results); err != nil {
		return fmt.Errorf("failed to save results: %w", err)
	}

	return nil
}

func saveResults(ctx api.ScrapeContext, results v1.ScrapeResults) error {
	dbErr := db.SaveResults(ctx, results)
	if dbErr != nil {
		//FIXME cache results to save to db later
		return fmt.Errorf("failed to update db: %w", dbErr)
	}

	persistedID := ctx.ScrapeConfig().GetPersistedID()
	if persistedID != nil {
		// If error in any of the scrape results, don't delete old items
		if len(results) > 0 && !v1.ScrapeResults(results).HasErr() {
			if err := DeleteStaleConfigItems(ctx, *persistedID); err != nil {
				return fmt.Errorf("error deleting stale config items: %w", err)
			}
		}
	}

	return nil
}
