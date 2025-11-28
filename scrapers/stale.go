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
		FROM config_items ci
		LEFT JOIN config_items_last_scraped_time lst ON lst.config_id = ci.id
		WHERE
			config_items.id = ci.id AND
			config_items.deleted_at IS NULL AND
			config_items.scraper_id = ? AND
			(
				((NOW() - lst.last_scraped_time) > INTERVAL '1 SECOND' * ?) OR
				(lst.config_id IS NULL AND (NOW() - ci.created_at) > INTERVAL '1 SECOND' * ?)
			)
		RETURNING config_items.type`

	var deletedConfigs []models.ConfigItem
	result := ctx.DB().Raw(deleteQuery, v1.DeletedReasonStale, scraperID, staleDuration.Seconds(), staleDuration.Seconds()).Scan(&deletedConfigs)
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
