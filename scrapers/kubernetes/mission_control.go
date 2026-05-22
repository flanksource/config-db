package kubernetes

import (
	"sort"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const connectionTypeLabel = "mission-control/connection-type"

var nonConnectionTypeSpecFields = map[string]struct{}{
	"properties":   {},
	"url":          {},
	"port":         {},
	"type":         {},
	"username":     {},
	"password":     {},
	"certificate":  {},
	"insecure_tls": {},
}

// Takes in a mission-control connection object and returns the connection type as a label for the config item
func mcConnectionTypeLabel(obj *unstructured.Unstructured) map[string]string {
	labels := make(map[string]string)

	spec, ok := obj.Object["spec"].(map[string]any)
	if !ok {
		return labels
	}

	// deprecated approach where the type is explicitly defined in the spec
	if t, ok := spec["type"].(string); ok && t != "" {
		labels[connectionTypeLabel] = t
		return labels
	}

	keys := make([]string, 0, len(spec))
	for key, value := range spec {
		if _, ignored := nonConnectionTypeSpecFields[key]; ignored || value == nil {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	if len(keys) > 0 {
		// Connection Objects are expected to have only one connection defined.
		// If more than one is defined, we choose the first one.
		labels[connectionTypeLabel] = keys[0]
	}

	return labels
}
