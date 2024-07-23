package changes

import (
	"fmt"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/tests/fixtures/dummy"
	"github.com/flanksource/duty/types"
	"github.com/google/go-cmp/cmp"
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
				Regexp: `name:\s*(?<name>.*?)\s+type:\s*(?<change_type>.*?)\s+is\s+.*\nseverity:\s*(?<severity>\w+)`,
				Mapping: v1.ChangeExtractionMapping{
					Type:     types.ExtractionVar{Value: "health"},
					Summary:  types.ExtractionVar{Expr: "text.substring(0, 25)"},
					Severity: types.ExtractionVar{Expr: "env.severity"},
				},
				Config: []types.EnvVarResourceSelector{
					{
						Type: types.ExtractionVar{Expr: "env.change_type"},
						Name: types.ExtractionVar{Expr: "env.name"},
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

			diff := cmp.Diff(got, tt.expected)
			Expect(diff).To(BeEmpty())
		})
	}
})
