package v1

import "github.com/flanksource/duty/types"

// GitHubActions scraper scrapes the workflow and its runs based on the given filter.
// By default, it fetches the last 7 days of workflow runs (Configurable via property: scrapers.githubactions.maxAge)
type GitHubActions struct {
	BaseScraper         `json:",inline" yaml:",inline"`
	Owner               string       `yaml:"owner" json:"owner"`
	Repository          string       `yaml:"repository" json:"repository"`
	PersonalAccessToken types.EnvVar `yaml:"personalAccessToken,omitempty" json:"personalAccessToken,omitempty"`
	Workflows           []string     `yaml:"workflows,omitempty" json:"workflows,omitempty"`

	// ConnectionName, if provided, will be used to populate personalAccessToken
	ConnectionName string `yaml:"connection,omitempty" json:"connection,omitempty"`

	// Returns workflow runs with the check run status or conclusion that you specify.
	// For example, a conclusion can be success or a status can be in_progress.
	Status string `yaml:"status,omitempty" json:"status,omitempty"`

	// Returns someone's workflow runs.
	// Use the login for the user who created the push associated with the check suite or workflow run.
	Actor string `yaml:"actor,omitempty" json:"actor,omitempty"`

	// Returns workflow runs associated with a branch. Use the name of the branch of the push.
	Branch string `yaml:"branch,omitempty" json:"branch,omitempty"`
}
