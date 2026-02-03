package changes

import (
	"fmt"

	v1 "github.com/flanksource/config-db/api"
	"github.com/flanksource/duty/tests/fixtures/dummy"
	"github.com/flanksource/duty/types"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Changes Extraction test", Ordered, func() {
	tests := []struct {
		name     string
		rule     v1.ChangeExtractionRule
		text     string
		expected []v1.ChangeResult
		wantErr  bool
	}{
		{
			name: "Basic extraction",
			rule: v1.ChangeExtractionRule{
				Regexp: `name:\s*(?<name>.*?)\s+type:\s*(?<config_type>.*?)\s+is\s+.*\nseverity:\s*(?<severity>\w+)`,
				Mapping: &v1.ChangeExtractionMapping{
					Type:     types.ValueExpression{Value: "health"},
					Summary:  types.ValueExpression{Expr: "text.substring(0, 25)"},
					Severity: types.ValueExpression{Expr: "env.severity"},
				},
				Config: []types.EnvVarResourceSelector{
					{
						Types: []types.ValueExpression{{Expr: "env.config_type"}},
						Name:  types.ValueExpression{Expr: "env.name"},
					},
				},
			},
			text: fmt.Sprintf(`name: %s type: %s is healthy
severity: high`, *dummy.KubernetesNodeA.Name, *dummy.KubernetesNodeA.Type),
			expected: []v1.ChangeResult{
				{
					Source:           "slack",
					Severity:         "high",
					ChangeType:       "health",
					Summary:          "name: node-a type: Kubern",
					ExternalChangeID: "cf08f4ed5a8a0cf9602f47f3d0efb953f64f56c4de58eeef1d54ea1cc43a6559",
					Details: map[string]any{"text": fmt.Sprintf(`name: %s type: %s is healthy
severity: high`, *dummy.KubernetesNodeA.Name, *dummy.KubernetesNodeA.Type)},
					ConfigID: dummy.KubernetesNodeA.ID.String(),
				},
			},
		},
		{
			name: "Defaults from regexp",
			rule: v1.ChangeExtractionRule{
				Regexp: `name:\s*(?<name>.*?)\s+type:\s*(?<config_type>.*?)\s+is\s+.*\nseverity:\s*(?<severity>\w+)`,
				Mapping: &v1.ChangeExtractionMapping{
					Type: types.ValueExpression{Value: "health"},
				},
				Config: []types.EnvVarResourceSelector{
					{
						Types: []types.ValueExpression{{Expr: "env.config_type"}},
						Name:  types.ValueExpression{Expr: "env.name"},
					},
				},
			},
			text: fmt.Sprintf(`name: %s type: %s is healthy
severity: high`, *dummy.KubernetesNodeA.Name, *dummy.KubernetesNodeA.Type),
			expected: []v1.ChangeResult{
				{
					Source:   "slack",
					Severity: "high",
					Details: map[string]any{"text": fmt.Sprintf(`name: %s type: %s is healthy
severity: high`, *dummy.KubernetesNodeA.Name, *dummy.KubernetesNodeA.Type)},
					ChangeType:       "health",
					ExternalChangeID: "cf08f4ed5a8a0cf9602f47f3d0efb953f64f56c4de58eeef1d54ea1cc43a6559",
					ConfigID:         dummy.KubernetesNodeA.ID.String(),
				},
			},
		},
		{
			name: "Defaults from regexp with override",
			rule: v1.ChangeExtractionRule{
				Regexp: `name:\s*(?P<name>[\w-]+)\s*\|\s*config_type:\s*(?P<config_type>[\w:]+)\s*\|\s*severity:\s*(?P<severity>\w+)\s*\|\s*summary:\s*(?P<summary>\w+)\s*\|\s*type:\s*(?P<type>[\w_]+)`,
				Mapping: &v1.ChangeExtractionMapping{
					Severity: types.ValueExpression{Value: "low"},
				},
				Config: []types.EnvVarResourceSelector{
					{
						Types: []types.ValueExpression{{Expr: "env.config_type"}},
						Name:  types.ValueExpression{Expr: "env.name"},
					},
				},
			},
			text: fmt.Sprintf(`name: %s | config_type: %s | severity: high | summary: ishealthy | type: health_update`, *dummy.KubernetesNodeA.Name, *dummy.KubernetesNodeA.Type),
			expected: []v1.ChangeResult{
				{
					Source:           "slack",
					Severity:         "low",
					Summary:          "ishealthy",
					ChangeType:       "health_update",
					Details:          map[string]any{"text": fmt.Sprintf(`name: %s | config_type: %s | severity: high | summary: ishealthy | type: health_update`, *dummy.KubernetesNodeA.Name, *dummy.KubernetesNodeA.Type)},
					ExternalChangeID: "a0a8e63a3dc458fc5eac682c7f87335f1927a2353d095ece0870301e3ffb95f8",
					ConfigID:         dummy.KubernetesNodeA.ID.String(),
				},
			},
		},
	}

	for _, tt := range tests {
		It(tt.name, func() {
			got, err := MapChanges(DefaultContext, tt.rule, tt.text)
			if !tt.wantErr {
				Expect(err).To(BeNil())
			} else {
				Expect(err).NotTo(BeNil())
			}

			diff := cmp.Diff(got, tt.expected, cmpopts.IgnoreUnexported(v1.ChangeResult{}))
			Expect(diff).To(BeEmpty())
		})
	}
})
