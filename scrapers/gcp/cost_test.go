// Covers the parts of the billing-export reader that do not need BigQuery: the query text,
// identifier validation, and the mapping from an export row onto an ExternalCost.
package gcp

import (
	"testing"
	"time"

	"github.com/onsi/gomega"

	v1 "github.com/flanksource/config-db/api/v1"
)

func TestCostLookbackBoundary(t *testing.T) {
	g := gomega.NewWithT(t)
	now := time.Date(2026, 8, 12, 23, 59, 30, 0, time.FixedZone("x", -5*3600))
	g.Expect(costLookbackBoundary(now, 0)).To(gomega.Equal(time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC)))
	g.Expect(costLookbackBoundary(now, 2)).To(gomega.Equal(time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)))
}

func TestBuildCostQuery(t *testing.T) {
	g := gomega.NewWithT(t)

	query, err := buildCostQuery("my-proj", "billing_export", "gcp_billing_export_resource_v1_01ABCD")
	g.Expect(err).ToNot(gomega.HaveOccurred())
	g.Expect(query).To(gomega.ContainSubstring("`my-proj.billing_export.gcp_billing_export_resource_v1_01ABCD`"))

	// Credits are a repeated record, so the effective cost has to unnest them. They are
	// negative, which is why this adds.
	g.Expect(query).To(gomega.ContainSubstring("SELECT SUM(c.amount) FROM UNNEST(credits) c"))
	g.Expect(query).To(gomega.ContainSubstring("CAST(SUM(cost) AS STRING) AS billed_cost"))

	// The lower bound is a query parameter rather than interpolated text.
	g.Expect(query).To(gomega.ContainSubstring("usage_start_time >= @since"))

	// Money must not round trip through float64 on the way to decimal, so every numeric
	// column leaves BigQuery as a string.
	for _, column := range []string{"billed_cost", "effective_cost", "credit_amount", "pricing_quantity"} {
		g.Expect(query).To(gomega.ContainSubstring("AS STRING) AS " + column))
	}
}

func TestBuildCostQueryRejectsBadIdentifiers(t *testing.T) {
	g := gomega.NewWithT(t)
	// Identifiers cannot be query parameters, so anything that could break out of the
	// table reference is refused rather than quoted.
	for _, c := range []struct{ project, dataset, table string }{
		{"my-proj", "bad-dataset", "t"},
		{"my-proj", "d", "t`; DROP"},
		{"my proj", "d", "t"},
	} {
		_, err := buildCostQuery(c.project, c.dataset, c.table)
		g.Expect(err).To(gomega.HaveOccurred(), "expected %q.%q.%q to be rejected", c.project, c.dataset, c.table)
	}
}

func TestCostRowToExternalCost(t *testing.T) {
	g := gomega.NewWithT(t)
	start := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	row := costRow{
		ResourceGlobalName: "//compute.googleapis.com/projects/demo/zones/us-central1-a/instances/vm-1",
		ResourceName:       "vm-1",
		ServiceDescription: "Compute Engine",
		SkuID:              "sku-123",
		ProjectID:          "demo",
		BillingAccountID:   "01ABCD-2345EF-67890A",
		Region:             "us-central1",
		Currency:           "USD",
		CostType:           "regular",
		PricingUnit:        "hour",
		UsageStartTime:     start,
		UsageEndTime:       start.Add(time.Hour),
		BilledCost:         "1.50",
		EffectiveCost:      "1.20",
		CreditAmount:       "-0.30",
		PricingQuantity:    "1",
	}

	cost, err := row.toExternalCost()
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// The global name is the same full resource name the asset scrape stores as an alias,
	// which is what lets the charge resolve to the instance.
	g.Expect(cost.ResourceID).To(gomega.Equal(row.ResourceGlobalName))
	g.Expect(cost.BilledCost.String()).To(gomega.Equal("1.5"))
	g.Expect(cost.EffectiveCost.String()).To(gomega.Equal("1.2"))
	g.Expect(cost.SubAccountID).To(gomega.Equal("demo"))
	g.Expect(cost.BillingAccountID).To(gomega.Equal("01ABCD-2345EF-67890A"))
	g.Expect(cost.ChargeCategory).To(gomega.Equal("Usage"))

	// The root has to match the project config item, whose external id is qualified.
	g.Expect(cost.RootConfigID.ConfigType).To(gomega.Equal(v1.GCPProject))
	g.Expect(cost.RootConfigID.ExternalID).To(gomega.Equal("projects/demo"))
}

func TestCostRowFallsBackToUnallocated(t *testing.T) {
	g := gomega.NewWithT(t)

	// Neither resource field is set, which is the shape of tax and support charges.
	row := costRow{ProjectID: "demo", BillingAccountID: "01ABCD", ServiceDescription: "Tax", CostType: "tax"}
	g.Expect(row.stableResourceID()).To(gomega.Equal("gcp:unallocated:01ABCD:demo:Tax:tax"))

	// Only the service-local name is set, which many services do instead.
	row.ResourceName = "some-resource"
	g.Expect(row.stableResourceID()).To(gomega.Equal("some-resource"))

	category, class := chargeKind("tax")
	g.Expect(category).To(gomega.Equal("Tax"))
	g.Expect(class).To(gomega.BeEmpty())

	category, class = chargeKind("adjustment")
	g.Expect(category).To(gomega.Equal("Adjustment"))
	g.Expect(class).To(gomega.Equal("Correction"))
}

func TestCostSourceKeyExcludesCredentials(t *testing.T) {
	g := gomega.NewWithT(t)
	config := v1.GCP{}
	config.ConnectionName = "connection://gcp/default"
	key := costSourceKey(config, "my-proj", "billing_export", "tbl")
	g.Expect(key).To(gomega.Equal("gcp-billing:connection=connection://gcp/default:my-proj.billing_export.tbl"))

	g.Expect(costSourceKey(v1.GCP{}, "my-proj", "d", "t")).To(gomega.ContainSubstring("credentials=ambient"))
}
