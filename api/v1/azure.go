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
	SubscriptionID string         `yaml:"subscriptionID" json:"subscriptionID"`
	Organisation   string         `yaml:"organisation" json:"organisation"`
	ClientID       kommons.EnvVar `yaml:"clientID,omitempty" json:"clientID,omitempty"`
	ClientSecret   kommons.EnvVar `yaml:"clientSecret,omitempty" json:"clientSecret,omitempty"`
	TenantID       string         `yaml:"tenantID" json:"tenantID"`
}
