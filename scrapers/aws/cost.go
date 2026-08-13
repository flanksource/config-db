// Reads AWS CUR from Athena and emits FOCUS-shaped ExternalCosts.
package aws

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/flanksource/duty/types"
	"github.com/shopspring/decimal"
	athena "github.com/uber/athenadriver/go"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

const defaultCostLookbackDays = 45

var curIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func quoteCURIdentifier(value string) (string, error) {
	if !curIdentifier.MatchString(value) {
		return "", fmt.Errorf("invalid Athena identifier %q", value)
	}
	return `"` + value + `"`, nil
}

func costLookbackBoundary(now time.Time, days int) time.Time {
	if days <= 0 {
		days = defaultCostLookbackDays
	}
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -days)
}

// CUR columns vary by report version. Expressions only reference discovered columns.
func curCostExpression(columns map[string]bool) string {
	ordinary := "line_item_unblended_cost"
	if columns["line_item_net_unblended_cost"] {
		ordinary = "COALESCE(line_item_net_unblended_cost, line_item_unblended_cost)"
	}
	cases := []string{}
	if columns["reservation_effective_cost"] {
		cases = append(cases, "WHEN line_item_line_item_type = 'DiscountedUsage' THEN COALESCE(reservation_effective_cost, 0)")
	}
	if columns["reservation_unused_amortized_upfront_fee_for_billing_period"] || columns["reservation_unused_recurring_fee"] {
		parts := []string{}
		if columns["reservation_unused_amortized_upfront_fee_for_billing_period"] {
			parts = append(parts, "COALESCE(reservation_unused_amortized_upfront_fee_for_billing_period, 0)")
		}
		if columns["reservation_unused_recurring_fee"] {
			parts = append(parts, "COALESCE(reservation_unused_recurring_fee, 0)")
		}
		cases = append(cases, "WHEN line_item_line_item_type = 'RIFee' THEN "+strings.Join(parts, " + "))
	}
	if columns["savings_plan_savings_plan_effective_cost"] {
		cases = append(cases, "WHEN line_item_line_item_type = 'SavingsPlanCoveredUsage' THEN COALESCE(savings_plan_savings_plan_effective_cost, 0)")
	}
	if columns["savings_plan_total_commitment_to_date"] && columns["savings_plan_used_commitment"] {
		cases = append(cases, "WHEN line_item_line_item_type = 'SavingsPlanRecurringFee' THEN COALESCE(savings_plan_total_commitment_to_date, 0) - COALESCE(savings_plan_used_commitment, 0)")
	}
	effective := ordinary
	if len(cases) > 0 {
		effective = "CASE " + strings.Join(cases, " ") + " ELSE " + ordinary + " END"
	}
	return fmt.Sprintf("SUM(line_item_unblended_cost) AS billed_cost, SUM(%s) AS effective_cost", effective)
}

var requiredCURColumns = []string{"line_item_usage_start_date", "line_item_usage_end_date", "line_item_resource_id", "line_item_product_code", "line_item_usage_account_id", "bill_payer_account_id", "line_item_currency_code", "line_item_unblended_cost", "line_item_usage_amount", "pricing_unit", "line_item_line_item_type"}

func validateCURColumns(columns map[string]bool) error {
	var missing []string
	for _, column := range requiredCURColumns {
		if !columns[column] {
			missing = append(missing, column)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("CUR table is missing required columns: %s", strings.Join(missing, ", "))
	}
	return nil
}

func buildCostQuery(database, table string, columns map[string]bool, boundary time.Time) (string, error) {
	if err := validateCURColumns(columns); err != nil {
		return "", err
	}
	db, err := quoteCURIdentifier(database)
	if err != nil {
		return "", err
	}
	tbl, err := quoteCURIdentifier(table)
	if err != nil {
		return "", err
	}
	optional := func(name string) string {
		if columns[name] {
			return fmt.Sprintf("COALESCE(CAST(%s AS varchar), '')", name)
		}
		return "''"
	}
	return fmt.Sprintf(`SELECT
 CAST(line_item_usage_start_date AS varchar), CAST(line_item_usage_end_date AS varchar),
 COALESCE(line_item_resource_id, ''), COALESCE(line_item_product_code, ''),
 %s, COALESCE(line_item_usage_account_id, ''), COALESCE(bill_payer_account_id, ''),
 COALESCE(line_item_currency_code, ''), %s,
 CAST(SUM(line_item_usage_amount) AS varchar), MAX(COALESCE(pricing_unit, '')),
 %s, %s, %s, %s
 FROM %s.%s
 WHERE line_item_usage_start_date >= TIMESTAMP '%s'
 GROUP BY line_item_usage_start_date, line_item_usage_end_date, line_item_resource_id,
 line_item_product_code, %s, line_item_usage_account_id, bill_payer_account_id,
 line_item_currency_code, %s, %s, %s, %s`,
		optional("product_region"), curCostExpression(columns), optional("line_item_line_item_type"),
		optional("product_sku"), optional("line_item_usage_type"), optional("line_item_operation"), db, tbl,
		boundary.Format("2006-01-02 15:04:05"), optional("product_region"), optional("line_item_line_item_type"),
		optional("product_sku"), optional("line_item_usage_type"), optional("line_item_operation")), nil
}

func getAWSAthenaConfig(awsConfig v1.AWS, accessKey, secretKey, sessionToken string) (*athena.Config, error) {
	conf := athena.NewNoOpsConfig()
	if err := conf.SetRegion(awsConfig.CostReporting.Region); err != nil {
		return nil, err
	}
	if err := conf.SetOutputBucket(awsConfig.CostReporting.S3BucketPath); err != nil {
		return nil, err
	}
	if accessKey != "" && secretKey != "" {
		if err := conf.SetAccessID(accessKey); err != nil {
			return nil, err
		}
		if err := conf.SetSecretAccessKey(secretKey); err != nil {
			return nil, err
		}
	}
	if sessionToken != "" {
		conf.SetSessionToken(sessionToken)
	}
	return conf, nil
}

type curLineItem struct {
	UsageStartDate, UsageEndDate, ResourceID, ProductCode, Region         string
	UsageAccountID, PayerAccountID, Currency, BilledCost, EffectiveCost   string
	PricingQuantity, PricingUnit, LineItemType, SKU, UsageType, Operation string
}

func chargeKind(lineType string) (string, string) {
	switch strings.ToLower(lineType) {
	case "tax":
		return "Tax", ""
	case "credit", "refund":
		return "Credit", "Correction"
	case "fee", "rifee", "savingsplanupfrontfee", "savingsplanrecurringfee":
		return "Purchase", ""
	case "discountedusage", "savingsplancoveredusage", "usage":
		return "Usage", ""
	default:
		return "Adjustment", ""
	}
}

func (r curLineItem) stableResourceID() string {
	if strings.TrimSpace(r.ResourceID) != "" {
		return r.ResourceID
	}
	return unallocatedResourceID(r)
}

func parseDecimal(name, value string) (decimal.Decimal, error) {
	v, err := decimal.NewFromString(strings.TrimSpace(value))
	if err != nil {
		return decimal.Zero, fmt.Errorf("invalid %s %q: %w", name, value, err)
	}
	return v, nil
}

func (r curLineItem) toExternalCost() (v1.ExternalCost, error) {
	var cost v1.ExternalCost
	start, err := parseCURTime(r.UsageStartDate)
	if err != nil {
		return cost, err
	}
	end, err := parseCURTime(r.UsageEndDate)
	if err != nil {
		return cost, err
	}
	billed, err := parseDecimal("billed cost", r.BilledCost)
	if err != nil {
		return cost, err
	}
	effective, err := parseDecimal("effective cost", r.EffectiveCost)
	if err != nil {
		return cost, err
	}
	quantity, err := parseDecimal("pricing quantity", r.PricingQuantity)
	if err != nil {
		return cost, err
	}
	category, class := chargeKind(r.LineItemType)
	resourceID := r.stableResourceID()
	cost = v1.ExternalCost{ResourceID: resourceID, ScraperID: "all", ChargePeriodStart: start,
		ChargePeriodEnd: end, BilledCost: &billed, EffectiveCost: &effective, BillingCurrency: r.Currency,
		ChargeCategory: category, ChargeClass: class, ServiceName: r.ProductCode, RegionID: r.Region,
		SubAccountID: r.UsageAccountID, BillingAccountID: r.PayerAccountID,
		SkuID: r.SKU, PricingQuantity: &quantity, PricingUnit: r.PricingUnit,
		Focus: types.JSONMap{"UsageType": r.UsageType, "Operation": r.Operation, "LineItemType": r.LineItemType}}
	return cost, nil
}

func parseCURTime(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05.000", "2006-01-02 15:04:05", "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp %q", value)
}

func discoverCURColumns(db *sql.DB, database, table string) (map[string]bool, error) {
	rows, err := db.Query(`SELECT column_name FROM information_schema.columns WHERE table_schema = ? AND table_name = ?`, database, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var c sql.NullString
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		if c.Valid {
			columns[c.String] = true
		}
	}
	return columns, rows.Err()
}

func fetchCosts(config v1.AWS, accessKey, secretKey, sessionToken, sourceKey string) ([]v1.ExternalCost, error) {
	athenaConf, err := getAWSAthenaConfig(config, accessKey, secretKey, sessionToken)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(athena.DriverName, athenaConf.Stringify())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	columns, err := discoverCURColumns(db, config.CostReporting.Database, config.CostReporting.Table)
	if err != nil {
		return nil, err
	}
	query, err := buildCostQuery(config.CostReporting.Database, config.CostReporting.Table, columns, costLookbackBoundary(time.Now(), config.CostReporting.LookbackDays))
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var costs []v1.ExternalCost
	for rows.Next() {
		var r curLineItem
		if err := rows.Scan(&r.UsageStartDate, &r.UsageEndDate, &r.ResourceID, &r.ProductCode, &r.Region,
			&r.UsageAccountID, &r.PayerAccountID, &r.Currency, &r.BilledCost, &r.EffectiveCost,
			&r.PricingQuantity, &r.PricingUnit, &r.LineItemType, &r.SKU, &r.UsageType, &r.Operation); err != nil {
			return nil, err
		}
		cost, err := r.toExternalCost()
		if err != nil {
			return nil, err
		}
		cost.SourceKey = sourceKey
		costs = append(costs, cost)
	}
	return costs, rows.Err()
}

type CostScraper struct{}

func costReportingEmpty(c v1.CostReporting) bool {
	return c.S3BucketPath == "" && c.Database == "" && c.Table == "" && c.Region == "" && c.LookbackDays == 0
}
func validateCostReporting(c v1.CostReporting) error {
	if costReportingEmpty(c) {
		return nil
	}
	var missing []string
	if c.S3BucketPath == "" {
		missing = append(missing, "s3BucketPath")
	}
	if c.Database == "" {
		missing = append(missing, "database")
	}
	if c.Table == "" {
		missing = append(missing, "table")
	}
	if c.Region == "" {
		missing = append(missing, "region")
	}
	if len(missing) > 0 {
		return fmt.Errorf("incomplete AWS costReporting: missing %s", strings.Join(missing, ", "))
	}
	return nil
}
func costSourceKey(config v1.AWS, database, table string) string {
	parts := make([]string, 0, 4)
	if config.AWSConnection.ConnectionName != "" {
		parts = append(parts, "connection="+config.AWSConnection.ConnectionName)
	}
	if config.AWSConnection.AssumeRole != "" {
		parts = append(parts, "role="+config.AWSConnection.AssumeRole)
	}
	if config.AWSConnection.Endpoint != "" {
		parts = append(parts, "endpoint="+config.AWSConnection.Endpoint)
	}
	if len(parts) == 0 {
		// Never include access keys in identity: they are secret and can rotate.
		parts = append(parts, "credentials=ambient")
	}
	// Athena database/table namespaces are regional, so region always participates.
	parts = append(parts, "region="+config.CostReporting.Region)
	return fmt.Sprintf("aws-cur:%s:%s.%s", strings.Join(parts, ","), database, table)
}

func unallocatedResourceID(r curLineItem) string {
	// Keep account/service/type dimensions in the identity so unrelated shared charges
	// never collapse. The aws:unallocated prefix is deliberately not a real config alias.
	return fmt.Sprintf("aws:unallocated:%s:%s:%s:%s", r.PayerAccountID, r.UsageAccountID, r.ProductCode, strings.ToLower(r.LineItemType))
}
func (CostScraper) CanScrape(config v1.ScraperSpec) bool {
	for _, c := range config.AWS {
		if !costReportingEmpty(c.CostReporting) {
			return true
		}
	}
	return false
}
func (CostScraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults
	for _, config := range ctx.ScrapeConfig().Spec.AWS {
		if costReportingEmpty(config.CostReporting) {
			continue
		}
		if err := validateCostReporting(config.CostReporting); err != nil {
			return results.Errorf(err, "invalid AWS cost reporting")
		}
		if _, err := quoteCURIdentifier(config.CostReporting.Database); err != nil {
			return results.Errorf(err, "invalid AWS cost reporting")
		}
		if _, err := quoteCURIdentifier(config.CostReporting.Table); err != nil {
			return results.Errorf(err, "invalid AWS cost reporting")
		}
		awsConn := config.AWSConnection.ToDutyAWSConnection(config.CostReporting.Region)
		if err := awsConn.Populate(ctx); err != nil {
			return results.Errorf(err, "hydrate AWS connection")
		}
		sdkConfig, err := awsConn.Client(ctx.Context)
		if err != nil {
			return results.Errorf(err, "create AWS client")
		}
		credentials, err := sdkConfig.Credentials.Retrieve(ctx.Context)
		if err != nil {
			return results.Errorf(err, "retrieve AWS credentials")
		}
		sourceKey := costSourceKey(config, config.CostReporting.Database, config.CostReporting.Table)
		costs, err := fetchCosts(config, credentials.AccessKeyID, credentials.SecretAccessKey, credentials.SessionToken, sourceKey)
		if err != nil {
			return results.Errorf(err, "failed to fetch costs")
		}
		for i := range costs {
			if strings.HasPrefix(costs[i].ResourceID, "aws:unallocated:") {
				continue
			}
			// AWS config aliases are account-scoped through tags. Preserve that scope so a
			// bare CUR resource ID cannot attach to an identically named resource in another
			// account; ambiguity remains a hard error in the common save path.
			if costs[i].ConfigExternalID.Labels == nil {
				costs[i].ConfigExternalID.Labels = map[string]string{}
			}
			if costs[i].SubAccountID != "" {
				costs[i].ConfigExternalID.Labels["account"] = costs[i].SubAccountID
			}
		}
		if len(costs) > 0 {
			result := v1.NewScrapeResult(config.BaseScraper)
			result.ExternalCosts = costs
			results = append(results, *result)
		}
	}
	return results
}
