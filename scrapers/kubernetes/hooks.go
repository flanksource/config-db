package kubernetes

import (
	v1 "github.com/flanksource/config-db/api/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type OnObject interface {
	// OnObject is called when a new object is observed, return true to skip the object
	OnObject(ctx *KubernetesContext, obj *unstructured.Unstructured) (bool, map[string]string, error)
}

type ParentLookupHook interface {
	ParentLookupHook(ctx *KubernetesContext, obj *unstructured.Unstructured) []v1.ConfigExternalKey
}

type ChildLookupHook interface {
	ChildLookupHook(ctx *KubernetesContext, obj *unstructured.Unstructured) []v1.ConfigExternalKey
}

type AliasLookupHook interface {
	AliasLookupHook(ctx *KubernetesContext, obj *unstructured.Unstructured) []string
}

var childlookupHooks []ChildLookupHook
var parentlookupHooks []ParentLookupHook
var aliaslookupHooks []AliasLookupHook
var onObjectHooks []OnObject

func OnObjectHooks(ctx *KubernetesContext, obj *unstructured.Unstructured) (bool, map[string]string, error) {
	labels := make(map[string]string)
	for _, hook := range onObjectHooks {
		skip, _labels, err := hook.OnObject(ctx, obj)
		for k, v := range _labels {
			labels[k] = v
		}
		if err != nil {
			return false, labels, err
		}
		if skip {
			return true, labels, nil
		}
	}
	return false, labels, nil
}

func ParentLookupHooks(ctx *KubernetesContext, obj *unstructured.Unstructured) []v1.ConfigExternalKey {
	parents := []v1.ConfigExternalKey{}
	for _, hook := range parentlookupHooks {
		parents = append(hook.ParentLookupHook(ctx, obj), parents...)
	}
	return parents
}

func ChildLookupHooks(ctx *KubernetesContext, obj *unstructured.Unstructured) []v1.ConfigExternalKey {
	children := []v1.ConfigExternalKey{}
	for _, hook := range childlookupHooks {
		children = append(hook.ChildLookupHook(ctx, obj), children...)
	}
	return children
}

func AliasLookupHooks(ctx *KubernetesContext, obj *unstructured.Unstructured) []string {
	var alias []string
	for _, hook := range aliaslookupHooks {
		alias = append(hook.AliasLookupHook(ctx, obj), alias...)
	}
	return alias
}
