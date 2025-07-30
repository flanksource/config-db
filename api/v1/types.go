package v1

import (
	"fmt"
	"slices"
	"strings"

	"github.com/flanksource/config-db/utils"

	"github.com/google/uuid"

	"gorm.io/gorm"
)

var AllScraperConfigs = map[string]any{
	"aws":            AWS{},
	"azure":          Azure{},
	"azuredevops":    AzureDevops{},
	"file":           File{},
	"gcp":            GCP{},
	"githubactions":  GitHubActions{},
	"http":           HTTP{},
	"kubernetes":     Kubernetes{},
	"kubernetesfile": KubernetesFile{},
	"logs":           Logs{},
	"slack":          Slack{},
	"sql":            SQL{},
	"terraform":      Terraform{},
	"trivy":          Trivy{},
}

type ChangeRetentionSpec struct {
	Name  string `json:"name,omitempty"`
	Age   string `json:"age,omitempty"`
	Count int    `json:"count,omitempty"`
}

type TypeRetentionSpec struct {
	Name       string `json:"name,omitempty"`
	CreatedAge string `json:"createdAge,omitempty"`
	UpdatedAge string `json:"updatedAge,omitempty"`
	DeletedAge string `json:"deletedAge,omitempty"`
}

type RetentionSpec struct {
	Changes      []ChangeRetentionSpec `json:"changes,omitempty"`
	Types        []TypeRetentionSpec   `json:"types,omitempty"`
	StaleItemAge string                `json:"staleItemAge,omitempty"`
}

// ScraperSpec defines the desired state of Config scraper
type ScraperSpec struct {
	LogLevel       string           `json:"logLevel,omitempty"`
	Schedule       string           `json:"schedule,omitempty"`
	GCP            []GCP            `json:"gcp,omitempty" yaml:"gcp,omitempty"`
	AWS            []AWS            `json:"aws,omitempty" yaml:"aws,omitempty"`
	File           []File           `json:"file,omitempty" yaml:"file,omitempty"`
	Kubernetes     []Kubernetes     `json:"kubernetes,omitempty" yaml:"kubernetes,omitempty"`
	KubernetesFile []KubernetesFile `json:"kubernetesFile,omitempty" yaml:"kubernetesFile,omitempty"`
	AzureDevops    []AzureDevops    `json:"azureDevops,omitempty" yaml:"azureDevops,omitempty"`
	GithubActions  []GitHubActions  `json:"githubActions,omitempty" yaml:"githubActions,omitempty"`
	Azure          []Azure          `json:"azure,omitempty" yaml:"azure,omitempty"`
	SQL            []SQL            `json:"sql,omitempty" yaml:"sql,omitempty"`
	Slack          []Slack          `json:"slack,omitempty" yaml:"slack,omitempty"`
	Trivy          []Trivy          `json:"trivy,omitempty" yaml:"trivy,omitempty"`
	Terraform      []Terraform      `json:"terraform,omitempty" yaml:"trivy,omitempty"`
	HTTP           []HTTP           `json:"http,omitempty"`
	Clickhouse     []Clickhouse     `json:"clickhouse,omitempty"`
	Logs           []Logs           `json:"logs,omitempty"`
	PubSub         []PubSub         `json:"pubsub,omitempty"`
	System         bool             `json:"system,omitempty"`

	// CRDSync when set to true, will create (or update) the corresponding database record
	// for a config item of the following types
	// - MissionControl::Playbook, MissionControl::ScrapeConfig, MissionControl::Canary
	CRDSync bool `json:"crdSync,omitempty"`

	Retention RetentionSpec `json:"retention,omitempty"`

	// Full flag when set will try to extract out changes from the scraped config.
	Full bool `json:"full,omitempty"`
}

func (c ScraperSpec) ApplyPlugin(plugins []ScrapePluginSpec) ScraperSpec {
	spec := c.DeepCopy()

	for i := range spec.GCP {
		spec.GCP[i].BaseScraper = spec.GCP[i].BaseScraper.ApplyPlugins(plugins...)
	}

	for i := range spec.AWS {
		spec.AWS[i].BaseScraper = spec.AWS[i].BaseScraper.ApplyPlugins(plugins...)
	}

	for i := range spec.File {
		spec.File[i].BaseScraper = spec.File[i].BaseScraper.ApplyPlugins(plugins...)
	}

	for i := range spec.Kubernetes {
		spec.Kubernetes[i].BaseScraper = spec.Kubernetes[i].BaseScraper.ApplyPlugins(plugins...)
	}

	for i := range spec.KubernetesFile {
		spec.KubernetesFile[i].BaseScraper = spec.KubernetesFile[i].BaseScraper.ApplyPlugins(plugins...)
	}

	for i := range spec.AzureDevops {
		spec.AzureDevops[i].BaseScraper = spec.AzureDevops[i].BaseScraper.ApplyPlugins(plugins...)
	}

	for i := range spec.GithubActions {
		spec.GithubActions[i].BaseScraper = spec.GithubActions[i].BaseScraper.ApplyPlugins(plugins...)
	}

	for i := range spec.Azure {
		spec.Azure[i].BaseScraper = spec.Azure[i].BaseScraper.ApplyPlugins(plugins...)
	}

	for i := range spec.SQL {
		spec.SQL[i].BaseScraper = spec.SQL[i].BaseScraper.ApplyPlugins(plugins...)
	}

	for i := range spec.Slack {
		spec.Slack[i].BaseScraper = spec.Slack[i].BaseScraper.ApplyPlugins(plugins...)
	}

	for i := range spec.Trivy {
		spec.Trivy[i].BaseScraper = spec.Trivy[i].BaseScraper.ApplyPlugins(plugins...)
	}

	for i := range spec.Terraform {
		spec.Terraform[i].BaseScraper = spec.Terraform[i].BaseScraper.ApplyPlugins(plugins...)
	}

	for i := range spec.HTTP {
		spec.HTTP[i].BaseScraper = spec.HTTP[i].BaseScraper.ApplyPlugins(plugins...)
	}

	for i := range spec.Logs {
		spec.Logs[i].BaseScraper = spec.Logs[i].BaseScraper.ApplyPlugins(plugins...)
	}

	return *spec
}

func (c ScraperSpec) GenerateName() (string, error) {
	return utils.Hash(c)
}

// IsEmpty ...
func (c ScraperSpec) IsEmpty() bool {
	return len(c.AWS) == 0 && len(c.File) == 0
}

func (c ScraperSpec) IsTrace() bool {
	return c.LogLevel == "trace"
}

func (c ScraperSpec) IsDebug() bool {
	return c.LogLevel == "debug"
}

type ExternalID struct {
	ConfigType string
	ExternalID string

	// Scraper id of the config
	// If left empty, the scraper id is the requester's scraper id.
	// Use `all` to disregard scraper id.
	ScraperID string

	Labels map[string]string
}

func (e ExternalID) GetKubernetesUID() string {
	configType := strings.ToLower(e.ConfigType)
	if strings.HasPrefix(configType, "kubernetes::") ||
		strings.HasPrefix(configType, "crossplane::") ||
		strings.HasPrefix(configType, "argo::") ||
		strings.HasPrefix(configType, "flux::") {

		if uuid.Validate(e.ExternalID) == nil {
			return e.ExternalID
		}
	}

	return ""
}

func (e ExternalID) Find(db *gorm.DB) *gorm.DB {
	query := db.Limit(1).Order("updated_at DESC").Where("deleted_at IS NULL").Where("? = ANY(external_id)", strings.ToLower(e.ExternalID))
	if e.ConfigType != "" {
		query = query.Where("type = ?", e.ConfigType)
	}
	if e.ScraperID != "all" && e.ScraperID != "" && !slices.Contains(ScraperLessTypes, e.ConfigType) {
		query = query.Where("scraper_id = ?", e.ScraperID)
	}
	for k, v := range e.Labels {
		query = query.Where("labels ->> ? = ?", k, v)
	}
	return query
}

func (e ExternalID) Key() string {
	return strings.ToLower(fmt.Sprintf("%s%s%s", e.ConfigType, e.ExternalID, e.ScraperID))
}

func (e ExternalID) String() string {
	if e.ScraperID != "" {
		return fmt.Sprintf("scraper_id=%s type=%s externalids=%s", e.ScraperID, e.ConfigType, e.ExternalID)
	}
	return fmt.Sprintf("type=%s externalids=%s", e.ConfigType, e.ExternalID)
}

func (e ExternalID) IsEmpty() bool {
	return e.ConfigType == "" || len(e.ExternalID) == 0
}

type ConfigDeleteReason string

var (
	// DeletedReasonStale is used when a config item doesn't get updated
	// for a period of `staleItemAge`.
	DeletedReasonStale ConfigDeleteReason = "STALE"

	// DeletedReasonFromAttribute is used when a deletion field (& reason)
	// is picked up from the scraped config itself.
	DeletedReasonFromAttribute ConfigDeleteReason = "FROM_ATTRIBUTE"

	// DeletedReasonFromDeleteField is used when a deletion field (& reason)
	// is picked up from the JSONPath expression provided in the scraper config.
	DeletedReasonFromDeleteField ConfigDeleteReason = "FROM_DELETE_FIELD"

	DeleteReasonEvent ConfigDeleteReason = "FROM_EVENT"
)
