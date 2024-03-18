package api

import (
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	dutyCtx "github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
	"k8s.io/client-go/rest"
)

type ScrapeContext struct {
	dutyCtx.Context

	temp *TempCache

	namespace            string
	kubernetesRestConfig *rest.Config

	jobHistory   *models.JobHistory
	scrapeConfig *v1.ScrapeConfig
}

func NewScrapeContext(ctx dutyCtx.Context) ScrapeContext {
	return ScrapeContext{
		Context: ctx,
		temp: &TempCache{
			ctx: ctx,
		},
	}
}

func (ctx ScrapeContext) TempCache() *TempCache {
	return ctx.temp
}

func (ctx ScrapeContext) withTempCache(cache *TempCache) ScrapeContext {
	ctx.temp = cache
	return ctx
}
func (ctx ScrapeContext) InitTempCache() (ScrapeContext, error) {
	if ctx.ScrapeConfig().GetPersistedID() == nil {
		cache, err := QueryCache(ctx.Context, "scraper_id IS NULL")
		if err != nil {
			return ctx, err
		}
		return ctx.withTempCache(cache), nil
	}
	cache, err := QueryCache(ctx.Context, "scraper_id = ?", ctx.ScrapeConfig().GetPersistedID())
	if err != nil {
		return ctx, err
	}
	return ctx.withTempCache(cache), nil
}

func (ctx ScrapeContext) WithValue(key, val any) ScrapeContext {
	return ScrapeContext{
		Context: dutyCtx.Context{
			Context: ctx.Context.WithValue(key, val),
		},
		temp:                 ctx.temp,
		namespace:            ctx.namespace,
		kubernetesRestConfig: ctx.kubernetesRestConfig,
		jobHistory:           ctx.jobHistory,
		scrapeConfig:         ctx.scrapeConfig,
	}

}

func (ctx ScrapeContext) WithScrapeConfig(scraper *v1.ScrapeConfig) ScrapeContext {
	ctx.scrapeConfig = scraper
	return ctx
}

func (ctx ScrapeContext) WithJobHistory(jobHistory *models.JobHistory) ScrapeContext {
	ctx.jobHistory = jobHistory
	return ctx
}

func (ctx ScrapeContext) DutyContext() dutyCtx.Context {
	return ctx.Context
}

func (ctx ScrapeContext) JobHistory() *models.JobHistory {
	h := ctx.jobHistory
	if h == nil {
		// Return dummy job history if unset
		return models.NewJobHistory(logger.GetZapLogger().Named("dummy_logger"), "dummy", "dummy", "dummy")
	}
	return h
}

func (ctx ScrapeContext) ScrapeConfig() *v1.ScrapeConfig {
	return ctx.scrapeConfig
}

func (ctx ScrapeContext) Namespace() string {
	return ctx.namespace
}

func (c ScrapeContext) KubernetesRestConfig() *rest.Config {
	return c.kubernetesRestConfig
}

func (ctx ScrapeContext) IsTrace() bool {
	return ctx.scrapeConfig.Spec.IsTrace()
}
