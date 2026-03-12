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

	resolvedUsers, skippedUsers, userMerges, err := resolveExternalUsers(ctx, extract.externalUsers, scraperID, now)
	if err != nil {
		return result, nil, err
	}
	result.Users.Skipped = skippedUsers

	resolvedGroups, skippedGroups, groupMerges, err := resolveExternalGroups(ctx, extract.externalGroups, scraperID, now)
	if err != nil {
		return result, nil, err
	}
	result.Groups.Skipped = skippedGroups

	resolvedRoles, skippedRoles, roleMerges, err := resolveExternalRoles(ctx, extract.externalRoles, scraperID, now)
	if err != nil {
		return result, nil, err
	}
	result.Roles.Skipped = skippedRoles

	// Combine all merges into a single idMap for user group resolution
	idMap := make(map[uuid.UUID]uuid.UUID)
	for k, v := range userMerges {
		idMap[k] = v
	}

	var resolvedUserGroups []dutyModels.ExternalUserGroup
	for _, ug := range extract.externalUserGroups {
		if savedID, ok := userMerges[ug.ExternalUserID]; ok {
			ug.ExternalUserID = savedID
		}
		if savedID, ok := groupMerges[ug.ExternalGroupID]; ok {
			ug.ExternalGroupID = savedID
		}
		if ug.ExternalUserID == uuid.Nil || ug.ExternalGroupID == uuid.Nil {
			ctx.Logger.Warnf("skipping external user group with nil user_id=%s or group_id=%s", ug.ExternalUserID, ug.ExternalGroupID)
			continue
		}
		resolvedUserGroups = append(resolvedUserGroups, ug)
	}

	counts, err := upsertExternalEntities(ctx, resolvedUsers, resolvedGroups, resolvedRoles, resolvedUserGroups, scraperID, userMerges, groupMerges, roleMerges)
	if err != nil {
		return result, nil, err
	}

	// Populate caches only after transaction has committed successfully.
	if scraperID != nil {
		// Evict loser IDs from caches
		for loserID := range userMerges {
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

func resolveExternalUsers(ctx api.ScrapeContext, users []dutyModels.ExternalUser, scraperID *uuid.UUID, now time.Time) ([]dutyModels.ExternalUser, int, map[uuid.UUID]uuid.UUID, error) {
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
				return nil, 0, nil, ctx.Oops().With("user", u.Name).Wrapf(err, "failed to generate id for external user")
			}
			u.ID = hid
		}
		if u.CreatedAt.IsZero() {
			u.CreatedAt = now
		}
		u.UpdatedAt = &now
		valid = append(valid, *u)
	}

	acc := entityAccessors[dutyModels.ExternalUser]{
		GetID:        func(u dutyModels.ExternalUser) uuid.UUID { return u.ID },
		SetID:        func(u *dutyModels.ExternalUser, id uuid.UUID) { u.ID = id },
		GetAliases:   func(u dutyModels.ExternalUser) []string { return u.Aliases },
		SetAliases:   func(u *dutyModels.ExternalUser, a []string) { u.Aliases = a },
		GetUpdatedAt: func(u dutyModels.ExternalUser) *time.Time { return u.UpdatedAt },
		MergeScalar: func(winner *dutyModels.ExternalUser, loser dutyModels.ExternalUser) {
			if loser.Name != "" {
				winner.Name = loser.Name
			}
			if loser.Email != nil {
				winner.Email = loser.Email
			}
			if loser.Tenant != "" {
				winner.Tenant = loser.Tenant
			}
			if loser.UserType != "" {
				winner.UserType = loser.UserType
			}
		},
	}

	merged, dbMerges, err := mergeByOverlappingAliases(ctx, valid, acc, func(aliases []string) ([]uuid.UUID, error) {
		return findAllExternalEntityIDsByAliases[dutyModels.ExternalUser](ctx, aliases)
	})
	if err != nil {
		return nil, 0, nil, err
	}

	return merged, skipped, dbMerges, nil
}

func resolveExternalGroups(ctx api.ScrapeContext, groups []dutyModels.ExternalGroup, scraperID *uuid.UUID, now time.Time) ([]dutyModels.ExternalGroup, int, map[uuid.UUID]uuid.UUID, error) {
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
				return nil, 0, nil, ctx.Oops().With("group", g.Name).Wrapf(err, "failed to generate id for external group")
			}
			g.ID = hid
		}
		if g.CreatedAt.IsZero() {
			g.CreatedAt = now
		}
		g.UpdatedAt = &now
		valid = append(valid, *g)
	}

	acc := entityAccessors[dutyModels.ExternalGroup]{
		GetID:        func(g dutyModels.ExternalGroup) uuid.UUID { return g.ID },
		SetID:        func(g *dutyModels.ExternalGroup, id uuid.UUID) { g.ID = id },
		GetAliases:   func(g dutyModels.ExternalGroup) []string { return g.Aliases },
		SetAliases:   func(g *dutyModels.ExternalGroup, a []string) { g.Aliases = a },
		GetUpdatedAt: func(g dutyModels.ExternalGroup) *time.Time { return g.UpdatedAt },
		MergeScalar: func(winner *dutyModels.ExternalGroup, loser dutyModels.ExternalGroup) {
			if loser.Name != "" {
				winner.Name = loser.Name
			}
			if loser.Tenant != "" {
				winner.Tenant = loser.Tenant
			}
			if loser.GroupType != "" {
				winner.GroupType = loser.GroupType
			}
		},
	}

	merged, dbMerges, err := mergeByOverlappingAliases(ctx, valid, acc, func(aliases []string) ([]uuid.UUID, error) {
		return findAllExternalEntityIDsByAliases[dutyModels.ExternalGroup](ctx, aliases)
	})
	if err != nil {
		return nil, 0, nil, err
	}

	return merged, skipped, dbMerges, nil
}

func resolveExternalRoles(ctx api.ScrapeContext, roles []dutyModels.ExternalRole, scraperID *uuid.UUID, now time.Time) ([]dutyModels.ExternalRole, int, map[uuid.UUID]uuid.UUID, error) {
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
				return nil, 0, nil, ctx.Oops().With("role", r.Name).Wrapf(err, "failed to generate id for external role")
			}
			r.ID = hid
		}
		if r.CreatedAt.IsZero() {
			r.CreatedAt = now
		}
		r.UpdatedAt = &now
		valid = append(valid, *r)
	}

	acc := entityAccessors[dutyModels.ExternalRole]{
		GetID:        func(r dutyModels.ExternalRole) uuid.UUID { return r.ID },
		SetID:        func(r *dutyModels.ExternalRole, id uuid.UUID) { r.ID = id },
		GetAliases:   func(r dutyModels.ExternalRole) []string { return r.Aliases },
		SetAliases:   func(r *dutyModels.ExternalRole, a []string) { r.Aliases = a },
		GetUpdatedAt: func(r dutyModels.ExternalRole) *time.Time { return r.UpdatedAt },
		MergeScalar: func(winner *dutyModels.ExternalRole, loser dutyModels.ExternalRole) {
			if loser.Name != "" {
				winner.Name = loser.Name
			}
			if loser.Tenant != "" {
				winner.Tenant = loser.Tenant
			}
			if loser.RoleType != "" {
				winner.RoleType = loser.RoleType
			}
			if loser.Description != "" {
				winner.Description = loser.Description
			}
		},
	}

	merged, dbMerges, err := mergeByOverlappingAliases(ctx, valid, acc, func(aliases []string) ([]uuid.UUID, error) {
		return findAllExternalEntityIDsByAliases[dutyModels.ExternalRole](ctx, aliases)
	})
	if err != nil {
		return nil, 0, nil, err
	}

	return merged, skipped, dbMerges, nil
}

type upsertCounts struct {
	usersSaved, usersDeleted   int
	groupsSaved, groupsDeleted int
	rolesSaved, rolesDeleted   int
}

func upsertExternalEntities(
	ctx api.ScrapeContext,
	users []dutyModels.ExternalUser,
	groups []dutyModels.ExternalGroup,
	roles []dutyModels.ExternalRole,
	userGroups []dutyModels.ExternalUserGroup,
	scraperID *uuid.UUID,
	userMerges, groupMerges, roleMerges map[uuid.UUID]uuid.UUID,
) (upsertCounts, error) {
	var counts upsertCounts
	if scraperID == nil {
		return counts, nil
	}

	scraperIDStr := scraperID.String()
	suffix := sanitizeForTempTable(scraperIDStr)

	tx := ctx.DB().Begin()
	if tx.Error != nil {
		return counts, fmt.Errorf("failed to begin transaction: %w", tx.Error)
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r) // Re-panic to propagate the error
		}
	}()

	tempUsers := fmt.Sprintf("_ext_users_%s", suffix)
	tempGroups := fmt.Sprintf("_ext_groups_%s", suffix)
	tempRoles := fmt.Sprintf("_ext_roles_%s", suffix)
	tempUserGroups := fmt.Sprintf("_ext_user_groups_%s", suffix)

	// Create temp tables for non-empty entity slices
	if len(users) > 0 {
		if err := createTempAndInsert(tx, tempUsers, "external_users", users); err != nil {
			tx.Rollback()
			return counts, fmt.Errorf("failed to setup temp users: %w", err)
		}
	}

	if len(groups) > 0 {
		if err := createTempAndInsert(tx, tempGroups, "external_groups", groups); err != nil {
			tx.Rollback()
			return counts, fmt.Errorf("failed to setup temp groups: %w", err)
		}
	}

	if len(roles) > 0 {
		if err := createTempAndInsert(tx, tempRoles, "external_roles", roles); err != nil {
			tx.Rollback()
			return counts, fmt.Errorf("failed to setup temp roles: %w", err)
		}
	}

	if len(userGroups) > 0 {
		if err := createTempAndInsert(tx, tempUserGroups, "external_user_groups", userGroups); err != nil {
			tx.Rollback()
			return counts, fmt.Errorf("failed to setup temp user groups: %w", err)
		}
	}

	// Apply merges: remap FKs and soft-delete losers before upserting
	if len(userMerges) > 0 {
		if err := mergeAwayExternalUsers(tx, userMerges); err != nil {
			tx.Rollback()
			return counts, fmt.Errorf("failed to merge external users: %w", err)
		}
	}
	if len(groupMerges) > 0 {
		if err := mergeAwayExternalGroups(tx, groupMerges); err != nil {
			tx.Rollback()
			return counts, fmt.Errorf("failed to merge external groups: %w", err)
		}
	}
	if len(roleMerges) > 0 {
		if err := mergeAwayExternalRoles(tx, roleMerges); err != nil {
			tx.Rollback()
			return counts, fmt.Errorf("failed to merge external roles: %w", err)
		}
	}

	// Upsert: users
	if len(users) > 0 {
		r := tx.Exec(fmt.Sprintf(`
			INSERT INTO external_users (id, aliases, name, account_id, user_type, email, scraper_id, created_at, updated_at, created_by)
			SELECT id, aliases, name, account_id, user_type, email, scraper_id, created_at, updated_at, created_by
			FROM %s
			ON CONFLICT (id) DO UPDATE SET
				aliases = NULLIF(ARRAY(SELECT DISTINCT unnest FROM unnest(external_users.aliases || EXCLUDED.aliases) ORDER BY 1), '{}'::text[]),
				name = EXCLUDED.name, account_id = EXCLUDED.account_id,
				user_type = EXCLUDED.user_type, email = EXCLUDED.email,
				updated_at = EXCLUDED.updated_at, deleted_at = NULL
		`, tempUsers))
		if r.Error != nil {
			tx.Rollback()
			return counts, fmt.Errorf("failed to upsert external users: %w", r.Error)
		}
		counts.usersSaved = int(r.RowsAffected)
	}

	// Upsert: groups
	if len(groups) > 0 {
		r := tx.Exec(fmt.Sprintf(`
			INSERT INTO external_groups (id, aliases, name, account_id, scraper_id, group_type, created_at, updated_at)
			SELECT id, aliases, name, account_id, scraper_id, group_type, created_at, updated_at
			FROM %s
			ON CONFLICT (id) DO UPDATE SET
				aliases = NULLIF(ARRAY(SELECT DISTINCT unnest FROM unnest(external_groups.aliases || EXCLUDED.aliases) ORDER BY 1), '{}'::text[]),
				name = EXCLUDED.name, account_id = EXCLUDED.account_id,
				group_type = EXCLUDED.group_type,
				updated_at = EXCLUDED.updated_at, deleted_at = NULL
		`, tempGroups))
		if r.Error != nil {
			tx.Rollback()
			return counts, fmt.Errorf("failed to upsert external groups: %w", r.Error)
		}
		counts.groupsSaved = int(r.RowsAffected)
	}

	// Upsert: roles
	if len(roles) > 0 {
		r := tx.Exec(fmt.Sprintf(`
			INSERT INTO external_roles (id, aliases, name, account_id, role_type, description, scraper_id, application_id, created_at, updated_at)
			SELECT id, aliases, name, account_id, role_type, description, scraper_id, application_id, created_at, updated_at
			FROM %s
			ON CONFLICT (id) DO UPDATE SET
				aliases = NULLIF(ARRAY(SELECT DISTINCT unnest FROM unnest(external_roles.aliases || EXCLUDED.aliases) ORDER BY 1), '{}'::text[]),
				name = EXCLUDED.name, account_id = EXCLUDED.account_id,
				role_type = EXCLUDED.role_type, description = EXCLUDED.description,
				updated_at = EXCLUDED.updated_at, deleted_at = NULL
		`, tempRoles))
		if r.Error != nil {
			tx.Rollback()
			return counts, fmt.Errorf("failed to upsert external roles: %w", r.Error)
		}
		counts.rolesSaved = int(r.RowsAffected)
	}

	// Upsert: user_groups
	if len(userGroups) > 0 {
		r := tx.Exec(fmt.Sprintf(`
			INSERT INTO external_user_groups (external_user_id, external_group_id, created_at)
			SELECT external_user_id, external_group_id, created_at
			FROM %s
			ON CONFLICT (external_user_id, external_group_id) DO UPDATE SET deleted_at = NULL
			WHERE external_user_groups.deleted_at IS NOT NULL
		`, tempUserGroups))
		if r.Error != nil {
			tx.Rollback()
			return counts, fmt.Errorf("failed to upsert external user groups: %w", r.Error)
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
				return counts, fmt.Errorf("failed to delete stale external user groups: %w", err)
			}
		} else if len(users) > 0 || len(groups) > 0 {
			if err := tx.Exec(`
				UPDATE external_user_groups SET deleted_at = NOW()
				WHERE deleted_at IS NULL
					AND external_user_id IN (SELECT id FROM external_users WHERE scraper_id = ?)
			`, *scraperID).Error; err != nil {
				tx.Rollback()
				return counts, fmt.Errorf("failed to delete stale external user groups: %w", err)
			}
		}
	}

	// FIXME: add stale deletion for external_users, external_groups, and external_roles

	if err := tx.Commit().Error; err != nil {
		return counts, fmt.Errorf("failed to commit external entities transaction: %w", err)
	}

	return counts, nil
}

// ensureExternalUserFromAliases creates a minimal external user from aliases if none exists.
// If multiple existing users match the aliases, they are merged first.
func ensureExternalUserFromAliases(ctx api.ScrapeContext, aliases []string, scraperID *uuid.UUID) error {
	sort.Strings(aliases)

	existingIDs, err := findAllExternalEntityIDsByAliases[dutyModels.ExternalUser](ctx, aliases)
	if err != nil {
		return fmt.Errorf("failed to find existing users by aliases: %w", err)
	}

	if len(existingIDs) > 1 {
		merges := make(map[uuid.UUID]uuid.UUID, len(existingIDs)-1)
		for _, loser := range existingIDs[1:] {
			merges[loser] = existingIDs[0]
		}
		tx := ctx.DB().Begin()
		if tx.Error != nil {
			return fmt.Errorf("failed to begin merge transaction: %w", tx.Error)
		}
		if err := mergeAwayExternalUsers(tx, merges); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to merge users: %w", err)
		}
		if err := tx.Commit().Error; err != nil {
			return fmt.Errorf("failed to commit merge: %w", err)
		}
	}

	var id uuid.UUID
	if len(existingIDs) > 0 {
		id = existingIDs[0]
	} else {
		id, err = hash.DeterministicUUID(aliases)
		if err != nil {
			return fmt.Errorf("failed to generate deterministic UUID: %w", err)
		}
	}

	now := time.Now()
	if err := ctx.DB().Exec(`
		INSERT INTO external_users (id, aliases, scraper_id, account_id, created_at, updated_at)
		VALUES (?, ?, ?, '', ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			aliases = NULLIF(ARRAY(SELECT DISTINCT unnest FROM unnest(external_users.aliases || EXCLUDED.aliases) ORDER BY 1), '{}'::text[]),
			updated_at = EXCLUDED.updated_at, deleted_at = NULL
	`, id, pq.StringArray(aliases), lo.FromPtr(scraperID), now, now).Error; err != nil {
		return fmt.Errorf("failed to upsert external user: %w", err)
	}
	ExternalUserIDCache.Set(id.String(), id, cache.DefaultExpiration)
	for _, alias := range aliases {
		ExternalUserCache.Set(alias, id, cache.DefaultExpiration)
	}
	return nil
}

// dedupeByID merges duplicate entities (by ID) keeping the first occurrence
// and appending unique aliases from later duplicates.
// Entities with a nil ID are passed through as-is.
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
