package api

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	dutyCtx "github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
	"github.com/samber/lo"
)

type ScrapeContext struct {
	dutyCtx.Context

	temp *TempCache

	namespace string

	jobHistory   *models.JobHistory
	scrapeConfig *v1.ScrapeConfig
}

func NewScrapeContext(ctx dutyCtx.Context) ScrapeContext {
	return ScrapeContext{
		Context: ctx,
		temp:    NewTempCache(),
	}
}

func (ctx ScrapeContext) PropertyOn(def bool, key string) bool {
	paths := []string{
		fmt.Sprintf("scraper.%s", key),
	}
	if ctx.scrapeConfig != nil && ctx.ScrapeConfig().GetUID() != "" {
		paths = append([]string{fmt.Sprintf("scraper.%s.%s", ctx.ScrapeConfig().GetUID(), key)}, paths...)
	}
	return ctx.Properties().On(def, paths...)
}

func (ctx ScrapeContext) TempCache() *TempCache {
	return ctx.temp
}

func (ctx ScrapeContext) withTempCache(cache *TempCache) ScrapeContext {
	ctx.temp = cache
	return ctx
}

var scraperTempCache = sync.Map{}

func (ctx ScrapeContext) InitTempCache() (ScrapeContext, error) {
	if ctx.ScrapeConfig().GetPersistedID() == nil {
		cache, err := QueryCache(ctx.Context, "scraper_id IS NULL")
		if err != nil {
			return ctx, err
		}
		return ctx.withTempCache(cache), nil
	}

	scraperID := ctx.ScrapeConfig().GetPersistedID()

	cache, err := QueryCache(ctx.Context, "scraper_id = ?", scraperID)
	if err != nil {
		return ctx, err
	}
	// We reset the scraper temp cache
	// For kubernetes consumer jobs, this cache can be reused
	// and is reset on every InitTempCache() call which happens
	// in RunScraper()
	scraperTempCache.Store(*scraperID, cache)
	return ctx.withTempCache(cache), nil
}

func (ctx ScrapeContext) WithValue(key, val any) ScrapeContext {
	return ScrapeContext{
		Context:      ctx.Context.WithValue(key, val),
		temp:         ctx.temp,
		namespace:    ctx.namespace,
		jobHistory:   ctx.jobHistory,
		scrapeConfig: ctx.scrapeConfig,
	}

}

func (ctx ScrapeContext) WithScrapeConfig(scraper *v1.ScrapeConfig) ScrapeContext {
	ctx.scrapeConfig = scraper

	ctx.Context = ctx.Context.WithObject(*scraper)

	// Try to use the temp cache if it exits
	if c, exists := scraperTempCache.Load(lo.FromPtr(scraper.GetPersistedID())); exists {
		ctx.temp = c.(*TempCache)
	}
	return ctx
}

func (ctx ScrapeContext) WithTimeout(timeout time.Duration) (c ScrapeContext, cancel context.CancelFunc, cancelTimeout context.CancelFunc) {

	ctx.Context, cancelTimeout = ctx.Context.WithTimeout(ctx.Properties().Duration("scraper.timeout", 4*time.Hour))
	c2, cancel := context.WithCancel(ctx.Context)
	ctx.Context = ctx.Context.Wrap(c2)
	return ctx, cancel, cancelTimeout
}

func (ctx ScrapeContext) WithJobHistory(jobHistory *models.JobHistory) ScrapeContext {
	ctx.jobHistory = jobHistory
	return ctx
}

func (ctx ScrapeContext) DutyContext() dutyCtx.Context {
	return ctx.Context.WithNamespace(ctx.Namespace())
}

func (ctx ScrapeContext) JobHistory() *models.JobHistory {
	h := ctx.jobHistory
	if h == nil {
		// Return dummy job history if unset
		return models.NewJobHistory(logger.GetLogger("dummy_logger"), "dummy", "dummy", "dummy")
	}
	return h
}

func (ctx ScrapeContext) ScrapeConfig() *v1.ScrapeConfig {
	return ctx.scrapeConfig
}

func (ctx ScrapeContext) ScraperID() string {
	if ctx.scrapeConfig == nil || ctx.scrapeConfig.GetPersistedID() == nil {
		return ""
	}
	return ctx.scrapeConfig.GetPersistedID().String()
}

func (ctx ScrapeContext) Namespace() string {
	if ctx.namespace != "" {
		return ctx.namespace
	}
	if ctx.ScrapeConfig() != nil {
		return ctx.ScrapeConfig().Namespace
	}
	return ""
}

func (ctx ScrapeContext) IsTrace() bool {
	return ctx.scrapeConfig.Spec.IsTrace()
}
