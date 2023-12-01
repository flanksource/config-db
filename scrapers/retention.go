package scrapers

import (
	"fmt"

	"github.com/flanksource/commons/duration"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/context"
	"github.com/google/uuid"
)

func ProcessChangeRetention(ctx context.Context, scraperID uuid.UUID, spec v1.ChangeRetentionSpec) error {
	age, err := duration.ParseDuration(spec.Age)
	if err != nil {
		return fmt.Errorf("error parsing age %s as duration: %w", spec.Age, err)
	}
	ageMinutes := int(age.Minutes())

	query := `
        WITH latest_config_changes AS (
            SELECT id, change_type, created_at, ROW_NUMBER() OVER(ORDER BY created_at DESC) AS seq
            FROM config_changes
            WHERE
                change_type = ? AND
                config_id IN (SELECT id FROM config_items WHERE scraper_id = ?) AND
                ((now()- created_at) < interval '1 minute' * ?)
        )
        DELETE FROM config_changes
        WHERE id IN (
            SELECT id from latest_config_changes WHERE seq > ?
        )
    `

	//query = `
	//UPDATE config_changes
	//SET deleted_at = NOW()
	//WHERE
	//change_type = ? AND
	//config_id IN (SELECT id FROM config_item WHERE scraper_id = ?) AND
	//((NOW() - created_at > INTERVAL '1 minute' * ?)) AND
	//deleted_at IS NULL
	//`

	result := ctx.DB().Exec(query, spec.Name, scraperID, ageMinutes, spec.Count)
	if err := result.Error; err != nil {
		return fmt.Errorf("error retaining config changes: %w", err)
	}

	if result.RowsAffected > 0 {
		logger.Infof("Marked %d config_changes as deleted", result.RowsAffected)
	}

	return nil
}
