package v1

import (
	"github.com/flanksource/duty/connection"
	"github.com/flanksource/duty/logs"
	"github.com/flanksource/duty/logs/gcpcloudlogging"
	"github.com/flanksource/duty/logs/loki"
	"github.com/flanksource/duty/logs/opensearch"
)

type Logs struct {
	BaseScraper `json:",inline"`

	// Loki specifies the Loki configuration for log scraping
	Loki *LokiConfig `json:"loki,omitempty"`

	// GCPCloudLogging specifies the GCP Cloud Logging configuration
	GCPCloudLogging *GCPCloudLoggingConfig `json:"gcpCloudLogging,omitempty"`

	// OpenSearch specifies the OpenSearch configuration for log scraping
	OpenSearch *OpenSearchConfig `json:"openSearch,omitempty"`

	// FieldMapping defines how source log fields map to canonical LogLine fields
	FieldMapping *logs.FieldMappingConfig `json:"fieldMapping,omitempty"`
}

// LokiConfig contains configuration for Loki log scraping
type LokiConfig struct {
	connection.Loki `json:",inline"`
	loki.Request    `json:",inline"`
}

// GCPCloudLoggingConfig contains configuration for GCP Cloud Logging
type GCPCloudLoggingConfig struct {
	connection.GCPConnection `json:",inline"`
	gcpcloudlogging.Request  `json:",inline"`
}

// OpenSearchConfig contains configuration for OpenSearch log scraping
type OpenSearchConfig struct {
	opensearch.Backend `json:",inline"`
	opensearch.Request `json:",inline"`
}
