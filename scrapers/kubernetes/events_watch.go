package kubernetes

import (
	"fmt"
	"sync"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	// refetchEventResourceInterval is the schedule on which K8s resources,
	// gathered from from the real-time events, are scraped.
	refetchEventResourceInterval = time.Second * 10
)

type consumerFunc func(ctx api.ScrapeContext, config v1.Kubernetes, involvedObjects []v1.KubernetesEvent) error

type eventWatcher struct {
	// eventBuffer keeps record of all the events grouped by the involvedObject.
	eventBuffer map[string]v1.KubernetesEvent

	// lock for involvedObjects.
	lock *sync.Mutex
}

func WatchEvents(ctx api.ScrapeContext, config v1.Kubernetes, consume consumerFunc) error {
	watcher := &eventWatcher{
		lock:        &sync.Mutex{},
		eventBuffer: make(map[string]v1.KubernetesEvent),
	}

	go watcher.consumeChangeEvents(ctx, config, consume)

	return watcher.Watch(ctx, config)
}

// WatchEvents watches Kubernetes events for any config changes & fetches
// the referenced config items in batches.
func (t *eventWatcher) Watch(ctx api.ScrapeContext, config v1.Kubernetes) error {
	logger.Infof("Watching kubernetes events. name=%s namespace=%s cluster=%s", config.Name, config.Namespace, config.ClusterName)

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

		t.lock.Lock()
		t.eventBuffer[string(event.InvolvedObject.UID)] = event
		t.lock.Unlock()
	}

	return nil
}

// consumeChangeEvents fetches the configs referenced by the changes and saves them.
// It clears the buffer after.
func (t *eventWatcher) consumeChangeEvents(ctx api.ScrapeContext, config v1.Kubernetes, consume consumerFunc) {
	for {
		time.Sleep(refetchEventResourceInterval)

		if len(t.eventBuffer) == 0 {
			continue
		}

		t.lock.Lock()
		allEvents := lo.Values(t.eventBuffer)
		t.eventBuffer = make(map[string]v1.KubernetesEvent)
		t.lock.Unlock()

		if err := consume(ctx, config, allEvents); err != nil {
			logger.Errorf("failed to run scraper %v: %w", ctx.ScrapeConfig().Name, err)
		}
	}
}
