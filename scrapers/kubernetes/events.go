package kubernetes

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
	"github.com/flanksource/mapstructure"
	"github.com/google/uuid"
	coreV1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type InvolvedObject coreV1.ObjectReference

// Event represents a Kubernetes Event object
type Event struct {
	Reason         string             `json:"reason,omitempty"`
	Message        string             `json:"message,omitempty"`
	Source         map[string]any     `json:"source,omitempty"`
	Metadata       *metav1.ObjectMeta `json:"metadata,omitempty" mapstructure:"metadata"`
	InvolvedObject *InvolvedObject    `json:"involvedObject,omitempty"`
}

func (t *Event) GetUID() string {
	return string(t.Metadata.UID)
}

func (t *Event) AsMap() (map[string]any, error) {
	eventJSON, err := json.Marshal(t)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal event object: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(eventJSON, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal event object: %v", err)
	}

	return result, nil
}

func (t *Event) FromObj(obj any) error {
	conf := mapstructure.DecoderConfig{
		TagName: "json", // Need to set this to json because when `obj` is v1.Event there's no mapstructure struct tag.
		Result:  t,
	}

	decoder, err := mapstructure.NewDecoder(&conf)
	if err != nil {
		return err
	}

	decoder.Decode(obj)
	return nil
}

func (t *Event) FromObjMap(obj any) error {
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}

	return json.Unmarshal(b, t)
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

func getSourceFromEvent(event Event) string {
	keyVals := make([]string, 0, len(event.Source))
	for k, v := range event.Source {
		keyVals = append(keyVals, fmt.Sprintf("%s=%s", k, v))
	}

	sort.Slice(keyVals, func(i, j int) bool { return keyVals[i] < keyVals[j] })
	return fmt.Sprintf("kubernetes/%s", strings.Join(keyVals, ","))
}

func getDetailsFromEvent(event Event) map[string]any {
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

func getChangeFromEvent(event Event, severityKeywords v1.SeverityKeywords) *v1.ChangeResult {
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
