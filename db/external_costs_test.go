// Pins the bucketing rule: charge periods are snapped to a clock-aligned day, week, or
// month and merged within that bucket by stable modeled identity.
package db

import (
	"fmt"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	Expect(err).ToNot(HaveOccurred())
	return t.UTC()
}

// cost builds a minimally-valid ExternalCost for one resource.
func cost(start, end string, amount string, opts ...func(*v1.ExternalCost)) v1.ExternalCost {
	amt := decimal.RequireFromString(amount)
	c := v1.ExternalCost{
		ResourceID:        "i-0abc",
		ChargePeriodStart: ts(start),
		ChargePeriodEnd:   ts(end),
		BilledCost:        decPtr(amt),
		EffectiveCost:     decPtr(amt),
		BillingCurrency:   "USD",
		ChargeCategory:    "Usage",
		ServiceName:       "AmazonEC2",
		SkuID:             "sku-1",
	}
	for _, o := range opts {
		o(&c)
	}
	return c
}

func withSKU(sku string) func(*v1.ExternalCost) {
	return func(c *v1.ExternalCost) { c.SkuID = sku }
}

func withCurrency(currency string) func(*v1.ExternalCost) {
	return func(c *v1.ExternalCost) { c.BillingCurrency = currency }
}

var _ = Describe("bucketFor", func() {
	DescribeTable("snaps a charge period to a clock-aligned bucket",
		func(start, end, wantStart, wantEnd, wantGrain string) {
			gotStart, gotEnd, gotGrain := bucketFor(ts(start), ts(end))
			Expect(gotGrain).To(Equal(wantGrain))
			Expect(gotStart).To(Equal(ts(wantStart)))
			Expect(gotEnd).To(Equal(ts(wantEnd)))
		},
		Entry("one hour",
			"2026-08-03T14:00:00Z", "2026-08-03T15:00:00Z",
			"2026-08-03T00:00:00Z", "2026-08-04T00:00:00Z", models.ConfigCostGrainDay),
		Entry("six hours",
			"2026-08-03T00:00:00Z", "2026-08-03T06:00:00Z",
			"2026-08-03T00:00:00Z", "2026-08-04T00:00:00Z", models.ConfigCostGrainDay),
		Entry("exactly one day stays a day",
			"2026-08-03T00:00:00Z", "2026-08-04T00:00:00Z",
			"2026-08-03T00:00:00Z", "2026-08-04T00:00:00Z", models.ConfigCostGrainDay),
		Entry("crossing midnight snaps to the day containing the start",
			"2026-08-03T23:00:00Z", "2026-08-04T01:00:00Z",
			"2026-08-03T00:00:00Z", "2026-08-04T00:00:00Z", models.ConfigCostGrainDay),
		// 2026-08-03 is a Monday; date_trunc('week') anchors on Monday.
		Entry("three days becomes a week",
			"2026-08-05T00:00:00Z", "2026-08-08T00:00:00Z",
			"2026-08-03T00:00:00Z", "2026-08-10T00:00:00Z", models.ConfigCostGrainWeek),
		Entry("exactly seven days stays a week",
			"2026-08-03T00:00:00Z", "2026-08-10T00:00:00Z",
			"2026-08-03T00:00:00Z", "2026-08-10T00:00:00Z", models.ConfigCostGrainWeek),
		Entry("a calendar month is not split",
			"2026-08-01T00:00:00Z", "2026-09-01T00:00:00Z",
			"2026-08-01T00:00:00Z", "2026-09-01T00:00:00Z", models.ConfigCostGrainMonth),
		Entry("a month-long period starting mid-month snaps to its month",
			"2026-08-15T00:00:00Z", "2026-09-14T00:00:00Z",
			"2026-08-01T00:00:00Z", "2026-09-01T00:00:00Z", models.ConfigCostGrainMonth),
		Entry("a non-UTC input is normalized",
			"2026-08-03T20:00:00-05:00", "2026-08-03T21:00:00-05:00",
			"2026-08-04T00:00:00Z", "2026-08-05T00:00:00Z", models.ConfigCostGrainDay),
	)
})

var _ = Describe("cost target resolution", func() {
	var (
		scraperID uuid.UUID
		ctx       api.ScrapeContext
	)

	BeforeEach(func() {
		scraperID = uuid.New()
		Expect(DefaultContext.DB().Exec(`INSERT INTO config_scrapers (id, name, namespace, spec, source) VALUES (?, ?, 'default', '{}', 'ConfigFile')`, scraperID, "cost-target-"+scraperID.String()).Error).To(Succeed())
		DeferCleanup(func() { DefaultContext.DB().Exec("DELETE FROM config_scrapers WHERE id = ?", scraperID) })
		scrapeConfig := v1.ScrapeConfig{ObjectMeta: metav1.ObjectMeta{UID: k8stypes.UID(scraperID.String())}}
		ctx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)
	})

	It("honors UUID precedence without resolving a different resource_id", func() {
		id := uuid.New()
		c := cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "1")
		c.ConfigID = &id
		c.ResourceID = "not-an-alias"
		Expect(resolveCostTarget(ctx, &c, &scraperID)).To(Succeed())
		Expect(c.ConfigID).To(Equal(&id))
	})

	It("honors external_config_id over resource_id", func() {
		id := uuid.New()
		typeName := "Test::CostTarget"
		Expect(DefaultContext.DB().Exec(`INSERT INTO config_items (id, scraper_id, type, config_class, external_id, created_at, updated_at) VALUES (?, ?, ?, ?, ARRAY[?]::text[], now(), now())`, id, scraperID, typeName, typeName, "preferred").Error).To(Succeed())
		DeferCleanup(func() { DefaultContext.DB().Exec("DELETE FROM config_items WHERE id = ?", id) })

		c := cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "1")
		c.ResourceID = "lower-priority"
		c.ConfigExternalID = v1.ExternalID{ExternalID: "preferred", ConfigType: typeName}
		Expect(resolveCostTarget(ctx, &c, &scraperID)).To(Succeed())
		Expect(c.ConfigID).To(Equal(&id))
	})

	It("errors when a scoped external id matches multiple configs", func() {
		typeName := "Test::AmbiguousCost"
		ids := []uuid.UUID{uuid.New(), uuid.New()}
		for _, id := range ids {
			Expect(DefaultContext.DB().Exec(`INSERT INTO config_items (id, scraper_id, type, config_class, external_id, created_at, updated_at) VALUES (?, ?, ?, ?, ARRAY[?]::text[], now(), now())`, id, scraperID, typeName, typeName, "duplicate").Error).To(Succeed())
		}
		DeferCleanup(func() { DefaultContext.DB().Exec("DELETE FROM config_items WHERE id IN ?", ids) })

		c := cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "1")
		c.ResourceID = ""
		c.ConfigExternalID = v1.ExternalID{ExternalID: "duplicate", ConfigType: typeName}
		Expect(resolveCostTarget(ctx, &c, &scraperID)).To(MatchError(ContainSubstring("ambiguous config reference")))
	})

	It("reports a cost batch error without failing the catalog save", func() {
		configID := uuid.New()
		history := models.NewJobHistory(DefaultContext.Logger, "cost-error-isolation", "", "")
		costCtx := ctx.WithJobHistory(history)
		bad := cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "1")
		bad.BillingCurrency = "not-a-currency"
		result := v1.ScrapeResult{
			ID:            "catalog-survives-cost-error",
			Type:          "Test::CostErrorIsolation",
			ConfigClass:   "Test",
			Name:          "catalog-survives-cost-error",
			Config:        map[string]any{"ready": true},
			ExternalCosts: []v1.ExternalCost{bad},
		}
		result.ConfigID = lo.ToPtr(configID.String())

		summary, err := saveResults(costCtx, []v1.ScrapeResult{result})
		Expect(err).ToNot(HaveOccurred())
		Expect(summary.ExternalCosts.Skipped).To(Equal(1))
		Expect(summary.Warnings).To(HaveLen(1))
		Expect(summary.Warnings[0].Error).To(ContainSubstring("failed to save some external costs"))
		Expect(history.ErrorCount).To(Equal(1))
		Expect(history.Errors).To(HaveLen(1))

		var stored models.ConfigItem
		Expect(DefaultContext.DB().First(&stored, "id = ?", configID).Error).To(Succeed())
		DeferCleanup(func() { DefaultContext.DB().Delete(&models.ConfigItem{}, configID) })
	})

	It("saves valid cost rows when another row is invalid", func() {
		valid := cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "3")
		valid.SourceKey = "test:partial-cost-batch"
		invalid := cost("2026-08-03T02:00:00Z", "2026-08-03T03:00:00Z", "4")
		invalid.BillingCurrency = "invalid"
		var summary v1.ScrapeSummary

		err := saveExternalCosts(ctx, []v1.ExternalCost{valid, invalid}, &scraperID, &summary)
		Expect(err).To(MatchError(ContainSubstring("external cost 1")))
		Expect(summary.ExternalCosts.Saved).To(Equal(1))
		Expect(summary.ExternalCosts.Skipped).To(Equal(1))

		var count int64
		Expect(DefaultContext.DB().Model(&models.ConfigCost{}).Where("source_key = ?", valid.SourceKey).Count(&count).Error).To(Succeed())
		Expect(count).To(Equal(int64(1)))
		DeferCleanup(func() { DefaultContext.DB().Where("source_key = ?", valid.SourceKey).Delete(&models.ConfigCost{}) })
	})

	It("moves a corrected source record without retaining its old target", func() {
		typeName := "Test::SourceRecordCorrection"
		ids := []uuid.UUID{uuid.New(), uuid.New()}
		for i, id := range ids {
			Expect(DefaultContext.DB().Exec(`INSERT INTO config_items (id, scraper_id, type, config_class, external_id, created_at, updated_at) VALUES (?, ?, ?, ?, ARRAY[?]::text[], now(), now())`, id, scraperID, typeName, typeName, fmt.Sprintf("correction-target-%d", i)).Error).To(Succeed())
		}
		DeferCleanup(func() { DefaultContext.DB().Exec("DELETE FROM config_items WHERE id IN ?", ids) })

		recordID := "provider-line-1"
		first := cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "1")
		first.SourceKey, first.SourceRecordID, first.ConfigID = "test:source-correction", &recordID, &ids[0]
		var summary v1.ScrapeSummary
		Expect(saveExternalCosts(ctx, []v1.ExternalCost{first}, &scraperID, &summary)).To(Succeed())

		corrected := cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "2")
		corrected.SourceKey, corrected.SourceRecordID, corrected.ConfigID = first.SourceKey, &recordID, &ids[1]
		Expect(saveExternalCosts(ctx, []v1.ExternalCost{corrected}, &scraperID, &summary)).To(Succeed())

		var rows []models.ConfigCost
		Expect(DefaultContext.DB().Where("source_key = ? AND source_record_id = ?", first.SourceKey, recordID).Find(&rows).Error).To(Succeed())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].ConfigID).To(Equal(&ids[1]))
		Expect(rows[0].EffectiveCost.String()).To(Equal("2"))
	})
})

var _ = Describe("bucketCosts", func() {
	It("merges 24 hourly rows into one day-grain row", func() {
		var costs []v1.ExternalCost
		day := ts("2026-08-03T00:00:00Z")
		for h := 0; h < 24; h++ {
			start := day.Add(time.Duration(h) * time.Hour)
			costs = append(costs, cost(
				start.Format(time.RFC3339),
				start.Add(time.Hour).Format(time.RFC3339),
				"0.25"))
		}

		got := bucketCosts(costs, nil)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Grain).To(Equal(models.ConfigCostGrainDay))
		Expect(got[0].PeriodStart).To(Equal(day))
		Expect(got[0].PeriodEnd).To(Equal(day.AddDate(0, 0, 1)))
		Expect(got[0].EffectiveCost.String()).To(Equal("6"))
		Expect(got[0].BilledCost.String()).To(Equal("6"))
	})

	It("merges a partial day into one day-grain row", func() {
		var costs []v1.ExternalCost
		day := ts("2026-08-03T00:00:00Z")
		for h := 0; h < 6; h++ {
			start := day.Add(time.Duration(h) * time.Hour)
			costs = append(costs, cost(
				start.Format(time.RFC3339),
				start.Add(time.Hour).Format(time.RFC3339),
				"1.5"))
		}

		got := bucketCosts(costs, nil)
		Expect(got).To(HaveLen(1))
		Expect(got[0].EffectiveCost.String()).To(Equal("9"))
	})

	It("keeps a monthly charge as a single row", func() {
		got := bucketCosts([]v1.ExternalCost{
			cost("2026-08-01T00:00:00Z", "2026-09-01T00:00:00Z", "300"),
		}, nil)

		Expect(got).To(HaveLen(1))
		Expect(got[0].Grain).To(Equal(models.ConfigCostGrainMonth))
		Expect(got[0].EffectiveCost.String()).To(Equal("300"))
	})

	It("separates distinct SKUs within the same day", func() {
		got := bucketCosts([]v1.ExternalCost{
			cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "1", withSKU("sku-a")),
			cost("2026-08-03T02:00:00Z", "2026-08-03T03:00:00Z", "2", withSKU("sku-b")),
		}, nil)

		Expect(got).To(HaveLen(2))
		Expect(got[0].Fingerprint).ToNot(Equal(got[1].Fingerprint))
	})

	It("never merges across source keys", func() {
		a := cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "1")
		b := cost("2026-08-03T02:00:00Z", "2026-08-03T03:00:00Z", "2")
		a.SourceKey, b.SourceKey = "source:a", "source:b"
		got := bucketCosts([]v1.ExternalCost{a, b}, nil)
		Expect(got).To(HaveLen(2))
		Expect([]string{got[0].SourceKey, got[1].SourceKey}).To(ConsistOf("source:a", "source:b"))
	})

	It("propagates structured unmatched target identity", func() {
		a := cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "1")
		a.ConfigExternalID.ConfigType = "AWS::EC2::Instance"
		a.ConfigExternalID.ScraperID = "all"
		a.ConfigExternalID.Labels = map[string]string{"account": "prod"}
		got := bucketCosts([]v1.ExternalCost{a}, nil)
		Expect(got[0].ExternalConfigType).ToNot(BeNil())
		Expect(*got[0].ExternalConfigType).To(Equal("AWS::EC2::Instance"))
		Expect(*got[0].ExternalConfigScraperID).To(Equal("all"))
		Expect(got[0].ExternalConfigLabels).To(HaveKeyWithValue("account", "prod"))
	})

	It("never merges across currencies", func() {
		got := bucketCosts([]v1.ExternalCost{
			cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "1", withCurrency("USD")),
			cost("2026-08-03T02:00:00Z", "2026-08-03T03:00:00Z", "1", withCurrency("EUR")),
		}, nil)

		Expect(got).To(HaveLen(2))
		currencies := []string{got[0].BillingCurrency, got[1].BillingCurrency}
		Expect(currencies).To(ConsistOf("USD", "EUR"))
	})

	It("keeps a negative correction as its own row", func() {
		correction := cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "-5")
		correction.ChargeClass = "Correction"

		got := bucketCosts([]v1.ExternalCost{
			cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "12"),
			correction,
		}, nil)

		Expect(got).To(HaveLen(2))
		var total decimal.Decimal
		for _, c := range got {
			total = total.Add(c.EffectiveCost)
		}
		Expect(total.String()).To(Equal("7"))
	})

	It("is idempotent", func() {
		input := []v1.ExternalCost{
			cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "1"),
			cost("2026-08-03T02:00:00Z", "2026-08-03T03:00:00Z", "2", withSKU("sku-b")),
			cost("2026-08-01T00:00:00Z", "2026-09-01T00:00:00Z", "300"),
		}

		Expect(bucketCosts(input, nil)).To(Equal(bucketCosts(input, nil)))
	})

	It("retains the resource id when the cost has no config item", func() {
		got := bucketCosts([]v1.ExternalCost{
			cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "3"),
		}, nil)

		Expect(got).To(HaveLen(1))
		Expect(got[0].ConfigID).To(BeNil())
		Expect(got[0].ExternalID).ToNot(BeNil())
		Expect(*got[0].ExternalID).To(Equal("i-0abc"))
	})

	It("does not duplicate a bucket when a passthrough field is added", func() {
		a := cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "1")
		a.Focus = map[string]any{"Tags": map[string]any{"env": "prod"}}
		b := cost("2026-08-03T02:00:00Z", "2026-08-03T03:00:00Z", "2")
		b.Focus = map[string]any{
			"Tags":              map[string]any{"env": "prod"},
			"NewProviderColumn": "added-mid-period",
		}

		got := bucketCosts([]v1.ExternalCost{a, b}, nil)
		Expect(got).To(HaveLen(1))
		Expect(got[0].EffectiveCost.String()).To(Equal("3"))
	})

	It("sums the optional metrics and never the unit prices", func() {
		a := cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "1")
		a.ListCost = decPtr(decimal.NewFromInt(4))
		a.PricingQuantity = decPtr(decimal.NewFromInt(10))
		a.PricingUnit = "Hours"
		a.Focus = map[string]any{"ListUnitPrice": 0.4}

		b := cost("2026-08-03T02:00:00Z", "2026-08-03T03:00:00Z", "1")
		b.ListCost = decPtr(decimal.NewFromInt(4))
		b.PricingQuantity = decPtr(decimal.NewFromInt(10))
		b.PricingUnit = "Hours"
		b.Focus = map[string]any{"ListUnitPrice": 0.4}

		got := bucketCosts([]v1.ExternalCost{a, b}, nil)
		Expect(got).To(HaveLen(1))
		Expect(got[0].ListCost.String()).To(Equal("8"))
		Expect(got[0].PricingQuantity.String()).To(Equal("20"))
		// Row-scoped prices are carried through untouched, never aggregated.
		Expect(got[0].Focus["ListUnitPrice"]).To(BeEquivalentTo(0.4))
	})
})

func decPtr(d decimal.Decimal) *decimal.Decimal { return &d }
