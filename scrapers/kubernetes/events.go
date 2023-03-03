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
			return "error"
		}
	}

	return ""
}

func getSourceFromEvent(obj *unstructured.Unstructured) string {
	val, ok := obj.Object["source"].(map[string]any)
	if !ok {
		return ""
	}

	return fmt.Sprintf("kubernetes/%s/%s", val["host"], val["component"])
}

func getChangeFromEvent(obj *unstructured.Unstructured, severityKeywords v1.SeverityKeywords) *v1.ChangeResult {
	eventCreatedAt := obj.GetCreationTimestamp().Time
	involvedObject, ok := obj.Object["involvedObject"].(map[string]any)
	if !ok {
		return nil
	}

	reason := obj.Object["reason"].(string)

	return &v1.ChangeResult{
		ChangeType:       reason,
		CreatedAt:        &eventCreatedAt,
		Details:          involvedObject,
		ExternalChangeID: string(obj.GetUID()),
		ExternalID:       involvedObject["uid"].(string),
		ExternalType:     ExternalTypePrefix + involvedObject["kind"].(string),
		Severity:         getSeverityFromReason(reason, severityKeywords.Error, severityKeywords.Warn),
		Source:           getSourceFromEvent(obj),
		Summary:          obj.Object["message"].(string),
	}
}
