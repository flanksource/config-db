package v1

import (
	"context"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/kommons"
)

type Scraper interface {
	Scrape(ctx ScrapeContext, config ConfigScraper) []ScrapeResult
}

type Analyzer func(configs []ScrapeResult) AnalysisResult

type AnalysisResult struct {
	Messages []string
}

type ScrapeResult struct {
	LastModified time.Time
	Type, Id     string
	Config       interface{}
}

type ScrapeContext struct {
	context.Context
	Namespace string
	Kommons   *kommons.Client
}

func (ctx ScrapeContext) GetNamespace() string {
	return ctx.Namespace
}

func (ctx ScrapeContext) IsTrace() bool {
	return logger.IsTraceEnabled()
}
