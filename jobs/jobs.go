package jobs

import (
	"reflect"
	"runtime"

	"github.com/flanksource/commons/logger"
	"github.com/robfig/cron/v3"
)

var FuncScheduler = cron.New()

func ScheduleJobs() {
	scheduleFunc := func(schedule string, fn func()) {
		if _, err := FuncScheduler.AddFunc(schedule, fn); err != nil {
			logger.Errorf("Error scheduling %s job", runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name())
		}
	}

	scheduleFunc("@every 24h", DeleteOldConfigChanges)
	scheduleFunc("@every 24h", DeleteOldConfigAnalysis)
	scheduleFunc("@every 24h", CleanupConfigItems)

	FuncScheduler.Start()
}
