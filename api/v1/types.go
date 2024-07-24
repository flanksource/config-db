package v1

import (
	"fmt"
	"strings"

	"github.com/flanksource/config-db/utils"
)

var AllScraperConfigs = map[string]any{
	"aws":            AWS{},
	"azure":          Azure{},
	"azuredevops":    AzureDevops{},
	"file":           File{},
	"githubactions":  GitHubActions{},
	"kubernetes":     Kubernetes{},
	"kubernetesfile": KubernetesFile{},
	"sql":            SQL{},
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
	Trivy          []Trivy          `json:"trivy,omitempty" yaml:"trivy,omitempty"`
	Retention      RetentionSpec    `json:"retention,omitempty"`

	// Full flag when set will try to extract out changes from the scraped config.
	Full bool `json:"full,omitempty"`
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
	ExternalID []string

	// Scraper id of the config
	// If left empty, the scraper id is the requester's scraper id.
	// Use `all` to disregard scraper id.
	ScraperID string
}

func (e ExternalID) String() string {
	if e.ScraperID != "" {
		return fmt.Sprintf("scraper_id=%s type=%s externalids=%s", e.ScraperID, e.ConfigType, strings.Join(e.ExternalID, ","))
	}
	return fmt.Sprintf("type=%s externalids=%s", e.ConfigType, strings.Join(e.ExternalID, ","))
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
)
