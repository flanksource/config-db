package kubernetes

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	// eventWatchInterval is the schedule on which new K8s resources are scraped
	// from the events
	eventWatchInterval = time.Second * 5
)

type consumerFunc func(ctx *v1.ScrapeContext, involvedObjects map[string]map[string]*InvolvedObject)

type eventWatcher struct {
	// involvedObjects keeps record of all the involved objects from events
	// by the resource kind.
	involvedObjects map[string]map[string]*InvolvedObject

	lock *sync.Mutex
}

func WatchEvents(ctx *v1.ScrapeContext, config v1.Kubernetes, consume consumerFunc) error {
	watcher := &eventWatcher{
		lock:            &sync.Mutex{},
		involvedObjects: make(map[string]map[string]*InvolvedObject),
	}

	go watcher.consumeChangeEvents(ctx, consume)

	return watcher.Watch(ctx, config)
}

// WatchEvents watches Kubernetes events for any config changes & fetches
// the referenced config items in batches.
func (t *eventWatcher) Watch(ctx *v1.ScrapeContext, config v1.Kubernetes) error {
	logger.Infof("Watching kubernetes events: %v", config)

	watcher, err := ctx.Kubernetes.CoreV1().Events(config.Namespace).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to watch events: %w", err)
	}
	defer watcher.Stop()

	for watchEvent := range watcher.ResultChan() {
		var event Event
		if err := event.FromObjMap(watchEvent.Object); err != nil {
			logger.Errorf("failed to unmarshal event (id=%s): %v", event.GetUID(), err)
			continue
		}

		if event.InvolvedObject == nil {
			continue
		}

		// TODO: Definitely need a new exclusion list in the kubernetes spec to exclude by pattern matching names (junit*, for example)
		if strings.Contains(event.InvolvedObject.Name, "junit") || strings.Contains(event.InvolvedObject.Name, "hello-world") {
			continue
		}

		if !utils.MatchItems(event.Reason, config.Event.Exclusions...) && false {
			change := getChangeFromEvent(event, config.Event.SeverityKeywords)
			if change != nil {
				if err := db.SaveResults(ctx, []v1.ScrapeResult{{Changes: []v1.ChangeResult{*change}}}); err != nil {
					logger.Errorf("error saving config change (event=%s): %v", event.Reason, err)
				}
			}
		}

		t.lock.Lock()
		if _, ok := t.involvedObjects[event.InvolvedObject.Kind]; !ok {
			t.involvedObjects[event.InvolvedObject.Kind] = make(map[string]*InvolvedObject)
		}
		t.involvedObjects[event.InvolvedObject.Kind][event.GetUID()] = event.InvolvedObject
		t.lock.Unlock()
	}

	return nil
}

// consumeChangeEvents fetches the configs referenced by the changes and saves them.
// It clears the buffer after.
func (t *eventWatcher) consumeChangeEvents(ctx *v1.ScrapeContext, consume consumerFunc) {
	for {
		time.Sleep(eventWatchInterval)

		if len(t.involvedObjects) == 0 {
			continue
		}

		t.lock.Lock()
		consume(ctx, t.involvedObjects)
		t.involvedObjects = make(map[string]map[string]*InvolvedObject)
		t.lock.Unlock()
	}
}
