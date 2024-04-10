package scrapers

import (
	gocontext "context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers/kubernetes"
	"github.com/flanksource/duty/job"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"github.com/samber/lo"
	"github.com/sethvargo/go-retry"
)

var (
	DefaultSchedule string

	scrapeJobScheduler = cron.New()
	scrapeJobs         sync.Map
)

func SyncScrapeConfigs(sc api.ScrapeContext) {
	j := &job.Job{
		Name:       "ConfigScraperSync",
		Context:    sc.DutyContext(),
		Schedule:   "@every 10m",
		Singleton:  true,
		JobHistory: true,
		Retention:  job.RetentionFew,
		RunNow:     true,
		Fn: func(jr job.JobRuntime) error {
			scraperConfigsDB, err := db.GetScrapeConfigsOfAgent(sc, uuid.Nil)
			if err != nil {
				logger.Fatalf("error getting configs from database: %v", err)
			}

			logger.Infof("Starting %d scrapers", len(scraperConfigsDB))
			for _, scraper := range scraperConfigsDB {
				_scraper, err := v1.ScrapeConfigFromModel(scraper)
				if err != nil {
					jr.History.AddErrorf("Error parsing config scraper[%s]: %v", scraper.ID, err)
					continue
				}

				if err := SyncScrapeJob(sc.WithScrapeConfig(&_scraper)); err != nil {
					jr.History.AddErrorf("Error syncing scrape job[%s]: %v", scraper.ID, err)
					continue
				}

				jr.History.SuccessCount += 1
			}
			return nil
		},
	}
	if err := j.AddToScheduler(scrapeJobScheduler); err != nil {
		logger.Fatalf("error scheduling ConfigScraperSync job: %v", err)
	}
}

func watchKubernetesEventsWithRetry(ctx api.ScrapeContext, config v1.Kubernetes) {
	const (
		timeout                 = time.Minute // how long to keep retrying before we reset and retry again
		exponentialBaseDuration = time.Second
	)

	for {
		backoff := retry.WithMaxDuration(timeout, retry.NewExponential(exponentialBaseDuration))
		err := retry.Do(ctx, backoff, func(ctxt gocontext.Context) error {
			ctx := ctxt.(api.ScrapeContext)
			if err := kubernetes.WatchEvents(ctx, config); err != nil {
				return retry.RetryableError(err)
			}

			return nil
		})

		logger.Errorf("Failed to watch kubernetes events. name=%s namespace=%s cluster=%s: %v", config.Name, config.Namespace, config.ClusterName, err)
	}
}

func SyncScrapeJob(sc api.ScrapeContext) error {
	id := sc.ScrapeConfig().GetPersistedID().String()

	var existingJob *job.Job
	if j, ok := scrapeJobs.Load(id); ok {
		existingJob = j.(*job.Job)
	}

	if sc.ScrapeConfig().GetDeletionTimestamp() != nil || sc.ScrapeConfig().Spec.Schedule == "@never" {
		DeleteScrapeJob(id)
		return nil
	}

	if existingJob == nil {
		newScrapeJob(sc)
		return nil
	}

	existingScraper := existingJob.Context.Value("scraper")
	if existingScraper != nil && !reflect.DeepEqual(existingScraper.(*v1.ScrapeConfig).Spec, sc.ScrapeConfig().Spec) {
		sc.DutyContext().Debugf("Rescheduling %s scraper with updated specs", sc.ScrapeConfig().Name)
		DeleteScrapeJob(id)
		newScrapeJob(sc)
	}

	return nil
}

func newScrapeJob(sc api.ScrapeContext) *job.Job {
	schedule, _ := lo.Coalesce(sc.ScrapeConfig().Spec.Schedule, DefaultSchedule)
	j := &job.Job{
		Name:         "Scraper",
		Context:      sc.DutyContext().WithObject(sc.ScrapeConfig().ObjectMeta).WithAnyValue("scraper", sc.ScrapeConfig()),
		Schedule:     schedule,
		Singleton:    true,
		JobHistory:   true,
		Retention:    job.RetentionBalanced,
		ResourceID:   sc.ScrapeConfig().GetPersistedID().String(),
		ResourceType: job.ResourceTypeScraper,
		RunNow:       true,
		ID:           fmt.Sprintf("%s/%s", sc.ScrapeConfig().Namespace, sc.ScrapeConfig().Name),
		Fn: func(jr job.JobRuntime) error {
			results, err := RunScraper(sc.WithJobHistory(jr.History))
			if err != nil {
				jr.History.AddError(err.Error())
				return fmt.Errorf("error running scraper[%s]: %w", sc.ScrapeConfig().Name, err)
			}
			jr.History.SuccessCount = len(results)
			return nil
		},
	}
	scrapeJobs.Store(sc.ScrapeConfig().GetPersistedID().String(), j)
	if err := j.AddToScheduler(scrapeJobScheduler); err != nil {
		logger.Errorf("[%s] failed to schedule %v", j.Name, err)
	}

	for _, config := range sc.ScrapeConfig().Spec.Kubernetes {
		go watchKubernetesEventsWithRetry(sc, config)
		k8sWatchJob := ConsumeKubernetesWatchEventsJobFunc(sc, config)
		if err := k8sWatchJob.AddToScheduler(scrapeJobScheduler); err != nil {
			logger.Fatalf("failed to schedule kubernetes watch event consumer job: %v", err)
		}
	}
	return j
}

// ConsumeKubernetesWatchEventsJobFunc returns a job that consumes kubernetes watch events
// for the given config of the scrapeconfig.
func ConsumeKubernetesWatchEventsJobFunc(sc api.ScrapeContext, config v1.Kubernetes) *job.Job {
	scrapeConfig := *sc.ScrapeConfig()
	return &job.Job{
		Name:         "ConsumeKubernetesWatchEvents",
		Context:      sc.DutyContext().WithObject(sc.ScrapeConfig().ObjectMeta),
		JobHistory:   true,
		Singleton:    true,
		Retention:    job.RetentionFew,
		RunNow:       true,
		Schedule:     "@every 15s",
		ResourceID:   string(scrapeConfig.GetUID()),
		ID:           fmt.Sprintf("%s/%s", sc.ScrapeConfig().Namespace, sc.ScrapeConfig().Name),
		ResourceType: job.ResourceTypeScraper,
		Fn: func(ctx job.JobRuntime) error {
			ch, ok := kubernetes.WatchEventBuffers[config.Hash()]
			if !ok {
				return fmt.Errorf("no watcher found for config (scrapeconfig: %s) %s", scrapeConfig.GetUID(), config.Hash())
			}
			events, _, _, _ := lo.Buffer(ch, len(ch))

			cc := api.NewScrapeContext(ctx.Context).WithScrapeConfig(&scrapeConfig).WithJobHistory(ctx.History)
			results, err := RunK8IncrementalScraper(cc, config, events)
			if err != nil {
				return err
			}

			if err := SaveResults(cc, results); err != nil {
				return fmt.Errorf("failed to save results: %w", err)
			}

			for i := range results {
				if results[i].Error != nil {
					ctx.History.AddError(results[i].Error.Error())
				} else {
					ctx.History.SuccessCount++
				}
			}

			return nil
		},
	}
}

func DeleteScrapeJob(id string) {
	if j, ok := scrapeJobs.Load(id); ok {
		existingJob := j.(*job.Job)
		existingJob.Unschedule()
		scrapeJobs.Delete(id)
	}
}
