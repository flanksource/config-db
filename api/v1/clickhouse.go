package v1

import (
	"fmt"

	"github.com/flanksource/duty/connection"
)

type Clickhouse struct {
	BaseScraper      `yaml:",inline" json:",inline"`
	AWSS3            *AWSS3            `json:"awsS3,omitempty"`
	AzureBlobStorage *AzureBlobStorage `json:"azureBlobStorage,omitempty"`

	// clickhouse://<user>:<password>@<host>:<port>/<database>?param1=value1&param2=value2
	ClickhouseURL string `json:"clickhouseURL,omitempty"`
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

	Bucket   string `json:"bucket,omitempty"`
	Path     string `json:"path,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}

func (az AzureBlobStorage) GetAccountKeyCommand() string {
	return fmt.Sprintf(`az storage account keys list -n %s | jq -r '.[0].value'`, az.Account)
}

func (az AzureBlobStorage) GetConnectionString(accKey string) string {
	ep := "core.windows.net"
	if az.EndpointSuffix != "" {
		ep = az.EndpointSuffix
	}
	return fmt.Sprintf("DefaultEndpointsProtocol=https;AccountName=%s;AccountKey=%s;EndpointSuffix=%s", az.Account, accKey, ep)
}
