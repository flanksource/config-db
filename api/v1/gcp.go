package v1

import (
	"slices"
	"strings"

	"github.com/flanksource/duty/connection"
)

const (
	GCSBucket         = "GCP::Bucket"
	GKECluster        = "GCP::GKECluster"
	RedisInstance     = "GCP::Redis"
	MemcacheInstance  = "GCP::MemCache"
	PubSubTopic       = "GCP::PubSub"
	CloudSQLInstance  = "GCP::Sqladmin::Instance"
	IAMRole           = "GCP::IAMRole"
	IAMServiceAccount = "GCP::ServiceAccount"

	GCPInstance   = "GCP::Compute::Instance"
	GCPSubnet     = "GCP::Compute::Subnetwork"
	GCPNetwork    = "GCP::Compute::Network"
	GCPDisk       = "GCP::Compute::Disk"
	GCPGKECluster = "GCP::Container::Cluster"
)

const (
	// Feature flags for GCP scraper
	IncludeIAMPolicy = "IAMPolicy"
	IncludeAuditLogs = "AuditLogs"
)

var (
	AllIncludes = []string{IncludeIAMPolicy, IncludeAuditLogs}
)

type GCP struct {
	BaseScraper              `json:",inline"`
	connection.GCPConnection `json:",inline"`
	Project                  string `json:"project"`

	// Include specifies which GCP scraping features to enable.
	// Supported values: "Assets", "IAMPolicy", "AuditLogs"
	Include []string `json:"include,omitempty"`

	// Exclude is a list of GCP asset types to exclude from scraping.
	Exclude []string `json:"exclude,omitempty"`

	AuditLogs GCPAuditLogs `json:"auditLogs,omitempty"`
}

type GCPAuditLogs struct {
	IncludeTypes []string `json:"includeTypes,omitempty"`
	ExcludeTypes []string `json:"excludeTypes,omitempty"`

	// The lookback period for audit logs.
	// Default: 7d
	MaxDuration string `json:"maxDuration,omitempty"`
}

func (gcp GCP) Includes(resource string) bool {
	if len(gcp.Include) == 0 {
		return true
	}
	for _, include := range gcp.Include {
		if strings.EqualFold(include, resource) {
			return true
		}
	}
	return false
}

func (gcp GCP) Excludes(resource string) bool {
	if len(gcp.Exclude) == 0 {
		return false
	}
	for _, exclude := range gcp.Exclude {
		if strings.EqualFold(exclude, resource) {
			return true
		}
	}
	return false
}

// GetAssetTypes returns the asset types to scrape from Include field.
func (gcp GCP) GetAssetTypes() []string {
	var assetTypes []string
	for _, include := range gcp.Include {
		if !slices.Contains(AllIncludes, include) {
			assetTypes = append(assetTypes, include)
		}
	}

	return assetTypes
}
