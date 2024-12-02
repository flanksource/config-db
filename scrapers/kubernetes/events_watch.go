package kubernetes

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"

	pq "github.com/emirpasic/gods/queues/priorityqueue"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

var (
	// BufferSize is the size of the channel that buffers kubernetes watch events
	BufferSize = 5000

	// WatchQueue stores a sync buffer per kubernetes config
	WatchQueue = sync.Map{}

	// DeleteResourceBuffer stores a buffer per kubernetes config
	// that contains the ids of resources that have been deleted.
	DeleteResourceBuffer = sync.Map{}

	lagBuckets = []float64{1000, 5000, 30_000, 120_000, 300_000, 600_000, 900_000, 1_800_000}
)

func getUnstructuredFromInformedObj(resource v1.KubernetesResourceToWatch, obj any) (*unstructured.Unstructured, error) {
	b, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("failed to unmarshal on add func: %v", err)
	}

	// The object returned by the informers do not have kind and apiversion set
	m["kind"] = resource.Kind
	m["apiVersion"] = resource.ApiVersion

	return &unstructured.Unstructured{Object: m}, nil
}

type QueueItem struct {
	Timestamp time.Time
	Event     *v1.KubernetesEvent
	Obj       *unstructured.Unstructured
}

func NewEventQueueItem(e *v1.KubernetesEvent) *QueueItem {
	return &QueueItem{
		Timestamp: time.Now(),
		Event:     e,
	}
}

func NewObjectQueueItem(e *unstructured.Unstructured) *QueueItem {
	return &QueueItem{
		Timestamp: time.Now(),
		Obj:       e,
	}
}

// PayloadTimestamp returns the timestamp of the event or the object it contains.
func (t *QueueItem) PayloadTimestamp() time.Time {
	if t.Event != nil {
		return t.Event.Metadata.CreationTimestamp.Time
	}

	if t.Obj != nil {
		return t.Obj.GetCreationTimestamp().Time
	}

	panic("queue item is empty")
}

func pqComparator(a, b any) int {
	var aTimestamp, bTimestamp time.Time
	qa := a.(*QueueItem)
	qb := b.(*QueueItem)

	if qa.PayloadTimestamp().Before(qb.PayloadTimestamp()) {
		return -1
	} else if aTimestamp.Equal(bTimestamp) {
		return 0
	} else {
		return 1
	}
}

// WatchResources watches Kubernetes resources with shared informers
func WatchResources(ctx api.ScrapeContext, config v1.Kubernetes) error {
	priorityQueue := pq.NewWith(pqComparator)
	if loaded, ok := WatchQueue.LoadOrStore(config.Hash(), priorityQueue); ok {
		priorityQueue = loaded.(*pq.Queue)
	}

	deleteBuffer := make(chan string, BufferSize)
	DeleteResourceBuffer.Store(config.Hash(), deleteBuffer)

	if config.Kubeconfig != nil {
		var err error
		c, err := ctx.WithKubeconfig(*config.Kubeconfig)
		if err != nil {
			return fmt.Errorf("failed to apply kube config: %w", err)
		}
		ctx.Context = *c
	}

	for _, watchResource := range lo.Uniq(config.Watch) {
		if err := globalSharedInformerManager.Register(ctx, watchResource, priorityQueue, deleteBuffer); err != nil {
			return fmt.Errorf("failed to register informer: %w", err)
		}
	}

	// Stop all the other active shared informers, if any, that were previously started
	// but then removed from the config.
	var existingWatches []string
	for _, w := range config.Watch {
		existingWatches = append(existingWatches, w.ApiVersion+w.Kind)
	}
	globalSharedInformerManager.stop(ctx, kubeConfigIdentifier(ctx), existingWatches...)

	ctx.Counter("kubernetes_scraper_resource_watcher", "scraper_id", ctx.ScraperID()).Add(1)
	return nil
}

// WatchEvents watches Kubernetes events for any config changes & fetches
// the referenced config items in batches.
func WatchEvents(ctx api.ScrapeContext, config v1.Kubernetes) error {
	priorityQueue := pq.NewWith(pqComparator)
	if loaded, ok := WatchQueue.LoadOrStore(config.Hash(), priorityQueue); ok {
		priorityQueue = loaded.(*pq.Queue)
	}

	if config.Kubeconfig != nil {
		var err error
		c, err := ctx.WithKubeconfig(*config.Kubeconfig)
		if err != nil {
			return fmt.Errorf("failed to apply kube config: %w", err)
		}
		ctx.Context = *c
	}

	listOpt := metav1.ListOptions{}
	watcher, err := ctx.Kubernetes().CoreV1().Events(config.Namespace).Watch(ctx, listOpt)
	if err != nil {
		return fmt.Errorf("failed to create a new event watcher: %w", err)
	}
	defer watcher.Stop()

	ctx.Counter("kubernetes_scraper_event_watcher", "scraper_id", ctx.ScraperID()).Add(1)
	for watchEvent := range watcher.ResultChan() {
		if watchEvent.Type == watch.Error {
			status, ok := watchEvent.Object.(*metav1.Status)
			if ok {
				ctx.Counter("kubernetes_scraper_error", "source", "watch", "reason", status.Status, "scraper_id", ctx.ScraperID()).Add(1)
				return fmt.Errorf("watch error: (status=%s, reason=%s, message=%s)", status.Status, status.Reason, status.Message)
			}
			ctx.Counter("kubernetes_scraper_error", "reason", "unknown_error_object", "scraper_id", ctx.ScraperID()).Add(1)

			return fmt.Errorf("watch error: unknown error object %T", watchEvent.Object)
		}

		var event v1.KubernetesEvent
		if err := event.FromObjMap(watchEvent.Object); err != nil {
			ctx.Counter("kubernetes_scraper_unmatched", "source", "watch", "reason", "unmarshal_error	", "scraper_id", ctx.ScraperID()).Add(1)
			ctx.Errorf("failed to unmarshal event (id=%s): %v", event.GetUID(), err)
			continue
		}

		// TODO: We receive old events (hours old) and that screws up the histogram
		ctx.Histogram("event_receive_lag", lagBuckets, "scraper", ctx.ScraperID()).
			Record(time.Duration(time.Since(event.Metadata.CreationTimestamp.Time).Milliseconds()))

		if event.InvolvedObject == nil {
			ctx.Counter("kubernetes_scraper_unmatched", "source", "watch", "reason", "involved_object_nil", "scraper_id", ctx.ScraperID()).Add(1)
			continue
		}

		// NOTE: Involved objects do not have labels.
		// As a result, we have to make use of the ignoredConfigsCache to filter out events of resources that have been excluded
		// with labels.
		if config.Exclusions.Filter(event.InvolvedObject.Name, event.InvolvedObject.Namespace, event.InvolvedObject.Kind, nil) {
			ctx.Counter("kubernetes_scraper_excluded", "source", "watch", "kind", event.InvolvedObject.Kind, "scraper_id", ctx.ScraperID()).Add(1)

			continue
		}
		ctx.Counter("kubernetes_scraper_events", "source", "watch", "kind", event.InvolvedObject.Kind, "scraper_id", ctx.ScraperID()).Add(1)

		priorityQueue.Enqueue(NewEventQueueItem(&event))
	}

	return nil
}

// kubeConfigIdentifier returns a unique identifier for a kubernetes config of a scraper.
func kubeConfigIdentifier(ctx api.ScrapeContext) string {
	rs := ctx.KubernetesRestConfig()
	if rs == nil {
		return ctx.ScrapeConfig().Name
	}

	return rs.Host
}
