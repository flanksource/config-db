package v1

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/flanksource/duty/connection"
	"github.com/flanksource/duty/types"
)

type Clickhouse struct {
	BaseScraper      `yaml:",inline" json:",inline"`
	AWSS3            *AWSS3            `json:"awsS3,omitempty"`
	AzureBlobStorage *AzureBlobStorage `json:"azureBlobStorage,omitempty"`

	// URL is the ClickHouse connection URL:
	// clickhouse://<user>:<password>@<host>:<port>/<database>?param1=value1&param2=value2
	URL types.EnvVar `yaml:"url,omitempty" json:"url,omitempty"`

	// Deprecated: Use the url field instead.
	ClickhouseURL string `yaml:"clickhouseURL,omitempty" json:"clickhouseURL,omitempty"`
	Query         string `json:"query"`
}

type AzureBlobStorage struct {
	*connection.AzureConnection `yaml:",inline" json:",inline"`

	Account        string `json:"account,omitempty"`
	Container      string `json:"container,omitempty"`
	Path           string `json:"path,omitempty"`
	EndpointSuffix string `json:"endpoint,omitempty"`
	CollectionName string `json:"collection"`
}

type AWSS3 struct {
	*AWSConnection `yaml:",inline" json:",inline"`

	Bucket    string `json:"bucket,omitempty"`
	Path      string `json:"path,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"`
	Format    string `json:"format,omitempty"`
	Structure string `json:"structure,omitempty"`
}

// GetAccountKeyCommand returns the Azure CLI command used to resolve the
// storage account key without requiring an additional JSON processor.
func (az AzureBlobStorage) GetAccountKeyCommand() string {
	return fmt.Sprintf(`az storage account keys list --account-name %s --query '[0].value' --output tsv`, shellQuote(az.Account))
}

// GetConnectionString builds a cloud connection string for suffixes and a
// BlobEndpoint connection string when endpoint is a complete URL.
func (az AzureBlobStorage) GetConnectionString(accKey string) string {
	endpoint := strings.TrimSpace(az.EndpointSuffix)
	if parsed, err := url.Parse(endpoint); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" {
		parsed.Path = strings.TrimRight(parsed.Path, "/")
		return fmt.Sprintf("DefaultEndpointsProtocol=%s;AccountName=%s;AccountKey=%s;BlobEndpoint=%s", parsed.Scheme, az.Account, accKey, parsed.String())
	}

	if endpoint == "" {
		endpoint = "core.windows.net"
	}
	return fmt.Sprintf("DefaultEndpointsProtocol=https;AccountName=%s;AccountKey=%s;EndpointSuffix=%s", az.Account, accKey, endpoint)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
