package github

import (
	"context"
	"fmt"
	"strings"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/types"
	"github.com/google/go-github/v73/github"
)

// organizationConfig is the config payload for an organization listed under
// `organizations:`. The org-level policy surfaces live on separate endpoints,
// so they are folded into a single payload alongside the organization itself.
type organizationConfig struct {
	Organization *github.Organization        `json:"organization"`
	Actions      *organizationActions        `json:"actions,omitempty"`
	Rulesets     []*github.RepositoryRuleset `json:"rulesets,omitempty"`
	Roles        []organizationRole          `json:"roles,omitempty"`
}

type organizationActions struct {
	Permissions *github.ActionsPermissions `json:"permissions,omitempty"`
	Allowed     *github.ActionsAllowed     `json:"allowed,omitempty"`
}

// organizationRole is an organization role together with who holds it. Built-in
// roles such as security manager are returned alongside custom ones, so this is
// where "who can administer security for this organization" is answered.
type organizationRole struct {
	Role  *github.CustomOrgRoles `json:"role"`
	Teams []string               `json:"teams,omitempty"`
	Users []string               `json:"users,omitempty"`
}

// organizationScrape carries everything the per-section scrapers need for one
// configured organization.
type organizationScrape struct {
	client       *GitHubClient
	spec         v1.GitHub
	org          v1.GitHubOrganization
	organization *github.Organization

	// repositories are the repositories this scrape produces config items for,
	// keyed by lowercased name. Repo-targeted access rows and relationships are
	// restricted to these so they stay resolvable: db/update.go raises a scrape
	// warning for every config_access whose target config does not exist.
	repositories map[string]v1.GitHubRepository
}

func (s *organizationScrape) name() string {
	return s.org.Name
}

// hasRepository reports whether repo is in the scrape's resolved repository set.
func (s *organizationScrape) hasRepository(repo string) bool {
	_, ok := s.repositories[strings.ToLower(strings.TrimSpace(repo))]
	return ok
}

// scrapeOrganizations scrapes every organization listed in the spec. seen is
// shared with the repository loop so an explicitly configured organization is
// never replaced by the thinner owner-derived result from
// appendOrganizationResult.
func scrapeOrganizations(
	ctx api.ScrapeContext,
	config v1.GitHub,
	repositories []v1.GitHubRepository,
	seen map[string]struct{},
) v1.ScrapeResults {
	var results v1.ScrapeResults

	for _, orgConfig := range config.Organizations {
		if err := ctx.Err(); err != nil {
			return results
		}

		orgConfig.Name = strings.TrimSpace(orgConfig.Name)
		if orgConfig.Name == "" {
			results.Errorf(fmt.Errorf("name is required"), "invalid GitHub organization")
			continue
		}

		ctx.Logger.V(2).Infof("scraping GitHub organization: %s", orgConfig.Name)

		client, err := NewGitHubClient(ctx, config, orgConfig.Name, "")
		if err != nil {
			results.Errorf(err, "failed to create GitHub client for %s", orgConfig.Name)
			continue
		}

		organization, _, err := client.Client.Organizations.Get(ctx, orgConfig.Name)
		if err != nil {
			results.Errorf(err, "failed to get GitHub organization %s", orgConfig.Name)
			continue
		}

		seen[githubOrganizationExternalID(orgConfig.Name)] = struct{}{}
		results = append(results, scrapeOrganization(ctx, &organizationScrape{
			client:       client,
			spec:         config,
			org:          orgConfig,
			organization: organization,
			repositories: organizationRepositories(orgConfig.Name, repositories),
		})...)
	}

	return results
}

func scrapeOrganization(ctx api.ScrapeContext, scrape *organizationScrape) v1.ScrapeResults {
	var results v1.ScrapeResults

	payload := organizationConfig{Organization: sanitizeOrganization(scrape.organization)}
	properties := organizationProperties(scrape.organization)

	if scrape.org.Settings {
		settings, errs := fetchOrganizationSettings(ctx, scrape)
		results = append(results, errs...)
		payload.Actions = settings.Actions
		payload.Rulesets = settings.Rulesets
		payload.Roles = settings.Roles
		properties = append(properties, organizationSettingsProperties(scrape.organization, settings)...)

		results = append(results, scrapeCodeSecurityConfigurations(ctx, scrape)...)
	}

	result := v1.ScrapeResult{
		Type:        ConfigTypeOrganization,
		ID:          githubOrganizationExternalID(scrape.name()),
		Name:        scrape.name(),
		ConfigClass: "Organization",
		Config:      payload,
		Tags:        v1.JSONStringMap{"owner": scrape.name()},
		CreatedAt:   scrape.organization.CreatedAt.GetTime(),
		Properties:  properties,
		ScraperLess: true,
	}

	if scrape.org.Apps {
		installations, errs := scrapeAppInstallations(ctx, scrape)
		results = append(results, errs...)
		results = append(results, installations.Results...)
		result.ExternalUsers = append(result.ExternalUsers, installations.Users...)
		result.ExternalRoles = append(result.ExternalRoles, installations.Roles...)
		result.ConfigAccess = append(result.ConfigAccess, installations.Access...)
	}

	if scrape.org.Members {
		rbac, errs := scrapeOrganizationRBAC(ctx, scrape)
		results = append(results, errs...)
		result.ExternalUsers = append(result.ExternalUsers, rbac.Users...)
		result.ExternalGroups = append(result.ExternalGroups, rbac.Groups...)
		result.ExternalRoles = append(result.ExternalRoles, rbac.Roles...)
		result.ExternalUserGroups = append(result.ExternalUserGroups, rbac.UserGroups...)
		result.ConfigAccess = append(result.ConfigAccess, rbac.Access...)
	}

	return append(results, result)
}

type organizationSettings struct {
	Actions  *organizationActions
	Rulesets []*github.RepositoryRuleset
	Roles    []organizationRole
}

// fetchOrganizationSettings reads the org policy surfaces that are not part of
// the organization object itself. Endpoints that are plan-gated or unavailable
// on GitHub Enterprise Server are skipped with a logged reason; everything else
// is surfaced as a scrape error.
func fetchOrganizationSettings(ctx api.ScrapeContext, scrape *organizationScrape) (organizationSettings, v1.ScrapeResults) {
	var settings organizationSettings
	var results v1.ScrapeResults
	org := scrape.name()

	// The security and policy fields are only returned to organization owners
	// holding admin:org; for anyone else GitHub silently omits them rather than
	// failing, so an absent default_repository_permission is the sentinel.
	if scrape.organization.DefaultRepoPermission == nil {
		results.Errorf(
			fmt.Errorf("settings require a token with the admin:org scope held by an organization owner"),
			"incomplete settings for GitHub organization %s", org,
		)
	}

	permissions, _, err := scrape.client.Client.Actions.GetActionsPermissions(ctx, org)
	switch {
	case err != nil && isOrganizationFeatureUnavailable(err):
		ctx.Logger.V(2).Infof("skipping actions permissions for %s: %v", org, err)
	case err != nil:
		results.Errorf(err, "failed to get actions permissions for GitHub organization %s", org)
	default:
		settings.Actions = &organizationActions{Permissions: permissions}
		if permissions.GetAllowedActions() == "selected" {
			allowed, _, err := scrape.client.Client.Actions.GetActionsAllowed(ctx, org)
			if err != nil {
				results.Errorf(err, "failed to get allowed actions for GitHub organization %s", org)
			} else {
				settings.Actions.Allowed = allowed
			}
		}
	}

	rulesets, err := scrape.client.ListOrganizationRulesets(ctx, org)
	switch {
	case err != nil && isOrganizationFeatureUnavailable(err):
		ctx.Logger.V(2).Infof("skipping rulesets for %s: %v", org, err)
	case err != nil:
		results.Errorf(err, "failed to list rulesets for GitHub organization %s", org)
	default:
		settings.Rulesets = rulesets
	}

	roles, err := scrape.client.ListOrganizationRoles(ctx, org)
	switch {
	case err != nil && isOrganizationFeatureUnavailable(err):
		ctx.Logger.V(2).Infof("skipping organization roles for %s: %v", org, err)
	case err != nil:
		results.Errorf(err, "failed to list roles for GitHub organization %s", org)
	default:
		settings.Roles = roles
	}

	return settings, results
}

// ListOrganizationRoles lists the organization roles along with the teams and
// users holding each of them. Assignments are only available per role, so the
// call per role is the only way GitHub exposes them.
func (c *GitHubClient) ListOrganizationRoles(ctx context.Context, org string) ([]organizationRole, error) {
	listed, _, err := c.Client.Organizations.ListRoles(ctx, org)
	if err != nil {
		return nil, fmt.Errorf("failed to list roles for github organization %s: %w", org, err)
	}

	var roles []organizationRole
	for _, role := range listed.CustomRepoRoles {
		if role.GetID() == 0 {
			return nil, fmt.Errorf("github organization %s returned role %q without an ID", org, role.GetName())
		}

		assigned := organizationRole{Role: sanitizeOrganizationRole(role)}
		options := &github.ListOptions{PerPage: 100}
		for {
			page, response, err := c.Client.Organizations.ListTeamsAssignedToOrgRole(ctx, org, role.GetID(), options)
			if err != nil {
				return nil, fmt.Errorf("failed to list teams assigned to role %q of github organization %s: %w",
					role.GetName(), org, err)
			}
			for _, team := range page {
				assigned.Teams = append(assigned.Teams, team.GetSlug())
			}
			if response.NextPage == 0 {
				break
			}
			options.Page = response.NextPage
		}

		options = &github.ListOptions{PerPage: 100}
		for {
			page, response, err := c.Client.Organizations.ListUsersAssignedToOrgRole(ctx, org, role.GetID(), options)
			if err != nil {
				return nil, fmt.Errorf("failed to list users assigned to role %q of github organization %s: %w",
					role.GetName(), org, err)
			}
			for _, user := range page {
				assigned.Users = append(assigned.Users, user.GetLogin())
			}
			if response.NextPage == 0 {
				break
			}
			options.Page = response.NextPage
		}

		roles = append(roles, assigned)
	}

	return roles, nil
}

// sanitizeOrganizationRole drops the organization object each role carries,
// which would otherwise repeat the full organization payload per role.
func sanitizeOrganizationRole(role *github.CustomOrgRoles) *github.CustomOrgRoles {
	return &github.CustomOrgRoles{
		ID:          role.ID,
		Name:        role.Name,
		Description: role.Description,
		Permissions: role.Permissions,
		BaseRole:    role.BaseRole,
		Source:      role.Source,
		CreatedAt:   role.CreatedAt,
		UpdatedAt:   role.UpdatedAt,
	}
}

func (c *GitHubClient) ListOrganizationRulesets(ctx context.Context, org string) ([]*github.RepositoryRuleset, error) {
	var rulesets []*github.RepositoryRuleset
	options := &github.ListOptions{PerPage: 100}

	for {
		page, response, err := c.Client.Organizations.GetAllRepositoryRulesets(ctx, org, options)
		if err != nil {
			return nil, fmt.Errorf("failed to list rulesets for github organization %s: %w", org, err)
		}
		rulesets = append(rulesets, page...)
		if response.NextPage == 0 {
			return rulesets, nil
		}
		options.Page = response.NextPage
	}
}

// isOrganizationFeatureUnavailable reports whether an org-level endpoint failed
// because the feature is not present for the organization — plan-gated (403) or
// missing on GitHub Enterprise Server (404) — rather than because the caller is
// not allowed to read it. See the 403 message-matching note in client.go.
func isOrganizationFeatureUnavailable(err error) bool {
	if isNotFound(err) {
		return true
	}

	msg, ok := forbiddenMessage(err)
	return ok && (strings.Contains(msg, "advanced security") ||
		strings.Contains(msg, "not available for") ||
		strings.Contains(msg, "upgrade") ||
		strings.Contains(msg, "not enabled"))
}

func organizationRepositories(owner string, repositories []v1.GitHubRepository) map[string]v1.GitHubRepository {
	owned := make(map[string]v1.GitHubRepository)
	for _, repo := range repositories {
		if strings.EqualFold(repo.Owner, owner) {
			owned[strings.ToLower(repo.Repo)] = repo
		}
	}
	return owned
}

func organizationProperties(org *github.Organization) []*types.Property {
	url := org.GetHTMLURL()
	if url == "" {
		return nil
	}

	return []*types.Property{{
		Name:  "URL",
		Type:  "url",
		Text:  url,
		Links: []types.Link{{URL: url, Type: "url"}},
	}}
}

func organizationSettingsProperties(org *github.Organization, settings organizationSettings) []*types.Property {
	properties := []*types.Property{
		booleanBadge("2FA Required", org.TwoFactorRequirementEnabled),
		booleanBadge("Advanced Security", org.AdvancedSecurityEnabledForNewRepos),
		booleanBadge("Secret Scanning", org.SecretScanningEnabledForNewRepos),
		booleanBadge("Members Can Create Repositories", org.MembersCanCreateRepos),
	}

	if permission := org.GetDefaultRepoPermission(); permission != "" {
		properties = append(properties, &types.Property{
			Name: "Default Repository Permission",
			Type: "badge",
			Text: permission,
		})
	}

	if len(settings.Rulesets) > 0 {
		properties = append(properties, &types.Property{
			Name: "Rulesets",
			Type: "number",
			Text: fmt.Sprintf("%d", len(settings.Rulesets)),
		})
	}

	return properties
}

// booleanBadge renders a tri-state setting: GitHub omits the admin-only policy
// fields entirely for callers without admin:org, and "unknown" must not be
// rendered as "false".
func booleanBadge(name string, value *bool) *types.Property {
	text := "unknown"
	if value != nil {
		text = fmt.Sprintf("%t", *value)
	}
	return &types.Property{Name: name, Type: "badge", Text: text}
}

func sanitizeOrganization(org *github.Organization) *github.Organization {
	if org == nil {
		return nil
	}

	return &github.Organization{
		ID:                          org.ID,
		NodeID:                      org.NodeID,
		Login:                       org.Login,
		Name:                        org.Name,
		Company:                     org.Company,
		Blog:                        org.Blog,
		Location:                    org.Location,
		Email:                       org.Email,
		TwitterUsername:             org.TwitterUsername,
		Description:                 org.Description,
		Type:                        org.Type,
		HTMLURL:                     org.HTMLURL,
		CreatedAt:                   org.CreatedAt,
		UpdatedAt:                   org.UpdatedAt,
		IsVerified:                  org.IsVerified,
		HasOrganizationProjects:     org.HasOrganizationProjects,
		HasRepositoryProjects:       org.HasRepositoryProjects,
		TwoFactorRequirementEnabled: org.TwoFactorRequirementEnabled,

		DefaultRepoPermission:                org.DefaultRepoPermission,
		MembersCanCreateRepos:                org.MembersCanCreateRepos,
		MembersCanCreatePublicRepos:          org.MembersCanCreatePublicRepos,
		MembersCanCreatePrivateRepos:         org.MembersCanCreatePrivateRepos,
		MembersCanCreateInternalRepos:        org.MembersCanCreateInternalRepos,
		MembersCanForkPrivateRepos:           org.MembersCanForkPrivateRepos,
		MembersCanCreatePages:                org.MembersCanCreatePages,
		MembersCanCreatePublicPages:          org.MembersCanCreatePublicPages,
		MembersCanCreatePrivatePages:         org.MembersCanCreatePrivatePages,
		WebCommitSignoffRequired:             org.WebCommitSignoffRequired,

		AdvancedSecurityEnabledForNewRepos:             org.AdvancedSecurityEnabledForNewRepos,
		DependabotAlertsEnabledForNewRepos:             org.DependabotAlertsEnabledForNewRepos,
		DependabotSecurityUpdatesEnabledForNewRepos:    org.DependabotSecurityUpdatesEnabledForNewRepos,
		DependencyGraphEnabledForNewRepos:              org.DependencyGraphEnabledForNewRepos,
		SecretScanningEnabledForNewRepos:               org.SecretScanningEnabledForNewRepos,
		SecretScanningPushProtectionEnabledForNewRepos: org.SecretScanningPushProtectionEnabledForNewRepos,
		SecretScanningValidityChecksEnabled:            org.SecretScanningValidityChecksEnabled,
	}
}
