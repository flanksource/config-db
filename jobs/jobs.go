package jobs

import (
	"github.com/flanksource/commons/logger"
	"github.com/robfig/cron/v3"
)

var FuncScheduler = cron.New()

func ScheduleJobs() {
	if _, err := FuncScheduler.AddFunc("@every 24h", DeleteOldConfigAnalysis); err != nil {
		logger.Errorf("Error scheduling DeleteOldConfigAnalysis job")
	}

	if _, err := FuncScheduler.AddFunc("@every 24h", DeleteOldConfigChanges); err != nil {
		logger.Errorf("Error scheduling DeleteOldConfigChanges job")
	}

	FuncScheduler.Start()
}
