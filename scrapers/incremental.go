package scrapers

import (
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	pq "github.com/emirpasic/gods/queues/priorityqueue"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers/kubernetes"
	"github.com/flanksource/config-db/utils/kube"
	"github.com/flanksource/duty/job"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func consumeKubernetesWatchJobKey(id string) string {
	return id + "-consume-kubernetes-watch"
}

// ConsumeKubernetesWatchJobFunc returns a job that consumes kubernetes objects received from shared informers
// for the given config of the scrapeconfig.
func ConsumeKubernetesWatchJobFunc(sc api.ScrapeContext, config v1.Kubernetes, queue *pq.Queue) *job.Job {
	return &job.Job{
		Name:         "ConsumeKubernetesWatch",
		Context:      sc.DutyContext().WithObject(sc.ScrapeConfig().ObjectMeta),
		JobHistory:   true,
		Singleton:    true,
		Retention:    job.RetentionFew,
		Schedule:     "@every 15s",
		ResourceID:   string(sc.ScrapeConfig().GetUID()),
		ID:           fmt.Sprintf("%s/%s", sc.ScrapeConfig().Namespace, sc.ScrapeConfig().Name),
		ResourceType: job.ResourceTypeScraper,
		Fn: func(ctx job.JobRuntime) error {
			plugins, err := db.LoadAllPlugins(ctx.Context)
			if err != nil {
				return fmt.Errorf("failed to load plugins: %w", err)
			}

			config := config.DeepCopy()

			sc := sc.WithScrapeConfig(sc.ScrapeConfig(), plugins...)
			config.BaseScraper = config.BaseScraper.ApplyPlugins(plugins...)

			var (
				objs           []*unstructured.Unstructured
				deletedObjects []string
				queuedTime     = map[string]time.Time{}

				seenObjects       = map[string]struct{}{}
				objectsFromEvents = map[string]v1.InvolvedObject{}
			)

			for {
				val, more := queue.Dequeue()
				if !more {
					break
				}

				// On the off chance the queue is populated faster than it's consumed
				// and to keep each run short, we set a limit.
				if len(objs) > kubernetes.BufferSize {
					break
				}

				queueItem, ok := val.(*kubernetes.QueueItem)
				if !ok {
					return fmt.Errorf("unexpected item in the priority queue: %T", val)
				}
				obj := queueItem.Obj

				if queueItem.Operation == kubernetes.QueueItemOperationDelete {
					deletedObjects = append(deletedObjects, string(obj.GetUID()))
					continue
				}

				if obj.GetKind() == "Event" {
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

					objectsFromEvents[string(involvedObject.UID)] = involvedObject
				} else {
					seenObjects[string(obj.GetUID())] = struct{}{}
				}

				queuedTime[string(obj.GetUID())] = queueItem.Timestamp
				objs = append(objs, obj)
			}

			// NOTE: Events whose involved objects aren't watched by informers, should be rescraped.
			// If we trigger delayed re-scrape on addition of a config_change then this shouldn't be necessary.
			var involvedObjectsToScrape []v1.InvolvedObject
			for id, involvedObject := range objectsFromEvents {
				if _, ok := seenObjects[id]; !ok {
					involvedObjectsToScrape = append(involvedObjectsToScrape, involvedObject)
				}
			}

			if res, err := kube.FetchInvolvedObjects(sc, involvedObjectsToScrape); err != nil {
				ctx.History.AddErrorf("failed to fetch involved objects from events: %v", err)
				return err
			} else {
				objs = append(objs, res...)
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
			if err := consumeResources(ctx, *sc.ScrapeConfig(), *config, objs, deletedObjects); err != nil {
				ctx.History.AddErrorf("failed to consume resources: %v", err)
				return err
			}

			for _, obj := range objs {
				lag := time.Since(queuedTime[string(obj.GetUID())])
				ctx.Histogram("informer_consume_lag", consumeLagBuckets, "scraper", sc.ScraperID(), "kind", obj.GetKind()).
					Record(time.Duration(lag.Milliseconds()))
			}

			return nil
		},
	}
}

func consumeResources(ctx job.JobRuntime, scrapeConfig v1.ScrapeConfig, config v1.Kubernetes, objs []*unstructured.Unstructured, deletedResourcesIDs []string) error {
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

	if len(deletedResourcesIDs) > 0 {
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

// processObjects runs the given fully populated objects through the kubernetes scraper.
func processObjects(ctx api.ScrapeContext, config v1.Kubernetes, objs []*unstructured.Unstructured) ([]v1.ScrapeResult, error) {
	var results v1.ScrapeResults
	var scraper kubernetes.KubernetesScraper
	res := scraper.IncrementalScrape(ctx, config, objs)
	for i := range res {
		scraped := processScrapeResult(ctx, res[i])
		results = append(results, scraped...)
	}

	return results, nil
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
