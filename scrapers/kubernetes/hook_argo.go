package kubernetes

import (
	"encoding/json"
	"strings"

	"github.com/Jeffail/gabs/v2"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type argo struct{}

func init() {
	childlookupHooks = append(childlookupHooks, argo{})
}

func (argo argo) ChildLookupHook(ctx *KubernetesContext, obj unstructured.Unstructured) []v1.ConfigExternalKey {
	children := []v1.ConfigExternalKey{}
	// Argo Applications have children references
	if strings.HasPrefix(obj.GetAPIVersion(), "argoproj.io") && obj.GetKind() == "Application" {
		o := gabs.Wrap(obj.Object)

		type argoResourceRef struct {
			Kind      string `json:"kind"`
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		}
		var ars []argoResourceRef
		if err := json.Unmarshal(o.S("status", "resources").Bytes(), &ars); err != nil {
			logger.Tracef("error marshaling status.resources for argo app[%s/%s]: %v", obj.GetNamespace(), obj.GetName(), err)
		} else {
			for _, resource := range ars {
				children = append([]v1.ConfigExternalKey{{
					Type:       ConfigTypePrefix + resource.Kind,
					ExternalID: alias(resource.Kind, resource.Namespace, resource.Name),
				}}, children...)
			}
		}
	}

	return children
}
