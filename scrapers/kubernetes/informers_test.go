package kubernetes

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/emirpasic/gods/queues/priorityqueue"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestPqComparator(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		Items    []QueueItem
		expected []string
	}{
		{
			name: "add should have higher priority than update",
			Items: []QueueItem{
				{
					Operation: QueueItemOperationAdd,
					Obj:       getUnstructured("Pod", "a", now),
				},
				{
					Operation: QueueItemOperationUpdate,
					Obj:       getUnstructured("Pod", "b", now),
				},
			},
			expected: []string{"a", "b"},
		},
		{
			name: "update should have higher priority than delete",
			Items: []QueueItem{
				{
					Operation: QueueItemOperationUpdate,
					Obj:       getUnstructured("Pod", "a", now),
				},
				{
					Operation: QueueItemOperationDelete,
					Obj:       getUnstructured("Pod", "b", now),
				},
			},
			expected: []string{"a", "b"},
		},
		{
			name: "same operation should compare by kind - Namespace vs Pod",
			Items: []QueueItem{
				{
					Operation: QueueItemOperationAdd,
					Obj:       getUnstructured("Namespace", "a", now),
				},
				{
					Operation: QueueItemOperationAdd,
					Obj:       getUnstructured("Pod", "b", now),
				},
			},
			expected: []string{"a", "b"},
		},
		{
			name: "same operation and kind should compare by timestamp - earlier first",
			Items: []QueueItem{
				{
					Operation: QueueItemOperationAdd,
					Obj:       getUnstructured("Pod", "a", now.Add(-1*time.Hour)),
				},
				{
					Operation: QueueItemOperationAdd,
					Obj:       getUnstructured("Pod", "b", now),
				},
			},
			expected: []string{"a", "b"},
		},
		{
			name: "namespace comes first even before a pod created earlier",
			Items: []QueueItem{
				{
					Operation: QueueItemOperationAdd,
					Obj:       getUnstructured("Pod", "a", now.Add(-1*time.Hour)),
				},
				{
					Operation: QueueItemOperationAdd,
					Obj:       getUnstructured("Namespace", "b", now),
				},
			},
			expected: []string{"b", "a"},
		},
		{
			name: "operation priority should override kind priority",
			Items: []QueueItem{
				{
					Operation: QueueItemOperationAdd,
					Obj:       getUnstructured("Pod", "a", now),
				},
				{
					Operation: QueueItemOperationDelete,
					Obj:       getUnstructured("Namespace", "b", now),
				},
			},
			expected: []string{"a", "b"},
		},
		{
			name: "unknown kind should use default priority",
			Items: []QueueItem{
				{
					Operation: QueueItemOperationAdd,
					Obj:       getUnstructured("Canary", "a", now),
				},
				{
					Operation: QueueItemOperationAdd,
					Obj:       getUnstructured("Pod", "b", now),
				},
			},
			expected: []string{"a", "b"},
		},
		{
			name: "events with managed fields",
			Items: []QueueItem{
				{
					Operation: QueueItemOperationAdd,
					Obj:       getUnstructuredEvent("Event", "a", now.Add(-2*time.Hour), now.Add(time.Hour)), // created ealier but re-created later
				},
				{
					Operation: QueueItemOperationAdd,
					Obj:       getUnstructuredEvent("Event", "b", now.Add(-time.Hour), now.Add(time.Minute)),
				},
			},
			expected: []string{"b", "a"},
		},
		{
			name: "identical objects of unknown kind with owner reference",
			Items: []QueueItem{
				{
					Operation: QueueItemOperationAdd,
					Obj:       getUnstructuredWithOwnerRef("Custom", "a", now, metav1.OwnerReference{Name: "http-canary", Kind: "Canary"}),
				},
				{
					Operation: QueueItemOperationAdd,
					Obj:       getUnstructured("Custom", "b", now),
				},
			},
			expected: []string{"a", "b"},
		},
		{
			name: "same objects",
			Items: []QueueItem{
				{
					Operation: QueueItemOperationAdd,
					Obj:       getUnstructuredWithResourceVersion("Pod", "a", "2c6a2f24-0199-435d-83a6-bd3f6d18d06d", "3", now.Add(-1*time.Hour)),
				},
				{
					Operation: QueueItemOperationAdd,
					Obj:       getUnstructuredWithResourceVersion("Pod", "a", "2c6a2f24-0199-435d-83a6-bd3f6d18d06d", "1", now.Add(-1*time.Hour)),
				},
				{
					Operation: QueueItemOperationAdd,
					Obj:       getUnstructuredWithResourceVersion("Pod", "a", "2c6a2f24-0199-435d-83a6-bd3f6d18d06d", "2", now.Add(-1*time.Hour)),
				},
				{
					Operation: QueueItemOperationAdd,
					Obj:       getUnstructuredWithResourceVersion("Pod", "a", "2c6a2f24-0199-435d-83a6-bd3f6d18d06d", "4", now.Add(-1*time.Hour)),
				},
			},
			expected: []string{"a-1", "a-2", "a-3", "a-4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := priorityqueue.NewWith(pqComparator)

			for _, item := range tt.Items {
				q.Enqueue(&item)
			}

			var result []string
			for {
				v, ok := q.Dequeue()
				if !ok {
					break
				}

				item := v.(*QueueItem)

				resourceVersion, ok, _ := unstructured.NestedString(item.Obj.Object, "metadata", "resourceVersion")
				if ok {
					result = append(result, fmt.Sprintf("%s-%s", item.Obj.GetName(), resourceVersion))
				} else {
					result = append(result, item.Obj.GetName())
				}
			}

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("Test %s failed\nExpected: %v\nGot: %v", tt.name, tt.expected, result)
			}
		})
	}
}

func getUnstructuredEvent(kind, name string, creationTimestamp, recreationTimestamp time.Time) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"kind": kind,
			"metadata": map[string]any{
				"name":              name,
				"creationTimestamp": creationTimestamp.Format(time.RFC3339),
				"managedFields": []any{
					map[string]any{
						"operation": "Update",
						"time":      recreationTimestamp.Format(time.RFC3339),
					},
				},
			},
		},
	}
}

func getUnstructuredWithResourceVersion(kind, name, uid, version string, creationTimestamp time.Time) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"kind": kind,
			"metadata": map[string]any{
				"uid":               uid,
				"name":              name,
				"creationTimestamp": creationTimestamp.Format(time.RFC3339),
				"resourceVersion":   version,
			},
		},
	}
}

func getUnstructured(kind, name string, creationTimestamp time.Time) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"kind": kind,
			"metadata": map[string]any{
				"uid":               uuid.NewString(),
				"name":              name,
				"creationTimestamp": creationTimestamp.Format(time.RFC3339),
			},
		},
	}
}

func getUnstructuredWithOwnerRef(kind, name string, creationTimestamp time.Time, ownerRef metav1.OwnerReference) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"kind": kind,
			"metadata": map[string]any{
				"uid":               uuid.NewString(),
				"name":              name,
				"creationTimestamp": creationTimestamp.Format(time.RFC3339),
				"ownerReferences": []any{
					map[string]any{
						"name": ownerRef.Name,
						"kind": ownerRef.Kind,
					},
				},
			},
		},
	}
}
