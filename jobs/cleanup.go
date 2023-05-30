package jobs

import (
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/duty/models"
)

const (
	DefaultConfigAnalysisRetentionDays = 60
	DefaultConfigChangeRetentionDays   = 60
)

var (
	ConfigAnalysisRetentionDays int
	ConfigChangeRetentionDays   int
)

func DeleteOldConfigAnalysis() {
	jobHistory := models.NewJobHistory("DeleteOldConfigAnalysis", "", "").Start()
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
	jobHistory := models.NewJobHistory("DeleteOldConfigChanges", "", "").Start()
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
