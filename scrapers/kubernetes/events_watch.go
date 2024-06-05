package kubernetes

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty/types"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils/kube"
)

var (
	// BufferSize is the size of the channel that buffers kubernetes watch events
	BufferSize = 5000

	// WatchEventBuffers stores a sync buffer per kubernetes config
	WatchEventBuffers = sync.Map{}

	WatchResourceBufferSize = 5000

	// WatchEventBuffers stores a sync buffer per kubernetes config
	WatchResourceBuffer = sync.Map{}

	// DeleteResourceBuffer stores a buffer per kubernetes config
	// that contains the ids of resources that have been deleted.
	DeleteResourceBuffer = sync.Map{}
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

// WatchResources watches Kubernetes resources
func WatchResources(ctx api.ScrapeContext, config v1.Kubernetes) error {

	buffer := make(chan *unstructured.Unstructured, ctx.DutyContext().Properties().Int("kubernetes.watch.resources.bufferSize", WatchResourceBufferSize))
	WatchResourceBuffer.Store(config.Hash(), buffer)

	deleteBuffer := make(chan string, WatchResourceBufferSize)
	DeleteResourceBuffer.Store(config.Hash(), deleteBuffer)

	var restConfig *rest.Config
	var err error
	if config.Kubeconfig != nil {
		ctx, restConfig, err = applyKubeconfig(ctx, *config.Kubeconfig)
		if err != nil {
			return fmt.Errorf("failed to apply kube config")
		}
	} else {
		restConfig, err = kube.DefaultRestConfig()
		if err != nil {
			return fmt.Errorf("failed to apply kube config")
		}
	}

	clientset, err := kube.ClientSetFromRestConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes clientset from rest config: %w", err)
	}

	factory := informers.NewSharedInformerFactory(clientset, 0)
	stopper := make(chan struct{})
	defer close(stopper)

	var wg sync.WaitGroup
	for _, watchResource := range lo.Uniq(config.Watch) {
		wg.Add(1)

		informer := getInformer(factory, watchResource.ApiVersion, watchResource.Kind)
		if informer == nil {
			return fmt.Errorf("could not find informer for: apiVersion=%v kind=%v", watchResource.ApiVersion, watchResource.Kind)
		}

		informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj any) {
				u, err := getUnstructuredFromInformedObj(watchResource, obj)
				if err != nil {
					logger.Errorf("failed to get unstructured from new object: %v", err)
					return
				}

				buffer <- u
			},
			UpdateFunc: func(oldObj any, newObj any) {
				u, err := getUnstructuredFromInformedObj(watchResource, newObj)
				if err != nil {
					logger.Errorf("failed to get unstructured from updated object: %v", err)
					return
				}

				buffer <- u
			},
			DeleteFunc: func(obj any) {
				u, err := getUnstructuredFromInformedObj(watchResource, obj)
				if err != nil {
					logger.Errorf("failed to get unstructured from deleted object: %v", err)
					return
				}

				deleteBuffer <- string(u.GetUID())
			},
		})

		go informer.Run(stopper)
	}

	ctx.Counter("kubernetes_scraper_resource_watcher", lo.FromPtr(ctx.ScrapeConfig().GetPersistedID()).String()).Add(1)
	ctx.Logger.V(1).Infof("waiting for informers")
	wg.Wait()

	return nil
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

// WatchEvents watches Kubernetes events for any config changes & fetches
// the referenced config items in batches.
func WatchEvents(ctx api.ScrapeContext, config v1.Kubernetes) error {
	buffer := make(chan v1.KubernetesEvent, ctx.DutyContext().Properties().Int("kubernetes.watch.events.bufferSize", BufferSize))
	WatchEventBuffers.Store(config.Hash(), buffer)

	if config.Kubeconfig != nil {
		var err error
		ctx, _, err = applyKubeconfig(ctx, *config.Kubeconfig)
		if err != nil {
			return fmt.Errorf("failed to apply kube config")
		}
	}

	listOpt := metav1.ListOptions{}
	watcher, err := ctx.Kubernetes().CoreV1().Events(config.Namespace).Watch(ctx, listOpt)
	if err != nil {
		return fmt.Errorf("failed to create a new event watcher: %w", err)
	}
	defer watcher.Stop()

	ctx.Counter("kubernetes_scraper_event_watcher", lo.FromPtr(ctx.ScrapeConfig().GetPersistedID()).String()).Add(1)
	for watchEvent := range watcher.ResultChan() {
		var event v1.KubernetesEvent
		if err := event.FromObj(watchEvent.Object); err != nil {
			logger.Errorf("failed to unmarshal event (id=%s): %v", event.GetUID(), err)
			continue
		}

		if event.InvolvedObject == nil {
			continue
		}

		if config.Exclusions.Filter(event.InvolvedObject.Name, event.InvolvedObject.Namespace, event.InvolvedObject.Kind, nil) {
			continue
		}

		buffer <- event
	}

	return nil
}

func applyKubeconfig(ctx api.ScrapeContext, kubeConfig types.EnvVar) (api.ScrapeContext, *rest.Config, error) {
	val, err := ctx.GetEnvValueFromCache(kubeConfig, ctx.GetNamespace())
	if err != nil {
		return ctx, nil, fmt.Errorf("failed to get kubeconfig from env: %w", err)
	}

	var client kubernetes.Interface
	var restConfig *rest.Config
	if strings.HasPrefix(val, "/") {
		client, restConfig, err = kube.NewKubeClientWithConfigPath(val)
		if err != nil {
			return ctx, nil, fmt.Errorf("failed to initialize kubernetes client from the provided kubeconfig: %w", err)
		}
	} else {
		client, restConfig, err = kube.NewKubeClientWithConfig(val)
		if err != nil {
			return ctx, nil, fmt.Errorf("failed to initialize kubernetes client from the provided kubeconfig: %w", err)
		}
	}

	ctx.Context = ctx.WithKubernetes(client)

	return ctx, restConfig, nil
}
