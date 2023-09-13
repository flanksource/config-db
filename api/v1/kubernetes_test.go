package v1

import (
	"testing"
)

func TestKubernetesConfigExclusions_Filter(t *testing.T) {
	type args struct {
		name      string
		namespace string
		kind      string
		labels    map[string]string
	}
	tests := []struct {
		name          string
		config        KubernetesConfigExclusions
		args          args
		shouldExclude bool
	}{
		{
			name: "exclusion by name",
			config: KubernetesConfigExclusions{
				Names: []string{"junit-*"},
			},
			args: args{
				name: "junit-123",
			},
			shouldExclude: true,
		},
		{
			name: "exclusion by namespace",
			config: KubernetesConfigExclusions{
				Namespaces: []string{"*-canaries"},
			},
			args: args{
				namespace: "customer-canaries",
			},
			shouldExclude: true,
		},
		{
			name: "exclusion by kind",
			config: KubernetesConfigExclusions{
				Kinds: []string{"*Chart"},
			},
			args: args{
				kind: "HelmChart",
			},
			shouldExclude: true,
		},
		{
			name: "exclusion by labels | exact match",
			config: KubernetesConfigExclusions{
				Labels: map[string]string{
					"prod": "env",
				},
			},
			args: args{
				labels: map[string]string{
					"prod": "env",
				},
			},
			shouldExclude: true,
		},
		{
			name: "exclusion by labels | one matches",
			config: KubernetesConfigExclusions{
				Labels: map[string]string{
					"prod":          "env",
					"is-billed":     "true",
					"trace-enabled": "true",
				},
			},
			args: args{
				labels: map[string]string{
					"prod":          "env",
					"trace-enabled": "false",
				},
			},
			shouldExclude: true,
		},
		{
			name:   "no exclusions",
			config: KubernetesConfigExclusions{},
			args: args{
				namespace: "default",
				name:      "test-foo",
			},
			shouldExclude: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.config.Filter(tt.args.name, tt.args.namespace, tt.args.kind, tt.args.labels); got != tt.shouldExclude {
				t.Errorf("KubernetesConfigExclusions.Filter() = %v, want %v", got, tt.shouldExclude)
			}
		})
	}
}
