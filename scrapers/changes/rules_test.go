package changes

import (
	"reflect"
	"testing"

	v1 "github.com/flanksource/config-db/api/v1"
)

func TestProcessRules(t *testing.T) {
	tests := []struct {
		name   string
		input  v1.ScrapeResult
		expect []v1.ChangeResult
		rules  []v1.ChangeMapping
		err    bool
	}{
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
			name: "Test Action: one exact matching rule",
			input: v1.ScrapeResult{
				Changes: []v1.ChangeResult{
					{ChangeType: "AddTags"},
					{ChangeType: "DeleteUser"},
				},
			},
			expect: []v1.ChangeResult{
				{ChangeType: "AddTags"},
				{ChangeType: "DeleteUser", Action: v1.Delete},
			},
		},
		{
			name: "Test Action: multiple matching rule",
			input: v1.ScrapeResult{
				Changes: []v1.ChangeResult{
					{ChangeType: "ActivateUser", Action: "Creation"},
					{ChangeType: "DeleteUserProfile"},
					{ChangeType: "TerminateInstances"},
					{ChangeType: "ContainerRemoval"},
				},
			},
			expect: []v1.ChangeResult{
				{ChangeType: "ActivateUser", Action: "Creation"},
				{ChangeType: "DeleteUserProfile", Action: v1.Delete},
				{ChangeType: "TerminateInstances", Action: v1.Delete},
				{ChangeType: "ContainerRemoval"},
			},
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
		{
			name: "Test Type & Summary | multiple change results",
			input: v1.ScrapeResult{
				Type: "HelmRelease",
				Changes: []v1.ChangeResult{
					{ChangeType: "diff", Patches: `{"status": {"failures": 0}}`},
					{ChangeType: "diff", Patches: `{"status": {"failures": 1}}`},
				},
			},
			expect: []v1.ChangeResult{
				{ChangeType: "diff", Patches: `{"status": {"failures": 0}}`},
				{
					ChangeType: "HelmReconcileFailed",
					Patches:    `{"status": {"failures": 1}}`,
					Summary:    "Reconcile failed 1",
				},
			},
		},
		{
			name: "Test Type, Summary & Action | all-in-one",
			input: v1.ScrapeResult{
				Type: "HelmRelease",
				Changes: []v1.ChangeResult{
					{ChangeType: "diff", Patches: `{"status": {"failures": 0}}`},
					{ChangeType: "diff", Patches: `{"status": {"failures": 1}}`},
					{ChangeType: "DeleteUser"},
				},
			},
			expect: []v1.ChangeResult{
				{ChangeType: "diff", Patches: `{"status": {"failures": 0}}`},
				{
					ChangeType: "HelmReconcileFailed",
					Patches:    `{"status": {"failures": 1}}`,
					Summary:    "Reconcile failed 1",
				},
				{ChangeType: "DeleteUser", Action: v1.Delete},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ProcessRules(&tt.input, tt.rules...); err != nil {
				if !tt.err {
					t.Errorf("unexpected Error: %v", err)
				}

				return
			}

			if !reflect.DeepEqual(tt.input.Changes, tt.expect) {
				t.Errorf("ProcessRules() = %v, want %v", tt.input.Changes, tt.expect)
			}
		})
	}
}
