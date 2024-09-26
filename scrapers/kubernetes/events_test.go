package kubernetes

import (
	"testing"
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_getSourceFromEvent(t *testing.T) {
	tests := []struct {
		name string
		args v1.KubernetesEvent
		want string
	}{
		{
			name: "simple", args: v1.KubernetesEvent{
				Source: map[string]string{
					"component": "kubelet",
					"host":      "minikube",
				},
			},
			want: "kubelet",
		},
		{
			name: "empty", args: v1.KubernetesEvent{
				Source: map[string]string{},
			},
			want: "kubernetes/",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getSourceFromEvent(tt.args); got != tt.want {
				t.Errorf("getSourceFromEvent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEvent_FromObjMap(t *testing.T) {
	t.Run("from object", func(t *testing.T) {
		eventV1 := corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name: "HI",
			},
		}
		var eventFromV1 v1.KubernetesEvent
		if err := eventFromV1.FromObjMap(eventV1); err != nil {
			t.Fatalf("error was not expected %v", err)
		}

		if eventFromV1.Metadata == nil {
			t.Fail()
		}
	})

	t.Run("from map", func(t *testing.T) {
		eventMap := map[string]any{
			"metadata": map[string]any{
				"name":              "HI",
				"namespace":         "default",
				"uid":               "1028a8ac-b028-456c-b3ea-869b9a9fba5f",
				"creationTimestamp": "2020-01-01T00:00:00Z",
			},
		}
		var eventFromMap v1.KubernetesEvent
		if err := eventFromMap.FromObjMap(eventMap); err != nil {
			t.Fatalf("error was not expected %v", err)
		}

		if eventFromMap.Metadata == nil {
			t.Fail()
		}
	})

	t.Run("from map II", func(t *testing.T) {
		eventMap := corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.Time{
					Time: time.Date(1995, 8, 1, 0, 0, 0, 0, time.UTC),
				},
			},
		}

		var expected v1.KubernetesEvent
		if err := expected.FromObjMap(eventMap); err != nil {
			t.Fatalf("error was not expected %v", err)
		}

		if !expected.Metadata.CreationTimestamp.Time.Equal(eventMap.ObjectMeta.CreationTimestamp.Time) {
			t.Fatalf("creation timestamps do not match, expected %v, got %v", eventMap.ObjectMeta.CreationTimestamp.Time, expected.Metadata.CreationTimestamp.Time)
		}
	})
}
