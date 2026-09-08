// Moves a charge onto the resource it names once the catalog has discovered that
// resource, for the charge periods no provider will restate again.
package jobs

import (
	"fmt"

	"github.com/flanksource/duty/job"
)

// The cost tables carry the same attribution and are re-pointed by the same rule.
// config_costs is the source of truth and is corrected first; config_cost_compact is
// corrected directly rather than left to compaction, because it outlives the raw rows it
// was built from and the oldest part of the series has no raw source left to rebuild from.
var costAttributionTables = []string{"config_costs", "config_cost_compact"}

// ReattributeConfigCosts re-points charges whose resource the catalog discovered late.
//
// Runs an hour ahead of ReconcileConfigCosts so the nightly rebuild carries the corrected
// attribution from raw into the compacted series in the same night.
var ReattributeConfigCosts = &job.Job{
	Name: "ReattributeConfigCosts", Schedule: "0 2 * * *", Singleton: true,
	JobHistory: true, Retention: job.RetentionBalanced,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = JobResourceType
		for _, table := range costAttributionTables {
			moved, err := reattributeCosts(ctx, table)
			if err != nil {
				return err
			}
			ctx.History.SuccessCount += moved
		}
		return nil
	},
}

// reattributeCosts asks again, for every charge in one cost table, which config item its
// external id names — and books the charge there.
//
// A charge is resolved once, at scrape time, and providers stop restating a billing period
// within weeks of closing it. Whatever attribution a row held when its period went quiet is
// therefore frozen. A resource discovered after its first charges landed keeps those early
// charges booked against the account root for as long as they are retained: the resource
// reports less spend than it incurred, and the same spend is listed as an undiscovered
// resource the catalog is missing.
//
// The lookup mirrors resolveCostTarget. external_config_type, external_config_scraper_id
// and external_config_labels are the scope the charge was originally resolved under, kept
// on the row as provenance, so asking here asks exactly what ingest asked. A resource id
// matching more than one config item is left where it is — never guess a resource.
//
// One case differs from ingest: a feed that supplies both an explicit config_id and a
// resource_id naming a different resource has the resource_id honoured here, where ingest
// honours the explicit id. The scrapers never emit both, and FOCUS defines ResourceId as
// the resource the charge is for.
func reattributeCosts(ctx job.JobRuntime, table string) (int, error) {
	// The distinct identities number in the resources, the rows in the charge periods —
	// several orders of magnitude apart. Materialising the identities is what turns a
	// catalog probe per row into one per resource; inlined, the planner runs the lateral
	// once for every row in the table.
	query := fmt.Sprintf(`
		WITH targets AS MATERIALIZED (
			SELECT DISTINCT external_id, external_config_type, external_config_scraper_id,
			       external_config_labels
			FROM %[1]s
			WHERE external_id IS NOT NULL
		), resolved AS MATERIALIZED (
			SELECT t.external_id, t.external_config_type, t.external_config_scraper_id,
			       t.external_config_labels, m.id AS target
			FROM targets t
			JOIN LATERAL (
				SELECT (array_agg(ci.id ORDER BY ci.id))[1] AS id, count(*) AS matches
				FROM config_items ci
				WHERE ci.external_id @> ARRAY[t.external_id]
				  AND (t.external_config_type IS NULL OR ci.type = t.external_config_type)
				  AND (t.external_config_scraper_id IS NULL
				       OR t.external_config_scraper_id = 'all'
				       OR ci.scraper_id::text = t.external_config_scraper_id)
				  AND NOT EXISTS (
					SELECT 1
					FROM jsonb_each_text(COALESCE(t.external_config_labels, '{}'::jsonb)) AS scope(key, value)
					WHERE COALESCE(ci.tags ->> scope.key, ci.labels ->> scope.key) IS DISTINCT FROM scope.value)
				  AND (ci.deleted_at IS NULL OR NOT EXISTS (
					SELECT 1 FROM config_items live
					WHERE live.external_id @> ARRAY[t.external_id] AND live.deleted_at IS NULL))
			) m ON m.matches = 1
		)
		UPDATE %[1]s c
		SET config_id = r.target, updated_at = now()
		FROM resolved r
		WHERE r.external_id = c.external_id
		  AND r.external_config_type IS NOT DISTINCT FROM c.external_config_type
		  AND r.external_config_scraper_id IS NOT DISTINCT FROM c.external_config_scraper_id
		  AND r.external_config_labels IS NOT DISTINCT FROM c.external_config_labels
		  AND c.config_id <> r.target`, table)

	result := ctx.DB().Exec(query)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to reattribute %s: %w", table, result.Error)
	}
	return int(result.RowsAffected), nil
}
