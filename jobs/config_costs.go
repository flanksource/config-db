// Keeps config_costs serviceable: refreshes the rollup the catalog reads, attaches
// late-arriving resources, optionally coarsens old buckets, and enforces retention.
package jobs

import (
	"fmt"
	"time"

	"github.com/flanksource/commons/duration"
	"github.com/flanksource/commons/properties"
	"github.com/flanksource/duty/job"
)

var configCostJobs = []*job.Job{
	RefreshConfigCostsRollup,
	CompactConfigCosts,
}

// RefreshConfigCostsRollup keeps the trailing-window totals the `configs` view serves in
// step with config_costs. The windows are relative to now(), so the rollup drifts stale
// on its own even when nothing new is scraped.
//
// Scheduled here rather than alongside the other catalog matview refreshes in
// incident-commander because config-db owns the writes to config_costs.
var RefreshConfigCostsRollup = &job.Job{
	Name:       "RefreshConfigCostsRollup",
	Schedule:   "@every 1h",
	Singleton:  true,
	JobHistory: true,
	Retention:  job.RetentionBalanced,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = JobResourceType
		if err := ctx.DB().Exec("SELECT refresh_config_costs_rollup()").Error; err != nil {
			return err
		}
		ctx.History.SuccessCount++
		return nil
	},
}

// CompactConfigCosts runs three passes, each independent of the others.
var CompactConfigCosts = &job.Job{
	Name:       "CompactConfigCosts",
	Schedule:   "@every 1h",
	Singleton:  true,
	JobHistory: true,
	Retention:  job.RetentionBalanced,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = JobResourceType

		attached, err := attachUnmatchedCosts(ctx)
		if err != nil {
			return err
		}
		ctx.History.SuccessCount += attached

		coarsened, err := coarsenConfigCosts(ctx)
		if err != nil {
			return err
		}
		ctx.History.SuccessCount += coarsened

		expired, err := expireConfigCosts(ctx)
		if err != nil {
			return err
		}
		ctx.History.SuccessCount += expired

		return nil
	},
}

// attachUnmatchedCosts claims spend whose resource has since been scraped.
//
// A cost already exists for the target bucket when the same resource was matched on a
// later run, so the orphan's amounts are folded into it and the orphan dropped. This is
// what stops a late-arriving resource from leaking into an account-level total.
func attachUnmatchedCosts(ctx job.JobRuntime) (int, error) {
	merge := ctx.DB().Exec(`
		WITH matched AS (
			SELECT c.id AS orphan_id, ci.id AS config_id
			FROM config_costs c
			JOIN config_items ci ON c.external_id = ANY(ci.external_id)
			WHERE c.config_id IS NULL AND ci.deleted_at IS NULL
		),
		folded AS (
			UPDATE config_costs target
			SET billed_cost      = target.billed_cost + o.billed_cost,
			    effective_cost   = target.effective_cost + o.effective_cost,
			    list_cost        = COALESCE(target.list_cost, 0) + COALESCE(o.list_cost, 0),
			    contracted_cost  = COALESCE(target.contracted_cost, 0) + COALESCE(o.contracted_cost, 0),
			    pricing_quantity = COALESCE(target.pricing_quantity, 0) + COALESCE(o.pricing_quantity, 0),
			    updated_at       = now()
			FROM config_costs o
			JOIN matched m ON m.orphan_id = o.id
			WHERE target.config_id = m.config_id
			  AND target.period_start = o.period_start
			  AND target.period_end = o.period_end
			  AND target.fingerprint = o.fingerprint
			RETURNING o.id AS orphan_id
		)
		DELETE FROM config_costs WHERE id IN (SELECT orphan_id FROM folded)`)
	if merge.Error != nil {
		return 0, fmt.Errorf("failed to fold unmatched costs into existing buckets: %w", merge.Error)
	}

	attach := ctx.DB().Exec(`
		UPDATE config_costs c
		SET config_id = ci.id, updated_at = now()
		FROM config_items ci
		WHERE c.config_id IS NULL
		  AND ci.deleted_at IS NULL
		  AND c.external_id = ANY(ci.external_id)`)
	if attach.Error != nil {
		return 0, fmt.Errorf("failed to attach unmatched costs: %w", attach.Error)
	}

	return int(merge.RowsAffected + attach.RowsAffected), nil
}

// coarsenConfigCosts rolls day buckets up into weeks and weeks into months once they are
// older than the configured thresholds.
//
// Disabled by default: coarsening discards the daily detail permanently, so it is an
// explicit operator choice rather than something that happens silently.
func coarsenConfigCosts(ctx job.JobRuntime) (int, error) {
	var total int

	for _, step := range []struct {
		from, to string
		truncTo  string
		interval string
		property string
	}{
		{"day", "week", "week", "7 days", "config.costs.compact.week.after"},
		{"week", "month", "month", "1 month", "config.costs.compact.month.after"},
	} {
		threshold := properties.String("", step.property)
		if threshold == "" {
			continue
		}

		parsed, err := duration.ParseDuration(threshold)
		if err != nil {
			return total, fmt.Errorf("invalid %s: %w", step.property, err)
		}

		rolled, err := coarsenGrain(ctx, step.from, step.to, step.truncTo, step.interval, time.Duration(parsed))
		if err != nil {
			return total, err
		}
		total += rolled
	}

	return total, nil
}

func coarsenGrain(ctx job.JobRuntime, from, to, truncTo, interval string, olderThan time.Duration) (int, error) {
	tx := ctx.DB().Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}

	insert := tx.Exec(fmt.Sprintf(`
		INSERT INTO config_costs (
			config_id, scraper_id, external_id, period_start, period_end, grain,
			charge_category, charge_class, service_name, service_category, sku_id, region_id,
			billing_account_id, sub_account_id, billing_currency,
			billed_cost, effective_cost, list_cost, contracted_cost, pricing_quantity, pricing_unit,
			focus, fingerprint
		)
		SELECT
			config_id, MIN(scraper_id), external_id,
			date_trunc('%s', period_start),
			date_trunc('%s', period_start) + interval '%s',
			'%s',
			charge_category, charge_class, service_name, service_category, sku_id, region_id,
			billing_account_id, sub_account_id, billing_currency,
			SUM(billed_cost), SUM(effective_cost), SUM(list_cost), SUM(contracted_cost),
			SUM(pricing_quantity), MIN(pricing_unit),
			NULL, fingerprint
		FROM config_costs
		WHERE grain = '%s' AND period_end < now() - make_interval(secs => ?)
		GROUP BY config_id, external_id, 4, charge_category, charge_class, service_name,
		         service_category, sku_id, region_id, billing_account_id, sub_account_id,
		         billing_currency, fingerprint
		ON CONFLICT (config_id, period_start, period_end, fingerprint)
		DO UPDATE SET billed_cost      = config_costs.billed_cost + excluded.billed_cost,
		              effective_cost   = config_costs.effective_cost + excluded.effective_cost,
		              list_cost        = COALESCE(config_costs.list_cost, 0) + COALESCE(excluded.list_cost, 0),
		              contracted_cost  = COALESCE(config_costs.contracted_cost, 0) + COALESCE(excluded.contracted_cost, 0),
		              pricing_quantity = COALESCE(config_costs.pricing_quantity, 0) + COALESCE(excluded.pricing_quantity, 0),
		              updated_at       = now()`,
		truncTo, truncTo, interval, to, from), olderThan.Seconds())
	if insert.Error != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to roll %s costs into %s: %w", from, to, insert.Error)
	}

	del := tx.Exec(fmt.Sprintf(
		`DELETE FROM config_costs WHERE grain = '%s' AND period_end < now() - make_interval(secs => ?)`, from),
		olderThan.Seconds())
	if del.Error != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to drop coarsened %s costs: %w", from, del.Error)
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return int(del.RowsAffected), nil
}

// expireConfigCosts drops history past the retention window. The default covers thirteen
// months so a year-on-year comparison still has last year's month to compare against.
func expireConfigCosts(ctx job.JobRuntime) (int, error) {
	retention := properties.String("400d", "config.costs.retention")
	parsed, err := duration.ParseDuration(retention)
	if err != nil {
		return 0, fmt.Errorf("invalid config.costs.retention: %w", err)
	}

	tx := ctx.DB().Exec(
		`DELETE FROM config_costs WHERE period_end < now() - make_interval(secs => ?)`,
		time.Duration(parsed).Seconds())
	if tx.Error != nil {
		return 0, fmt.Errorf("failed to expire config costs: %w", tx.Error)
	}
	return int(tx.RowsAffected), nil
}
