package aws

import (
	"strings"
	"testing"
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	. "github.com/onsi/gomega"
)

func baseCURColumns() map[string]bool {
	columns := map[string]bool{}
	for _, column := range requiredCURColumns {
		columns[column] = true
	}
	return columns
}

func TestCostLookbackBoundary(t *testing.T) {
	g := NewWithT(t)
	now := time.Date(2026, 8, 12, 23, 59, 30, 0, time.FixedZone("x", -5*3600))
	g.Expect(costLookbackBoundary(now, 0)).To(Equal(time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC)))
}

func TestBuildCostQueryVariants(t *testing.T) {
	g := NewWithT(t)
	columns := baseCURColumns()
	for _, c := range []string{"reservation_effective_cost", "reservation_unused_amortized_upfront_fee_for_billing_period", "reservation_unused_recurring_fee", "savings_plan_savings_plan_effective_cost", "savings_plan_total_commitment_to_date", "savings_plan_used_commitment", "line_item_net_unblended_cost", "product_region"} {
		columns[c] = true
	}
	query, err := buildCostQuery("cur_db", "cur_table", columns, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(query).To(ContainSubstring(`FROM "cur_db"."cur_table"`))
	g.Expect(query).To(ContainSubstring("WHEN line_item_line_item_type = 'DiscountedUsage' THEN COALESCE(reservation_effective_cost, 0)"))
	g.Expect(query).To(ContainSubstring("WHEN line_item_line_item_type = 'RIFee' THEN COALESCE(reservation_unused_amortized_upfront_fee_for_billing_period, 0) + COALESCE(reservation_unused_recurring_fee, 0)"))
	g.Expect(query).To(ContainSubstring("WHEN line_item_line_item_type = 'SavingsPlanCoveredUsage'"))
	g.Expect(query).To(ContainSubstring("WHEN line_item_line_item_type = 'SavingsPlanRecurringFee'"))
	g.Expect(query).To(ContainSubstring("TIMESTAMP '2026-07-01 00:00:00'"))

	delete(columns, "line_item_unblended_cost")
	_, err = buildCostQuery("cur_db", "cur_table", columns, time.Now())
	g.Expect(err).To(MatchError(ContainSubstring("line_item_unblended_cost")))
	columns["line_item_unblended_cost"] = true
	_, err = buildCostQuery("bad-db", "cur_table", columns, time.Now())
	g.Expect(err).To(HaveOccurred())
}

func TestCostReportingValidationAndSource(t *testing.T) {
	g := NewWithT(t)
	g.Expect(costReportingEmpty(v1.CostReporting{})).To(BeTrue())
	g.Expect(validateCostReporting(v1.CostReporting{Table: "cur"})).To(MatchError(ContainSubstring("s3BucketPath, database, region")))
	config := v1.AWS{
		AWSConnection: v1.AWSConnection{
			ConnectionName: "connection://default/billing",
			AssumeRole:     "arn:aws:iam::123456789012:role/billing",
		},
		CostReporting: v1.CostReporting{Region: "us-east-1"},
	}
	g.Expect(costSourceKey(config, "db", "table")).To(Equal("aws-cur:connection=connection://default/billing,role=arn:aws:iam::123456789012:role/billing,region=us-east-1:db.table"))
}

func TestCURCostConversion(t *testing.T) {
	g := NewWithT(t)
	base := curLineItem{UsageStartDate: "2026-08-01T00:00:00Z", UsageEndDate: "2026-08-02T00:00:00Z",
		ProductCode: "AWSSupport", UsageAccountID: "sub", PayerAccountID: "payer", Currency: "USD",
		BilledCost: "-2.50", EffectiveCost: "-2.25", PricingQuantity: "0", LineItemType: "Credit",
		SKU: "sku", UsageType: "support", Operation: "monthly"}
	cost, err := base.toExternalCost()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(cost.ResourceID).To(Equal("aws:unallocated:payer:sub:AWSSupport:credit"))
	g.Expect(cost.ChargeCategory).To(Equal("Credit"))
	g.Expect(cost.ChargeClass).To(Equal("Correction"))
	g.Expect(cost.EffectiveCost.String()).To(Equal("-2.25"))
	g.Expect(cost.Focus).To(HaveKeyWithValue("UsageType", "support"))
	base.LineItemType = "Tax"
	cost, err = base.toExternalCost()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(cost.ChargeCategory).To(Equal("Tax"))
	base.LineItemType = "RIFee"
	cost, err = base.toExternalCost()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(cost.ChargeCategory).To(Equal("Purchase"))
	base.PricingQuantity = "not-a-number"
	_, err = base.toExternalCost()
	g.Expect(err).To(HaveOccurred())
	g.Expect(strings.ToLower(err.Error())).To(ContainSubstring("pricing quantity"))
}
