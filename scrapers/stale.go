package scrapers

import (
	"github.com/flanksource/commons/duration"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/db"
	"github.com/google/uuid"
)

var (
	StaleTimeout string
)

func DeleteStaleConfigItems(scraperID uuid.UUID) error {
	// Get stale timeout in relative terms
	staleDuration, err := duration.ParseDuration(StaleTimeout)
	if err != nil {
		return err
	}
	staleMinutes := int(staleDuration.Minutes())

	query := `
        UPDATE config_items
        SET deleted_at = NOW()
        WHERE
            ((NOW() - updated_at) > INTERVAL '1 minute' * ?) AND
            deleted_at IS NULL AND
            scraper_id = ?`

	result := db.DefaultDB().Exec(query, staleMinutes, scraperID)
	if err := result.Error; err != nil {
		return err
	}

	if result.RowsAffected > 0 {
		logger.Infof("Marked %d items as deleted", result.RowsAffected)
	}
	return nil
}
