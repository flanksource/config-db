// Derives the queryable cost series in config_cost_compact from the raw config_costs
// landing zone, refreshes the summary the catalog reads, and enforces retention on both.
package jobs

import (
	"fmt"
	"time"

	"github.com/flanksource/commons/duration"
	"github.com/flanksource/commons/properties"
	"github.com/flanksource/duty/job"
	"github.com/flanksource/duty/models"

	"github.com/flanksource/config-db/db"
)

// Compaction and retention thresholds. Every one is a property so an operator can trade
// resolution against storage without a release.
const (
	propCompactDayAfter  = "config.costs.compact.level2.after" // raw rows older than this are summarised at level 2
	propCompact30dAfter  = "config.costs.compact.level3.after" // level-2 rows older than this are rolled into level 3
	propCompactWindow    = "config.costs.compact.window"       // how far back to look for restated raw rows
	propCostsRetention   = "config.costs.retention"            // config_costs
	propCompactRetention = "config.costs.compact.retention"    // config_cost_compact

	defaultCompactDayAfter  = "48h"
	defaultCompact30dAfter  = "90d"
	defaultCompactWindow    = "2h"
	defaultCostsRetention   = "90d"
	defaultCompactRetention = "365d"
)

var configCostJobs = []*job.Job{RefreshConfigCostSummary, CompactConfigCosts}

// RefreshConfigCostSummary keeps the trailing-window totals the `configs` view serves in
// step with config_cost_compact. The windows are relative to now(), so the summary goes
// stale on its own even when nothing new is scraped — and cost_1h decays fastest, which is
// what sets the cadence.
var RefreshConfigCostSummary = &job.Job{
	Name: "RefreshConfigCostSummary", Schedule: "@every 15m", Singleton: true,
	JobHistory: true, Retention: job.RetentionBalanced,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = JobResourceType
		if err := ctx.DB().Exec("SELECT refresh_config_cost_summary()").Error; err != nil {
			return err
		}
		ctx.History.SuccessCount++
		return nil
	},
}

// CompactConfigCosts rebuilds config_cost_compact from config_costs and ages the result
// down the level ladder. Runs every 30 minutes so the finest level the summary reads is
// never far behind the scrapers.
var CompactConfigCosts = &job.Job{
	Name: "CompactConfigCosts", Schedule: "@every 30m", Singleton: true,
	JobHistory: true, Retention: job.RetentionBalanced,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = JobResourceType

		// Level widths come from properties and must still form a ladder; a
		// misconfiguration is rejected before anything is written.
		levels, err := db.ResolveCostLevels()
		if err != nil {
			return err
		}

		// A raw row is compacted at the coarser of its own level and the level its age
		// implies. Age alone is not enough: a month-long charge is level 3 the moment it
		// is scraped, and forcing it into a level-1 bucket would report a month of spend
		// as if it happened in one hour.
		//
		// Both passes read from config_costs, which keeps its rows, so both REPLACE the
		// bucket rather than add to it.
		levelUp, err := thresholdOf(propCompactDayAfter, defaultCompactDayAfter)
		if err != nil {
			return err
		}
		young := fmt.Sprintf("period_end >= now() - make_interval(secs => %f)", levelUp.Seconds())
		aged := fmt.Sprintf("period_end < now() - make_interval(secs => %f)", levelUp.Seconds())

		fine, err := compactFromRaw(ctx, models.ConfigCostLevel1, levels.L1,
			fmt.Sprintf("grain = '%s' AND %s", models.ConfigCostLevel1, young))
		if err != nil {
			return err
		}
		ctx.History.SuccessCount += fine

		coarse, err := compactFromRaw(ctx, models.ConfigCostLevel2, levels.L2,
			fmt.Sprintf("(grain = '%s' AND %s) OR grain = '%s'",
				models.ConfigCostLevel1, aged, models.ConfigCostLevel2))
		if err != nil {
			return err
		}
		ctx.History.SuccessCount += coarse

		// Long charge periods never pass through the finer levels at all.
		native, err := compactFromRaw(ctx, models.ConfigCostLevel3, levels.L3,
			fmt.Sprintf("grain = '%s'", models.ConfigCostLevel3))
		if err != nil {
			return err
		}
		ctx.History.SuccessCount += native

		// A level-1 row that has aged past the threshold has just been rewritten at level
		// 2. Without this the finer copy survives alongside the coarser one and the same
		// money is counted twice.
		superseded := ctx.DB().Exec(
			fmt.Sprintf(`DELETE FROM config_cost_compact WHERE grain = ? AND %s`, aged),
			models.ConfigCostLevel1)
		if superseded.Error != nil {
			return fmt.Errorf("failed to drop superseded level-1 costs: %w", superseded.Error)
		}
		ctx.History.SuccessCount += int(superseded.RowsAffected)

		rolled, err := rollToCoarsestLevel(ctx, levels)
		if err != nil {
			return err
		}
		ctx.History.SuccessCount += rolled

		expired, err := expireCosts(ctx, "config_costs", propCostsRetention, defaultCostsRetention)
		if err != nil {
			return err
		}
		ctx.History.SuccessCount += expired

		expired, err = expireCosts(ctx, "config_cost_compact", propCompactRetention, defaultCompactRetention)
		if err != nil {
			return err
		}
		ctx.History.SuccessCount += expired

		return nil
	},
}

// compactFromRaw recomputes one grain of config_cost_compact from config_costs.
//
// Only buckets containing raw rows touched since the last pass are rebuilt. Providers
// restate open billing periods for weeks, and config_costs keeps its rows rather than
// handing them over, so a bucket already written here can change afterwards — which is why
// the conflict clause SETs the recomputed total instead of adding to it. Adding would
// double-count every restatement.
func compactFromRaw(ctx job.JobRuntime, grain string, width time.Duration, ageFilter string) (int, error) {
	window, err := thresholdOf(propCompactWindow, defaultCompactWindow)
	if err != nil {
		return 0, err
	}

	// cost_bucket takes the width as a parameter, which is what lets it stay IMMUTABLE
	// while the width itself is an operator setting. It is the same epoch-anchored
	// arithmetic as db.truncateTo, so ingestion and compaction agree exactly.
	seconds := int64(width / time.Second)
	bucketStart := fmt.Sprintf("cost_bucket(period_start, %d)", seconds)
	bucketEnd := fmt.Sprintf("cost_bucket(period_start, %d) + make_interval(secs => %d)", seconds, seconds)

	// Grouping by the full identity tuple means scraper_id and pricing_unit are exact
	// rather than an arbitrary MIN(): both are functionally determined by columns already
	// in the group — scraper_id by source_key, pricing_unit by fingerprint.
	query := fmt.Sprintf(`
		INSERT INTO config_cost_compact (
			config_id, scraper_id, source_key, external_id, external_config_type,
			external_config_scraper_id, external_config_labels, period_start, period_end, grain,
			charge_category, charge_class, service_name, service_category, sku_id, region_id,
			billing_currency, billed_cost, effective_cost, list_cost, contracted_cost,
			pricing_quantity, pricing_unit, focus, fingerprint
		)
		SELECT
			config_id, scraper_id, source_key, external_id, external_config_type,
			external_config_scraper_id, external_config_labels,
			%s, %s, ?::text,
			charge_category, charge_class, service_name, service_category, sku_id, region_id,
			billing_currency,
			SUM(billed_cost), SUM(effective_cost), SUM(list_cost), SUM(contracted_cost),
			SUM(pricing_quantity), pricing_unit,
			(array_agg(focus ORDER BY period_start))[1], fingerprint
		FROM config_costs
		WHERE %s
		  AND (config_id, source_key, fingerprint) IN (
			SELECT config_id, source_key, fingerprint FROM config_costs
			WHERE updated_at >= now() - make_interval(secs => ?)
		  )
		GROUP BY config_id, scraper_id, source_key, external_id, external_config_type,
		         external_config_scraper_id, external_config_labels, 8, 9,
		         charge_category, charge_class, service_name, service_category, sku_id,
		         region_id, billing_currency, pricing_unit, fingerprint
		ON CONFLICT (source_key, config_id, period_start, period_end, fingerprint)
		DO UPDATE SET grain            = excluded.grain,
		              external_id      = excluded.external_id,
		              billed_cost      = excluded.billed_cost,
		              effective_cost   = excluded.effective_cost,
		              list_cost        = excluded.list_cost,
		              contracted_cost  = excluded.contracted_cost,
		              pricing_quantity = excluded.pricing_quantity,
		              focus            = excluded.focus,
		              updated_at       = now()`, bucketStart, bucketEnd, ageFilter)

	result := ctx.DB().Exec(query, grain, window.Seconds())
	if result.Error != nil {
		return 0, fmt.Errorf("failed to compact raw costs at %s grain: %w", grain, result.Error)
	}
	return int(result.RowsAffected), nil
}

// rollToCoarsestLevel coarsens level-2 rows that have outlived the raw data they were
// built from.
//
// Unlike compactFromRaw this is terminal and additive: config_costs no longer holds the
// source rows, so the 1d rows are the only remaining record and are summed into the 30d
// bucket and deleted. Idempotent because the second pass finds no 1d rows left in range.
func rollToCoarsestLevel(ctx job.JobRuntime, levels db.CostLevels) (int, error) {
	threshold, err := thresholdOf(propCompact30dAfter, defaultCompact30dAfter)
	if err != nil {
		return 0, err
	}

	tx := ctx.DB().Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	// Level widths divide each other, so a level-2 bucket always falls entirely inside one
	// level-3 bucket and this stays pure summation.
	seconds := int64(levels.L3 / time.Second)
	insert := fmt.Sprintf(`
		INSERT INTO config_cost_compact (
			config_id, scraper_id, source_key, external_id, external_config_type,
			external_config_scraper_id, external_config_labels, period_start, period_end, grain,
			charge_category, charge_class, service_name, service_category, sku_id, region_id,
			billing_currency, billed_cost, effective_cost, list_cost, contracted_cost,
			pricing_quantity, pricing_unit, focus, fingerprint
		)
		SELECT
			config_id, scraper_id, source_key, external_id, external_config_type,
			external_config_scraper_id, external_config_labels,
			cost_bucket(period_start, %[1]d), cost_bucket(period_start, %[1]d) + make_interval(secs => %[1]d), ?::text,
			charge_category, charge_class, service_name, service_category, sku_id, region_id,
			billing_currency,
			SUM(billed_cost), SUM(effective_cost), SUM(list_cost), SUM(contracted_cost),
			SUM(pricing_quantity), pricing_unit,
			(array_agg(focus ORDER BY period_start))[1], fingerprint
		FROM config_cost_compact
		WHERE grain = ? AND period_end < now() - make_interval(secs => ?)
		GROUP BY config_id, scraper_id, source_key, external_id, external_config_type,
		         external_config_scraper_id, external_config_labels, 8, 9,
		         charge_category, charge_class, service_name, service_category, sku_id,
		         region_id, billing_currency, pricing_unit, fingerprint
		ON CONFLICT (source_key, config_id, period_start, period_end, fingerprint)
		DO UPDATE SET billed_cost      = config_cost_compact.billed_cost + excluded.billed_cost,
		              effective_cost   = config_cost_compact.effective_cost + excluded.effective_cost,
		              list_cost        = COALESCE(config_cost_compact.list_cost, 0) + COALESCE(excluded.list_cost, 0),
		              contracted_cost  = COALESCE(config_cost_compact.contracted_cost, 0) + COALESCE(excluded.contracted_cost, 0),
		              pricing_quantity = COALESCE(config_cost_compact.pricing_quantity, 0) + COALESCE(excluded.pricing_quantity, 0),
		              updated_at       = now()`, seconds)

	if err := tx.Exec(insert, models.ConfigCostLevel3, models.ConfigCostLevel2, threshold.Seconds()).Error; err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to roll level-2 costs into level 3: %w", err)
	}

	del := tx.Exec(`DELETE FROM config_cost_compact WHERE grain = ? AND period_end < now() - make_interval(secs => ?)`,
		models.ConfigCostLevel2, threshold.Seconds())
	if del.Error != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to drop rolled level-2 costs: %w", del.Error)
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return int(del.RowsAffected), nil
}

// expireCosts drops history past the retention window. config_costs defaults to 90 days as
// a raw audit trail; config_cost_compact defaults to a year so a year-on-year comparison
// still has last year to compare against.
func expireCosts(ctx job.JobRuntime, table, property, fallback string) (int, error) {
	retention, err := thresholdOf(property, fallback)
	if err != nil {
		return 0, err
	}
	result := ctx.DB().Exec(
		fmt.Sprintf(`DELETE FROM %s WHERE period_end < now() - make_interval(secs => ?)`, table),
		retention.Seconds())
	if result.Error != nil {
		return 0, fmt.Errorf("failed to expire %s: %w", table, result.Error)
	}
	return int(result.RowsAffected), nil
}

func thresholdOf(property, fallback string) (time.Duration, error) {
	raw := properties.String(fallback, property)
	parsed, err := duration.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", property, raw, err)
	}
	return time.Duration(parsed), nil
}
