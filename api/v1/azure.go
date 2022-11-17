package v1

import "github.com/flanksource/kommons"

type AzureDevops struct {
	Organization        string         `yaml:"organization" json:"organization"`
	PersonalAccessToken kommons.EnvVar `yaml:"personalAccessToken" json:"personalAccessToken"`
	Projects            []string       `yaml:"projects" json:"projects"`
	Pipelines           []string       `yaml:"pipelines" json:"pipelines"`
}
