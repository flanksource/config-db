package kubernetes

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// InvolvedObject represents a Kubernetes InvolvedObject object
type InvolvedObject struct {
	UID       string `json:"uid,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

// Event represents a Kubernetes Event object
type Event struct {
	Reason         string          `json:"reason,omitempty"`
	Message        string          `json:"message,omitempty"`
	InvolvedObject *InvolvedObject `json:"involvedObject,omitempty"`
}

func (t *Event) FromObjMap(obj map[string]interface{}) error {
	eventJSON, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("failed to marshal event object: %v", err)
	}

	if err := json.Unmarshal(eventJSON, t); err != nil {
		return fmt.Errorf("failed to unmarshal event object: %v", err)
	}

	return nil
}

func getSeverityFromReason(reason string, errKeywords, warnKeywords []string) string {
	if utils.MatchItems(reason, errKeywords...) {
		return "error"
	}

	if utils.MatchItems(reason, warnKeywords...) {
		return "warn"
	}

	return ""
}

func getSourceFromEvent(obj *unstructured.Unstructured) string {
	val, ok := obj.Object["source"].(map[string]any)
	if !ok {
		return ""
	}

	keyVals := make([]string, 0, len(val))
	for k, v := range val {
		keyVals = append(keyVals, fmt.Sprintf("%s=%s", k, v))
	}

	sort.Slice(keyVals, func(i, j int) bool { return keyVals[i] < keyVals[j] })
	return fmt.Sprintf("kubernetes/%s", strings.Join(keyVals, ","))
}

func getDetailsFromEvent(obj *unstructured.Unstructured) map[string]any {
	details := make(map[string]any)

	for k, v := range obj.Object {
		switch k {
		case "involvedObject":
			continue

		case "metadata":
			if metadata, ok := v.(map[string]any); ok {
				delete(metadata, "managedFields")
			}
		}

		details[k] = v
	}

	return details
}

func getChangeFromEvent(obj *unstructured.Unstructured, severityKeywords v1.SeverityKeywords) *v1.ChangeResult {
	eventCreatedAt := obj.GetCreationTimestamp().Time

	var event Event
	if err := event.FromObjMap(obj.Object); err != nil {
		logger.Errorf("failed to parse event: %v", err)
		return nil
	}

	if event.InvolvedObject == nil {
		logger.Debugf("event has no involved object: %v", event)
		return nil
	}

	_, err := uuid.Parse(event.InvolvedObject.UID)
	if err != nil {
		event.InvolvedObject.UID = fmt.Sprintf("Kubernetes/%s/%s/%s", event.InvolvedObject.Kind, event.InvolvedObject.Namespace, event.InvolvedObject.Name)
	}

	return &v1.ChangeResult{
		ChangeType:       event.Reason,
		CreatedAt:        &eventCreatedAt,
		Details:          getDetailsFromEvent(obj),
		ExternalChangeID: string(obj.GetUID()),
		ExternalID:       event.InvolvedObject.UID,
		ConfigType:       ConfigTypePrefix + event.InvolvedObject.Kind,
		Severity:         getSeverityFromReason(event.Reason, severityKeywords.Error, severityKeywords.Warn),
		Source:           getSourceFromEvent(obj),
		Summary:          event.Message,
	}
}
