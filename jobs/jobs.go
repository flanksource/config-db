package jobs

import (
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/job"
	"github.com/robfig/cron/v3"
)

const JobResourceType = "configs"

var FuncScheduler = cron.New()

// ScheduleJobs schedules the given job
func ScheduleJob(ctx context.Context, j *job.Job) {
	j.Context = ctx
	j.AddToScheduler(FuncScheduler)
}

func ScheduleJobs(ctx context.Context) {
	for _, j := range cleanupJobs {
		var job = j
		job.Context = ctx
		if err := job.AddToScheduler(FuncScheduler); err != nil {
			logger.Fatalf(err.Error())
		}
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
