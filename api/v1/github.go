package v1

import "github.com/flanksource/duty/types"

type GitHubActions struct {
	BaseScraper         `json:",inline"`
	Owner               string       `yaml:"owner" json:"owner"`
	Repository          string       `yaml:"repository" json:"repository"`
	PersonalAccessToken types.EnvVar `yaml:"personalAccessToken" json:"personalAccessToken"`
	Workflows           []string     `yaml:"workflows" json:"workflows"`
}
