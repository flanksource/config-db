package v1

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/utils"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"
)

const maxTagsCount = 5

// Analyzer ...
// +kubebuilder:object:generate=false
type Analyzer func(configs []ScrapeResult) AnalysisResult

var severityRank = map[models.Severity]int{
	models.SeverityCritical: 5,
	models.SeverityHigh:     4,
	models.SeverityMedium:   3,
	models.SeverityLow:      2,
	models.SeverityInfo:     1,
}

// IsMoreSevere compares whether s1 is more severe than s2.
func IsMoreSevere(s1, s2 models.Severity) bool {
	return severityRank[s1] > severityRank[s2]
}

// AnalysisResult ...
// +kubebuilder:object:generate=false
type AnalysisResult struct {
	ExternalID    string
	ConfigType    string
	Summary       string              // Summary of the analysis
	Analysis      map[string]any      // Detailed metadata of the analysis
	AnalysisType  models.AnalysisType // Type of analysis, e.g. availability, compliance, cost, security, performance.
	Severity      models.Severity     // Severity of the analysis, e.g. critical, high, medium, low, info
	Source        string              // Source indicates who/what made the analysis. example: Azure advisor, AWS Trusted advisor
	Analyzer      string              // Very brief description of the analysis
	Messages      []string            // A detailed paragraphs of the analysis
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
		Severity:      t.Severity,
		AnalysisType:  t.AnalysisType,
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
	Diff             *string                `json:"diff,omitempty"`

	// UpdateExisting indicates whether to update an existing change
	UpdateExisting bool `json:"update_existing"`
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
	// Config ID of the parent
	ConfigID string
	// ConfigExternalID is the external id to lookup the actual config item ID.
	// used when the config id is not known.
	ConfigExternalID ExternalID

	// Related External ID to lookup the actual config item ID.
	// Used when the related config id is not known.
	RelatedExternalID ExternalID
	// Config ID of the related config.
	RelatedConfigID string

	Relationship string
}

func (r RelationshipResult) String() string {
	s := ""
	if r.ConfigID != "" {
		s += fmt.Sprintf("config_id=%s ", r.ConfigID)
	}
	if !r.ConfigExternalID.IsEmpty() {
		s += fmt.Sprintf("config_external_id=%s ", r.ConfigExternalID)
	}
	if r.RelatedConfigID != "" {
		s += fmt.Sprintf("related_config_id=%s ", r.RelatedConfigID)
	}
	if !r.RelatedExternalID.IsEmpty() {
		s += fmt.Sprintf("related_external_id=%s ", r.RelatedExternalID)
	}
	s += fmt.Sprintf("relationship=%s", r.Relationship)
	return s
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

type Tag struct {
	Name     string `json:"name"`
	Label    string `json:"label,omitempty"`
	JSONPath string `json:"jsonpath,omitempty"`
	Value    string `json:"value,omitempty"`
}

type Tags []Tag

func (t Tags) Has(name string) bool {
	for _, item := range t {
		if item.Name == name {
			return true
		}
	}

	return false
}

func (t Tags) sanitize() Tags {
	for i := range t {
		t[i].Name = strings.TrimSpace(t[i].Name)
		t[i].Label = strings.TrimSpace(t[i].Label)
		t[i].JSONPath = strings.TrimSpace(t[i].JSONPath)
		t[i].Value = strings.TrimSpace(t[i].Value)
	}

	return t
}

func (t Tags) valid() error {
	if len(t) > maxTagsCount {
		return fmt.Errorf("too many tags. only %d allowed", maxTagsCount)
	}

	seen := make(map[string]struct{})
	for _, tag := range t {
		if tag.Name == "" {
			return fmt.Errorf("tag with an empty name")
		}

		if _, ok := seen[tag.Name]; ok {
			return fmt.Errorf("tag name %s is duplicated", tag.Name)
		}

		if tag.Value == "" && tag.JSONPath == "" && tag.Label == "" {
			return fmt.Errorf("tag %q should specify either value, jsonpath or label", tag.Name)
		}

		seen[tag.Name] = struct{}{}
	}

	return nil
}

func (t Tags) Eval(labels map[string]string, config string) (map[string]string, error) {
	if len(t) == 0 {
		return nil, nil
	}

	t = t.sanitize()
	if err := t.valid(); err != nil {
		return nil, err
	}

	output := make(map[string]string, len(t))
	for _, tag := range t {
		if tag.Value != "" {
			output[tag.Name] = tag.Value
		} else if tag.Label != "" {
			if val, ok := labels[tag.Label]; ok {
				output[tag.Name] = val
			}
		} else if tag.JSONPath != "" {
			if !utils.IsJSONPath(tag.JSONPath) {
				return nil, fmt.Errorf("tag %q has an invalid json path %s", tag.Name, tag.JSONPath)
			}

			if jsonExpr, err := jp.ParseString(tag.JSONPath); err != nil {
				return nil, fmt.Errorf("failed to parse jsonpath: %s: %v", tag.JSONPath, err)
			} else {
				parsedConfig, err := oj.ParseString(config)
				if err != nil {
					return nil, fmt.Errorf("failed to parse config: %w", err)
				}

				if extractedValues := jsonExpr.Get(parsedConfig); len(extractedValues) > 0 {
					output[tag.Name] = fmt.Sprintf("%v", extractedValues[0])
				}
			}
		}
	}

	return output, nil
}

// ScrapeResult ...
// +kubebuilder:object:generate=false
type ScrapeResult struct {
	// ID is the id of the config at it's origin (i.e. the external id)
	// Eg: For Azure, it's the azure resource id and for kuberetenes it's the object UID.
	// If it's a valid UUID & ConfigID is nil, it'll be used as the primary key id of the config item in the database.
	ID string `json:"id,omitempty"`

	// Config ID is the globally unique and persistent uuid of config item,
	// if no suitable ID exists, then it will be generated.
	ConfigID *string `json:"-"`

	CreatedAt           *time.Time          `json:"created_at,omitempty"`
	DeletedAt           *time.Time          `json:"deleted_at,omitempty"`
	DeleteReason        ConfigDeleteReason  `json:"delete_reason,omitempty"`
	LastModified        time.Time           `json:"last_modified,omitempty"`
	ConfigClass         string              `json:"config_class,omitempty"`
	Type                string              `json:"config_type,omitempty"`
	Status              string              `json:"status,omitempty"` // status extracted from the config itself
	Name                string              `json:"name,omitempty"`
	Description         string              `json:"description,omitempty"`
	Aliases             []string            `json:"aliases,omitempty"`
	Source              string              `json:"source,omitempty"`
	Config              interface{}         `json:"config,omitempty"`
	Format              string              `json:"format,omitempty"`
	Icon                string              `json:"icon,omitempty"`
	Labels              JSONStringMap       `json:"labels,omitempty"`
	Tags                Tags                `json:"tags,omitempty"`
	BaseScraper         BaseScraper         `json:"-"`
	Error               error               `json:"-"`
	AnalysisResult      *AnalysisResult     `json:"analysis,omitempty"`
	Changes             []ChangeResult      `json:"-"`
	RelationshipResults RelationshipResults `json:"-"`
	Ignore              []string            `json:"-"`
	Action              string              `json:",omitempty"`
	ParentExternalID    string              `json:"-"`
	ParentType          string              `json:"-"`
	Properties          types.Properties    `json:"properties,omitempty"`
	LastScrapedTime     *time.Time          `json:"last_scraped_time"`

	// RelationshipSelectors are used to form relationship of this scraped item with other items.
	// Unlike `RelationshipResults`, selectors give you the flexibility to form relationship without
	// knowing the external ids of the item to be linked.
	RelationshipSelectors []RelationshipSelector `json:"-"`
}

func (s ScrapeResult) AsMap() map[string]any {
	output := make(map[string]any)

	b, err := json.Marshal(s)
	if err != nil {
		logger.Errorf("failed to marshal change result: %v", err)
		return output
	}

	if err := json.Unmarshal(b, &output); err != nil {
		logger.Errorf("failed to unmarshal change result: %v", err)
	}

	return output
}

func NewScrapeResult(base BaseScraper) *ScrapeResult {
	return &ScrapeResult{
		BaseScraper: base,
		Format:      base.Format,
		Tags:        base.Tags,
		Labels:      base.Labels,
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
		ID:           s.ID,
		Source:       s.Source,
		Config:       config,
		Labels:       s.Labels,
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

	// If config is of type string, try unmarshal first
	if _, ok := r.Config.(string); ok {
		if err := json.Unmarshal([]byte(r.Config.(string)), &output); err != nil {
			logger.Errorf("failed to unmarshal config: %v", err)
		}
		return output
	}

	// Marshal and unmarshal into json for other types
	b, err := json.Marshal(r.Config)
	if err != nil {
		logger.Errorf("failed to marshal config: %v", err)
		return output
	}

	if err := json.Unmarshal(b, &output); err != nil {
		logger.Errorf("failed to unmarshal config: %v", err)
		return output
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
