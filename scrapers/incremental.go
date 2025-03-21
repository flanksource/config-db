package scrapers

import (
	gocontext "context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers/kubernetes"
	"github.com/flanksource/config-db/utils/kube"
	"github.com/flanksource/duty/job"
	"github.com/samber/lo"
	"github.com/sethvargo/go-retry"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var (
	consumeLagBuckets           = []float64{500, 1_000, 3_000, 5_000, 10_000, 15_000, 30_000, 60_000, 100_000, 150_000, 300_000, 600_000}
	involvedObjectsFetchBuckets = []float64{500, 1_000, 3_000, 5_000, 10_000, 15_000, 30_000, 60_000, 100_000, 150_000, 300_000, 600_000}
)

func consumeKubernetesWatchJobKey(id string) string {
	return id + "-consume-kubernetes-watch"
}

// ConsumeKubernetesWatchJobFunc returns a job that consumes kubernetes objects received from shared informers
// for the given config of the scrapeconfig.
func ConsumeKubernetesWatchJobFunc(sc api.ScrapeContext, config v1.Kubernetes, queue *collections.Queue[*kubernetes.QueueItem]) *job.Job {
	return &job.Job{
		Name:         "ConsumeKubernetesWatch",
		Context:      sc.DutyContext().WithObject(sc.ScrapeConfig().ObjectMeta),
		JobHistory:   true,
		Singleton:    true,
		Retention:    job.RetentionFailed,
		Schedule:     "@every 3s",
		ResourceID:   string(sc.ScrapeConfig().GetUID()),
		ID:           fmt.Sprintf("%s/%s", sc.ScrapeConfig().Namespace, sc.ScrapeConfig().Name),
		ResourceType: job.ResourceTypeScraper,
		Fn: func(jobCtx job.JobRuntime) error {
			plugins, err := db.LoadAllPlugins(jobCtx.Context)
			if err != nil {
				return fmt.Errorf("failed to load plugins: %w", err)
			}

			config := config.DeepCopy()
			config.BaseScraper = config.BaseScraper.ApplyPlugins(plugins...)

			sc := sc.WithScrapeConfig(sc.ScrapeConfig(), plugins...).AsIncrementalScrape()

			// Use temp cache if it already exists for scraper
			if tc, exists := TempCacheStore[sc.ScraperID()]; exists {
				sc = sc.WithTempCache(tc)
			}

			sc.Context = sc.WithKubernetes(config.KubernetesConnection)

			var (
				objs           []unstructured.Unstructured
				deletedObjects []unstructured.Unstructured
				queuedTime     = map[string]time.Time{}

				objectsFromEvents []v1.InvolvedObject
			)

			for {
				queueItem, more := queue.Dequeue()
				if !more {
					break
				}

				// On the off chance the queue is populated faster than it's consumed
				// and to keep each run short, we set a limit.
				if len(objs) > kubernetes.BufferSize {
					break
				}

				obj := queueItem.Obj
				queuedTime[string(obj.GetUID())] = queueItem.Timestamp

				if queueItem.Operation == kubernetes.QueueItemOperationDelete {
					deletedObjects = append(deletedObjects, obj)
					continue
				}

				objs = append(objs, obj)

				if obj.GetKind() == "Event" {
					// For events, we want to re-scrape their involved objects as well
					involvedObjectRaw, ok, _ := unstructured.NestedMap(obj.Object, "involvedObject")
					if !ok {
						continue
					}

					if _, ok := kubernetes.IgnoredConfigsCache.Load(involvedObjectRaw["uid"]); ok {
						continue
					}

					var involvedObject v1.InvolvedObject
					if err := runtime.DefaultUnstructuredConverter.FromUnstructured(involvedObjectRaw, &involvedObject); err != nil {
						return fmt.Errorf("failed to unmarshal endpoint (%s/%s): %w", obj.GetUID(), obj.GetName(), err)
					}

					// If the involvedObject does not exist in db, we requeue the event after 30s
					// to prevent foreign key errors
					ci, err := sc.TempCache().Get(sc, string(involvedObject.UID), api.IgnoreNotFound)
					if err != nil {
						return fmt.Errorf("failed to fetch from cache: %w", err)
					} else if ci == nil {
						// Remove event object from objects list
						objs = lo.DropRight(objs, 1)

						// Re-enqueue the event with a delay but only if it wasn't previously enqueued
						// else the event gets recycled indefinitely
						if queueItem.Operation != kubernetes.QueueItemOperationReEnqueue {
							queue.EnqueueWithDelay(kubernetes.NewQueueItem(obj, kubernetes.QueueItemOperationReEnqueue), 30*time.Second)
						}
					}

					objectsFromEvents = append(objectsFromEvents, involvedObject)
				}
			}

			objectsFromEvents = lo.UniqBy(objectsFromEvents, func(obj v1.InvolvedObject) string { return string(obj.UID) })
			if len(objectsFromEvents) > 0 {
				go func(eventInvolvedObjs []v1.InvolvedObject) {
					start := time.Now()

					cc := api.NewScrapeContext(jobCtx.Context).WithScrapeConfig(sc.ScrapeConfig()).WithJobHistory(jobCtx.History).AsIncrementalScrape()
					cc.Context = cc.Context.WithoutName().WithName(fmt.Sprintf("watch[%s/%s]", cc.GetNamespace(), cc.GetName()))

					backoff := retry.WithMaxRetries(3, retry.NewExponential(time.Second))
					err := retry.Do(jobCtx, backoff, func(_ctx gocontext.Context) error {
						objs, err := kube.FetchInvolvedObjects(cc, eventInvolvedObjs)
						if err != nil {
							return retry.RetryableError(err)
						}

						percent := float64(len(objs)) / float64(len(eventInvolvedObjs))
						if percent < 0.5 {
							// smells like a bug
							jobCtx.Logger.V(3).Infof("requested %d involved objects but fetched only %d", len(eventInvolvedObjs), len(objs))
						}

						// we put these involved objects back into the queue
						for _, obj := range objs {
							queue.Enqueue(kubernetes.NewQueueItem(*obj, kubernetes.QueueItemOperationReEnqueue))
							jobCtx.Histogram("involved_objects_enqueue", involvedObjectsFetchBuckets, "scraper_id", cc.ScraperID()).
								Record(time.Duration(time.Since(start).Milliseconds()))
						}

						return nil
					})
					if err != nil {
						jobCtx.History.AddErrorf("failed to get invovled objects: %v", err)
					}
				}(objectsFromEvents)
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
			if err := consumeResources(jobCtx, *sc.ScrapeConfig(), *config, objs, deletedObjects); err != nil {
				jobCtx.History.AddErrorf("failed to consume resources: %v", err)
				return err
			}

			for _, obj := range objs {
				queuedtime, ok := queuedTime[string(obj.GetUID())]
				if !ok {
					jobCtx.Warnf("found object (%s/%s/%s) with zero queuedTime", obj.GetNamespace(), obj.GetName(), obj.GetUID())
					continue
				}

				lag := time.Since(queuedtime)
				jobCtx.Histogram("informer_consume_lag", consumeLagBuckets,
					"scraper", sc.ScraperID(),
					"kind", obj.GetKind(),
				).Record(time.Duration(lag.Milliseconds()))
			}

			return nil
		},
	}
}

func consumeResources(ctx job.JobRuntime, scrapeConfig v1.ScrapeConfig, config v1.Kubernetes, objs, deletedResources []unstructured.Unstructured) error {
	cc := api.NewScrapeContext(ctx.Context).WithScrapeConfig(&scrapeConfig).WithJobHistory(ctx.History).AsIncrementalScrape()
	cc.Context = cc.Context.WithoutName().WithName(fmt.Sprintf("watch[%s/%s]", cc.GetNamespace(), cc.GetName()))
	results, err := processObjects(cc, config, objs)
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

	if len(deletedResources) > 0 {
		deletedResourceIDs := lo.Map(deletedResources, func(item unstructured.Unstructured, _ int) string {
			return string(item.GetUID())
		})

		total, err := db.SoftDeleteConfigItems(ctx.Context, v1.DeleteReasonEvent, deletedResourceIDs...)
		if err != nil {
			return fmt.Errorf("failed to delete %d resources: %w", len(deletedResources), err)
		} else if total != len(deletedResources) {
			ctx.GetSpan().SetAttributes(attribute.StringSlice("deletedResourcesIDs", deletedResourceIDs))
			if cc.PropertyOn(false, "log.missing") {
				ctx.Logger.Warnf("attempted to delete %d resources but only deleted %d", len(deletedResources), total)
			}
		}

		for _, c := range deletedResources {
			ctx.Counter("scraper_deleted",
				"scraper_id", cc.ScraperID(),
				"kind", kubernetes.GetConfigType(lo.ToPtr(c)),
				"reason", string(v1.DeleteReasonEvent),
			).Add(1)
		}

		ctx.History.SuccessCount += total
	}

	return nil
}

// processObjects runs the given fully populated objects through the kubernetes scraper.
func processObjects(ctx api.ScrapeContext, config v1.Kubernetes, objs []unstructured.Unstructured) ([]v1.ScrapeResult, error) {
	var results v1.ScrapeResults
	var scraper kubernetes.KubernetesScraper
	res := scraper.IncrementalScrape(ctx, config, lo.ToSlicePtr(objs))
	for i := range res {
		scraped := processScrapeResult(ctx, res[i])
		results = append(results, scraped...)
	}

	return results, nil
}

func dedup(objs []unstructured.Unstructured) []unstructured.Unstructured {
	var output []unstructured.Unstructured
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
