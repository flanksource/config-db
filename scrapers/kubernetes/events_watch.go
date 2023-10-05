package kubernetes

import (
	"fmt"
	"sync"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	// refetchEventResourceInterval is the schedule on which K8s resources,
	// gathered from from the real-time events, are scraped.
	refetchEventResourceInterval = time.Second * 10
)

type consumerFunc func(ctx api.ScrapeContext, config v1.Kubernetes, involvedObjects []*v1.InvolvedObject) error

type eventWatcher struct {
	// involvedObjects keeps record of all the involved objects from events
	// by the resource kind.
	involvedObjects map[string]map[string]*v1.InvolvedObject

	// lock for involvedObjects.
	lock *sync.Mutex
}

func WatchEvents(ctx api.ScrapeContext, config v1.Kubernetes, consume consumerFunc) error {
	watcher := &eventWatcher{
		lock:            &sync.Mutex{},
		involvedObjects: make(map[string]map[string]*v1.InvolvedObject),
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

		if config.Event.Exclusions.Filter(event) {
			change := getChangeFromEvent(event, config.Event.SeverityKeywords)
			if change != nil {
				if err := db.SaveResults(ctx, []v1.ScrapeResult{{Changes: []v1.ChangeResult{*change}}}); err != nil {
					logger.Errorf("error saving config change (event=%s): %v", event.Reason, err)
				}
			}
		}

		t.lock.Lock()
		if _, ok := t.involvedObjects[event.InvolvedObject.Kind]; !ok {
			t.involvedObjects[event.InvolvedObject.Kind] = make(map[string]*v1.InvolvedObject)
		}
		t.involvedObjects[event.InvolvedObject.Kind][event.GetUID()] = event.InvolvedObject
		t.lock.Unlock()
	}

	return nil
}

// consumeChangeEvents fetches the configs referenced by the changes and saves them.
// It clears the buffer after.
func (t *eventWatcher) consumeChangeEvents(ctx api.ScrapeContext, config v1.Kubernetes, consume consumerFunc) {
	for {
		time.Sleep(refetchEventResourceInterval)

		if len(t.involvedObjects) == 0 {
			continue
		}

		t.lock.Lock()
		var resourceIDs []*v1.InvolvedObject
		for _, resources := range t.involvedObjects {
			for _, r := range resources {
				resourceIDs = append(resourceIDs, r)
			}
		}
		t.lock.Unlock()

		if err := consume(ctx, config, resourceIDs); err != nil {
			logger.Errorf("failed to run scraper %v: %w", ctx.ScrapeConfig().Name, err)
		}

		t.involvedObjects = make(map[string]map[string]*v1.InvolvedObject)
	}
}
