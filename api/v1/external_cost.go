// ExternalCost is a FOCUS v1.4 cost line item emitted by any scraper through the
// `external_costs` reserved key, and resolved to a config_costs row during save.
package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/duty/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ExternalCost carries one charge period's spend for a single resource.
//
// The charge period is half-open — [ChargePeriodStart, ChargePeriodEnd) — matching
// FOCUS. Persistence snaps it to a clock-aligned bucket; see db.bucketFor.
//
// +kubebuilder:object:generate=false
type ExternalCost struct {
	ConfigID         *uuid.UUID `json:"config_id,omitempty"`
	ConfigExternalID ExternalID `json:"external_config_id,omitempty"`

	// ResourceID is the FOCUS ResourceId. It doubles as the config lookup key when no
	// explicit config reference is given, and is retained on the persisted row so
	// unmatched spend can be attached once the config item shows up.
	ResourceID string `json:"resource_id,omitempty"`

	// ScraperID controls how ConfigExternalID is resolved. Empty means the running
	// scraper; `all` disregards scraper ownership, which is what a SQL scraper emitting
	// costs for Kubernetes- or AWS-scraped resources needs.
	ScraperID string `json:"scraper_id,omitempty"`

	ChargePeriodStart time.Time `json:"charge_period_start"`
	ChargePeriodEnd   time.Time `json:"charge_period_end"`

	BilledCost      decimal.Decimal  `json:"billed_cost"`
	EffectiveCost   decimal.Decimal  `json:"effective_cost"`
	ListCost        *decimal.Decimal `json:"list_cost,omitempty"`
	ContractedCost  *decimal.Decimal `json:"contracted_cost,omitempty"`
	BillingCurrency string           `json:"billing_currency"`

	ChargeCategory   string `json:"charge_category,omitempty"`
	ChargeClass      string `json:"charge_class,omitempty"`
	ServiceName      string `json:"service_name,omitempty"`
	ServiceCategory  string `json:"service_category,omitempty"`
	SkuID            string `json:"sku_id,omitempty"`
	RegionID         string `json:"region_id,omitempty"`
	BillingAccountID string `json:"billing_account_id,omitempty"`
	SubAccountID     string `json:"sub_account_id,omitempty"`

	PricingQuantity *decimal.Decimal `json:"pricing_quantity,omitempty"`
	PricingUnit     string           `json:"pricing_unit,omitempty"`

	// Focus keeps every key that isn't one of the fields above: the FOCUS long tail
	// (Tags, SkuPriceDetails, CommitmentDiscount*, ...) and the x_* custom columns that
	// FOCUS v1.4 CustomColumnHandling requires providers to be able to carry through.
	Focus types.JSONMap `json:"-"`
}

// ChargeCategoryUsage is the FOCUS default when a row does not state one.
const ChargeCategoryUsage = "Usage"

// costFieldAliases maps each field to the keys that fill it. Both the FOCUS
// PascalCase column names and this repo's snake_case convention are accepted, so a raw
// FOCUS export row pastes in with no transform.
var costFieldAliases = map[string][]string{
	"config_id":           {"config_id", "ConfigId", "ConfigID"},
	"external_config_id":  {"external_config_id"},
	"resource_id":         {"resource_id", "ResourceId", "ResourceID"},
	"scraper_id":          {"scraper_id", "ScraperId"},
	"charge_period_start": {"charge_period_start", "ChargePeriodStart"},
	"charge_period_end":   {"charge_period_end", "ChargePeriodEnd"},
	"billed_cost":         {"billed_cost", "BilledCost"},
	"effective_cost":      {"effective_cost", "EffectiveCost"},
	"list_cost":           {"list_cost", "ListCost"},
	"contracted_cost":     {"contracted_cost", "ContractedCost"},
	"billing_currency":    {"billing_currency", "BillingCurrency"},
	"charge_category":     {"charge_category", "ChargeCategory"},
	"charge_class":        {"charge_class", "ChargeClass"},
	"service_name":        {"service_name", "ServiceName"},
	"service_category":    {"service_category", "ServiceCategory"},
	"sku_id":              {"sku_id", "SkuId", "SkuID"},
	"region_id":           {"region_id", "RegionId", "RegionID"},
	"billing_account_id":  {"billing_account_id", "BillingAccountId", "BillingAccountID"},
	"sub_account_id":      {"sub_account_id", "SubAccountId", "SubAccountID"},
	"pricing_quantity":    {"pricing_quantity", "PricingQuantity"},
	"pricing_unit":        {"pricing_unit", "PricingUnit"},
}

func (c *ExternalCost) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	canonical := make(map[string]json.RawMessage, len(raw))
	focus := types.JSONMap{}
	consumed := make(map[string]struct{}, len(raw))

	for field, aliases := range costFieldAliases {
		for _, alias := range aliases {
			v, ok := raw[alias]
			if !ok {
				continue
			}
			if _, taken := canonical[field]; !taken {
				canonical[field] = v
			}
			consumed[alias] = struct{}{}
		}
	}

	for key, v := range raw {
		if _, ok := consumed[key]; ok {
			continue
		}
		var decoded any
		if err := json.Unmarshal(v, &decoded); err != nil {
			return fmt.Errorf("failed to unmarshal external cost key %q: %w", key, err)
		}
		focus[key] = decoded
	}

	normalized, err := json.Marshal(canonical)
	if err != nil {
		return err
	}

	type alias ExternalCost
	var aux alias
	if err := json.Unmarshal(normalized, &aux); err != nil {
		return err
	}

	*c = ExternalCost(aux)
	if len(focus) > 0 {
		c.Focus = focus
	}
	return nil
}

// Fingerprint is the merge key for this cost within its bucket: a hash of the dimension
// tuple only. Metrics are deliberately excluded — two rows with the same dimensions in
// the same period are the same line item and their amounts merge.
func (c ExternalCost) Fingerprint() string {
	parts := []string{
		c.resourceKey(),
		c.chargeCategory(),
		c.ChargeClass,
		c.ServiceName,
		c.ServiceCategory,
		c.SkuID,
		c.RegionID,
		c.BillingAccountID,
		c.SubAccountID,
		c.BillingCurrency,
		c.PricingUnit,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// resourceKey identifies the resource this cost belongs to, for fingerprinting only.
func (c ExternalCost) resourceKey() string {
	if c.ResourceID != "" {
		return c.ResourceID
	}
	if c.ConfigExternalID.ExternalID != "" {
		return c.ConfigExternalID.ExternalID
	}
	if c.ConfigID != nil {
		return c.ConfigID.String()
	}
	return ""
}

func (c ExternalCost) chargeCategory() string {
	if c.ChargeCategory == "" {
		return ChargeCategoryUsage
	}
	return c.ChargeCategory
}

// HasConfigRef reports whether this cost can be attached to a config item, either
// directly or by external id lookup.
//
// Tests ConfigExternalID.ExternalID rather than ExternalID.IsEmpty(): a cost may carry an
// external id with no config type (the type usually lives in the scraper spec, not the
// scraped body), and lookup by external id alone still resolves.
func (c ExternalCost) HasConfigRef() bool {
	return c.ConfigID != nil || c.ConfigExternalID.ExternalID != "" || c.ResourceID != ""
}

func (c ExternalCost) Validate() error {
	if !c.HasConfigRef() {
		return fmt.Errorf("external cost has no config reference and no resource_id")
	}
	if c.ChargePeriodStart.IsZero() || c.ChargePeriodEnd.IsZero() {
		return fmt.Errorf("external cost is missing charge_period_start or charge_period_end")
	}
	if !c.ChargePeriodEnd.After(c.ChargePeriodStart) {
		return fmt.Errorf("external cost charge_period_end (%s) must be after charge_period_start (%s)",
			c.ChargePeriodEnd.Format(time.RFC3339), c.ChargePeriodStart.Format(time.RFC3339))
	}
	if c.BillingCurrency == "" {
		return fmt.Errorf("external cost is missing billing_currency")
	}
	return nil
}

func (c ExternalCost) String() string {
	return c.Pretty().String()
}

func (c ExternalCost) Pretty() api.Text {
	t := clicky.Text(c.resourceKey(), "font-bold")
	if c.ServiceName != "" {
		t = t.Append(" service=", "text-muted").Append(c.ServiceName)
	}
	t = t.Append(" ", "").Append(fmt.Sprintf("%s %s", c.EffectiveCost.String(), c.BillingCurrency), "text-green-700")
	t = t.Append(" ", "").Append(c.ChargePeriodStart.Format(time.RFC3339), "text-muted")
	return t
}
