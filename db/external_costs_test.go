// Pins the bucketing rule: charge periods are snapped to a clock-aligned hour, day, or
// rolling 30-day bucket and merged within it by stable modeled identity.
package db

import (
	"time"

	"github.com/flanksource/commons/properties"

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

// defaultLevels mirrors the shipped property defaults: 1h, 1d, 30d.
var defaultLevels = CostLevels{L1: time.Hour, L2: 24 * time.Hour, L3: 30 * 24 * time.Hour}

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	Expect(err).ToNot(HaveOccurred())
	return t.UTC()
}

// testCostTarget stands in for a resolved config item. config_costs.config_id is NOT NULL,
// so bucketCosts only ever sees costs whose target has already been resolved.
var testCostTarget = uuid.New()

// cost builds a minimally-valid, already-resolved ExternalCost for one resource.
func cost(start, end string, amount string, opts ...func(*v1.ExternalCost)) v1.ExternalCost {
	amt := decimal.RequireFromString(amount)
	c := v1.ExternalCost{
		ConfigID:          &testCostTarget,
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

var _ = Describe("ResolveCostLevels", func() {
	AfterEach(func() {
		for _, p := range []string{PropCostLevel1, PropCostLevel2, PropCostLevel3} {
			properties.Set(p, "")
		}
	})

	It("defaults to 1h / 1d / 30d", func() {
		levels, err := ResolveCostLevels()
		Expect(err).ToNot(HaveOccurred())
		Expect(levels).To(Equal(defaultLevels))
	})

	It("accepts widths that divide each other", func() {
		properties.Set(PropCostLevel1, "15m")
		properties.Set(PropCostLevel2, "6h")
		properties.Set(PropCostLevel3, "168h")
		levels, err := ResolveCostLevels()
		Expect(err).ToNot(HaveOccurred())
		Expect(levels.L1).To(Equal(15 * time.Minute))
		Expect(levels.L3).To(Equal(168 * time.Hour))
	})

	It("rejects a ladder that would force a row to be split", func() {
		// 7h does not divide into 24h, so an hour bucket would straddle two level-2
		// buckets and compaction would stop being exact. Refuse rather than approximate.
		properties.Set(PropCostLevel1, "7h")
		properties.Set(PropCostLevel2, "24h")
		_, err := ResolveCostLevels()
		Expect(err).To(MatchError(ContainSubstring("whole multiple")))

		properties.Set(PropCostLevel1, "1h")
		properties.Set(PropCostLevel2, "24h")
		properties.Set(PropCostLevel3, "100h")
		_, err = ResolveCostLevels()
		Expect(err).To(MatchError(ContainSubstring("whole multiple")))
	})

	It("rejects an unparseable width", func() {
		properties.Set(PropCostLevel1, "not-a-duration")
		_, err := ResolveCostLevels()
		Expect(err).To(MatchError(ContainSubstring("config.costs.level1")))
	})
})

var _ = Describe("bucketFor", func() {
	DescribeTable("snaps a charge period to a clock-aligned bucket",
		func(start, end, wantStart, wantEnd, wantGrain string) {
			gotStart, gotEnd, gotGrain := bucketFor(ts(start), ts(end), defaultLevels)
			Expect(gotGrain).To(Equal(wantGrain))
			Expect(gotStart).To(Equal(ts(wantStart)))
			Expect(gotEnd).To(Equal(ts(wantEnd)))
		},
		Entry("one hour",
			"2026-08-03T14:00:00Z", "2026-08-03T15:00:00Z",
			"2026-08-03T14:00:00Z", "2026-08-03T15:00:00Z", models.ConfigCostLevel1),
		Entry("under an hour snaps to the hour containing the start",
			"2026-08-03T14:20:00Z", "2026-08-03T14:35:00Z",
			"2026-08-03T14:00:00Z", "2026-08-03T15:00:00Z", models.ConfigCostLevel1),
		Entry("crossing the hour boundary snaps to the hour containing the start",
			"2026-08-03T14:50:00Z", "2026-08-03T15:40:00Z",
			"2026-08-03T14:00:00Z", "2026-08-03T15:00:00Z", models.ConfigCostLevel1),
		Entry("six hours becomes a day",
			"2026-08-03T00:00:00Z", "2026-08-03T06:00:00Z",
			"2026-08-03T00:00:00Z", "2026-08-04T00:00:00Z", models.ConfigCostLevel2),
		Entry("exactly one day stays a day",
			"2026-08-03T00:00:00Z", "2026-08-04T00:00:00Z",
			"2026-08-03T00:00:00Z", "2026-08-04T00:00:00Z", models.ConfigCostLevel2),
		Entry("crossing midnight snaps to the day containing the start",
			"2026-08-03T23:00:00Z", "2026-08-04T01:00:00Z",
			"2026-08-03T00:00:00Z", "2026-08-04T00:00:00Z", models.ConfigCostLevel2),
		// There is no week grain: anything over a day goes straight to the rolling 30d
		// bucket, which is anchored on the Unix epoch rather than the calendar.
		Entry("three days becomes a 30d bucket",
			"2026-08-05T00:00:00Z", "2026-08-08T00:00:00Z",
			"2026-08-05T00:00:00Z", "2026-09-04T00:00:00Z", models.ConfigCostLevel3),
		Entry("a calendar month is not split, and does not align to one",
			"2026-08-01T00:00:00Z", "2026-09-01T00:00:00Z",
			"2026-07-06T00:00:00Z", "2026-08-05T00:00:00Z", models.ConfigCostLevel3),
		Entry("a non-UTC input is normalized",
			"2026-08-03T20:00:00-05:00", "2026-08-03T20:30:00-05:00",
			"2026-08-04T01:00:00Z", "2026-08-04T02:00:00Z", models.ConfigCostLevel1),
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
		c.ConfigID = nil
		c.ResourceID = "lower-priority"
		c.ConfigExternalID = v1.ExternalID{ExternalID: "preferred", ConfigType: typeName}
		Expect(resolveCostTarget(ctx, &c, &scraperID)).To(Succeed())
		Expect(c.ConfigID).To(Equal(&id))
	})

	It("falls back to the root when a scoped external id matches multiple configs", func() {
		typeName := "Test::AmbiguousCost"
		ids := []uuid.UUID{uuid.New(), uuid.New()}
		for _, id := range ids {
			Expect(DefaultContext.DB().Exec(`INSERT INTO config_items (id, scraper_id, type, config_class, external_id, created_at, updated_at) VALUES (?, ?, ?, ?, ARRAY[?]::text[], now(), now())`, id, scraperID, typeName, typeName, "duplicate").Error).To(Succeed())
		}
		DeferCleanup(func() { DefaultContext.DB().Exec("DELETE FROM config_items WHERE id IN ?", ids) })

		root := uuid.New()
		Expect(DefaultContext.DB().Exec(`INSERT INTO config_items (id, scraper_id, type, config_class, external_id, created_at, updated_at) VALUES (?, ?, 'Test::Account', 'Test', ARRAY[?]::text[], now(), now())`, root, scraperID, "ambiguous-root").Error).To(Succeed())
		DeferCleanup(func() { DefaultContext.DB().Exec("DELETE FROM config_items WHERE id = ?", root) })

		// Never guess which of the two resources it was; never lose the money either.
		c := cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "1")
		c.ConfigID = nil
		c.ResourceID = ""
		c.ConfigExternalID = v1.ExternalID{ExternalID: "duplicate", ConfigType: typeName}
		c.RootConfigID = v1.ExternalID{ExternalID: "ambiguous-root", ConfigType: "Test::Account"}
		Expect(resolveCostTarget(ctx, &c, &scraperID)).To(Succeed())
		Expect(c.ConfigID).To(Equal(&root))
	})

	It("errors when nothing resolves and the scraper supplied no root", func() {
		c := cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "1")
		c.ConfigID = nil
		c.ResourceID = "never-scraped"
		Expect(resolveCostTarget(ctx, &c, &scraperID)).
			To(MatchError(ContainSubstring("no root_config_id")))
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
		// config_id is a real FK now, so the surviving row needs a real target.
		target := uuid.New()
		Expect(DefaultContext.DB().Exec(`INSERT INTO config_items (id, scraper_id, type, config_class, external_id, created_at, updated_at) VALUES (?, ?, 'Test::CostTarget', 'Test', ARRAY[?]::text[], now(), now())`, target, scraperID, "partial-batch-target").Error).To(Succeed())
		DeferCleanup(func() { DefaultContext.DB().Exec("DELETE FROM config_items WHERE id = ?", target) })

		valid := cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "3")
		valid.ConfigID = &target
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

})

var _ = Describe("bucketCosts", func() {
	It("keeps 24 hourly rows at hour grain, one row per hour", func() {
		// Hourly CUR rows are already at the finest grain, so ingestion does not merge
		// them. The compaction job rolls them into a day once they pass the threshold.
		var costs []v1.ExternalCost
		day := ts("2026-08-03T00:00:00Z")
		for h := 0; h < 24; h++ {
			start := day.Add(time.Duration(h) * time.Hour)
			costs = append(costs, cost(
				start.Format(time.RFC3339),
				start.Add(time.Hour).Format(time.RFC3339),
				"0.25"))
		}

		got := bucketCosts(costs, nil, defaultLevels)
		Expect(got).To(HaveLen(24))
		var total decimal.Decimal
		for i, row := range got {
			Expect(row.Grain).To(Equal(models.ConfigCostLevel1))
			Expect(row.PeriodStart).To(Equal(day.Add(time.Duration(i) * time.Hour)))
			Expect(row.PeriodEnd).To(Equal(row.PeriodStart.Add(time.Hour)))
			total = total.Add(row.EffectiveCost)
		}
		Expect(total.String()).To(Equal("6"))
	})

	It("merges sub-hour rows sharing one hour", func() {
		var costs []v1.ExternalCost
		hour := ts("2026-08-03T09:00:00Z")
		for q := 0; q < 4; q++ {
			start := hour.Add(time.Duration(q) * 15 * time.Minute)
			costs = append(costs, cost(
				start.Format(time.RFC3339),
				start.Add(15*time.Minute).Format(time.RFC3339),
				"1.5"))
		}

		got := bucketCosts(costs, nil, defaultLevels)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Grain).To(Equal(models.ConfigCostLevel1))
		Expect(got[0].PeriodStart).To(Equal(hour))
		Expect(got[0].PeriodEnd).To(Equal(hour.Add(time.Hour)))
		Expect(got[0].EffectiveCost.String()).To(Equal("6"))
	})

	It("merges multi-hour rows landing in the same day", func() {
		// Periods longer than an hour snap to the day, so these two do merge.
		day := ts("2026-08-03T00:00:00Z")
		got := bucketCosts([]v1.ExternalCost{
			cost("2026-08-03T00:00:00Z", "2026-08-03T06:00:00Z", "4"),
			cost("2026-08-03T06:00:00Z", "2026-08-03T12:00:00Z", "5"),
		}, nil, defaultLevels)

		Expect(got).To(HaveLen(1))
		Expect(got[0].Grain).To(Equal(models.ConfigCostLevel2))
		Expect(got[0].PeriodStart).To(Equal(day))
		Expect(got[0].EffectiveCost.String()).To(Equal("9"))
	})

	It("keeps a month-long charge as a single 30d row", func() {
		got := bucketCosts([]v1.ExternalCost{
			cost("2026-08-01T00:00:00Z", "2026-09-01T00:00:00Z", "300"),
		}, nil, defaultLevels)

		Expect(got).To(HaveLen(1))
		Expect(got[0].Grain).To(Equal(models.ConfigCostLevel3))
		Expect(got[0].EffectiveCost.String()).To(Equal("300"))
	})

	It("separates distinct SKUs within the same bucket", func() {
		got := bucketCosts([]v1.ExternalCost{
			cost("2026-08-03T01:00:00Z", "2026-08-03T01:30:00Z", "1", withSKU("sku-a")),
			cost("2026-08-03T01:30:00Z", "2026-08-03T02:00:00Z", "2", withSKU("sku-b")),
		}, nil, defaultLevels)

		// Same hour bucket, so only the SKU can be separating them.
		Expect(got).To(HaveLen(2))
		Expect(got[0].PeriodStart).To(Equal(got[1].PeriodStart))
		Expect(got[0].Fingerprint).ToNot(Equal(got[1].Fingerprint))
	})

	It("never merges across source keys", func() {
		a := cost("2026-08-03T01:00:00Z", "2026-08-03T01:30:00Z", "1")
		b := cost("2026-08-03T02:00:00Z", "2026-08-03T03:00:00Z", "2")
		a.SourceKey, b.SourceKey = "source:a", "source:b"
		got := bucketCosts([]v1.ExternalCost{a, b}, nil, defaultLevels)
		Expect(got).To(HaveLen(2))
		Expect([]string{got[0].SourceKey, got[1].SourceKey}).To(ConsistOf("source:a", "source:b"))
	})

	It("propagates structured unmatched target identity", func() {
		a := cost("2026-08-03T01:00:00Z", "2026-08-03T01:30:00Z", "1")
		a.ConfigExternalID.ConfigType = "AWS::EC2::Instance"
		a.ConfigExternalID.ScraperID = "all"
		a.ConfigExternalID.Labels = map[string]string{"account": "prod"}
		got := bucketCosts([]v1.ExternalCost{a}, nil, defaultLevels)
		Expect(got[0].ExternalConfigType).ToNot(BeNil())
		Expect(*got[0].ExternalConfigType).To(Equal("AWS::EC2::Instance"))
		Expect(*got[0].ExternalConfigScraperID).To(Equal("all"))
		Expect(got[0].ExternalConfigLabels).To(HaveKeyWithValue("account", "prod"))
	})

	It("never merges across currencies", func() {
		got := bucketCosts([]v1.ExternalCost{
			cost("2026-08-03T01:00:00Z", "2026-08-03T01:30:00Z", "1", withCurrency("USD")),
			cost("2026-08-03T01:30:00Z", "2026-08-03T02:00:00Z", "1", withCurrency("EUR")),
		}, nil, defaultLevels)

		Expect(got).To(HaveLen(2))
		Expect(got[0].PeriodStart).To(Equal(got[1].PeriodStart))
		currencies := []string{got[0].BillingCurrency, got[1].BillingCurrency}
		Expect(currencies).To(ConsistOf("USD", "EUR"))
	})

	It("keeps a negative correction as its own row", func() {
		correction := cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "-5")
		correction.ChargeClass = "Correction"

		got := bucketCosts([]v1.ExternalCost{
			cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "12"),
			correction,
		}, nil, defaultLevels)

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

		Expect(bucketCosts(input, nil, defaultLevels)).To(Equal(bucketCosts(input, nil, defaultLevels)))
	})

	It("keeps the resource id as provenance once a target is resolved", func() {
		// config_costs.config_id is NOT NULL, so resolution (to the resource or to the
		// scraper's root) always happens before bucketing. The resource id survives on
		// the row regardless, so a root-attributed charge still shows what it was for.
		got := bucketCosts([]v1.ExternalCost{
			cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "3"),
		}, nil, defaultLevels)
		Expect(got).To(HaveLen(1))
		Expect(got[0].ConfigID).To(Equal(testCostTarget))
		Expect(got[0].ExternalID).ToNot(BeNil())
		Expect(*got[0].ExternalID).To(Equal("i-0abc"))
	})

	It("drops a cost that reached bucketing with no target", func() {
		// Defensive: an unresolved row would otherwise be attributed to the nil UUID.
		unresolved := cost("2026-08-03T01:00:00Z", "2026-08-03T02:00:00Z", "3")
		unresolved.ConfigID = nil
		Expect(bucketCosts([]v1.ExternalCost{unresolved}, nil, defaultLevels)).To(BeEmpty())
	})

	It("does not duplicate a bucket when a passthrough field is added", func() {
		a := cost("2026-08-03T01:00:00Z", "2026-08-03T01:30:00Z", "1")
		a.Focus = map[string]any{"Tags": map[string]any{"env": "prod"}}
		b := cost("2026-08-03T01:30:00Z", "2026-08-03T02:00:00Z", "2")
		b.Focus = map[string]any{
			"Tags":              map[string]any{"env": "prod"},
			"NewProviderColumn": "added-mid-period",
		}

		got := bucketCosts([]v1.ExternalCost{a, b}, nil, defaultLevels)
		Expect(got).To(HaveLen(1))
		Expect(got[0].EffectiveCost.String()).To(Equal("3"))
	})

	It("sums the optional metrics and never the unit prices", func() {
		a := cost("2026-08-03T01:00:00Z", "2026-08-03T01:30:00Z", "1")
		a.ListCost = decPtr(decimal.NewFromInt(4))
		a.PricingQuantity = decPtr(decimal.NewFromInt(10))
		a.PricingUnit = "Hours"
		a.Focus = map[string]any{"ListUnitPrice": 0.4}

		b := cost("2026-08-03T01:30:00Z", "2026-08-03T02:00:00Z", "1")
		b.ListCost = decPtr(decimal.NewFromInt(4))
		b.PricingQuantity = decPtr(decimal.NewFromInt(10))
		b.PricingUnit = "Hours"
		b.Focus = map[string]any{"ListUnitPrice": 0.4}

		got := bucketCosts([]v1.ExternalCost{a, b}, nil, defaultLevels)
		Expect(got).To(HaveLen(1))
		Expect(got[0].ListCost.String()).To(Equal("8"))
		Expect(got[0].PricingQuantity.String()).To(Equal("20"))
		// Row-scoped prices are carried through untouched, never aggregated.
		Expect(got[0].Focus["ListUnitPrice"]).To(BeEquivalentTo(0.4))
	})
})

func decPtr(d decimal.Decimal) *decimal.Decimal { return &d }
