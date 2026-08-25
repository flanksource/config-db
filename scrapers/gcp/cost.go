// Reads the Cloud Billing export from BigQuery and emits FOCUS-shaped ExternalCosts.
package gcp

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"cloud.google.com/go/bigquery"
	"github.com/flanksource/duty/types"
	"github.com/shopspring/decimal"
	"google.golang.org/api/iterator"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

const defaultCostLookbackDays = 45

// BigQuery dataset and table names allow letters, digits and underscores; project ids also
// allow hyphens and dots. Neither can be a query parameter, so they are validated rather
// than quoted into the statement.
var (
	bqDatasetOrTable = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	bqProject        = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

func validateCostIdentifiers(project, dataset, table string) error {
	for _, f := range []struct {
		name, value string
		pattern     *regexp.Regexp
	}{
		{"project", project, bqProject},
		{"dataset", dataset, bqDatasetOrTable},
		{"table", table, bqDatasetOrTable},
	} {
		if !f.pattern.MatchString(f.value) {
			return fmt.Errorf("invalid BigQuery %s %q", f.name, f.value)
		}
	}
	return nil
}

func costLookbackBoundary(now time.Time, days int) time.Time {
	if days <= 0 {
		days = defaultCostLookbackDays
	}
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -days)
}

// buildCostQuery reads the detailed export. Money is cast to string so it reaches
// shopspring/decimal without a float64 round trip, the same way the CUR reader does it.
//
// Credits are a repeated record rather than columns, so the effective cost is the charge
// plus its credits, which are negative. The gross charge stays in billed_cost.
func buildCostQuery(project, dataset, table string) (string, error) {
	if err := validateCostIdentifiers(project, dataset, table); err != nil {
		return "", err
	}
	const creditsForRow = `IFNULL((SELECT SUM(c.amount) FROM UNNEST(credits) c), 0)`
	return fmt.Sprintf(`SELECT
  IFNULL(resource.global_name, '') AS resource_global_name,
  IFNULL(resource.name, '') AS resource_name,
  IFNULL(service.description, '') AS service_description,
  IFNULL(sku.id, '') AS sku_id,
  IFNULL(project.id, '') AS project_id,
  IFNULL(billing_account_id, '') AS billing_account_id,
  IFNULL(location.region, '') AS region,
  IFNULL(currency, '') AS currency,
  IFNULL(cost_type, '') AS cost_type,
  IFNULL(usage.pricing_unit, '') AS pricing_unit,
  usage_start_time,
  usage_end_time,
  CAST(SUM(cost) AS STRING) AS billed_cost,
  CAST(SUM(cost + %[1]s) AS STRING) AS effective_cost,
  CAST(SUM(%[1]s) AS STRING) AS credit_amount,
  CAST(SUM(IFNULL(usage.amount_in_pricing_units, 0)) AS STRING) AS pricing_quantity
FROM `+"`%[2]s.%[3]s.%[4]s`"+`
WHERE usage_start_time >= @since AND usage_end_time > usage_start_time
GROUP BY 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12`, creditsForRow, project, dataset, table), nil
}

type costRow struct {
	ResourceGlobalName string    `bigquery:"resource_global_name"`
	ResourceName       string    `bigquery:"resource_name"`
	ServiceDescription string    `bigquery:"service_description"`
	SkuID              string    `bigquery:"sku_id"`
	ProjectID          string    `bigquery:"project_id"`
	BillingAccountID   string    `bigquery:"billing_account_id"`
	Region             string    `bigquery:"region"`
	Currency           string    `bigquery:"currency"`
	CostType           string    `bigquery:"cost_type"`
	PricingUnit        string    `bigquery:"pricing_unit"`
	UsageStartTime     time.Time `bigquery:"usage_start_time"`
	UsageEndTime       time.Time `bigquery:"usage_end_time"`
	BilledCost         string    `bigquery:"billed_cost"`
	EffectiveCost      string    `bigquery:"effective_cost"`
	CreditAmount       string    `bigquery:"credit_amount"`
	PricingQuantity    string    `bigquery:"pricing_quantity"`
}

// chargeKind maps the export's cost_type onto the FOCUS charge category and class.
func chargeKind(costType string) (string, string) {
	switch strings.ToLower(costType) {
	case "tax":
		return "Tax", ""
	case "adjustment":
		return "Adjustment", "Correction"
	case "rounding_error":
		return "Adjustment", ""
	default:
		return "Usage", ""
	}
}

// stableResourceID prefers the globally unique name, which is the same full resource name
// the asset scrape stores as an alias. Not every service populates it.
func (r costRow) stableResourceID() string {
	if strings.TrimSpace(r.ResourceGlobalName) != "" {
		return r.ResourceGlobalName
	}
	if strings.TrimSpace(r.ResourceName) != "" {
		return r.ResourceName
	}
	return unallocatedResourceID(r)
}

// unallocatedResourceID names spend with no resource of its own — tax, support, and
// project-level charges. The prefix is deliberately not a real GCP resource name.
func unallocatedResourceID(r costRow) string {
	return fmt.Sprintf("gcp:unallocated:%s:%s:%s:%s",
		r.BillingAccountID, r.ProjectID, r.ServiceDescription, strings.ToLower(r.CostType))
}

func parseDecimal(name, value string) (decimal.Decimal, error) {
	v, err := decimal.NewFromString(strings.TrimSpace(value))
	if err != nil {
		return decimal.Zero, fmt.Errorf("invalid %s %q: %w", name, value, err)
	}
	return v, nil
}

func (r costRow) toExternalCost() (v1.ExternalCost, error) {
	var cost v1.ExternalCost
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

	category, class := chargeKind(r.CostType)
	focus := types.JSONMap{"CostType": r.CostType}
	if r.CreditAmount != "" {
		focus["CreditAmount"] = r.CreditAmount
	}
	if r.ResourceName != "" {
		focus["ResourceName"] = r.ResourceName
	}

	cost = v1.ExternalCost{
		ResourceID: r.stableResourceID(),
		ScraperID:  "all",
		// The project is this scraper's root: spend that names no resource, or names one
		// that has not been scraped, is booked there rather than dropped.
		RootConfigID: v1.ExternalID{
			ConfigType: v1.GCPProject,
			ExternalID: v1.ProjectPrefix + r.ProjectID,
			ScraperID:  "all",
		},
		ChargePeriodStart: r.UsageStartTime.UTC(),
		ChargePeriodEnd:   r.UsageEndTime.UTC(),
		BilledCost:        &billed,
		EffectiveCost:     &effective,
		BillingCurrency:   r.Currency,
		ChargeCategory:    category,
		ChargeClass:       class,
		ServiceName:       r.ServiceDescription,
		RegionID:          r.Region,
		SkuID:             r.SkuID,
		BillingAccountID:  r.BillingAccountID,
		SubAccountID:      r.ProjectID,
		PricingQuantity:   &quantity,
		PricingUnit:       r.PricingUnit,
		Focus:             focus,
	}
	return cost, nil
}

// costSourceKey identifies the producer namespace. Credentials are never part of it: they
// are secret and they rotate.
func costSourceKey(config v1.GCP, project, dataset, table string) string {
	parts := make([]string, 0, 2)
	if config.ConnectionName != "" {
		parts = append(parts, "connection="+config.ConnectionName)
	}
	if len(parts) == 0 {
		parts = append(parts, "credentials=ambient")
	}
	return fmt.Sprintf("gcp-billing:%s:%s.%s.%s", strings.Join(parts, ","), project, dataset, table)
}

// costProject is where the export dataset lives, which need not be a scraped project.
func costProject(config v1.GCP) string {
	if config.CostReporting.Project != "" {
		return strings.TrimPrefix(config.CostReporting.Project, v1.ProjectPrefix)
	}
	if projects := config.ConfiguredProjects(); len(projects) == 1 {
		return projects[0]
	}
	return ""
}

func fetchCosts(ctx *GCPContext, config v1.GCP, project, sourceKey string, boundary time.Time) ([]v1.ExternalCost, error) {
	client, err := bigquery.NewClient(ctx, project, ctx.ClientOpts...)
	if err != nil {
		return nil, fmt.Errorf("create BigQuery client: %w", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			ctx.Warnf("gcp costs: failed to close BigQuery client: %v", err)
		}
	}()

	query, err := buildCostQuery(project, config.CostReporting.Dataset, config.CostReporting.Table)
	if err != nil {
		return nil, err
	}

	q := client.Query(query)
	q.Parameters = []bigquery.QueryParameter{{Name: "since", Value: boundary}}
	it, err := q.Read(ctx)
	if err != nil {
		return nil, costQueryError(err, config.CostReporting.Table)
	}

	var costs []v1.ExternalCost
	for {
		var row costRow
		if err := it.Next(&row); err == iterator.Done {
			break
		} else if err != nil {
			return nil, costQueryError(err, config.CostReporting.Table)
		}
		cost, err := row.toExternalCost()
		if err != nil {
			return nil, err
		}
		cost.SourceKey = sourceKey
		costs = append(costs, cost)
	}
	return costs, nil
}

// costQueryError explains the failure the standard export produces, which is otherwise a
// bare complaint about an unknown field.
func costQueryError(err error, table string) error {
	if strings.Contains(err.Error(), "resource") {
		return fmt.Errorf("failed to read billing export %s (a missing resource column means this is the standard export; cost reporting needs the detailed export, gcp_billing_export_resource_v1_*): %w", table, err)
	}
	return fmt.Errorf("failed to read billing export %s: %w", table, err)
}

type CostScraper struct{}

func (CostScraper) CanScrape(configs v1.ScraperSpec) bool {
	for _, c := range configs.GCP {
		if !c.CostReporting.IsEmpty() {
			return true
		}
	}
	return false
}

func (CostScraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults
	for _, config := range ctx.ScrapeConfig().Spec.GCP {
		if config.CostReporting.IsEmpty() {
			continue
		}
		if err := config.CostReporting.Validate(); err != nil {
			return results.Errorf(err, "invalid GCP cost reporting")
		}

		project := costProject(config)
		if project == "" {
			return results.Errorf(
				fmt.Errorf("set costReporting.project: it cannot be inferred from a scrape covering %d projects", len(config.ConfiguredProjects())),
				"invalid GCP cost reporting")
		}

		gcpCtx, err := NewGCPContext(ctx, config)
		if err != nil {
			return results.Errorf(err, "failed to create GCP context")
		}

		sourceKey := costSourceKey(config, project, config.CostReporting.Dataset, config.CostReporting.Table)
		boundary := costLookbackBoundary(time.Now(), config.CostReporting.LookbackDays)
		costs, err := fetchCosts(gcpCtx, config, project, sourceKey, boundary)
		if err != nil {
			return results.Errorf(err, "failed to fetch GCP costs")
		}

		for i := range costs {
			if strings.HasPrefix(costs[i].ResourceID, "gcp:unallocated:") {
				continue
			}
			// Assets carry their owning project as a tag, so scoping the lookup by it stops
			// a resource name matching an identically named resource in another project.
			if costs[i].ConfigExternalID.Labels == nil {
				costs[i].ConfigExternalID.Labels = map[string]string{}
			}
			if costs[i].SubAccountID != "" {
				costs[i].ConfigExternalID.Labels["project"] = costs[i].SubAccountID
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
