package scrapers

import (
	"database/sql"
	"fmt"

	"github.com/flanksource/commons/duration"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/properties"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/context"
	"github.com/google/uuid"
)

func ProcessChangeRetention(ctx context.Context, scraperID uuid.UUID, spec v1.ChangeRetentionSpec) error {
	if spec.Age == "" && spec.Count <= 0 {
		return fmt.Errorf("both age and count cannot be empty")
	}

	var totalDeleted int64

	if spec.Age != "" {
		age, err := duration.ParseDuration(spec.Age)
		if err != nil {
			return fmt.Errorf("error parsing age %s as duration: %w", spec.Age, err)
		}
		ageMinutes := int(age.Minutes())

		const query = `
			DELETE FROM config_changes
			WHERE id IN (
				SELECT cc.id
				FROM config_changes cc
				JOIN config_items ci ON cc.config_id = ci.id
				WHERE ci.scraper_id = @scraperID
					AND cc.change_type = @changeType
					AND cc.created_at < now() - interval '1 minute' * @ageMinutes
				LIMIT @batchSize
			)
		`
		deleted, err := deleteInBatches(ctx, query,
			sql.Named("changeType", spec.Name),
			sql.Named("scraperID", scraperID),
			sql.Named("ageMinutes", ageMinutes),
			sql.Named("batchSize", properties.Int(1000, "change_retention.delete_batch_size")),
		)
		if err != nil {
			return fmt.Errorf("error retaining config changes by age: %w", err)
		}
		totalDeleted += deleted
	}

	if spec.Count > 0 {
		const query = `
			DELETE FROM config_changes
			WHERE id IN (
				SELECT id FROM (
					SELECT id, ROW_NUMBER() OVER (PARTITION BY config_id ORDER BY created_at DESC) AS seq
					FROM config_changes
					WHERE change_type = @changeType
						AND config_id IN (SELECT id FROM config_items WHERE scraper_id = @scraperID)
				) ranked
				WHERE seq > @count
				LIMIT @batchSize
			)
		`
		deleted, err := deleteInBatches(ctx, query,
			sql.Named("changeType", spec.Name),
			sql.Named("scraperID", scraperID),
			sql.Named("count", spec.Count),
			sql.Named("batchSize", properties.Int(1000, "change_retention.delete_batch_size")),
		)
		if err != nil {
			return fmt.Errorf("error retaining config changes by count: %w", err)
		}
		totalDeleted += deleted
	}

	if totalDeleted > 0 {
		logger.Infof("Deleted %d config_changes for Scraper[%s] as per ChangeRetentionSpec[%s]", totalDeleted, scraperID, spec.Name)
	}
	return nil
}

func deleteInBatches(ctx context.Context, query string, args ...any) (int64, error) {
	var total int64
	for {
		result := ctx.DB().Exec(query, args...)
		if result.Error != nil {
			return total, result.Error
		}
		total += result.RowsAffected
		if result.RowsAffected == 0 {
			return total, nil
		}
	}
}
