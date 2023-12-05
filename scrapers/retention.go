package scrapers

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/flanksource/commons/duration"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/context"
	"github.com/google/uuid"
)

func ProcessChangeRetention(ctx context.Context, scraperID uuid.UUID, spec v1.ChangeRetentionSpec) error {
	var whereClauses []string

	var ageMinutes int
	if spec.Age != "" {
		age, err := duration.ParseDuration(spec.Age)
		if err != nil {
			return fmt.Errorf("error parsing age %s as duration: %w", spec.Age, err)
		}
		ageMinutes = int(age.Minutes())

		whereClauses = append(whereClauses, `((now()- created_at) > interval '1 minute' * @ageMinutes)`)
	}

	if spec.Count > 0 {
		whereClauses = append(whereClauses, `seq > @count`)
	}

	if len(whereClauses) == 0 {
		return fmt.Errorf("both age and count cannot be empty")
	}

	query := fmt.Sprintf(`
        WITH latest_config_changes AS (
            SELECT id, change_type, created_at, ROW_NUMBER() OVER(ORDER BY created_at DESC) AS seq
            FROM config_changes
            WHERE
                change_type = @changeType AND
                config_id IN (SELECT id FROM config_items WHERE scraper_id = @scraperID)
        )
        DELETE FROM config_changes
        WHERE id IN (
            SELECT id from latest_config_changes
            WHERE %s
        )
    `, strings.Join(whereClauses, " OR "))

	result := ctx.DB().Exec(query,
		sql.Named("changeType", spec.Name),
		sql.Named("scraperID", scraperID),
		sql.Named("ageMinutes", ageMinutes),
		sql.Named("count", spec.Count),
	)
	if err := result.Error; err != nil {
		return fmt.Errorf("error retaining config changes: %w", err)
	}

	if result.RowsAffected > 0 {
		logger.Infof("Deleted %d config_changes as per ChangeRetentionSpec[%s]", result.RowsAffected, spec.Name)
	}
	return nil
}
