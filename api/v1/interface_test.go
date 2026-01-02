package v1

import (
	"reflect"
	"sort"
	"testing"

	"github.com/flanksource/commons/collections/set"
)

func newIDSet(ids ...string) set.Set[string] {
	s := set.New[string]()
	for _, id := range ids {
		s.Add(id)
	}
	return s
}

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
				Orphaned: map[string]OrphanedChanges{
					"foo": {Count: 1, IDs: newIDSet("foo-1")},
					"bar": {Count: 2, IDs: newIDSet("bar-1")},
				},
			},
			summary2: ChangeSummary{
				Orphaned: map[string]OrphanedChanges{
					"foo": {Count: 3, IDs: newIDSet("foo-2")},
					"baz": {Count: 4, IDs: newIDSet("baz-1")},
				},
			},
			expected: ChangeSummary{
				Orphaned: map[string]OrphanedChanges{
					"foo": {Count: 4, IDs: newIDSet("foo-1", "foo-2")},
					"bar": {Count: 2, IDs: newIDSet("bar-1")},
					"baz": {Count: 4, IDs: newIDSet("baz-1")},
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
				Orphaned: map[string]OrphanedChanges{
					"foo": {Count: 1, IDs: newIDSet("foo-1")},
					"bar": {Count: 2, IDs: newIDSet("bar-1")},
				},
				Ignored: map[string]int{
					"baz": 3,
					"qux": 4,
				},
			},
			summary2: ChangeSummary{
				Orphaned: map[string]OrphanedChanges{
					"foo":  {Count: 5, IDs: newIDSet("foo-2")},
					"quux": {Count: 6, IDs: newIDSet("quux-1")},
				},
				Ignored: map[string]int{
					"baz":   7,
					"corge": 8,
				},
			},
			expected: ChangeSummary{
				Orphaned: map[string]OrphanedChanges{
					"foo":  {Count: 6, IDs: newIDSet("foo-1", "foo-2")},
					"bar":  {Count: 2, IDs: newIDSet("bar-1")},
					"quux": {Count: 6, IDs: newIDSet("quux-1")},
				},
				Ignored: map[string]int{
					"baz":   10,
					"qux":   4,
					"corge": 8,
				},
			},
		},
		{
			name: "merge summaries with ignored by action",
			summary1: ChangeSummary{
				IgnoredByAction: map[string]map[string]int{
					"ignore": {
						"CreateSomething": 2,
						"UpdateSomething": 1,
					},
				},
			},
			summary2: ChangeSummary{
				IgnoredByAction: map[string]map[string]int{
					"ignore": {
						"CreateSomething": 3,
						"DeleteSomething": 5,
					},
					"skip": {
						"CreateSomething": 1,
					},
				},
			},
			expected: ChangeSummary{
				IgnoredByAction: map[string]map[string]int{
					"ignore": {
						"CreateSomething": 5,
						"UpdateSomething": 1,
						"DeleteSomething": 5,
					},
					"skip": {
						"CreateSomething": 1,
					},
				},
			},
		},
		{
			name: "merge summaries with all fields",
			summary1: ChangeSummary{
				Orphaned: map[string]OrphanedChanges{
					"foo": {Count: 1, IDs: newIDSet("foo-1")},
				},
				Ignored: map[string]int{
					"bar": 2,
				},
				IgnoredByAction: map[string]map[string]int{
					"ignore": {
						"baz": 3,
					},
				},
			},
			summary2: ChangeSummary{
				Orphaned: map[string]OrphanedChanges{
					"foo": {Count: 4, IDs: newIDSet("foo-2")},
				},
				Ignored: map[string]int{
					"bar": 5,
				},
				IgnoredByAction: map[string]map[string]int{
					"ignore": {
						"baz": 6,
					},
				},
			},
			expected: ChangeSummary{
				Orphaned: map[string]OrphanedChanges{
					"foo": {Count: 5, IDs: newIDSet("foo-1", "foo-2")},
				},
				Ignored: map[string]int{
					"bar": 7,
				},
				IgnoredByAction: map[string]map[string]int{
					"ignore": {
						"baz": 9,
					},
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
				got := tt.summary1.Orphaned[k]
				if got.Count != v.Count {
					t.Errorf("Expected %d orphaned changes for %s, got %d", v.Count, k, got.Count)
				}
				gotIDs := got.IDs.ToSlice()
				wantIDs := v.IDs.ToSlice()
				sort.Strings(gotIDs)
				sort.Strings(wantIDs)
				if !reflect.DeepEqual(gotIDs, wantIDs) {
					t.Errorf("Expected orphaned ids %v for %s, got %v", wantIDs, k, gotIDs)
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
			if len(tt.summary1.IgnoredByAction) != len(tt.expected.IgnoredByAction) {
				t.Errorf("Expected %d ignored by action entries, got %d", len(tt.expected.IgnoredByAction), len(tt.summary1.IgnoredByAction))
			}
			for action, changeTypes := range tt.expected.IgnoredByAction {
				gotChangeTypes := tt.summary1.IgnoredByAction[action]
				if len(gotChangeTypes) != len(changeTypes) {
					t.Errorf("Expected %d change types for action %s, got %d", len(changeTypes), action, len(gotChangeTypes))
				}
				for changeType, count := range changeTypes {
					if gotChangeTypes[changeType] != count {
						t.Errorf("Expected %d ignored by action for %s/%s, got %d", count, action, changeType, gotChangeTypes[changeType])
					}
				}
			}
		})
	}
}
