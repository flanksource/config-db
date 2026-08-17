// ExternalCost is a FOCUS v1.4 cost line item emitted by any scraper through the
// `external_costs` reserved key, and resolved to a config_costs row during save.
package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/duty/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ExternalCost carries one charge period's spend for a single resource.
// +kubebuilder:object:generate=false
type ExternalCost struct {
	ConfigID         *uuid.UUID `json:"config_id,omitempty"`
	ConfigExternalID ExternalID `json:"external_config_id,omitempty"`
	ResourceID       string     `json:"resource_id,omitempty"`
	ScraperID        string     `json:"scraper_id,omitempty"`

	// RootConfigID is the emitting scraper's root config item — AWS::::Account,
	// GCP::Project, and so on. Every scraper package hardcodes its own.
	//
	// Spend that has no resource of its own (tax, support, credits) or whose ResourceID
	// does not resolve is attributed here. config_costs.config_id is NOT NULL, so this is
	// what stops unattributable spend from being dropped; ResourceID is still stored as
	// provenance, so the row shows what the money was actually for.
	RootConfigID ExternalID `json:"root_config_id,omitempty"`

	// SourceKey identifies the producer namespace. SaveResults supplies
	// scraper:<scraper UUID> when it is omitted.
	SourceKey string `json:"source_key,omitempty"`

	ChargePeriodStart time.Time `json:"charge_period_start"`
	ChargePeriodEnd   time.Time `json:"charge_period_end"`

	// Pointers distinguish a missing required monetary field from an explicit zero.
	BilledCost      *decimal.Decimal `json:"billed_cost"`
	EffectiveCost   *decimal.Decimal `json:"effective_cost"`
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

	// Focus retains FOCUS dimensions without a dedicated field and x_* columns.
	Focus types.JSONMap `json:"-"`
}

const ChargeCategoryUsage = "Usage"

var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

// costFieldAliases accepts both the API's snake_case and native FOCUS spelling.
var costFieldAliases = map[string][]string{
	"config_id":           {"config_id", "ConfigId", "ConfigID"},
	"external_config_id":  {"external_config_id", "ExternalConfigId", "ExternalConfigID"},
	"root_config_id":      {"root_config_id", "RootConfigId", "RootConfigID"},
	"resource_id":         {"resource_id", "ResourceId", "ResourceID"},
	"scraper_id":          {"scraper_id", "ScraperId", "ScraperID"},
	"source_key":          {"source_key", "SourceKey"},
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

func equivalentJSON(a, b json.RawMessage) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

func (c *ExternalCost) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	canonical := make(map[string]json.RawMessage, len(raw))
	consumed := make(map[string]struct{}, len(raw))
	for field, aliases := range costFieldAliases {
		var selected json.RawMessage
		var selectedAlias string
		for _, alias := range aliases {
			v, ok := raw[alias]
			if !ok {
				continue
			}
			consumed[alias] = struct{}{}
			if selected != nil && !equivalentJSON(selected, v) {
				return fmt.Errorf("external cost has conflicting aliases %q and %q for %s", selectedAlias, alias, field)
			}
			if selected == nil {
				selected, selectedAlias = v, alias
			}
		}
		if selected != nil {
			canonical[field] = selected
		}
	}

	type wire ExternalCost
	normalized, err := json.Marshal(canonical)
	if err != nil {
		return err
	}
	var decoded wire
	if err := json.Unmarshal(normalized, &decoded); err != nil {
		return err
	}
	*c = ExternalCost(decoded)

	for key, value := range raw {
		if _, ok := consumed[key]; ok {
			continue
		}
		if c.Focus == nil {
			c.Focus = types.JSONMap{}
		}
		var v any
		if err := json.Unmarshal(value, &v); err != nil {
			return fmt.Errorf("failed to unmarshal external cost key %q: %w", key, err)
		}
		c.Focus[key] = v
	}
	return nil
}

// MarshalJSON emits the stable snake_case API shape and losslessly adds unknown FOCUS
// fields. Dedicated fields always win over colliding Focus keys.
func (c ExternalCost) MarshalJSON() ([]byte, error) {
	type wire ExternalCost
	base, err := json.Marshal(wire(c))
	if err != nil {
		return nil, err
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(base, &out); err != nil {
		return nil, err
	}
	recognized := map[string]struct{}{}
	for _, aliases := range costFieldAliases {
		for _, alias := range aliases {
			recognized[alias] = struct{}{}
		}
	}
	for key, value := range c.Focus {
		if _, reserved := recognized[key]; reserved {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal external cost key %q: %w", key, err)
		}
		out[key] = encoded
	}
	return json.Marshal(out)
}

// Fingerprint hashes the stable, explicitly modeled identity fields only. Focus is a
// lossless passthrough payload, not identity: providers may add passthrough columns at any
// time, and including them would turn a schema change into a second billable observation.
//
// The account identifiers are hashed here even though they are stored inside the focus
// payload rather than in their own columns. Demoting them from columns must not demote
// them out of identity, or spend from two sub-accounts sharing a resource id would merge
// into one row under whichever account happened to be seen first.
//
// SourceKey is part of the database merge key rather than the fingerprint, so identical
// dimensions arriving from two different feeds stay separate rows.
func (c ExternalCost) Fingerprint() string {
	labels, _ := json.Marshal(c.ConfigExternalID.Labels)
	selector := strings.Join([]string{c.ConfigExternalID.ConfigType, c.ConfigExternalID.ScraperID, string(labels)}, "\x00")
	parts := []string{
		c.resourceKey(), selector, c.chargeCategory(), c.ChargeClass, c.ServiceName,
		c.ServiceCategory, c.SkuID, c.RegionID, c.BillingAccountID,
		c.SubAccountID, c.BillingCurrency, c.PricingUnit,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func (c ExternalCost) resourceKey() string {
	if strings.TrimSpace(c.ResourceID) != "" {
		return NormalizeExternalID(c.ResourceID)
	}
	if c.ConfigExternalID.ExternalID != "" {
		return NormalizeExternalID(c.ConfigExternalID.ExternalID)
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

// HasConfigRef reports whether this cost can be attributed to a config item — either to a
// specific resource, or to the emitting scraper's root.
//
// Tests ConfigExternalID.ExternalID rather than ExternalID.IsEmpty(): a cost may carry an
// external id with no config type (the type usually lives in the scraper spec, not the
// scraped body), and lookup by external id alone still resolves.
func (c ExternalCost) HasConfigRef() bool {
	return c.ConfigID != nil || c.ConfigExternalID.ConfigID != "" ||
		strings.TrimSpace(c.ConfigExternalID.ExternalID) != "" ||
		strings.TrimSpace(c.ResourceID) != "" || c.HasRootRef()
}

// HasRootRef reports whether a fallback target is available for spend with no resource.
func (c ExternalCost) HasRootRef() bool {
	return c.RootConfigID.ConfigID != "" || strings.TrimSpace(c.RootConfigID.ExternalID) != ""
}

func (c *ExternalCost) Validate() error {
	if !c.HasConfigRef() {
		return fmt.Errorf("external cost has no config reference, resource_id, or root_config_id")
	}
	if c.ConfigID != nil && c.ConfigExternalID.ConfigID != "" && !strings.EqualFold(c.ConfigID.String(), c.ConfigExternalID.ConfigID) {
		return fmt.Errorf("external cost has conflicting config_id values %s and %s", c.ConfigID, c.ConfigExternalID.ConfigID)
	}
	if c.ChargePeriodStart.IsZero() || c.ChargePeriodEnd.IsZero() {
		return fmt.Errorf("external cost is missing charge_period_start or charge_period_end")
	}
	if !c.ChargePeriodEnd.After(c.ChargePeriodStart) {
		return fmt.Errorf("external cost charge_period_end (%s) must be after charge_period_start (%s)", c.ChargePeriodEnd.Format(time.RFC3339), c.ChargePeriodStart.Format(time.RFC3339))
	}
	if c.BilledCost == nil {
		return fmt.Errorf("external cost is missing billed_cost")
	}
	if c.EffectiveCost == nil {
		return fmt.Errorf("external cost is missing effective_cost")
	}
	c.BillingCurrency = strings.ToUpper(strings.TrimSpace(c.BillingCurrency))
	if !currencyCode.MatchString(c.BillingCurrency) {
		return fmt.Errorf("external cost billing_currency must be a nonempty 3-letter code")
	}
	// Identifiers keep the case the provider used: they are stored as provenance. Both
	// places that compare them normalize independently — findConfigMatches when looking a
	// config item up, and resourceKey when hashing the fingerprint — so normalizing here
	// would only lose information.
	c.ResourceID = strings.TrimSpace(c.ResourceID)
	c.ConfigExternalID.ExternalID = strings.TrimSpace(c.ConfigExternalID.ExternalID)
	c.SourceKey = strings.TrimSpace(c.SourceKey)
	return nil
}

func (c ExternalCost) String() string { return c.Pretty().String() }

func (c ExternalCost) Pretty() api.Text {
	t := clicky.Text(c.resourceKey(), "font-bold")
	if c.ServiceName != "" {
		t = t.Append(" service=", "text-muted").Append(c.ServiceName)
	}
	amount := "<missing>"
	if c.EffectiveCost != nil {
		amount = c.EffectiveCost.String()
	}
	t = t.Append(" ", "").Append(fmt.Sprintf("%s %s", amount, c.BillingCurrency), "text-green-700")
	return t.Append(" ", "").Append(c.ChargePeriodStart.Format(time.RFC3339), "text-muted")
}
