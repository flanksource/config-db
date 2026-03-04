package v1

import (
	"github.com/flanksource/duty/connection"
	"github.com/flanksource/duty/logs"
	"github.com/flanksource/duty/logs/azureloganalytics"
	"github.com/flanksource/duty/logs/bigquery"
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

	// BigQuery specifies the BigQuery configuration for log scraping
	BigQuery *BigQueryConfig `json:"bigQuery,omitempty"`

	// AzureLogAnalytics specifies the Azure Log Analytics configuration for log scraping
	AzureLogAnalytics *AzureLogAnalyticsConfig `json:"azureLogAnalytics,omitempty"`

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

// BigQueryConfig contains configuration for BigQuery log scraping
type BigQueryConfig struct {
	connection.GCPConnection `json:",inline"`
	bigquery.Request         `json:",inline"`
}

// AzureLogAnalyticsConfig contains configuration for Azure Log Analytics log scraping
type AzureLogAnalyticsConfig struct {
	connection.AzureConnection `json:",inline"`
	azureloganalytics.Request  `json:",inline"`
}
