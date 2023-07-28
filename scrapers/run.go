package scrapers

import (
	"fmt"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/google/uuid"
)

func RunScraper(scraper v1.ScraperSpec) (v1.ScrapeResults, error) {
	id, err := uuid.Parse(scraper.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse uuid[%s]: %w", scraper.ID, err)
	}

	ctx := api.NewScrapeContext(&scraper, &id)
	var results v1.ScrapeResults
	var scraperErr, dbErr error
	if results, scraperErr = Run(ctx, scraper); scraperErr != nil {
		return nil, fmt.Errorf("failed to run scraper %v: %w", scraper, scraperErr)
	}

	if dbErr = db.SaveResults(ctx, results); dbErr != nil {
		//FIXME cache results to save to db later
		return nil, fmt.Errorf("failed to update db: %w", dbErr)
	}

	// If error in any of the scrape results, don't delete old items
	if scraperErr == nil && dbErr == nil && len(results) > 0 && !results.HasErr() {
		if err = DeleteStaleConfigItems(id); err != nil {
			return nil, fmt.Errorf("error deleting stale config items: %w", err)
		}
	}

	return results, nil
}
