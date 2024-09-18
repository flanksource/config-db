package scrapers

import (
	"fmt"
	"time"

	"github.com/flanksource/commons/duration"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/context"
	"github.com/google/uuid"
)

var (
	DefaultStaleTimeout = "24h"
)

func DeleteStaleConfigItems(ctx context.Context, staleTimeout string, scraperID uuid.UUID) (int64, error) {
	var staleDuration time.Duration
	if val := ctx.Value(contextKeyScrapeStart); val != nil {
		if start, ok := val.(time.Time); ok {
			staleDuration = time.Since(start)
		}
	}

	if staleTimeout == "keep" {
		return 0, nil
	} else if staleTimeout == "" {
		if defaultVal, exists := ctx.Properties()["config.retention.stale_item_age"]; exists {
			staleTimeout = defaultVal
		} else {
			staleTimeout = DefaultStaleTimeout
		}
	}

	if parsed, err := duration.ParseDuration(staleTimeout); err != nil {
		return 0, fmt.Errorf("failed to parse stale timeout %s: %w", staleTimeout, err)
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

	result := ctx.DB().Exec(deleteQuery, v1.DeletedReasonStale, staleDuration.Seconds(), scraperID)
	if err := result.Error; err != nil {
		return 0, fmt.Errorf("failed to delete stale config items: %w", err)
	}

	if result.RowsAffected > 0 {
		ctx.Logger.V(3).Infof("Deleted %d stale config items", result.RowsAffected)
	}

	return result.RowsAffected, nil
}
