package kubernetes

import (
	"reflect"
	"testing"
	"time"

	"github.com/emirpasic/gods/queues/priorityqueue"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestPqComparator(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		a        QueueItem
		b        QueueItem
		expected []string
	}{
		{
			name: "add should have higher priority than update",
			a: QueueItem{
				Operation: "add",
				Obj:       getUnstructured("Pod", "a", now),
			},
			b: QueueItem{
				Operation: "update",
				Obj:       getUnstructured("Pod", "b", now),
			},
			expected: []string{"a", "b"},
		},
		{
			name: "update should have higher priority than delete",
			a: QueueItem{
				Operation: "update",
				Obj:       getUnstructured("Pod", "a", now),
			},
			b: QueueItem{
				Operation: "delete",
				Obj:       getUnstructured("Pod", "b", now),
			},
			expected: []string{"a", "b"},
		},
		{
			name: "same operation should compare by kind - Namespace vs Pod",
			a: QueueItem{
				Operation: "add",
				Obj:       getUnstructured("Namespace", "a", now),
			},
			b: QueueItem{
				Operation: "add",
				Obj:       getUnstructured("Pod", "b", now),
			},
			expected: []string{"a", "b"},
		},
		{
			name: "same operation and kind should compare by timestamp - earlier first",
			a: QueueItem{
				Operation: "add",
				Obj:       getUnstructured("Pod", "a", now.Add(-1*time.Hour)),
			},
			b: QueueItem{
				Operation: "add",
				Obj:       getUnstructured("Pod", "b", now),
			},
			expected: []string{"a", "b"},
		},
		{
			name: "namespace comes first even before a pod created earlier",
			a: QueueItem{
				Operation: "add",
				Obj:       getUnstructured("Pod", "a", now.Add(-1*time.Hour)),
			},
			b: QueueItem{
				Operation: "add",
				Obj:       getUnstructured("Namespace", "b", now),
			},
			expected: []string{"b", "a"},
		},
		{
			name: "operation priority should override kind priority",
			a: QueueItem{
				Operation: "add",
				Obj:       getUnstructured("Pod", "a", now),
			},
			b: QueueItem{
				Operation: "delete",
				Obj:       getUnstructured("Namespace", "b", now),
			},
			expected: []string{"a", "b"},
		},
		{
			name: "unknown kind should use default priority",
			a: QueueItem{
				Operation: "add",
				Obj:       getUnstructured("Canary", "a", now),
			},
			b: QueueItem{
				Operation: "add",
				Obj:       getUnstructured("Pod", "b", now),
			},
			expected: []string{"a", "b"},
		},
		{
			name: "events with managed fields",
			a: QueueItem{
				Operation: "add",
				Obj:       getUnstructuredEvent("Event", "a", now.Add(-2*time.Hour), now.Add(time.Hour)), // created ealier but re-created later
			},
			b: QueueItem{
				Operation: "add",
				Obj:       getUnstructuredEvent("Event", "b", now.Add(-time.Hour), now.Add(time.Minute)),
			},
			expected: []string{"b", "a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := priorityqueue.NewWith(pqComparator)
			q.Enqueue(&tt.a)
			q.Enqueue(&tt.b)

			var result []string
			for {
				v, ok := q.Dequeue()
				if !ok {
					break
				}

				item := v.(*QueueItem)
				result = append(result, item.Obj.GetName())
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

func getUnstructured(kind, name string, creationTimestamp time.Time) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"kind": kind,
			"metadata": map[string]any{
				"name":              name,
				"creationTimestamp": creationTimestamp.Format(time.RFC3339),
			},
		},
	}
}
