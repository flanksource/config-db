package jobs

import (
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/job"
	"github.com/robfig/cron/v3"
)

var FuncScheduler = cron.New()

func ScheduleJobs(ctx context.Context) {
	if err := job.NewJob(ctx, "Delete Old Config Changes", "@every 24h", DeleteOldConfigChanges).
		RunOnStart().AddToScheduler(FuncScheduler); err != nil {
		logger.Errorf("Failed to schedule sync jobs for team component: %v", err)
	}

	if err := job.NewJob(ctx, "Delete Old Config Analyses", "@every 24h", DeleteOldConfigAnalysis).
		RunOnStart().AddToScheduler(FuncScheduler); err != nil {
		logger.Errorf("Failed to schedule sync jobs for team component: %v", err)
	}

	if err := job.NewJob(ctx, "Cleanup Config Items", "@every 24h", CleanupConfigItems).
		RunOnStart().AddToScheduler(FuncScheduler); err != nil {
		logger.Errorf("Failed to schedule sync jobs for team component: %v", err)
	}

	if err := job.NewJob(ctx, "Process Change Retention Rules", "@every 1h", ProcessChangeRetentionRules).
		RunOnStart().AddToScheduler(FuncScheduler); err != nil {
		logger.Errorf("Failed to schedule sync jobs for team component: %v", err)
	}

	if api.UpstreamConfig.Valid() {
		// Syncs config_items to upstream in real-time
		if err := StartUpstreamConsumer(ctx); err != nil {
			logger.Fatalf("Failed to start event consumer: %v", err)
		}

		for _, j := range UpstreamJobs {
			var job = j
			job.Context = ctx
			if err := job.AddToScheduler(FuncScheduler); err != nil {
				logger.Fatalf(err.Error())
			}
		}
	}

	FuncScheduler.Start()
}
