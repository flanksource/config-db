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
	ctx, err := ctx.InitTempCache()
	if err != nil {
		return nil, err
	}

	ctx = ctx.WithValue(contextKeyScrapeStart, time.Now())
	ctx.Context = ctx.WithName(fmt.Sprintf("%s/%s", ctx.ScrapeConfig().Namespace, ctx.ScrapeConfig().Name))

	results, scraperErr := Run(ctx)
	if scraperErr != nil {
		return nil, fmt.Errorf("failed to run scraper %v: %w", ctx.ScrapeConfig().Name, scraperErr)
	}

	if err := db.SaveResults(ctx, results); err != nil {
		return nil, fmt.Errorf("failed to save results: %w", err)
	}

	if err := UpdateStaleConfigItems(ctx, results); err != nil {
		return nil, fmt.Errorf("failed to update stale config items: %w", err)
	}

	ctx.Logger.V(1).Infof("Completed scraping with %d results in %s", len(results), time.Since(ctx.Value(contextKeyScrapeStart).(time.Time)))
	return results, nil
}

func UpdateStaleConfigItems(ctx api.ScrapeContext, results v1.ScrapeResults) error {
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
			query := `UPDATE config_items
				SET deleted_at = NULL
				WHERE deleted_at IS NOT NULL
					AND deleted_at != updated_at
					AND ((NOW() - last_scraped_time) <= INTERVAL '1 SECOND' * ?)`
			tx := ctx.DutyContext().DB().Exec(query, time.Since(start).Seconds())
			if err := tx.Error; err != nil {
				return fmt.Errorf("error un-deleting stale config items: %w", err)
			}

			ctx.Logger.V(3).Infof("undeleted %d stale config items", tx.RowsAffected)
		}
	}

	return nil
}
