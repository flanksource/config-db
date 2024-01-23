package jobs

import (
	"reflect"
	"runtime"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	"github.com/robfig/cron/v3"
)

var FuncScheduler = cron.New()

func ScheduleJobs() {
	scheduleFunc := func(schedule string, fn func()) {
		if _, err := FuncScheduler.AddFunc(schedule, fn); err != nil {
			logger.Fatalf("Error scheduling %s job", runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name())
		}
	}

	scheduleFunc("@every 24h", DeleteOldConfigChanges)
	scheduleFunc("@every 24h", DeleteOldConfigAnalysis)
	scheduleFunc("@every 24h", CleanupConfigItems)
	scheduleFunc("@every 1h", ProcessChangeRetentionRules)

	if api.UpstreamConfig.Valid() {
		// Syncs config_items to upstream in real-time
		if err := StartUpstreamConsumer(api.DefaultContext.DutyContext()); err != nil {
			logger.Fatalf("Failed to start event consumer: %v", err)
		}

		for _, j := range UpstreamJobs {
			var job = j
			job.Context = api.DefaultContext.DutyContext()
			if err := job.AddToScheduler(FuncScheduler); err != nil {
				logger.Errorf(err.Error())
			}
		}
	}

	FuncScheduler.Start()
}
