package kubernetes

import (
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// map<namespace><kind><name>: <id>
type ResourceIDMap map[string]map[string]map[string]string

type ResourceIDMapContainer struct {
	mu   sync.RWMutex
	data ResourceIDMap
}

func (t *ResourceIDMapContainer) Set(namespace, kind, name, id string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.data[namespace] == nil {
		t.data[namespace] = make(map[string]map[string]string)
	}
	if t.data[namespace][kind] == nil {
		t.data[namespace][kind] = make(map[string]string)
	}
	t.data[namespace][kind][name] = id
}

func (t *ResourceIDMapContainer) Get(namespace, kind, name string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if kinds, ok := t.data[namespace]; ok {
		if names, ok := kinds[kind]; ok {
			if id, ok := names[name]; ok {
				return id
			}
		}
	}

	return ""
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
	resourceIDMap := make(map[string]map[string]map[string]string)
	for _, obj := range objs {
		if resourceIDMap[obj.GetNamespace()] == nil {
			resourceIDMap[obj.GetNamespace()] = make(map[string]map[string]string)
		}
		if resourceIDMap[obj.GetNamespace()][obj.GetKind()] == nil {
			resourceIDMap[obj.GetNamespace()][obj.GetKind()] = make(map[string]string)
		}
		resourceIDMap[obj.GetNamespace()][obj.GetKind()][obj.GetName()] = string(obj.GetUID())
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
		output[k] = make(map[string]map[string]string)
		for k2, v2 := range v {
			output[k][k2] = make(map[string]string)
			for k3, v3 := range v2 {
				output[k][k2][k3] = v3
			}
		}
	}

	// Then, update or add data from latest
	for k, v := range latest {
		if _, ok := output[k]; !ok {
			output[k] = make(map[string]map[string]string)
		}
		for k2, v2 := range v {
			if _, ok := output[k][k2]; !ok {
				output[k][k2] = make(map[string]string)
			}
			for k3, v3 := range v2 {
				output[k][k2][k3] = v3
			}
		}
	}

	return output
}
