package kubernetes

import (
	"encoding/json"
	"strings"

	"github.com/Jeffail/gabs/v2"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/types"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type argo struct{}

func init() {
	childlookupHooks = append(childlookupHooks, argo{})
	propertyLookupHooks = append(propertyLookupHooks, argo{})
}

func (argo argo) ChildLookupHook(ctx *KubernetesContext, obj *unstructured.Unstructured) []v1.ConfigExternalKey {
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
			ctx.Tracef("error marshaling status.resources for argo app[%s/%s]: %v", obj.GetNamespace(), obj.GetName(), err)
		} else {
			for _, resource := range ars {
				extID := KubernetesAlias(ctx.ClusterName(), resource.Kind, resource.Namespace, resource.Name)
				children = append([]v1.ConfigExternalKey{{
					Type:       ConfigTypePrefix + resource.Kind,
					ExternalID: extID,
				}}, children...)

				childExternalIDToAppID[extID] = string(obj.GetUID())
			}
		}
	}

	return children
}

var childExternalIDToAppID = make(map[string]string) // argo child external id -> argo app id
var appIDToRepo = make(map[string]string)            // argo app id -> repo

func (a argo) PropertyLookupHook(ctx *KubernetesContext, obj *unstructured.Unstructured) types.Properties {
	if strings.HasPrefix(obj.GetAPIVersion(), "argoproj.io") && obj.GetKind() == "Application" {
		repoURL, _, _ := unstructured.NestedString(obj.Object, "spec", "source", "repoURL")
		if repoURL == "" {
			return nil
		}

		appIDToRepo[string(obj.GetUID())] = repoURL
		return types.Properties{
			{
				Name:  "git_url",
				Label: "Git URL",
				Text:  repoURL,
			},
		}
	}

	extID := KubernetesAlias(ctx.ClusterName(), obj.GetKind(), obj.GetNamespace(), obj.GetName())
	if repo := appIDToRepo[childExternalIDToAppID[extID]]; repo != "" {
		return types.Properties{
			{
				Name:  "git_url",
				Label: "Git URL",
				Text:  repo,
			},
		}
	}
	return nil
}
