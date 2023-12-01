package scrapers

import (
	"fmt"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
)

func RunScraper(ctx api.ScrapeContext) (v1.ScrapeResults, error) {
	results, scraperErr := Run(ctx)
	if scraperErr != nil {
		return nil, fmt.Errorf("failed to run scraper %v: %w", ctx.ScrapeConfig().Name, scraperErr)
	}

	dbErr := db.SaveResults(ctx, results)
	if dbErr != nil {
		//FIXME cache results to save to db later
		return nil, fmt.Errorf("failed to update db: %w", dbErr)
	}

	persistedID := ctx.ScrapeConfig().GetPersistedID()
	if persistedID != nil {
		// If error in any of the scrape results, don't delete old items
		if len(results) > 0 && !v1.ScrapeResults(results).HasErr() {
			if err := DeleteStaleConfigItems(ctx.DutyContext(), *persistedID); err != nil {
				return nil, fmt.Errorf("error deleting stale config items: %w", err)
			}
		}

		// Process change retention
		for _, change := range ctx.ScrapeConfig().Spec.Retention.Changes {
			if err := ProcessChangeRetention(ctx.DutyContext(), *persistedID, change); err != nil {
				logger.Errorf("Error processing change retention rules: %v", err)
			}
		}
	}

	return results, nil
}
