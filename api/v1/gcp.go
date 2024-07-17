package v1

import "strings"

const (
	GCSBucket         = "GCP::Bucket"
	GKECluster        = "GCP::GKECluster"
	RedisInstance     = "GCP::Redis"
	MemcacheInstance  = "GCP::MemCache"
	PubSubTopic       = "GCP::PubSub"
	CloudSQLInstance  = "GCP::CloudSQL"
	IAMRole           = "GCP::IAMRole"
	IAMServiceAccount = "GCP::ServiceAccount"
)

type GCP struct {
	BaseScraper
	Project        string
	*GCPConnection `json:",inline"`
	Include        []string `json:"include,omitempty"`
	Exclude        []string `json:"exclude,omitempty"`
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
