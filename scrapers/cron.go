package scrapers

import (
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty/context"
	dutyEcho "github.com/flanksource/duty/echo"
	"github.com/flanksource/duty/job"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"github.com/samber/lo"
	"golang.org/x/sync/semaphore"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers/kubernetes"
)

var (
	DefaultSchedule    string
	MinScraperSchedule = time.Second * 29 // 29 to account for any ms errors

	scrapeJobScheduler = cron.New()
	scrapeJobs         sync.Map
)

const scrapeJobName = "Scraper"

func init() {
	dutyEcho.RegisterCron(scrapeJobScheduler)
}

var (
	globalScraperSempahore *semaphore.Weighted
	scraperTypeSemaphores  map[string]*semaphore.Weighted

	// Total number of scraper jobs allowed to run concurrently
	ScraperConcurrency = 12
)

func Stop() {
	scrapeJobScheduler.Stop()
}

func SyncScrapeConfigs(sc context.Context) {
	if globalScraperSempahore == nil {
		globalScraperSempahore = semaphore.NewWeighted(int64(sc.Properties().Int("scraper.concurrency", ScraperConcurrency)))
	}

	if scraperTypeSemaphores == nil {
		scraperTypeSemaphores = map[string]*semaphore.Weighted{
			"aws":            semaphore.NewWeighted(int64(sc.Properties().Int("scraper.aws.concurrency", 2))),
			"azure":          semaphore.NewWeighted(int64(sc.Properties().Int("scraper.azure.concurrency", 2))),
			"azuredevops":    semaphore.NewWeighted(int64(sc.Properties().Int("scraper.azuredevops.concurrency", 5))),
			"file":           semaphore.NewWeighted(int64(sc.Properties().Int("scraper.file.concurrency", 10))),
			"gcp":            semaphore.NewWeighted(int64(sc.Properties().Int("scraper.gcp.concurrency", 2))),
			"githubactions":  semaphore.NewWeighted(int64(sc.Properties().Int("scraper.githubactions.concurrency", 5))),
			"http":           semaphore.NewWeighted(int64(sc.Properties().Int("scraper.http.concurrency", 10))),
			"kubernetes":     semaphore.NewWeighted(int64(sc.Properties().Int("scraper.kubernetes.concurrency", 3))),
			"kubernetesfile": semaphore.NewWeighted(int64(sc.Properties().Int("scraper.kubernetesfile.concurrency", 3))),
			"slack":          semaphore.NewWeighted(int64(sc.Properties().Int("scraper.slack.concurrency", 5))),
			"sql":            semaphore.NewWeighted(int64(sc.Properties().Int("scraper.sql.concurrency", 10))),
			"terraform":      semaphore.NewWeighted(int64(sc.Properties().Int("scraper.terraform.concurrency", 10))),
			"trivy":          semaphore.NewWeighted(int64(sc.Properties().Int("scraper.trivy.concurrency", 1))),
		}
	}

	DefaultSchedule = sc.Properties().String("scrapers.default.schedule", DefaultSchedule)
	j := &job.Job{
		Name:       "ConfigScraperSync",
		Context:    sc,
		Schedule:   "@every 10m",
		Singleton:  true,
		JobHistory: true,
		Retention:  job.RetentionFew,
		RunNow:     true,
		Fn: func(jr job.JobRuntime) error {
			scraperConfigsDB, err := db.GetScrapeConfigsOfAgent(jr.Context, uuid.Nil)
			if err != nil {
				return fmt.Errorf("error getting configs from database: %v", err)
			}

			for _, scraper := range scraperConfigsDB {
				_scraper, err := v1.ScrapeConfigFromModel(scraper)
				if err != nil {
					jr.History.AddErrorf("Error parsing config scraper[%s]: %v", scraper.ID, err)
					continue
				}

				scrapeCtx := api.NewScrapeContext(sc).WithScrapeConfig(&_scraper)
				if err := SyncScrapeJob(scrapeCtx); err != nil {
					jr.History.AddErrorf("Error syncing scrape job[%s]: %v", scraper.ID, err)

					{
						// also, add to the job's history
						jobHistory := models.NewJobHistory(scrapeCtx.Logger, scrapeJobName, job.ResourceTypeScraper, scraper.ID.String())
						jobHistory.Start()
						jobHistory.AddError(err)
						if err := jobHistory.End().Persist(scrapeCtx.DB()); err != nil {
							logger.Errorf("error persisting job history: %v", err)
						}
					}

					continue
				}

				jr.History.SuccessCount += 1
			}

			// cleanup dangling scraper jobs
			var existing []string
			for _, m := range scraperConfigsDB {
				existing = append(existing, m.ID.String())
				existing = append(existing, consumeKubernetesWatchJobKey(m.ID.String()))
			}

			scrapeJobs.Range(func(_key, value any) bool {
				key := _key.(string)
				if collections.Contains(existing, key) {
					return true
				}

				jr.Logger.V(0).Infof("found a dangling scraper job: %s", key)
				DeleteScrapeJob(key)
				return true
			})

			return nil
		},
	}
	if err := j.AddToScheduler(scrapeJobScheduler); err != nil {
		logger.Fatalf("error scheduling ConfigScraperSync job: %v", err)
	}
}

func SyncScrapeJob(sc api.ScrapeContext) error {
	id := sc.ScraperID()

	var existingJob *job.Job
	if j, ok := scrapeJobs.Load(id); ok {
		existingJob = j.(*job.Job)
	}

	if sc.ScrapeConfig().GetDeletionTimestamp() != nil || sc.ScrapeConfig().Spec.Schedule == "@never" {
		DeleteScrapeJob(id)
		return nil
	}

	if existingJob == nil {
		return scheduleScraperJob(sc)
	}

	existingScraper := existingJob.Context.Value("scraper")
	if existingScraper != nil && !reflect.DeepEqual(existingScraper.(*v1.ScrapeConfig).Spec, sc.ScrapeConfig().Spec) {
		sc.DutyContext().Debugf("Rescheduling %s scraper with updated specs", sc.ScrapeConfig().Name)
		DeleteScrapeJob(id)
		return scheduleScraperJob(sc)
	}

	return nil
}

func newScraperJob(sc api.ScrapeContext) *job.Job {
	schedule, _ := lo.Coalesce(sc.Properties().String(fmt.Sprintf("scraper.%s.schedule", sc.ScrapeConfig().UID), sc.ScrapeConfig().Spec.Schedule), DefaultSchedule)
	minScheduleAllowed := sc.Properties().Duration(fmt.Sprintf("scraper.%s.schedule.min", sc.ScrapeConfig().Type()), MinScraperSchedule)

	// Attempt to get a fixed interval from the schedule.
	// NOTE: Only works for fixed interval schedules.
	parsedSchedule, err := cron.ParseStandard(schedule)
	if err == nil {
		interval := time.Until(parsedSchedule.Next(time.Now()))
		if interval < minScheduleAllowed {
			newSchedule := fmt.Sprintf("@every %ds", int(minScheduleAllowed.Seconds()))
			sc.Logger.Infof("[%s] scraper schedule %s too short, using minimum allowed %q", sc.ScrapeConfig().Name, schedule, newSchedule)

			schedule = newSchedule
		}
	}

	semaphores := []*semaphore.Weighted{globalScraperSempahore}
	if s, ok := scraperTypeSemaphores[sc.ScrapeConfig().Type()]; ok {
		// Only when the scraper type is known we add the per type semaphore
		semaphores = append([]*semaphore.Weighted{s}, semaphores...)
	}

	return &job.Job{
		Name:         scrapeJobName,
		Context:      sc.DutyContext().WithObject(sc.ScrapeConfig().ObjectMeta).WithAnyValue("scraper", sc.ScrapeConfig()),
		Schedule:     schedule,
		Singleton:    true,
		JobHistory:   true,
		Semaphores:   semaphores,
		RunNow:       sc.PropertyOn(false, "runNow"),
		Retention:    job.RetentionBalanced,
		ResourceID:   sc.ScraperID(),
		ResourceType: job.ResourceTypeScraper,
		ID:           fmt.Sprintf("%s/%s", sc.ScrapeConfig().Namespace, sc.ScrapeConfig().Name),
		Fn: func(jr job.JobRuntime) error {
			output, err := RunScraper(sc.WithJobHistory(jr.History))
			if err != nil {
				jr.History.AddError(err.Error())
				return fmt.Errorf("error running scraper[%s]: %w", sc.ScrapeConfig().Name, err)
			}
			jr.History.SuccessCount = output.Total
			jr.History.AddDetails("scrape_summary", output.Summary)
			return nil
		},
	}
}

func scheduleScraperJob(sc api.ScrapeContext) error {
	j := newScraperJob(sc)

	if sc.PropertyOn(false, "disable") {
		return nil
	}

	scrapeJobs.Store(sc.ScraperID(), j)
	if err := j.AddToScheduler(scrapeJobScheduler); err != nil {
		return fmt.Errorf("[%s] failed to schedule %v", j.Name, err)
	}

	for _, config := range sc.ScrapeConfig().Spec.Kubernetes {
		if sc.PropertyOn(false, "watch.disable") {
			config.Watch = []v1.KubernetesResourceToWatch{}
		} else if len(config.Watch) == 0 {
			config.Watch = v1.DefaultWatchKinds
		}

		// always watch for event objects
		config.Watch = v1.AddEventResourceToWatch(config.Watch)

		queue, err := kubernetes.WatchResources(sc, config)
		if err != nil {
			return fmt.Errorf("failed to watch kubernetes resources: %v", err)
		}

		if config.Kubeconfig != nil {
			c, err := sc.WithKubeconfig(*config.Kubeconfig)
			if err != nil {
				return fmt.Errorf("failed to apply custom kubeconfig: %w", err)
			}
			sc.Context = *c

		}
		watchConsumerJob := ConsumeKubernetesWatchJobFunc(sc, config, queue)
		if err := watchConsumerJob.AddToScheduler(scrapeJobScheduler); err != nil {
			return fmt.Errorf("failed to schedule kubernetes watch consumer job: %v", err)
		}
		scrapeJobs.Store(consumeKubernetesWatchJobKey(sc.ScraperID()), watchConsumerJob)
	}

	return nil
}

func DeleteScrapeJob(id string) {
	logger.Debugf("deleting scraper job for %s", id)

	if j, ok := scrapeJobs.Load(id); ok {
		existingJob := j.(*job.Job)
		existingJob.Unschedule()
		scrapeJobs.Delete(id)
	}

	if j, ok := scrapeJobs.Load(consumeKubernetesWatchJobKey(id)); ok {
		existingJob := j.(*job.Job)
		existingJob.Unschedule()
		scrapeJobs.Delete(id)
	}
}
