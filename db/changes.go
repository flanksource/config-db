package db

import (
	"time"

	"github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/db/models"
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
	err := ctx.DB().Model(&models.ConfigChange{}).
		Where("fingerprint in (?)", fingerprints).
		Where("NOW() - created_at <= ?", window).
		Order("created_at").
		Find(&output).
		Error
	return output, err
}
