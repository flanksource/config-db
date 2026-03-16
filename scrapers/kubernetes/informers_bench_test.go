package kubernetes

import (
	"encoding/json"
	"fmt"
	"runtime"
	"testing"
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	corev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

const (
	benchNumPods   = 5000
	benchNumEvents = 5000
	benchNumCRDs   = 1000
)

// benchSink prevents the compiler from optimizing away benchmark results.
var benchSink any

// ---------------------------------------------------------------------------
// Data generators
// ---------------------------------------------------------------------------

// benchGenTypedPod builds a realistic corev1.Pod with 2 containers, labels,
// annotations, env vars, resource limits, volumes, and status conditions.
func benchGenTypedPod(i int) *corev1.Pod {
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("pod-%d", i),
			Namespace: "default",
			UID:       types.UID(fmt.Sprintf("uid-pod-%d", i)),
			Labels: map[string]string{
				"app":        fmt.Sprintf("app-%d", i%50),
				"team":       fmt.Sprintf("team-%d", i%10),
				"env":        "production",
				"version":    "v1.2.3",
				"managed-by": "helm",
			},
			Annotations: map[string]string{
				"kubectl.kubernetes.io/last-applied-configuration": `{"apiVersion":"v1","kind":"Pod","metadata":{"labels":{"app":"nginx"}}}`,
				"prometheus.io/scrape":                             "true",
				"prometheus.io/port":                               "8080",
			},
			CreationTimestamp: metav1.Time{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute)},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1",
				Kind:       "ReplicaSet",
				Name:       fmt.Sprintf("deploy-%d-abc123", i%100),
				UID:        types.UID(fmt.Sprintf("uid-rs-%d", i%100)),
			}},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: fmt.Sprintf("registry.example.com/app-%d:v1.2.3", i%50),
					Ports: []corev1.ContainerPort{
						{ContainerPort: 8080, Protocol: corev1.ProtocolTCP},
						{ContainerPort: 9090, Protocol: corev1.ProtocolTCP},
					},
					Env: []corev1.EnvVar{
						{Name: "DB_HOST", Value: "postgres.default.svc"},
						{Name: "DB_PORT", Value: "5432"},
						{Name: "LOG_LEVEL", Value: "info"},
						{Name: "SECRET_KEY", ValueFrom: &corev1.EnvVarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "app-secrets"},
								Key:                  "secret-key",
							},
						}},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    apiresource.MustParse("100m"),
							corev1.ResourceMemory: apiresource.MustParse("128Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    apiresource.MustParse("500m"),
							corev1.ResourceMemory: apiresource.MustParse("512Mi"),
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{Name: "config", MountPath: "/etc/config", ReadOnly: true},
						{Name: "data", MountPath: "/data"},
					},
				},
				{
					Name:  "sidecar",
					Image: "envoyproxy/envoy:v1.28",
					Ports: []corev1.ContainerPort{
						{ContainerPort: 15001, Protocol: corev1.ProtocolTCP},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    apiresource.MustParse("50m"),
							corev1.ResourceMemory: apiresource.MustParse("64Mi"),
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{Name: "config", VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: "app-config"},
					},
				}},
				{Name: "data", VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				}},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Time{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}},
				{Type: corev1.PodInitialized, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Time{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}},
				{Type: corev1.ContainersReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Time{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}},
			},
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:         "main",
					Ready:        true,
					RestartCount: 0,
					State:        corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.Time{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}},
					Image:        fmt.Sprintf("registry.example.com/app-%d:v1.2.3", i%50),
					ImageID:      "docker-pullable://registry.example.com/app@sha256:abc123def456",
				},
				{
					Name:         "sidecar",
					Ready:        true,
					RestartCount: 0,
					State:        corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: metav1.Time{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}},
					Image:        "envoyproxy/envoy:v1.28",
					ImageID:      "docker-pullable://envoyproxy/envoy@sha256:789abc",
				},
			},
			PodIP:  fmt.Sprintf("10.0.%d.%d", i/256, i%256),
			HostIP: fmt.Sprintf("192.168.1.%d", i%255+1),
		},
	}
}

func benchGenTypedEvent(i int) *corev1.Event {
	return &corev1.Event{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Event"},
		ObjectMeta: metav1.ObjectMeta{
			Name:              fmt.Sprintf("event-%d", i),
			Namespace:         "default",
			UID:               types.UID(fmt.Sprintf("uid-event-%d", i)),
			CreationTimestamp: metav1.Time{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second)},
		},
		InvolvedObject: corev1.ObjectReference{
			APIVersion: "v1",
			Kind:       "Pod",
			Name:       fmt.Sprintf("pod-%d", i%benchNumPods),
			Namespace:  "default",
			UID:        types.UID(fmt.Sprintf("uid-pod-%d", i%benchNumPods)),
		},
		Reason:         "Scheduled",
		Message:        fmt.Sprintf("Successfully assigned default/pod-%d to node-%d", i%benchNumPods, i%10),
		Type:           "Normal",
		Source:         corev1.EventSource{Component: "default-scheduler", Host: fmt.Sprintf("node-%d", i%10)},
		FirstTimestamp: metav1.Time{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second)},
		LastTimestamp:  metav1.Time{Time: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)},
		Count:          int32(i%10 + 1),
	}
}

// benchToUnstructured converts a typed object to *unstructured.Unstructured
// via JSON round-trip, producing identical data for apples-to-apples comparison.
func benchToUnstructured(obj any) *unstructured.Unstructured {
	data, err := json.Marshal(obj)
	if err != nil {
		panic(fmt.Sprintf("benchToUnstructured: marshal failed: %v", err))
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		panic(fmt.Sprintf("benchToUnstructured: unmarshal failed: %v", err))
	}
	return &unstructured.Unstructured{Object: m}
}

func benchGenUnstructuredCRD(i int) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "canaries.flanksource.com/v1",
			"kind":       "Canary",
			"metadata": map[string]any{
				"name":              fmt.Sprintf("canary-%d", i),
				"namespace":         "default",
				"uid":               fmt.Sprintf("uid-canary-%d", i),
				"creationTimestamp": "2026-01-01T00:00:00Z",
				"labels": map[string]any{
					"app":  fmt.Sprintf("app-%d", i%20),
					"team": fmt.Sprintf("team-%d", i%5),
				},
				"annotations": map[string]any{
					"flanksource.com/owner": fmt.Sprintf("team-%d", i%5),
				},
			},
			"spec": map[string]any{
				"interval": int64(30),
				"http": []any{
					map[string]any{
						"name":          fmt.Sprintf("http-check-%d", i),
						"url":           fmt.Sprintf("https://api.example.com/health/%d", i),
						"method":        "GET",
						"timeout":       int64(5000),
						"responseCodes": []any{int64(200), int64(201)},
						"headers": map[string]any{
							"Authorization": "Bearer token-xxx",
							"Accept":        "application/json",
						},
					},
				},
			},
			"status": map[string]any{
				"status":    "Healthy",
				"lastCheck": "2026-01-01T01:00:00Z",
				"conditions": []any{
					map[string]any{
						"type":               "Ready",
						"status":             "True",
						"lastTransitionTime": "2026-01-01T00:00:00Z",
					},
				},
				"checkStatuses": []any{
					map[string]any{
						"name":     fmt.Sprintf("http-check-%d", i),
						"status":   true,
						"duration": int64(150),
						"message":  "OK",
					},
				},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Helper: pre-generate all benchmark objects
// ---------------------------------------------------------------------------

type benchObject struct {
	obj      any
	resource v1.KubernetesResourceToWatch
}

// benchGenTypedObjects returns 10k objects (5k Pods + 5k Events) as typed structs.
// Typed informers cannot handle CRDs, so CRDs are excluded.
func benchGenTypedObjects() []benchObject {
	objects := make([]benchObject, 0, benchNumPods+benchNumEvents)
	podResource := v1.KubernetesResourceToWatch{ApiVersion: "v1", Kind: "Pod"}
	eventResource := v1.KubernetesResourceToWatch{ApiVersion: "v1", Kind: "Event"}
	for i := 0; i < benchNumPods; i++ {
		objects = append(objects, benchObject{obj: benchGenTypedPod(i), resource: podResource})
	}
	for i := 0; i < benchNumEvents; i++ {
		objects = append(objects, benchObject{obj: benchGenTypedEvent(i), resource: eventResource})
	}
	return objects
}

// benchGenDynamicObjects returns 11k objects (5k Pods + 5k Events + 1k CRDs) as unstructured.
func benchGenDynamicObjects() []benchObject {
	objects := make([]benchObject, 0, benchNumPods+benchNumEvents+benchNumCRDs)
	podResource := v1.KubernetesResourceToWatch{ApiVersion: "v1", Kind: "Pod"}
	eventResource := v1.KubernetesResourceToWatch{ApiVersion: "v1", Kind: "Event"}
	crdResource := v1.KubernetesResourceToWatch{ApiVersion: "canaries.flanksource.com/v1", Kind: "Canary"}
	for i := 0; i < benchNumPods; i++ {
		objects = append(objects, benchObject{obj: benchToUnstructured(benchGenTypedPod(i)), resource: podResource})
	}
	for i := 0; i < benchNumEvents; i++ {
		objects = append(objects, benchObject{obj: benchToUnstructured(benchGenTypedEvent(i)), resource: eventResource})
	}
	for i := 0; i < benchNumCRDs; i++ {
		objects = append(objects, benchObject{obj: benchGenUnstructuredCRD(i), resource: crdResource})
	}
	return objects
}

// ---------------------------------------------------------------------------
// CPU Benchmarks: Event Processing
//
// Measures the per-event cost of converting informer objects to unstructured.
//   TypedInformer:   typed struct → json.Marshal → json.Unmarshal → map (unavoidable)
//   DynamicInformer: type assert + DeepCopy (no serialization)
// ---------------------------------------------------------------------------

func BenchmarkEventProcessing(b *testing.B) {
	typedObjects := benchGenTypedObjects()
	dynamicObjects := benchGenDynamicObjects()

	b.Run("TypedInformer", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			o := typedObjects[i%len(typedObjects)]
			result, _ := getUnstructuredFromInformedObj(o.resource, o.obj)
			benchSink = result
		}
	})

	b.Run("DynamicInformer", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			o := dynamicObjects[i%len(dynamicObjects)]
			u := o.obj.(*unstructured.Unstructured).DeepCopy()
			u.SetKind(o.resource.Kind)
			u.SetAPIVersion(o.resource.ApiVersion)
			benchSink = u
		}
	})
}

// ---------------------------------------------------------------------------
// Memory Benchmarks: Informer Cache Footprint
//
// Measures the total heap cost of holding all watched objects in the informer's
// internal store (map[string]interface{}).
//
// The typed informer stores compact Go structs (e.g. *corev1.Pod).
// The dynamic informer stores *unstructured.Unstructured (nested map[string]any).
// ---------------------------------------------------------------------------

func BenchmarkCacheMemory(b *testing.B) {
	b.Run("TypedInformer", func(b *testing.B) {
		runtime.GC()
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)

		cache := make(map[string]any, benchNumPods+benchNumEvents)
		for i := 0; i < benchNumPods; i++ {
			cache[fmt.Sprintf("default/pod-%d", i)] = benchGenTypedPod(i)
		}
		for i := 0; i < benchNumEvents; i++ {
			cache[fmt.Sprintf("default/event-%d", i)] = benchGenTypedEvent(i)
		}

		runtime.GC()
		runtime.GC()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)

		totalObjects := float64(benchNumPods + benchNumEvents)
		b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc)/(1024*1024), "heap-MB")
		b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc)/totalObjects, "bytes/object")
		runtime.KeepAlive(cache)
	})

	b.Run("DynamicInformer", func(b *testing.B) {
		runtime.GC()
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)

		cache := make(map[string]any, benchNumPods+benchNumEvents+benchNumCRDs)
		for i := 0; i < benchNumPods; i++ {
			cache[fmt.Sprintf("default/pod-%d", i)] = benchToUnstructured(benchGenTypedPod(i))
		}
		for i := 0; i < benchNumEvents; i++ {
			cache[fmt.Sprintf("default/event-%d", i)] = benchToUnstructured(benchGenTypedEvent(i))
		}
		for i := 0; i < benchNumCRDs; i++ {
			cache[fmt.Sprintf("default/canary-%d", i)] = benchGenUnstructuredCRD(i)
		}

		runtime.GC()
		runtime.GC()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)

		totalObjects := float64(benchNumPods + benchNumEvents + benchNumCRDs)
		b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc)/(1024*1024), "heap-MB")
		b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc)/totalObjects, "bytes/object")
		runtime.KeepAlive(cache)
	})
}

// ---------------------------------------------------------------------------
// CPU Benchmarks: Deserialization (initial List simulation)
//
// Measures the cost of deserializing a JSON byte slice into:
//   - A typed Go struct (what the typed informer does, though it normally uses protobuf)
//   - A map[string]any (what the dynamic informer does)
//
// Note: the typed informer actually uses protobuf on the wire which is faster
// than JSON. This benchmark uses JSON for both, so the typed result here is a
// conservative (slower) estimate of real-world typed informer performance.
// ---------------------------------------------------------------------------

func BenchmarkDeserialization(b *testing.B) {
	// Pre-generate JSON blobs from typed objects so both paths decode identical bytes.
	type jsonBlob struct {
		data   []byte
		isPod  bool // true = Pod, false = Event
	}

	blobs := make([]jsonBlob, 0, benchNumPods+benchNumEvents)
	for i := 0; i < benchNumPods; i++ {
		data, _ := json.Marshal(benchGenTypedPod(i))
		blobs = append(blobs, jsonBlob{data: data, isPod: true})
	}
	for i := 0; i < benchNumEvents; i++ {
		data, _ := json.Marshal(benchGenTypedEvent(i))
		blobs = append(blobs, jsonBlob{data: data, isPod: false})
	}

	// CRD blobs for the dynamic benchmark
	crdBlobs := make([][]byte, benchNumCRDs)
	for i := 0; i < benchNumCRDs; i++ {
		crdBlobs[i], _ = json.Marshal(benchGenUnstructuredCRD(i).Object)
	}

	b.Run("Typed_JSON", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			blob := blobs[i%len(blobs)]
			if blob.isPod {
				var pod corev1.Pod
				_ = json.Unmarshal(blob.data, &pod)
				benchSink = &pod
			} else {
				var event corev1.Event
				_ = json.Unmarshal(blob.data, &event)
				benchSink = &event
			}
		}
	})

	b.Run("Unstructured_JSON", func(b *testing.B) {
		allBlobs := make([][]byte, 0, len(blobs)+len(crdBlobs))
		for _, blob := range blobs {
			allBlobs = append(allBlobs, blob.data)
		}
		allBlobs = append(allBlobs, crdBlobs...)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var m map[string]any
			_ = json.Unmarshal(allBlobs[i%len(allBlobs)], &m)
			benchSink = &unstructured.Unstructured{Object: m}
		}
	})
}
