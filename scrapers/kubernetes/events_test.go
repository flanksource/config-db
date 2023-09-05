package kubernetes

import (
	"testing"
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
