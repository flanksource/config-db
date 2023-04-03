package v1

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Scraper ...
// +kubebuilder:object:generate=false
type Scraper interface {
	Scrape(ctx *ScrapeContext, config ConfigScraper) ScrapeResults
	CanScrape(config ConfigScraper) bool
}

// Analyzer ...
// +kubebuilder:object:generate=false
type Analyzer func(configs []ScrapeResult) AnalysisResult

// AnalysisResult ...
// +kubebuilder:object:generate=false
type AnalysisResult struct {
	ExternalID    string
	ExternalType  string
	Summary       string            // Summary of the analysis
	Analysis      map[string]string // Detailed metadata of the analysis
	AnalysisType  string            // Type of analysis, e.g. availability, compliance, cost, security, performance.
	Severity      string            // Severity of the analysis, e.g. critical, high, medium, low, info
	Source        string            // Source indicates who/what made the analysis. example: Azure advisor, AWS Trusted advisor
	Analyzer      string            // Name of the analyzer that generated the analysis
	Messages      []string          // A detailed paragraphs of the analysis
	Status        string
	FirstObserved *time.Time
	LastObserved  *time.Time
	Error         error
}

// ToConfigAnalysis converts this analysis result to a config analysis
// db model.
func (t *AnalysisResult) ToConfigAnalysis() models.ConfigAnalysis {
	return models.ConfigAnalysis{
		ExternalID:    t.ExternalID,
		ExternalType:  t.ExternalType,
		Analyzer:      t.Analyzer,
		Message:       strings.Join(t.Messages, ";"),
		Severity:      t.Severity,
		AnalysisType:  t.AnalysisType,
		Summary:       t.Summary,
		Status:        t.Status,
		Source:        t.Source,
		FirstObserved: t.FirstObserved,
		LastObserved:  t.LastObserved,
	}
}

// +kubebuilder:object:generate=false
type ChangeResult struct {
	ExternalID       string
	ExternalType     string
	ExternalChangeID string
	Action           ChangeAction
	ChangeType       string
	Patches          string
	Summary          string
	Severity         string
	Source           string
	CreatedAt        *time.Time
	Details          map[string]interface{}
}

func (c ChangeResult) String() string {
	return fmt.Sprintf("%s/%s: %s", c.ExternalType, c.ExternalID, c.ChangeType)
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

// +kubebuilder:object:generate=false
type AnalysisResults []AnalysisResult

// +kubebuilder:object:generate=false
type ScrapeResults []ScrapeResult

type RelationshipResult struct {
	ConfigExternalID  ExternalID
	RelatedExternalID ExternalID
	Relationship      string
}

type RelationshipResults []RelationshipResult

func (s *ScrapeResults) AddChange(change ChangeResult) *ScrapeResults {
	*s = append(*s, ScrapeResult{
		Changes: []ChangeResult{change},
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
	logger.Errorf("%s: %v", fmt.Sprintf(msg, args...), e)
	*s = append(*s, ScrapeResult{Error: e})
	return *s
}

// ScrapeResult ...
// +kubebuilder:object:generate=false
type ScrapeResult struct {
	CreatedAt           *time.Time          `json:"created_at,omitempty"`
	DeletedAt           *time.Time          `json:"deleted_at,omitempty"`
	LastModified        time.Time           `json:"last_modified,omitempty"`
	Type                string              `json:"type,omitempty"`
	ExternalType        string              `json:"external_type,omitempty"`
	Account             string              `json:"account,omitempty"`
	Network             string              `json:"network,omitempty"`
	Subnet              string              `json:"subnet,omitempty"`
	Region              string              `json:"region,omitempty"`
	Zone                string              `json:"zone,omitempty"`
	Name                string              `json:"name,omitempty"`
	Namespace           string              `json:"namespace,omitempty"`
	ID                  string              `json:"id,omitempty"`
	Aliases             []string            `json:"aliases,omitempty"`
	Source              string              `json:"source,omitempty"`
	Config              interface{}         `json:"config,omitempty"`
	Format              string              `json:"format,omitempty"`
	Icon                string              `json:"icon,omitempty"`
	Tags                JSONStringMap       `json:"tags,omitempty"`
	BaseScraper         BaseScraper         `json:"-"`
	Error               error               `json:"-"`
	AnalysisResult      *AnalysisResult     `json:"analysis,omitempty"`
	Changes             []ChangeResult      `json:"-"`
	RelationshipResults RelationshipResults `json:"-"`
	Ignore              []string            `json:"-"`
	Action              string              `json:",omitempty"`
	ParentExternalID    string              `json:"-"`
	ParentExternalType  string              `json:"-"`
}

func NewScrapeResult(base BaseScraper) *ScrapeResult {
	return &ScrapeResult{
		BaseScraper: base,
		Format:      base.Format,
		Tags:        base.Tags,
	}
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
		Format:       s.Format,
		Error:        s.Error,
	}
	return clone
}

func (r ScrapeResult) String() string {
	s := fmt.Sprintf("%s/%s (%s)", r.Type, r.Name, r.ID)

	if len(r.Changes) > 0 {
		s += fmt.Sprintf(" changes=%d", len(r.Changes))
	}
	if len(r.RelationshipResults) > 0 {
		s += fmt.Sprintf(" relationships=%d", len(r.RelationshipResults))
	}
	if r.AnalysisResult != nil {
		s += " analysis=1"
	}
	return s
}

// QueryColumn ...
type QueryColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// QueryResult ...
// +kubebuilder:object:generate=false
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
// +kubebuilder:object:generate=false
type ScrapeContext struct {
	context.Context
	Namespace            string
	Kubernetes           *kubernetes.Clientset
	KubernetesRestConfig *rest.Config
	Scraper              *ConfigScraper
	ScraperID            *uuid.UUID
}

func (ctx ScrapeContext) Find(path string) ([]string, error) {
	return filepath.Glob(path)
}

// Read returns the contents of a file, the base filename and an error
func (ctx ScrapeContext) Read(path string) ([]byte, string, error) {
	content, err := os.ReadFile(path)
	filename := filepath.Base(path)
	return content, filename, err
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
	return ctx.Scraper != nil && ctx.Scraper.IsTrace()
}
