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

	resolvedUsers, skippedUsers, userIDMap, err := resolveExternalUsers(ctx, extract.externalUsers, scraperID, now)
	if err != nil {
		return result, nil, err
	}
	result.Users.Skipped = skippedUsers

	resolvedGroups, skippedGroups, groupIDMap, err := resolveExternalGroups(ctx, extract.externalGroups, scraperID, now)
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
		if savedID, ok := userIDMap[ug.ExternalUserID]; ok {
			ug.ExternalUserID = savedID
		}
		if savedID, ok := groupIDMap[ug.ExternalGroupID]; ok {
			ug.ExternalGroupID = savedID
		}
		if ug.ExternalUserID == uuid.Nil || ug.ExternalGroupID == uuid.Nil {
			ctx.Logger.Warnf("skipping external user group with nil user_id=%s or group_id=%s", ug.ExternalUserID, ug.ExternalGroupID)
			continue
		}
		resolvedUserGroups = append(resolvedUserGroups, ug)
	}

	counts, err := upsertExternalEntities(ctx, resolvedUsers, resolvedGroups, resolvedRoles, resolvedUserGroups, scraperID)
	if err != nil {
		return result, nil, err
	}

	// Populate caches only after transaction has committed successfully.
	// When scraperID is nil, upsertExternalEntities skips insertion,
	// so we must not cache IDs that don't exist in the DB.
	if scraperID != nil {
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
	return result, userIDMap, nil
}

func resolveExternalUsers(ctx api.ScrapeContext, users []dutyModels.ExternalUser, scraperID *uuid.UUID, now time.Time) ([]dutyModels.ExternalUser, int, map[uuid.UUID]uuid.UUID, error) {
	var resolved []dutyModels.ExternalUser
	var skipped int
	idMap := make(map[uuid.UUID]uuid.UUID)
	seen := make(map[uuid.UUID]int) // id -> index in resolved slice

	for _, u := range users {
		u.ScraperID = lo.Ternary(u.ScraperID == uuid.Nil, lo.FromPtr(scraperID), u.ScraperID)
		originalID := u.ID

		if u.ID == uuid.Nil && len(u.Aliases) == 0 {
			ctx.Logger.Warnf("skipping external user %q with no ID and no aliases", u.Name)
			skipped++
			continue
		}

		if len(u.Aliases) > 0 {
			sort.Strings(u.Aliases)
			existingID, err := findExternalEntityIDByAliases[dutyModels.ExternalUser](ctx, u.Aliases)
			if err != nil {
				return nil, 0, nil, ctx.Oops().With("aliases", u.Aliases).Wrapf(err, "failed to find external user by aliases")
			}
			if existingID != nil {
				if u.ID != uuid.Nil && u.ID != *existingID {
					u.Aliases = append(u.Aliases, u.ID.String())
				}
				u.ID = *existingID
			} else if u.ID == uuid.Nil {
				hid, err := hash.DeterministicUUID(u.Aliases)
				if err != nil {
					return nil, 0, nil, ctx.Oops().With("user", u.Name).Wrapf(err, "failed to generate id for external user")
				}
				u.ID = hid
			}
		}

		if u.CreatedAt.IsZero() {
			u.CreatedAt = now
		}
		u.UpdatedAt = &now

		// Deduplicate by resolved ID — merge aliases into first occurrence
		if idx, exists := seen[u.ID]; exists {
			for _, alias := range u.Aliases {
				if !slices.Contains(resolved[idx].Aliases, alias) {
					resolved[idx].Aliases = append(resolved[idx].Aliases, alias)
				}
			}
		} else {
			seen[u.ID] = len(resolved)
			resolved = append(resolved, u)
		}

		if originalID != uuid.Nil && originalID != u.ID {
			idMap[originalID] = u.ID
		}
	}
	return resolved, skipped, idMap, nil
}

func resolveExternalGroups(ctx api.ScrapeContext, groups []dutyModels.ExternalGroup, scraperID *uuid.UUID, now time.Time) ([]dutyModels.ExternalGroup, int, map[uuid.UUID]uuid.UUID, error) {
	var resolved []dutyModels.ExternalGroup
	var skipped int
	idMap := make(map[uuid.UUID]uuid.UUID)

	for _, g := range groups {
		g.ScraperID = lo.Ternary(g.ScraperID == uuid.Nil, lo.FromPtr(scraperID), g.ScraperID)
		originalID := g.ID

		if g.ID == uuid.Nil && len(g.Aliases) == 0 {
			ctx.Logger.Warnf("skipping external group %q with no ID and no aliases", g.Name)
			skipped++
			continue
		}

		if len(g.Aliases) > 0 {
			existingID, err := findExternalEntityIDByAliases[dutyModels.ExternalGroup](ctx, g.Aliases)
			if err != nil {
				return nil, 0, nil, ctx.Oops().With("aliases", g.Aliases).Wrapf(err, "failed to find external group by aliases")
			}
			if existingID != nil {
				g.ID = *existingID
			} else if g.ID == uuid.Nil {
				hid, err := hash.DeterministicUUID(g.Aliases)
				if err != nil {
					return nil, 0, nil, ctx.Oops().With("group", g.Name).Wrapf(err, "failed to generate id for external group")
				}
				g.ID = hid
			}
		}

		if g.CreatedAt.IsZero() {
			g.CreatedAt = now
		}
		g.UpdatedAt = &now
		resolved = append(resolved, g)
		if originalID != uuid.Nil && originalID != g.ID {
			idMap[originalID] = g.ID
		}
	}
	return resolved, skipped, idMap, nil
}

func resolveExternalRoles(ctx api.ScrapeContext, roles []dutyModels.ExternalRole, scraperID *uuid.UUID, now time.Time) ([]dutyModels.ExternalRole, int, error) {
	var resolved []dutyModels.ExternalRole
	var skipped int

	for _, r := range roles {
		r.ScraperID = lo.Ternary(r.ScraperID == nil, scraperID, r.ScraperID)

		if r.ID == uuid.Nil && len(r.Aliases) == 0 {
			ctx.Logger.Warnf("skipping external role %q with no ID and no aliases", r.Name)
			skipped++
			continue
		}

		if len(r.Aliases) > 0 {
			existingID, err := findExternalEntityIDByAliases[dutyModels.ExternalRole](ctx, r.Aliases)
			if err != nil {
				return nil, 0, ctx.Oops().With("aliases", r.Aliases).Wrapf(err, "failed to find external role by aliases")
			}
			if existingID != nil {
				r.ID = *existingID
			} else if r.ID == uuid.Nil {
				hid, err := hash.DeterministicUUID(r.Aliases)
				if err != nil {
					return nil, 0, ctx.Oops().With("role", r.Name).Wrapf(err, "failed to generate id for external role")
				}
				r.ID = hid
			}
		}

		if r.CreatedAt.IsZero() {
			r.CreatedAt = now
		}
		r.UpdatedAt = &now
		resolved = append(resolved, r)
	}
	return resolved, skipped, nil
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

	// Upsert: users
	if len(users) > 0 {
		r := tx.Exec(fmt.Sprintf(`
			INSERT INTO external_users (id, aliases, name, account_id, user_type, email, scraper_id, created_at, updated_at, created_by)
			SELECT id, aliases, name, account_id, user_type, email, scraper_id, created_at, updated_at, created_by
			FROM %s
			ON CONFLICT (id) DO UPDATE SET
				aliases = ARRAY(SELECT DISTINCT unnest FROM unnest(external_users.aliases || EXCLUDED.aliases) ORDER BY 1),
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
				aliases = EXCLUDED.aliases, name = EXCLUDED.name, account_id = EXCLUDED.account_id,
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
				aliases = EXCLUDED.aliases, name = EXCLUDED.name, account_id = EXCLUDED.account_id,
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

	// Stale deletion: user_groups first (FK dependency)
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
		// No user groups scraped but we have users/groups — delete all user_groups for this scraper's users
		if err := tx.Exec(`
			UPDATE external_user_groups SET deleted_at = NOW()
			WHERE deleted_at IS NULL
				AND external_user_id IN (SELECT id FROM external_users WHERE scraper_id = ?)
		`, *scraperID).Error; err != nil {
			tx.Rollback()
			return counts, fmt.Errorf("failed to delete stale external user groups: %w", err)
		}
	}

	// Stale deletion: roles (hard delete, preserving current behavior)
	if len(roles) > 0 {
		r := tx.Exec(fmt.Sprintf(`
			DELETE FROM external_roles WHERE scraper_id = ?
				AND NOT EXISTS (SELECT 1 FROM %s t WHERE t.id = external_roles.id)
		`, tempRoles), *scraperID)
		if r.Error != nil {
			tx.Rollback()
			return counts, fmt.Errorf("failed to delete stale external roles: %w", r.Error)
		}
		counts.rolesDeleted = int(r.RowsAffected)
	}

	// Stale deletion: groups (soft delete)
	if len(groups) > 0 {
		r := tx.Exec(fmt.Sprintf(`
			UPDATE external_groups SET deleted_at = NOW()
			WHERE scraper_id = ? AND deleted_at IS NULL
				AND NOT EXISTS (SELECT 1 FROM %s t WHERE t.id = external_groups.id)
		`, tempGroups), *scraperID)
		if r.Error != nil {
			tx.Rollback()
			return counts, fmt.Errorf("failed to delete stale external groups: %w", r.Error)
		}
		counts.groupsDeleted = int(r.RowsAffected)
	}

	// Stale deletion: users (soft delete)
	if len(users) > 0 {
		r := tx.Exec(fmt.Sprintf(`
			UPDATE external_users SET deleted_at = NOW()
			WHERE scraper_id = ? AND deleted_at IS NULL
				AND NOT EXISTS (SELECT 1 FROM %s t WHERE t.id = external_users.id)
		`, tempUsers), *scraperID)
		if r.Error != nil {
			tx.Rollback()
			return counts, fmt.Errorf("failed to delete stale external users: %w", r.Error)
		}
		counts.usersDeleted = int(r.RowsAffected)
	}

	if err := tx.Commit().Error; err != nil {
		return counts, fmt.Errorf("failed to commit external entities transaction: %w", err)
	}

	return counts, nil
}

// ensureExternalUserFromAliases creates a minimal external user from aliases if none exists.
func ensureExternalUserFromAliases(ctx api.ScrapeContext, aliases []string, scraperID *uuid.UUID) error {
	sort.Strings(aliases)
	id, err := hash.DeterministicUUID(aliases)
	if err != nil {
		return fmt.Errorf("failed to generate deterministic UUID: %w", err)
	}
	now := time.Now()
	user := dutyModels.ExternalUser{
		ID:        id,
		Aliases:   aliases,
		ScraperID: lo.FromPtr(scraperID),
		CreatedAt: now,
		UpdatedAt: &now,
	}
	if err := ctx.DB().Exec(`
		INSERT INTO external_users (id, aliases, scraper_id, account_id, created_at, updated_at)
		VALUES (?, ?, ?, '', ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			aliases = ARRAY(SELECT DISTINCT unnest FROM unnest(external_users.aliases || EXCLUDED.aliases) ORDER BY 1),
			updated_at = EXCLUDED.updated_at, deleted_at = NULL
	`, user.ID, pq.StringArray(user.Aliases), user.ScraperID, user.CreatedAt, user.UpdatedAt).Error; err != nil {
		return fmt.Errorf("failed to upsert external user: %w", err)
	}
	ExternalUserIDCache.Set(user.ID.String(), user.ID, cache.DefaultExpiration)
	for _, alias := range aliases {
		ExternalUserCache.Set(alias, user.ID, cache.DefaultExpiration)
	}
	return nil
}

func createTempAndInsert[T any](tx *gorm.DB, tempTable, sourceTable string, items []T) error {
	if err := tx.Exec(fmt.Sprintf(`CREATE TEMP TABLE %s (LIKE %s INCLUDING ALL) ON COMMIT DROP`, tempTable, sourceTable)).Error; err != nil {
		return fmt.Errorf("failed to create temp table %s: %w", tempTable, err)
	}
	if err := tx.Table(tempTable).CreateInBatches(items, 200).Error; err != nil {
		return fmt.Errorf("failed to insert into temp table %s: %w", tempTable, err)
	}
	return nil
}
