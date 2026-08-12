// Snaps scraped FOCUS charge periods onto clock-aligned buckets, merges them by
// dimension fingerprint, and upserts the result into config_costs.
package db

import (
	"fmt"
	"sort"
	"time"

	"github.com/flanksource/duty"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

// bucketFor snaps a half-open charge period onto the clock-aligned bucket that holds it.
//
//	duration ≤ 24h        → the day containing the start
//	duration ≤ 7d         → the ISO week containing the start (Monday-anchored, as date_trunc)
//	duration > 7d         → the calendar month containing the start
//
// Periods are snapped, never split: a monthly recurring charge stays one row. The
// returned bounds are half-open [start, end) and always UTC.
func bucketFor(start, end time.Time) (time.Time, time.Time, string) {
	start = start.UTC()
	duration := end.UTC().Sub(start)

	switch {
	case duration <= 24*time.Hour:
		day := truncateDay(start)
		return day, day.AddDate(0, 0, 1), dutyModels.ConfigCostGrainDay
	case duration <= 7*24*time.Hour:
		week := truncateWeek(start)
		return week, week.AddDate(0, 0, 7), dutyModels.ConfigCostGrainWeek
	default:
		month := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
		return month, month.AddDate(0, 1, 0), dutyModels.ConfigCostGrainMonth
	}
}

func truncateDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// truncateWeek anchors on Monday to match Postgres date_trunc('week', ...).
func truncateWeek(t time.Time) time.Time {
	day := truncateDay(t)
	offset := (int(day.Weekday()) + 6) % 7
	return day.AddDate(0, 0, -offset)
}

// bucketCosts merges scraped costs into the rows that will be written to config_costs.
//
// Rows sharing a target, a bucket, and a dimension fingerprint are one line item, and
// their summable metrics add up. Currencies, SKUs, charge categories and corrections all
// live in the fingerprint, so they never merge into each other.
//
// The unit prices FOCUS declares row-scoped (ListUnitPrice, ContractedUnitPrice,
// PricingCurrency*UnitPrice) are never summed; they ride along in the focus payload of
// the first contributing row.
//
// Config references must already be resolved: ConfigID set, or nil for spend that could
// not be attached to a config item.
func bucketCosts(costs []v1.ExternalCost, scraperID *uuid.UUID) []dutyModels.ConfigCost {
	type key struct {
		configID    string
		externalID  string
		periodStart time.Time
		periodEnd   time.Time
		fingerprint string
	}

	merged := make(map[key]*dutyModels.ConfigCost, len(costs))
	order := make([]key, 0, len(costs))

	for _, c := range costs {
		periodStart, periodEnd, grain := bucketFor(c.ChargePeriodStart, c.ChargePeriodEnd)

		var configID string
		if c.ConfigID != nil {
			configID = c.ConfigID.String()
		}
		externalID := c.ResourceID
		if externalID == "" {
			externalID = c.ConfigExternalID.ExternalID
		}

		k := key{
			configID:    configID,
			externalID:  externalID,
			periodStart: periodStart,
			periodEnd:   periodEnd,
			fingerprint: c.Fingerprint(),
		}

		if existing, ok := merged[k]; ok {
			existing.BilledCost = existing.BilledCost.Add(c.BilledCost)
			existing.EffectiveCost = existing.EffectiveCost.Add(c.EffectiveCost)
			existing.ListCost = addOptional(existing.ListCost, c.ListCost)
			existing.ContractedCost = addOptional(existing.ContractedCost, c.ContractedCost)
			existing.PricingQuantity = addOptional(existing.PricingQuantity, c.PricingQuantity)
			continue
		}

		row := &dutyModels.ConfigCost{
			ConfigID:         c.ConfigID,
			ScraperID:        scraperID,
			ExternalID:       nilIfEmpty(externalID),
			PeriodStart:      periodStart,
			PeriodEnd:        periodEnd,
			Grain:            grain,
			ChargeCategory:   chargeCategoryOrDefault(c.ChargeCategory),
			ChargeClass:      nilIfEmpty(c.ChargeClass),
			ServiceName:      nilIfEmpty(c.ServiceName),
			ServiceCategory:  nilIfEmpty(c.ServiceCategory),
			SkuID:            nilIfEmpty(c.SkuID),
			RegionID:         nilIfEmpty(c.RegionID),
			BillingAccountID: nilIfEmpty(c.BillingAccountID),
			SubAccountID:     nilIfEmpty(c.SubAccountID),
			BillingCurrency:  c.BillingCurrency,
			BilledCost:       c.BilledCost,
			EffectiveCost:    c.EffectiveCost,
			ListCost:         copyOptional(c.ListCost),
			ContractedCost:   copyOptional(c.ContractedCost),
			PricingQuantity:  copyOptional(c.PricingQuantity),
			PricingUnit:      nilIfEmpty(c.PricingUnit),
			Focus:            c.Focus,
			Fingerprint:      k.fingerprint,
		}
		merged[k] = row
		order = append(order, k)
	}

	// Stable output so a re-scrape of identical input produces an identical batch.
	sort.Slice(order, func(i, j int) bool {
		if !order[i].periodStart.Equal(order[j].periodStart) {
			return order[i].periodStart.Before(order[j].periodStart)
		}
		if order[i].fingerprint != order[j].fingerprint {
			return order[i].fingerprint < order[j].fingerprint
		}
		return order[i].configID < order[j].configID
	})

	out := make([]dutyModels.ConfigCost, 0, len(order))
	for _, k := range order {
		out = append(out, *merged[k])
	}
	return out
}

func chargeCategoryOrDefault(category string) string {
	if category == "" {
		return v1.ChargeCategoryUsage
	}
	return category
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func copyOptional(d *decimal.Decimal) *decimal.Decimal {
	if d == nil {
		return nil
	}
	v := *d
	return &v
}

func addOptional(a, b *decimal.Decimal) *decimal.Decimal {
	if b == nil {
		return a
	}
	if a == nil {
		return copyOptional(b)
	}
	sum := a.Add(*b)
	return &sum
}

// upsertConfigCosts writes the bucketed rows, replacing the metrics of any bucket that
// already exists.
//
// Replace rather than accumulate: FOCUS providers deliver open billing periods with
// overwrite semantics, so re-scraping today restates today's total. Adding would
// double-count on every run. Corrections arrive as their own rows with a distinct
// fingerprint and therefore still add correctly.
//
// There is no stale-row soft delete. A period disappearing from the source is not a
// deletion — a closed billing period is immutable. Removal is retention-driven only.
func upsertConfigCosts(ctx api.ScrapeContext, costs []dutyModels.ConfigCost, scraperID *uuid.UUID) (int, error) {
	if len(costs) == 0 {
		return 0, nil
	}

	suffix := "no_scraper"
	if scraperID != nil {
		suffix = sanitizeForTempTable(scraperID.String())
	}
	tempTable := fmt.Sprintf("_scrape_config_costs_%s", suffix)

	tx := ctx.DB().Begin()
	if tx.Error != nil {
		return 0, fmt.Errorf("failed to begin transaction: %w", tx.Error)
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	if err := duty.ApplySessionProperties(ctx.DutyContext(), tx); err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to apply session properties: %w", err)
	}

	// The temp table inherits config_costs' merge index (INCLUDING ALL), so duplicates
	// within this batch collapse here before the real insert sees them.
	if err := createTempAndInsert(tx, tempTable, "config_costs", costs); err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to setup temp config costs: %w", err)
	}

	insert := fmt.Sprintf(`
		INSERT INTO config_costs SELECT * FROM %s
		ON CONFLICT (config_id, period_start, period_end, fingerprint)
		DO UPDATE SET billed_cost = excluded.billed_cost,
		              effective_cost = excluded.effective_cost,
		              list_cost = excluded.list_cost,
		              contracted_cost = excluded.contracted_cost,
		              pricing_quantity = excluded.pricing_quantity,
		              focus = excluded.focus,
		              scraper_id = excluded.scraper_id,
		              updated_at = now()`, tempTable)

	result := tx.Exec(insert)
	if result.Error != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to upsert config costs: %w", result.Error)
	}

	if err := tx.Commit().Error; err != nil {
		return 0, fmt.Errorf("failed to commit config costs: %w", err)
	}

	return int(result.RowsAffected), nil
}

// saveExternalCosts resolves each scraped cost to a config item and merges it into
// config_costs.
//
// Runs after the config item bulk insert so items created by this same scrape are
// already resolvable. A cost whose resource cannot be resolved is not dropped: it is
// stored with a null config_id and its resource id, visible through
// config_costs_unmatched, and attached by the compaction job once the item appears.
func saveExternalCosts(ctx api.ScrapeContext, costs []v1.ExternalCost, scraperID *uuid.UUID, summary *v1.ScrapeSummary) {
	if len(costs) == 0 {
		return
	}
	summary.ExternalCosts.Scraped = len(costs)

	resolved := make([]v1.ExternalCost, 0, len(costs))
	for _, cost := range costs {
		if cost.ConfigID == nil {
			lookup := cost.ConfigExternalID
			if lookup.ExternalID == "" {
				lookup.ExternalID = cost.ResourceID
			}
			if lookup.ScraperID == "" && cost.ScraperID != "" {
				lookup.ScraperID = cost.ScraperID
			}

			if lookup.ExternalID != "" {
				configID, err := ctx.TempCache().FindExternalID(ctx, lookup)
				if err != nil {
					summary.AddWarning("ExternalCosts", fmt.Sprintf("failed to find config (%s) for cost: %v", lookup.Pretty().ANSI(), err))
				} else if configID != "" {
					id := uuid.MustParse(configID)
					cost.ConfigID = &id
				}
			}
		}

		// config_costs requires a config item or a resource id to hang the spend on.
		if cost.ConfigID == nil && cost.ResourceID == "" && cost.ConfigExternalID.ExternalID == "" {
			summary.ExternalCosts.Skipped++
			continue
		}

		resolved = append(resolved, cost)
	}

	saved, err := upsertConfigCosts(ctx, bucketCosts(resolved, scraperID), scraperID)
	if err != nil {
		summary.AddWarning("ExternalCosts", fmt.Sprintf("failed to upsert config costs: %v", err))
		return
	}
	summary.ExternalCosts.Saved += saved
}
