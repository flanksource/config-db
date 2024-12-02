package db

import (
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
)

func PersistScrapePluginFromCRD(ctx context.Context, plugin *v1.ScrapePlugin) error {
	m, err := plugin.ToModel()
	if err != nil {
		return err
	}
	m.Source = models.SourceCRD

	return ctx.DB().Save(m).Error
}

func DeleteScrapePlugin(ctx context.Context, id string) error {
	return ctx.DB().Model(&models.ScrapePlugin{}).Where("id = ?", id).Update("deleted_at", duty.Now()).Error
}
