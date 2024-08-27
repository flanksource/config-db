package kubernetes

import (
	"fmt"
	"strings"
	"sync"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

type informerCacheData struct {
	informer cache.SharedInformer
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

func (t *SharedInformerManager) Register(ctx api.ScrapeContext, watchResource v1.KubernetesResourceToWatch, buffer chan<- *unstructured.Unstructured, deleteBuffer chan<- string) error {
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
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			u, err := getUnstructuredFromInformedObj(watchResource, obj)
			if err != nil {
				logger.Errorf("failed to get unstructured from new object: %v", err)
				return
			}

			ctx.Logger.V(3).Infof("added: %s %s %s", u.GetUID(), u.GetKind(), u.GetName())
			buffer <- u
		},
		UpdateFunc: func(oldObj any, newObj any) {
			u, err := getUnstructuredFromInformedObj(watchResource, newObj)
			if err != nil {
				logger.Errorf("failed to get unstructured from updated object: %v", err)
				return
			}

			ctx.Logger.V(3).Infof("updated: %s %s %s", u.GetUID(), u.GetKind(), u.GetName())
			buffer <- u
		},
		DeleteFunc: func(obj any) {
			u, err := getUnstructuredFromInformedObj(watchResource, obj)
			if err != nil {
				logToJobHistory(ctx.DutyContext(), "DeleteK8sWatchResource", ctx.ScrapeConfig().GetPersistedID(), "failed to get unstructured %v", err)
				return
			}

			ctx.Logger.V(3).Infof("deleted: %s %s %s", u.GetUID(), u.GetKind(), u.GetName())
			deleteBuffer <- string(u.GetUID())
		},
	})
	if err != nil {
		return fmt.Errorf("failed to add informer event handlers: %w", err)
	}

	go func() {
		informer.Run(stopper)
		ctx.Logger.V(1).Infof("stopped shared informer for: %v", watchResource)
	}()
	return nil
}

// getOrCreate returns an existing shared informer instance or creates & returns a new one.
func (t *SharedInformerManager) getOrCreate(ctx api.ScrapeContext, apiVersion, kind string) (cache.SharedInformer, chan struct{}, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	cacheKey := apiVersion + kind
	kubeconfig := ctx.KubernetesRestConfig().Host

	if val, ok := t.cache[kubeconfig]; ok {
		if data, ok := val[cacheKey]; ok {
			return data.informer, data.stopper, false
		}
	}

	factory := informers.NewSharedInformerFactory(ctx.Kubernetes(), 0)
	stopper := make(chan struct{})

	informer := getInformer(factory, apiVersion, kind)
	ctx.Gauge("kubernetes_active_shared_informers").Add(1)

	cacheValue := &informerCacheData{
		stopper:  stopper,
		informer: informer,
	}
	if _, ok := t.cache[kubeconfig]; ok {
		t.cache[kubeconfig][cacheKey] = cacheValue
	} else {
		t.cache[kubeconfig] = map[string]*informerCacheData{
			cacheKey: cacheValue,
		}
	}

	return informer, stopper, true
}

// stop stops all shared informers for the given kubeconfig
// apart from the ones provided.
func (t *SharedInformerManager) stop(ctx api.ScrapeContext, kubeconfig string, exception ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	var toDelete []string
	if informers, ok := t.cache[kubeconfig]; ok {
		for key, cached := range informers {
			if !lo.Contains(exception, key) {
				ctx.Logger.V(1).Infof("stopping informer for %s", key)

				cached.informer.IsStopped()
				ctx.Gauge("kubernetes_active_shared_informers").Sub(1)

				toDelete = append(toDelete, key)
				close(cached.stopper)
			}
		}
	}

	for _, key := range toDelete {
		delete(t.cache[kubeconfig], key)
	}
}

func getInformer(factory informers.SharedInformerFactory, apiVersion, kind string) cache.SharedInformer {
	// TODO: need to populate this

	kind = strings.ToLower(kind)
	switch strings.ToLower(apiVersion) {
	case "v1":
		switch kind {
		case "pod":
			return factory.Core().V1().Pods().Informer()
		case "node":
			return factory.Core().V1().Nodes().Informer()
		}

	case "apps/v1":
		switch kind {
		case "deployment":
			return factory.Apps().V1().Deployments().Informer()
		case "daemonset":
			return factory.Apps().V1().DaemonSets().Informer()
		case "replicaset":
			return factory.Apps().V1().ReplicaSets().Informer()
		case "statefulset":
			return factory.Apps().V1().StatefulSets().Informer()
		}

	case "batch/v1":
		switch kind {
		case "cronjob":
			return factory.Batch().V1().CronJobs().Informer()
		case "job":
			return factory.Batch().V1().Jobs().Informer()
		}
	}

	return nil
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
