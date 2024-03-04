package scrapers

import (
	"fmt"
	"time"

	"github.com/flanksource/commons/duration"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/google/uuid"
)

var (
	StaleTimeout string
)

func DeleteStaleConfigItems(ctx api.ScrapeContext, scraperID uuid.UUID) error {
	var staleDuration time.Duration
	if val := ctx.Value(contextKeyScrapeStart); val != nil {
		if start, ok := val.(time.Time); ok {
			staleDuration = time.Since(start)
		}
	}

	if parsed, err := duration.ParseDuration(StaleTimeout); err != nil {
		return fmt.Errorf("failed to parse stale timeout %s: %w", StaleTimeout, err)
	} else if time.Duration(parsed) > staleDuration {
		// Use which ever is greater
		staleDuration = time.Duration(parsed)
	}

	deleteQuery := `
        UPDATE config_items
        SET
            deleted_at = NOW(),
            delete_reason = ?
        WHERE
            ((NOW() - last_scraped_time) > INTERVAL '1 SECOND' * ?) AND
            deleted_at IS NULL AND
            scraper_id = ?`

	result := ctx.DutyContext().DB().Exec(deleteQuery, v1.DeletedReasonStale, staleDuration.Seconds(), scraperID)
	if err := result.Error; err != nil {
		return fmt.Errorf("failed to delete stale config items: %w", err)
	}

	if result.RowsAffected > 0 {
		logger.Debugf("Deleted %d stale config items", result.RowsAffected)
	}

	return nil
}
