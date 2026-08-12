// Pins the bucketing rule: charge periods are snapped to a clock-aligned day, week, or
// month and merged within that bucket by dimension fingerprint. Pure, no database.
package db

import (
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/shopspring/decimal"
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
		BilledCost:        amt,
		EffectiveCost:     amt,
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
