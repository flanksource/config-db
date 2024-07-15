package kubernetes

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

// map<namespace><kind><name>: <id>
type ResourceIDMap map[string]map[string]map[string]string

func (t ResourceIDMap) Set(namespace, kind, name, id string) {
	if t[namespace] == nil {
		t[namespace] = make(map[string]map[string]string)
	}
	if t[namespace][kind] == nil {
		t[namespace][kind] = make(map[string]string)
	}
	t[namespace][kind][name] = id
}

func (t ResourceIDMap) Get(namespace, kind, name string) string {
	if kinds, ok := t[namespace]; ok {
		if names, ok := kinds[kind]; ok {
			if id, ok := names[name]; ok {
				return id
			}
		}
	}

	return ""
}

func getResourceIDsFromObjs(objs []*unstructured.Unstructured) ResourceIDMap {
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

	return resourceIDMap
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
