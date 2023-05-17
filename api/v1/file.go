package v1

import (
	"net/url"

	"github.com/flanksource/duty/models"
)

// File ...
type File struct {
	BaseScraper `json:",inline"`
	URL         string   `json:"url,omitempty" yaml:"url,omitempty"`
	Paths       []string `json:"paths,omitempty" yaml:"paths,omitempty"`
	Ignore      []string `json:"ignore,omitempty" yaml:"ignore,omitempty"`
	Format      string   `json:"format,omitempty" yaml:"format,omitempty"`
	Icon        string   `json:"icon,omitempty" yaml:"icon,omitempty"`

	// ConnectionName is used to populate the URL
	ConnectionName string `json:"connection,omitempty" yaml:"connection,omitempty"`
}

func (f File) RedactedString() string {
	if f.URL == "" {
		return f.URL
	}

	url, err := url.Parse(f.URL)
	if err != nil {
		return f.URL
	}

	return url.Redacted()
}

func (t File) GetConnection() *models.Connection {
	return &models.Connection{
		URL: t.URL,
	}
}
