package db

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/flanksource/commons/hash"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/flanksource/config-db/api"
)

type externalEntitySyncResult struct {
	Users  v1.EntitySummary[dutyModels.ExternalUser]
	Groups v1.EntitySummary[dutyModels.ExternalGroup]
	Roles  v1.EntitySummary[dutyModels.ExternalRole]
}

// syncedExternalEntities carries the canonical, post-merge external entities
// out of syncExternalEntities so callers (notably SaveResults) can publish them
// back into the originating *v1.ScrapeResult slices via result.Resolved.
type syncedExternalEntities struct {
	// Users/Groups/Roles are post-resolve, post-merge: each entry's ID has
	// already been rewritten to the canonical winner ID returned by the SQL
	// merge_and_upsert_external_* functions.
	Users  []dutyModels.ExternalUser
	Groups []dutyModels.ExternalGroup
	Roles  []dutyModels.ExternalRole
	// UserIDMap and GroupIDMap are the raw loser→winner maps from the merge,
	// kept for callers (e.g. SaveResults remapping configAccess.ExternalUserID).
	UserIDMap  map[uuid.UUID]uuid.UUID
	GroupIDMap map[uuid.UUID]uuid.UUID
}

func syncExternalEntities(ctx api.ScrapeContext, extract *extractResult, scraperID *uuid.UUID) (externalEntitySyncResult, syncedExternalEntities, error) {
	var result externalEntitySyncResult
	result.Users.Scraped = len(extract.externalUsers)
	result.Groups.Scraped = len(extract.externalGroups)
	result.Roles.Scraped = len(extract.externalRoles)

	var synced syncedExternalEntities

	now := time.Now()

	resolvedUsers, skippedUsers, err := resolveExternalUsers(ctx, extract.externalUsers, scraperID, now)
	if err != nil {
		return result, synced, err
	}
	result.Users.Skipped = skippedUsers

	resolvedGroups, skippedGroups, err := resolveExternalGroups(ctx, extract.externalGroups, scraperID, now)
	if err != nil {
		return result, synced, err
	}
	result.Groups.Skipped = skippedGroups

	resolvedRoles, skippedRoles, err := resolveExternalRoles(ctx, extract.externalRoles, scraperID, now)
	if err != nil {
		return result, synced, err
	}
	result.Roles.Skipped = skippedRoles

	// Resolve v1.ExternalUserGroup linkages to models.ExternalUserGroup. Entries
	// with explicit UUIDs are passed through; alias-only entries (e.g. from the
	// Azure DevOps scraper, which describes identities by descriptor) are
	// resolved against the just-resolved users/groups via alias overlap.
	resolvedUserGroups := resolveExternalUserGroups(ctx, extract.externalUserGroups, resolvedUsers, resolvedGroups)

	counts, idMap, err := upsertExternalEntities(ctx, resolvedUsers, resolvedGroups, resolvedRoles, resolvedUserGroups, scraperID)
	if err != nil {
		return result, synced, err
	}

	if scraperID != nil {
		for loserID := range idMap {
			ExternalUserIDCache.Delete(loserID.String())
		}
		for _, u := range resolvedUsers {
			ExternalUserIDCache.Set(u.ID.String(), u.ID)
			for _, alias := range u.Aliases {
				ExternalUserCache.Set(alias, u.ID)
			}
		}
		for _, g := range resolvedGroups {
			ExternalGroupIDCache.Set(g.ID.String(), g.ID)
			for _, alias := range g.Aliases {
				ExternalGroupCache.Set(alias, g.ID)
			}
		}
		for _, r := range resolvedRoles {
			ExternalRoleIDCache.Set(r.ID.String(), r.ID)
			for _, alias := range r.Aliases {
				ExternalRoleCache.Set(alias, r.ID)
			}
		}
	}

	// Apply the merge winner-id rewrites to the resolved slices so SaveResults
	// can publish the canonical post-merge view back to result.Resolved.
	for i := range resolvedUsers {
		if winner, ok := idMap[resolvedUsers[i].ID]; ok {
			resolvedUsers[i].ID = winner
		}
	}

	synced = syncedExternalEntities{
		Users:     resolvedUsers,
		Groups:    resolvedGroups,
		Roles:     resolvedRoles,
		UserIDMap: idMap,
	}

	result.Users.Saved = counts.usersSaved
	result.Users.Deleted = counts.usersDeleted
	result.Users.Entities = resolvedUsers
	result.Groups.Saved = counts.groupsSaved
	result.Groups.Deleted = counts.groupsDeleted
	result.Groups.Entities = resolvedGroups
	result.Roles.Saved = counts.rolesSaved
	result.Roles.Deleted = counts.rolesDeleted
	result.Roles.Entities = resolvedRoles
	return result, synced, nil
}

func resolveExternalUsers(ctx api.ScrapeContext, users []dutyModels.ExternalUser, scraperID *uuid.UUID, now time.Time) ([]dutyModels.ExternalUser, int, error) {
	var valid []dutyModels.ExternalUser
	var skipped int

	for i := range users {
		u := &users[i]
		u.ScraperID = lo.Ternary(u.ScraperID == uuid.Nil, lo.FromPtr(scraperID), u.ScraperID)
		if u.ID == uuid.Nil && len(u.Aliases) == 0 {
			ctx.Logger.Warnf("skipping external user %q with no ID and no aliases", u.Name)
			skipped++
			continue
		}
		if u.ID == uuid.Nil {
			sort.Strings(u.Aliases)
			hid, err := hash.DeterministicUUID(u.Aliases)
			if err != nil {
				return nil, 0, ctx.Oops().With("user", u.Name).Wrapf(err, "failed to generate id for external user")
			}
			u.ID = hid
		}
		if u.CreatedAt.IsZero() {
			u.CreatedAt = now
		}
		u.UpdatedAt = &now
		valid = append(valid, *u)
	}

	return valid, skipped, nil
}

func resolveExternalGroups(ctx api.ScrapeContext, groups []dutyModels.ExternalGroup, scraperID *uuid.UUID, now time.Time) ([]dutyModels.ExternalGroup, int, error) {
	var valid []dutyModels.ExternalGroup
	var skipped int

	for i := range groups {
		g := &groups[i]
		g.ScraperID = lo.Ternary(g.ScraperID == uuid.Nil, lo.FromPtr(scraperID), g.ScraperID)
		if g.ID == uuid.Nil && len(g.Aliases) == 0 {
			ctx.Logger.Warnf("skipping external group %q with no ID and no aliases", g.Name)
			skipped++
			continue
		}
		if g.ID == uuid.Nil {
			sort.Strings(g.Aliases)
			hid, err := hash.DeterministicUUID(g.Aliases)
			if err != nil {
				return nil, 0, ctx.Oops().With("group", g.Name).Wrapf(err, "failed to generate id for external group")
			}
			g.ID = hid
		}
		if g.CreatedAt.IsZero() {
			g.CreatedAt = now
		}
		g.UpdatedAt = &now
		valid = append(valid, *g)
	}

	return valid, skipped, nil
}

// resolveExternalUserGroups converts v1.ExternalUserGroup entries (which may
// describe their user/group either by canonical UUID or by alias) into
// models.ExternalUserGroup entries with concrete UUIDs. Alias-only entries are
// matched against the alias lists of the supplied users/groups; entries that
// fail to resolve on either side are dropped with a warning.
func resolveExternalUserGroups(
	ctx api.ScrapeContext,
	in []v1.ExternalUserGroup,
	users []dutyModels.ExternalUser,
	groups []dutyModels.ExternalGroup,
) []dutyModels.ExternalUserGroup {
	if len(in) == 0 {
		return nil
	}

	// Build sets of valid IDs so direct-UUID references can be verified
	// against the actual entities we're about to upsert. A scraper might
	// emit an external_user_groups entry whose external_user_id / external_group_id
	// is a raw upstream UUID (e.g. Azure Graph group.id) that will NOT be the
	// final DB id because resolveExternalUsers / resolveExternalGroups synthesize
	// new hash-based IDs when the entity is pushed without its own `id` field.
	// Trusting the direct UUID blindly in that case produces a dangling FK and
	// the entire external-entity transaction rolls back.
	validUserIDs := make(map[uuid.UUID]struct{}, len(users))
	userByAlias := make(map[string]uuid.UUID, len(users)*2)
	for _, u := range users {
		validUserIDs[u.ID] = struct{}{}
		for _, a := range u.Aliases {
			userByAlias[a] = u.ID
		}
	}
	validGroupIDs := make(map[uuid.UUID]struct{}, len(groups))
	groupByAlias := make(map[string]uuid.UUID, len(groups)*2)
	for _, g := range groups {
		validGroupIDs[g.ID] = struct{}{}
		for _, a := range g.Aliases {
			groupByAlias[a] = g.ID
		}
	}

	// resolveID accepts a direct UUID only when it references an entity we
	// actually have. Otherwise it falls through to alias resolution.
	resolveID := func(direct *uuid.UUID, aliases []string, validIDs map[uuid.UUID]struct{}, lookup map[string]uuid.UUID) uuid.UUID {
		if direct != nil && *direct != uuid.Nil {
			if _, ok := validIDs[*direct]; ok {
				return *direct
			}
			// Direct UUID references an entity not in this scrape — try to
			// recover via the alias list before giving up.
		}
		for _, a := range aliases {
			if id, ok := lookup[a]; ok {
				return id
			}
		}
		return uuid.Nil
	}

	var droppedUnknownUser, droppedUnknownGroup int
	out := make([]dutyModels.ExternalUserGroup, 0, len(in))
	for _, ug := range in {
		userID := resolveID(ug.ExternalUserID, ug.ExternalUserAliases, validUserIDs, userByAlias)
		groupID := resolveID(ug.ExternalGroupID, ug.ExternalGroupAliases, validGroupIDs, groupByAlias)
		if userID == uuid.Nil {
			droppedUnknownUser++
			continue
		}
		if groupID == uuid.Nil {
			droppedUnknownGroup++
			continue
		}
		out = append(out, dutyModels.ExternalUserGroup{
			ExternalUserID:  userID,
			ExternalGroupID: groupID,
		})
	}
	if droppedUnknownUser > 0 || droppedUnknownGroup > 0 {
		ctx.Logger.Warnf(
			"dropped %d external_user_groups rows (unresolved user=%d, unresolved group=%d of %d input)",
			droppedUnknownUser+droppedUnknownGroup, droppedUnknownUser, droppedUnknownGroup, len(in),
		)
	}
	return out
}

func resolveExternalRoles(ctx api.ScrapeContext, roles []dutyModels.ExternalRole, scraperID *uuid.UUID, now time.Time) ([]dutyModels.ExternalRole, int, error) {
	var valid []dutyModels.ExternalRole
	var skipped int

	for i := range roles {
		r := &roles[i]
		r.ScraperID = lo.Ternary(r.ScraperID == nil, scraperID, r.ScraperID)
		if r.ID == uuid.Nil && len(r.Aliases) == 0 {
			ctx.Logger.Warnf("skipping external role %q with no ID and no aliases", r.Name)
			skipped++
			continue
		}
		if r.ID == uuid.Nil {
			sort.Strings(r.Aliases)
			hid, err := hash.DeterministicUUID(r.Aliases)
			if err != nil {
				return nil, 0, ctx.Oops().With("role", r.Name).Wrapf(err, "failed to generate id for external role")
			}
			r.ID = hid
		}
		if r.CreatedAt.IsZero() {
			r.CreatedAt = now
		}
		r.UpdatedAt = &now
		valid = append(valid, *r)
	}

	return valid, skipped, nil
}

type upsertCounts struct {
	usersSaved, usersDeleted   int
	groupsSaved, groupsDeleted int
	rolesSaved, rolesDeleted   int
}

func remapExternalUserGroups(userGroups []dutyModels.ExternalUserGroup, userIDMap, groupIDMap map[uuid.UUID]uuid.UUID) []dutyModels.ExternalUserGroup {
	if len(userGroups) == 0 || (len(userIDMap) == 0 && len(groupIDMap) == 0) {
		return userGroups
	}

	remapped := make([]dutyModels.ExternalUserGroup, len(userGroups))
	for i, ug := range userGroups {
		if winner, ok := userIDMap[ug.ExternalUserID]; ok {
			ug.ExternalUserID = winner
		}
		if winner, ok := groupIDMap[ug.ExternalGroupID]; ok {
			ug.ExternalGroupID = winner
		}
		remapped[i] = ug
	}

	return remapped
}

// liveTableForMergeFunc returns the live table name targeted by the named
// merge_and_upsert_external_* SQL function. Used by the dump helper to
// query overlapping live rows when the merge fails.
func liveTableForMergeFunc(funcName string) string {
	switch funcName {
	case "merge_and_upsert_external_users":
		return "external_users"
	case "merge_and_upsert_external_groups":
		return "external_groups"
	case "merge_and_upsert_external_roles":
		return "external_roles"
	default:
		return ""
	}
}

// runMergeFunctionWithDump invokes one of the merge_and_upsert_external_*
// SQL functions inside a savepoint. On error (e.g. a unique constraint
// violation), it rolls back to the savepoint, dumps the temp table contents
// AND any live rows that overlap with the temp table by alias or id, as
// JSON to a file under <cwd>/traces/, and logs the file path so the
// offending rows can be inspected. The original error is returned so the
// caller can decide whether to abort the outer transaction.
//
// The savepoint is necessary because Postgres aborts the transaction on
// error, making any further query on the same tx fail. Rolling back to a
// savepoint resets the abort state without losing the temp table (which is
// transaction-scoped via ON COMMIT DROP, so it survives savepoint rollback).
func runMergeFunctionWithDump(
	ctx api.ScrapeContext,
	tx *gorm.DB,
	funcName string,
	tempTable string,
	merges any,
) error {
	// Postgres caps identifiers at 63 bytes; keep the savepoint name short.
	savepoint := "sp_merge_" + sanitizeForTempTable(tempTable)
	if spErr := tx.Exec("SAVEPOINT " + savepoint).Error; spErr != nil {
		// If we can't even create the savepoint, just run the merge directly.
		return tx.Raw(fmt.Sprintf("SELECT * FROM %s(?::TEXT)", funcName), tempTable).Scan(merges).Error
	}

	mergeErr := tx.Raw(fmt.Sprintf("SELECT * FROM %s(?::TEXT)", funcName), tempTable).Scan(merges).Error
	if mergeErr == nil {
		tx.Exec("RELEASE SAVEPOINT " + savepoint) //nolint:errcheck
		return nil
	}

	// Roll back the savepoint so the temp table query below can run.
	if rbErr := tx.Exec("ROLLBACK TO SAVEPOINT " + savepoint).Error; rbErr != nil {
		ctx.Logger.Warnf("%s failed (%v) and savepoint rollback also failed (%v); cannot dump temp table", funcName, mergeErr, rbErr)
		return mergeErr
	}

	// Dump the temp table contents as JSON. Use jsonb_agg(to_jsonb(t)) to get
	// a single JSON array of all rows in one round-trip.
	var tempDump string
	tempDumpQuery := fmt.Sprintf("SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY t.id), '[]'::jsonb)::text FROM %s t", tempTable)
	if err := tx.Raw(tempDumpQuery).Scan(&tempDump).Error; err != nil {
		ctx.Logger.Warnf("%s failed (%v); failed to dump temp table %s: %v", funcName, mergeErr, tempTable, err)
		return mergeErr
	}

	// Also dump any live rows that overlap with the temp table — by alias OR
	// by id — including soft-deleted rows. This is the data needed to
	// diagnose temp↔live collisions that the merge function's edge build
	// missed (e.g. soft-deleted rows whose aliases collide with new temp
	// rows after the live row is resurrected by the ON CONFLICT branch).
	liveDump := "[]"
	if liveTable := liveTableForMergeFunc(funcName); liveTable != "" {
		liveDumpQuery := fmt.Sprintf(`
			SELECT COALESCE(jsonb_agg(to_jsonb(live) ORDER BY live.id), '[]'::jsonb)::text
			FROM %s live
			WHERE EXISTS (
				SELECT 1 FROM %s tmp
				WHERE tmp.id = live.id
					OR (live.aliases IS NOT NULL AND tmp.aliases IS NOT NULL AND live.aliases && tmp.aliases)
			)
		`, liveTable, tempTable)
		if err := tx.Raw(liveDumpQuery).Scan(&liveDump).Error; err != nil {
			ctx.Logger.Warnf("%s failed (%v); failed to dump overlapping live rows from %s: %v", funcName, mergeErr, liveTable, err)
			liveDump = fmt.Sprintf("\"<dump failed: %s>\"", err.Error())
		}
	}

	if path, writeErr := writeMergeFailureDump(funcName, tempTable, mergeErr, tempDump, liveDump); writeErr != nil {
		ctx.Logger.Warnf("%s failed: %v; failed to write dump file: %v", funcName, mergeErr, writeErr)
		ctx.Logger.Tracef("temp table %s contents: %s\noverlapping live rows: %s", tempTable, tempDump, liveDump)
	} else {
		ctx.Logger.Warnf("%s failed: %v — dumped %s rows + overlapping live rows to %s", funcName, mergeErr, tempTable, path)
	}
	return mergeErr
}

// writeMergeFailureDump writes the JSON dump of a failed merge's temp table
// and any overlapping live rows to <cwd>/traces/<funcName>-<timestamp>.json.
// Returns the absolute path of the written file.
func writeMergeFailureDump(funcName, tempTable string, mergeErr error, tempDump, liveDump string) (string, error) {
	dir := "traces"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create traces dir: %w", err)
	}

	ts := time.Now().Format("20060102-150405.000000000")
	name := fmt.Sprintf("%s-%s.json", funcName, ts)
	path := filepath.Join(dir, name)

	// Wrap the raw jsonb dumps with a small envelope so the file records the
	// failing function, the original error message, the temp table rows, and
	// any live rows whose id or aliases overlap with the temp table.
	envelope := fmt.Sprintf(`{
  "function": %q,
  "temp_table": %q,
  "error": %q,
  "rows": %s,
  "live_overlapping_rows": %s
}
`, funcName, tempTable, mergeErr.Error(), tempDump, liveDump)

	if err := os.WriteFile(path, []byte(envelope), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}

	abs, absErr := filepath.Abs(path)
	if absErr != nil {
		return path, nil
	}
	return abs, nil
}

func upsertExternalEntities(
	ctx api.ScrapeContext,
	users []dutyModels.ExternalUser,
	groups []dutyModels.ExternalGroup,
	roles []dutyModels.ExternalRole,
	userGroups []dutyModels.ExternalUserGroup,
	scraperID *uuid.UUID,
) (upsertCounts, map[uuid.UUID]uuid.UUID, error) {
	var counts upsertCounts
	userIDMap := make(map[uuid.UUID]uuid.UUID)
	groupIDMap := make(map[uuid.UUID]uuid.UUID)

	if scraperID == nil {
		return counts, userIDMap, nil
	}

	suffix := sanitizeForTempTable(scraperID.String())

	tx := ctx.DB().Begin()
	if tx.Error != nil {
		return counts, nil, fmt.Errorf("failed to begin transaction: %w", tx.Error)
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	// Apply any `postgres.session.*` properties as `SET LOCAL` settings so
	// values like `postgres.session.eu_debug.enabled=on` take effect for the
	// upcoming merge_and_upsert_external_* calls (their RAISE NOTICE output
	// is gated on that GUC).
	if err := duty.ApplySessionProperties(ctx.DutyContext(), tx); err != nil {
		tx.Rollback()
		return counts, nil, fmt.Errorf("failed to apply session properties: %w", err)
	}

	tempUsers := fmt.Sprintf("_ext_users_%s", suffix)
	tempGroups := fmt.Sprintf("_ext_groups_%s", suffix)
	tempRoles := fmt.Sprintf("_ext_roles_%s", suffix)
	tempUserGroups := fmt.Sprintf("_ext_user_groups_%s", suffix)

	if len(users) > 0 {
		if err := createTempAndInsert(tx, tempUsers, "external_users", users); err != nil {
			tx.Rollback()
			return counts, nil, fmt.Errorf("failed to setup temp users: %w", err)
		}
	}

	if len(groups) > 0 {
		if err := createTempAndInsert(tx, tempGroups, "external_groups", groups); err != nil {
			tx.Rollback()
			return counts, nil, fmt.Errorf("failed to setup temp groups: %w", err)
		}
	}

	if len(roles) > 0 {
		if err := createTempAndInsert(tx, tempRoles, "external_roles", roles); err != nil {
			tx.Rollback()
			return counts, nil, fmt.Errorf("failed to setup temp roles: %w", err)
		}
	}

	// Call stored procedures that handle merge + upsert atomically. Each
	// merge runs inside a savepoint so we can dump the temp table contents on
	// failure (e.g. unique-constraint violation on aliases) for diagnosis.
	if len(users) > 0 {
		var merges []struct {
			LoserID  uuid.UUID `gorm:"column:loser_id"`
			WinnerID uuid.UUID `gorm:"column:winner_id"`
		}
		if err := runMergeFunctionWithDump(ctx, tx, "merge_and_upsert_external_users", tempUsers, &merges); err != nil {
			tx.Rollback()
			return counts, nil, fmt.Errorf("failed to merge and upsert external users: %w", err)
		}
		for _, m := range merges {
			userIDMap[m.LoserID] = m.WinnerID
		}
		counts.usersSaved = len(users)
	}

	if len(groups) > 0 {
		var merges []struct {
			LoserID  uuid.UUID `gorm:"column:loser_id"`
			WinnerID uuid.UUID `gorm:"column:winner_id"`
		}
		if err := runMergeFunctionWithDump(ctx, tx, "merge_and_upsert_external_groups", tempGroups, &merges); err != nil {
			tx.Rollback()
			return counts, nil, fmt.Errorf("failed to merge and upsert external groups: %w", err)
		}
		for _, m := range merges {
			groupIDMap[m.LoserID] = m.WinnerID
		}
		counts.groupsSaved = len(groups)
	}

	if len(roles) > 0 {
		var merges []struct {
			LoserID  uuid.UUID `gorm:"column:loser_id"`
			WinnerID uuid.UUID `gorm:"column:winner_id"`
		}
		if err := runMergeFunctionWithDump(ctx, tx, "merge_and_upsert_external_roles", tempRoles, &merges); err != nil {
			tx.Rollback()
			return counts, nil, fmt.Errorf("failed to merge and upsert external roles: %w", err)
		}
		counts.rolesSaved = len(roles)
	}

	// The user_groups + stale-deletion block runs inside its own savepoint so
	// a failure here (most commonly a foreign-key violation from
	// scraper-emitted user/group refs that don't resolve to entities we
	// actually have) does NOT abort the already-successful user/group/role
	// upserts above. On failure we roll back just this savepoint, log a
	// warning, and fall through to tx.Commit() so the partial data still
	// lands in the DB.
	ugSavepoint := "sp_ext_user_groups_" + suffix
	if err := tx.Exec("SAVEPOINT " + ugSavepoint).Error; err != nil {
		ctx.Logger.Warnf("failed to create user_groups savepoint (%v); proceeding without isolation", err)
		ugSavepoint = ""
	}

	userGroupsErr := upsertExternalUserGroupsBlock(ctx, tx, tempUserGroups, userGroups, users, groups, userIDMap, groupIDMap, scraperID)
	if userGroupsErr != nil {
		if ugSavepoint != "" {
			if rbErr := tx.Exec("ROLLBACK TO SAVEPOINT " + ugSavepoint).Error; rbErr != nil {
				// If we can't even roll back the savepoint, the outer
				// transaction is hosed — abort everything.
				tx.Rollback()
				return counts, nil, fmt.Errorf("failed to upsert external user groups (%v) and savepoint rollback also failed: %w", userGroupsErr, rbErr)
			}
			ctx.Logger.Warnf("external_user_groups upsert failed, rolled back to savepoint so users/groups/roles still persist: %v", userGroupsErr)
		} else {
			// No savepoint to roll back to — the outer tx is aborted.
			tx.Rollback()
			return counts, nil, fmt.Errorf("failed to upsert external user groups: %w", userGroupsErr)
		}
	} else if ugSavepoint != "" {
		tx.Exec("RELEASE SAVEPOINT " + ugSavepoint) //nolint:errcheck
	}

	if err := tx.Commit().Error; err != nil {
		return counts, nil, fmt.Errorf("failed to commit external entities transaction: %w", err)
	}

	return counts, userIDMap, nil
}

// upsertExternalUserGroupsBlock runs the user_groups temp-table insert, the
// real-table upsert, and the stale-membership cleanup. Split out of
// upsertExternalEntities so the savepoint isolation logic in the caller stays
// readable — any error returned from here bubbles up and triggers a savepoint
// rollback rather than an outer-transaction rollback, preserving the already-
// committed user/group/role writes.
func upsertExternalUserGroupsBlock(
	ctx api.ScrapeContext,
	tx *gorm.DB,
	tempUserGroups string,
	userGroups []dutyModels.ExternalUserGroup,
	users []dutyModels.ExternalUser,
	groups []dutyModels.ExternalGroup,
	userIDMap map[uuid.UUID]uuid.UUID,
	groupIDMap map[uuid.UUID]uuid.UUID,
	scraperID *uuid.UUID,
) error {
	if len(userGroups) > 0 {
		remappedUserGroups := remapExternalUserGroups(userGroups, userIDMap, groupIDMap)
		if err := createTempAndInsert(tx, tempUserGroups, "external_user_groups", remappedUserGroups); err != nil {
			return fmt.Errorf("failed to setup temp user groups: %w", err)
		}

		r := tx.Exec(fmt.Sprintf(`
			INSERT INTO external_user_groups (external_user_id, external_group_id, created_at)
			SELECT external_user_id, external_group_id, created_at FROM %s
			ON CONFLICT (external_user_id, external_group_id) DO UPDATE SET deleted_at = NULL
			WHERE external_user_groups.deleted_at IS NOT NULL
		`, tempUserGroups))
		if r.Error != nil {
			return fmt.Errorf("failed to upsert external user groups: %w", r.Error)
		}
	}

	// Stale deletion: user_group memberships
	if !ctx.IsIncrementalScrape() {
		if len(userGroups) > 0 {
			if err := tx.Exec(fmt.Sprintf(`
				UPDATE external_user_groups SET deleted_at = NOW()
				WHERE deleted_at IS NULL
					AND external_user_id IN (SELECT id FROM external_users WHERE scraper_id = ?)
					AND NOT EXISTS (SELECT 1 FROM %s t
						WHERE t.external_user_id = external_user_groups.external_user_id
							AND t.external_group_id = external_user_groups.external_group_id)
			`, tempUserGroups), *scraperID).Error; err != nil {
				return fmt.Errorf("failed to delete stale external user groups: %w", err)
			}
		} else if len(users) > 0 || len(groups) > 0 {
			if err := tx.Exec(`
				UPDATE external_user_groups SET deleted_at = NOW()
				WHERE deleted_at IS NULL
					AND external_user_id IN (SELECT id FROM external_users WHERE scraper_id = ?)
			`, *scraperID).Error; err != nil {
				return fmt.Errorf("failed to delete stale external user groups: %w", err)
			}
		}
	}
	return nil
}

func ensureExternalUserFromAliases(ctx api.ScrapeContext, aliases []string, scraperID *uuid.UUID) error {
	sort.Strings(aliases)

	id, err := hash.DeterministicUUID(aliases)
	if err != nil {
		return fmt.Errorf("failed to generate deterministic UUID: %w", err)
	}

	now := time.Now()

	tx := ctx.DB().Begin()
	if tx.Error != nil {
		return fmt.Errorf("failed to begin transaction: %w", tx.Error)
	}

	tempTable := fmt.Sprintf("_ext_users_ensure_%s", sanitizeForTempTable(id.String()))
	if err := tx.Exec(fmt.Sprintf(`CREATE TEMP TABLE %s (LIKE external_users INCLUDING ALL) ON COMMIT DROP`, tempTable)).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to create temp table: %w", err)
	}

	if err := tx.Exec(fmt.Sprintf(`
		INSERT INTO %s (id, aliases, scraper_id, account_id, name, user_type, created_at, updated_at)
		VALUES (?, ?, ?, '', '', '', ?, ?)
	`, tempTable), id, pq.StringArray(aliases), lo.FromPtr(scraperID), now, now).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to insert into temp table: %w", err)
	}

	var merges []struct {
		LoserID  uuid.UUID `gorm:"column:loser_id"`
		WinnerID uuid.UUID `gorm:"column:winner_id"`
	}
	if err := tx.Raw("SELECT * FROM merge_and_upsert_external_users(?::TEXT)", tempTable).Scan(&merges).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to merge and upsert: %w", err)
	}

	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	ExternalUserIDCache.Set(id.String(), id)
	for _, alias := range aliases {
		ExternalUserCache.Set(alias, id)
	}
	return nil
}

func dedupeByID[T any](
	items []T,
	getID func(T) uuid.UUID,
	getAliases func(T) []string,
	setAliases func(*T, []string),
) []T {
	out, _ := dedupeByIDWithIndex(items, getID, getAliases, setAliases)
	return out
}

// dedupeByIDWithIndex behaves like dedupeByID but additionally returns a slice
// `indexMap` parallel to `items` where indexMap[i] is the position of the
// deduped survivor of items[i] in the returned slice.
//
// Entries with non-nil IDs are deduped by exact ID match.
// Entries with nil IDs (e.g. those from scrapers that intentionally do not
// synthesize IDs and rely on the SQL merge to assign canonical UUIDs) are
// deduped by alias overlap using union-find: any chain of items connected
// by shared aliases collapses into a single survivor entry whose alias set
// is the union of the chain. This is required because the upstream
// merge_and_upsert_external_* SQL functions enforce a unique constraint on
// the aliases column (partial index where deleted_at IS NULL), so distinct
// temp-table rows that share any alias would violate it.
func dedupeByIDWithIndex[T any](
	items []T,
	getID func(T) uuid.UUID,
	getAliases func(T) []string,
	setAliases func(*T, []string),
) ([]T, []int) {
	// First pass: group all items into clusters by alias overlap (union-find)
	// for nil-ID entries, and by exact ID match for non-nil entries.
	parent := make([]int, len(items))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}

	idToIdx := make(map[uuid.UUID]int)
	aliasToIdx := make(map[string]int)
	for i, item := range items {
		id := getID(item)
		if id != uuid.Nil {
			if existing, ok := idToIdx[id]; ok {
				union(existing, i)
			} else {
				idToIdx[id] = i
			}
		}
		// Index by alias too — overlapping aliases unite both nil-ID and
		// non-nil-ID entries (the merge SQL function would otherwise reject
		// them anyway because the aliases unique index spans the whole table).
		for _, alias := range getAliases(item) {
			if existing, ok := aliasToIdx[alias]; ok {
				union(existing, i)
			} else {
				aliasToIdx[alias] = i
			}
		}
	}

	// Second pass: pick a survivor per cluster. Prefer items with a non-nil
	// ID (they carry the authoritative canonical ID, e.g. an AAD-supplied
	// Azure object UUID) over nil-ID items from descriptor-only scrapers like
	// Azure DevOps. Fall back to the first item in the cluster.
	survivor := make(map[int]int) // root -> input index
	for i := range items {
		root := find(i)
		cur, ok := survivor[root]
		if !ok {
			survivor[root] = i
			continue
		}
		if getID(items[cur]) == uuid.Nil && getID(items[i]) != uuid.Nil {
			survivor[root] = i
		}
	}

	// Third pass: build the output, merging aliases from every cluster member
	// into its survivor.
	rootToOutIdx := make(map[int]int)
	var out []T
	indexMap := make([]int, len(items))
	for i := range items {
		root := find(i)
		survivorIdx := survivor[root]
		outIdx, ok := rootToOutIdx[root]
		if !ok {
			outIdx = len(out)
			rootToOutIdx[root] = outIdx
			out = append(out, items[survivorIdx])
		}
		// Merge this item's aliases into the survivor (if it isn't itself the
		// survivor entry which is already in `out`).
		if i != survivorIdx {
			for _, alias := range getAliases(items[i]) {
				if !slices.Contains(getAliases(out[outIdx]), alias) {
					setAliases(&out[outIdx], append(getAliases(out[outIdx]), alias))
				}
			}
		}
		indexMap[i] = outIdx
	}
	return out, indexMap
}

func createTempAndInsert[T any](tx *gorm.DB, tempTable, sourceTable string, items []T) error {
	if err := tx.Exec(fmt.Sprintf(`CREATE TEMP TABLE %s (LIKE %s INCLUDING ALL) ON COMMIT DROP`, tempTable, sourceTable)).Error; err != nil {
		return fmt.Errorf("failed to create temp table %s: %w", tempTable, err)
	}
	if err := tx.Table(tempTable).Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(items, 200).Error; err != nil {
		return fmt.Errorf("failed to insert into temp table %s: %w", tempTable, err)
	}
	return nil
}
