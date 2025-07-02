package v1

import "github.com/flanksource/duty/types"

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

	// Returns workflow runs associated with a branch. Use the name of the branch of the push.
	Actor string `yaml:"actor,omitempty" json:"actor,omitempty"`

	// Returns workflow runs associated with a branch. Use the name of the branch of the push.
	Branch string `yaml:"branch,omitempty" json:"branch,omitempty"`

	// Returns workflow runs created within the given date-time range.
	// For more information on the syntax, see "Understanding the search syntax."
	// Docs: https://docs.github.com/en/search-github/getting-started-with-searching-on-github/understanding-the-search-syntax#query-for-dates
	Created string `yaml:"created,omitempty" json:"created,omitempty"`
}
