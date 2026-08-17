package kube

import (
	"testing"

	v1 "github.com/flanksource/config-db/api/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestInvolvedObjectGVK(t *testing.T) {
	tests := []struct {
		name    string
		object  v1.InvolvedObject
		want    schema.GroupVersionKind
		wantErr bool
	}{
		{
			name:   "core resource",
			object: v1.InvolvedObject{APIVersion: "v1", Kind: "Pod", Namespace: "default", Name: "nginx"},
			want:   schema.GroupVersionKind{Version: "v1", Kind: "Pod"},
		},
		{
			name:   "grouped resource",
			object: v1.InvolvedObject{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "default", Name: "nginx"},
			want:   schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
		},
		{
			name:   "cluster scoped resource",
			object: v1.InvolvedObject{APIVersion: "v1", Kind: "Node", Name: "worker-1"},
			want:   schema.GroupVersionKind{Version: "v1", Kind: "Node"},
		},
		{
			name:    "missing api version",
			object:  v1.InvolvedObject{Kind: "Pod", Name: "nginx"},
			wantErr: true,
		},
		{
			name:    "api version without version",
			object:  v1.InvolvedObject{APIVersion: "apps/", Kind: "Deployment", Name: "nginx"},
			wantErr: true,
		},
		{
			name:    "invalid api version",
			object:  v1.InvolvedObject{APIVersion: "apps/v1/extra", Kind: "Deployment", Name: "nginx"},
			wantErr: true,
		},
		{
			name:    "missing kind",
			object:  v1.InvolvedObject{APIVersion: "v1", Name: "nginx"},
			wantErr: true,
		},
		{
			name:    "missing name",
			object:  v1.InvolvedObject{APIVersion: "v1", Kind: "Pod"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := involvedObjectGVK(tt.object)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("involvedObjectGVK() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("involvedObjectGVK() unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("involvedObjectGVK() = %v, want %v", got, tt.want)
			}
		})
	}
}
