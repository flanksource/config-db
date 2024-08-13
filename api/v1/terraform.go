package v1

import (
	"errors"
	"fmt"

	"github.com/flanksource/duty/connection"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
)

type TerraformStateSource struct {
	S3    *connection.S3Connection  `json:"s3,omitempty"`
	GCS   *connection.GCSConnection `json:"gcs,omitempty"`
	Local string                    `json:"local,omitempty"`
}

func (t *TerraformStateSource) Path() string {
	if t.Local != "" {
		return t.Local
	}

	if t.S3 != nil {
		return t.S3.ObjectPath
	}

	if t.GCS != nil {
		// TODO:
		return ""
	}

	return ""
}

func (t *TerraformStateSource) Connection(ctx context.Context) (*models.Connection, error) {
	if t.Local != "" {
		return &models.Connection{Type: models.ConnectionTypeFolder}, nil
	}

	if t.S3 != nil {
		if err := t.S3.Populate(ctx); err != nil {
			return nil, fmt.Errorf("failed to populate S3 connection: %v", err)
		}

		connection := &models.Connection{Type: models.ConnectionTypeS3}
		connection, err := connection.Merge(ctx, t.S3)
		if err != nil {
			return nil, fmt.Errorf("failed to merge S3 connection: %v", err)
		}

		return connection, nil
	}

	if t.GCS != nil {
		if err := t.GCS.HydrateConnection(ctx); err != nil {
			return nil, fmt.Errorf("failed to populate GCP connection: %v", err)
		}

		connection := &models.Connection{Type: models.ConnectionTypeGCP}
		connection, err := connection.Merge(ctx, t.GCS)
		if err != nil {
			return nil, fmt.Errorf("failed to merge GCP connection: %v", err)
		}

		return connection, nil
	}

	return nil, errors.New("state source is empty")
}

type Terraform struct {
	BaseScraper `json:",inline"`
	Name        types.GoTemplate     `json:"name"`
	State       TerraformStateSource `json:"state"`
}
