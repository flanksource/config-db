package kubernetes

import (
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("getSourceFromEvent", func() {
	DescribeTable("returns the correct source",
		func(event v1.KubernetesEvent, expected string) {
			Expect(getSourceFromEvent(event)).To(Equal(expected))
		},
		Entry("simple", v1.KubernetesEvent{
			Source: map[string]string{
				"component": "kubelet",
				"host":      "minikube",
			},
		}, "kubelet"),
		Entry("empty", v1.KubernetesEvent{
			Source: map[string]string{},
		}, "kubernetes/"),
	)
})

var _ = Describe("KubernetesEvent.FromObjMap", func() {
	It("populates metadata from a corev1.Event object", func() {
		eventV1 := corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "HI"},
		}
		var event v1.KubernetesEvent
		Expect(event.FromObjMap(eventV1)).To(Succeed())
		Expect(event.Metadata).ToNot(BeNil())
	})

	It("populates metadata from a map", func() {
		eventMap := map[string]any{
			"metadata": map[string]any{
				"name":              "HI",
				"namespace":         "default",
				"uid":               "1028a8ac-b028-456c-b3ea-869b9a9fba5f",
				"creationTimestamp": "2020-01-01T00:00:00Z",
			},
		}
		var event v1.KubernetesEvent
		Expect(event.FromObjMap(eventMap)).To(Succeed())
		Expect(event.Metadata).ToNot(BeNil())
	})

	It("preserves creationTimestamp from a corev1.Event", func() {
		ts := time.Date(1995, 8, 1, 0, 0, 0, 0, time.UTC)
		eventObj := corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.Time{Time: ts},
			},
		}
		var event v1.KubernetesEvent
		Expect(event.FromObjMap(eventObj)).To(Succeed())
		Expect(event.Metadata.CreationTimestamp.Time).To(Equal(ts))
	})
})
