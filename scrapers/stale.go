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
	staleHours := int(staleDuration.Hours())

	query := `
        UPDATE config_items
        SET deleted_at = NOW()
        WHERE
            ((NOW() - updated_at) > INTERVAL '1 hour' * ?) AND
            deleted_at IS NULL AND
            scraper_id = ?`

	result := db.DefaultDB().Exec(query, staleHours, scraperID)
	if err := result.Error; err != nil {
		return err
	}

	logger.Infof("Marked %d items as deleted", result.RowsAffected)
	return nil
}
