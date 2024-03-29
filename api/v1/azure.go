package v1

import (
	"github.com/flanksource/duty/types"
)

type AzureDevops struct {
	BaseScraper         `json:",inline"`
	ConnectionName      string       `yaml:"connection,omitempty" json:"connection,omitempty"`
	Organization        string       `yaml:"organization,omitempty" json:"organization,omitempty"`
	PersonalAccessToken types.EnvVar `yaml:"personalAccessToken,omitempty" json:"personalAccessToken,omitempty"`
	Projects            []string     `yaml:"projects" json:"projects"`
	Pipelines           []string     `yaml:"pipelines" json:"pipelines"`
}

type Azure struct {
	BaseScraper    `json:",inline"`
	ConnectionName string           `yaml:"connection,omitempty" json:"connection,omitempty"`
	SubscriptionID string           `yaml:"subscriptionID" json:"subscriptionID"`
	Organisation   string           `yaml:"organisation" json:"organisation"`
	ClientID       types.EnvVar     `yaml:"clientID,omitempty" json:"clientID,omitempty"`
	ClientSecret   types.EnvVar     `yaml:"clientSecret,omitempty" json:"clientSecret,omitempty"`
	TenantID       string           `yaml:"tenantID,omitempty" json:"tenantID,omitempty"`
	Exclusions     *AzureExclusions `yaml:"exclusions,omitempty" json:"exclusions,omitempty"`
}

type AzureExclusions struct {
	// ActivityLogs is a list of operations to exclude from activity logs.
	// Example:
	//  "Microsoft.ContainerService/managedClusters/listClusterAdminCredential/action"
	//  "Microsoft.ContainerService/managedClusters/listClusterUserCredential/action"
	ActivityLogs []string `yaml:"activityLogs,omitempty" json:"activityLogs,omitempty"`
}
