package api

import (
	"strings"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
	"github.com/flanksource/duty/context"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// TempCache is a temporary cache of config items that is used to speed up config item lookups during scrape, when all config items for a scraper are looked up any way
type TempCache struct {
	db      *gorm.DB
	items   map[string]models.ConfigItem
	aliases map[string]string
}

func (t *TempCache) FindExternal(ext v1.ExternalID) (*models.ConfigItem, error) {
	return t.Find(ext.ConfigType, ext.ExternalID[0])
}

func (t *TempCache) FindExternalID(ext v1.ExternalID) (string, error) {
	if item, err := t.Find(ext.ConfigType, ext.ExternalID[0]); err != nil {
		return "", err
	} else if item != nil {
		return item.ID, nil
	}
	return "", nil
}

func (t *TempCache) Find(typ, id string) (*models.ConfigItem, error) {
	typ = strings.ToLower(typ)
	id = strings.ToLower(id)

	if strings.HasPrefix(typ, "kubernetes::") && uuid.Validate(id) == nil {
		// kubernetes external ids are stored are the same as the config ids
		return t.Get(id)
	}
	if t.aliases == nil {
		t.aliases = make(map[string]string)
	}

	if alias, ok := t.aliases[typ+id]; ok {
		return t.Get(alias)
	}

	result := models.ConfigItem{}
	if err := t.db.Limit(1).Find(&result, "lower(type) = ? and external_id  @> ?", typ, pq.StringArray{id}).Error; err != nil {
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
	t.aliases[strings.ToLower(*item.Type+item.ExternalID[0])] = item.ID
	t.items[strings.ToLower(item.ID)] = item
}

func (t *TempCache) Get(id string) (*models.ConfigItem, error) {
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
	if err := t.db.Limit(1).Find(&result, "id = ? ", id).Error; err != nil {
		return nil, err
	}
	if result.ID != "" {
		t.Insert(result)
		return &result, nil
	}

	return nil, nil
}

func QueryCache(ctx context.Context, query string, args ...interface{}) (*TempCache, error) {
	t := TempCache{
		db:      ctx.DB(),
		items:   make(map[string]models.ConfigItem),
		aliases: make(map[string]string),
	}
	items := []models.ConfigItem{}
	if err := t.db.Table("config_items").Where(query, args...).Find(&items).Error; err != nil {
		return nil, err
	}
	for _, item := range items {
		t.Insert(item)
	}
	return &t, nil
}
