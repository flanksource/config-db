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
	}{
		{
			name: "Test with empty ScrapeResult",
			input: v1.ScrapeResult{
				Changes: []v1.ChangeResult{},
			},
			expect: []v1.ChangeResult{},
		},
		{
			name: "Test with one exact matching rule",
			input: v1.ScrapeResult{
				Changes: []v1.ChangeResult{
					{ChangeType: "AddTags"},
					{ChangeType: "WipeDevice"},
				},
			},
			expect: []v1.ChangeResult{
				{ChangeType: "AddTags"},
				{ChangeType: "WipeDevice", Action: v1.Delete},
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
		{
			name: "Test with unrecognized action",
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
			name: "Test with empty action",
			input: v1.ScrapeResult{
				Changes: []v1.ChangeResult{
					{ChangeType: ""},
				},
			},
			expect: []v1.ChangeResult{
				{ChangeType: ""},
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
