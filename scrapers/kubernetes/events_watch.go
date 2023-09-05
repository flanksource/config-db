package kubernetes

import (
	"context"
	"fmt"
	"time"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	// eventWatchTimeout is the timeout for watching events
	eventWatchTimeout = time.Second * 10

	// maxBufferSize is the maximum number of events that can be buffered before consuming.
	maxBufferSize = 50

	changesBuffer []*v1.ChangeResult
)

type consumerFunc func(ctx *v1.ScrapeContext, changesBuffer []*v1.ChangeResult)

// WatchEvents watches Kubernetes events for any config changes & fetches
// the referenced config items in batches.
func WatchEvents(ctx *v1.ScrapeContext, config v1.Kubernetes, consume consumerFunc) error {
	logger.Infof("Watching kubernetes events: %v", config)

	watcher, err := ctx.Kubernetes.CoreV1().Events(config.Namespace).Watch(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to watch events: %w", err)
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			consumeChangeEvents(ctx, consume)
			return nil

		case <-time.After(eventWatchTimeout):
			consumeChangeEvents(ctx, consume)

		case watchEvent := <-watcher.ResultChan():
			var event Event
			if err := event.FromObjMap(watchEvent.Object); err != nil {
				logger.Errorf("failed to unmarshal event (id=%s): %v", event.GetUID(), err)
				continue
			}

			logger.Infof("New Event: reason=%s source=%s", event.Reason, event.Source)

			if utils.MatchItems(event.Reason, config.Event.Exclusions...) {
				continue
			}

			change := getChangeFromEvent(event, config.Event.SeverityKeywords)
			if change == nil {
				logger.Debugf("No change detected")
				continue
			}

			changesBuffer = append(changesBuffer, change)
			if len(changesBuffer) >= maxBufferSize {
				consumeChangeEvents(ctx, consume)
			}
		}
	}
}

// consumeChangeEvents fetches the configs referenced by the changes and saves them.
// It clears the buffer after.
func consumeChangeEvents(ctx *v1.ScrapeContext, consume consumerFunc) {
	logger.Infof("Consuming buffer. Len: %d", len(changesBuffer))
	if len(changesBuffer) == 0 {
		return
	}

	consume(ctx, changesBuffer)

	changesBuffer = nil
}
