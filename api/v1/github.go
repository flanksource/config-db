package v1

import (
	"time"

	"github.com/flanksource/duty/types"
)

// GitHubOrganizationConfigType is the config type of the organization config
// item, distinct from the GitHubOrganization spec below.
const GitHubOrganizationConfigType = "GitHub::Organization"

// GitHubActions scraper scrapes the workflow and its runs based on the given filter.
// By default, it fetches the last 7 days of workflow runs (Configurable via property: scrapers.githubactions.maxAge)
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

	// Returns someone's workflow runs.
	// Use the login for the user who created the push associated with the check suite or workflow run.
	Actor string `yaml:"actor,omitempty" json:"actor,omitempty"`

	// Returns workflow runs associated with a branch. Use the name of the branch of the push.
	Branch string `yaml:"branch,omitempty" json:"branch,omitempty"`
}

// GitHub scraper creates GitHub::Repository config items and optionally
// attaches security alerts and OpenSSF scorecard results as analyses.
type GitHub struct {
	BaseScraper `json:",inline" yaml:",inline"`

	// Repositories is the list of repositories to scrape
	Repositories []GitHubRepository `yaml:"repositories" json:"repositories"`

	// Organizations to scrape for settings, installed apps and membership.
	// Repository owners are always attached to their organization, but only
	// organizations listed here are scraped beyond their name.
	Organizations []GitHubOrganization `yaml:"organizations,omitempty" json:"organizations,omitempty"`

	PersonalAccessToken types.EnvVar `yaml:"personalAccessToken,omitempty" json:"personalAccessToken,omitempty"`

	// ConnectionName, if provided, will be used to populate personalAccessToken
	ConnectionName string `yaml:"connection,omitempty" json:"connection,omitempty"`

	// Security enables fetching Dependabot, code scanning, and secret scanning alerts
	Security bool `yaml:"security,omitempty" json:"security,omitempty"`

	// OpenSSF enables fetching OpenSSF Scorecard data
	OpenSSF bool `yaml:"openssf,omitempty" json:"openssf,omitempty"`

	// Permissions configures repository collaborator and team access collection
	Permissions *GitHubPermissions `yaml:"permissions,omitempty" json:"permissions,omitempty"`

	// SecurityFilters for security alerts (only used when security=true)
	SecurityFilters GitHubSecurityFilters `yaml:"securityFilters,omitempty" json:"securityFilters,omitempty"`
}

// GitHubPermissions configures repository RBAC collection.
type GitHubPermissions struct {
	// Enabled maps effective collaborators and repository teams to external users,
	// groups, roles, and config access records.
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
}

// GitHubOrganization specifies an organization to scrape and how deeply.
// Settings and Apps require organization administration read access, Rulesets
// requires organization administration write access, and Members requires
// organization members read access.
type GitHubOrganization struct {
	// Name is the organization login, e.g. acme
	Name string `yaml:"name" json:"name"`

	// Settings collects organization security and policy settings: 2FA
	// requirement, default repository permission, member repository and page
	// creation policy, Advanced Security / Dependabot / secret scanning
	// defaults for new repositories, Actions permissions, custom organization
	// roles and code security configurations.
	Settings bool `yaml:"settings,omitempty" json:"settings,omitempty"`

	// Rulesets collects organization repository rulesets. GitHub requires the
	// organization Administration write permission even though this is a
	// read-only API operation.
	Rulesets bool `yaml:"rulesets,omitempty" json:"rulesets,omitempty"`

	// Apps collects installed GitHub App installations. Installations granted
	// to all repositories are related to the configured repository set; GitHub's
	// organization endpoint does not expose selected repository grants.
	Apps bool `yaml:"apps,omitempty" json:"apps,omitempty"`

	// Members collects organization members and their organization role,
	// teams, team membership and team to repository grants.
	Members bool `yaml:"members,omitempty" json:"members,omitempty"`
}

// GitHubRepository specifies a repository or repository selector to scrape.
type GitHubRepository struct {
	Owner string `yaml:"owner" json:"owner"`

	// Repo can be an exact repository name or comma-separated collections.MatchItems patterns.
	// Pattern selectors discover matching non-archived repositories for Owner.
	Repo string `yaml:"repo" json:"repo"`
}

// GitHubSecurityFilters defines filtering options for security alerts
type GitHubSecurityFilters struct {
	Severity []string `yaml:"severity,omitempty" json:"severity,omitempty"`
	State    []string `yaml:"state,omitempty" json:"state,omitempty"`
	MaxAge   string   `yaml:"maxAge,omitempty" json:"maxAge,omitempty"`
}

func (f GitHubSecurityFilters) ParseMaxAge() (time.Duration, error) {
	if f.MaxAge == "" {
		return 0, nil
	}
	return time.ParseDuration(f.MaxAge)
}
