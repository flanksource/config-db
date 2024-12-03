package kube

import (
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

var fetchDelayBuckets = []float64{500, 1_000, 3_000, 5_000, 10_000, 20_000, 30_000, 60_000}

func FetchInvolvedObjects(ctx api.ScrapeContext, iObjs []v1.InvolvedObject) ([]*unstructured.Unstructured, error) {
	clientMap := map[schema.GroupVersionKind]dynamic.NamespaceableResourceInterface{}

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(ctx.Properties().Int("kubernetes.get.concurrency", 10))

	ctx.Context.Context.Context = egCtx

	var mu sync.Mutex
	var output []*unstructured.Unstructured

	for _, iObj := range iObjs {
		gv, _ := schema.ParseGroupVersion(iObj.APIVersion)
		gvk := schema.GroupVersionKind{
			Group:   gv.Group,
			Version: gv.Version,
			Kind:    iObj.Kind,
		}

		client, ok := clientMap[gvk]
		if !ok {
			c, err := ctx.KubernetesDynamicClient().GetClientByGroupVersionKind(gvk.Group, gvk.Version, gvk.Kind)
			if err != nil {
				return nil, fmt.Errorf("failed to create dynamic client for %s: %w", gvk, err)
			}

			client = c
			clientMap[gvk] = c
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
