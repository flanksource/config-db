package scrapers

import (
	gocontext "context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	pq "github.com/emirpasic/gods/queues/priorityqueue"
	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty/context"
	dutyEcho "github.com/flanksource/duty/echo"
	"github.com/flanksource/duty/job"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"github.com/samber/lo"
	"github.com/sethvargo/go-retry"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/semaphore"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

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
				existing = append(existing, consumeKubernetesWatchEventsJobKey(m.ID.String()))
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

		if err != nil {
			if errors.Is(err, gocontext.DeadlineExceeded) || errors.Is(err, gocontext.Canceled) {
				return
			}

			logger.Errorf("failed to watch kubernetes events. cluster=%s: %v", config.ClusterName, err)
		}
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

	if sc.PropertyOn(false, "watch.disable") {
		return nil
	}

	for _, config := range sc.ScrapeConfig().Spec.Kubernetes {
		if len(config.Watch) == 0 {
			config.Watch = v1.DefaultWatchKinds
		}

		go watchKubernetesEventsWithRetry(sc, config)

		if err := kubernetes.WatchResources(sc, config); err != nil {
			return fmt.Errorf("failed to watch kubernetes resources: %v", err)
		}

		watchConsumerJob := ConsumeKubernetesWatchJobFunc(sc, config)
		if err := watchConsumerJob.AddToScheduler(scrapeJobScheduler); err != nil {
			return fmt.Errorf("failed to schedule kubernetes watch consumer job: %v", err)
		}
		scrapeJobs.Store(consumeKubernetesWatchEventsJobKey(sc.ScraperID()), watchConsumerJob)
	}

	return nil
}

func consumeKubernetesWatchEventsJobKey(id string) string {
	return id + "-consume-kubernetes-watch-events"
}

// ConsumeKubernetesWatchJobFunc returns a job that consumes kubernetes watch events
// for the given config of the scrapeconfig.
func ConsumeKubernetesWatchJobFunc(sc api.ScrapeContext, config v1.Kubernetes) *job.Job {
	scrapeConfig := *sc.ScrapeConfig()
	return &job.Job{
		Name:         "ConsumeKubernetesWatch",
		Context:      sc.DutyContext().WithObject(sc.ScrapeConfig().ObjectMeta),
		JobHistory:   true,
		Singleton:    true,
		Retention:    job.RetentionFew,
		Schedule:     "@every 15s",
		ResourceID:   string(scrapeConfig.GetUID()),
		ID:           fmt.Sprintf("%s/%s", sc.ScrapeConfig().Namespace, sc.ScrapeConfig().Name),
		ResourceType: job.ResourceTypeScraper,
		Fn: func(ctx job.JobRuntime) error {
			var queue *pq.Queue
			if q, ok := kubernetes.WatchQueue.Load(config.Hash()); !ok {
				return fmt.Errorf("no watch queue found for config (scrapeconfig: %s) %s", scrapeConfig.GetUID(), config.Hash())
			} else {
				queue = q.(*pq.Queue)
			}

			var events []v1.KubernetesEvent
			var objs []*unstructured.Unstructured
			var count int
			for {
				val, more := queue.Dequeue()
				if !more {
					break
				}

				// On the off chance the queue is populated faster than it's consumed
				// and to keep each run short, we set a limit.
				if count > kubernetes.BufferSize {
					break
				}

				queueItem, ok := val.(*kubernetes.QueueItem)
				if !ok {
					return fmt.Errorf("unexpected item in the priority queue: %T", val)
				}

				if queueItem.Event != nil {
					events = append(events, *queueItem.Event)
				} else if queueItem.Obj != nil {
					objs = append(objs, queueItem.Obj)
				}
			}

			// NOTE: The resource watcher can return multiple objects for the same NEW resource.
			// Example: if a new pod is created, we'll get that pod object multiple times for different events.
			// All those resource objects are seen as distinct new config items.
			// Hence, we need to use the latest one otherwise saving fails
			// as we'll be trying to BATCH INSERT multiple config items with the same id.
			//
			// In the process, we will lose diff changes though.
			// If diff changes are necessary, then we can split up the results in such
			// a way that no two objects in a batch have the same id.
			objs = dedup(objs)
			if err := consumeResources(ctx, scrapeConfig, config, objs); err != nil {
				ctx.History.AddErrorf("failed to consume resources: %v", err)
				return err
			}

			return consumeWatchEvents(ctx, scrapeConfig, config, events)
		},
	}
}

func consumeWatchEvents(ctx job.JobRuntime, scrapeConfig v1.ScrapeConfig, config v1.Kubernetes, events []v1.KubernetesEvent) error {
	cc := api.NewScrapeContext(ctx.Context).WithScrapeConfig(&scrapeConfig).WithJobHistory(ctx.History).AsIncrementalScrape()
	cc.Context = cc.Context.WithoutName().WithName(fmt.Sprintf("%s/%s", ctx.GetNamespace(), ctx.GetName()))
	results, err := RunK8IncrementalScraper(cc, config, events)
	if err != nil {
		return err
	}

	if summary, err := db.SaveResults(cc, results); err != nil {
		return fmt.Errorf("failed to save results: %w", err)
	} else {
		ctx.History.AddDetails("scrape_summary", summary)
	}

	for i := range results {
		if results[i].Error != nil {
			ctx.History.AddError(results[i].Error.Error())
		} else {
			ctx.History.SuccessCount++
		}
	}

	return nil
}

func consumeResources(ctx job.JobRuntime, scrapeConfig v1.ScrapeConfig, config v1.Kubernetes, objs []*unstructured.Unstructured) error {
	cc := api.NewScrapeContext(ctx.Context).WithScrapeConfig(&scrapeConfig).WithJobHistory(ctx.History).AsIncrementalScrape()
	cc.Context = cc.Context.WithoutName().WithName(fmt.Sprintf("watch[%s/%s]", cc.GetNamespace(), cc.GetName()))
	results, err := RunK8sObjectsScraper(cc, config, objs)
	if err != nil {
		return err
	}

	if summary, err := db.SaveResults(cc, results); err != nil {
		return fmt.Errorf("failed to save %d results: %w", len(results), err)
	} else {
		ctx.History.AddDetails("scrape_summary", summary)
	}

	for i := range results {
		if results[i].Error != nil {
			ctx.History.AddError(results[i].Error.Error())
		} else {
			ctx.History.SuccessCount++
		}
	}

	_deleteCh, ok := kubernetes.DeleteResourceBuffer.Load(config.Hash())
	if !ok {
		return fmt.Errorf("no resource watcher channel found for config (scrapeconfig: %s)", config.Hash())
	}
	deleteChan := _deleteCh.(chan string)

	if len(deleteChan) > 0 {
		deletedResourcesIDs, _, _, _ := lo.Buffer(deleteChan, len(deleteChan))

		total, err := db.SoftDeleteConfigItems(ctx.Context, deletedResourcesIDs...)
		if err != nil {
			return fmt.Errorf("failed to delete %d resources: %w", len(deletedResourcesIDs), err)
		} else if total != len(deletedResourcesIDs) {
			ctx.GetSpan().SetAttributes(attribute.StringSlice("deletedResourcesIDs", deletedResourcesIDs))
			if cc.PropertyOn(false, "log.missing") {
				ctx.Logger.Warnf("attempted to delete %d resources but only deleted %d", len(deletedResourcesIDs), total)
			}
		}

		ctx.History.SuccessCount += total
	}

	return nil
}

func dedup(objs []*unstructured.Unstructured) []*unstructured.Unstructured {
	var output []*unstructured.Unstructured
	seen := make(map[types.UID]struct{})

	// Iterate in reverse, cuz we want the latest
	for i := len(objs) - 1; i >= 0; i-- {
		if _, ok := seen[objs[i].GetUID()]; ok {
			continue
		}

		seen[objs[i].GetUID()] = struct{}{}
		output = append(output, objs[i])
	}

	return output
}

func DeleteScrapeJob(id string) {
	logger.Debugf("deleting scraper job for %s", id)

	if j, ok := scrapeJobs.Load(id); ok {
		existingJob := j.(*job.Job)
		existingJob.Unschedule()
		scrapeJobs.Delete(id)
	}

	if j, ok := scrapeJobs.Load(consumeKubernetesWatchEventsJobKey(id)); ok {
		existingJob := j.(*job.Job)
		existingJob.Unschedule()
		scrapeJobs.Delete(id)
	}
}
