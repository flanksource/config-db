package v1

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/commons/collections/set"
	"github.com/flanksource/commons/har"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/flanksource/is-healthy/pkg/health"
	"github.com/ohler55/ojg/jp"
	"github.com/ohler55/ojg/oj"
	"github.com/samber/lo"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/fields"

	"github.com/flanksource/config-db/utils"
)

var ErrRateLimited = errors.New("rate limited")

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
	ExternalID      string              `json:"external_id,omitempty"`
	ConfigType      string              `json:"config_type,omitempty"`
	Summary         string              `json:"summary,omitempty"`
	Analysis        map[string]any      `json:"analysis,omitempty"`
	AnalysisType    models.AnalysisType `json:"analysis_type,omitempty"`
	Severity        models.Severity     `json:"severity,omitempty"`
	Source          string              `json:"source,omitempty"`
	Analyzer        string              `json:"analyzer,omitempty"`
	Messages        []string            `json:"messages,omitempty"`
	Status          string              `json:"status,omitempty"`
	FirstObserved   *time.Time          `json:"first_observed,omitempty"`
	LastObserved    *time.Time          `json:"last_observed,omitempty"`
	Error           error               `json:"-"`
	ExternalConfigs []ExternalID        `json:"external_configs,omitempty"`
}

// ToConfigAnalysis converts this analysis result to a config analysis
// db model.
func (t *AnalysisResult) ToConfigAnalysis() models.ConfigAnalysis {
	return models.ConfigAnalysis{
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

	// AncestorType is set by move-up/copy-up change mappings to specify
	// which ancestor config type to target during parent chain traversal.
	AncestorType string `json:"-"`

	// Target is set by copy/move change mappings to specify the config item
	// selector for resolving target config items.
	Target *duty.RelationshipSelectorTemplate `json:"-"`

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

// +kubebuilder:object:generate=false
type EntitySummary struct {
	Scraped int `json:"scraped,omitempty"`
	Saved   int `json:"saved,omitempty"`
	Skipped int `json:"skipped,omitempty"`
	Deleted int `json:"deleted,omitempty"`
}

func (e EntitySummary) IsEmpty() bool {
	return e.Scraped == 0 && e.Saved == 0 && e.Skipped == 0 && e.Deleted == 0
}

func (e EntitySummary) Merge(other EntitySummary) EntitySummary {
	return EntitySummary{
		Scraped: e.Scraped + other.Scraped,
		Saved:   e.Saved + other.Saved,
		Skipped: e.Skipped + other.Skipped,
		Deleted: e.Deleted + other.Deleted,
	}
}

func (e EntitySummary) Pretty() api.Text {
	t := clicky.Text("")
	if e.Scraped > 0 {
		t = t.Appendf("%d", e.Scraped).AddText(" scraped", "muted")
	}
	if e.Saved > 0 {
		t = t.Space().Appendf("%d", e.Saved).AddText(" saved", "success")
	}
	if e.Skipped > 0 {
		t = t.Space().Appendf("%d", e.Skipped).AddText(" skipped", "warning")
	}
	if e.Deleted > 0 {
		t = t.Space().Appendf("%d", e.Deleted).AddText(" deleted", "error")
	}
	return t
}

func (e EntitySummary) String() string {
	return e.Pretty().String()
}

// +kubebuilder:object:generate=false
type ScrapeSummary struct {
	ConfigTypes    map[string]ConfigTypeScrapeSummary `json:"config_types,omitempty"`
	ExternalUsers  EntitySummary                      `json:"external_users,omitempty"`
	ExternalGroups EntitySummary                      `json:"external_groups,omitempty"`
	ExternalRoles  EntitySummary                      `json:"external_roles,omitempty"`
	ConfigAccess   EntitySummary                      `json:"config_access,omitempty"`
	AccessLogs     EntitySummary                      `json:"access_logs,omitempty"`
}

func NewScrapeSummary() ScrapeSummary {
	return ScrapeSummary{ConfigTypes: make(map[string]ConfigTypeScrapeSummary)}
}

func (summary ScrapeSummary) HasUpdates() bool {
	totals := summary.Totals()
	if totals.Added > 0 || totals.Updated > 0 || totals.Changes > 0 || totals.Deduped > 0 {
		return true
	}
	return !summary.ExternalUsers.IsEmpty() ||
		!summary.ExternalGroups.IsEmpty() ||
		!summary.ExternalRoles.IsEmpty() ||
		!summary.ConfigAccess.IsEmpty() ||
		!summary.AccessLogs.IsEmpty()
}

func (s *ScrapeSummary) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	// Detect new-format payloads by checking for any known top-level key
	newFormatKeys := []string{"config_types", "external_users", "external_groups", "external_roles", "config_access", "access_logs"}
	isNewFormat := false
	for _, key := range newFormatKeys {
		if _, ok := raw[key]; ok {
			isNewFormat = true
			break
		}
	}

	if isNewFormat {
		type Alias ScrapeSummary
		var a Alias
		if err := json.Unmarshal(data, &a); err != nil {
			return err
		}
		*s = ScrapeSummary(a)
		return nil
	}

	// Legacy format: treat entire payload as map[string]ConfigTypeScrapeSummary
	var m map[string]ConfigTypeScrapeSummary
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	s.ConfigTypes = m
	return nil
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
	if summary.Changes > 0 {
		s = append(s, fmt.Sprintf("changes=%d", summary.Changes))
	}
	if summary.Deduped > 0 {
		s = append(s, fmt.Sprintf("deduped=%d", summary.Deduped))
	}
	for _, w := range summary.Warnings {
		s = append(s, fmt.Sprintf("warning=%s", w))
	}

	if summary.Change != nil && len(summary.Change.Ignored) > 0 {
		s = append(s, fmt.Sprintf("ignored=%d", lo.Sum(lo.Values(summary.Change.Ignored))))
	}
	if summary.Change != nil && len(summary.Change.Orphaned) > 0 {
		total := 0
		for _, orphaned := range summary.Change.Orphaned {
			total += orphanedCount(orphaned)
		}
		s = append(s, fmt.Sprintf("orphaned=%d", total))
	}

	return strings.Join(s, ", ")
}

func (s ScrapeSummary) String() string {
	types := lo.Keys(s.ConfigTypes)
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
		Changes:   a.Changes + b.Changes,
		Deduped:   a.Deduped + b.Deduped,
		Change:    change,
	}
}

func (s ScrapeSummary) Totals() ConfigTypeScrapeSummary {
	merged := ConfigTypeScrapeSummary{
		Change: &ChangeSummary{},
	}

	for _, v := range s.ConfigTypes {
		merged = merged.Merge(v)
	}
	return merged
}

func (t *ScrapeSummary) initConfigTypes() {
	if t.ConfigTypes == nil {
		t.ConfigTypes = make(map[string]ConfigTypeScrapeSummary)
	}
}

func (t *ScrapeSummary) AddChangeSummary(configType string, cs ChangeSummary) {
	t.initConfigTypes()
	v := t.ConfigTypes[configType]
	v.Change = &ChangeSummary{
		Ignored:          cs.Ignored,
		IgnoredByAction:  cs.IgnoredByAction,
		Orphaned:         cs.Orphaned,
		ForeignKeyErrors: cs.ForeignKeyErrors,
	}
	t.ConfigTypes[configType] = v
}

func (t *ScrapeSummary) AddInserted(configType string) {
	t.initConfigTypes()
	v := t.ConfigTypes[configType]
	v.Added++
	t.ConfigTypes[configType] = v
}

func (t *ScrapeSummary) AddUpdated(configType string) {
	t.initConfigTypes()
	v := t.ConfigTypes[configType]
	v.Updated++
	t.ConfigTypes[configType] = v
}

func (t *ScrapeSummary) AddUnchanged(configType string) {
	t.initConfigTypes()
	v := t.ConfigTypes[configType]
	v.Unchanged++
	t.ConfigTypes[configType] = v
}

func (t *ScrapeSummary) AddWarning(configType, warning string) {
	t.initConfigTypes()
	v := t.ConfigTypes[configType]
	v.Warnings = append(v.Warnings, warning)
	t.ConfigTypes[configType] = v
}

func (t *ScrapeSummary) AddChanges(configType string, count int) {
	t.initConfigTypes()
	v := t.ConfigTypes[configType]
	v.Changes += count
	t.ConfigTypes[configType] = v
}

func (t *ScrapeSummary) AddDeduped(configType string, count int) {
	t.initConfigTypes()
	v := t.ConfigTypes[configType]
	v.Deduped += count
	t.ConfigTypes[configType] = v
}

func (s *ScrapeSummary) Merge(other ScrapeSummary) {
	s.initConfigTypes()
	for k, v := range other.ConfigTypes {
		if existing, ok := s.ConfigTypes[k]; ok {
			s.ConfigTypes[k] = existing.Merge(v)
		} else {
			s.ConfigTypes[k] = v
		}
	}
	s.ExternalUsers = s.ExternalUsers.Merge(other.ExternalUsers)
	s.ExternalGroups = s.ExternalGroups.Merge(other.ExternalGroups)
	s.ExternalRoles = s.ExternalRoles.Merge(other.ExternalRoles)
	s.ConfigAccess = s.ConfigAccess.Merge(other.ConfigAccess)
	s.AccessLogs = s.AccessLogs.Merge(other.AccessLogs)
}

// +kubebuilder:object:generate=false
type ChangeSummary struct {
	Orphaned         map[string]OrphanedChanges `json:"orphaned,omitempty"`
	Ignored          map[string]int             `json:"ignored,omitempty"`
	IgnoredByAction  map[string]map[string]int  `json:"ignored_by_action,omitempty"` // action -> change_type -> count
	ForeignKeyErrors int                        `json:"foreign_key_errors,omitempty"`
}

// +kubebuilder:object:generate=false
type OrphanedChanges struct {
	IDs   set.Set[string] `json:"ids,omitempty"`
	Count int             `json:"count,omitempty"`
}

func (t ChangeSummary) IsEmpty() bool {
	return len(t.Orphaned) == 0 && len(t.Ignored) == 0
}

func (t *ChangeSummary) AddOrphaned(typ, id string) {
	if t.Orphaned == nil {
		t.Orphaned = make(map[string]OrphanedChanges)
	}
	orphaned := t.Orphaned[typ]
	orphaned.Count++
	if id != "" {
		if orphaned.IDs == nil {
			orphaned.IDs = set.New[string]()
		}
		orphaned.IDs.Add(id)
	}
	t.Orphaned[typ] = orphaned
}

func (t *ChangeSummary) AddIgnored(typ string) {
	if t.Ignored == nil {
		t.Ignored = make(map[string]int)
	}
	t.Ignored[typ] += 1
}

func (t *ChangeSummary) AddIgnoredByAction(action, changeType string) {
	if t.IgnoredByAction == nil {
		t.IgnoredByAction = make(map[string]map[string]int)
	}
	if t.IgnoredByAction[action] == nil {
		t.IgnoredByAction[action] = make(map[string]int)
	}
	t.IgnoredByAction[action][changeType] += 1
}

func (t *ChangeSummary) Merge(b ChangeSummary) {
	if b.Orphaned != nil {
		if t.Orphaned == nil {
			t.Orphaned = make(map[string]OrphanedChanges)
		}
		for k, v := range b.Orphaned {
			orphaned := t.Orphaned[k]
			orphaned.Count += orphanedCount(v)
			if v.IDs != nil {
				if orphaned.IDs == nil {
					orphaned.IDs = set.New[string]()
				}
				for _, id := range v.IDs.ToSlice() {
					orphaned.IDs.Add(id)
				}
			}
			t.Orphaned[k] = orphaned
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

	if b.IgnoredByAction != nil {
		if t.IgnoredByAction == nil {
			t.IgnoredByAction = make(map[string]map[string]int)
		}
		for action, changeTypes := range b.IgnoredByAction {
			if t.IgnoredByAction[action] == nil {
				t.IgnoredByAction[action] = make(map[string]int)
			}
			for changeType, count := range changeTypes {
				t.IgnoredByAction[action][changeType] += count
			}
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
	for _, changeTypes := range t.IgnoredByAction {
		for _, count := range changeTypes {
			ignored += count
		}
	}
	for _, v := range t.Orphaned {
		orphaned += orphanedCount(v)
	}
	return ignored, orphaned, t.ForeignKeyErrors
}

func orphanedCount(orphaned OrphanedChanges) int {
	if orphaned.Count > 0 {
		return orphaned.Count
	}
	if orphaned.IDs == nil {
		return 0
	}
	return len(orphaned.IDs.ToSlice())
}

// +kubebuilder:object:generate=false
type ChangeSummaryByType map[string]ChangeSummary

func (t *ChangeSummaryByType) Merge(typ string, b ChangeSummary) {
	v := (*t)[typ]
	v.Merge(b)
	(*t)[typ] = v
}

// +kubebuilder:object:generate=false
type ConfigTypeScrapeSummary struct {
	Added     int            `json:"added,omitempty"`
	Updated   int            `json:"updated,omitempty"`
	Unchanged int            `json:"unchanged,omitempty"`
	Changes   int            `json:"changes,omitempty"`
	Deduped   int            `json:"deduped,omitempty"`
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

func (s *ScrapeResults) RateLimited(msg string, resetAt *time.Time) ScrapeResults {
	logger.Warnf("rate limited: %s (reset at %v)", msg, resetAt)
	*s = append(*s, ScrapeResult{
		Error:            fmt.Errorf("%s: %w", msg, ErrRateLimited),
		RateLimitResetAt: resetAt,
	})
	return *s
}

func (t ScrapeResults) IsRateLimited() bool {
	for _, r := range t {
		if errors.Is(r.Error, ErrRateLimited) {
			return true
		}
	}
	return false
}

func (t ScrapeResults) GetRateLimitResetAt() *time.Time {
	for _, r := range t {
		if r.RateLimitResetAt != nil {
			return r.RateLimitResetAt
		}
	}
	return nil
}

func (s *ScrapeResults) Errorf(e error, msg string, args ...any) ScrapeResults {
	errMsg := fmt.Sprintf(msg, args...)
	logger.Errorf("%s: %v", errMsg, e)
	*s = append(*s, ScrapeResult{Error: fmt.Errorf("%s: %w", errMsg, e)})
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
	ScraperID  string
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
	Locations           []string            `json:"locations,omitempty"`
	Format              string              `json:"format,omitempty"`
	Icon                string              `json:"icon,omitempty"`
	Labels              JSONStringMap       `json:"labels,omitempty"`
	Tags                JSONStringMap       `json:"tags,omitempty"`
	BaseScraper         BaseScraper         `json:"-"`
	Error               error               `json:"-"`
	AnalysisResult      *AnalysisResult     `json:"analysis,omitempty"`
	Changes             []ChangeResult      `json:"-"`
	RelationshipResults RelationshipResults `json:"-"`
	Ignore              []string            `json:"-"`
	Action              string              `json:",omitempty"`
	Properties          types.Properties    `json:"properties,omitempty"`

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

	RateLimitResetAt *time.Time `json:"-"`

	// For storing struct as map[string]any
	_map map[string]any `json:"-"`

	// OmitNilFields lets post-processor know whether to omit nil fields
	// inside config or not. Default is true
	OmitNilFields *bool `json:"-"`

	// Only for GCP Scraper
	GCPStructPB *structpb.Struct `json:"-"`
}

func (s ScrapeResult) Debug() api.Text {
	t := clicky.Text("")

	t = t.Append("ID: ", "text-muted").Append(s.ID)
	t = t.Append(" Name: ", "text-muted").Append(s.Name)
	t = t.Append(" Type: ", "text-muted").Append(s.Type)
	t = t.Append(" Status: ", "text-muted").Append(s.Status)
	if s.Health != "" && s.Health != models.HealthUnknown {
		t = t.Append(" Health: ", "text-muted").Append(string(s.Health))
	}
	if s.Source != "" {
		t = t.NewLine().Append("Source: ", "text-muted").Append(s.Source)
	}
	if s.ScraperLess {
		t = t.NewLine().Append("Scraper Less: ", "text-muted").Append("true")
	}
	if s.Icon != "" {
		t = t.NewLine().Append("Icon: ", "text-muted").Append(s.Icon)
	}

	if s.Error != nil {
		t = t.Append(" Error: ", "text-red-500").Append(s.Error.Error())
	}
	if len(s.Aliases) > 0 {
		t = t.Append(" Aliases: ", "text-muted").Append(strings.Join(s.Aliases, ", "))
	}
	if s.Description != "" {
		t = t.NewLine().Append("Description: ", "text-muted").Append(s.Description)
	}

	if len(s.Locations) > 0 {
		t = t.NewLine().Append("Locations: ", "text-muted").Append(strings.Join(s.Locations, ", "))
	}

	if len(s.Labels) > 0 {
		t = t.NewLine().Append("Labels: ", "text-muted").Append(clicky.Map(s.Labels))
	}
	if len(s.Tags) > 0 {
		t = t.NewLine().Append("Tags: ", "text-muted").Append(clicky.Map(s.Tags))
	}
	if len(s.Properties) > 0 {
		t = t.NewLine().Append("Properties: ", "text-muted").Append(clicky.Map(s.Properties.AsMap()))
	}

	if len(s.Changes) > 0 {
		t = t.NewLine().Append("Changes: ", "text-muted")
		for _, change := range s.Changes {
			t = t.NewLine().Append(fmt.Sprintf(" - %s: %s", change.ChangeType, change.Summary))
			if len(change.Details) > 0 {
				t = t.NewLine().Append(clicky.Map(change.Details, "max-w-[100ch]")).NewLine()
			}
		}
	}

	switch v := s.Config.(type) {
	case string:
		if s.Format == "json" || s.Format == "" {
			t = t.NewLine().Append(clicky.CodeBlock("json", v)).NewLine()
		} else {
			t = t.NewLine().Append(clicky.CodeBlock(s.Format, v)).NewLine()
		}
	case map[string]any:
		t = t.NewLine().Append(clicky.Map(v), "max-w-[100ch]").NewLine()
	case map[string]string:
		t = t.NewLine().Append(clicky.Map(v), "max-w-[100ch]").NewLine()
	default:
		t = t.NewLine().Append(fmt.Sprintf("%v", v)).NewLine()
	}

	return t
}

func (s ScrapeResult) Columns() []api.ColumnDef {
	return []api.ColumnDef{
		clicky.Column("ID").Build(),
		clicky.Column("Name").Build(),
		clicky.Column("Type").Build(),
		clicky.Column("Health").Build(),
		clicky.Column("Details").Build(),
		clicky.Column("Error").Build(),
	}

}
func (s ScrapeResult) Row() map[string]any {
	row := make(map[string]any)
	row["ID"] = clicky.Text(s.ID)
	row["Name"] = clicky.Text(s.Name)
	row["Type"] = clicky.Text(s.Type)
	row["Health"] = clicky.Text(string(s.Health))
	row["Details"] = s.configDetails()
	if s.Error != nil {
		row["Error"] = clicky.Text(s.Error.Error())
	} else {
		row["Error"] = clicky.Text("")
	}
	return row
}

func (s ScrapeResult) configDetails() api.Collapsed {
	if s.Config == nil {
		return clicky.Collapsed("empty", clicky.Text(""))
	}

	var data any
	switch v := s.Config.(type) {
	case string:
		if json.Unmarshal([]byte(v), &data) != nil {
			data = v
		}
	default:
		data = v
	}

	b, err := yaml.Marshal(data)
	if err != nil {
		b = []byte(fmt.Sprintf("%v", s.Config))
	}
	yamlStr := string(b)

	content := clicky.Text("")
	if len(s.Labels) > 0 {
		content = content.Append("Labels: ", "text-gray-500 font-medium").Append(clicky.Map(s.Labels, "badge")).NewLine()
	}
	if len(s.Tags) > 0 {
		content = content.Append("Tags: ", "text-gray-500 font-medium").Append(clicky.Map(s.Tags, "badge")).NewLine()
	}
	content = content.Append(clicky.CodeBlock("yaml", yamlStr), "min-w-[600px] block")

	label := fmt.Sprintf("Config (%d bytes)", len(yamlStr))
	return clicky.Collapsed(label, content)
}

// CountsGrid renders scrape result counts as a 2-column grid.
// +kubebuilder:object:generate=false
type CountsGrid []countEntry

type countEntry struct {
	label string
	count int
}

func (g CountsGrid) HTML() string {
	var b strings.Builder
	b.WriteString(`<div class="grid grid-cols-2 gap-x-8 gap-y-2">`)
	for _, e := range g {
		fmt.Fprintf(&b,
			`<div class="flex justify-between px-3 py-1 bg-gray-50 rounded">`+
				`<span class="text-sm font-medium text-gray-500">%s</span>`+
				`<span class="text-sm text-gray-900">%d</span>`+
				`</div>`, e.label, e.count)
	}
	b.WriteString(`</div>`)
	return b.String()
}

func (g CountsGrid) String() string {
	var parts []string
	for _, e := range g {
		parts = append(parts, fmt.Sprintf("%s: %d", e.label, e.count))
	}
	return strings.Join(parts, ", ")
}

func (g CountsGrid) ANSI() string     { return g.String() }
func (g CountsGrid) Markdown() string { return g.String() }

// BuildCounts returns scrape result counts as a 2-column grid.
func BuildCounts(all FullScrapeResults) CountsGrid {
	return CountsGrid{
		{"Configs", len(all.Configs)},
		{"Analysis", len(all.Analysis)},
		{"Changes", len(all.Changes)},
		{"Relationships", len(all.Relationships)},
		{"External Roles", len(all.ExternalRoles)},
		{"External Users", len(all.ExternalUsers)},
		{"External Groups", len(all.ExternalGroups)},
		{"External User Groups", len(all.ExternalUserGroups)},
		{"Config Access", len(all.ConfigAccess)},
		{"Config Access Logs", len(all.ConfigAccessLogs)},
	}
}

var ansiEscapeRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// LogLine is a single log entry that renders as a table row with colored level prefix.
// +kubebuilder:object:generate=false
type LogLine struct {
	text api.Text
}

func (l LogLine) Columns() []api.ColumnDef {
	return []api.ColumnDef{
		clicky.Column("Line").Build(),
	}
}

func (l LogLine) Row() map[string]any {
	return map[string]any{"Line": l.text}
}

// BuildLogLines parses raw log text into LogLine rows for table rendering.
func BuildLogLines(rawLogs string) []LogLine {
	cleaned := ansiEscapeRegex.ReplaceAllString(rawLogs, "")
	lines := strings.Split(strings.TrimRight(cleaned, "\n"), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return nil
	}

	out := make([]LogLine, 0, len(lines))
	for _, line := range lines {
		out = append(out, LogLine{text: colorLogLine(line)})
	}
	return out
}

var logLevelRegex = regexp.MustCompile(`\s(INF|ERR|WRN|DBG\S*|TRC\S*|FTL)\s`)

var logLevelColors = map[string]string{
	"INF": "text-green-600",
	"ERR": "text-red-600",
	"WRN": "text-yellow-600",
	"DBG": "text-blue-600",
	"TRC": "text-gray-500",
	"FTL": "text-red-600",
}

// colorLogLine highlights the log level prefix with appropriate colors.
// Matches DBG, DBG-1, TRC-2, etc.
func colorLogLine(line string) api.Text {
	loc := logLevelRegex.FindStringIndex(line)
	if loc == nil {
		return clicky.Text(line)
	}

	// loc covers the match including surrounding spaces
	tag := strings.TrimSpace(line[loc[0]:loc[1]])
	before := line[:loc[0]+1]
	after := line[loc[1]-1:]

	// Base level is the prefix before any dash (DBG-1 → DBG)
	base := tag
	if i := strings.IndexByte(tag, '-'); i >= 0 {
		base = tag[:i]
	}
	color := logLevelColors[base]

	return clicky.Text(before).Append(tag, color).Append(after)
}

// HAREntry renders a single HAR request/response as a table row.
// +kubebuilder:object:generate=false
type HAREntry struct {
	Method   string
	URL      string
	Status   int
	Duration string
}

func (h HAREntry) Columns() []api.ColumnDef {
	return []api.ColumnDef{
		clicky.Column("Method").Build(),
		clicky.Column("URL").Build(),
		clicky.Column("Status").Build(),
		clicky.Column("Duration").Build(),
	}
}

func (h HAREntry) Row() map[string]any {
	statusStyle := "text-green-600"
	if h.Status >= 400 {
		statusStyle = "text-red-600"
	} else if h.Status >= 300 {
		statusStyle = "text-yellow-600"
	}
	return map[string]any{
		"Method":   clicky.Text(h.Method),
		"URL":      clicky.Text(h.URL),
		"Status":   clicky.Text(fmt.Sprintf("%d", h.Status)).WithStyles(statusStyle),
		"Duration": clicky.Text(h.Duration),
	}
}

// BuildHAREntries converts HAR entries into table rows.
func BuildHAREntries(entries []har.Entry) []HAREntry {
	out := make([]HAREntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, HAREntry{
			Method:   e.Request.Method,
			URL:      e.Request.URL,
			Status:   e.Response.Status,
			Duration: fmt.Sprintf("%.0fms", e.Time),
		})
	}
	return out
}

func (s ScrapeResult) IsMetadataOnly() bool {
	return s.Config == nil
}

func (s ScrapeResult) Pretty() api.Text {
	t := clicky.Text("")

	t = t.Append("ID: ", "text-muted").Append(s.ID)
	t = t.Append(" Name: ", "text-muted").Append(s.Name)
	t = t.Append(" Type: ", "text-muted").Append(s.Type)
	if len(s.Tags) > 0 {
		t = t.NewLine().Append("Tags: ", "text-muted").Append(clicky.Map(s.Tags))
	}

	return t
}

// +kubebuilder:object:generate=false
type ExternalConfigAccessLog struct {
	models.ConfigAccessLog
	ConfigExternalID ExternalID `json:"external_config_id,omitempty"`
}

// +kubebuilder:object:generate=false
type ExternalConfigAccess struct {
	models.ConfigAccess
	ConfigExternalID     ExternalID `json:"external_config_id"`
	ExternalUserAliases  []string   `json:"external_user_aliases"`
	ExternalRoleAliases  []string   `json:"external_role_aliases"`
	ExternalGroupAliases []string   `json:"external_group_aliases"`
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

func (s ScrapeResult) OmitNil() bool {
	// If unset, always omit nil
	if s.OmitNilFields == nil {
		return true
	}
	return lo.FromPtr(s.OmitNilFields)
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
		"id":            s.ID,
		"created_at":    s.CreatedAt,
		"deleted_at":    s.DeletedAt,
		"delete_reason": s.DeleteReason,
		"last_modified": s.LastModified,
		"config_class":  s.ConfigClass,
		"config_type":   s.Type,
		"status":        s.Status,
		"health":        s.Health,
		"ready":         s.Ready,
		"name":          s.Name,
		"description":   s.Description,
		"aliases":       s.Aliases,
		"source":        s.Source,
		"config":        s.Config, // TODO Change
		"format":        s.Format,
		"icon":          s.Icon,
		"labels":        s.Labels,
		"tags":          s.Tags,
		"action":        s.Action,
		"properties":    s.Properties.AsMap(),
		"scraper_less":  s.ScraperLess,
	}
	return s._map
}

func NewScrapeResult(base BaseScraper) *ScrapeResult {
	return &ScrapeResult{
		BaseScraper: base,
		Format:      base.Format,
		Tags:        base.Tags.AsMap(),
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

// +kubebuilder:object:generate=false
type FullScrapeResults struct {
	Configs            []ScrapeResult              `json:"configs,omitempty"`
	Analysis           []models.ConfigAnalysis     `json:"analysis,omitempty"`
	Changes            []models.ConfigChange       `json:"changes,omitempty"`
	Relationships      []models.ConfigRelationship `json:"relationships,omitempty"`
	ExternalRoles      []models.ExternalRole       `json:"external_roles,omitempty"`
	ExternalUsers      []models.ExternalUser       `json:"external_users,omitempty"`
	ExternalGroups     []models.ExternalGroup      `json:"external_groups,omitempty"`
	ExternalUserGroups []models.ExternalUserGroup  `json:"external_user_groups,omitempty"`
	ConfigAccess       []ExternalConfigAccess      `json:"config_access,omitempty"`
	ConfigAccessLogs   []ExternalConfigAccessLog   `json:"config_access_logs,omitempty"`
}

func MergeScrapeResults(results ...ScrapeResults) FullScrapeResults {
	full := FullScrapeResults{}
	for _, res := range results {
		for _, r := range res {
			if r.Error != nil {
				continue
			}

			if r.AnalysisResult != nil {
				full.Analysis = append(full.Analysis, r.AnalysisResult.ToConfigAnalysis())
			}

			for _, change := range r.Changes {
				configChange := models.ConfigChange{
					ChangeType:        change.ChangeType,
					Severity:          models.Severity(change.Severity),
					Source:            change.Source,
					Summary:           change.Summary,
					CreatedAt:         change.CreatedAt,
					ExternalChangeID:  lo.ToPtr(change.ExternalChangeID),
					ExternalID:        change.ExternalID,
					ConfigType:        change.ConfigType,
					Diff:              lo.FromPtr(change.Diff),
					Patches:           change.Patches,
					ExternalCreatedBy: change.CreatedBy,
				}
				if change.Details != nil {
					if detailsJSON, err := json.Marshal(change.Details); err == nil {
						configChange.Details = detailsJSON
					}
				}
				full.Changes = append(full.Changes, configChange)
			}

			for _, rel := range r.RelationshipResults {
				full.Relationships = append(full.Relationships, models.ConfigRelationship{
					ConfigID:  rel.ConfigID,
					RelatedID: rel.RelatedConfigID,
					Relation:  rel.Relationship,
				})
			}

			if !r.IsMetadataOnly() {
				full.Configs = append(full.Configs, r)
			}

			full.ExternalRoles = append(full.ExternalRoles, r.ExternalRoles...)
			full.ExternalUsers = append(full.ExternalUsers, r.ExternalUsers...)
			full.ExternalGroups = append(full.ExternalGroups, r.ExternalGroups...)
			full.ExternalUserGroups = append(full.ExternalUserGroups, r.ExternalUserGroups...)
			full.ConfigAccess = append(full.ConfigAccess, r.ConfigAccess...)
			full.ConfigAccessLogs = append(full.ConfigAccessLogs, r.ConfigAccessLogs...)
		}
	}
	return full
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
