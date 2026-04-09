package db

import (
	"fmt"
	"time"

	"github.com/flanksource/commons/hash"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
	dutydb "github.com/flanksource/duty/db"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/flanksource/config-db/api"
)

type permissionChangeResult struct {
	added            []*models.ConfigChange
	removed          []*models.ConfigChange
	saved            int
	foreignKeyErrors int
}

type staleAccessResult struct {
	ConfigID  string
	UserName  string
	RoleName  string
	GroupName string
}

func upsertConfigAccess(ctx api.ScrapeContext, accesses []v1.ExternalConfigAccess, scraperID *uuid.UUID) (permissionChangeResult, error) {
	var result permissionChangeResult
	if scraperID == nil {
		return result, nil
	}

	now := time.Now()
	scraperIDStr := scraperID.String()
	tempTable := fmt.Sprintf("_scrape_config_access_%s", sanitizeForTempTable(scraperIDStr))

	tx := ctx.DB().Begin()
	if tx.Error != nil {
		return result, fmt.Errorf("failed to begin transaction: %w", tx.Error)
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r) // Re-panic to propagate the error
		}
	}()

	seen := make(map[string]struct{})
	var items []dutyModels.ConfigAccess
	for _, ca := range accesses {
		if ca.ID == "" {
			hid, err := deterministicAccessID(ca.ToConfigAccess())
			if err != nil {
				tx.Rollback()
				return result, fmt.Errorf("failed to generate config access id: %w", err)
			}
			ca.ID = hid
		}

		if _, ok := seen[ca.ID]; ok {
			continue
		}
		seen[ca.ID] = struct{}{}

		items = append(items, ca.ToConfigAccess())
	}
	result.saved = len(items)

	if err := createTempAndInsert(tx, tempTable, "config_access", items); err != nil {
		tx.Rollback()
		return result, fmt.Errorf("failed to setup temp config access: %w", err)
	}

	// Insert stub entities for any missing user/role/group references so the
	// config_access FK insert doesn't fail. Each stub insert runs inside a
	// savepoint so an error doesn't abort the outer transaction.
	//
	// Notes on the SQL below:
	//   - scraper_id uses `?::uuid` for all three tables. external_roles.scraper_id
	//     is nominally nullable, but its check constraint
	//     (application_id IS NOT NULL OR scraper_id IS NOT NULL) forces one to be set.
	//   - aliases is inserted as NULL, not ARRAY[]::text[], because all three
	//     tables have a partial unique index on aliases WHERE deleted_at IS NULL,
	//     and an empty array would collide across multiple stub rows. NULLs are
	//     distinct under that index.
	//   - ON CONFLICT DO NOTHING (no target) so any future unique index, not just
	//     the PK, is tolerated.
	for _, stub := range []struct {
		entity    string
		table     string
		column    string
		extraCols string
		extraVals string
	}{
		{
			entity: "user", table: "external_users", column: "external_user_id",
			extraCols: ", account_id, user_type", extraVals: ", '', 'Stub'",
		},
		{
			entity: "role", table: "external_roles", column: "external_role_id",
			extraCols: ", account_id, role_type, description", extraVals: ", '', 'Stub', ''",
		},
		{
			entity: "group", table: "external_groups", column: "external_group_id",
			extraCols: ", account_id, group_type", extraVals: ", '', 'Stub'",
		},
	} {
		stubSQL := fmt.Sprintf(`
			INSERT INTO %s (id, name, aliases, scraper_id, created_at, updated_at %s)
			SELECT DISTINCT t.%s, t.%s::text, NULL::text[], ?::uuid, ?::timestamptz, ?::timestamptz %s
			FROM %s t
			WHERE t.%s IS NOT NULL
			  AND NOT EXISTS (SELECT 1 FROM %s e WHERE e.id = t.%s)
			ON CONFLICT DO NOTHING
		`, stub.table, stub.extraCols,
			stub.column, stub.column, stub.extraVals,
			tempTable,
			stub.column, stub.table, stub.column)

		savepoint := fmt.Sprintf("stub_%s", stub.entity)
		if err := tx.Exec("SAVEPOINT " + savepoint).Error; err != nil {
			ctx.Logger.Warnf("failed to create savepoint for stub %s: %v", stub.entity, err)
			continue
		}

		r := tx.Exec(stubSQL, scraperID.String(), now, now)
		if r.Error != nil {
			ctx.Logger.Warnf("failed to create stub %ss: %v", stub.entity, r.Error)
			tx.Exec("ROLLBACK TO SAVEPOINT " + savepoint)
			continue
		}
		tx.Exec("RELEASE SAVEPOINT " + savepoint)
		if r.RowsAffected > 0 {
			ctx.Logger.Warnf("created %d stub %s(s) for missing config_access references", r.RowsAffected, stub.entity)
		}
	}

	// Upsert: insert new records, restore soft-deleted ones
	if len(items) > 0 {
		var newRows []struct {
			ID              string
			ConfigID        uuid.UUID
			ExternalUserID  *uuid.UUID
			ExternalRoleID  *uuid.UUID
			ExternalGroupID *uuid.UUID
		}
		newSQL := fmt.Sprintf(`
			INSERT INTO config_access (id, config_id, external_user_id, external_role_id,
				external_group_id, scraper_id, application_id, source, created_at)
			SELECT t.id, t.config_id, t.external_user_id, t.external_role_id,
				t.external_group_id, t.scraper_id, t.application_id, t.source, t.created_at
			FROM %s t
			ON CONFLICT (id) DO UPDATE SET deleted_at = NULL
			WHERE config_access.deleted_at IS NOT NULL
			RETURNING id, config_id, external_user_id, external_role_id, external_group_id
		`, tempTable)

		tx.Exec("SAVEPOINT bulk_insert")

		if err := tx.Raw(newSQL).Scan(&newRows).Error; err != nil {
			if !dutydb.IsForeignKeyError(err) {
				tx.Rollback()
				return result, fmt.Errorf("failed to upsert config access: %w", err)
			}

			// Restore transaction state; temp table is preserved.
			tx.Exec("ROLLBACK TO SAVEPOINT bulk_insert")

			// Row-by-row with exception handling: successfully inserted rows
			// are deleted from the temp table; FK violations stay.
			fallbackSQL := fmt.Sprintf(`
				DO $$
				DECLARE
					v_rec RECORD;
				BEGIN
					FOR v_rec IN SELECT * FROM %s LOOP
						BEGIN
							INSERT INTO config_access (id, config_id, external_user_id, external_role_id,
								external_group_id, scraper_id, application_id, source, created_at)
							VALUES (v_rec.id, v_rec.config_id, v_rec.external_user_id, v_rec.external_role_id,
								v_rec.external_group_id, v_rec.scraper_id, v_rec.application_id, v_rec.source, v_rec.created_at)
							ON CONFLICT (id) DO UPDATE SET deleted_at = NULL
							WHERE config_access.deleted_at IS NOT NULL;
							DELETE FROM %s WHERE id = v_rec.id;
						EXCEPTION WHEN foreign_key_violation THEN
							NULL;
						END;
					END LOOP;
				END $$;
			`, tempTable, tempTable)

			if err := tx.Exec(fallbackSQL).Error; err != nil {
				tx.Rollback()
				return result, fmt.Errorf("failed to fallback upsert config access: %w", err)
			}

			var fkErrorCount int64
			tx.Raw(fmt.Sprintf("SELECT count(*) FROM %s", tempTable)).Scan(&fkErrorCount)
			result.foreignKeyErrors = int(fkErrorCount)
			result.saved -= result.foreignKeyErrors

			if fkErrorCount > 0 {
				ctx.Logger.Warnf("config_access: %d rows with FK violations (scraper=%s)", fkErrorCount, scraperIDStr)
				logFKDiagnostics(ctx, tx, tempTable)
			}
		}

		for _, row := range newRows {
			summary := buildPermissionSummary(tx, v1.ChangeTypePermissionAdded, row.ExternalUserID, row.ExternalRoleID, row.ExternalGroupID)
			result.added = append(result.added, &models.ConfigChange{
				ID:         uuid.NewString(),
				ConfigID:   row.ConfigID.String(),
				ChangeType: v1.ChangeTypePermissionAdded,
				Summary:    summary,
				CreatedAt:  now,
			})
		}
	}

	// Soft-delete stale records not in current scrape.
	// Skip during incremental scrapes because the batch is partial and
	// would incorrectly remove records that simply weren't in this batch.
	if !ctx.IsIncrementalScrape() {
		var staleRows []staleAccessResult
		staleSQL := fmt.Sprintf(`
			WITH stale AS (
				UPDATE config_access
				SET deleted_at = NOW()
				WHERE scraper_id = ?
					AND deleted_at IS NULL
					AND NOT EXISTS (
						SELECT 1 FROM %s t
						WHERE t.config_id = config_access.config_id
							AND t.scraper_id = config_access.scraper_id
							AND t.external_user_id IS NOT DISTINCT FROM config_access.external_user_id
							AND t.external_role_id IS NOT DISTINCT FROM config_access.external_role_id
							AND t.external_group_id IS NOT DISTINCT FROM config_access.external_group_id
					)
				RETURNING config_access.config_id, config_access.external_user_id, config_access.external_role_id, config_access.external_group_id
			)
			SELECT s.config_id,
				COALESCE(eu.name, '') as user_name,
				COALESCE(er.name, '') as role_name,
				COALESCE(eg.name, '') as group_name
			FROM stale s
			LEFT JOIN external_users eu ON s.external_user_id = eu.id
			LEFT JOIN external_roles er ON s.external_role_id = er.id
			LEFT JOIN external_groups eg ON s.external_group_id = eg.id
		`, tempTable)

		if err := tx.Raw(staleSQL, *scraperID).Scan(&staleRows).Error; err != nil {
			tx.Rollback()
			return result, fmt.Errorf("failed to delete stale config access: %w", err)
		}

		for _, row := range staleRows {
			result.removed = append(result.removed, &models.ConfigChange{
				ID:         uuid.NewString(),
				ConfigID:   row.ConfigID,
				ChangeType: v1.ChangeTypePermissionRemoved,
				Summary:    buildRemovalSummary(row),
				CreatedAt:  now,
			})
		}
	}

	if err := tx.Commit().Error; err != nil {
		return result, fmt.Errorf("failed to commit config access transaction: %w", err)
	}

	return result, nil
}

func deterministicAccessID(ca dutyModels.ConfigAccess) (string, error) {
	hid, err := hash.DeterministicUUID([]string{
		ca.ConfigID.String(),
		uuidPtrStr(ca.ExternalUserID),
		uuidPtrStr(ca.ExternalRoleID),
		uuidPtrStr(ca.ExternalGroupID),
		uuidPtrStr(ca.ScraperID),
	})
	if err != nil {
		return "", err
	}
	return hid.String(), nil
}

func uuidPtrStr(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func buildPermissionSummary(tx *gorm.DB, changeType string, userID, roleID, groupID *uuid.UUID) string {
	parts := []string{}
	if userID != nil {
		var name string
		if err := tx.Model(&dutyModels.ExternalUser{}).Select("name").Where("id = ?", *userID).Scan(&name).Error; err == nil && name != "" {
			parts = append(parts, fmt.Sprintf("user %s", name))
		}
	}
	if roleID != nil {
		var name string
		if err := tx.Model(&dutyModels.ExternalRole{}).Select("name").Where("id = ?", *roleID).Scan(&name).Error; err == nil && name != "" {
			parts = append(parts, fmt.Sprintf("role %s", name))
		}
	}
	if groupID != nil {
		var name string
		if err := tx.Model(&dutyModels.ExternalGroup{}).Select("name").Where("id = ?", *groupID).Scan(&name).Error; err == nil && name != "" {
			parts = append(parts, fmt.Sprintf("group %s", name))
		}
	}
	if len(parts) == 0 {
		return changeType
	}
	return fmt.Sprintf("%s: %s", changeType, joinParts(parts))
}

func buildRemovalSummary(row staleAccessResult) string {
	parts := []string{}
	if row.UserName != "" {
		parts = append(parts, fmt.Sprintf("user %s", row.UserName))
	}
	if row.RoleName != "" {
		parts = append(parts, fmt.Sprintf("role %s", row.RoleName))
	}
	if row.GroupName != "" {
		parts = append(parts, fmt.Sprintf("group %s", row.GroupName))
	}
	if len(parts) == 0 {
		return v1.ChangeTypePermissionRemoved
	}
	return fmt.Sprintf("%s: %s", v1.ChangeTypePermissionRemoved, joinParts(parts))
}

func joinParts(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += ", " + parts[i]
	}
	return result
}

func logFKDiagnostics(ctx api.ScrapeContext, tx *gorm.DB, tempTable string) {
	type diagRow struct {
		ExternalUserID  *uuid.UUID `gorm:"column:external_user_id"`
		ExternalGroupID *uuid.UUID `gorm:"column:external_group_id"`
		ExternalRoleID  *uuid.UUID `gorm:"column:external_role_id"`
		Reason          string     `gorm:"column:reason"`
	}

	diagSQL := fmt.Sprintf(`
		SELECT t.external_user_id, t.external_group_id, t.external_role_id,
			CASE
				WHEN t.external_user_id IS NOT NULL AND eu.id IS NULL THEN 'user_missing'
				WHEN t.external_user_id IS NOT NULL AND eu.deleted_at IS NOT NULL THEN 'user_deleted'
				WHEN t.external_group_id IS NOT NULL AND eg.id IS NULL THEN 'group_missing'
				WHEN t.external_group_id IS NOT NULL AND eg.deleted_at IS NOT NULL THEN 'group_deleted'
				WHEN t.external_role_id IS NOT NULL AND er.id IS NULL THEN 'role_missing'
				WHEN t.external_role_id IS NOT NULL AND er.deleted_at IS NOT NULL THEN 'role_deleted'
				WHEN NOT EXISTS (SELECT 1 FROM config_items ci WHERE ci.id = t.config_id) THEN 'config_missing'
				ELSE 'unknown'
			END AS reason
		FROM %s t
		LEFT JOIN external_users eu ON eu.id = t.external_user_id
		LEFT JOIN external_groups eg ON eg.id = t.external_group_id
		LEFT JOIN external_roles er ON er.id = t.external_role_id
		LIMIT 100
	`, tempTable)

	var rows []diagRow
	if err := tx.Raw(diagSQL).Scan(&rows).Error; err != nil {
		ctx.Logger.Warnf("  failed to diagnose FK errors: %v", err)
		return
	}

	type groupKey struct {
		Reason string
		FKID   string
	}
	counts := make(map[groupKey]int)
	for _, row := range rows {
		fkID := "unknown"
		switch {
		case row.Reason == "user_missing" || row.Reason == "user_deleted":
			fkID = uuidPtrStr(row.ExternalUserID)
		case row.Reason == "group_missing" || row.Reason == "group_deleted":
			fkID = uuidPtrStr(row.ExternalGroupID)
		case row.Reason == "role_missing" || row.Reason == "role_deleted":
			fkID = uuidPtrStr(row.ExternalRoleID)
		}
		counts[groupKey{Reason: row.Reason, FKID: fkID}]++
	}

	logged := 0
	for key, count := range counts {
		if logged >= 10 {
			break
		}
		ctx.Logger.Warnf("  reason=%s id=%s count=%d", key.Reason, key.FKID, count)
		logged++
	}
}

func sanitizeForTempTable(s string) string {
	result := make([]byte, 0, len(s))
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			result = append(result, c)
		}
	}
	return string(result)
}
