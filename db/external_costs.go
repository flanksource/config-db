// Snaps scraped FOCUS charge periods onto clock-aligned buckets and persists them.
package db

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/flanksource/duty"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

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
func truncateWeek(t time.Time) time.Time {
	day := truncateDay(t)
	return day.AddDate(0, 0, -(int(day.Weekday())+6)%7)
}

func bucketCosts(costs []v1.ExternalCost, scraperID *uuid.UUID) []dutyModels.ConfigCost {
	type key struct {
		source, configID, externalID, fingerprint string
		periodStart, periodEnd                    time.Time
	}
	merged := make(map[key]*dutyModels.ConfigCost, len(costs))
	order := make([]key, 0, len(costs))
	for _, c := range costs {
		start, end, grain := bucketFor(c.ChargePeriodStart, c.ChargePeriodEnd)
		configID := ""
		if c.ConfigID != nil {
			configID = c.ConfigID.String()
		}
		externalID := v1.NormalizeExternalID(c.ResourceID)
		if externalID == "" {
			externalID = v1.NormalizeExternalID(c.ConfigExternalID.ExternalID)
		}
		k := key{c.SourceKey, configID, externalID, c.Fingerprint(), start, end}
		if existing := merged[k]; existing != nil {
			existing.BilledCost = existing.BilledCost.Add(*c.BilledCost)
			existing.EffectiveCost = existing.EffectiveCost.Add(*c.EffectiveCost)
			existing.ListCost = addOptional(existing.ListCost, c.ListCost)
			existing.ContractedCost = addOptional(existing.ContractedCost, c.ContractedCost)
			existing.PricingQuantity = addOptional(existing.PricingQuantity, c.PricingQuantity)
			continue
		}
		labels := make(map[string]any, len(c.ConfigExternalID.Labels))
		for k, v := range c.ConfigExternalID.Labels {
			labels[k] = v
		}
		row := &dutyModels.ConfigCost{
			ConfigID: c.ConfigID, ScraperID: scraperID, SourceKey: c.SourceKey,
			SourceRecordID: c.SourceRecordID, ExternalID: nilIfEmpty(externalID),
			ExternalConfigType:      nilIfEmpty(c.ConfigExternalID.ConfigType),
			ExternalConfigScraperID: nilIfEmpty(firstNonempty(c.ConfigExternalID.ScraperID, c.ScraperID)),
			ExternalConfigLabels:    labels,
			PeriodStart:             start, PeriodEnd: end, Grain: grain,
			ChargeCategory: chargeCategoryOrDefault(c.ChargeCategory), ChargeClass: nilIfEmpty(c.ChargeClass),
			ServiceName: nilIfEmpty(c.ServiceName), ServiceCategory: nilIfEmpty(c.ServiceCategory),
			SkuID: nilIfEmpty(c.SkuID), RegionID: nilIfEmpty(c.RegionID),
			BillingAccountID: nilIfEmpty(c.BillingAccountID), SubAccountID: nilIfEmpty(c.SubAccountID),
			BillingCurrency: c.BillingCurrency, BilledCost: *c.BilledCost, EffectiveCost: *c.EffectiveCost,
			ListCost: copyOptional(c.ListCost), ContractedCost: copyOptional(c.ContractedCost),
			PricingQuantity: copyOptional(c.PricingQuantity), PricingUnit: nilIfEmpty(c.PricingUnit),
			Focus: c.Focus, Fingerprint: k.fingerprint,
		}
		merged[k] = row
		order = append(order, k)
	}
	sort.Slice(order, func(i, j int) bool {
		if !order[i].periodStart.Equal(order[j].periodStart) {
			return order[i].periodStart.Before(order[j].periodStart)
		}
		if order[i].source != order[j].source {
			return order[i].source < order[j].source
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

func firstNonempty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
func chargeCategoryOrDefault(v string) string {
	if v == "" {
		return v1.ChargeCategoryUsage
	}
	return v
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
	v := a.Add(*b)
	return &v
}

func upsertConfigCosts(ctx api.ScrapeContext, costs []dutyModels.ConfigCost, scraperID *uuid.UUID) (int, error) {
	if len(costs) == 0 {
		return 0, nil
	}
	suffix := "no_scraper"
	if scraperID != nil {
		suffix = sanitizeForTempTable(scraperID.String())
	}
	table := fmt.Sprintf("_scrape_config_costs_%s", suffix)
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
		return 0, err
	}
	// A source-native record may be corrected onto a different target or period. The
	// regular merge key includes both, so serialize cost writes and remove the previous
	// version by its immutable source identity before inserting the restatement.
	if err := tx.Exec(`LOCK TABLE config_costs IN SHARE ROW EXCLUSIVE MODE`).Error; err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to lock config costs: %w", err)
	}
	if err := createTempAndInsert(tx, table, "config_costs", costs); err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to setup temp config costs: %w", err)
	}
	deleteRestated := fmt.Sprintf(`DELETE FROM config_costs existing USING %s incoming
		WHERE incoming.source_record_id IS NOT NULL
		  AND existing.source_key = incoming.source_key
		  AND existing.source_record_id = incoming.source_record_id`, table)
	if err := tx.Exec(deleteRestated).Error; err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to replace restated source records: %w", err)
	}

	query := fmt.Sprintf(`INSERT INTO config_costs SELECT * FROM %s
		ON CONFLICT (source_key, config_id, period_start, period_end, fingerprint)
		DO UPDATE SET external_id=excluded.external_id,
		external_config_type=excluded.external_config_type,
		external_config_scraper_id=excluded.external_config_scraper_id,
		external_config_labels=excluded.external_config_labels,
		charge_category=excluded.charge_category, charge_class=excluded.charge_class,
		service_name=excluded.service_name, service_category=excluded.service_category,
		sku_id=excluded.sku_id, region_id=excluded.region_id,
		billing_account_id=excluded.billing_account_id, sub_account_id=excluded.sub_account_id,
		billing_currency=excluded.billing_currency, pricing_unit=excluded.pricing_unit,
		billed_cost=excluded.billed_cost, effective_cost=excluded.effective_cost,
		list_cost=excluded.list_cost, contracted_cost=excluded.contracted_cost,
		pricing_quantity=excluded.pricing_quantity, focus=excluded.focus,
		source_record_id=excluded.source_record_id, scraper_id=excluded.scraper_id, updated_at=now()`, table)
	result := tx.Exec(query)
	if result.Error != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to upsert config costs: %w", result.Error)
	}
	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return int(result.RowsAffected), nil
}

// findConfigMatches performs a deterministic, bounded lookup and exposes ambiguity.
func findConfigMatches(db *gorm.DB, lookup v1.ExternalID, defaultScraperID *uuid.UUID) ([]uuid.UUID, error) {
	externalID := v1.NormalizeExternalID(lookup.ExternalID)
	if externalID == "" {
		return nil, nil
	}
	q := db.Table("config_items").Select("id").Where("deleted_at IS NULL").Where("? = ANY(external_id)", externalID)
	if lookup.ConfigType != "" {
		q = q.Where("type = ?", lookup.ConfigType)
	}
	scope := lookup.ScraperID
	if scope == "" && defaultScraperID != nil {
		scope = defaultScraperID.String()
	}
	if scope != "" && scope != "all" && !slices.Contains(v1.ScraperLessTypes, lookup.ConfigType) {
		q = q.Where("scraper_id = ?", scope)
	}
	keys := make([]string, 0, len(lookup.Labels))
	for k := range lookup.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		q = q.Where("labels ->> ? = ?", k, lookup.Labels[k])
	}
	var ids []uuid.UUID
	if err := q.Order("id ASC").Limit(3).Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

func resolveCostTarget(ctx api.ScrapeContext, cost *v1.ExternalCost, scraperID *uuid.UUID) error {
	// Explicit UUID has absolute precedence. ResourceID remains provenance only.
	if cost.ConfigExternalID.ConfigID != "" {
		id, err := uuid.Parse(strings.TrimSpace(cost.ConfigExternalID.ConfigID))
		if err != nil {
			return fmt.Errorf("invalid external_config_id.config_id: %w", err)
		}
		if cost.ConfigID != nil && *cost.ConfigID != id {
			return fmt.Errorf("conflicting explicit config UUIDs %s and %s", cost.ConfigID, id)
		}
		cost.ConfigID = &id
	}
	if cost.ConfigID != nil {
		return nil
	}

	// Structured external identity wins over ResourceID. Type/scraper/labels only scope
	// this external ID; they are never a standalone selector.
	lookup := cost.ConfigExternalID
	if lookup.ExternalID == "" {
		lookup.ExternalID = cost.ResourceID
	}
	if lookup.ExternalID == "" {
		return nil
	}
	if lookup.ScraperID == "" {
		lookup.ScraperID = cost.ScraperID
	}
	ids, err := findConfigMatches(ctx.DB(), lookup, scraperID)
	if err != nil {
		return fmt.Errorf("find config %s: %w", lookup.Pretty().ANSI(), err)
	}
	if len(ids) > 1 {
		return fmt.Errorf("ambiguous config reference %s matched %v", lookup.Pretty().ANSI(), ids)
	}
	if len(ids) == 1 {
		id := ids[0]
		cost.ConfigID = &id
	}
	return nil
}

func saveExternalCosts(ctx api.ScrapeContext, costs []v1.ExternalCost, scraperID *uuid.UUID, summary *v1.ScrapeSummary) error {
	if len(costs) == 0 {
		return nil
	}
	summary.ExternalCosts.Scraped = len(costs)
	resolved := make([]v1.ExternalCost, 0, len(costs))
	sourceRecords := make(map[string]int)
	var costErrors []error
	for i := range costs {
		cost := costs[i]
		skip := func(err error) {
			costErrors = append(costErrors, fmt.Errorf("external cost %d: %w", i, err))
			summary.ExternalCosts.Skipped++
		}
		if cost.SourceKey == "" {
			if scraperID == nil {
				skip(fmt.Errorf("has no source_key and scraper UUID is unavailable"))
				continue
			}
			cost.SourceKey = "scraper:" + scraperID.String()
		}
		if err := cost.Validate(); err != nil {
			skip(err)
			continue
		}
		if cost.SourceRecordID != nil {
			key := cost.SourceKey + "\x00" + *cost.SourceRecordID
			if previous, found := sourceRecords[key]; found {
				skip(fmt.Errorf("repeats source record %q from external cost %d within source %q", *cost.SourceRecordID, previous, cost.SourceKey))
				continue
			}
			sourceRecords[key] = i
		}
		if cost.ConfigExternalID.ScraperID == "" {
			cost.ConfigExternalID.ScraperID = cost.ScraperID
			if cost.ConfigExternalID.ScraperID == "" && scraperID != nil {
				cost.ConfigExternalID.ScraperID = scraperID.String()
			}
		}
		if err := resolveCostTarget(ctx, &cost, scraperID); err != nil {
			skip(err)
			continue
		}
		resolved = append(resolved, cost)
	}

	if len(resolved) > 0 {
		saved, err := upsertConfigCosts(ctx, bucketCosts(resolved, scraperID), scraperID)
		if err != nil {
			summary.ExternalCosts.Skipped += len(resolved)
			costErrors = append(costErrors, fmt.Errorf("failed to persist %d valid external costs: %w", len(resolved), err))
		} else {
			summary.ExternalCosts.Saved += saved
		}
	}
	return errors.Join(costErrors...)
}
