package api

import (
	"fmt"
	"strings"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
	"github.com/flanksource/duty/context"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/samber/lo"
)

// TempCache is a temporary cache of config items that is used to speed up config item lookups during scrape, when all config items for a scraper are looked up any way
type TempCache struct {
	items   map[string]models.ConfigItem
	aliases map[string]string
}

func (t *TempCache) FindExternalID(ctx ScrapeContext, ext v1.ExternalID) (string, error) {
	if item, err := t.Find(ctx, ext); err != nil {
		return "", err
	} else if item != nil {
		return item.ID, nil
	}
	return "", nil
}

func (t *TempCache) Find(ctx ScrapeContext, lookup v1.ExternalID) (*models.ConfigItem, error) {
	configType := strings.ToLower(lookup.ConfigType)
	externalID := lookup.ExternalID[0]
	scraperID := lo.CoalesceOrEmpty(lookup.ScraperID, string(ctx.ScrapeConfig().GetUID()))

	if strings.HasPrefix(configType, "kubernetes::") && uuid.Validate(externalID) == nil {
		// kubernetes external ids are stored are the same as the config ids
		return t.Get(ctx, externalID)
	}

	if t.aliases == nil {
		t.aliases = make(map[string]string)
	}

	if alias, ok := t.aliases[scraperID+configType+externalID]; ok {
		return t.Get(ctx, alias)
	}

	var result models.ConfigItem
	query := ctx.DB().Limit(1).Order("updated_at DESC").Where("deleted_at IS NULL").Where("type = ? and external_id @> ?", lookup.ConfigType, pq.StringArray{externalID})
	if scraperID != "all" && scraperID != "" {
		query = query.Where("scraper_id = ?", scraperID)
	}
	if err := query.Find(&result).Error; err != nil {
		return nil, err
	}

	if result.ID != "" {
		t.Insert(result)
		return &result, nil
	}

	return nil, nil
}

func (t *TempCache) Insert(item models.ConfigItem) {
	if t.aliases == nil {
		t.aliases = make(map[string]string)
	}
	if t.items == nil {
		t.items = make(map[string]models.ConfigItem)
	}

	for _, id := range item.ExternalID {
		if item.Type != nil {
			t.aliases[lo.FromPtr(item.ScraperID).String()+strings.ToLower(*item.Type)+id] = item.ID
		} else {
			t.aliases[lo.FromPtr(item.ScraperID).String()+strings.ToLower(id)] = item.ID
		}
	}

	t.items[strings.ToLower(item.ID)] = item
}

func (t *TempCache) Get(ctx ScrapeContext, id string) (*models.ConfigItem, error) {
	id = strings.ToLower(id)
	if id == "" {
		return nil, nil
	}
	if t.items == nil {
		t.items = make(map[string]models.ConfigItem)
	}
	if item, ok := t.items[id]; ok {
		return &item, nil
	}

	result := models.ConfigItem{}
	if err := ctx.DB().Limit(1).Find(&result, "id = ? ", id).Error; err != nil {
		return nil, err
	}
	if result.ID != "" {
		t.Insert(result)
		return &result, nil
	}

	return nil, nil
}

func QueryCache(ctx context.Context, query string, args ...interface{}) (*TempCache, error) {
	if ctx.DB() == nil {
		return nil, fmt.Errorf("no db configured")
	}
	t := TempCache{
		items:   make(map[string]models.ConfigItem),
		aliases: make(map[string]string),
	}
	items := []models.ConfigItem{}
	if err := ctx.DB().Table("config_items").Where("deleted_at IS NULL").Where(query, args...).Find(&items).Error; err != nil {
		return nil, err
	}
	for _, item := range items {
		t.Insert(item)
	}
	return &t, nil
}
