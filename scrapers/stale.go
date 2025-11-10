package scrapers

import (
	"fmt"
	"time"

	"github.com/flanksource/commons/duration"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
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

	switch staleTimeout {
	case "keep":
		return 0, nil
	case "":
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
		FROM config_items_last_scraped_time
		WHERE
			config_items_last_scraped_time.config_id = config_items.id AND
			((NOW() - config_items_last_scraped_time.last_scraped_time) > INTERVAL '1 SECOND' * ?) AND
			config_items.deleted_at IS NULL AND
			config_items.scraper_id = ?
		RETURNING config_items.type`

	var deletedConfigs []models.ConfigItem
	result := ctx.DB().Raw(deleteQuery, v1.DeletedReasonStale, staleDuration.Seconds(), scraperID).Scan(&deletedConfigs)
	if err := result.Error; err != nil {
		return 0, fmt.Errorf("failed to delete stale config items: %w", err)
	}

	if len(deletedConfigs) > 0 {
		ctx.Logger.V(3).Infof("deleted %d stale config items for scraper: %s", len(deletedConfigs), scraperID)
	}

	for _, c := range deletedConfigs {
		ctx.Counter("scraper_deleted", "scraper_id", scraperID.String(), "kind", c.Type, "reason", string(v1.DeletedReasonStale)).Add(1)
	}

	return result.RowsAffected, nil
}
