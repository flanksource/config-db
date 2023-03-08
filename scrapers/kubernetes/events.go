package kubernetes

import (
	"fmt"
	"sort"
	"strings"

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
