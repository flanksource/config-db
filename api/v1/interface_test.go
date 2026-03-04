package v1

import (
	"sort"

	"github.com/flanksource/commons/collections/set"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func newIDSet(ids ...string) set.Set[string] {
	s := set.New[string]()
	for _, id := range ids {
		s.Add(id)
	}
	return s
}

func sortedIDs(s set.Set[string]) []string {
	ids := s.ToSlice()
	sort.Strings(ids)
	return ids
}

var _ = Describe("ChangeSummary", func() {
	DescribeTable("Merge",
		func(summary1, summary2, expected ChangeSummary) {
			summary1.Merge(summary2)

			Expect(summary1.Ignored).To(Equal(expected.Ignored))
			Expect(summary1.IgnoredByAction).To(Equal(expected.IgnoredByAction))
			Expect(len(summary1.Orphaned)).To(Equal(len(expected.Orphaned)))
			for k, v := range expected.Orphaned {
				got := summary1.Orphaned[k]
				Expect(got.Count).To(Equal(v.Count))
				Expect(sortedIDs(got.IDs)).To(Equal(sortedIDs(v.IDs)))
			}
		},
		Entry("merge empty summaries",
			ChangeSummary{},
			ChangeSummary{},
			ChangeSummary{}),
		Entry("merge summaries with orphaned changes",
			ChangeSummary{
				Orphaned: map[string]OrphanedChanges{
					"foo": {Count: 1, IDs: newIDSet("foo-1")},
					"bar": {Count: 2, IDs: newIDSet("bar-1")},
				},
			},
			ChangeSummary{
				Orphaned: map[string]OrphanedChanges{
					"foo": {Count: 3, IDs: newIDSet("foo-2")},
					"baz": {Count: 4, IDs: newIDSet("baz-1")},
				},
			},
			ChangeSummary{
				Orphaned: map[string]OrphanedChanges{
					"foo": {Count: 4, IDs: newIDSet("foo-1", "foo-2")},
					"bar": {Count: 2, IDs: newIDSet("bar-1")},
					"baz": {Count: 4, IDs: newIDSet("baz-1")},
				},
			}),
		Entry("merge summaries with ignored changes",
			ChangeSummary{
				Ignored: map[string]int{"foo": 1, "bar": 2},
			},
			ChangeSummary{
				Ignored: map[string]int{"foo": 3, "baz": 4},
			},
			ChangeSummary{
				Ignored: map[string]int{"foo": 4, "bar": 2, "baz": 4},
			}),
		Entry("merge summaries with both orphaned and ignored changes",
			ChangeSummary{
				Orphaned: map[string]OrphanedChanges{
					"foo": {Count: 1, IDs: newIDSet("foo-1")},
					"bar": {Count: 2, IDs: newIDSet("bar-1")},
				},
				Ignored: map[string]int{"baz": 3, "qux": 4},
			},
			ChangeSummary{
				Orphaned: map[string]OrphanedChanges{
					"foo":  {Count: 5, IDs: newIDSet("foo-2")},
					"quux": {Count: 6, IDs: newIDSet("quux-1")},
				},
				Ignored: map[string]int{"baz": 7, "corge": 8},
			},
			ChangeSummary{
				Orphaned: map[string]OrphanedChanges{
					"foo":  {Count: 6, IDs: newIDSet("foo-1", "foo-2")},
					"bar":  {Count: 2, IDs: newIDSet("bar-1")},
					"quux": {Count: 6, IDs: newIDSet("quux-1")},
				},
				Ignored: map[string]int{"baz": 10, "qux": 4, "corge": 8},
			}),
		Entry("merge summaries with ignored by action",
			ChangeSummary{
				IgnoredByAction: map[string]map[string]int{
					"ignore": {"CreateSomething": 2, "UpdateSomething": 1},
				},
			},
			ChangeSummary{
				IgnoredByAction: map[string]map[string]int{
					"ignore": {"CreateSomething": 3, "DeleteSomething": 5},
					"skip":   {"CreateSomething": 1},
				},
			},
			ChangeSummary{
				IgnoredByAction: map[string]map[string]int{
					"ignore": {"CreateSomething": 5, "UpdateSomething": 1, "DeleteSomething": 5},
					"skip":   {"CreateSomething": 1},
				},
			}),
		Entry("merge summaries with all fields",
			ChangeSummary{
				Orphaned:        map[string]OrphanedChanges{"foo": {Count: 1, IDs: newIDSet("foo-1")}},
				Ignored:         map[string]int{"bar": 2},
				IgnoredByAction: map[string]map[string]int{"ignore": {"baz": 3}},
			},
			ChangeSummary{
				Orphaned:        map[string]OrphanedChanges{"foo": {Count: 4, IDs: newIDSet("foo-2")}},
				Ignored:         map[string]int{"bar": 5},
				IgnoredByAction: map[string]map[string]int{"ignore": {"baz": 6}},
			},
			ChangeSummary{
				Orphaned:        map[string]OrphanedChanges{"foo": {Count: 5, IDs: newIDSet("foo-1", "foo-2")}},
				Ignored:         map[string]int{"bar": 7},
				IgnoredByAction: map[string]map[string]int{"ignore": {"baz": 9}},
			}),
	)
})
