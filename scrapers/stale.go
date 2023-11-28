package scrapers

import (
	"github.com/flanksource/commons/duration"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/duty/context"
	"github.com/google/uuid"
)

var (
	StaleTimeout string
)

func DeleteStaleConfigItems(ctx context.Context, scraperID uuid.UUID) error {
	// Get stale timeout in relative terms
	staleDuration, err := duration.ParseDuration(StaleTimeout)
	if err != nil {
		return err
	}
	staleMinutes := int(staleDuration.Minutes())

	// TODO Check deleted_at against updated_at and if updated_at is greater
	// and reason is missing scrape, remove deleted_at
	deleteQuery := `
        UPDATE config_items
        SET
            deleted_at = NOW(),
            delete_reason = ?
        WHERE
            ((NOW() - updated_at) > INTERVAL '1 minute' * ?) AND
            deleted_at IS NULL AND
            scraper_id = ?`

	result := ctx.DB().Exec(deleteQuery, v1.DeletedReasonMissingScrape, staleMinutes, scraperID)
	if err := result.Error; err != nil {
		return err
	}

	if result.RowsAffected > 0 {
		logger.Infof("Marked %d items as deleted", result.RowsAffected)
	}

	undeleteQuery := `
        UPDATE config_items
        SET
            deleted_at = NULL,
            delete_reason = NULL
        WHERE
            deleted_at IS NOT NULL AND
            delete_reason = ? AND
            updated_at > deleted_at AND
            scraper_id = ?`

	result = db.DefaultDB().Exec(undeleteQuery, v1.DeletedReasonMissingScrape, scraperID)
	if err := result.Error; err != nil {
		return err
	}

	if result.RowsAffected > 0 {
		logger.Infof("Marked %d items as not deleted", result.RowsAffected)
	}
	return nil
}
