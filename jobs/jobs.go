package jobs

import (
	"reflect"
	"runtime"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	"github.com/robfig/cron/v3"
)

var FuncScheduler = cron.New()

const (
	PullConfigScrapersFromUpstreamSchedule = "@every 30s"
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

	if api.UpstreamConfig.Valid() {
		pullJob := &UpstreamPullJob{}
		pullJob.Run()

		pushJob := &UpstreamPushJob{MaxAge: time.Minute * 5}
		pushJob.Run()

		if _, err := FuncScheduler.AddJob(PullConfigScrapersFromUpstreamSchedule, pullJob); err != nil {
			logger.Fatalf("Failed to schedule job [PullUpstreamScrapeConfigs]: %v", err)
		}

		if _, err := FuncScheduler.AddJob(PushConfigResultsToUpstreamSchedule, pushJob); err != nil {
			logger.Fatalf("Failed to schedule job [UpstreamPushJob]: %v", err)
		}

		scheduleFunc(ReconcileConfigsToUpstreamSchedule, ReconcileConfigScraperResults)
	}

	FuncScheduler.Start()
}
