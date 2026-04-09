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
	Type    string          `json:"type"`              // "orphaned", "fk_error", "ignored"
	Message string          `json:"message,omitempty"`
	Change  *v1.ChangeResult `json:"change,omitempty"` // Full change details when applicable
}

type Snapshot struct {
	Scrapers      []ScraperProgress        `json:"scrapers"`
	Results       v1.FullScrapeResults     `json:"results"`
	Relationships []UIRelationship         `json:"relationships,omitempty"`
	ConfigMeta    map[string]ConfigMeta    `json:"config_meta,omitempty"`
	Issues        []ScrapeIssue            `json:"issues,omitempty"`
	Counts        Counts                   `json:"counts"`
	SaveSummary   *SaveSummary             `json:"save_summary,omitempty"`
	ScrapeSpec    any                      `json:"scrape_spec,omitempty"`
	HAR           []har.Entry              `json:"har,omitempty"`
	Logs          string                   `json:"logs"`
	Done          bool                     `json:"done"`
	StartedAt     int64                    `json:"started_at"`
}
