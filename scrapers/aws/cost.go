// Reads the AWS Cost and Usage Report from Athena and emits it as FOCUS-shaped
// ExternalCosts, one row per charge period per resource.
package aws

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/shopspring/decimal"
	athena "github.com/uber/athenadriver/go"

	"github.com/flanksource/config-db/api"
)

// costLookbackDays bounds the CUR scan. History accumulates in config_costs, so each run
// only needs to (re)state the recent, still-open billing days — AWS restates them for a
// few days after the fact, and the upsert replaces rather than accumulates.
const costLookbackDays = 7

// costQueryTemplate reads the CUR at its native charge-period grain and lets the save
// path bucket it. The columns map onto FOCUS:
//
//	line_item_usage_start_date  -> ChargePeriodStart
//	line_item_usage_end_date    -> ChargePeriodEnd
//	line_item_resource_id       -> ResourceId
//	line_item_product_code      -> ServiceName
//	line_item_unblended_cost    -> BilledCost
//	line_item_net_unblended_cost-> EffectiveCost
const costQueryTemplate = `
    SELECT
        line_item_usage_start_date,
        line_item_usage_end_date,
        line_item_resource_id,
        line_item_product_code,
        product_region,
        line_item_usage_account_id,
        bill_payer_account_id,
        line_item_currency_code,
        SUM(line_item_unblended_cost) AS billed_cost,
        SUM(line_item_net_unblended_cost) AS effective_cost,
        SUM(line_item_usage_amount) AS pricing_quantity,
        MAX(pricing_unit) AS pricing_unit
    FROM $table
    WHERE line_item_usage_start_date >= date_add('day', -$lookback, now())
      AND line_item_resource_id <> ''
    GROUP BY
        line_item_usage_start_date,
        line_item_usage_end_date,
        line_item_resource_id,
        line_item_product_code,
        product_region,
        line_item_usage_account_id,
        bill_payer_account_id,
        line_item_currency_code
`

func getAWSAthenaConfig(awsConfig v1.AWS) (*athena.Config, error) {
	conf := athena.NewNoOpsConfig()

	if err := conf.SetRegion(awsConfig.CostReporting.Region); err != nil {
		return nil, err
	}

	if err := conf.SetOutputBucket(awsConfig.CostReporting.S3BucketPath); err != nil {
		return nil, err
	}

	if len(awsConfig.AWSConnection.AccessKey.ValueStatic) > 0 && len(awsConfig.AWSConnection.SecretKey.ValueStatic) > 0 {
		if err := conf.SetAccessID(awsConfig.AWSConnection.AccessKey.ValueStatic); err != nil {
			return nil, err
		}

		if err := conf.SetSecretAccessKey(awsConfig.AWSConnection.SecretKey.ValueStatic); err != nil {
			return nil, err
		}
	}

	return conf, nil
}

// curLineItem is one grouped CUR row. Athena returns everything as text.
type curLineItem struct {
	UsageStartDate  string
	UsageEndDate    string
	ResourceID      string
	ProductCode     string
	Region          string
	UsageAccountID  string
	PayerAccountID  string
	Currency        string
	BilledCost      string
	EffectiveCost   string
	PricingQuantity string
	PricingUnit     string
}

// toExternalCost converts a CUR row into the FOCUS shape. Parse failures are returned,
// not swallowed: a silently-zeroed cost is worse than a visibly failed scrape.
func (r curLineItem) toExternalCost() (v1.ExternalCost, error) {
	var cost v1.ExternalCost

	start, err := parseCURTime(r.UsageStartDate)
	if err != nil {
		return cost, fmt.Errorf("resource %s: invalid usage start date: %w", r.ResourceID, err)
	}
	end, err := parseCURTime(r.UsageEndDate)
	if err != nil {
		return cost, fmt.Errorf("resource %s: invalid usage end date: %w", r.ResourceID, err)
	}

	billed, err := decimal.NewFromString(r.BilledCost)
	if err != nil {
		return cost, fmt.Errorf("resource %s: invalid unblended cost %q: %w", r.ResourceID, r.BilledCost, err)
	}
	effective, err := decimal.NewFromString(r.EffectiveCost)
	if err != nil {
		// net_unblended_cost is empty on accounts without discounts.
		effective = billed
	}

	cost = v1.ExternalCost{
		ResourceID: r.ResourceID,
		// CUR resource ids are ARNs or bare resource ids owned by whichever AWS scraper
		// collected them, which is not necessarily this one.
		ScraperID:         "all",
		ChargePeriodStart: start,
		ChargePeriodEnd:   end,
		BilledCost:        billed,
		EffectiveCost:     effective,
		BillingCurrency:   r.Currency,
		ChargeCategory:    v1.ChargeCategoryUsage,
		ServiceName:       r.ProductCode,
		RegionID:          r.Region,
		SubAccountID:      r.UsageAccountID,
		BillingAccountID:  r.PayerAccountID,
		PricingUnit:       r.PricingUnit,
	}

	if quantity, err := decimal.NewFromString(r.PricingQuantity); err == nil {
		cost.PricingQuantity = &quantity
	}

	return cost, nil
}

// parseCURTime reads the timestamp formats Athena returns for CUR date columns.
func parseCURTime(value string) (time.Time, error) {
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02 15:04:05.000",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
	} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp %q", value)
}

func fetchCosts(config v1.AWS) ([]v1.ExternalCost, error) {
	athenaConf, err := getAWSAthenaConfig(config)
	if err != nil {
		return nil, err
	}

	athenaDB, err := sql.Open(athena.DriverName, athenaConf.Stringify())
	if err != nil {
		return nil, err
	}
	defer athenaDB.Close()

	table := fmt.Sprintf("%s.%s", config.CostReporting.Database, config.CostReporting.Table)
	query := strings.ReplaceAll(costQueryTemplate, "$table", table)
	query = strings.ReplaceAll(query, "$lookback", fmt.Sprintf("%d", costLookbackDays))

	rows, err := athenaDB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var costs []v1.ExternalCost
	for rows.Next() {
		var r curLineItem
		if err := rows.Scan(
			&r.UsageStartDate, &r.UsageEndDate, &r.ResourceID, &r.ProductCode,
			&r.Region, &r.UsageAccountID, &r.PayerAccountID, &r.Currency,
			&r.BilledCost, &r.EffectiveCost, &r.PricingQuantity, &r.PricingUnit,
		); err != nil {
			return nil, fmt.Errorf("failed to scan athena row: %w", err)
		}

		cost, err := r.toExternalCost()
		if err != nil {
			return nil, err
		}
		costs = append(costs, cost)
	}

	return costs, rows.Err()
}

type CostScraper struct{}

// CanScrape disables legacy rolling-cost writes until they use the config_costs tables.
func (CostScraper) CanScrape(v1.ScraperSpec) bool {
	return false
}

func (awsCost CostScraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults

	for _, awsConfig := range ctx.ScrapeConfig().Spec.AWS {
		costs, err := fetchCosts(awsConfig)
		if err != nil {
			return results.Errorf(err, "failed to fetch costs")
		}
		if len(costs) == 0 {
			continue
		}

		// Costs are scrape-level, like external users and access: one entity-only result
		// carries them all, and SaveResults attaches each to its config item.
		result := v1.NewScrapeResult(awsConfig.BaseScraper)
		result.ExternalCosts = costs
		results = append(results, *result)
	}

	return results
}
