package kubernetes

import (
	"context"
	"fmt"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func WatchEvents(ctx *v1.ScrapeContext, config v1.Kubernetes) error {
	logger.Infof("Watching kubernetes events: %v", config)

	watcher, err := ctx.Kubernetes.CoreV1().Events(config.Namespace).Watch(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to watch events: %w", err)
	}
	defer watcher.Stop()

	for watchEvent := range watcher.ResultChan() {
		var event Event
		if err := event.FromObjMap(watchEvent); err != nil {
			logger.Errorf("failed to unmarshal event: %v", err)
			continue
		}

		if utils.MatchItems(event.Reason, config.Event.Exclusions...) {
			continue
		}

		change := getChangeFromEvent(event, config.Event.SeverityKeywords)
		if change != nil {
			logger.Infof("There is a change %v", change)
		}
	}

	return nil
}
