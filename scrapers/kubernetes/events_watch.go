package kubernetes

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
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

// WatchResources watches Kubernetes resources with shared informers
func WatchResources(ctx api.ScrapeContext, config v1.Kubernetes) error {
	buffer := make(chan *unstructured.Unstructured, ctx.DutyContext().Properties().Int("kubernetes.watch.resources.bufferSize", WatchResourceBufferSize))
	WatchResourceBuffer.Store(config.Hash(), buffer)

	deleteBuffer := make(chan string, WatchResourceBufferSize)
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
		if err := globalSharedInformerManager.Register(ctx, watchResource, buffer, deleteBuffer); err != nil {
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
	buffer := make(chan v1.KubernetesEvent, ctx.DutyContext().Properties().Int("kubernetes.watch.events.bufferSize", BufferSize))
	WatchEventBuffers.Store(config.Hash(), buffer)

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
				return fmt.Errorf("watch error: (status=%s, reason=%s, message=%s)", status.Status, status.Reason, status.Message)
			}

			return fmt.Errorf("watch error: unknown error object %T", watchEvent.Object)
		}

		var event v1.KubernetesEvent
		if err := event.FromObjMap(watchEvent.Object); err != nil {
			ctx.Errorf("failed to unmarshal event (id=%s): %v", event.GetUID(), err)
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

// kubeConfigIdentifier returns a unique identifier for a kubernetes config of a scraper.
func kubeConfigIdentifier(ctx api.ScrapeContext) string {
	rs := ctx.KubernetesRestConfig()
	if rs == nil {
		return ctx.ScrapeConfig().Name
	}

	return rs.Host
}
