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
	// Permissions configures fetching pipeline permissions to determine who can execute pipelines
	Permissions *AzureDevopsPermissions `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	// MaxAge limits pipeline run scraping to runs created within this duration (e.g. "7d", "24h").
	// Defaults to the system property azuredevops.pipeline.max_age, which defaults to 7d.
	MaxAge string `yaml:"maxAge,omitempty" json:"maxAge,omitempty"`
	// Releases filters classic release pipelines to scrape by name or glob
	Releases []string `yaml:"releases,omitempty" json:"releases,omitempty"`
}

// AzureDevopsPermissions configures permission fetching for Azure DevOps pipelines
type AzureDevopsPermissions struct {
	// Enabled enables fetching pipeline permissions
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	// RateLimit specifies how often to refresh permissions (e.g., "6h", "24h")
	// Defaults to "24h" if not set
	RateLimit string `yaml:"rateLimit,omitempty" json:"rateLimit,omitempty"`
}

type Entra struct {
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
	Entra          *Entra           `yaml:"entra,omitempty" json:"entra,omitempty"`
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
