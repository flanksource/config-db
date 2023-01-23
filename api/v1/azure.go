package v1

import (
	"github.com/flanksource/kommons"
)

type AzureDevops struct {
	BaseScraper         `json:",inline"`
	Organization        string         `yaml:"organization" json:"organization"`
	PersonalAccessToken kommons.EnvVar `yaml:"personalAccessToken" json:"personalAccessToken"`
	Projects            []string       `yaml:"projects" json:"projects"`
	Pipelines           []string       `yaml:"pipelines" json:"pipelines"`
}
type Azure struct {
	BaseScraper    `json:",inline"`
	SubscriptionId string   `yaml:"subscriptionId" json:"subscriptionId"`
	Organisation   string   `yaml:"organisation" json:"organisation"`
	Region         []string `yaml:"region" json:"region"`
}
