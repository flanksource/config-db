package db

import (
	"fmt"
	"time"

	"github.com/flanksource/commons/hash"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/flanksource/config-db/api"
)

type permissionChangeResult struct {
	added   []*models.ConfigChange
	removed []*models.ConfigChange
	saved   int
}

type staleAccessResult struct {
	ConfigID  string
	UserName  string
	RoleName  string
	GroupName string
}

func upsertConfigAccess(ctx api.ScrapeContext, accesses []v1.ExternalConfigAccess, scraperID *uuid.UUID) (permissionChangeResult, error) {
	var result permissionChangeResult
	if scraperID == nil || len(accesses) == 0 {
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
		}
	}()

	if err := tx.Exec(fmt.Sprintf(`CREATE TEMP TABLE %s (LIKE config_access INCLUDING ALL) ON COMMIT DROP`, tempTable)).Error; err != nil {
		tx.Rollback()
		return result, fmt.Errorf("failed to create temp table: %w", err)
	}

	seen := make(map[string]struct{})
	for _, ca := range accesses {
		if ca.ID == "" {
			hid, err := deterministicAccessID(ca.ConfigAccess)
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

		if err := tx.Table(tempTable).Create(&ca.ConfigAccess).Error; err != nil {
			tx.Rollback()
			return result, fmt.Errorf("failed to insert into temp table: %w", err)
		}
		result.saved++
	}

	// Upsert: insert new records, restore soft-deleted ones
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

	if err := tx.Raw(newSQL).Scan(&newRows).Error; err != nil {
		tx.Rollback()
		return result, fmt.Errorf("failed to upsert config access: %w", err)
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

	// Soft-delete stale records not in current scrape
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

func sanitizeForTempTable(s string) string {
	result := make([]byte, 0, len(s))
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			result = append(result, c)
		}
	}
	return string(result)
}
