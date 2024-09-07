package api

import (
	"fmt"
	"strings"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
	"github.com/flanksource/duty/context"
	dutydb "github.com/flanksource/duty/db"
	"github.com/samber/lo"
)

func init() {
	logger.SkipFrameSuffixes = append(logger.SkipFrameSuffixes, "api/cache.go")
}

// TempCache is a temporary cache of config items that is used to speed up config item lookups during scrape, when all config items for a scraper are looked up any way
type TempCache struct {
	items    map[string]models.ConfigItem
	aliases  map[string]string
	notFound map[string]struct{}
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

	if lookup.ScraperID == "" {
		lookup.ScraperID = string(ctx.ScrapeConfig().GetUID())
	}

	if _, ok := t.notFound[lookup.Key()]; ok {
		return nil, nil
	}

	if uid := lookup.GetKubernetesUID(); uid != "" {
		// kubernetes external ids are stored are the same as the config ids
		return t.Get(ctx, uid)
	}

	if t.aliases == nil {
		t.aliases = make(map[string]string)
	}

	if alias, ok := t.aliases[lookup.Key()]; ok {
		return t.Get(ctx, alias)
	}

	var result models.ConfigItem
	if err := lookup.Find(ctx.DB()).Find(&result).Error; err != nil {
		return nil, err
	}

	if result.ID != "" {
		t.Insert(result)
		return &result, nil
	} else {

		if t.notFound == nil {
			t.notFound = make(map[string]struct{})
		}
		t.notFound[lookup.Key()] = struct{}{}
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
	delete(t.notFound, strings.ToLower(item.ID))
	delete(t.notFound, v1.ExternalID{
		ConfigType: lo.FromPtr(item.Type),
		ExternalID: []string(item.ExternalID),
		ScraperID:  lo.FromPtr(item.ScraperID).String(),
	}.Key())
}

func (t *TempCache) Get(ctx ScrapeContext, id string) (*models.ConfigItem, error) {
	id = strings.ToLower(id)
	if id == "" {
		return nil, nil
	}

	if _, notFound := t.notFound[id]; notFound {
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
		return nil, dutydb.ErrorDetails(err)
	}

	if result.ID != "" {
		t.Insert(result)
		return &result, nil
	} else {
		if t.notFound == nil {
			t.notFound = make(map[string]struct{})
		}
		t.notFound[id] = struct{}{}
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
