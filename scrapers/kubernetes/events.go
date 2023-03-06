package kubernetes

import (
	"fmt"
	"strings"

	v1 "github.com/flanksource/config-db/api/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var (
	defaultErrKeywords  = []string{"failed", "error"}
	defaultWarnKeywords = []string{"backoff", "unhealthy", "nodeoutofdisk", "nodeoutofmemory", "nodeoutofpid"}
)

func getSeverityFromReason(reason string, errKeywords, warnKeywords []string) string {
	if len(errKeywords) == 0 {
		errKeywords = defaultErrKeywords
	}

	if len(warnKeywords) == 0 {
		warnKeywords = defaultWarnKeywords
	}

	for _, k := range errKeywords {
		if strings.Contains(strings.ToLower(reason), k) {
			return "error"
		}
	}

	for _, k := range warnKeywords {
		if strings.Contains(strings.ToLower(reason), k) {
			return "warn"
		}
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
