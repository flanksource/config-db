package kubernetes

import (
	"fmt"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

func getSourceFromEvent(obj *unstructured.Unstructured) string {
	val, ok := obj.Object["source"].(map[string]any)
	if !ok {
		return ""
	}

	host, ok := val["host"]
	if !ok {
		host = "<unknown-host>"
	}

	component, ok := val["component"]
	if !ok {
		component = "<unknown-component>"
	}

	return fmt.Sprintf("kubernetes/component=%s,host=%s", component, host)
}

func getDetailsFromEvent(obj *unstructured.Unstructured) map[string]any {
	details := make(map[string]any)

	for k, v := range obj.Object {
		if k == "involvedObject" {
			continue
		}

		details[k] = v
	}

	return details
}

func getChangeFromEvent(obj *unstructured.Unstructured, severityKeywords v1.SeverityKeywords) *v1.ChangeResult {
	eventCreatedAt := obj.GetCreationTimestamp().Time
	involvedObject, ok := obj.Object["involvedObject"].(map[string]any)
	if !ok {
		return nil
	}

	var (
		reason, _             = obj.Object["reason"].(string)
		message, _            = obj.Object["message"].(string)
		uid, _                = involvedObject["uid"].(string)
		involvedObjectKind, _ = involvedObject["kind"].(string)
	)

	return &v1.ChangeResult{
		ChangeType:       reason,
		CreatedAt:        &eventCreatedAt,
		Details:          getDetailsFromEvent(obj),
		ExternalChangeID: string(obj.GetUID()),
		ExternalID:       uid,
		ExternalType:     ExternalTypePrefix + involvedObjectKind,
		Severity:         getSeverityFromReason(reason, severityKeywords.Error, severityKeywords.Warn),
		Source:           getSourceFromEvent(obj),
		Summary:          message,
	}
}
