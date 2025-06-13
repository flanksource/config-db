package v1

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/flanksource/is-healthy/pkg/health"
	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/fields"

	"github.com/flanksource/config-db/utils"
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
	ExternalID string `json:"external_id"`
	ConfigType string `json:"config_type"`

	// Scraper id of the config for external config lookup.
	// If left empty, the scraper id is the requester's scraper id.
	// Use `all` to disregard scraper id (useful when changes come from different scraper).
	ScraperID string `json:"scraper_id"`

	ExternalChangeID string         `json:"external_change_id"`
	Action           ChangeAction   `json:"action"`
	ChangeType       string         `json:"change_type"`
	Patches          string         `json:"patches"`
	Summary          string         `json:"summary"`
	Severity         string         `json:"severity"`
	Source           string         `json:"source"`
	CreatedBy        *string        `json:"created_by"`
	CreatedAt        *time.Time     `json:"created_at"`
	Details          map[string]any `json:"details"`
	Diff             *string        `json:"diff,omitempty"`

	ConfigID string `json:"configID,omitempty"`

	// UpdateExisting indicates whether to update an existing change
	UpdateExisting bool `json:"update_existing"`

	// For storing struct as map[string]any
	_map map[string]any `json:"-"`
}

func (r *ChangeResult) AsMap() map[string]any {
	if r._map != nil {
		return r._map
	}
	r._map = map[string]any{
		"external_id":        r.ExternalID,
		"config_type":        r.ConfigType,
		"external_change_id": r.ExternalChangeID,
		"action":             r.Action,
		"change_type":        r.ChangeType,
		"patches":            r.Patches,
		"summary":            r.Summary,
		"severity":           r.Severity,
		"source":             r.Source,
		"created_by":         r.CreatedBy,
		"created_at":         r.CreatedAt,
		"details":            r.Details,
		"diff":               r.Diff,
		"config_id":          r.ConfigID,
		"configID":           r.ConfigID, // config_id should be used, this for backward compatibility
		"update_existing":    r.UpdateExisting,
	}
	return r._map
}

func (r *ChangeResult) FlushMap() {
	r._map = nil
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
	if c.ConfigID == "" {
		return fmt.Sprintf("{%s/%s}, {%s/%s}", c.ConfigType, c.ExternalID, c.ChangeType, c.ExternalChangeID)
	}

	return fmt.Sprintf("{%s}, {%s/%s}", c.ConfigID, c.ChangeType, c.ExternalChangeID)
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

type ScrapeSummary map[string]ConfigTypeScrapeSummary

func (summary ScrapeSummary) HasUpdates() bool {
	totals := summary.Totals()
	return totals.Added > 0 || totals.Updated > 0

}

func (summary ConfigTypeScrapeSummary) String() string {
	s := []string{}

	if summary.Added > 0 {
		s = append(s, fmt.Sprintf("added=%d", summary.Added))
	}
	if summary.Updated > 0 {
		s = append(s, fmt.Sprintf("updated=%d", summary.Updated))
	}
	if summary.Unchanged > 0 {
		s = append(s, fmt.Sprintf("unchanged=%d", summary.Unchanged))
	}
	for _, w := range summary.Warnings {
		s = append(s, "warning=%s", w)
	}

	if summary.Change != nil && len(summary.Change.Ignored) > 0 {
		s = append(s, fmt.Sprintf("ignored=%d", lo.Sum(lo.Values(summary.Change.Ignored))))
	}
	if summary.Change != nil && len(summary.Change.Orphaned) > 0 {
		s = append(s, fmt.Sprintf("orphaned=%d", lo.Sum(lo.Values(summary.Change.Orphaned))))
	}

	return strings.Join(s, ", ")
}

func (s ScrapeSummary) String() string {
	types := lo.Keys(s)
	if len(types) <= 3 {
		return fmt.Sprintf("(%s) %v", types, s.Totals())

	}
	return fmt.Sprintf("types=%d, %v", len(types), s.Totals())
}

func (a ConfigTypeScrapeSummary) Merge(b ConfigTypeScrapeSummary) ConfigTypeScrapeSummary {
	change := &ChangeSummary{}
	if a.Change != nil {
		change.Merge(*a.Change)
	}
	if b.Change != nil {
		change.Merge(*b.Change)
	}
	return ConfigTypeScrapeSummary{
		Added:     a.Added + b.Added,
		Updated:   a.Updated + b.Updated,
		Unchanged: a.Unchanged + b.Unchanged,
		Change:    change,
	}
}

func (summaries ScrapeSummary) Totals() ConfigTypeScrapeSummary {
	merged := ConfigTypeScrapeSummary{
		Change: &ChangeSummary{},
	}

	for _, s := range summaries {
		merged = merged.Merge(s)
	}
	return merged
}

func (t *ScrapeSummary) AddChangeSummary(configType string, cs ChangeSummary) {
	v := (*t)[configType]
	v.Change = &ChangeSummary{
		Ignored:          cs.Ignored,
		Orphaned:         cs.Orphaned,
		ForeignKeyErrors: cs.ForeignKeyErrors,
	}
	(*t)[configType] = v
}

func (t *ScrapeSummary) AddInserted(configType string) {
	v := (*t)[configType]
	v.Added++
	(*t)[configType] = v
}

func (t *ScrapeSummary) AddUpdated(configType string) {
	v := (*t)[configType]
	v.Updated++
	(*t)[configType] = v
}

func (t *ScrapeSummary) AddUnchanged(configType string) {
	v := (*t)[configType]
	v.Unchanged++
	(*t)[configType] = v
}

func (t *ScrapeSummary) AddWarning(configType, warning string) {
	v := (*t)[configType]
	v.Warnings = append(v.Warnings, warning)
	(*t)[configType] = v
}

type ChangeSummary struct {
	Orphaned         map[string]int `json:"orphaned,omitempty"`
	Ignored          map[string]int `json:"ignored,omitempty"`
	ForeignKeyErrors int            `json:"foreign_key_errors,omitempty"`
}

func (t ChangeSummary) IsEmpty() bool {
	return len(t.Orphaned) == 0 && len(t.Ignored) == 0
}

func (t *ChangeSummary) AddOrphaned(typ string) {
	if t.Orphaned == nil {
		t.Orphaned = make(map[string]int)
	}
	t.Orphaned[typ] += 1
}

func (t *ChangeSummary) AddIgnored(typ string) {
	if t.Ignored == nil {
		t.Ignored = make(map[string]int)
	}
	t.Ignored[typ] += 1
}

func (t *ChangeSummary) Merge(b ChangeSummary) {
	if b.Orphaned != nil {
		if t.Orphaned == nil {
			t.Orphaned = make(map[string]int)
		}
		for k, v := range b.Orphaned {
			t.Orphaned[k] += v
		}
	}

	if b.Ignored != nil {
		if t.Ignored == nil {
			t.Ignored = make(map[string]int)
		}
		for k, v := range b.Ignored {
			t.Ignored[k] += v
		}
	}
}

func (t *ChangeSummary) Totals() (ignored, orphaned, errors int) {
	if t == nil {
		return 0, 0, 0
	}
	for _, v := range t.Ignored {
		ignored += v
	}
	for _, v := range t.Orphaned {
		orphaned += v
	}
	return ignored, orphaned, t.ForeignKeyErrors
}

type ChangeSummaryByType map[string]ChangeSummary

func (t *ChangeSummaryByType) Merge(typ string, b ChangeSummary) {
	v := (*t)[typ]
	v.Merge(b)
	(*t)[typ] = v
}

type ConfigTypeScrapeSummary struct {
	Added     int            `json:"added,omitempty"`
	Updated   int            `json:"updated,omitempty"`
	Unchanged int            `json:"unchanged,omitempty"`
	Change    *ChangeSummary `json:"change,omitempty"`
	Warnings  []string       `json:"warnings,omitempty"`
}

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

// Swap swaps the parent and child
func (t *RelationshipResult) Swap() {
	t.ConfigID, t.RelatedConfigID = t.RelatedConfigID, t.ConfigID
	t.ConfigExternalID, t.RelatedExternalID = t.RelatedExternalID, t.ConfigExternalID
}

func (t RelationshipResult) WithConfig(id string, ext ExternalID) RelationshipResult {
	if id != "" {
		t.ConfigID = id
	} else {
		t.ConfigExternalID = ext
	}
	return t
}

func (t RelationshipResult) WithRelated(id string, ext ExternalID) RelationshipResult {
	if id != "" {
		t.RelatedConfigID = id
	} else {
		t.RelatedExternalID = ext
	}
	return t
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

func (s *ScrapeResults) AddChange(base BaseScraper, change ChangeResult) *ScrapeResults {
	*s = append(*s, ScrapeResult{
		BaseScraper: base,
		Changes:     []ChangeResult{change},
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

func (t *Tags) Append(name, value string) {
	if t == nil {
		return
	}

	if value == "" {
		return
	}

	if *t == nil {
		*t = make(Tags, 0, 1)
	}

	*t = append(*t, Tag{Name: name, Value: value})
}

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
		return fmt.Errorf("too many tags (%d). only %d allowed", len(t), maxTagsCount)
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

func (t Tags) AsMap() map[string]string {
	if len(t) == 0 {
		return nil
	}

	output := make(map[string]string, len(t))
	for i := range t {
		output[t[i].Name] = t[i].Value
	}

	return output
}

func (t Tags) Eval(labels map[string]string, config string) (Tags, error) {
	if len(t) == 0 {
		return nil, nil
	}

	t = t.sanitize()
	if err := t.valid(); err != nil {
		return nil, err
	}

	for _, tag := range t {
		if tag.Value != "" {
			// do nothing
		} else if tag.Label != "" {
			if val, ok := labels[tag.Label]; ok {
				tag.Value = val
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
					tag.Value = fmt.Sprintf("%v", extractedValues[0])
				}
			}
		}
	}

	return t, nil
}

type ConfigExternalKey struct {
	ExternalID string
	Type       string
}

type DirectedRelationship struct {
	Selector duty.RelationshipSelector
	Parent   bool
}

// ScrapeResult ...
// +kubebuilder:object:generate=false
type ScrapeResult struct {
	types.NoOpResourceSelectable

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
	Health              models.Health       `json:"health,omitempty"`
	Ready               bool                `json:"ready,omitempty"`
	Name                string              `json:"name,omitempty"`
	Description         string              `json:"description,omitempty"`
	Aliases             []string            `json:"aliases,omitempty"`
	Source              string              `json:"source,omitempty"`
	Config              any                 `json:"config,omitempty"`
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
	Properties          types.Properties    `json:"properties,omitempty"`
	LastScrapedTime     *time.Time          `json:"last_scraped_time"`

	// ScraperLess when true indicates that this config item
	// does not belong to any scraper. Example: AWS region & availability zone.
	ScraperLess bool `json:"scraper_less,omitempty"`

	// List of candidate parents in order of precision.
	Parents []ConfigExternalKey `json:"-"`

	// List of children whose hard parent should be set to this config item.
	Children []ConfigExternalKey `json:"-"`

	// RelationshipSelectors are used to form relationship of this scraped item with other items.
	// Unlike `RelationshipResults`, selectors give you the flexibility to form relationship without
	// knowing the external ids of the item to be linked.
	RelationshipSelectors []DirectedRelationship `json:"-"`

	ExternalRoles      []models.ExternalRole      `json:"-"`
	ExternalUsers      []models.ExternalUser      `json:"-"`
	ExternalGroups     []models.ExternalGroup     `json:"-"`
	ExternalUserGroups []models.ExternalUserGroup `json:"-"`
	ConfigAccess       []ExternalConfigAccess     `json:"-"`
	ConfigAccessLogs   []ExternalConfigAccessLog  `json:"-"`

	// For storing struct as map[string]any
	_map map[string]any `json:"-"`
}

// +kubebuilder:object:generate=false
type ExternalConfigAccessLog struct {
	models.ConfigAccessLog
	ConfigExternalID ExternalID
}

// +kubebuilder:object:generate=false
type ExternalConfigAccess struct {
	models.ConfigAccess
	ConfigExternalID ExternalID
}

var _ types.ResourceSelectable = (*ScrapeResult)(nil)

func (e ScrapeResult) GetFieldsMatcher() fields.Fields {
	return types.GenericFieldMatcher{Fields: e.AsMap()}
}

func (s ScrapeResult) GetID() string {
	return s.ID
}

func (s ScrapeResult) GetName() string {
	return s.Name
}

func (s ScrapeResult) GetType() string {
	return s.Type
}

// SetHealthIfEmpty sets the health, status & readiness of the scrape result
// based on the config type.
func (s ScrapeResult) SetHealthIfEmpty() ScrapeResult {
	if s.Status == "" && s.Health == "" {
		s = s.WithHealthStatus(health.GetHealthByConfigType(s.Type, s.ConfigMap()))
		return s
	}

	if s.Health == "" {
		s.Health = models.HealthUnknown
	}

	return s
}

func (s ScrapeResult) WithHealthStatus(hs health.HealthStatus) ScrapeResult {
	s.Ready = hs.Ready

	if hs.Health != "" {
		s.Health = models.Health(hs.Health)
	}

	if hs.Status != "" {
		s.Status = string(hs.Status)
	}

	if hs.Message != "" {
		s.Description = hs.Message
	}

	return s
}

func (s *ScrapeResult) AsMap() map[string]any {
	if s._map != nil {
		return s._map
	}
	s._map = map[string]any{
		"id":                s.ID,
		"created_at":        s.CreatedAt,
		"deleted_at":        s.DeletedAt,
		"delete_reason":     s.DeleteReason,
		"last_modified":     s.LastModified,
		"config_class":      s.ConfigClass,
		"config_type":       s.Type,
		"status":            s.Status,
		"health":            s.Health,
		"ready":             s.Ready,
		"name":              s.Name,
		"description":       s.Description,
		"aliases":           s.Aliases,
		"source":            s.Source,
		"config":            s.Config, // TODO Change
		"format":            s.Format,
		"icon":              s.Icon,
		"labels":            s.Labels,
		"tags":              s.Tags.AsMap(),
		"action":            s.Action,
		"properties":        s.Properties.AsMap(),
		"last_scraped_time": s.LastScrapedTime,
		"scraper_less":      s.ScraperLess,
	}
	return s._map
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

func Ellipses(str string, length int) string {
	if len(str) > length {
		if len(str) < 3 || length < 3 {
			return str
		}
		return str[0:length/2] + "..." + str[len(str)-length/2-3:]
	}
	return str
}

func (r ScrapeResult) String() string {
	s := fmt.Sprintf("%s/%s (%s)", r.ConfigClass, Ellipses(r.Name, 40), Ellipses(r.ID, 40))

	if r.Health != models.HealthUnknown {
		s += fmt.Sprintf(" %s", r.Health)
	}

	if r.Status != "" {
		s += fmt.Sprintf(" %s", Ellipses(r.Status, 20))
	}

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
func (r ScrapeResult) ConfigString() string {

	// If config is of type string, try unmarshal first
	if v, ok := r.Config.(string); ok {
		return v
	}

	// Marshal and unmarshal into json for other types
	b, err := json.Marshal(r.Config)
	if err != nil {
		logger.Errorf("failed to marshal config: %v", err)
		return ""
	}

	return string(b)
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
