package v1

import (
	"net/url"

	"github.com/flanksource/duty/models"
)

// File ...
type File struct {
	BaseScraper `json:",inline"`
	URL         string   `json:"url,omitempty"`
	Paths       []string `json:"paths,omitempty"`
	Ignore      []string `json:"ignore,omitempty"`
	Format      string   `json:"format,omitempty"`
	Icon        string   `json:"icon,omitempty"`
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
