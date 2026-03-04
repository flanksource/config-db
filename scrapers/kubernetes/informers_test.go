package kubernetes

import (
	"fmt"
	"time"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/hash"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func drainQueue(q *collections.Queue[*QueueItem]) []string {
	var result []string
	for {
		item, ok := q.Dequeue()
		if !ok {
			break
		}
		resourceVersion, ok, _ := unstructured.NestedString(item.Obj.Object, "metadata", "resourceVersion")
		if ok {
			result = append(result, fmt.Sprintf("%s-%s", item.Obj.GetName(), resourceVersion))
		} else {
			result = append(result, item.Obj.GetName())
		}
	}
	return result
}

func newQueue(name string) *collections.Queue[*QueueItem] {
	q, err := collections.NewQueue(collections.QueueOpts[*QueueItem]{
		Metrics: collections.MetricsOpts[*QueueItem]{
			Name: fmt.Sprintf("m_%s", hash.Sha256Hex(name)[:10]),
		},
		Comparator: pqComparator,
		Equals:     queueItemIsEqual,
		Dedupe:     true,
	})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return q
}

var _ = Describe("PqComparator", func() {
	var now time.Time

	BeforeEach(func() {
		now = time.Now()
	})

	DescribeTable("priority ordering",
		func(items []QueueItem, expected []string) {
			q := newQueue(CurrentSpecReport().FullText())
			for i := range items {
				q.Enqueue(&items[i])
			}
			Expect(drainQueue(q)).To(Equal(expected))
		},
		Entry("add should have higher priority than update",
			[]QueueItem{
				{Operation: QueueItemOperationAdd, Obj: getUnstructured("Pod", "a", time.Now())},
				{Operation: QueueItemOperationUpdate, Obj: getUnstructured("Pod", "b", time.Now())},
			},
			[]string{"a", "b"},
		),
		Entry("update should have higher priority than delete",
			[]QueueItem{
				{Operation: QueueItemOperationUpdate, Obj: getUnstructured("Pod", "a", time.Now())},
				{Operation: QueueItemOperationDelete, Obj: getUnstructured("Pod", "b", time.Now())},
			},
			[]string{"a", "b"},
		),
		Entry("same operation should compare by kind - Namespace vs Pod",
			[]QueueItem{
				{Operation: QueueItemOperationAdd, Obj: getUnstructured("Namespace", "a", time.Now())},
				{Operation: QueueItemOperationAdd, Obj: getUnstructured("Pod", "b", time.Now())},
			},
			[]string{"a", "b"},
		),
		Entry("namespace comes first even before a pod created earlier",
			[]QueueItem{
				{Operation: QueueItemOperationAdd, Obj: getUnstructured("Pod", "a", time.Now().Add(-1*time.Hour))},
				{Operation: QueueItemOperationAdd, Obj: getUnstructured("Namespace", "b", time.Now())},
			},
			[]string{"b", "a"},
		),
		Entry("operation priority should override kind priority",
			[]QueueItem{
				{Operation: QueueItemOperationAdd, Obj: getUnstructured("Pod", "a", time.Now())},
				{Operation: QueueItemOperationDelete, Obj: getUnstructured("Namespace", "b", time.Now())},
			},
			[]string{"a", "b"},
		),
		Entry("unknown kind should use default priority",
			[]QueueItem{
				{Operation: QueueItemOperationAdd, Obj: getUnstructured("Canary", "a", time.Now())},
				{Operation: QueueItemOperationAdd, Obj: getUnstructured("Pod", "b", time.Now())},
			},
			[]string{"a", "b"},
		),
		Entry("re-enqueue should have same priority as add",
			[]QueueItem{
				{Operation: QueueItemOperationUpdate, Obj: getUnstructured("Pod", "a", time.Now())},
				{Operation: QueueItemOperationReEnqueue, Obj: getUnstructured("Pod", "b", time.Now())},
			},
			[]string{"b", "a"},
		),
	)

	It("same operation and kind should compare by timestamp - earlier first", func() {
		q := newQueue("timestamp-ordering")
		q.Enqueue(&QueueItem{Operation: QueueItemOperationAdd, Obj: getUnstructured("Pod", "a", now.Add(-1*time.Hour))})
		q.Enqueue(&QueueItem{Operation: QueueItemOperationAdd, Obj: getUnstructured("Pod", "b", now)})
		Expect(drainQueue(q)).To(Equal([]string{"a", "b"}))
	})

	It("events with managed fields should order by managed field time", func() {
		q := newQueue("events-managed-fields")
		q.Enqueue(&QueueItem{Operation: QueueItemOperationAdd, Obj: getUnstructuredEvent("Event", "a", now.Add(-2*time.Hour), now.Add(time.Hour))})
		q.Enqueue(&QueueItem{Operation: QueueItemOperationAdd, Obj: getUnstructuredEvent("Event", "b", now.Add(-time.Hour), now.Add(time.Minute))})
		Expect(drainQueue(q)).To(Equal([]string{"b", "a"}))
	})

	It("identical objects of unknown kind with owner reference", func() {
		q := newQueue("owner-ref")
		q.Enqueue(&QueueItem{Operation: QueueItemOperationAdd, Obj: getUnstructuredWithOwnerRef("Custom", "a", now, metav1.OwnerReference{Name: "http-canary", Kind: "Canary"})})
		q.Enqueue(&QueueItem{Operation: QueueItemOperationAdd, Obj: getUnstructured("Custom", "b", now)})
		Expect(drainQueue(q)).To(Equal([]string{"a", "b"}))
	})

	It("same objects should dedupe to highest resource version", func() {
		q := newQueue("same-objects")
		q.Enqueue(&QueueItem{Operation: QueueItemOperationAdd, Obj: getUnstructuredWithResourceVersion("Pod", "a", "2c6a2f24-0199-435d-83a6-bd3f6d18d06d", "3", now.Add(-1*time.Hour))})
		q.Enqueue(&QueueItem{Operation: QueueItemOperationAdd, Obj: getUnstructuredWithResourceVersion("Pod", "a", "2c6a2f24-0199-435d-83a6-bd3f6d18d06d", "1", now.Add(-1*time.Hour))})
		q.Enqueue(&QueueItem{Operation: QueueItemOperationAdd, Obj: getUnstructuredWithResourceVersion("Pod", "a", "2c6a2f24-0199-435d-83a6-bd3f6d18d06d", "2", now.Add(-1*time.Hour))})
		q.Enqueue(&QueueItem{Operation: QueueItemOperationAdd, Obj: getUnstructuredWithResourceVersion("Pod", "a", "2c6a2f24-0199-435d-83a6-bd3f6d18d06d", "4", now.Add(-1*time.Hour))})
		Expect(drainQueue(q)).To(Equal([]string{"a-4"}))
	})
})

func getUnstructuredEvent(kind, name string, creationTimestamp, recreationTimestamp time.Time) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"kind": kind,
			"metadata": map[string]any{
				"uid":              uuid.NewString(),
				"name":             name,
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
				"uid":              uid,
				"name":             name,
				"creationTimestamp": creationTimestamp.Format(time.RFC3339),
				"resourceVersion":  version,
			},
		},
	}
}

func getUnstructured(kind, name string, creationTimestamp time.Time) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"kind": kind,
			"metadata": map[string]any{
				"uid":              uuid.NewString(),
				"name":             name,
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
				"uid":              uuid.NewString(),
				"name":             name,
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
