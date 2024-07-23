package v1

import (
	"github.com/flanksource/duty/connection"
	"github.com/flanksource/duty/types"
)

type TerraformStateSource struct {
	S3    connection.S3Connection  `json:"s3,omitempty"`
	GCP   connection.GCPConnection `json:"gcp,omitempty"`
	Local string                   `json:"local,omitempty"`
}

type Terraform struct {
	BaseScraper `json:",inline"`
	Name        types.GoTemplate     `json:"name"`
	State       TerraformStateSource `json:"state"`
}
