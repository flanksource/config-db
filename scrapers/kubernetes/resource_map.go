package kubernetes

import (
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type ResourceIDMap map[string]string

type ResourceIDMapContainer struct {
	mu   sync.RWMutex
	data ResourceIDMap
}

func ResourceIDMapKey(namespace, kind, name string) string {
	return namespace + "|" + kind + "|" + name
}

func (t *ResourceIDMapContainer) Set(namespace, kind, name, id string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.data[ResourceIDMapKey(namespace, kind, name)] = id
}

func (t *ResourceIDMapContainer) Get(namespace, kind, name string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.data[ResourceIDMapKey(namespace, kind, name)]
}

type PerClusterResourceIDMap struct {
	mu   sync.Mutex
	data map[string]ResourceIDMap
}

func (t *PerClusterResourceIDMap) Swap(clusterID string, resourceIDMap ResourceIDMap) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.data == nil {
		t.data = make(map[string]ResourceIDMap)
	}

	t.data[clusterID] = resourceIDMap
}

func (t *PerClusterResourceIDMap) MergeAndUpdate(clusterID string, resourceIDMap ResourceIDMap) ResourceIDMap {
	t.mu.Lock()
	defer t.mu.Unlock()

	cached, ok := t.data[clusterID]
	if ok {
		resourceIDMap = mergeResourceIDMap(resourceIDMap, cached)
	}

	if t.data == nil {
		t.data = make(map[string]ResourceIDMap)
	}

	t.data[clusterID] = resourceIDMap
	return resourceIDMap
}

func NewResourceIDMap(objs []*unstructured.Unstructured) *ResourceIDMapContainer {
	resourceIDMap := make(ResourceIDMap)
	for _, obj := range objs {
		resourceIDMap[ResourceIDMapKey(obj.GetNamespace(), obj.GetKind(), obj.GetName())] = string(obj.GetUID())
	}

	return &ResourceIDMapContainer{
		data: resourceIDMap,
		mu:   sync.RWMutex{},
	}
}

func mergeResourceIDMap(latest, cached ResourceIDMap) ResourceIDMap {
	if len(latest) == 0 {
		return cached
	}

	if len(cached) == 0 {
		return latest
	}

	output := make(ResourceIDMap)

	// First, copy all data from cached
	for k, v := range cached {
		output[k] = v
	}

	// Then, update or add data from latest
	for k, v := range latest {
		output[k] = v
	}

	return output
}
