package jobs

import (
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

func DeleteOldConfigAnalysis(ctx job.JobRuntime) error {
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
}

func DeleteOldConfigChanges(ctx job.JobRuntime) error {
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
}

func CleanupConfigItems(ctx job.JobRuntime) error {
	if ConfigItemRetentionDays <= 0 {
		ConfigItemRetentionDays = DefaultConfigItemRetentionDays
	}

	tx := db.DefaultDB().Exec(`
        DELETE FROM config_items
        WHERE
            (NOW() - deleted_at) > INTERVAL '1 day' * ? AND
            id NOT IN (
                SELECT config_id FROM evidences WHERE config_id IS NOT NULL
                UNION
                SELECT config_id FROM config_changes WHERE id IN (SELECT config_change_id FROM evidences)
                UNION
                SELECT config_id FROM config_analysis WHERE id IN (SELECT config_analysis_id FROM evidences)
            )
    `, ConfigItemRetentionDays)
	ctx.History.SuccessCount = int(tx.RowsAffected)
	return tx.Error
}
