package kubernetes

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	pq "github.com/emirpasic/gods/queues/priorityqueue"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/is-healthy/pkg/health"
	"github.com/google/uuid"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
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

// WatchResources watches Kubernetes resources with shared informers
func WatchResources(ctx api.ScrapeContext, config v1.Kubernetes) (*pq.Queue, error) {
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
			return nil, fmt.Errorf("failed to apply kube config: %w", err)
		}
		ctx.Context = *c
	}

	for _, watchResource := range lo.Uniq(config.Watch) {
		if err := globalSharedInformerManager.Register(ctx, watchResource, priorityQueue, deleteBuffer); err != nil {
			return nil, fmt.Errorf("failed to register informer: %w", err)
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
	return priorityQueue, nil
}

type informerCacheData struct {
	informer informers.GenericInformer
	stopper  chan (struct{})
}

// singleton
var globalSharedInformerManager = SharedInformerManager{
	cache: make(map[string]map[string]*informerCacheData),
}

// SharedInformerManager distributes the same share informer for a given pair of
// <kubeconfig, groupVersionKind>
type SharedInformerManager struct {
	mu    sync.Mutex
	cache map[string]map[string]*informerCacheData
}

type DeleteObjHandler func(ctx context.Context, id string) error

func (t *SharedInformerManager) Register(ctx api.ScrapeContext, watchResource v1.KubernetesResourceToWatch, queue *pq.Queue, deleteBuffer chan<- string) error {
	apiVersion, kind := watchResource.ApiVersion, watchResource.Kind

	informer, stopper, isNew := t.getOrCreate(ctx, apiVersion, kind)
	if informer == nil {
		return fmt.Errorf("could not find informer for: apiVersion=%v kind=%v", apiVersion, kind)
	}

	if !isNew {
		// event handlers have already been set.
		// nothing left to do.
		return nil
	}

	ctx.Context = ctx.WithName("watch." + ctx.ScrapeConfig().Name)

	ctx.Logger.V(1).Infof("registering shared informer for: %v", watchResource)
	_, err := informer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			u, err := getUnstructuredFromInformedObj(watchResource, obj)
			if err != nil {
				ctx.Counter("kubernetes_informer_errors",
					"type", "add",
					"reason", "unmarshal_error",
					"scraper_id", ctx.ScraperID()).Add(1)
				logger.Errorf("failed to get unstructured from new object: %v", err)
				return
			}

			ctx.Counter("kubernetes_informer_events", "type", "add", "kind", u.GetKind(), "scraper_id", ctx.ScraperID()).Add(1)
			if ctx.Properties().On(false, "scraper.log.items") {
				ctx.Logger.V(4).Infof("added: %s %s %s", u.GetUID(), u.GetKind(), u.GetName())
			}

			// TODO: We receive very old objects (months old) and that screws up the histogram
			ctx.Histogram("informer_receive_lag", lagBuckets,
				"scraper", ctx.ScraperID(),
				"kind", watchResource.Kind,
				"operation", "add",
			).Record(time.Duration(time.Since(u.GetCreationTimestamp().Time).Milliseconds()))

			queue.Enqueue(NewQueueItem(u))
		},
		UpdateFunc: func(oldObj any, newObj any) {
			u, err := getUnstructuredFromInformedObj(watchResource, newObj)
			if err != nil {
				ctx.Counter("kubernetes_informer_errors",
					"type", "update",
					"reason", "unmarshal_error",
					"scraper_id", ctx.ScraperID()).Add(1)

				logger.Errorf("failed to get unstructured from updated object: %v", err)
				return
			}

			ctx.Counter("kubernetes_informer_events", "type", "update", "kind", u.GetKind(), "scraper_id", ctx.ScraperID()).Add(1)

			if ctx.Properties().On(false, "scraper.log.items") {
				ctx.Logger.V(3).Infof("updated: %s %s %s", u.GetUID(), u.GetKind(), u.GetName())
			}

			lastUpdatedTime := health.GetLastUpdatedTime(u)
			if lastUpdatedTime != nil && lastUpdatedTime.After(u.GetCreationTimestamp().Time) && lastUpdatedTime.Before(time.Now()) {
				ctx.Histogram("informer_receive_lag", lagBuckets,
					"scraper", ctx.ScraperID(),
					"kind", watchResource.Kind,
					"operation", "update",
				).Record(time.Duration(time.Since(*lastUpdatedTime).Milliseconds()))
			}

			queue.Enqueue(NewQueueItem(u))
		},
		DeleteFunc: func(obj any) {
			u, err := getUnstructuredFromInformedObj(watchResource, obj)
			if err != nil {
				ctx.Counter("kubernetes_informer_errors",
					"type", "delete",
					"reason", "unmarshal_error",
					"scraper_id", ctx.ScraperID()).Add(1)
				logToJobHistory(ctx.DutyContext(), "DeleteK8sWatchResource", ctx.ScrapeConfig().GetPersistedID(), "failed to get unstructured %v", err)
				return
			}

			if u.GetKind() == "Event" {
				return
			}

			ctx.Counter("kubernetes_informer_events", "type", "delete", "kind", u.GetKind(), "scraper_id", ctx.ScraperID()).Add(1)

			if ctx.Properties().On(false, "scraper.log.items") {
				ctx.Logger.V(3).Infof("deleted: %s %s %s", u.GetUID(), u.GetKind(), u.GetName())
			}

			if u.GetDeletionTimestamp() != nil {
				ctx.Histogram("informer_receive_lag", lagBuckets,
					"scraper", ctx.ScraperID(),
					"kind", watchResource.Kind,
					"operation", "delete",
				).Record(time.Duration(time.Since(u.GetDeletionTimestamp().Time).Milliseconds()))
			}

			deleteBuffer <- string(u.GetUID())
		},
	})
	if err != nil {
		return fmt.Errorf("failed to add informer event handlers: %w", err)
	}

	go func() {
		informer.Informer().Run(stopper)
		ctx.Logger.V(1).Infof("stopped shared informer for: %v", watchResource)
	}()
	return nil
}

// getOrCreate returns an existing shared informer instance or creates & returns a new one.
func (t *SharedInformerManager) getOrCreate(ctx api.ScrapeContext, apiVersion, kind string) (informers.GenericInformer, chan struct{}, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	cacheKey := apiVersion + kind
	clusterID := kubeConfigIdentifier(ctx)

	if val, ok := t.cache[clusterID]; ok {
		if data, ok := val[cacheKey]; ok {
			return data.informer, data.stopper, false
		}
	}

	factory := informers.NewSharedInformerFactory(ctx.Kubernetes(), 0)
	stopper := make(chan struct{})

	informer, err := getInformer(factory, apiVersion, kind)
	if err != nil {
		return nil, nil, false
	}
	ctx.Gauge("kubernetes_active_shared_informers").Add(1)

	cacheValue := &informerCacheData{
		stopper:  stopper,
		informer: informer,
	}
	if _, ok := t.cache[clusterID]; ok {
		t.cache[clusterID][cacheKey] = cacheValue
	} else {
		t.cache[clusterID] = map[string]*informerCacheData{
			cacheKey: cacheValue,
		}
	}

	return informer, stopper, true
}

// stop stops all shared informers for the given kubeconfig
// apart from the ones provided.
func (t *SharedInformerManager) stop(ctx api.ScrapeContext, clusterID string, exception ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	var toDelete []string
	if informers, ok := t.cache[clusterID]; ok {
		for key, cached := range informers {
			if !lo.Contains(exception, key) {
				ctx.Logger.V(1).Infof("stopping informer for %s", key)

				cached.informer.Informer().IsStopped()
				ctx.Gauge("kubernetes_active_shared_informers").Sub(1)

				toDelete = append(toDelete, key)
				close(cached.stopper)
			}
		}
	}

	for _, key := range toDelete {
		delete(t.cache[clusterID], key)
	}
}

func getInformer(factory informers.SharedInformerFactory, apiVersion, kind string) (informers.GenericInformer, error) {
	gvk := schema.FromAPIVersionAndKind(apiVersion, kind)
	gvr := schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: KindToResource(gvk.Kind),
	}

	return factory.ForResource(gvr)
}

// logToJobHistory logs any failures in saving a playbook run to the job history.
func logToJobHistory(ctx context.Context, job string, scraperID *uuid.UUID, err string, args ...any) {
	jobHistory := models.NewJobHistory(ctx.Logger, job, "", lo.FromPtr(scraperID).String())
	jobHistory.Start()
	jobHistory.AddErrorf(err, args...)

	if err := jobHistory.End().Persist(ctx.DB()); err != nil {
		logger.Errorf("error persisting job history: %v", err)
	}
}

func KindToResource(kind string) string {
	return strings.ToLower(kind) + "s"
}

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

// kubeConfigIdentifier returns a unique identifier for a kubernetes config of a scraper.
func kubeConfigIdentifier(ctx api.ScrapeContext) string {
	rs := ctx.KubernetesRestConfig()
	if rs == nil {
		return ctx.ScrapeConfig().Name
	}

	return rs.Host
}

type QueueItem struct {
	Timestamp time.Time // Queued time
	Obj       *unstructured.Unstructured
}

func NewQueueItem(obj *unstructured.Unstructured) *QueueItem {
	return &QueueItem{
		Timestamp: time.Now(),
		Obj:       obj,
	}
}

func pqComparator(a, b any) int {
	var aTimestamp, bTimestamp time.Time
	qa := a.(*QueueItem)
	qb := b.(*QueueItem)

	if qa.Obj.GetCreationTimestamp().Time.Before(qb.Obj.GetCreationTimestamp().Time) {
		return -1
	} else if aTimestamp.Equal(bTimestamp) {
		return 0
	} else {
		return 1
	}
}
