// Covers the parts of the billing-export reader that do not need BigQuery: the query text,
// identifier validation, and the mapping from an export row onto an ExternalCost.
package gcp

import (
	"testing"

	"cloud.google.com/go/bigquery"
	"time"

	"github.com/onsi/gomega"

	v1 "github.com/flanksource/config-db/api/v1"
)

// allCostColumns is an export carrying the optional list-price columns.
var allCostColumns = map[string]bool{
	"cost_at_list":                    true,
	"cost_at_effective_price_default": true,
}

func TestCostLookbackBoundary(t *testing.T) {
	g := gomega.NewWithT(t)
	now := time.Date(2026, 8, 12, 23, 59, 30, 0, time.FixedZone("x", -5*3600))
	g.Expect(costLookbackBoundary(now, 0)).To(gomega.Equal(time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC)))
	g.Expect(costLookbackBoundary(now, 2)).To(gomega.Equal(time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)))
}

func TestBuildCostQuery(t *testing.T) {
	g := gomega.NewWithT(t)

	query, err := buildCostQuery("my-proj", "billing_export", "gcp_billing_export_resource_v1_01ABCD", allCostColumns)
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
		_, err := buildCostQuery(c.project, c.dataset, c.table, allCostColumns)
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
		ProjectNumber:      "210987654321",
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

	cost, err := row.toExternalCost("")
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// The global name is the same full resource name the asset scrape stores as an alias,
	// which is what lets the charge resolve to the instance.
	g.Expect(cost.ResourceID).To(gomega.Equal(row.ResourceGlobalName))
	g.Expect(cost.ConfigExternalID.ExternalID).To(gomega.Equal(row.ResourceGlobalName))
	g.Expect(cost.BilledCost.String()).To(gomega.Equal("1.5"))
	g.Expect(cost.EffectiveCost.String()).To(gomega.Equal("1.2"))
	g.Expect(cost.SubAccountID).To(gomega.Equal("demo"))
	g.Expect(cost.BillingAccountID).To(gomega.Equal("01ABCD-2345EF-67890A"))
	g.Expect(cost.ChargeCategory).To(gomega.Equal("Usage"))

	// The root has to match the alias asset inventory writes for a project, which is its
	// full resource name and carries the number rather than the id.
	g.Expect(cost.RootConfigID.ConfigType).To(gomega.Equal(v1.GCPProject))
	g.Expect(cost.RootConfigID.ExternalID).To(
		gomega.Equal("//cloudresourcemanager.googleapis.com/projects/210987654321"))

	// The scoping label matches the project tag the asset scrape writes, which is the id.
	g.Expect(cost.SubAccountID).To(gomega.Equal("demo"))
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

func TestProjectResourceName(t *testing.T) {
	g := gomega.NewWithT(t)

	row := costRow{ProjectID: "demo", ProjectNumber: "210987654321"}
	g.Expect(row.projectResourceName()).To(
		gomega.Equal("//cloudresourcemanager.googleapis.com/projects/210987654321"))

	// Without a number there is no identifier the asset scrape would have written, so the
	// row is left to skip rather than rooted somewhere it does not belong.
	g.Expect(costRow{ProjectID: "demo"}.projectResourceName()).To(gomega.BeEmpty())
}

func TestCostRootFallsBackToOrganization(t *testing.T) {
	g := gomega.NewWithT(t)
	const org = "//cloudresourcemanager.googleapis.com/organizations/123456789012"

	// A row that names a project is booked there, which keeps the charge where it was
	// incurred even when the organization is known.
	withProject := costRow{ProjectID: "demo", ProjectNumber: "210987654321"}
	root := withProject.costRoot(org)
	g.Expect(root.ConfigType).To(gomega.Equal(v1.GCPProject))
	g.Expect(root.ExternalID).To(
		gomega.Equal("//cloudresourcemanager.googleapis.com/projects/210987654321"))

	// Tax, support and billing-account adjustments name no project at all. Without the
	// organization they have nowhere to go and the money is dropped.
	noProject := costRow{CostType: "regular", ServiceDescription: "Duet AI"}
	root = noProject.costRoot(org)
	g.Expect(root.ConfigType).To(gomega.Equal(v1.GCPOrganization))
	g.Expect(root.ExternalID).To(gomega.Equal(org))

	// A project-scoped scrape names no organization, so such a row still has no root.
	g.Expect(noProject.costRoot("")).To(gomega.Equal(v1.ExternalID{}))
}

func TestOrganizationResourceName(t *testing.T) {
	g := gomega.NewWithT(t)
	const want = "//cloudresourcemanager.googleapis.com/organizations/123456789012"

	// Accepted either bare or already qualified.
	g.Expect(organizationResourceName(v1.GCP{Organization: "123456789012"})).To(gomega.Equal(want))
	g.Expect(organizationResourceName(v1.GCP{Organization: "organizations/123456789012"})).To(gomega.Equal(want))
	g.Expect(organizationResourceName(v1.GCP{})).To(gomega.BeEmpty())
}

func TestOptionalListPriceColumns(t *testing.T) {
	g := gomega.NewWithT(t)

	// Present: read and aggregated like the rest of the money.
	query, err := buildCostQuery("p", "d", "t", allCostColumns)
	g.Expect(err).ToNot(gomega.HaveOccurred())
	g.Expect(query).To(gomega.ContainSubstring("CAST(SUM(IFNULL(cost_at_list, 0)) AS STRING) AS list_cost"))
	g.Expect(query).To(gomega.ContainSubstring("AS contracted_cost"))

	// Absent: selected as NULL so the query still runs against an export written before
	// these columns existed, rather than failing on an unknown field.
	query, err = buildCostQuery("p", "d", "t", map[string]bool{})
	g.Expect(err).ToNot(gomega.HaveOccurred())
	g.Expect(query).To(gomega.ContainSubstring("CAST(NULL AS STRING) AS list_cost"))
	g.Expect(query).ToNot(gomega.ContainSubstring("cost_at_list,"))
}

func TestOptionalCostsReachTheExternalCost(t *testing.T) {
	g := gomega.NewWithT(t)
	start := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	row := costRow{
		ProjectID: "demo", ProjectNumber: "210987654321", Currency: "USD",
		UsageStartTime: start, UsageEndTime: start.Add(time.Hour),
		BilledCost: "1.50", EffectiveCost: "1.20", PricingQuantity: "1",
		ListCost:       bigquery.NullString{StringVal: "2.00", Valid: true},
		ContractedCost: bigquery.NullString{StringVal: "1.75", Valid: true},
	}

	cost, err := row.toExternalCost("")
	g.Expect(err).ToNot(gomega.HaveOccurred())
	g.Expect(cost.ListCost).ToNot(gomega.BeNil())
	g.Expect(cost.ListCost.String()).To(gomega.Equal("2"))
	g.Expect(cost.ContractedCost.String()).To(gomega.Equal("1.75"))

	// An export without them leaves the fields unset rather than reporting a list price of
	// zero, which would read as everything being free.
	row.ListCost, row.ContractedCost = bigquery.NullString{}, bigquery.NullString{}
	cost, err = row.toExternalCost("")
	g.Expect(err).ToNot(gomega.HaveOccurred())
	g.Expect(cost.ListCost).To(gomega.BeNil())
	g.Expect(cost.ContractedCost).To(gomega.BeNil())
}

// The full global name is the primary lookup identity. The generic cost resolver only
// falls back to its trailing segment when the exact name is absent from inventory.
func TestResourceLookupID(t *testing.T) {
	g := gomega.NewWithT(t)

	for _, globalName := range []string{
		"//compute.googleapis.com/projects/345678901234/zones/europe-west1-c/disk/1342029463145423244",
		"//compute.googleapis.com/projects/345678901234/zones/2103/instances/1109397387100823948",
		"//storage.googleapis.com/projects/demo/buckets/my-bucket",
		"//container.googleapis.com/projects/workload-prod-eu-02/locations/europe-west1/clusters/workload-prod-eu-02",
	} {
		g.Expect(costRow{ResourceGlobalName: globalName}.resourceLookupID()).To(gomega.Equal(globalName))
	}

	// Falls back to the service-local name when there is no global name.
	g.Expect(costRow{ResourceName: "vm-1"}.resourceLookupID()).To(gomega.Equal("vm-1"))

	// Spend with no resource must not resolve to anything: it belongs on the project or
	// organization, and a synthetic id is not something the asset scrape ever wrote.
	g.Expect(costRow{
		ProjectID: "demo", BillingAccountID: "01ABCD", ServiceDescription: "Tax", CostType: "tax",
	}.resourceLookupID()).To(gomega.BeEmpty())
}
