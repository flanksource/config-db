package changes

import (
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TestProcessRules", Ordered, func() {
	tests := []struct {
		name   string
		input  v1.ScrapeResult
		expect []v1.ChangeResult
		rules  []v1.ChangeMapping
		err    bool
	}{
		{
			name: "health mapping - fail",
			input: v1.ScrapeResult{
				Changes: []v1.ChangeResult{{ChangeType: "diff", Patches: ""}},
			},
			expect: []v1.ChangeResult{{ChangeType: "diff", Patches: ""}},
			rules: []v1.ChangeMapping{{
				Type:   "HealthCheckPassed",
				Filter: `change.change_type == 'diff' && jq('try .status.conditions[] | select(.type == "Healthy").message', patch).contains('Health check passed')`,
			}},
		},
		{
			name: "health mapping - fail II",
			input: v1.ScrapeResult{
				Changes: []v1.ChangeResult{{ChangeType: "diff", Patches: `{"status": {}}`}},
			},
			expect: []v1.ChangeResult{{ChangeType: "diff", Patches: `{"status": {}}`}},
			rules: []v1.ChangeMapping{{
				Type:   "HealthCheckPassed",
				Filter: `change.change_type == 'diff' && jq('try .status.conditions[] | select(.type == "Healthy").message', patch).contains('Health check passed')`,
			}},
		},
		{
			name: "health mapping - pass",
			input: v1.ScrapeResult{
				Changes: []v1.ChangeResult{{ChangeType: "diff", Patches: `{"status": {"conditions": [{"type": "Healthy", "message": "Health check passed"}]}}`}},
			},
			expect: []v1.ChangeResult{{ChangeType: "HealthCheckPassed", Patches: `{"status": {"conditions": [{"type": "Healthy", "message": "Health check passed"}]}}`}},
			rules: []v1.ChangeMapping{{
				Type:   "HealthCheckPassed",
				Filter: `change.change_type == 'diff' && jq('try .status.conditions[] | select(.type == "Healthy").message', patch).contains('Health check passed')`,
			}},
		},
		{
			name: "Should error out on bad filter",
			input: v1.ScrapeResult{
				Changes: []v1.ChangeResult{
					{ChangeType: "AddTags"},
				},
			},
			rules: []v1.ChangeMapping{{Filter: "bad filter"}},
			err:   true,
		},
		{
			name: "Test Action: empty ScrapeResult",
			input: v1.ScrapeResult{
				Changes: []v1.ChangeResult{},
			},
			expect: []v1.ChangeResult{},
		},
		{
			name: "Test Action: unrecognized action",
			input: v1.ScrapeResult{
				Changes: []v1.ChangeResult{
					{ChangeType: "UnrecognizedAction"},
				},
			},
			expect: []v1.ChangeResult{
				{ChangeType: "UnrecognizedAction"},
			},
		},
		{
			name: "Test Action: empty action",
			input: v1.ScrapeResult{
				Changes: []v1.ChangeResult{
					{ChangeType: ""},
				},
			},
			expect: []v1.ChangeResult{
				{ChangeType: ""},
			},
		},
		{
			name: "Test Type & Summary | single change result",
			input: v1.ScrapeResult{
				Type: "HelmRelease",
				Changes: []v1.ChangeResult{
					{ChangeType: "diff", Patches: `{"status": {"failures": 0}}`},
				},
			},
			expect: []v1.ChangeResult{
				{ChangeType: "diff", Patches: `{"status": {"failures": 0}}`},
			},
		},
	}

	for _, tt := range tests {
		It(tt.name, func() {
			err := ProcessRules(api.NewScrapeContext(DefaultContext), &tt.input, tt.rules...)
			if tt.err {
				Expect(err).ToNot(BeNil())
			} else {
				Expect(err).To(BeNil())
				Expect(cleanChangeResults(tt.input.Changes)).To(ConsistOf(tt.expect))
			}
		})
	}
})

func cleanChangeResults(changes []v1.ChangeResult) []v1.ChangeResult {
	var cleanChanges []v1.ChangeResult
	for _, c := range changes {
		c.FlushMap()
		cleanChanges = append(cleanChanges, c)
	}
	return cleanChanges
}
