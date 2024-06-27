package jobs

import (
	"fmt"
	"time"

	"github.com/flanksource/duty"
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
	Retention:  job.RetentionBalanced,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = JobResourceType
		if ConfigAnalysisRetentionDays <= 0 {
			ConfigAnalysisRetentionDays = DefaultConfigAnalysisRetentionDays
		}

		tx := ctx.DB().Exec(`
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
	Retention:  job.RetentionBalanced,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = JobResourceType
		if ConfigChangeRetentionDays <= 0 {
			ConfigChangeRetentionDays = DefaultConfigChangeRetentionDays
		}

		tx := ctx.DB().Exec(`
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
	Schedule:   "0 2 * * *", // Everynight at 2 AM
	Singleton:  true,
	JobHistory: true,
	Retention:  job.RetentionBalanced,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = JobResourceType
		retention := ctx.Properties().Duration("config.retention.period", (time.Hour * 24 * time.Duration(ConfigItemRetentionDays)))
		days := int64(retention.Hours() / 24)

		var prevCount int64
		if err := ctx.DB().Raw("SELECT COUNT(*) FROM config_items").Scan(&prevCount).Error; err != nil {
			return fmt.Errorf("failed to count config items: %w", duty.DBErrorDetails(err))
		}

		if err := ctx.DB().Exec("SELECT delete_old_config_items(?)", days).Error; err != nil {
			return fmt.Errorf("failed to delete config items: %w", duty.DBErrorDetails(err))
		}

		var newCount int64
		if err := ctx.DB().Raw("SELECT COUNT(*) FROM config_items").Scan(&newCount).Error; err != nil {
			return fmt.Errorf("failed to count config items: %w", duty.DBErrorDetails(err))
		}
		ctx.History.SuccessCount = int(newCount - prevCount)
		return nil
	},
}
