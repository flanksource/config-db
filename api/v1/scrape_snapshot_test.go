package v1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ScrapeSnapshot", func() {
	Describe("DiffSnapshots", func() {
		It("returns zero diff for identical snapshots", func() {
			s := &ScrapeSnapshot{
				PerScraper: map[string]EntityWindowCounts{
					"aws": {Total: 10},
				},
				ExternalUsers: EntityWindowCounts{Total: 5, UpdatedLast: 2},
			}
			diff := DiffSnapshots(s, s)
			Expect(diff.PerScraper).To(BeEmpty())
			Expect(diff.ExternalUsers.IsZero()).To(BeTrue())
		})

		It("subtracts field-wise across entities", func() {
			before := &ScrapeSnapshot{
				ExternalUsers: EntityWindowCounts{Total: 10, UpdatedLast: 0, DeletedWeek: 1},
			}
			after := &ScrapeSnapshot{
				ExternalUsers: EntityWindowCounts{Total: 13, UpdatedLast: 3, DeletedWeek: 2},
			}
			diff := DiffSnapshots(before, after)
			Expect(diff.ExternalUsers.Total).To(Equal(3))
			Expect(diff.ExternalUsers.UpdatedLast).To(Equal(3))
			Expect(diff.ExternalUsers.DeletedWeek).To(Equal(1))
		})

		It("emits negative deltas when after is smaller", func() {
			before := &ScrapeSnapshot{
				ConfigAccess: EntityWindowCounts{Total: 100},
			}
			after := &ScrapeSnapshot{
				ConfigAccess: EntityWindowCounts{Total: 97, DeletedLast: 3},
			}
			diff := DiffSnapshots(before, after)
			Expect(diff.ConfigAccess.Total).To(Equal(-3))
			Expect(diff.ConfigAccess.DeletedLast).To(Equal(3))
		})

		It("unions map keys from before and after", func() {
			before := &ScrapeSnapshot{
				PerConfigType: map[string]EntityWindowCounts{
					"Kubernetes::Pod": {Total: 50},
					"AWS::EC2":        {Total: 20},
				},
			}
			after := &ScrapeSnapshot{
				PerConfigType: map[string]EntityWindowCounts{
					"Kubernetes::Pod":        {Total: 55, UpdatedLast: 5},
					"AWS::EC2":               {Total: 20},
					"Kubernetes::Deployment": {Total: 3, UpdatedLast: 3},
				},
			}
			diff := DiffSnapshots(before, after)

			// Unchanged AWS::EC2 is omitted.
			_, hasEC2 := diff.PerConfigType["AWS::EC2"]
			Expect(hasEC2).To(BeFalse())

			// Changed Pod shows +5.
			Expect(diff.PerConfigType["Kubernetes::Pod"].Total).To(Equal(5))
			Expect(diff.PerConfigType["Kubernetes::Pod"].UpdatedLast).To(Equal(5))

			// Wholly new Deployment shows as +3.
			Expect(diff.PerConfigType["Kubernetes::Deployment"].Total).To(Equal(3))
		})

		It("treats a key present only in before as a removal", func() {
			before := &ScrapeSnapshot{
				PerScraper: map[string]EntityWindowCounts{
					"removed-scraper": {Total: 42},
				},
			}
			after := &ScrapeSnapshot{
				PerScraper: map[string]EntityWindowCounts{},
			}
			diff := DiffSnapshots(before, after)
			Expect(diff.PerScraper["removed-scraper"].Total).To(Equal(-42))
		})

		It("treats nil before as zero", func() {
			after := &ScrapeSnapshot{
				ExternalUsers: EntityWindowCounts{Total: 7},
			}
			diff := DiffSnapshots(nil, after)
			Expect(diff.ExternalUsers.Total).To(Equal(7))
		})

		It("treats nil after as zero", func() {
			before := &ScrapeSnapshot{
				ExternalUsers: EntityWindowCounts{Total: 7},
			}
			diff := DiffSnapshots(before, nil)
			Expect(diff.ExternalUsers.Total).To(Equal(-7))
		})

		It("returns zero diff when both sides are nil", func() {
			diff := DiffSnapshots(nil, nil)
			Expect(diff.PerScraper).To(BeNil())
			Expect(diff.PerConfigType).To(BeNil())
			Expect(diff.ExternalUsers.IsZero()).To(BeTrue())
		})
	})

	Describe("PrettyShort", func() {
		It("returns 'no changes' for a zero diff", func() {
			diff := ScrapeSnapshotDiff{}
			Expect(diff.PrettyShort()).To(Equal("no changes"))
		})

		It("emits signed deltas for non-zero entity totals", func() {
			diff := ScrapeSnapshotDiff{
				PerConfigType: map[string]EntityWindowCounts{
					"Kubernetes::Pod": {Total: 5},
				},
				ExternalUsers: EntityWindowCounts{Total: 2},
				ConfigAccess:  EntityWindowCounts{Total: -1},
			}
			Expect(diff.PrettyShort()).To(Equal("configs=+5 users=+2 access=-1"))
		})
	})

	Describe("EntityWindowCounts", func() {
		It("IsZero is true for the zero value", func() {
			Expect(EntityWindowCounts{}.IsZero()).To(BeTrue())
		})
		It("IsZero is false when any field is set", func() {
			Expect(EntityWindowCounts{Total: 1}.IsZero()).To(BeFalse())
			Expect(EntityWindowCounts{UpdatedLast: 1}.IsZero()).To(BeFalse())
			Expect(EntityWindowCounts{DeletedWeek: 1}.IsZero()).To(BeFalse())
		})
		It("Sub is field-wise", func() {
			a := EntityWindowCounts{Total: 10, UpdatedLast: 3, DeletedHour: 1}
			b := EntityWindowCounts{Total: 4, UpdatedLast: 1, DeletedHour: 0}
			delta := a.Sub(b)
			Expect(delta.Total).To(Equal(6))
			Expect(delta.UpdatedLast).To(Equal(2))
			Expect(delta.DeletedHour).To(Equal(1))
		})
	})
})
