package kubernetes

import (
	"fmt"
	"strings"
	"sync"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty/types"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
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

	var channels []<-chan watch.Event
	for _, k := range lo.Uniq(config.Watch) {
		client, err := kube.GetClientByGroupVersionKind(restConfig, k.ApiVersion, k.Kind)
		if err != nil {
			return fmt.Errorf("failed to create client for kind(%s): %v", k, err)
		}

		watcher, err := client.Watch(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to create watcher for kind(%s): %v", k, err)
		}
		defer watcher.Stop()

		channels = append(channels, watcher.ResultChan())
	}

	for watchEvent := range utils.MergeChannels(channels...) {
		obj, ok := watchEvent.Object.(*unstructured.Unstructured)
		if ok {
			if watchEvent.Type == watch.Deleted {
				deleteBuffer <- string(obj.GetUID())
			} else {
				buffer <- obj
			}
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
