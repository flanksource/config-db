package db

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/flanksource/commons/hash"
	v1 "github.com/flanksource/config-db/api/v1"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/flanksource/config-db/api"
)

type externalEntitySyncResult struct {
	Users  v1.EntitySummary
	Groups v1.EntitySummary
	Roles  v1.EntitySummary
}

func syncExternalEntities(ctx api.ScrapeContext, extract *extractResult, scraperID *uuid.UUID) (externalEntitySyncResult, map[uuid.UUID]uuid.UUID, error) {
	var result externalEntitySyncResult
	result.Users.Scraped = len(extract.externalUsers)
	result.Groups.Scraped = len(extract.externalGroups)
	result.Roles.Scraped = len(extract.externalRoles)

	now := time.Now()

	resolvedUsers, skippedUsers, err := resolveExternalUsers(ctx, extract.externalUsers, scraperID, now)
	if err != nil {
		return result, nil, err
	}
	result.Users.Skipped = skippedUsers

	resolvedGroups, skippedGroups, err := resolveExternalGroups(ctx, extract.externalGroups, scraperID, now)
	if err != nil {
		return result, nil, err
	}
	result.Groups.Skipped = skippedGroups

	resolvedRoles, skippedRoles, err := resolveExternalRoles(ctx, extract.externalRoles, scraperID, now)
	if err != nil {
		return result, nil, err
	}
	result.Roles.Skipped = skippedRoles

	var resolvedUserGroups []dutyModels.ExternalUserGroup
	for _, ug := range extract.externalUserGroups {
		if ug.ExternalUserID == uuid.Nil || ug.ExternalGroupID == uuid.Nil {
			ctx.Logger.Warnf("skipping external user group with nil user_id=%s or group_id=%s", ug.ExternalUserID, ug.ExternalGroupID)
			continue
		}
		resolvedUserGroups = append(resolvedUserGroups, ug)
	}

	counts, idMap, err := upsertExternalEntities(ctx, resolvedUsers, resolvedGroups, resolvedRoles, resolvedUserGroups, scraperID)
	if err != nil {
		return result, nil, err
	}

	if scraperID != nil {
		for loserID := range idMap {
			ExternalUserIDCache.Delete(loserID.String())
		}
		for _, u := range resolvedUsers {
			ExternalUserIDCache.Set(u.ID.String(), u.ID, cache.DefaultExpiration)
			for _, alias := range u.Aliases {
				ExternalUserCache.Set(alias, u.ID, cache.DefaultExpiration)
			}
		}
		for _, g := range resolvedGroups {
			for _, alias := range g.Aliases {
				ExternalGroupCache.Set(alias, g.ID, cache.DefaultExpiration)
			}
		}
		for _, r := range resolvedRoles {
			for _, alias := range r.Aliases {
				ExternalRoleCache.Set(alias, r.ID, cache.DefaultExpiration)
			}
		}
	}

	result.Users.Saved = counts.usersSaved
	result.Users.Deleted = counts.usersDeleted
	result.Groups.Saved = counts.groupsSaved
	result.Groups.Deleted = counts.groupsDeleted
	result.Roles.Saved = counts.rolesSaved
	result.Roles.Deleted = counts.rolesDeleted
	return result, idMap, nil
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
		if u.ID != uuid.Nil {
			u.Aliases = appendUnique(u.Aliases, u.ID.String())
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
		if g.ID != uuid.Nil {
			g.Aliases = appendUnique(g.Aliases, g.ID.String())
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
		if r.ID != uuid.Nil {
			r.Aliases = appendUnique(r.Aliases, r.ID.String())
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

	// Call stored procedures that handle merge + upsert atomically
	if len(users) > 0 {
		var merges []struct {
			LoserID  uuid.UUID `gorm:"column:loser_id"`
			WinnerID uuid.UUID `gorm:"column:winner_id"`
		}
		if err := tx.Raw("SELECT * FROM merge_and_upsert_external_users(?)", tempUsers).Scan(&merges).Error; err != nil {
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
		if err := tx.Raw("SELECT * FROM merge_and_upsert_external_groups(?)", tempGroups).Scan(&merges).Error; err != nil {
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
		if err := tx.Raw("SELECT * FROM merge_and_upsert_external_roles(?)", tempRoles).Scan(&merges).Error; err != nil {
			tx.Rollback()
			return counts, nil, fmt.Errorf("failed to merge and upsert external roles: %w", err)
		}
		counts.rolesSaved = len(roles)
	}

	if len(userGroups) > 0 {
		remappedUserGroups := remapExternalUserGroups(userGroups, userIDMap, groupIDMap)
		if err := createTempAndInsert(tx, tempUserGroups, "external_user_groups", remappedUserGroups); err != nil {
			tx.Rollback()
			return counts, nil, fmt.Errorf("failed to setup temp user groups: %w", err)
		}
	}

	if len(userGroups) > 0 {
		r := tx.Exec(fmt.Sprintf(`
			INSERT INTO external_user_groups (external_user_id, external_group_id, created_at)
			SELECT external_user_id, external_group_id, created_at FROM %s
			ON CONFLICT (external_user_id, external_group_id) DO UPDATE SET deleted_at = NULL
			WHERE external_user_groups.deleted_at IS NOT NULL
		`, tempUserGroups))
		if r.Error != nil {
			tx.Rollback()
			return counts, nil, fmt.Errorf("failed to upsert external user groups: %w", r.Error)
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
				tx.Rollback()
				return counts, nil, fmt.Errorf("failed to delete stale external user groups: %w", err)
			}
		} else if len(users) > 0 || len(groups) > 0 {
			if err := tx.Exec(`
				UPDATE external_user_groups SET deleted_at = NOW()
				WHERE deleted_at IS NULL
					AND external_user_id IN (SELECT id FROM external_users WHERE scraper_id = ?)
			`, *scraperID).Error; err != nil {
				tx.Rollback()
				return counts, nil, fmt.Errorf("failed to delete stale external user groups: %w", err)
			}
		}
	}

	if err := tx.Commit().Error; err != nil {
		return counts, nil, fmt.Errorf("failed to commit external entities transaction: %w", err)
	}

	return counts, userIDMap, nil
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
	if err := tx.Raw("SELECT * FROM merge_and_upsert_external_users(?)", tempTable).Scan(&merges).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to merge and upsert: %w", err)
	}

	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	ExternalUserIDCache.Set(id.String(), id, cache.DefaultExpiration)
	for _, alias := range aliases {
		ExternalUserCache.Set(alias, id, cache.DefaultExpiration)
	}
	return nil
}

func dedupeByID[T any](
	items []T,
	getID func(T) uuid.UUID,
	getAliases func(T) []string,
	setAliases func(*T, []string),
) []T {
	seen := make(map[uuid.UUID]int)
	var out []T
	for _, item := range items {
		id := getID(item)
		if id == uuid.Nil {
			out = append(out, item)
			continue
		}
		if idx, exists := seen[id]; exists {
			for _, alias := range getAliases(item) {
				if !slices.Contains(getAliases(out[idx]), alias) {
					setAliases(&out[idx], append(getAliases(out[idx]), alias))
				}
			}
		} else {
			seen[id] = len(out)
			out = append(out, item)
		}
	}
	return out
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

func appendUnique(slice []string, item string) []string {
	if !slices.Contains(slice, item) {
		return append(slice, item)
	}
	return slice
}
