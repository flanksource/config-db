package jobs

import (
	gocontext "context"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
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

func DeleteOldConfigAnalysis() {
	ctx := context.NewContext(gocontext.Background()).WithDB(db.DefaultDB(), db.Pool)
	jobHistory := models.NewJobHistory(ctx.Logger, "DeleteOldConfigAnalysis", "", "").Start()
	_ = db.PersistJobHistory(jobHistory)

	if ConfigAnalysisRetentionDays <= 0 {
		ConfigAnalysisRetentionDays = DefaultConfigAnalysisRetentionDays
	}
	err := db.DefaultDB().Exec(`
        DELETE FROM config_analysis
        WHERE
            (NOW() - last_observed) > INTERVAL '1 day' * ? AND
            id NOT IN (SELECT config_analysis_id FROM evidences)
    `, ConfigAnalysisRetentionDays).Error

	if err != nil {
		logger.Errorf("Error deleting old config analysis: %v", err)
		jobHistory.AddError(err.Error())
	} else {
		jobHistory.IncrSuccess()
	}

	_ = db.PersistJobHistory(jobHistory.End())
}

func DeleteOldConfigChanges() {
	ctx := context.NewContext(gocontext.Background()).WithDB(db.DefaultDB(), db.Pool)
	jobHistory := models.NewJobHistory(ctx.Logger, "DeleteOldConfigChanges", "", "").Start()
	_ = db.PersistJobHistory(jobHistory)

	if ConfigChangeRetentionDays <= 0 {
		ConfigChangeRetentionDays = DefaultConfigChangeRetentionDays
	}
	err := db.DefaultDB().Exec(`
        DELETE FROM config_changes
        WHERE
            (NOW() - created_at) > INTERVAL '1 day' * ? AND
            id NOT IN (SELECT config_change_id FROM evidences)
    `, ConfigChangeRetentionDays).Error

	if err != nil {
		logger.Errorf("Error deleting old config changes: %v", err)
		jobHistory.AddError(err.Error())
	} else {
		jobHistory.IncrSuccess()
	}

	_ = db.PersistJobHistory(jobHistory.End())
}

func CleanupConfigItems() {
	ctx := context.NewContext(gocontext.Background()).WithDB(db.DefaultDB(), db.Pool)
	jobHistory := models.NewJobHistory(ctx.Logger, "CleanupConfigItems", "", "").Start()
	_ = db.PersistJobHistory(jobHistory)
	defer func() {
		_ = db.PersistJobHistory(jobHistory.End())
	}()

	if ConfigItemRetentionDays <= 0 {
		ConfigItemRetentionDays = DefaultConfigItemRetentionDays
	}
	err := db.DefaultDB().Exec(`
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
    `, ConfigItemRetentionDays).Error

	if err != nil {
		logger.Errorf("Error cleaning up config items: %v", err)
		jobHistory.AddError(err.Error())
	} else {
		jobHistory.IncrSuccess()
	}
}
