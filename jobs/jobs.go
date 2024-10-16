package jobs

import (
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	"github.com/flanksource/duty/context"
	dutyEcho "github.com/flanksource/duty/echo"
	"github.com/flanksource/duty/job"
	"github.com/robfig/cron/v3"
)

const JobResourceType = "configs"

var FuncScheduler = cron.New()

func init() {
	dutyEcho.RegisterCron(FuncScheduler)
}

// ScheduleJobs schedules the given job
func ScheduleJob(ctx context.Context, j *job.Job) error {
	j.Context = ctx
	return j.AddToScheduler(FuncScheduler)
}

func ScheduleJobs(ctx context.Context) {
	for _, j := range cleanupJobs {
		job := j
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
		for _, j := range UpstreamJobs {
			job := j
			job.Context = ctx
			if err := job.AddToScheduler(FuncScheduler); err != nil {
				logger.Fatalf(err.Error())
			}
		}
	}
}

func Stop() {
	FuncScheduler.Stop()
}
