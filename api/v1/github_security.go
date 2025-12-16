package v1

import (
	"time"

	"github.com/flanksource/duty/types"
)

// GitHubSecurity scraper fetches security alerts from GitHub repositories
// including Dependabot alerts, code scanning alerts, secret scanning alerts,
// and security advisories.
type GitHubSecurity struct {
	BaseScraper `json:",inline" yaml:",inline"`

	// Repositories is the list of repositories to scan
	Repositories []GitHubSecurityRepository `yaml:"repositories" json:"repositories"`

	// PersonalAccessToken for GitHub API authentication
	// Required scopes: repo (full) or security_events (read)
	PersonalAccessToken types.EnvVar `yaml:"personalAccessToken,omitempty" json:"personalAccessToken,omitempty"`

	// ConnectionName, if provided, will be used to populate personalAccessToken
	ConnectionName string `yaml:"connection,omitempty" json:"connection,omitempty"`

	// Filters for security alerts
	Filters GitHubSecurityFilters `yaml:"filters,omitempty" json:"filters,omitempty"`
}

// GitHubSecurityRepository specifies a repository to scan
type GitHubSecurityRepository struct {
	Owner string `yaml:"owner" json:"owner"`
	Repo  string `yaml:"repo" json:"repo"`
}

// GitHubSecurityFilters defines filtering options for security alerts
type GitHubSecurityFilters struct {
	// Severity filters: critical, high, medium, low
	Severity []string `yaml:"severity,omitempty" json:"severity,omitempty"`

	// State filters: open, closed, dismissed, fixed
	State []string `yaml:"state,omitempty" json:"state,omitempty"`

	// MaxAge filters alerts by age (e.g., "90d", "30d")
	MaxAge string `yaml:"maxAge,omitempty" json:"maxAge,omitempty"`
}

// ParseMaxAge converts the MaxAge string to a time.Duration
func (f GitHubSecurityFilters) ParseMaxAge() (time.Duration, error) {
	if f.MaxAge == "" {
		return 0, nil
	}
	return time.ParseDuration(f.MaxAge)
}
