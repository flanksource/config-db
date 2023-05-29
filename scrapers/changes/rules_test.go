package changes

import (
	"reflect"
	"testing"

	v1 "github.com/flanksource/config-db/api/v1"
)

func TestProcessRules(t *testing.T) {
	// Override the Rules
	Rules = changeRule{
		allRules: map[string]Change{
			"DeleteUser":         {Action: v1.Delete},
			"Delete*":            {Action: v1.Delete},
			"TerminateInstances": {Action: v1.Delete},
		},
	}
	Rules.init()

	tests := []struct {
		name   string
		input  v1.ScrapeResult
		expect []v1.ChangeResult
	}{
		{
			name: "Test with empty ScrapeResult",
			input: v1.ScrapeResult{
				Changes: []v1.ChangeResult{},
			},
			expect: []v1.ChangeResult{},
		},
		{
			name: "Test with one ChangeType matching a rule",
			input: v1.ScrapeResult{
				Changes: []v1.ChangeResult{
					{ChangeType: "DeleteUser"},
					{ChangeType: "TerminateInstances"},
				},
			},
			expect: []v1.ChangeResult{
				{ChangeType: "DeleteUser", Action: v1.Delete},
				{ChangeType: "TerminateInstances", Action: v1.Delete},
			},
		},
		{
			name: "Test wildcard rule",
			input: v1.ScrapeResult{
				Changes: []v1.ChangeResult{
					{ChangeType: "ActivateUser", Action: "Creation"},
					{ChangeType: "DeleteUserProfile"},
					{ChangeType: "TerminateInstances"},
				},
			},
			expect: []v1.ChangeResult{
				{ChangeType: "ActivateUser", Action: "Creation"},
				{ChangeType: "DeleteUserProfile", Action: v1.Delete},
				{ChangeType: "TerminateInstances", Action: v1.Delete},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ProcessRules(tt.input)
			if !reflect.DeepEqual(result, tt.expect) {
				t.Errorf("ProcessRules() = %v, want %v", result, tt.expect)
			}
		})
	}
}
