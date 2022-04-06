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
	Analyzer string
	Messages []string
}

type ScrapeResult struct {
	LastModified time.Time   `json:"last_modified,omitempty"`
	Type         string      `json:"type,omitempty"`
	Account      string      `json:"account,omitempty"`
	Network      string      `json:"network,omitempty"`
	Subnet       string      `json:"subnet,omitempty"`
	Region       string      `json:"region,omitempty"`
	Zone         string      `json:"zone,omitempty"`
	Name         string      `json:"name,omitempty"`
	Namespace    string      `json:"namespace,omitempty"`
	Id           string      `json:"id,omitempty"`
	Config       interface{} `json:"config,omitempty"`
}

type QueryColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type QueryResult struct {
	Count int `json:"count"`
	Columns []QueryColumn `json:"columns"`
	Results []map[string]interface{} `json:"results"`
}

type ScrapeContext struct {
	context.Context
	Namespace string
	Kommons   *kommons.Client
	Scraper   *ConfigScraper
}

func (ctx ScrapeContext) WithScraper(config *ConfigScraper) ScrapeContext {
	ctx.Scraper = config
	return ctx

}

func (ctx ScrapeContext) GetNamespace() string {
	return ctx.Namespace
}

func (ctx ScrapeContext) IsTrace() bool {
	return logger.IsTraceEnabled()
}
