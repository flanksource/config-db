package kube

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

var fetchDelayBuckets = []float64{10, 50, 100, 500, 1_000, 5_000, 10_000, 30_000, 60_000}

func listAllCRDs(ctx api.ScrapeContext) ([]string, error) {
	k8s, err := ctx.Kubernetes()
	if err != nil {
		return nil, fmt.Errorf("error fetching k8s client: %w", err)
	}
	cs, err := clientset.NewForConfig(k8s.RestConfig())
	if err != nil {
		return nil, fmt.Errorf("error creating api extension clientset: %w", err)
	}
	allCRDs, err := cs.ApiextensionsV1().CustomResourceDefinitions().List(ctx.Context, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("error fetching all crds from clientset: %w", err)
	}
	var crds []string
	for _, crd := range allCRDs.Items {
		crds = append(crds, crd.GetName())
	}
	return crds, nil
}

func FetchInvolvedObjects(ctx api.ScrapeContext, iObjs []v1.InvolvedObject) ([]*unstructured.Unstructured, error) {
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(ctx.Properties().Int("kubernetes.get.concurrency", 10))

	ctx.Context.Context.Context = egCtx

	var mu sync.Mutex
	var output []*unstructured.Unstructured

	k8s, err := ctx.Kubernetes()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch kubernetes client: %w", err)
	}

	for _, iObj := range iObjs {
		gvk, err := involvedObjectGVK(iObj)
		if err != nil {
			ctx.Logger.V(3).Infof("skipping invalid involved object: %v", err)
			continue
		}

		client, err := k8s.GetClientByGroupVersionKind(ctx, gvk.Group, gvk.Version, gvk.Kind)
		if err != nil {
			// We suspect if this happens we might be on the wrong k8s context
			// so we list all CRDs in job history error
			if strings.Contains(err.Error(), "no matches for") {
				crdList, err2 := listAllCRDs(ctx)
				if err2 != nil {
					ctx.JobHistory().AddErrorf("error listing existing crds: %v", err2)
				} else {
					ctx.JobHistory().AddErrorf("failed to create dynamic client %s: %v, existing crds: %s", gvk, err, strings.Join(crdList, ","))
				}
			}

			// Skip this client and continue with others
			continue
		}

		eg.Go(func() error {
			found, err := fetchObject(ctx, client, iObj)
			if err != nil {
				return err
			} else if found != nil {
				mu.Lock()
				output = append(output, found)
				mu.Unlock()
			}

			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return output, nil
}

func involvedObjectGVK(iObj v1.InvolvedObject) (schema.GroupVersionKind, error) {
	if strings.TrimSpace(iObj.APIVersion) == "" {
		return schema.GroupVersionKind{}, fmt.Errorf("missing apiVersion for %q", iObj.Name)
	}
	if strings.TrimSpace(iObj.Kind) == "" {
		return schema.GroupVersionKind{}, fmt.Errorf("missing kind for %q", iObj.Name)
	}
	if strings.TrimSpace(iObj.Name) == "" {
		return schema.GroupVersionKind{}, fmt.Errorf("missing name for kind %q", iObj.Kind)
	}

	gv, err := schema.ParseGroupVersion(iObj.APIVersion)
	if err != nil {
		return schema.GroupVersionKind{}, fmt.Errorf("invalid apiVersion %q for %s/%s: %w", iObj.APIVersion, iObj.Kind, iObj.Name, err)
	}
	if gv.Version == "" {
		return schema.GroupVersionKind{}, fmt.Errorf("apiVersion %q has no version for %s/%s", iObj.APIVersion, iObj.Kind, iObj.Name)
	}

	return gv.WithKind(iObj.Kind), nil
}

func fetchObject(ctx api.ScrapeContext, client dynamic.NamespaceableResourceInterface, iObj v1.InvolvedObject) (*unstructured.Unstructured, error) {
	start := time.Now()
	obj, err := client.Namespace(iObj.Namespace).Get(ctx, iObj.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("failed to get object %s/%s: %w", iObj.Namespace, iObj.Name, err)
	}

	ctx.Histogram("kubernetes_object_get", fetchDelayBuckets,
		"scraper_id", ctx.ScraperID(),
		"kind", obj.GetKind(),
	).Record(time.Duration(time.Since(start).Milliseconds()))

	return obj, nil
}
