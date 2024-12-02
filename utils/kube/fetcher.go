package kube

import (
	"fmt"

	"github.com/flanksource/duty/context"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	v1 "github.com/flanksource/config-db/api/v1"
)

func FetchInvolvedObjects(ctx context.Context, iObjs []v1.InvolvedObject) ([]*unstructured.Unstructured, error) {
	var objGrouped = map[schema.GroupVersionKind][]v1.InvolvedObject{}
	for _, iObj := range iObjs {
		gv, _ := schema.ParseGroupVersion(iObj.APIVersion)
		gvk := schema.GroupVersionKind{
			Group:   gv.Group,
			Version: gv.Version,
			Kind:    iObj.Kind,
		}

		objGrouped[gvk] = append(objGrouped[gvk], iObj)
	}

	// TODO: add concurrency
	var output []*unstructured.Unstructured
	for gvk, iObjs := range objGrouped {
		objs, err := fetchGVKObjects(ctx, gvk, iObjs...)
		if err != nil {
			return nil, err
		}

		output = append(output, objs...)
	}

	return output, nil
}

// fetchGVKObjects fetches the given objects belonging to the same GVK.
func fetchGVKObjects(ctx context.Context, gvk schema.GroupVersionKind, objs ...v1.InvolvedObject) ([]*unstructured.Unstructured, error) {
	client, err := ctx.KubernetesDynamicClient().GetClientByGroupVersionKind(gvk.Group, gvk.Version, gvk.Kind)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client for %s: %w", gvk, err)
	}

	var output []*unstructured.Unstructured
	for _, iObj := range objs {
		obj, err := client.Namespace(iObj.Namespace).Get(ctx, iObj.Name, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				// The object might have been deleted. we don't want to fail the rest.
				continue
			}

			return nil, fmt.Errorf("failed to get object %s %s/%s: %w", gvk, iObj.Namespace, iObj.Name, err)
		}

		output = append(output, obj)
	}

	return output, nil
}
