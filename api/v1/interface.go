package v1

import (
	"context"
	"fmt"
	"time"

	"github.com/flanksource/commons/logger"
	fs "github.com/flanksource/confighub/filesystem"
	"github.com/flanksource/kommons"
)

// Scraper ...
type Scraper interface {
	Scrape(ctx ScrapeContext, config ConfigScraper, manager Manager) []ScrapeResult
}

// Analyzer ...
type Analyzer func(configs []ScrapeResult) AnalysisResult

// AnalysisResult ...
type AnalysisResult struct {
	Analyzer string
	Messages []string
}

// Manager ...
type Manager struct {
	Finder fs.Finder
}

// ScrapeResult ...
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
	ID           string      `json:"id,omitempty"`
	Source       string      `json:"source,omitempty"`
	Config       interface{} `json:"config,omitempty"`
}

func (s ScrapeResult) String() string {
	return fmt.Sprintf("%s/%s", s.Type, s.ID)
}

// QueryColumn ...
type QueryColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// QueryResult ...
type QueryResult struct {
	Count   int                      `json:"count"`
	Columns []QueryColumn            `json:"columns"`
	Results []map[string]interface{} `json:"results"`
}

// QueryRequest ...
type QueryRequest struct {
	Query string `json:"query"`
}

// ScrapeContext ...
type ScrapeContext struct {
	context.Context
	Namespace string
	Kommons   *kommons.Client
	Scraper   *ConfigScraper
}

// WithScraper ...
func (ctx ScrapeContext) WithScraper(config *ConfigScraper) ScrapeContext {
	ctx.Scraper = config
	return ctx

}

// GetNamespace ...
func (ctx ScrapeContext) GetNamespace() string {
	return ctx.Namespace
}

// IsTrace ...
func (ctx ScrapeContext) IsTrace() bool {
	return logger.IsTraceEnabled()
}
