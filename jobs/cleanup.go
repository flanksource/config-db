package jobs

import (
	"fmt"
	"time"

	"github.com/flanksource/config-db/db"
	"github.com/flanksource/duty/job"
)

const (
	DefaultConfigAnalysisRetentionDays = 60
	DefaultConfigChangeRetentionDays   = 60
	DefaultConfigItemRetentionDays     = 7
)

var (
	ConfigAnalysisRetentionDays int
	ConfigChangeRetentionDays   int
	ConfigItemRetentionDays     int
)

var cleanupJobs = []*job.Job{
	CleanupConfigAnalysis,
	CleanupConfigChanges,
	CleanupConfigItems,
}

var CleanupConfigAnalysis = &job.Job{
	Name:       "CleanupConfigAnalysis",
	Schedule:   "@every 24h",
	Singleton:  true,
	JobHistory: true,
	Retention:  job.Retention3Day,
	RunNow:     true,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = JobResourceType
		if ConfigAnalysisRetentionDays <= 0 {
			ConfigAnalysisRetentionDays = DefaultConfigAnalysisRetentionDays
		}

		tx := db.DefaultDB().Exec(`
        DELETE FROM config_analysis
        WHERE
            (NOW() - last_observed) > INTERVAL '1 day' * ? AND
            id NOT IN (SELECT config_analysis_id FROM evidences)
    `, ConfigAnalysisRetentionDays)

		ctx.History.SuccessCount = int(tx.RowsAffected)
		return tx.Error
	},
}

var CleanupConfigChanges = &job.Job{
	Name:       "CleanupConfigChanges",
	Schedule:   "@every 24h",
	Singleton:  true,
	JobHistory: true,
	Retention:  job.Retention3Day,
	RunNow:     true,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = JobResourceType
		if ConfigChangeRetentionDays <= 0 {
			ConfigChangeRetentionDays = DefaultConfigChangeRetentionDays
		}

		tx := db.DefaultDB().Exec(`
        DELETE FROM config_changes
        WHERE
            (NOW() - created_at) > INTERVAL '1 day' * ? AND
            id NOT IN (SELECT config_change_id FROM evidences)
    `, ConfigChangeRetentionDays)
		ctx.History.SuccessCount = int(tx.RowsAffected)
		return tx.Error
	},
}

var CleanupConfigItems = &job.Job{
	Name:       "CleanupConfigItems",
	Schedule:   "@every 24h",
	Singleton:  true,
	JobHistory: true,
	Retention:  job.Retention3Day,
	RunNow:     true,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = JobResourceType
		retention := ctx.Properties().Duration("config.retention.period", (time.Hour * 24 * time.Duration(ConfigItemRetentionDays)))
		seconds := int64(retention.Seconds())

		ctx.Tracef("cleaning up config items older than %v", retention)

		linkedConfigsQuery := `
		SELECT config_id FROM evidences WHERE config_id IS NOT NULL
		UNION
		SELECT config_id FROM config_changes WHERE id IN (SELECT config_change_id FROM evidences)
		UNION
		SELECT config_id FROM config_analysis WHERE id IN (SELECT config_analysis_id FROM evidences)
		UNION
		SELECT config_id FROM playbook_RUNS WHERE config_id IS NOT NULL
		`

		relationshipDeleteQuery := fmt.Sprintf(`
		DELETE FROM config_relationships 
		WHERE deleted_at < NOW() - interval '1 SECONDS' * ? 
		OR config_id in (SELECT id FROM config_items WHERE id NOT IN (%s) AND deleted_at < NOW() - interval '1 SECONDS' * ?) 
		OR related_id in (SELECT id FROM config_items WHERE id NOT IN (%s) AND deleted_at < NOW() - interval '1 SECONDS' * ?)`, linkedConfigsQuery, linkedConfigsQuery)
		if tx := ctx.Context.DB().Exec(relationshipDeleteQuery, seconds, seconds, seconds); tx.Error != nil {
			return fmt.Errorf("failed to delete config relationships: %w", tx.Error)
		} else {
			ctx.Tracef("deleted %d config relationships", tx.RowsAffected)
		}

		// break the parent relationship of deleted configs
		breakParentRelationshipQuery := fmt.Sprintf(`
		UPDATE config_items 
		SET parent_id = NULL 
			WHERE id IN (%s) AND parent_id IS NOT NULL AND deleted_at < NOW() - interval '7 days'`,
			linkedConfigsQuery)
		if tx := ctx.Context.DB().Exec(breakParentRelationshipQuery); tx.Error != nil {
			return fmt.Errorf("failed to remove config parent relationships: %w", tx.Error)
		} else {
			ctx.Tracef("removed %d config parent relationships", tx.RowsAffected)
		}

		configDeleteQuery := fmt.Sprintf(`
		DELETE FROM config_items
		WHERE
			deleted_at < NOW() - interval '1 SECONDS' * ? AND
			id NOT IN (%s)`, linkedConfigsQuery)
		for {
			tx := ctx.Context.DB().Exec(configDeleteQuery, seconds)
			if tx.Error != nil {
				return fmt.Errorf("failed to delete config items: %w", tx.Error)
			}

			if tx.RowsAffected == 0 {
				break
			}

			ctx.Tracef("removed %d config items", tx.RowsAffected)
			ctx.History.SuccessCount = int(tx.RowsAffected)
		}

		return nil
	},
}
