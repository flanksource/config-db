package kubernetes

import (
	"strings"

	v1 "github.com/flanksource/config-db/api"
	"github.com/flanksource/duty/query"
	"github.com/flanksource/duty/types"
	"github.com/google/uuid"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type flux struct{}

func isKustomizationObject(obj *unstructured.Unstructured) bool {
	if obj.GetKind() == "Kustomization" && strings.HasPrefix(obj.GetAPIVersion(), "kustomize.toolkit.fluxcd.io") {
		return true
	}
	return false
}

func init() {
	parentlookupHooks = append(parentlookupHooks, flux{})
	propertyLookupHooks = append(propertyLookupHooks, flux{})
}

func (flux flux) ParentLookupHook(ctx *KubernetesContext, obj *unstructured.Unstructured) []v1.ConfigExternalKey {
	helmName := obj.GetLabels()["helm.toolkit.fluxcd.io/name"]
	helmNamespace := obj.GetLabels()["helm.toolkit.fluxcd.io/namespace"]
	if helmName != "" && helmNamespace != "" {
		return []v1.ConfigExternalKey{{
			Type: ConfigTypePrefix + "HelmRelease",
			ExternalID: lo.CoalesceOrEmpty(
				ctx.GetID(helmNamespace, "HelmRelease", helmName),
				KubernetesAlias(ctx.ClusterName(), "HelmRelease", helmNamespace, helmName)),
		}}
	}

	kustomizeName := obj.GetLabels()["kustomize.toolkit.fluxcd.io/name"]
	kustomizeNamespace := obj.GetLabels()["kustomize.toolkit.fluxcd.io/namespace"]
	// Kustomization objects should not have Kustomization parents
	if kustomizeName != "" && kustomizeNamespace != "" && !isKustomizationObject(obj) {
		return []v1.ConfigExternalKey{{
			Type: ConfigTypePrefix + "Kustomization",
			ExternalID: lo.CoalesceOrEmpty(
				ctx.GetID(kustomizeNamespace, "Kustomization", kustomizeName),
				KubernetesAlias(ctx.ClusterName(), "Kustomization", kustomizeNamespace, kustomizeName)),
		}}
	}

	return nil
}

func (f flux) PropertyLookupHook(ctx *KubernetesContext, obj *unstructured.Unstructured) types.Properties {
	kustomizeName := obj.GetLabels()["kustomize.toolkit.fluxcd.io/name"]
	kustomizeNamespace := obj.GetLabels()["kustomize.toolkit.fluxcd.io/namespace"]
	if kustomizeName == "" || kustomizeNamespace == "" {
		return nil
	}

	id, err := uuid.Parse(string(obj.GetUID()))
	if err != nil {
		return nil
	}
	s, err := query.GetGitOpsSource(ctx.Context, id)
	if err != nil || s.Git.URL == "" {
		return nil
	}

	return types.Properties{
		{
			Name:  "git_url",
			Label: "Git URL",
			Text:  s.Git.URL,
		},
		{
			Name:  "git_file",
			Label: "Git File",
			Text:  s.Kustomize.File,
		},
		{
			Name:  "git_source",
			Label: "Git Source",
			Text:  s.Git.Link,
		},
	}
}
