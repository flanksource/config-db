package v1

import (
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

type GCP struct {
	BaseScraper              `json:",inline"`
	connection.GCPConnection `json:",inline"`
	Project                  string `json:"project"`

	// Include is a list of GCP asset types to scrape.
	// Reference: https://cloud.google.com/asset-inventory/docs/supported-asset-types
	// Example: storage.googleapis.com/Bucket
	Include []string `json:"include,omitempty"`

	// Exclude is a list of GCP asset types to exclude from scraping.
	Exclude []string `json:"exclude,omitempty"`

	AuditLogs GCPAuditLogs `json:"auditLogs,omitempty"`
}

type GCPAuditLogs struct {
	Enabled      bool     `json:"enabled,omitempty"`
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
