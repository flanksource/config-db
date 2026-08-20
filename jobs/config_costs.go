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

var configCostJobs = []*job.Job{RefreshConfigCostSummary, CompactConfigCosts, ReconcileConfigCosts}

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

// CompactConfigCosts keeps config_cost_compact in step with config_costs. It touches only
// the identities with a reason to have changed, so its cost tracks the scrape rate rather
// than the size of the history.
var CompactConfigCosts = &job.Job{
	Name: "CompactConfigCosts", Schedule: "@every 30m", Singleton: true,
	JobHistory: true, Retention: job.RetentionBalanced,
	Fn: func(ctx job.JobRuntime) error { return compactCosts(ctx, false) },
}

// ReconcileConfigCosts rebuilds config_cost_compact from config_costs in full, ignoring
// the restatement window.
//
// The incremental pass only visits identities that something marked as needing work, so a
// transition missed while the job was down is never revisited. This pass is what stops
// that from being permanent: it recomputes the whole retained range from raw, so any
// divergence between the two tables lasts at most a day — well inside the raw retention
// the recovery reads from.
var ReconcileConfigCosts = &job.Job{
	Name: "ReconcileConfigCosts", Schedule: "0 3 * * *", Singleton: true,
	JobHistory: true, Retention: job.RetentionBalanced,
	Fn: func(ctx job.JobRuntime) error { return compactCosts(ctx, true) },
}

// compactCosts runs the compaction ladder. When full is set every row in range is
// recomputed from raw; otherwise the passes are limited to identities with pending work.
func compactCosts(ctx job.JobRuntime, full bool) error {
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
	thresholds, err := resolveCostThresholds()
	if err != nil {
		return err
	}
	// The compaction passes read config_costs as c; the superseded-delete reads
	// config_cost_compact, so it needs the same predicate unqualified.
	young := fmt.Sprintf("c.period_end >= now() - make_interval(secs => %f)", thresholds.LevelUp.Seconds())
	aged := fmt.Sprintf("c.period_end < now() - make_interval(secs => %f)", thresholds.LevelUp.Seconds())
	agedCompact := fmt.Sprintf("period_end < now() - make_interval(secs => %f)", thresholds.LevelUp.Seconds())

	compact := func(grain string, width time.Duration, ageFilter string) (int, error) {
		if full {
			return compactFull(ctx, grain, width, ageFilter)
		}
		return compactIncremental(ctx, grain, width, thresholds.Window, thresholds.LevelUp, ageFilter)
	}

	fine, err := compact(models.ConfigCostLevel1, levels.L1,
		fmt.Sprintf("c.grain = '%s' AND %s", models.ConfigCostLevel1, young))
	if err != nil {
		return err
	}
	ctx.History.SuccessCount += fine

	coarse, err := compact(models.ConfigCostLevel2, levels.L2,
		fmt.Sprintf("(c.grain = '%s' AND %s) OR c.grain = '%s'",
			models.ConfigCostLevel1, aged, models.ConfigCostLevel2))
	if err != nil {
		return err
	}
	ctx.History.SuccessCount += coarse

	// Long charge periods never pass through the finer levels at all.
	native, err := compact(models.ConfigCostLevel3, levels.L3,
		fmt.Sprintf("c.grain = '%s'", models.ConfigCostLevel3))
	if err != nil {
		return err
	}
	ctx.History.SuccessCount += native

	// A level-1 row that has aged past the threshold has just been rewritten at level 2 —
	// the coarse pass takes every identity holding one, which is what makes this safe.
	// Without the delete the finer copy survives alongside the coarser one and the same
	// money is counted twice.
	superseded := ctx.DB().Exec(
		fmt.Sprintf(`DELETE FROM config_cost_compact WHERE grain = ? AND %s`, agedCompact),
		models.ConfigCostLevel1)
	if superseded.Error != nil {
		return fmt.Errorf("failed to drop superseded level-1 costs: %w", superseded.Error)
	}
	ctx.History.SuccessCount += int(superseded.RowsAffected)

	rolled, err := rollToCoarsestLevel(ctx, levels, thresholds.Roll)
	if err != nil {
		return err
	}
	ctx.History.SuccessCount += rolled

	expired, err := expireCosts(ctx, "config_costs", thresholds.RawRetention)
	if err != nil {
		return err
	}
	ctx.History.SuccessCount += expired

	expired, err = expireCosts(ctx, "config_cost_compact", thresholds.CompactRetention)
	if err != nil {
		return err
	}
	ctx.History.SuccessCount += expired

	return nil
}

// pendingBuckets names the buckets that can still change: those holding raw rows restated
// inside the window, and those holding a level-1 copy about to be superseded.
//
// The second arm is what keeps the superseded-delete honest. That delete drops level-1
// rows on age alone, so the coarse pass must rewrite every bucket holding one before it
// runs — selecting exactly that set means the two can never disagree.
func pendingBuckets(bucketSeconds int64, window, levelUp time.Duration) string {
	return fmt.Sprintf(`
		SELECT DISTINCT config_id, source_key, fingerprint, cost_bucket(period_start, %[1]d) AS bucket
		FROM config_costs WHERE updated_at >= now() - make_interval(secs => %[2]f)
		UNION
		SELECT DISTINCT config_id, source_key, fingerprint, cost_bucket(period_start, %[1]d) AS bucket
		FROM config_cost_compact
		WHERE grain = '%[3]s' AND period_end < now() - make_interval(secs => %[4]f)`,
		bucketSeconds, window.Seconds(), models.ConfigCostLevel1, levelUp.Seconds())
}

// The projection is identical either way; only how the rows are reached differs.
const compactTargetColumns = `config_id, scraper_id, source_key, external_id, external_config_type,
			external_config_scraper_id, external_config_labels, period_start, period_end, grain,
			charge_category, charge_class, service_name, service_category, sku_id, region_id,
			billing_currency, billed_cost, effective_cost, list_cost, contracted_cost,
			pricing_quantity, pricing_unit, focus, fingerprint`

// Grouping by the full identity tuple means scraper_id and pricing_unit are exact rather
// than an arbitrary MIN(): both are functionally determined by columns already in the
// group — scraper_id by source_key, pricing_unit by fingerprint.
const compactGroupBy = `c.config_id, c.scraper_id, c.source_key, c.external_id, c.external_config_type,
		         c.external_config_scraper_id, c.external_config_labels,
		         c.charge_category, c.charge_class, c.service_name, c.service_category,
		         c.sku_id, c.region_id, c.billing_currency, c.pricing_unit, c.fingerprint`

// config_costs keeps its rows rather than handing them over, and providers restate open
// billing periods for weeks, so a bucket already written here can change afterwards. The
// conflict clause SETs the recomputed total instead of adding to it; adding would
// double-count every restatement.
const compactOnConflict = `ON CONFLICT (source_key, config_id, period_start, period_end, fingerprint)
		DO UPDATE SET grain            = excluded.grain,
		              external_id      = excluded.external_id,
		              billed_cost      = excluded.billed_cost,
		              effective_cost   = excluded.effective_cost,
		              list_cost        = excluded.list_cost,
		              contracted_cost  = excluded.contracted_cost,
		              pricing_quantity = excluded.pricing_quantity,
		              focus            = excluded.focus,
		              updated_at       = now()`

// compactAggregates is the SELECT list shared by both access paths. Callers supply the
// expressions for period_start and period_end, which is where the two differ.
func compactAggregates(bucketStart, bucketEnd string) string {
	return fmt.Sprintf(`c.config_id, c.scraper_id, c.source_key, c.external_id, c.external_config_type,
			c.external_config_scraper_id, c.external_config_labels,
			%s, %s, ?::text,
			c.charge_category, c.charge_class, c.service_name, c.service_category, c.sku_id, c.region_id,
			c.billing_currency,
			SUM(c.billed_cost), SUM(c.effective_cost), SUM(c.list_cost), SUM(c.contracted_cost),
			SUM(c.pricing_quantity), c.pricing_unit,
			(array_agg(c.focus ORDER BY c.period_start))[1], c.fingerprint`, bucketStart, bucketEnd)
}

// compactIncremental recomputes only the buckets that have a reason to have changed.
//
// The bucket is reached by an equality on the identity plus a range on period_start,
// which is a prefix of the merge index — so this drives off the pending set and touches
// only the rows inside those buckets. Comparing a computed cost_bucket() instead would
// leave nothing indexable and force a scan of the whole retained history every run.
func compactIncremental(ctx job.JobRuntime, grain string, width, window, levelUp time.Duration, ageFilter string) (int, error) {
	seconds := int64(width / time.Second)
	query := fmt.Sprintf(`
		INSERT INTO config_cost_compact (%s)
		WITH pending AS (%s)
		SELECT %s
		FROM pending p
		JOIN config_costs c
		  ON c.source_key = p.source_key AND c.config_id = p.config_id
		 AND c.fingerprint = p.fingerprint
		 AND c.period_start >= p.bucket
		 AND c.period_start <  p.bucket + make_interval(secs => %d)
		WHERE %s
		GROUP BY p.bucket, %s
		%s`,
		compactTargetColumns,
		pendingBuckets(seconds, window, levelUp),
		compactAggregates("p.bucket", fmt.Sprintf("p.bucket + make_interval(secs => %d)", seconds)),
		seconds, ageFilter, compactGroupBy, compactOnConflict)

	result := ctx.DB().Exec(query, grain)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to compact raw costs at %s grain: %w", grain, result.Error)
	}
	return int(result.RowsAffected), nil
}

// compactFull rebuilds every bucket in range from raw, ignoring what has changed.
//
// There is no pending set to drive from here, so this reads config_costs directly and
// buckets inline — a scan is the right plan when the answer covers the whole table.
func compactFull(ctx job.JobRuntime, grain string, width time.Duration, ageFilter string) (int, error) {
	seconds := int64(width / time.Second)
	bucketStart := fmt.Sprintf("cost_bucket(c.period_start, %d)", seconds)
	bucketEnd := fmt.Sprintf("cost_bucket(c.period_start, %d) + make_interval(secs => %d)", seconds, seconds)
	query := fmt.Sprintf(`
		INSERT INTO config_cost_compact (%s)
		SELECT %s
		FROM config_costs c
		WHERE %s
		GROUP BY %s, %s
		%s`,
		compactTargetColumns, compactAggregates(bucketStart, bucketEnd),
		ageFilter, bucketStart, compactGroupBy, compactOnConflict)

	result := ctx.DB().Exec(query, grain)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to rebuild raw costs at %s grain: %w", grain, result.Error)
	}
	return int(result.RowsAffected), nil
}

// rollToCoarsestLevel coarsens level-2 rows that have outlived the raw data they were
// built from.
//
// Unlike compactFromRaw this is terminal and additive: config_costs no longer holds the
// source rows, so the 1d rows are the only remaining record and are summed into the 30d
// bucket and deleted. Idempotent because the second pass finds no 1d rows left in range.
func rollToCoarsestLevel(ctx job.JobRuntime, levels db.CostLevels, threshold time.Duration) (int, error) {
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
func expireCosts(ctx job.JobRuntime, table string, retention time.Duration) (int, error) {
	result := ctx.DB().Exec(
		fmt.Sprintf(`DELETE FROM %s WHERE period_end < now() - make_interval(secs => ?)`, table),
		retention.Seconds())
	if result.Error != nil {
		return 0, fmt.Errorf("failed to expire %s: %w", table, result.Error)
	}
	return int(result.RowsAffected), nil
}

// costThresholds is the resolved compaction and retention ladder.
type costThresholds struct {
	LevelUp          time.Duration // raw rows older than this are summarised at level 2
	Roll             time.Duration // level-2 rows older than this are rolled into level 3
	Window           time.Duration // how far back to look for restated raw rows
	RawRetention     time.Duration // config_costs
	CompactRetention time.Duration // config_cost_compact
}

// resolveCostThresholds reads the compaction and retention thresholds and checks they
// still describe a survivable path down the ladder.
//
// The two are independent properties but not independent settings: a row must outlive the
// level it is compacted at. The superseded-delete drops a finer copy on age alone, so if
// the source it would be rebuilt from has already expired the money is gone from both
// tables at once. Reject that here rather than delete spend that cannot be recovered.
func resolveCostThresholds() (costThresholds, error) {
	var t costThresholds
	for _, f := range []struct {
		property, fallback string
		into               *time.Duration
	}{
		{propCompactDayAfter, defaultCompactDayAfter, &t.LevelUp},
		{propCompact30dAfter, defaultCompact30dAfter, &t.Roll},
		{propCompactWindow, defaultCompactWindow, &t.Window},
		{propCostsRetention, defaultCostsRetention, &t.RawRetention},
		{propCompactRetention, defaultCompactRetention, &t.CompactRetention},
	} {
		d, err := thresholdOf(f.property, f.fallback)
		if err != nil {
			return t, err
		}
		*f.into = d
	}

	if t.RawRetention <= t.LevelUp {
		return t, fmt.Errorf("%s (%s) must be longer than %s (%s): raw costs would expire before they are summarised at level 2",
			propCostsRetention, t.RawRetention, propCompactDayAfter, t.LevelUp)
	}
	if t.CompactRetention <= t.Roll {
		return t, fmt.Errorf("%s (%s) must be longer than %s (%s): compacted costs would expire before they are rolled into level 3",
			propCompactRetention, t.CompactRetention, propCompact30dAfter, t.Roll)
	}
	return t, nil
}

func thresholdOf(property, fallback string) (time.Duration, error) {
	raw := properties.String(fallback, property)
	parsed, err := duration.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", property, raw, err)
	}
	return time.Duration(parsed), nil
}
