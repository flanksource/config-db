// Covers the tolerant FOCUS/snake_case unmarshaller and stable cost fingerprint.
package v1

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ExternalCost unmarshalling", func() {
	It("accepts a raw FOCUS export row", func() {
		var cost ExternalCost
		Expect(json.Unmarshal([]byte(`{
			"ResourceId": "i-0abc",
			"ChargePeriodStart": "2026-08-03T14:00:00Z",
			"ChargePeriodEnd": "2026-08-03T15:00:00Z",
			"BilledCost": 0.0416,
			"EffectiveCost": 0.0312,
			"BillingCurrency": "USD",
			"ChargeCategory": "Usage",
			"ServiceName": "AmazonEC2",
			"SkuId": "BoxUsage:m5.large",
			"RegionId": "us-east-1",
			"SubAccountId": "123456789012",
			"PricingQuantity": 1,
			"PricingUnit": "Hours"
		}`), &cost)).To(Succeed())

		Expect(cost.ResourceID).To(Equal("i-0abc"))
		Expect(cost.ChargePeriodStart.Format("2006-01-02T15:04:05Z")).To(Equal("2026-08-03T14:00:00Z"))
		Expect(cost.BilledCost.String()).To(Equal("0.0416"))
		Expect(cost.EffectiveCost.String()).To(Equal("0.0312"))
		Expect(cost.SkuID).To(Equal("BoxUsage:m5.large"))
		Expect(cost.RegionID).To(Equal("us-east-1"))
		Expect(cost.SubAccountID).To(Equal("123456789012"))
		Expect(cost.PricingUnit).To(Equal("Hours"))
		Expect(cost.PricingQuantity).ToNot(BeNil())
		Expect(cost.Validate()).To(Succeed())
	})

	It("accepts snake_case", func() {
		var cost ExternalCost
		Expect(json.Unmarshal([]byte(`{
			"resource_id": "i-0abc",
			"charge_period_start": "2026-08-03T00:00:00Z",
			"charge_period_end": "2026-08-04T00:00:00Z",
			"billed_cost": 1.5,
			"effective_cost": 1.2,
			"billing_currency": "EUR",
			"service_name": "AmazonEC2"
		}`), &cost)).To(Succeed())

		Expect(cost.ResourceID).To(Equal("i-0abc"))
		Expect(cost.BillingCurrency).To(Equal("EUR"))
		Expect(cost.EffectiveCost.String()).To(Equal("1.2"))
	})

	It("rejects conflicting duplicate aliases", func() {
		var cost ExternalCost
		Expect(json.Unmarshal([]byte(`{"resource_id":"a","ResourceId":"b"}`), &cost)).
			To(MatchError(ContainSubstring("conflicting aliases")))
	})

	It("marshals recognized fields without Focus overrides", func() {
		var cost ExternalCost
		Expect(json.Unmarshal([]byte(`{
			"ResourceId":"i-0abc","ChargePeriodStart":"2026-08-03T00:00:00Z",
			"ChargePeriodEnd":"2026-08-04T00:00:00Z","BilledCost":0,
			"EffectiveCost":0,"BillingCurrency":"usd","x_team":"platform"
		}`), &cost)).To(Succeed())
		cost.Focus["resource_id"] = "evil"
		encoded, err := json.Marshal(cost)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(encoded)).To(ContainSubstring(`"resource_id":"i-0abc"`))
		Expect(string(encoded)).To(ContainSubstring(`"x_team":"platform"`))
	})

	It("keeps unrecognized keys, including x_* custom columns", func() {
		var cost ExternalCost
		Expect(json.Unmarshal([]byte(`{
			"resource_id": "i-0abc",
			"charge_period_start": "2026-08-03T00:00:00Z",
			"charge_period_end": "2026-08-04T00:00:00Z",
			"billed_cost": 1,
			"effective_cost": 1,
			"billing_currency": "USD",
			"x_CostCenter": "platform",
			"ListUnitPrice": 0.4,
			"Tags": {"env": "prod"}
		}`), &cost)).To(Succeed())

		Expect(cost.Focus).To(HaveKeyWithValue("x_CostCenter", "platform"))
		Expect(cost.Focus).To(HaveKeyWithValue("ListUnitPrice", BeEquivalentTo(0.4)))
		Expect(cost.Focus).To(HaveKey("Tags"))
		// Recognized keys are not duplicated into the passthrough payload.
		Expect(cost.Focus).ToNot(HaveKey("billed_cost"))
		Expect(cost.Focus).ToNot(HaveKey("resource_id"))
	})
})

var _ = Describe("ExternalCost.Validate", func() {
	base := func() ExternalCost {
		var cost ExternalCost
		Expect(json.Unmarshal([]byte(`{
			"resource_id": "i-0abc",
			"charge_period_start": "2026-08-03T00:00:00Z",
			"charge_period_end": "2026-08-04T00:00:00Z",
			"billed_cost": 1,
			"effective_cost": 1,
			"billing_currency": "USD"
		}`), &cost)).To(Succeed())
		return cost
	}

	It("rejects an inverted charge period", func() {
		cost := base()
		cost.ChargePeriodEnd = cost.ChargePeriodStart.Add(-1)
		Expect(cost.Validate()).To(MatchError(ContainSubstring("must be after")))
	})

	It("rejects a zero-length charge period", func() {
		cost := base()
		cost.ChargePeriodEnd = cost.ChargePeriodStart
		Expect(cost.Validate()).To(MatchError(ContainSubstring("must be after")))
	})

	It("distinguishes missing costs from explicit zero", func() {
		cost := base()
		cost.BilledCost = nil
		Expect(cost.Validate()).To(MatchError(ContainSubstring("billed_cost")))
		cost = base()
		cost.EffectiveCost = nil
		Expect(cost.Validate()).To(MatchError(ContainSubstring("effective_cost")))
	})

	It("normalizes and validates currency", func() {
		cost := base()
		cost.BillingCurrency = " usd "
		Expect(cost.Validate()).To(Succeed())
		Expect(cost.BillingCurrency).To(Equal("USD"))
		cost.BillingCurrency = "US"
		Expect(cost.Validate()).To(MatchError(ContainSubstring("3-letter")))
	})

	It("rejects a missing currency", func() {
		cost := base()
		cost.BillingCurrency = ""
		Expect(cost.Validate()).To(MatchError(ContainSubstring("billing_currency")))
	})

	It("rejects a cost with nothing to attach it to", func() {
		cost := base()
		cost.ResourceID = ""
		Expect(cost.Validate()).To(MatchError(ContainSubstring("no config reference")))
	})
})

var _ = Describe("ExternalCost.Fingerprint", func() {
	base := ExternalCost{
		ResourceID:      "i-0abc",
		BillingCurrency: "USD",
		ChargeCategory:  "Usage",
		ServiceName:     "AmazonEC2",
		SkuID:           "sku-1",
	}

	It("ignores the metrics and the charge period", func() {
		other := base
		other.ChargePeriodStart = other.ChargePeriodStart.Add(1)
		Expect(other.Fingerprint()).To(Equal(base.Fingerprint()))
	})

	It("separates currencies", func() {
		other := base
		other.BillingCurrency = "EUR"
		Expect(other.Fingerprint()).ToNot(Equal(base.Fingerprint()))
	})

	It("keeps all Focus passthrough fields out of identity", func() {
		a, b := base, base
		a.Focus = map[string]any{"ListUnitPrice": 1.25, "PricingQuantity": 2}
		b.Focus = map[string]any{
			"ListUnitPrice":     9.75,
			"PricingQuantity":   200,
			"Tags":              map[string]any{"env": "prod"},
			"x_CostCenter":      "platform",
			"NewProviderColumn": "added-mid-period",
		}
		Expect(a.Fingerprint()).To(Equal(b.Fingerprint()))
	})

	It("scopes unmatched identity by its external selector", func() {
		a, b := base, base
		a.ConfigExternalID = ExternalID{ConfigType: "TypeA", ScraperID: "all", Labels: map[string]string{"env": "prod"}}
		b.ConfigExternalID = ExternalID{ConfigType: "TypeB", ScraperID: "all", Labels: map[string]string{"env": "prod"}}
		Expect(a.Fingerprint()).ToNot(Equal(b.Fingerprint()))
	})

	It("ignores Focus entirely", func() {
		// Focus is a lossless passthrough, not identity: a provider adding a column must
		// not turn one billable observation into two.
		a, b := base, base
		a.Focus = map[string]any{"Tags": map[string]any{"env": "prod", "team": "platform"}}
		b.Focus = map[string]any{"Tags": map[string]any{"team": "platform", "env": "dev"}, "NewColumn": true}
		Expect(a.Fingerprint()).To(Equal(b.Fingerprint()))
	})

	It("keeps the account identifiers in identity even though they are stored in focus", func() {
		// Demoting them out of their own columns must not demote them out of the merge
		// key, or two sub-accounts sharing a resource id would collapse into one row.
		a, b := base, base
		a.SubAccountID, b.SubAccountID = "111122223333", "444455556666"
		Expect(a.Fingerprint()).ToNot(Equal(b.Fingerprint()))

		a, b = base, base
		a.BillingAccountID, b.BillingAccountID = "payer-a", "payer-b"
		Expect(a.Fingerprint()).ToNot(Equal(b.Fingerprint()))
	})

	It("separates corrections from the original charge", func() {
		other := base
		other.ChargeClass = "Correction"
		Expect(other.Fingerprint()).ToNot(Equal(base.Fingerprint()))
	})

	It("treats an unset charge category as Usage", func() {
		other := base
		other.ChargeCategory = ""
		Expect(other.Fingerprint()).To(Equal(base.Fingerprint()))
	})
})
