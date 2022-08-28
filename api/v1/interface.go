package v1

import (
	"context"
	"fmt"
	"time"

	"github.com/flanksource/commons/logger"
	fs "github.com/flanksource/config-db/filesystem"
	"github.com/flanksource/kommons"
)

// Scraper ...
type Scraper interface {
	Scrape(ctx ScrapeContext, config ConfigScraper, manager Manager) ScrapeResults
}

// Analyzer ...
type Analyzer func(configs []ScrapeResult) AnalysisResult

// AnalysisResult ...
type AnalysisResult struct {
	ExternalID    string
	ExternalType  string
	Summary       string
	Analysis      map[string]string
	AnalysisType  string
	Status        string
	Severity      string
	FirstObserved *time.Time
	LastObserved  *time.Time
	Analyzer      string
	Messages      []string
	Error         error
}

type ChangeResult struct {
	ExternalID   string
	ExternalType string
	ChangeType   string
	Patches      string
	Summary      string
	Severity     string
	Source       string
	CreatedAt    *time.Time
	Details      map[string]string
}

func (result AnalysisResult) String() string {
	return fmt.Sprintf("%s: %s", result.Analyzer, result.Messages)
}
func (result *AnalysisResult) Message(msg string) *AnalysisResult {
	if msg == "" {
		return result
	}
	result.Messages = append(result.Messages, msg)
	return result
}

type AnalysisResults []AnalysisResult

// Manager ...
type Manager struct {
	Finder fs.Finder
}

type ScrapeResults []ScrapeResult

func (s *ScrapeResults) AddChange(change ChangeResult) *ScrapeResults {
	*s = append(*s, ScrapeResult{
		ChangeResult: &change,
	})
	return s
}

func (s *ScrapeResults) Analysis(analyzer string, externalType string, id string) *AnalysisResult {
	result := AnalysisResult{
		Analyzer:     analyzer,
		ExternalType: externalType,
		ExternalID:   id,
	}
	*s = append(*s, ScrapeResult{
		AnalysisResult: &result,
	})
	return &result
}

func (s *ScrapeResults) Errorf(e error, msg string, args ...interface{}) ScrapeResults {
	logger.Errorf(msg, args...)
	*s = append(*s, ScrapeResult{Error: e})
	return *s
}

// ScrapeResult ...
type ScrapeResult struct {
	CreatedAt      *time.Time      `json:"created_at,omitempty"`
	LastModified   time.Time       `json:"last_modified,omitempty"`
	Type           string          `json:"type,omitempty"`
	ExternalType   string          `json:"external_type,omitempty"`
	Account        string          `json:"account,omitempty"`
	Network        string          `json:"network,omitempty"`
	Subnet         string          `json:"subnet,omitempty"`
	Region         string          `json:"region,omitempty"`
	Zone           string          `json:"zone,omitempty"`
	Name           string          `json:"name,omitempty"`
	Namespace      string          `json:"namespace,omitempty"`
	ID             string          `json:"id,omitempty"`
	Aliases        []string        `json:"aliases,omitempty"`
	Source         string          `json:"source,omitempty"`
	Config         interface{}     `json:"config,omitempty"`
	Tags           JSONStringMap   `json:"tags,omitempty"`
	BaseScraper    BaseScraper     `json:"-"`
	Error          error           `json:"-"`
	AnalysisResult *AnalysisResult `json:"analysis,omitempty"`
	ChangeResult   *ChangeResult   `json:"change,omitempty"`
	Ignore         []string        `json:"-"`
}

func (s ScrapeResult) Success(config interface{}) ScrapeResult {
	s.Config = config
	return s
}

func (s ScrapeResult) Errorf(msg string, args ...interface{}) ScrapeResult {
	s.Error = fmt.Errorf(msg, args...)
	return s
}

func (s ScrapeResult) Clone(config interface{}) ScrapeResult {
	clone := ScrapeResult{
		LastModified: s.LastModified,
		Aliases:      s.Aliases,
		Type:         s.Type,
		Account:      s.Account,
		Network:      s.Network,
		Subnet:       s.Subnet,
		Region:       s.Region,
		Zone:         s.Zone,
		Name:         s.Name,
		Namespace:    s.Namespace,
		ID:           s.ID,
		Source:       s.Source,
		Config:       config,
		Tags:         s.Tags,
		BaseScraper:  s.BaseScraper,
		Error:        s.Error,
	}
	return clone
}

func (s ScrapeResult) String() string {
	return fmt.Sprintf("%s/%s (%s)", s.Type, s.Name, s.ID)
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
