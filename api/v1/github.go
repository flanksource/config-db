package v1

import "github.com/flanksource/duty/types"

type GitHubActions struct {
	BaseScraper         `json:",inline"`
	Owner               string       `yaml:"owner" json:"owner"`
	Repository          string       `yaml:"repository" json:"repository"`
	PersonalAccessToken types.EnvVar `yaml:"personalAccessToken" json:"personalAccessToken"`
	// ConnectionName, if provided, will be used to populate personalAccessToken
	ConnectionName string   `yaml:"connection,omitempty" json:"connection,omitempty"`
	Workflows      []string `yaml:"workflows" json:"workflows"`
}
