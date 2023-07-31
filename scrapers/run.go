package scrapers

import (
	"fmt"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
)

func RunScraper(ctx *v1.ScrapeContext) (v1.ScrapeResults, error) {
	results, scraperErr := Run(ctx)
	if scraperErr != nil {
		return nil, fmt.Errorf("failed to run scraper %v: %w", ctx.ScrapeConfig.Name, scraperErr)
	}

	dbErr := db.SaveResults(ctx, results)
	if dbErr != nil {
		//FIXME cache results to save to db later
		return nil, fmt.Errorf("failed to update db: %w", dbErr)
	}

	// If error in any of the scrape results, don't delete old items
	if len(results) > 0 && !v1.ScrapeResults(results).HasErr() {
		if err := DeleteStaleConfigItems(*ctx.ScrapeConfig.GetPersistedID()); err != nil {
			return nil, fmt.Errorf("error deleting stale config items: %w", err)
		}
	}

	return results, nil
}
