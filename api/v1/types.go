package v1

import (
	"fmt"
	"strings"

	"github.com/flanksource/config-db/utils"
	"github.com/lib/pq"

	"gorm.io/gorm"
)

// ConfigScraper ...
type ConfigScraper struct {
	ID             string           `json:"-"`
	LogLevel       string           `json:"logLevel,omitempty"`
	Schedule       string           `json:"schedule,omitempty"`
	AWS            []AWS            `json:"aws,omitempty" yaml:"aws,omitempty"`
	File           []File           `json:"file,omitempty" yaml:"file,omitempty"`
	Kubernetes     []Kubernetes     `json:"kubernetes,omitempty" yaml:"kubernetes,omitempty"`
	KubernetesFile []KubernetesFile `json:"kubernetesFile,omitempty" yaml:"kubernetesFile,omitempty"`
	AzureDevops    []AzureDevops    `json:"azureDevops,omitempty" yaml:"azureDevops,omitempty"`
	GithubActions  []GitHubActions  `json:"githubActions,omitempty" yaml:"githubActions,omitempty"`
	SQL            []SQL            `json:"sql,omitempty" yaml:"sql,omitempty"`

	// Full flag when set will try to extract out changes from the scraped config.
	Full bool `json:"full,omitempty"`
}

func (c ConfigScraper) GenerateName() (string, error) {
	return utils.Hash(c)
}

// IsEmpty ...
func (c ConfigScraper) IsEmpty() bool {
	return len(c.AWS) == 0 && len(c.File) == 0
}

func (c ConfigScraper) IsTrace() bool {
	return c.LogLevel == "trace"
}

type ExternalID struct {
	ExternalType string
	ExternalID   []string
}

func (e ExternalID) String() string {
	return fmt.Sprintf("%s/%s", e.ExternalType, strings.Join(e.ExternalID, ","))
}

func (e ExternalID) IsEmpty() bool {
	return e.ExternalType == "" && len(e.ExternalID) == 0
}

func (e ExternalID) CacheKey() string {
	return fmt.Sprintf("external_id:%s:%s", e.ExternalType, strings.Join(e.ExternalID, ","))
}

func (e ExternalID) WhereClause(db *gorm.DB) *gorm.DB {
	return db.Where("external_type = ? AND external_id  @> ?", e.ExternalType, pq.StringArray(e.ExternalID))
}
