package kubernetes

import (
	"fmt"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	// BufferSize is the size of the channel that buffers kubernetes watch events
	BufferSize = 5000

	// WatchEventBuffers stores a sync buffer per kubernetes config
	WatchEventBuffers = make(map[string]chan v1.KubernetesEvent)
)

// WatchEvents watches Kubernetes events for any config changes & fetches
// the referenced config items in batches.
func WatchEvents(ctx api.ScrapeContext, config v1.Kubernetes) error {
	ctx.Logger.V(1).Infof("Watching kubernetes events. namespace=%s cluster=%s", config.Namespace, config.ClusterName)

	buffer := make(chan v1.KubernetesEvent, ctx.DutyContext().Properties().Int("kubernetes.watch.events.bufferSize", BufferSize))
	WatchEventBuffers[config.Hash()] = buffer

	watcher, err := ctx.Kubernetes().CoreV1().Events(config.Namespace).Watch(ctx, metav1.ListOptions{})
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
