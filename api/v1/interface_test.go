package v1

import (
	"testing"
)

func TestChangeSummary_Merge(t *testing.T) {
	tests := []struct {
		name     string
		summary1 ChangeSummary
		summary2 ChangeSummary
		expected ChangeSummary
	}{
		{
			name:     "merge empty summaries",
			summary1: ChangeSummary{},
			summary2: ChangeSummary{},
			expected: ChangeSummary{},
		},
		{
			name: "merge summaries with orphaned changes",
			summary1: ChangeSummary{
				Orphaned: map[string]int{
					"foo": 1,
					"bar": 2,
				},
			},
			summary2: ChangeSummary{
				Orphaned: map[string]int{
					"foo": 3,
					"baz": 4,
				},
			},
			expected: ChangeSummary{
				Orphaned: map[string]int{
					"foo": 4,
					"bar": 2,
					"baz": 4,
				},
			},
		},
		{
			name: "merge summaries with ignored changes",
			summary1: ChangeSummary{
				Ignored: map[string]int{
					"foo": 1,
					"bar": 2,
				},
			},
			summary2: ChangeSummary{
				Ignored: map[string]int{
					"foo": 3,
					"baz": 4,
				},
			},
			expected: ChangeSummary{
				Ignored: map[string]int{
					"foo": 4,
					"bar": 2,
					"baz": 4,
				},
			},
		},
		{
			name: "merge summaries with both orphaned and ignored changes",
			summary1: ChangeSummary{
				Orphaned: map[string]int{
					"foo": 1,
					"bar": 2,
				},
				Ignored: map[string]int{
					"baz": 3,
					"qux": 4,
				},
			},
			summary2: ChangeSummary{
				Orphaned: map[string]int{
					"foo":  5,
					"quux": 6,
				},
				Ignored: map[string]int{
					"baz":   7,
					"corge": 8,
				},
			},
			expected: ChangeSummary{
				Orphaned: map[string]int{
					"foo":  6,
					"bar":  2,
					"quux": 6,
				},
				Ignored: map[string]int{
					"baz":   10,
					"qux":   4,
					"corge": 8,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.summary1.Merge(tt.summary2)
			if len(tt.summary1.Orphaned) != len(tt.expected.Orphaned) {
				t.Errorf("Expected %d orphaned changes, got %d", len(tt.expected.Orphaned), len(tt.summary1.Orphaned))
			}
			for k, v := range tt.expected.Orphaned {
				if tt.summary1.Orphaned[k] != v {
					t.Errorf("Expected %d orphaned changes for %s, got %d", v, k, tt.summary1.Orphaned[k])
				}
			}
			if len(tt.summary1.Ignored) != len(tt.expected.Ignored) {
				t.Errorf("Expected %d ignored changes, got %d", len(tt.expected.Ignored), len(tt.summary1.Ignored))
			}
			for k, v := range tt.expected.Ignored {
				if tt.summary1.Ignored[k] != v {
					t.Errorf("Expected %d ignored changes for %s, got %d", v, k, tt.summary1.Ignored[k])
				}
			}
		})
	}
}
