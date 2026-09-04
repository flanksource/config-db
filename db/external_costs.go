// Snaps scraped FOCUS charge periods onto clock-aligned buckets and persists them.
package db

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/flanksource/commons/duration"
	"github.com/flanksource/commons/properties"
	"github.com/flanksource/duty"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

// Compaction level widths. The stored grain names the level, not its width, so an
// operator can retune resolution without a migration.
const (
	PropCostLevel1 = "config.costs.level1"
	PropCostLevel2 = "config.costs.level2"
	PropCostLevel3 = "config.costs.level3"

	DefaultCostLevel1 = "1h"
	DefaultCostLevel2 = "24h"
	DefaultCostLevel3 = "720h" // 30 days
)

// CostLevels is the resolved width of each compaction level, finest first.
type CostLevels struct {
	L1, L2, L3 time.Duration
}

// ResolveCostLevels reads the configured level widths and checks they still form a
// ladder.
//
// Each width must divide the next exactly. That is what makes compaction pure summation:
// a level-1 bucket always falls entirely inside one level-2 bucket, so no row is ever
// split and no amount is ever derived. A misconfiguration is rejected here rather than
// silently producing approximate money.
func ResolveCostLevels() (CostLevels, error) {
	var levels CostLevels
	for _, l := range []struct {
		property, fallback string
		into               *time.Duration
	}{
		{PropCostLevel1, DefaultCostLevel1, &levels.L1},
		{PropCostLevel2, DefaultCostLevel2, &levels.L2},
		{PropCostLevel3, DefaultCostLevel3, &levels.L3},
	} {
		raw := properties.String(l.fallback, l.property)
		parsed, err := duration.ParseDuration(raw)
		if err != nil {
			return levels, fmt.Errorf("invalid %s %q: %w", l.property, raw, err)
		}
		*l.into = time.Duration(parsed)
	}

	// Widths cross into SQL as a whole number of seconds (cost_bucket takes a bigint), and
	// bucketFor computes period_end from the untruncated duration. A sub-second width
	// therefore truncates to zero and divides by zero in SQL, and a fractional-second one
	// makes the bucket start and end disagree so the buckets stop tiling. Reject both here
	// rather than let either reach the database.
	for _, l := range []struct {
		name  string
		width time.Duration
	}{{"level1", levels.L1}, {"level2", levels.L2}, {"level3", levels.L3}} {
		if l.width < time.Second {
			return levels, fmt.Errorf("cost %s width must be at least 1s (got %s)", l.name, l.width)
		}
		if l.width%time.Second != 0 {
			return levels, fmt.Errorf("cost %s width must be a whole number of seconds (got %s)", l.name, l.width)
		}
	}

	// Dividing cleanly is not enough: each level has to be strictly coarser than the one
	// below. Equal widths give a level its predecessor's exact bucket bounds, and since
	// the merge key does not include the grain, the terminal roll then lands on the row it
	// was built from — adding it to itself before the grain cleanup deletes it.
	switch {
	case levels.L2%levels.L1 != 0:
		return levels, fmt.Errorf("cost level2 (%s) must be a whole multiple of level1 (%s)", levels.L2, levels.L1)
	case levels.L2 <= levels.L1:
		return levels, fmt.Errorf("cost level2 (%s) must be coarser than level1 (%s)", levels.L2, levels.L1)
	case levels.L3%levels.L2 != 0:
		return levels, fmt.Errorf("cost level3 (%s) must be a whole multiple of level2 (%s)", levels.L3, levels.L2)
	case levels.L3 <= levels.L2:
		return levels, fmt.Errorf("cost level3 (%s) must be coarser than level2 (%s)", levels.L3, levels.L2)
	}
	return levels, nil
}

// bucketFor snaps a half-open charge period onto the bucket containing it, at the finest
// level wide enough to hold it.
//
// Periods are snapped, never split: a monthly recurring charge stays one row. Returned
// bounds are half-open [start, end) and always UTC.
//
// Note the level ladder is independent of the age ladder: a charge period longer than
// level 2 is written at level 3 immediately and still lives in config_costs until the
// compaction job ages it out.
func bucketFor(start, end time.Time, levels CostLevels) (time.Time, time.Time, string) {
	start = start.UTC()
	duration := end.UTC().Sub(start)

	width, grain := levels.L3, dutyModels.ConfigCostLevel3
	switch {
	case duration <= levels.L1:
		width, grain = levels.L1, dutyModels.ConfigCostLevel1
	case duration <= levels.L2:
		width, grain = levels.L2, dutyModels.ConfigCostLevel2
	}

	bucket := truncateTo(start, width)
	return bucket, bucket.Add(width), grain
}

// truncateTo floors t onto a multiple of width, anchored on the Unix epoch — the same
// arithmetic as the cost_bucket() SQL function the compaction job uses, so the two agree
// exactly. Anchoring on the epoch makes the common widths land on natural boundaries:
// an hour gives clock hours, a day gives UTC midnights.
func truncateTo(t time.Time, width time.Duration) time.Time {
	secs, w := t.UTC().Unix(), int64(width/time.Second)
	if w <= 0 {
		return t.UTC()
	}
	r := secs % w
	if r < 0 {
		r += w
	}
	return time.Unix(secs-r, 0).UTC()
}

func bucketCosts(costs []v1.ExternalCost, scraperID *uuid.UUID, levels CostLevels) []dutyModels.ConfigCost {
	// Mirrors the config_costs merge key exactly. Keying on anything the database does not
	// treat as identity — the external id, for one — splits rows here that the database
	// then rejects as two inserts onto one conflict key.
	// config_id is deliberately absent. The fingerprint already carries the resource the
	// charge is for, so which config item the charge resolved to is an attribution that
	// moves between scrapes, not part of the charge's identity.
	type key struct {
		source, fingerprint    string
		periodStart, periodEnd time.Time
	}
	merged := make(map[key]*dutyModels.ConfigCost, len(costs))
	order := make([]key, 0, len(costs))
	for _, c := range costs {
		start, end, grain := bucketFor(c.ChargePeriodStart, c.ChargePeriodEnd, levels)
		configID := ""
		if c.ConfigID != nil {
			configID = c.ConfigID.String()
		}
		if configID == "" {
			// resolveCostTarget guarantees a target before bucketing; a zero value here
			// would silently attribute the row to the nil UUID.
			continue
		}
		externalID := v1.NormalizeExternalID(c.ResourceID)
		if externalID == "" {
			externalID = v1.NormalizeExternalID(c.ConfigExternalID.ExternalID)
		}
		k := key{c.SourceKey, c.Fingerprint(), start, end}
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
			ConfigID: *c.ConfigID, ScraperID: scraperID, SourceKey: c.SourceKey,
			ExternalID:              nilIfEmpty(externalID),
			ExternalConfigType:      nilIfEmpty(c.ConfigExternalID.ConfigType),
			ExternalConfigScraperID: nilIfEmpty(firstNonempty(c.ConfigExternalID.ScraperID, c.ScraperID)),
			ExternalConfigLabels:    labels,
			PeriodStart:             start, PeriodEnd: end, Grain: grain,
			ChargeCategory: chargeCategoryOrDefault(c.ChargeCategory), ChargeClass: nilIfEmpty(c.ChargeClass),
			ServiceName: nilIfEmpty(c.ServiceName), ServiceCategory: nilIfEmpty(c.ServiceCategory),
			SkuID: nilIfEmpty(c.SkuID), RegionID: nilIfEmpty(c.RegionID),
			BillingCurrency: c.BillingCurrency, BilledCost: *c.BilledCost, EffectiveCost: *c.EffectiveCost,
			ListCost: copyOptional(c.ListCost), ContractedCost: copyOptional(c.ContractedCost),
			PricingQuantity: copyOptional(c.PricingQuantity), PricingUnit: nilIfEmpty(c.PricingUnit),
			Focus: withAccounts(c), Fingerprint: k.fingerprint,
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
		return order[i].fingerprint < order[j].fingerprint
	})
	out := make([]dutyModels.ConfigCost, 0, len(order))
	for _, k := range order {
		out = append(out, *merged[k])
	}
	return out
}

// Account identifier keys inside the focus payload. They have no dedicated columns, but
// ExternalCost.Fingerprint still hashes them, so two sub-accounts never merge.
const (
	focusBillingAccountID = "billing_account_id"
	focusSubAccountID     = "sub_account_id"
)

// withAccounts folds the account identifiers into the focus payload without mutating the
// caller's map.
func withAccounts(c v1.ExternalCost) types.JSONMap {
	if c.BillingAccountID == "" && c.SubAccountID == "" {
		return c.Focus
	}
	out := make(types.JSONMap, len(c.Focus)+2)
	for k, v := range c.Focus {
		out[k] = v
	}
	if c.BillingAccountID != "" {
		out[focusBillingAccountID] = c.BillingAccountID
	}
	if c.SubAccountID != "" {
		out[focusSubAccountID] = c.SubAccountID
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
	if err := createTempAndInsert(tx, table, "config_costs", costs); err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to setup temp config costs: %w", err)
	}

	// Columns the scrape restates. Both the SET list and the guard below are built from
	// this, so they cannot drift apart and start rewriting rows nothing changed.
	//
	// config_id is restated like any other column: a charge is re-resolved every scrape,
	// and when the catalog discovers the resource it names the charge moves off the
	// account root onto that resource. The newest resolution is the best one, and letting
	// it overwrite is what keeps a retarget from leaving the old booking behind.
	restated := []string{
		"config_id", "external_id", "external_config_type", "external_config_scraper_id",
		"external_config_labels", "charge_category", "charge_class", "service_name",
		"service_category", "sku_id", "region_id", "billing_currency", "pricing_unit",
		"billed_cost", "effective_cost", "list_cost", "contracted_cost",
		"pricing_quantity", "focus", "grain", "scraper_id",
	}
	assignments := make([]string, 0, len(restated)+1)
	current := make([]string, 0, len(restated))
	incoming := make([]string, 0, len(restated))
	for _, column := range restated {
		assignments = append(assignments, fmt.Sprintf("%[1]s=excluded.%[1]s", column))
		current = append(current, "config_costs."+column)
		incoming = append(incoming, "excluded."+column)
	}
	assignments = append(assignments, "updated_at=now()")

	// Plain upsert against the single merge key. There is no second identity to
	// reconcile, so this needs neither a table lock nor a delete-then-insert pass.
	//
	// The guard matters more than it looks: providers restate a window unchanged for weeks,
	// so most of a re-scrape is byte-identical. Without it every one of those rows is
	// rewritten, and because updated_at is indexed the rewrite cannot be a HOT update and
	// touches every index on the table.
	query := fmt.Sprintf(`INSERT INTO config_costs SELECT * FROM %s
		ON CONFLICT (source_key, period_start, period_end, fingerprint)
		DO UPDATE SET %s
		WHERE (%s) IS DISTINCT FROM (%s)`,
		table, strings.Join(assignments, ", "),
		strings.Join(current, ", "), strings.Join(incoming, ", "))
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

// configLookups memoises resolution for the duration of one save.
//
// A provider restates the same resource for every hour of the window, so a batch is
// overwhelmingly repeats: one real export resolved 164 distinct resources across 177,580
// rows. The memo is built per save rather than shared, because config items are written
// earlier in the same call and a longer-lived cache would answer "not scraped yet" for a
// resource that had just been created.
type configLookups map[string][]uuid.UUID

// lookupKey covers every field findConfigMatches filters on, labels included: two lookups
// differing only by scope are different questions.
func lookupKey(lookup v1.ExternalID, defaultScraperID *uuid.UUID) string {
	scope := lookup.ScraperID
	if scope == "" && defaultScraperID != nil {
		scope = defaultScraperID.String()
	}
	keys := make([]string, 0, len(lookup.Labels))
	for k := range lookup.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys)+3)
	parts = append(parts, lookup.ConfigType, v1.NormalizeExternalID(lookup.ExternalID), scope)
	for _, k := range keys {
		parts = append(parts, k+"="+lookup.Labels[k])
	}
	return strings.Join(parts, "\x00")
}

// findConfigMatches performs a deterministic, bounded lookup and exposes ambiguity.
func findConfigMatches(db *gorm.DB, lookup v1.ExternalID, defaultScraperID *uuid.UUID, memo configLookups) ([]uuid.UUID, error) {
	externalID := v1.NormalizeExternalID(lookup.ExternalID)
	if externalID == "" {
		return nil, nil
	}
	key := lookupKey(lookup, defaultScraperID)
	if memo != nil {
		if cached, ok := memo[key]; ok {
			return cached, nil
		}
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
		// Scoping dimensions reach config_items through either map: the cloud scrapers
		// write the owning account to tags, kubernetes writes its own to labels. Tags win
		// where both carry the key, so a scraper-set scope is never overridden by a label
		// the resource itself happens to carry.
		q = q.Where("COALESCE(tags ->> ?, labels ->> ?) = ?", k, k, lookup.Labels[k])
	}
	var ids []uuid.UUID
	if err := q.Order("id ASC").Limit(3).Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	if memo != nil {
		memo[key] = ids
	}
	return ids, nil
}

// costLookupCandidates returns lookup identities from most to least precise.
//
// GCP's billing export usually supplies the same full resource name Cloud Asset
// Inventory stores. Some services instead end that name in a numeric provider id while
// inventory stores the human-readable name and the numeric id as a separate alias. Try
// the full name first so a short name cannot collide with another type, then retain the
// numeric-id fallback for those services.
func costLookupCandidates(lookup v1.ExternalID) []v1.ExternalID {
	candidates := []v1.ExternalID{lookup}
	name := strings.TrimSpace(lookup.ExternalID)
	if !strings.HasPrefix(name, "//") || !strings.Contains(name, ".googleapis.com/") {
		return candidates
	}
	if index := strings.LastIndex(name, "/"); index >= 0 && index < len(name)-1 {
		basename := name[index+1:]
		if v1.NormalizeExternalID(basename) != v1.NormalizeExternalID(name) {
			fallback := lookup
			fallback.ExternalID = basename
			candidates = append(candidates, fallback)
		}
	}
	return candidates
}

// resolveCostTarget picks the config item this cost is attributed to.
//
// Precedence: explicit UUID, then structured external identity, then resource id, then the
// emitting scraper's root config item. config_costs.config_id is NOT NULL, so the root is
// what keeps spend that has no resource — or whose resource has not been scraped yet —
// from being dropped. ResourceID is preserved on the row either way, so a root-attributed
// charge still shows what it was for.
//
// An external identity matching more than one config item is treated as unattributable
// and falls back to the root: never guess a resource, never lose the money.
func resolveCostTarget(ctx api.ScrapeContext, cost *v1.ExternalCost, scraperID *uuid.UUID, memo configLookups) error {
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
	if lookup.ScraperID == "" {
		lookup.ScraperID = cost.ScraperID
	}

	if lookup.ExternalID != "" {
		for _, candidate := range costLookupCandidates(lookup) {
			ids, err := findConfigMatches(ctx.DB(), candidate, scraperID, memo)
			if err != nil {
				return fmt.Errorf("find config %s: %w", candidate.Pretty().ANSI(), err)
			}
			if len(ids) == 1 {
				cost.ConfigID = &ids[0]
				return nil
			}
			if len(ids) > 1 {
				// A less precise candidate cannot disambiguate an already ambiguous exact
				// identity, so stop here rather than letting the basename guess.
				ctx.Logger.V(3).Infof("cost reference %s matched %d configs; attributing to the root", candidate.Pretty().ANSI(), len(ids))
				return resolveCostRoot(ctx, cost, scraperID, memo)
			}
		}
	}

	return resolveCostRoot(ctx, cost, scraperID, memo)
}

// resolveCostRoot attributes the cost to the emitting scraper's root config item.
func resolveCostRoot(ctx api.ScrapeContext, cost *v1.ExternalCost, scraperID *uuid.UUID, memo configLookups) error {
	root := cost.RootConfigID
	if root.ConfigID != "" {
		id, err := uuid.Parse(strings.TrimSpace(root.ConfigID))
		if err != nil {
			return fmt.Errorf("invalid root_config_id.config_id: %w", err)
		}
		cost.ConfigID = &id
		return nil
	}
	if root.ExternalID == "" {
		return fmt.Errorf("cost has no resolvable config and the scraper supplied no root_config_id")
	}
	if root.ScraperID == "" {
		root.ScraperID = cost.ScraperID
	}

	ids, err := findConfigMatches(ctx.DB(), root, scraperID, memo)
	if err != nil {
		return fmt.Errorf("find root config %s: %w", root.Pretty().ANSI(), err)
	}
	switch len(ids) {
	case 0:
		return fmt.Errorf("root config %s has not been scraped yet", root.Pretty().ANSI())
	case 1:
		cost.ConfigID = &ids[0]
		return nil
	default:
		return fmt.Errorf("root config %s matched %d config items", root.Pretty().ANSI(), len(ids))
	}
}

// maxCostSkipCauses bounds how many distinct reasons are named before the rest are
// counted anonymously.
const maxCostSkipCauses = 5

// costSkips groups per-row failures by cause. A single missing root config produces one
// failure per row, so reporting them individually buries the job history under tens of
// thousands of identical lines and says nothing the count does not.
type costSkips struct {
	total  int
	counts map[string]int
	first  map[string]int
	order  []string
}

func (s *costSkips) add(index int, err error) {
	s.total++
	if s.counts == nil {
		s.counts, s.first = map[string]int{}, map[string]int{}
	}
	cause := err.Error()
	if _, seen := s.counts[cause]; !seen {
		if len(s.order) >= maxCostSkipCauses {
			return // counted in total, not named
		}
		s.order = append(s.order, cause)
		s.first[cause] = index
	}
	s.counts[cause]++
}

// err summarises the failures, naming each distinct cause once with how often it occurred
// and where it first did.
func (s *costSkips) err(scraped int) error {
	if s.total == 0 {
		return nil
	}
	named := 0
	parts := make([]string, 0, len(s.order)+1)
	for _, cause := range s.order {
		named += s.counts[cause]
		parts = append(parts, fmt.Sprintf("%s (x%d, first at cost %d)", cause, s.counts[cause], s.first[cause]))
	}
	if rest := s.total - named; rest > 0 {
		parts = append(parts, fmt.Sprintf("and %d more with other causes", rest))
	}
	return fmt.Errorf("skipped %d of %d external costs: %s", s.total, scraped, strings.Join(parts, "; "))
}

func saveExternalCosts(ctx api.ScrapeContext, costs []v1.ExternalCost, scraperID *uuid.UUID, summary *v1.ScrapeSummary) error {
	if len(costs) == 0 {
		return nil
	}
	summary.ExternalCosts.Scraped = len(costs)

	levels, err := ResolveCostLevels()
	if err != nil {
		return err
	}

	resolved := make([]v1.ExternalCost, 0, len(costs))
	memo := make(configLookups)
	var skips costSkips
	for i := range costs {
		cost := costs[i]
		skip := func(err error) {
			skips.add(i, err)
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
		if cost.ConfigExternalID.ScraperID == "" {
			cost.ConfigExternalID.ScraperID = cost.ScraperID
			if cost.ConfigExternalID.ScraperID == "" && scraperID != nil {
				cost.ConfigExternalID.ScraperID = scraperID.String()
			}
		}
		if err := resolveCostTarget(ctx, &cost, scraperID, memo); err != nil {
			skip(err)
			continue
		}
		resolved = append(resolved, cost)
	}

	// Every row failing to resolve looks the same from outside as never having scraped
	// any, so say which it was. The usual cause is a target whose config item does not
	// exist yet, which resolves itself once that scrape has run at least once.
	if len(resolved) == 0 {
		ctx.Warnf("external costs: %v", skips.err(len(costs)))
		return skips.err(len(costs))
	}

	saved, err := upsertConfigCosts(ctx, bucketCosts(resolved, scraperID, levels), scraperID)
	if err != nil {
		summary.ExternalCosts.Skipped += len(resolved)
		return errors.Join(skips.err(len(costs)),
			fmt.Errorf("failed to persist %d valid external costs: %w", len(resolved), err))
	}

	summary.ExternalCosts.Saved += saved

	if err := skips.err(len(costs)); err != nil {
		ctx.Warnf("external costs: %v", err)
		return err
	}
	return nil
}
