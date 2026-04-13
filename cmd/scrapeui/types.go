package scrapeui

import (
	"time"

	"github.com/flanksource/commons/har"
	v1 "github.com/flanksource/config-db/api/v1"
)

type ScraperStatus string

const (
	ScraperPending  ScraperStatus = "pending"
	ScraperRunning  ScraperStatus = "running"
	ScraperComplete ScraperStatus = "complete"
	ScraperError    ScraperStatus = "error"
)

type ScraperProgress struct {
	Name        string        `json:"name"`
	Status      ScraperStatus `json:"status"`
	StartedAt   *time.Time    `json:"started_at,omitempty"`
	DurationSec float64       `json:"duration_secs,omitempty"`
	Error       string        `json:"error,omitempty"`
	ResultCount int           `json:"result_count"`
}

type Counts struct {
	Configs        int `json:"configs"`
	Changes        int `json:"changes"`
	Analysis       int `json:"analysis"`
	Relationships  int `json:"relationships"`
	ExternalUsers  int `json:"external_users"`
	ExternalGroups int `json:"external_groups"`
	ExternalRoles  int `json:"external_roles"`
	ConfigAccess   int `json:"config_access"`
	AccessLogs     int `json:"access_logs"`
	Errors         int `json:"errors"`
}

type SaveSummary struct {
	ConfigTypes map[string]TypeSummary `json:"config_types,omitempty"`
}

type TypeSummary struct {
	Added     int `json:"added"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Changes   int `json:"changes"`
}

// UIRelationship is a frontend-friendly relationship that uses
// external IDs and resolved names instead of internal DB UUIDs.
type UIRelationship struct {
	ConfigExternalID  string `json:"config_id"`
	RelatedExternalID string `json:"related_id"`
	Relation          string `json:"relation"`
	ConfigName        string `json:"config_name,omitempty"`
	RelatedName       string `json:"related_name,omitempty"`
}

// ConfigMeta carries resolved metadata (parents, locations) per config external ID.
type ConfigMeta struct {
	Parents  []string `json:"parents,omitempty"`
	Location string   `json:"location,omitempty"`
}

// ScrapeIssue represents an orphaned change, FK error, or other pipeline issue.
type ScrapeIssue struct {
	Type    string           `json:"type"`              // "orphaned", "fk_error", "warning"
	Message string           `json:"message,omitempty"`
	Change  *v1.ChangeResult `json:"change,omitempty"`
	Warning *v1.Warning      `json:"warning,omitempty"`
}

// PropertyInfo is a UI-friendly representation of a resolved property.
type PropertyInfo struct {
	Value   any    `json:"value,omitempty"`
	Default any    `json:"default,omitempty"`
	Type    string `json:"type,omitempty"`
}

// LogLevelInfo carries the effective log levels for display in the Spec tab.
type LogLevelInfo struct {
	Scraper string `json:"scraper,omitempty"`
	Global  string `json:"global,omitempty"`
}

// BuildInfo carries the build-time version/commit/date for display in the
// scrape UI. Populated by the server at startup from the cmd package.
type BuildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

type Snapshot struct {
	Scrapers      []ScraperProgress                 `json:"scrapers"`
	Results       v1.FullScrapeResults              `json:"results"`
	Relationships []UIRelationship                  `json:"relationships,omitempty"`
	ConfigMeta    map[string]ConfigMeta             `json:"config_meta,omitempty"`
	Issues        []ScrapeIssue                     `json:"issues,omitempty"`
	Counts        Counts                            `json:"counts"`
	SaveSummary   *SaveSummary                      `json:"save_summary,omitempty"`
	Snapshots     map[string]*v1.ScrapeSnapshotPair `json:"snapshots,omitempty"`
	ScrapeSpec    any                               `json:"scrape_spec,omitempty"`
	Properties    map[string]PropertyInfo            `json:"properties,omitempty"`
	LogLevel      *LogLevelInfo                      `json:"log_level,omitempty"`
	HAR                []har.Entry                    `json:"har,omitempty"`
	Logs               string                       `json:"logs"`
	Done               bool                         `json:"done"`
	StartedAt          int64                         `json:"started_at"`
	BuildInfo          *BuildInfo                    `json:"build_info,omitempty"`
	LastScrapeSummary  *v1.ScrapeSummary             `json:"last_scrape_summary,omitempty"`
}
