package v1

import (
	"github.com/flanksource/commons/collections"
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

// EntraID is the Azure Active Directory (AAD) configuration.
type EntraID struct {
	Users              []types.ResourceSelector `yaml:"users,omitempty" json:"users,omitempty"`
	Groups             []types.ResourceSelector `yaml:"groups,omitempty" json:"groups,omitempty"`
	AppRegistrations   []types.ResourceSelector `yaml:"appRegistrations,omitempty" json:"appRegistrations,omitempty"`
	EnterpriseApps     []types.ResourceSelector `yaml:"enterpriseApps,omitempty" json:"enterpriseApps,omitempty"`
	AppRoleAssignments []types.ResourceSelector `yaml:"appRoleAssignments,omitempty" json:"appRoleAssignments,omitempty"`
}

type Azure struct {
	BaseScraper    `json:",inline"`
	ConnectionName string           `yaml:"connection,omitempty" json:"connection,omitempty"`
	SubscriptionID string           `yaml:"subscriptionID" json:"subscriptionID"`
	ClientID       types.EnvVar     `yaml:"clientID,omitempty" json:"clientID,omitempty"`
	ClientSecret   types.EnvVar     `yaml:"clientSecret,omitempty" json:"clientSecret,omitempty"`
	TenantID       string           `yaml:"tenantID,omitempty" json:"tenantID,omitempty"`
	Include        []string         `yaml:"include,omitempty" json:"include,omitempty"`
	Exclusions     *AzureExclusions `yaml:"exclusions,omitempty" json:"exclusions,omitempty"`
	EntraID        *EntraID         `yaml:"entraID,omitempty" json:"entraID,omitempty"`
}

func (azure Azure) Includes(resource string) bool {
	if len(azure.Include) == 0 {
		return true
	}
	return collections.MatchItems(resource, azure.Include...)
}

type AzureExclusions struct {
	// ActivityLogs is a list of operations to exclude from activity logs.
	// Example:
	//  "Microsoft.ContainerService/managedClusters/listClusterAdminCredential/action"
	//  "Microsoft.ContainerService/managedClusters/listClusterUserCredential/action"
	ActivityLogs []string `yaml:"activityLogs,omitempty" json:"activityLogs,omitempty"`
}
