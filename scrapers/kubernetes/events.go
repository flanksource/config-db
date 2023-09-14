package kubernetes

import (
	"fmt"
	"sort"
	"strings"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/types"
)

func getSeverityFromReason(reason string, errKeywords, warnKeywords []string) string {
	if utils.MatchItems(reason, errKeywords...) {
		return "error"
	}

	if utils.MatchItems(reason, warnKeywords...) {
		return "warn"
	}

	return ""
}

func getSourceFromEvent(event v1.KubernetesEvent) string {
	keyVals := make([]string, 0, len(event.Source))
	for k, v := range event.Source {
		keyVals = append(keyVals, fmt.Sprintf("%s=%s", k, v))
	}

	sort.Slice(keyVals, func(i, j int) bool { return keyVals[i] < keyVals[j] })
	return fmt.Sprintf("kubernetes/%s", strings.Join(keyVals, ","))
}

func getDetailsFromEvent(event v1.KubernetesEvent) map[string]any {
	details, err := event.AsMap()
	if err != nil {
		logger.Errorf("failed to convert event to map: %v", err)
		return nil
	}

	delete(details, "involvedObject")

	if metadata, ok := details["metadata"].(map[string]any); ok {
		delete(metadata, "managedFields")
	}

	return details
}

func getChangeFromEvent(event v1.KubernetesEvent, severityKeywords v1.SeverityKeywords) *v1.ChangeResult {
	_, err := uuid.Parse(string(event.InvolvedObject.UID))
	if err != nil {
		event.InvolvedObject.UID = types.UID(fmt.Sprintf("Kubernetes/%s/%s/%s", event.InvolvedObject.Kind, event.InvolvedObject.Namespace, event.InvolvedObject.Name))
	}

	return &v1.ChangeResult{
		ChangeType:       event.Reason,
		CreatedAt:        &event.Metadata.CreationTimestamp.Time,
		Details:          getDetailsFromEvent(event),
		ExternalChangeID: event.GetUID(),
		ExternalID:       string(event.InvolvedObject.UID),
		ConfigType:       ConfigTypePrefix + event.InvolvedObject.Kind,
		Severity:         getSeverityFromReason(event.Reason, severityKeywords.Error, severityKeywords.Warn),
		Source:           getSourceFromEvent(event),
		Summary:          event.Message,
	}
}
