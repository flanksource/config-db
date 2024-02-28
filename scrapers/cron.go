package scrapers

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/job"
	"github.com/robfig/cron/v3"
)

var (
	cronManger        *cron.Cron
	DefaultSchedule   string
	cronIDFunctionMap map[string]cron.EntryID

	// ConcurrentJobLocks keeps track of the currently running jobs.
	ConcurrentJobLocks sync.Map

	scrapeJobScheduler = cron.New()
	scrapeJobs         sync.Map
)

func SyncScrapeJob(sc api.ScrapeContext) error {
	id := sc.ScrapeConfig().GetPersistedID().String()

	var existingJob *job.Job
	if j, ok := scrapeJobs.Load(id); ok {
		existingJob = j.(*job.Job)
	}

	if sc.ScrapeConfig().GetDeletionTimestamp() != nil || sc.ScrapeConfig().Spec.Schedule == "@never" {
		existingJob.Unschedule()
		scrapeJobs.Delete(id)
		return nil
	}

	if existingJob == nil {
		newScrapeJob(sc)
		return nil
	}

	existingScraper := existingJob.Context.Value("scraper")
	if existingScraper != nil && !reflect.DeepEqual(existingScraper.(*v1.ScrapeConfig).Spec, sc.ScrapeConfig()) {
		sc.DutyContext().Debugf("Rescheduling %s scraper with updated specs", sc.ScrapeConfig().Name)
		existingJob.Unschedule()
		newScrapeJob(sc)
	}
	return nil
}

func newScrapeJob(sc api.ScrapeContext) *job.Job {
	j := &job.Job{
		Name:         "Scraper",
		Context:      sc.DutyContext().WithObject(sc.ScrapeConfig().ObjectMeta).WithAnyValue("scraper", sc.ScrapeConfig()),
		Schedule:     sc.ScrapeConfig().Spec.Schedule,
		Singleton:    true,
		JobHistory:   true,
		Retention:    job.RetentionDay,
		ResourceID:   sc.ScrapeConfig().GetPersistedID().String(),
		ResourceType: job.ResourceTypeScraper,
		RunNow:       true,
		ID:           fmt.Sprintf("%s/%s", sc.ScrapeConfig().Namespace, sc.ScrapeConfig().Name),
		Fn: func(jr job.JobRuntime) error {
			results, err := RunScraper(sc.WithJobHistory(jr.History))
			if err != nil {
				jr.History.AddError(err.Error())
				return err
			}
			//jr.Job.JobHistory
			_ = results
			return nil
		},
	}
	scrapeJobs.Store(sc.ScrapeConfig().GetPersistedID().String(), j)
	if err := j.AddToScheduler(scrapeJobScheduler); err != nil {
		logger.Errorf("[%s] failed to schedule %v", j.Name, err)
	}
	return j
}

func DeleteScrapeJob(id string) {
	if j, ok := scrapeJobs.Load(id); ok {
		existingJob := j.(*job.Job)
		existingJob.Unschedule()
		scrapeJobs.Delete(id)
	}
}

// AtomicRunner wraps the given function, identified by a unique ID,
// with a mutex so that the function executes only once at a time, preventing concurrent executions.
func AtomicRunner(id string, fn func()) func() {
	return func() {
		val, _ := ConcurrentJobLocks.LoadOrStore(id, &sync.Mutex{})
		lock, ok := val.(*sync.Mutex)
		if !ok {
			logger.Warnf("expected mutex but got %T for scraper(id=%s)", lock, id)
			return
		}

		if !lock.TryLock() {
			logger.Debugf("scraper (id=%s) is already running. skipping this run ...", id)
			return
		}
		defer lock.Unlock()

		fn()
	}
}

func init() {
	cronIDFunctionMap = make(map[string]cron.EntryID)
	cronManger = cron.New()
	cronManger.Start()
}
