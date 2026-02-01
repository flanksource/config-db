package db

import (
	"encoding/json"
	"fmt"
	"time"

	v1 "github.com/flanksource/config-db/api"
	"github.com/flanksource/duty"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
	gocache "github.com/patrickmn/go-cache"
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

func DeleteStaleScrapePlugin(ctx context.Context, newer *v1.ScrapePlugin) error {
	return ctx.DB().Model(&models.ScrapePlugin{}).
		Where("name = ? AND namespace = ?", newer.Name, newer.Namespace).
		Where("deleted_at IS NULL").
		Update("deleted_at", duty.Now()).Error
}

var cachedPlugin = gocache.New(time.Hour, time.Hour)

func LoadAllPlugins(ctx context.Context) ([]v1.ScrapePluginSpec, error) {
	if v, found := cachedPlugin.Get("only"); found {
		return v.([]v1.ScrapePluginSpec), nil
	}

	return ReloadAllScrapePlugins(ctx)
}

func ReloadAllScrapePlugins(ctx context.Context) ([]v1.ScrapePluginSpec, error) {
	var plugins []models.ScrapePlugin
	if err := ctx.DB().Where("deleted_at IS NULL").Find(&plugins).Error; err != nil {
		return nil, err
	}

	specs := make([]v1.ScrapePluginSpec, 0, len(plugins))
	for _, p := range plugins {
		var spec v1.ScrapePluginSpec
		if err := json.Unmarshal(p.Spec, &spec); err != nil {
			return nil, fmt.Errorf("failed to unmarshal scrape plugin spec(%s): %w", p.ID, err)
		}

		specs = append(specs, spec)
	}

	cachedPlugin.SetDefault("only", specs)

	return specs, nil
}
