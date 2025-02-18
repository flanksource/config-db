package kubernetes

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/is-healthy/pkg/health"
	"github.com/google/uuid"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	informerLagBuckets = []float64{1_000, 5_000, 30_000, 120_000, 300_000, 600_000, 900_000, 1_800_000}
)

// WatchResources watches Kubernetes resources with shared informers
func WatchResources(ctx api.ScrapeContext, config v1.Kubernetes) (*collections.Queue[*QueueItem], error) {
	priorityQueue, err := collections.NewQueue(collections.QueueOpts[*QueueItem]{
		Metrics: collections.MetricsOpts[*QueueItem]{
			Name: "shared_informer",
			Labels: map[string]any{
				"scraper_id": ctx.ScraperID(),
			},
		},
		Comparator: pqComparator,
		Equals:     queueItemIsEqual,
		Dedupe:     true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create queue: %w", err)
	}

	clientset, restConfig, err := config.KubernetesConnection.Populate(ctx.Context, false)
	if err != nil {
		return nil, err
	}
	ctx.Context = ctx.WithKubernetes(clientset, restConfig)

	for _, watchResource := range lo.Uniq(config.Watch) {
		if err := globalSharedInformerManager.Register(ctx, watchResource, priorityQueue); err != nil {
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

func (t *SharedInformerManager) Register(ctx api.ScrapeContext, watchResource v1.KubernetesResourceToWatch, queue *collections.Queue[*QueueItem]) error {
	registrationTime := time.Now()

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
			receivedAt := time.Now().Round(time.Second)

			u, err := getUnstructuredFromInformedObj(watchResource, obj)
			if err != nil {
				ctx.Counter("kubernetes_informer_errors",
					"type", "add",
					"reason", "unmarshal_error",
					"scraper_id", ctx.ScraperID()).Add(1)
				logger.Errorf("failed to get unstructured from new object: %v", err)
				return
			}

			queue.Enqueue(NewQueueItem(u, QueueItemOperationAdd))

			if ctx.Properties().On(false, "scraper.log.items") {
				ctx.Logger.V(4).Infof("added: %s %s %s", u.GetUID(), u.GetKind(), u.GetName())
			}

			ctx.Counter("kubernetes_informer_events",
				"type", "add",
				"kind", u.GetKind(),
				"scraper_id", ctx.ScraperID(),
				"valid_timestamp", lo.Ternary(u.GetCreationTimestamp().Time.After(registrationTime), "true", "false"),
			).Add(1)

			// This is a way to avoid instrumenting old objects so they don't skew the lag time.
			if u.GetCreationTimestamp().Time.After(registrationTime) {
				ctx.Histogram("informer_receive_lag", informerLagBuckets,
					"scraper", ctx.ScraperID(),
					"kind", watchResource.Kind,
					"operation", "add",
				).Record(time.Duration(u.GetCreationTimestamp().Time.Sub(receivedAt).Milliseconds()))
			}
		},
		UpdateFunc: func(oldObj any, newObj any) {
			// Kubernetes object timestamps are only precise to the second, so we round
			// the current time to the nearest second to avoid incorrectly marking
			// timestamps as being in the past due to millisecond differences.
			receivedAt := time.Now().UTC().Round(time.Second)

			u, err := getUnstructuredFromInformedObj(watchResource, newObj)
			if err != nil {
				ctx.Counter("kubernetes_informer_errors",
					"type", "update",
					"reason", "unmarshal_error",
					"scraper_id", ctx.ScraperID()).Add(1)

				logger.Errorf("failed to get unstructured from updated object: %v", err)
				return
			}

			if ctx.Properties().On(false, "scraper.log.items") {
				ctx.Logger.V(3).Infof("updated: %s %s %s", u.GetUID(), u.GetKind(), u.GetName())
			}

			lastUpdatedTime := lo.FromPtr(health.GetLastUpdatedTime(u))
			lastUpdatedInFuture := lastUpdatedTime.After(receivedAt)
			if !lastUpdatedInFuture {
				ctx.Histogram("informer_receive_lag", informerLagBuckets,
					"scraper", ctx.ScraperID(),
					"kind", watchResource.Kind,
					"operation", "update",
				).Record(time.Duration(receivedAt.Sub(lastUpdatedTime).Milliseconds()))
			} else {
				ctx.Warnf("%s/%s/%s has last updated time %s into the future. receivedAt=%s, lastupdatedTime=%s",
					u.GetKind(), u.GetNamespace(), u.GetName(), lastUpdatedTime.Sub(receivedAt), receivedAt, lastUpdatedTime)
			}

			ctx.Counter("kubernetes_informer_events",
				"type", "update",
				"kind", u.GetKind(),
				"scraper_id", ctx.ScraperID(),
				"valid_timestamp", lo.Ternary(!lastUpdatedInFuture, "true", "false"),
			).Add(1)

			queue.Enqueue(NewQueueItem(u, QueueItemOperationUpdate))
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

			if ctx.Properties().On(false, "scraper.log.items") {
				ctx.Logger.V(3).Infof("deleted: %s %s %s", u.GetUID(), u.GetKind(), u.GetName())
			}

			if u.GetDeletionTimestamp() != nil {
				ctx.Histogram("informer_receive_lag", informerLagBuckets,
					"scraper", ctx.ScraperID(),
					"kind", watchResource.Kind,
					"operation", "delete",
				).Record(time.Duration(time.Since(u.GetDeletionTimestamp().Time).Milliseconds()))
			}

			ctx.Counter("kubernetes_informer_events",
				"type", "delete",
				"kind", u.GetKind(),
				"scraper_id", ctx.ScraperID(),
				"valid_timestamp", lo.Ternary(u.GetDeletionTimestamp() != nil, "true", "false"),
			).Add(1)

			queue.Enqueue(NewQueueItem(u, QueueItemOperationDelete))
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
	rc := ctx.Kubernetes().RestConfig()
	if rc == nil {
		return ctx.ScrapeConfig().GetPersistedID().String() + ctx.ScrapeConfig().Name
	}
	return ctx.KubeAuthFingerprint()
}

type QueueItemOperation int

const (
	QueueItemOperationAdd QueueItemOperation = iota + 1
	QueueItemOperationUpdate
	QueueItemOperationDelete
	QueueItemOperationReEnqueue // Involved objects from events are re-enqueued
)

func (t *QueueItemOperation) Priority() int {
	// smaller value represents higher priority
	priority := map[QueueItemOperation]int{
		QueueItemOperationAdd:       1,
		QueueItemOperationReEnqueue: 1,
		QueueItemOperationUpdate:    2,
		QueueItemOperationDelete:    3,
	}

	return priority[*t]
}

type QueueItem struct {
	Timestamp time.Time // Queued time
	Obj       *unstructured.Unstructured
	Operation QueueItemOperation
}

func NewQueueItem(obj *unstructured.Unstructured, operation QueueItemOperation) *QueueItem {
	return &QueueItem{
		Timestamp: time.Now(),
		Obj:       obj,
		Operation: operation,
	}
}

func queueItemIsEqual(qa, qb *QueueItem) bool {
	return qa.Obj.GetUID() == qb.Obj.GetUID()
}

func pqComparator(qa, qb *QueueItem) int {
	if qa.Obj.GetUID() == qb.Obj.GetUID() {
		resourceVersionA, ok, _ := unstructured.NestedString(qa.Obj.Object, "metadata", "resourceVersion")
		if ok {
			resourceVersionB, _, _ := unstructured.NestedString(qb.Obj.Object, "metadata", "resourceVersion")

			// Because of the way we are deduping, we want the latest version in front of the queue.
			// the later versions are discarded.
			return strings.Compare(resourceVersionB, resourceVersionA)
		}
	}

	if opResult := pqCompareOperation(qa.Operation, qb.Operation); opResult != 0 {
		return opResult
	}

	if opResult := pqCompareOwnerRef(qa.Obj.GetOwnerReferences(), qb.Obj.GetOwnerReferences()); opResult != 0 {
		return opResult
	}

	if opResult := pqCompareKind(qa.Obj.GetKind(), qb.Obj.GetKind()); opResult != 0 {
		return opResult
	}

	lastUpdatedTimeA := *health.GetLastUpdatedTime(qa.Obj)
	lastUpdatedTimeB := *health.GetLastUpdatedTime(qb.Obj)

	if lastUpdatedTimeA.Before(lastUpdatedTimeB) {
		return -1
	} else if lastUpdatedTimeA.Equal(lastUpdatedTimeB) {
		return 0
	} else {
		return 1
	}
}

func pqCompareOperation(a, b QueueItemOperation) int {
	return a.Priority() - b.Priority()
}

func pqCompareOwnerRef(a, b []metav1.OwnerReference) int {
	if len(a) == len(b) {
		return 0
	}

	return len(b) - len(a)
}

func pqCompareKind(a, b string) int {
	// smaller means earlier in the queue
	priority := map[string]int{
		"Namespace":          1,
		"Deployment":         2,
		"StatefulSet":        2,
		"DaemonSet":          2,
		"Service":            2,
		"ClusterRole":        2,
		"Role":               2,
		"HelmChart":          2,
		"HelmRepository":     2,
		"OCIRepository":      2,
		"ClusterRoleBinding": 3,
		"RoleBinding":        3,
		"Endpoints":          3,
		"CronJob":            3,
		"Job":                3,
		"ReplicaSet":         3,
		"Pod":                4,
		"Event":              5,
	}

	const unknownKindPriority = 3 // set medium priority for unknown kinds

	pa := lo.CoalesceOrEmpty(priority[a], unknownKindPriority)
	pb := lo.CoalesceOrEmpty(priority[b], unknownKindPriority)

	return pa - pb
}
