package kubernetes

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func Test_getSourceFromEvent(t *testing.T) {
	type args struct {
		obj *unstructured.Unstructured
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "simple", args: args{
				&unstructured.Unstructured{
					Object: map[string]interface{}{
						"source": map[string]interface{}{
							"component": "kubelet",
							"host":      "minikube",
						},
					},
				},
			},
			want: "kubernetes/component=kubelet,host=minikube",
		},
		{
			name: "empty", args: args{
				&unstructured.Unstructured{
					Object: map[string]interface{}{
						"source": map[string]interface{}{},
					},
				},
			},
			want: "kubernetes/",
		},
		{
			name: "nil source", args: args{
				&unstructured.Unstructured{
					Object: map[string]interface{}{
						"source": nil,
					},
				},
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getSourceFromEvent(tt.args.obj); got != tt.want {
				t.Errorf("getSourceFromEvent() = %v, want %v", got, tt.want)
			}
		})
	}
}
