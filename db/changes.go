package db

import (
	"time"

	"github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/db/models"
	"github.com/samber/lo"
)

func GetWorkflowRunCount(ctx api.ScrapeContext, workflowID string) (int64, error) {
	var count int64
	err := ctx.DB().Table("config_changes").
		Where("config_id = (?)", ctx.DB().Table("config_items").Select("id").Where("? = ANY(external_id)", workflowID)).
		Count(&count).
		Error
	return count, err
}

func GetChangesWithFingerprints(ctx api.ScrapeContext, fingerprints []string, window time.Duration) ([]*models.ConfigChange, error) {
	var output []*models.ConfigChange
	err := ctx.DB().Debug().Model(&models.ConfigChange{}).
		Where("fingerprint in (?)", fingerprints).
		Joins("LEFT JOIN config_items ON config_changes.config_id = config_items.id").
		Where("config_items.scraper_id = ?", lo.FromPtr(ctx.ScrapeConfig().GetPersistedID())).
		Where("NOW() - config_changes.created_at <= ?", window).
		Order("config_changes.created_at").
		Find(&output).
		Error
	return output, err
}
