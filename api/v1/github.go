package v1

import "github.com/flanksource/kommons"

type GitHubActions struct {
	BaseScraper         `json:",inline"`
	Owner               string         `yaml:"owner" json:"owner"`
	Repository          string         `yaml:"repository" json:"repository"`
	PersonalAccessToken kommons.EnvVar `yaml:"personalAccessToken" json:"personalAccessToken"`
	Workflows           []string       `yaml:"workflows" json:"workflows"`
}
