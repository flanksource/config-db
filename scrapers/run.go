package scrapers

import (
	"fmt"
	"time"

	"github.com/flanksource/commons/logger"
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

	if err := SaveResults(ctx, results); err != nil {
		return nil, fmt.Errorf("failed to save results: %w", err)
	}

	return results, nil
}

func SaveResults(ctx api.ScrapeContext, results v1.ScrapeResults) error {
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

	// Any config item that was previously marked as deleted should be un-deleted
	// if the item was re-discovered in this run.
	if val := ctx.Value(contextKeyScrapeStart); val != nil {
		if start, ok := val.(time.Time); ok {
			query := `UPDATE config_items SET deleted_at = NULL WHERE deleted_at IS NOT NULL AND ((NOW() - updated_at) <= INTERVAL '1 SECOND' * ?)`
			tx := ctx.DutyContext().DB().Exec(query, time.Since(start).Seconds())
			if err := tx.Error; err != nil {
				return fmt.Errorf("error un-deleting stale config items: %w", err)
			}

			logger.Debugf("undeleted %d stale config items", tx.RowsAffected)
		}
	}

	return nil
}
