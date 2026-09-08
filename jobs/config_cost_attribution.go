// Moves a charge onto the resource it names once the catalog has discovered that
// resource, for the charge periods no provider will restate again.
package jobs

import (
	"fmt"
	"strings"

	"github.com/flanksource/duty/job"

	v1 "github.com/flanksource/config-db/api/v1"
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

// scraperLessTypes renders the config types findConfigMatches never scopes by scraper, as
// a SQL array. Read from the resolver's own list so the two cannot drift apart.
func scraperLessTypes() string {
	quoted := make([]string, 0, len(v1.ScraperLessTypes))
	for _, configType := range v1.ScraperLessTypes {
		quoted = append(quoted, "'"+strings.ReplaceAll(configType, "'", "''")+"'")
	}
	return "ARRAY[" + strings.Join(quoted, ", ") + "]::text[]"
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
// on the row as provenance, so asking here asks exactly what ingest asked; a charge naming
// no scraper is scoped to the one that emitted it, as findConfigMatches scopes it to the
// scraper doing the scrape. A resource id matching more than one config item is left where
// it is — never guess a resource.
//
// Candidates are ranked the way resolveCostTarget ranks them: a GCP resource name that
// matches nothing falls back to its last segment, which is how the services that bill
// under a numeric provider id reach the resource inventory stores under its name. The full
// name always wins, so a short name cannot pull a charge onto another type. Within the
// candidates of one precision, a live config item wins over a soft-deleted one — and both
// tiers are drawn from the same scoped set, so an item the scope excludes neither claims a
// charge nor blocks one.
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
			       external_config_labels, scraper_id
			FROM %[1]s
			WHERE external_id IS NOT NULL
		), scoped AS MATERIALIZED (
			SELECT t.*,
			       CASE WHEN t.external_id LIKE '//%%'
			             AND t.external_id LIKE '%%.googleapis.com/%%'
			             AND regexp_replace(t.external_id, '^.*/', '') NOT IN ('', t.external_id)
			            THEN regexp_replace(t.external_id, '^.*/', '')
			       END AS basename,
			       CASE WHEN COALESCE(t.external_config_scraper_id, t.scraper_id::text) = 'all'
			             OR t.external_config_type = ANY (%[2]s)
			            THEN NULL
			            ELSE COALESCE(t.external_config_scraper_id, t.scraper_id::text)
			       END AS scraper_scope
			FROM targets t
		), resolved AS MATERIALIZED (
			SELECT t.external_id, t.external_config_type, t.external_config_scraper_id,
			       t.external_config_labels, t.scraper_id, m.id AS target
			FROM scoped t
			JOIN LATERAL (
				SELECT (array_agg(c.id ORDER BY c.id))[1] AS id, count(*) AS matches
				FROM (
					SELECT r.id, r.live, r.specificity,
					       min(r.specificity) OVER ()                         AS best,
					       bool_or(r.live) OVER (PARTITION BY r.specificity)  AS any_live
					FROM (
						SELECT ci.id,
						       (ci.deleted_at IS NULL) AS live,
						       CASE WHEN ci.external_id @> ARRAY[t.external_id] THEN 0 ELSE 1 END AS specificity
						FROM config_items ci
						WHERE (ci.external_id @> ARRAY[t.external_id]
						       OR (t.basename IS NOT NULL AND ci.external_id @> ARRAY[t.basename]))
						  AND (t.external_config_type IS NULL OR ci.type = t.external_config_type)
						  AND (t.scraper_scope IS NULL OR ci.scraper_id::text = t.scraper_scope)
						  AND NOT EXISTS (
							SELECT 1
							FROM jsonb_each_text(COALESCE(t.external_config_labels, '{}'::jsonb)) AS scope(key, value)
							WHERE COALESCE(ci.tags ->> scope.key, ci.labels ->> scope.key) IS DISTINCT FROM scope.value)
					) r
				) c
				WHERE c.specificity = c.best AND c.live = c.any_live
			) m ON m.matches = 1
		)
		UPDATE %[1]s c
		SET config_id = r.target, updated_at = now()
		FROM resolved r
		WHERE r.external_id = c.external_id
		  AND r.external_config_type IS NOT DISTINCT FROM c.external_config_type
		  AND r.external_config_scraper_id IS NOT DISTINCT FROM c.external_config_scraper_id
		  AND r.external_config_labels IS NOT DISTINCT FROM c.external_config_labels
		  AND r.scraper_id IS NOT DISTINCT FROM c.scraper_id
		  AND c.config_id <> r.target`, table, scraperLessTypes())

	result := ctx.DB().Exec(query)
	if result.Error != nil {
		return 0, fmt.Errorf("failed to reattribute %s: %w", table, result.Error)
	}
	return int(result.RowsAffected), nil
}
