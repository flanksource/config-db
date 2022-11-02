package v1

import (
	"fmt"
	"github.com/lib/pq"
	"strings"

	"gorm.io/gorm"
)

// ConfigScraper ...
type ConfigScraper struct {
	LogLevel       string           `json:"logLevel,omitempty"`
	Schedule       string           `json:"schedule,omitempty"`
	AWS            []AWS            `json:"aws,omitempty" yaml:"aws,omitempty"`
	File           []File           `json:"file,omitempty" yaml:"file,omitempty"`
	Kubernetes     []Kubernetes     `json:"kubernetes,omitempty" yaml:"kubernetes,omitempty"`
	KubernetesFile []KubernetesFile `json:"kubernetesFile,omitempty" yaml:"kubernetesFile,omitempty"`
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

func (e ExternalID) CacheKey() string {
	return fmt.Sprintf("external_id:%s:%s", e.ExternalType, strings.Join(e.ExternalID, ","))
}

func (e ExternalID) WhereClause(db *gorm.DB) *gorm.DB {
	return db.Where("external_type = ? and external_id  @> ?", e.ExternalType, pq.StringArray(e.ExternalID))
}
