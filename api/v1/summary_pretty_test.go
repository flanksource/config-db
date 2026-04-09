package v1

import (
	"encoding/json"

	"github.com/flanksource/duty/models"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ScrapeSummary", func() {
	Describe("PrettyShort", func() {
		DescribeTable("formats summary correctly",
			func(summary ScrapeSummary, expected string) {
				Expect(summary.PrettyShort()).To(Equal(expected))
			},
			Entry("empty summary",
				ScrapeSummary{},
				"no changes"),
			Entry("configs only",
				ScrapeSummary{
					ConfigTypes: map[string]ConfigTypeScrapeSummary{
						"AWS::EC2::Instance": {Added: 5, Updated: 12, Unchanged: 100},
					},
				},
				"configs(+5/~12/=100)"),
			Entry("configs with changes and deduped",
				ScrapeSummary{
					ConfigTypes: map[string]ConfigTypeScrapeSummary{
						"AWS::EC2::Instance": {Added: 5, Updated: 12, Unchanged: 100, Changes: 8, Deduped: 3},
					},
				},
				"configs(+5/~12/=100) changes(+8/dedup=3)"),
			Entry("full summary with entities",
				ScrapeSummary{
					ConfigTypes: map[string]ConfigTypeScrapeSummary{
						"AWS::EC2::Instance": {Added: 1},
					},
					ExternalUsers:  EntitySummary[models.ExternalUser]{Scraped: 50, Saved: 50},
					ExternalGroups: EntitySummary[models.ExternalGroup]{Scraped: 10, Saved: 10},
					ExternalRoles:  EntitySummary[models.ExternalRole]{Scraped: 5, Saved: 5},
					ConfigAccess:   EntitySummary[struct{}]{Scraped: 200, Saved: 200},
					AccessLogs:     EntitySummary[struct{}]{Scraped: 1000, Saved: 1000},
				},
				"configs(+1/~0/=0) users=50 groups=10 roles=5 access=200 logs=1000"),
		)
	})

	Describe("UnmarshalJSON backward compatibility", func() {
		It("should parse old map format", func() {
			oldJSON := `{
				"AWS::EC2::Instance": {"added": 3, "updated": 5, "unchanged": 10},
				"Kubernetes::Pod": {"added": 1}
			}`
			var s ScrapeSummary
			Expect(json.Unmarshal([]byte(oldJSON), &s)).To(Succeed())
			Expect(s.ConfigTypes["AWS::EC2::Instance"].Added).To(Equal(3))
			Expect(s.ConfigTypes["AWS::EC2::Instance"].Updated).To(Equal(5))
			Expect(s.ConfigTypes["AWS::EC2::Instance"].Unchanged).To(Equal(10))
			Expect(s.ConfigTypes["Kubernetes::Pod"].Added).To(Equal(1))
		})

		It("should parse new struct format", func() {
			newJSON := `{
				"config_types": {
					"AWS::EC2::Instance": {"added": 3, "updated": 5}
				},
				"external_users": {"scraped": 50, "saved": 48, "skipped": 2}
			}`
			var s ScrapeSummary
			Expect(json.Unmarshal([]byte(newJSON), &s)).To(Succeed())
			Expect(s.ConfigTypes["AWS::EC2::Instance"].Added).To(Equal(3))
			Expect(s.ConfigTypes["AWS::EC2::Instance"].Updated).To(Equal(5))
			Expect(s.ExternalUsers.Scraped).To(Equal(50))
			Expect(s.ExternalUsers.Saved).To(Equal(48))
			Expect(s.ExternalUsers.Skipped).To(Equal(2))
		})

		It("should roundtrip new format", func() {
			original := ScrapeSummary{
				ConfigTypes: map[string]ConfigTypeScrapeSummary{
					"AWS::EC2::Instance": {Added: 3, Changes: 10, Deduped: 2},
				},
				ExternalUsers: EntitySummary[models.ExternalUser]{Scraped: 50, Saved: 48},
			}
			data, err := json.Marshal(original)
			Expect(err).ToNot(HaveOccurred())

			var decoded ScrapeSummary
			Expect(json.Unmarshal(data, &decoded)).To(Succeed())
			Expect(decoded.ConfigTypes["AWS::EC2::Instance"]).To(Equal(original.ConfigTypes["AWS::EC2::Instance"]))
			Expect(decoded.ExternalUsers).To(Equal(original.ExternalUsers))
		})
	})

	Describe("ConfigTypeScrapeSummary Merge", func() {
		It("should merge all fields correctly", func() {
			a := ConfigTypeScrapeSummary{Added: 1, Updated: 2, Changes: 5, Deduped: 3}
			b := ConfigTypeScrapeSummary{Added: 3, Updated: 4, Changes: 10, Deduped: 7}
			merged := a.Merge(b)
			Expect(merged.Added).To(Equal(4))
			Expect(merged.Updated).To(Equal(6))
			Expect(merged.Changes).To(Equal(15))
			Expect(merged.Deduped).To(Equal(10))
		})
	})

	Describe("ScrapeSummary Merge", func() {
		It("should merge config types and entity summaries", func() {
			a := ScrapeSummary{
				ConfigTypes: map[string]ConfigTypeScrapeSummary{
					"AWS::EC2::Instance": {Added: 1, Changes: 5},
				},
				ExternalUsers: EntitySummary[models.ExternalUser]{Scraped: 10, Saved: 8, Deleted: 1},
			}
			b := ScrapeSummary{
				ConfigTypes: map[string]ConfigTypeScrapeSummary{
					"AWS::EC2::Instance": {Added: 2, Changes: 3},
					"Kubernetes::Pod":    {Added: 5},
				},
				ExternalUsers: EntitySummary[models.ExternalUser]{Scraped: 20, Saved: 18, Deleted: 2},
			}
			a.Merge(b)

			Expect(a.ConfigTypes["AWS::EC2::Instance"].Added).To(Equal(3))
			Expect(a.ConfigTypes["AWS::EC2::Instance"].Changes).To(Equal(8))
			Expect(a.ConfigTypes["Kubernetes::Pod"].Added).To(Equal(5))
			Expect(a.ExternalUsers.Scraped).To(Equal(30))
			Expect(a.ExternalUsers.Saved).To(Equal(26))
			Expect(a.ExternalUsers.Deleted).To(Equal(3))
		})
	})

	Describe("EntitySummary IsEmpty", func() {
		It("should return true for zero value", func() {
			Expect(EntitySummary[models.ExternalUser]{}.IsEmpty()).To(BeTrue())
		})
		It("should return false when Scraped is set", func() {
			Expect(EntitySummary[models.ExternalUser]{Scraped: 1}.IsEmpty()).To(BeFalse())
		})
		It("should return false when Deleted is set", func() {
			Expect(EntitySummary[models.ExternalUser]{Deleted: 1}.IsEmpty()).To(BeFalse())
		})
	})

	Describe("Totals", func() {
		It("should aggregate across all config types", func() {
			s := ScrapeSummary{
				ConfigTypes: map[string]ConfigTypeScrapeSummary{
					"A": {Added: 1, Updated: 2, Unchanged: 3, Changes: 10, Deduped: 5},
					"B": {Added: 4, Updated: 5, Unchanged: 6, Changes: 20, Deduped: 8},
				},
			}
			totals := s.Totals()
			Expect(totals.Added).To(Equal(5))
			Expect(totals.Updated).To(Equal(7))
			Expect(totals.Unchanged).To(Equal(9))
			Expect(totals.Changes).To(Equal(30))
			Expect(totals.Deduped).To(Equal(13))
		})
	})

	Describe("HasUpdates", func() {
		It("should return false for empty summary", func() {
			Expect(ScrapeSummary{}.HasUpdates()).To(BeFalse())
		})
		It("should return true with config updates", func() {
			s := ScrapeSummary{ConfigTypes: map[string]ConfigTypeScrapeSummary{"A": {Added: 1}}}
			Expect(s.HasUpdates()).To(BeTrue())
		})
		It("should return true with entity updates only", func() {
			s := ScrapeSummary{ExternalUsers: EntitySummary[models.ExternalUser]{Saved: 5}}
			Expect(s.HasUpdates()).To(BeTrue())
		})
	})
})
