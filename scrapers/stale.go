package scrapers

import (
	"github.com/flanksource/commons/duration"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/db"
)

var (
	StaleTimeout string
)

func DeleteStaleConfigItems() {
	// Get stale timeout in relative terms
	staleDuration, err := duration.ParseDuration(StaleTimeout)
	if err != nil {
		logger.Errorf("Error parsing duration %s: %v", StaleTimeout, err)
		return
	}
	staleHours := int(staleDuration.Hours())

	query := `
        UPDATE config_items
        SET deleted_at = NOW()
        WHERE
            ((NOW() - updated_at) > INTERVAL '1 hour' * ?) AND
            deleted_at IS NULL
    `

	result := db.DefaultDB().Exec(query, staleHours)
	if err := result.Error; err != nil {
		logger.Errorf("Error marking config as stale: %v", err)
		return
	}

	logger.Infof("Marked %d items as deleted", result.RowsAffected)
}
