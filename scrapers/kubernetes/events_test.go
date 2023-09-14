package kubernetes

import (
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_getSourceFromEvent(t *testing.T) {
	tests := []struct {
		name string
		args Event
		want string
	}{
		{
			name: "simple", args: Event{
				Source: map[string]interface{}{
					"component": "kubelet",
					"host":      "minikube",
				},
			},
			want: "kubernetes/component=kubelet,host=minikube",
		},
		{
			name: "empty", args: Event{
				Source: map[string]any{},
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
		eventV1 := v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name: "HI",
			},
		}
		var eventFromV1 Event
		if err := eventFromV1.FromObj(eventV1); err != nil {
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
		var eventFromMap Event
		if err := eventFromMap.FromObjMap(eventMap); err != nil {
			t.Fatalf("error was not expected %v", err)
		}

		if eventFromMap.Metadata == nil {
			t.Fail()
		}
	})
}
