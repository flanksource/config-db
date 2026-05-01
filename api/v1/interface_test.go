package v1

import (
	"sort"
	"time"

	"github.com/flanksource/commons/collections/set"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
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

var _ = Describe("ScrapeSummary.AsMap", func() {
	It("exposes nested values via json-tagged snake_case keys", func() {
		ts := time.Date(2026, 4, 12, 13, 10, 47, 0, time.UTC)
		summary := ScrapeSummary{
			ConfigTypes: map[string]ConfigTypeScrapeSummary{
				"Azure::Application": {
					Added:      3,
					AccessLogs: EntitySummary[struct{}]{LastCreatedAt: &ts, Scraped: 7},
				},
			},
			AccessLogs: EntitySummary[struct{}]{LastCreatedAt: &ts, Scraped: 7},
		}

		m := summary.AsMap()

		accessLogs, ok := m["access_logs"].(map[string]any)
		Expect(ok).To(BeTrue(), "access_logs must serialize as a nested map")
		Expect(accessLogs).To(HaveKey("last_created_at"))
		Expect(accessLogs["last_created_at"]).To(Equal("2026-04-12T13:10:47Z"))

		configTypes, ok := m["config_types"].(map[string]any)
		Expect(ok).To(BeTrue())
		azureApp, ok := configTypes["Azure::Application"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(azureApp).To(HaveKeyWithValue("added", float64(3)))
		azureAppAccess, ok := azureApp["access_logs"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(azureAppAccess["last_created_at"]).To(Equal("2026-04-12T13:10:47Z"))
	})

	It("returns an empty map for a zero-value summary without panicking", func() {
		Expect(ScrapeSummary{}.AsMap()).NotTo(BeNil())
	})
})

var _ = Describe("ScrapeSummary warnings", func() {
	It("deduplicates structured warnings inline by error", func() {
		summary := NewScrapeSummary()

		summary.AddScrapeWarning(Warning{Error: "duplicate warning", Input: "first", Expr: "expr-a"})
		summary.AddScrapeWarning(Warning{Error: "duplicate warning", Input: "second", Expr: "expr-b"})

		Expect(summary.Warnings).To(HaveLen(1))
		Expect(summary.Warnings[0].Count).To(Equal(2))
		Expect(summary.Warnings[0].Input).To(Equal("first"))
		Expect(summary.Warnings[0].Expr).To(Equal("expr-a"))
	})

	It("deduplicates config type warnings inline", func() {
		summary := NewScrapeSummary()

		summary.AddWarning("AWS::EC2::Instance", "duplicate warning")
		summary.AddWarning("AWS::EC2::Instance", "duplicate warning")

		Expect(summary.ConfigTypes["AWS::EC2::Instance"].Warnings).To(Equal([]string{"duplicate warning"}))
	})
})

var _ = Describe("MergeScrapeResults entity dedupe", func() {
	sid := "S-1-9-1551374245-1204400969-2402986413-2179408616-3-1284223813-3851149125-2957882383-1282321495"
	persisted := uuid.MustParse("63f8e883-b5df-cda7-18e3-17f34e410afa")

	It("collapses three rows of the same group across scrape paths into one", func() {
		// Three ScrapeResults emit the same logical group:
		//   r1: ACL-path emit (nil id, prefixed display name).
		//   r2: graph-permissions emit (nil id, clean display name, extra alias).
		//   r3: post-save emit (real id, prefixed display name).
		// All three share the SID alias, so they must collapse to ONE row in
		// FullScrapeResults.ExternalGroups.
		r1 := ScrapeResults{{ExternalGroups: []models.ExternalGroup{{
			Name:    `[TEAM FOUNDATION]\ExampleApp Shared Architecture Runway Team`,
			Aliases: pq.StringArray{sid},
		}}}}
		r2 := ScrapeResults{{ExternalGroups: []models.ExternalGroup{{
			Name:    "ExampleApp Shared Architecture Runway Team",
			Aliases: pq.StringArray{sid, "aadgp.X"},
		}}}}
		r3 := ScrapeResults{{ExternalGroups: []models.ExternalGroup{{
			ID:      persisted,
			Name:    `[TEAM FOUNDATION]\ExampleApp Shared Architecture Runway Team`,
			Aliases: pq.StringArray{sid},
		}}}}

		full := MergeScrapeResults(r1, r2, r3)

		Expect(full.ExternalGroups).To(HaveLen(1), "all three rows must collapse via SID alias overlap")
		Expect(full.ExternalGroups[0].ID).To(Equal(persisted), "the persisted-id row wins")
		Expect([]string(full.ExternalGroups[0].Aliases)).To(ConsistOf(sid, "aadgp.X"))
	})

	It("does not merge groups that do not share any alias", func() {
		r := ScrapeResults{
			{ExternalGroups: []models.ExternalGroup{{Name: "Alpha", Aliases: pq.StringArray{"alpha-sid"}}}},
			{ExternalGroups: []models.ExternalGroup{{Name: "Bravo", Aliases: pq.StringArray{"bravo-sid"}}}},
		}
		full := MergeScrapeResults(r)
		Expect(full.ExternalGroups).To(HaveLen(2))
	})

	It("collapses users with overlapping email aliases across emits", func() {
		// One scrape path emits a nil-id user with [email, AAD-id]; another
		// emits a nil-id user with [email] only. They must merge.
		full := MergeScrapeResults(
			ScrapeResults{{ExternalUsers: []models.ExternalUser{{
				Aliases: pq.StringArray{"alice@example.com", "aad-id-1"},
			}}}},
			ScrapeResults{{ExternalUsers: []models.ExternalUser{{
				Aliases: pq.StringArray{"alice@example.com"},
			}}}},
		)
		Expect(full.ExternalUsers).To(HaveLen(1))
		Expect([]string(full.ExternalUsers[0].Aliases)).To(ConsistOf("alice@example.com", "aad-id-1"))
	})
})
