package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
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

type AnalysisType string

const (
	AnalysisTypeAvailability   AnalysisType = "availability"
	AnalysisTypeCompliance     AnalysisType = "compliance"
	AnalysisTypeCost           AnalysisType = "cost"
	AnalysisTypeOther          AnalysisType = "other"
	AnalysisTypePerformance    AnalysisType = "performance"
	AnalysisTypeRecommendation AnalysisType = "recommendation"
	AnalysisTypeReliability    AnalysisType = "reliability"
	AnalysisTypeSecurity       AnalysisType = "security"
)

type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
	SeverityInfo     Severity = "info"
)

var severityRank = map[Severity]int{
	SeverityCritical: 5,
	SeverityHigh:     4,
	SeverityMedium:   3,
	SeverityLow:      2,
	SeverityInfo:     1,
}

// IsMoreSevere compares whether s1 is more severe than s2.
func IsMoreSevere(s1, s2 Severity) bool {
	return severityRank[s1] > severityRank[s2]
}

// AnalysisResult ...
// +kubebuilder:object:generate=false
type AnalysisResult struct {
	ExternalID    string
	ConfigType    string
	Summary       string         // Summary of the analysis
	Analysis      map[string]any // Detailed metadata of the analysis
	AnalysisType  AnalysisType   // Type of analysis, e.g. availability, compliance, cost, security, performance.
	Severity      Severity       // Severity of the analysis, e.g. critical, high, medium, low, info
	Source        string         // Source indicates who/what made the analysis. example: Azure advisor, AWS Trusted advisor
	Analyzer      string         // Very brief description of the analysis
	Messages      []string       // A detailed paragraphs of the analysis
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
		ConfigType:    t.ConfigType,
		Analyzer:      t.Analyzer,
		Message:       strings.Join(t.Messages, "<br><br>"),
		Severity:      string(t.Severity),
		AnalysisType:  string(t.AnalysisType),
		Summary:       t.Summary,
		Analysis:      t.Analysis,
		Status:        t.Status,
		Source:        t.Source,
		FirstObserved: t.FirstObserved,
		LastObserved:  t.LastObserved,
	}
}

// +kubebuilder:object:generate=false
type ChangeResult struct {
	ExternalID       string                 `json:"external_id"`
	ConfigType       string                 `json:"config_type"`
	ExternalChangeID string                 `json:"external_change_id"`
	Action           ChangeAction           `json:"action"`
	ChangeType       string                 `json:"change_type"`
	Patches          string                 `json:"patches"`
	Summary          string                 `json:"summary"`
	Severity         string                 `json:"severity"`
	Source           string                 `json:"source"`
	CreatedBy        *string                `json:"created_by"`
	CreatedAt        *time.Time             `json:"created_at"`
	Details          map[string]interface{} `json:"details"`
}

func (r ChangeResult) AsMap() map[string]any {
	output := make(map[string]any)

	b, err := json.Marshal(r)
	if err != nil {
		logger.Errorf("failed to marshal change result: %v", err)
		return output
	}

	if err := json.Unmarshal(b, &output); err != nil {
		logger.Errorf("failed to unmarshal change result: %v", err)
	}

	return output
}

func (r ChangeResult) PatchesMap() map[string]any {
	output := make(map[string]any)
	if r.Patches != "" {
		if err := json.Unmarshal([]byte(r.Patches), &output); err != nil {
			logger.Errorf("failed to unmarshal: %v", err)
		}
	}

	return output
}

func (c ChangeResult) String() string {
	return fmt.Sprintf("%s/%s: %s", c.ConfigType, c.ExternalID, c.ChangeType)
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

func (t ScrapeResults) HasErr() bool {
	for _, r := range t {
		if r.Error != nil {
			return true
		}
	}

	return false
}

func (t ScrapeResults) Errors() []string {
	var errs []string
	for _, r := range t {
		if r.Error != nil {
			errs = append(errs, r.Error.Error())
		}
	}

	return errs
}

func (t *ScrapeResults) Add(r ...ScrapeResult) {
	*t = append(*t, r...)
}

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

func (s *ScrapeResults) Analysis(analyzer string, configType string, id string) *AnalysisResult {
	result := AnalysisResult{
		Analyzer:   analyzer,
		ConfigType: configType,
		ExternalID: id,
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
	ConfigClass         string              `json:"config_class,omitempty"`
	Type                string              `json:"config_type,omitempty"`
	Status              string              `json:"status,omitempty"` // status extracted from the config itself
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
	ParentType          string              `json:"-"`
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

func (s ScrapeResult) SetError(err error) ScrapeResult {
	s.Error = err
	return s
}

func (s ScrapeResult) Clone(config interface{}) ScrapeResult {
	clone := ScrapeResult{
		LastModified: s.LastModified,
		Aliases:      s.Aliases,
		ConfigClass:  s.ConfigClass,
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
	s := fmt.Sprintf("%s/%s (%s)", r.ConfigClass, r.Name, r.ID)

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

// ConfigMap returns the underlying config as a map
func (r ScrapeResult) ConfigMap() map[string]any {
	output := make(map[string]any)

	b, err := json.Marshal(r.Config)
	if err != nil {
		logger.Errorf("failed to marshal config: %v", err)
		return output
	}

	if err := json.Unmarshal(b, &output); err != nil {
		logger.Errorf("failed to unmarshal config: %v", err)
	}

	return output
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

// RunNowResponse represents the response body for a run now request
type RunNowResponse struct {
	Total   int      `json:"total"`
	Success int      `json:"success"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
}

// ScrapeContext ...
// +kubebuilder:object:generate=false
type ScrapeContext struct {
	context.Context
	DB                   *gorm.DB
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

// HydrateConnectionByURL ...
func (ctx *ScrapeContext) HydrateConnectionByURL(connectionName string) (*models.Connection, error) {
	if connectionName == "" {
		return nil, nil
	}

	if !strings.HasPrefix(connectionName, "connection://") {
		return nil, fmt.Errorf("invalid connection name: [%s]", connectionName)
	}

	if ctx.DB == nil {
		return nil, errors.New("db has not been initialized")
	}

	if ctx.Kubernetes == nil {
		return nil, errors.New("kubernetes clientset has not been initialized")
	}

	connection, err := duty.HydratedConnectionByURL(ctx, ctx.DB, ctx.Kubernetes, ctx.Namespace, connectionName)
	if err != nil {
		return nil, err
	}

	// Connection name was explicitly provided but was not found.
	// That's an error.
	if connection == nil {
		return nil, fmt.Errorf("connection %s not found", connectionName)
	}

	return connection, nil
}
