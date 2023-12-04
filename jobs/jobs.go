package jobs

import (
	"reflect"
	"runtime"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	"github.com/robfig/cron/v3"
)

var FuncScheduler = cron.New()

const (
	PullConfigScrapersFromUpstreamSchedule = "@every 5m"
	PushConfigResultsToUpstreamSchedule    = "@every 10s"
	ReconcileConfigsToUpstreamSchedule     = "@every 3h"
)

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
		pullJob := &UpstreamPullJob{}
		pullJob.Run()

		if _, err := FuncScheduler.AddJob(PullConfigScrapersFromUpstreamSchedule, pullJob); err != nil {
			logger.Fatalf("Failed to schedule job [PullUpstreamScrapeConfigs]: %v", err)
		}

		// Syncs scrape config results to upstream in real-time
		if err := StartConsumser(api.DefaultContext); err != nil {
			logger.Fatalf("Failed to start event consumer: %v", err)
		}

		scheduleFunc(ReconcileConfigsToUpstreamSchedule, ReconcileConfigScraperResults)
	}

	FuncScheduler.Start()
}
