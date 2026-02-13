package v1

import (
	"fmt"
	"time"

	"github.com/flanksource/duty/connection"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
)

// Folder defines the configuration for scraping directory listings
type Folder struct {
	BaseScraper `json:",inline"`

	// Path is the directory path to scan
	Path string `json:"path"`

	// S3 connection configuration
	S3 *connection.S3Connection `json:"s3,omitempty"`

	// GCS connection configuration
	GCS *connection.GCSConnection `json:"gcs,omitempty"`

	// Local file system path
	Local string `json:"local,omitempty"`

	// Recursive scan subdirectories
	Recursive bool `json:"recursive,omitempty"`

	// Filter defines filtering options for files/folders
	Filter FolderFilter `json:"filter,omitempty"`
}

// FolderFilter defines filtering options for directory listings
type FolderFilter struct {
	// MinAge filters files older than this duration (e.g. "1h", "30d")
	MinAge string `json:"minAge,omitempty"`

	// MaxAge filters files newer than this duration
	MaxAge string `json:"maxAge,omitempty"`

	// MinSize filters files larger than this size in bytes
	MinSize *int64 `json:"minSize,omitempty"`

	// MaxSize filters files smaller than this size in bytes
	MaxSize *int64 `json:"maxSize,omitempty"`

	// Regex pattern to match file names
	Regex string `json:"regex,omitempty"`

	// Glob pattern to match file names
	Glob string `json:"glob,omitempty"`
}

func (f *Folder) GetPath() string {
	if f.Path != "" {
		return f.Path
	}

	if f.Local != "" {
		return f.Local
	}

	if f.S3 != nil {
		return f.S3.ObjectPath
	}

	if f.GCS != nil {
		// GCS paths should be specified in the Path field
		// GCS connection handles bucket configuration
		return ""
	}

	return ""
}

func (f *Folder) GetConnection(ctx context.Context) (*models.Connection, error) {
	if f.Local != "" || f.Path != "" {
		return &models.Connection{Type: models.ConnectionTypeFolder}, nil
	}

	// Note: S3 and GCS have different method names (Populate vs HydrateConnection)
	// defined by the duty library, so we handle them separately
	if f.S3 != nil {
		if err := f.S3.Populate(ctx); err != nil {
			return nil, fmt.Errorf("failed to populate S3 connection: %v", err)
		}

		connection := &models.Connection{Type: models.ConnectionTypeS3}
		connection, err := connection.Merge(ctx, f.S3)
		if err != nil {
			return nil, fmt.Errorf("failed to merge S3 connection: %v", err)
		}

		return connection, nil
	}

	if f.GCS != nil {
		if err := f.GCS.HydrateConnection(ctx); err != nil {
			return nil, fmt.Errorf("failed to populate GCS connection: %v", err)
		}

		connection := &models.Connection{Type: models.ConnectionTypeGCP}
		connection, err := connection.Merge(ctx, f.GCS)
		if err != nil {
			return nil, fmt.Errorf("failed to merge GCS connection: %v", err)
		}

		return connection, nil
	}

	return nil, fmt.Errorf("no valid connection configured")
}

// FolderFilterContext holds compiled filter patterns for efficient filtering
type FolderFilterContext struct {
	Filter    FolderFilter
	MinAge    *time.Duration
	MaxAge    *time.Duration
	AllowDir  bool
	RegexComp interface{} // To store compiled regex if needed
	GlobComp  interface{} // To store compiled glob if needed
}
