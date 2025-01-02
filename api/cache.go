package api

import (
	"fmt"
	"strings"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
	"github.com/flanksource/duty/context"
	dutydb "github.com/flanksource/duty/db"
	"github.com/google/uuid"
	"github.com/samber/lo"
)

func init() {
	logger.SkipFrameSuffixes = append(logger.SkipFrameSuffixes, "api/cache.go")
}

// TempCache is a temporary cache of config items that is used to speed up config item lookups during scrape, when all config items for a scraper are looked up any way
type TempCache struct {
	items     map[string]models.ConfigItem
	aliases   map[string]string
	notFound  map[string]struct{}
	scraperID string
}

func NewTempCache() *TempCache {
	return &TempCache{
		items:    make(map[string]models.ConfigItem),
		aliases:  make(map[string]string),
		notFound: make(map[string]struct{}),
	}
}

func (t *TempCache) SetScraperID(id string) {
	t.scraperID = id
}

func (t TempCache) GetScraperID() string {
	return t.scraperID
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
		lookup.ScraperID = ctx.ScraperID()
	}

	if _, ok := t.notFound[lookup.Key()]; ok {
		ctx.Counter("temp_cache_missing_hit",
			"scraper_id", ctx.ScraperID(),
			"scrape_type", lo.Ternary(ctx.IsIncrementalScrape(), "incremental", "full"),
			"query_type", "external_id",
		).Add(1)
		return nil, nil
	}

	if uid := lookup.GetKubernetesUID(); uid != "" {
		// kubernetes external ids are stored are the same as the config ids
		return t.Get(ctx, uid)
	}

	if alias, ok := t.aliases[lookup.Key()]; ok {
		return t.Get(ctx, alias)
	}

	var result models.ConfigItem
	if err := lookup.Find(ctx.DB()).Find(&result).Error; err != nil {
		return nil, err
	}

	ctx.Counter("temp_cache_miss",
		"scraper_id", ctx.ScraperID(),
		"scrape_type", lo.Ternary(ctx.IsIncrementalScrape(), "incremental", "full"),
		"query_type", "external_id",
		"config_type", result.Type,
	).Add(1)

	if result.ID == "" {
		t.notFound[lookup.Key()] = struct{}{}
		return nil, nil
	}

	t.Insert(result)
	return &result, nil
}

func (t *TempCache) Insert(item models.ConfigItem) {
	scraperID := lo.FromPtr(item.ScraperID).String()
	if scraperID == uuid.Nil.String() {
		scraperID = t.GetScraperID()
	}
	for _, extID := range item.ExternalID {
		key := v1.ExternalID{ConfigType: item.Type, ExternalID: extID, ScraperID: scraperID}.Key()
		t.aliases[key] = item.ID

		// Remove from nonFound cache
		delete(t.notFound, key)
	}

	t.items[strings.ToLower(item.ID)] = item
	delete(t.notFound, strings.ToLower(item.ID))
}

type CacheOption string

var (
	IgnoreNotFound CacheOption = "IgnoreNotFound"
)

func (t *TempCache) Get(ctx ScrapeContext, id string, opts ...CacheOption) (*models.ConfigItem, error) {
	id = strings.ToLower(id)
	if id == "" {
		return nil, nil
	}

	optMap := lo.SliceToMap(opts, func(opt CacheOption) (CacheOption, bool) {
		return opt, true
	})

	if _, notFound := t.notFound[id]; notFound && !optMap[IgnoreNotFound] {
		ctx.Counter("temp_cache_missing_hit",
			"scraper_id", ctx.ScraperID(),
			"scrape_type", lo.Ternary(ctx.IsIncrementalScrape(), "incremental", "full"),
			"query_type", "id",
		).Add(1)
		return nil, nil
	}

	if item, ok := t.items[id]; ok {
		ctx.Counter("temp_cache_hit",
			"scraper_id", ctx.ScraperID(),
			"scrape_type", lo.Ternary(ctx.IsIncrementalScrape(), "incremental", "full"),
			"query_type", "id",
		).Add(1)
		return &item, nil
	}

	result := models.ConfigItem{}

	if uuid.Validate(id) == nil {
		if err := ctx.DB().Limit(1).Find(&result, "id = ? ", id).Error; err != nil {
			return nil, dutydb.ErrorDetails(err)
		}
	} else {
		if r, err := t.Find(ctx, v1.ExternalID{
			ExternalID: id,
		}); err != nil {
			return nil, dutydb.ErrorDetails(err)
		} else if r != nil {
			result = *r
		}
	}

	ctx.Counter("temp_cache_miss",
		"scraper_id", ctx.ScraperID(),
		"scrape_type", lo.Ternary(ctx.IsIncrementalScrape(), "incremental", "full"),
		"query_type", "id",
		"config_type", result.Type,
	).Add(1)

	if result.ID != "" {
		t.Insert(result)
		return &result, nil
	} else {
		t.notFound[id] = struct{}{}
	}

	return nil, nil
}

func QueryCache(ctx context.Context, scraperID string, query string, args ...interface{}) (*TempCache, error) {
	if ctx.DB() == nil {
		return nil, fmt.Errorf("no db configured")
	}
	t := NewTempCache()
	t.SetScraperID(scraperID)
	items := []models.ConfigItem{}
	if err := ctx.DB().Table("config_items").Where("deleted_at IS NULL").Where(query, args...).Find(&items).Error; err != nil {
		return nil, err
	}
	for _, item := range items {
		t.Insert(item)
	}
	return t, nil
}
