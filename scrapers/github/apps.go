package github

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/google/go-github/v73/github"
	"github.com/lib/pq"
)

const appInstallationSource = "github-app-installation"

// nonRepositoryPermissionPrefix marks the permissions GitHub scopes to the
// organization rather than to its repositories. They are excluded when
// collapsing an installation's permission set into a repository role so that,
// say, organization_administration:write does not read as repository admin.
const nonRepositoryPermissionPrefix = "organization_"

// nonRepositoryPermissions are the remaining organization- and user-scoped
// permission names that do not carry the organization_ prefix. Anything not
// listed here counts towards the repository role, so a permission GitHub adds
// later is included rather than silently dropped.
var nonRepositoryPermissions = map[string]struct{}{
	"blocking":                    {},
	"codespaces_lifecycle_admin":  {},
	"codespaces_user_secrets":     {},
	"copilot_messages":            {},
	"emails":                      {},
	"followers":                   {},
	"gists":                       {},
	"git_signing_ssh_public_keys": {},
	"gpg_keys":                    {},
	"interaction_limits":          {},
	"keys":                        {},
	"members":                     {},
	"plan":                        {},
	"profile":                     {},
	"starring":                    {},
	"team_discussions":            {},
	"user_events":                 {},
	"watching":                    {},
}

// appInstallationConfig is the config payload for a GitHub::AppInstallation.
// Permissions is the flat form of the installation's permission set; the
// repository role derived from it is deliberately coarse, so keeping the map
// means no grant detail is lost.
type appInstallationConfig struct {
	Installation *github.Installation `json:"installation"`
	Permissions  map[string]string    `json:"permissions,omitempty"`
}

type appInstallations struct {
	Results v1.ScrapeResults
	Users   []models.ExternalUser
	Roles   []models.ExternalRole
	Access  []v1.ExternalConfigAccess
}

// installationRepositories pairs an installation with the repositories it can
// reach that are also in the scrape's resolved repository set.
type installationRepositories struct {
	Installation      *github.Installation
	Repositories      []string
	RepositoriesKnown bool
}

type appInstallationInput struct {
	Owner         string
	BaseScraper   v1.BaseScraper
	Installations []installationRepositories
}

func scrapeAppInstallations(ctx api.ScrapeContext, scrape *organizationScrape) (appInstallations, v1.ScrapeResults) {
	var errs v1.ScrapeResults
	org := scrape.name()

	installations, err := scrape.client.ListOrganizationInstallations(ctx, org)
	if err != nil {
		errs.Errorf(err, "failed to list app installations for GitHub organization %s", org)
		return appInstallations{}, errs
	}

	input := appInstallationInput{Owner: org, BaseScraper: scrape.spec.BaseScraper}
	for _, installation := range installations {
		repositories, known := resolveInstallationRepositories(scrape, installation)
		input.Installations = append(input.Installations, installationRepositories{
			Installation:      installation,
			Repositories:      repositories,
			RepositoriesKnown: known,
		})
	}

	result, err := buildAppInstallations(input)
	if err != nil {
		errs.Errorf(err, "failed to map app installations for GitHub organization %s", org)
		return appInstallations{}, errs
	}

	return result, errs
}

// resolveInstallationRepositories returns exact repository grants only when
// the organization endpoint says the installation covers all repositories.
// That endpoint does not expose the repository names for selected grants.
func resolveInstallationRepositories(
	scrape *organizationScrape,
	installation *github.Installation,
) ([]string, bool) {
	if installation.GetRepositorySelection() != "all" {
		return nil, false
	}

	names := make([]string, 0, len(scrape.repositories))
	for _, repo := range scrape.repositories {
		names = append(names, repo.Repo)
	}
	sort.Strings(names)
	return names, true
}

func (c *GitHubClient) ListOrganizationInstallations(ctx context.Context, org string) ([]*github.Installation, error) {
	var installations []*github.Installation
	options := &github.ListOptions{PerPage: 100}

	for {
		page, response, err := c.Client.Organizations.ListInstallations(ctx, org, options)
		if err != nil {
			return nil, fmt.Errorf("failed to list app installations for github organization %s: %w", org, err)
		}
		installations = append(installations, page.Installations...)
		if response.NextPage == 0 {
			return installations, nil
		}
		options.Page = response.NextPage
	}
}

func buildAppInstallations(input appInstallationInput) (appInstallations, error) {
	var result appInstallations
	seenRoles := make(map[string]struct{})

	for _, installed := range input.Installations {
		installation := installed.Installation
		if installation == nil {
			return appInstallations{}, fmt.Errorf("returned a nil app installation")
		}

		alias, err := githubAppAlias(installation)
		if err != nil {
			return appInstallations{}, fmt.Errorf("invalid github app installation %d: %w", installation.GetID(), err)
		}

		permissions, err := installationPermissionMap(installation.Permissions)
		if err != nil {
			return appInstallations{}, fmt.Errorf("github app %q: %w", installation.GetAppSlug(), err)
		}

		result.Results = append(result.Results, buildAppInstallationResult(input, installed, permissions))
		result.Users = append(result.Users, models.ExternalUser{
			Aliases:  pq.StringArray{alias},
			Name:     installation.GetAppSlug(),
			Tenant:   input.Owner,
			UserType: "GitHub::App",
		})

		if len(installed.Repositories) == 0 {
			continue
		}

		role, err := effectiveInstallationRole(permissions)
		if err != nil {
			return appInstallations{}, fmt.Errorf("github app %q: %w", installation.GetAppSlug(), err)
		}
		roleAlias := githubRepositoryRoleAlias(input.Owner, role)
		if _, ok := seenRoles[roleAlias]; !ok {
			seenRoles[roleAlias] = struct{}{}
			result.Roles = append(result.Roles, models.ExternalRole{
				Tenant:   input.Owner,
				Aliases:  pq.StringArray{roleAlias},
				RoleType: "GitHub::Repository",
				Name:     role,
			})
		}

		for _, repo := range installed.Repositories {
			result.Access = append(result.Access, v1.ExternalConfigAccess{
				Source: github.Ptr(appInstallationSource),
				ConfigExternalID: v1.ExternalID{
					ConfigType: ConfigTypeRepository,
					ExternalID: githubRepositoryExternalID(input.Owner, repo),
				},
				ExternalUserAliases: []string{alias},
				ExternalRoleAliases: []string{roleAlias},
			})
		}
	}

	return result, nil
}

func buildAppInstallationResult(
	input appInstallationInput,
	installed installationRepositories,
	permissions map[string]string,
) v1.ScrapeResult {
	installation := installed.Installation
	externalConfigID := githubAppInstallationExternalID(input.Owner, installation.GetID())

	properties := []*types.Property{
		{Name: "Repository Selection", Type: "badge", Text: installation.GetRepositorySelection()},
		{Name: "Suspended", Type: "badge", Text: fmt.Sprintf("%t", installation.SuspendedAt != nil)},
	}
	if installed.RepositoriesKnown {
		properties = append(properties, &types.Property{
			Name: "Repositories",
			Type: "number",
			Text: fmt.Sprintf("%d", len(installed.Repositories)),
		})
	}
	if url := installation.GetHTMLURL(); url != "" {
		properties = append(properties, &types.Property{
			Name:  "URL",
			Type:  "url",
			Text:  url,
			Links: []types.Link{{URL: url, Type: "url"}},
		})
	}

	relationships := []v1.RelationshipResult{{
		ConfigExternalID: v1.ExternalID{
			ConfigType: ConfigTypeOrganization,
			ExternalID: githubOrganizationExternalID(input.Owner),
			ScraperID:  "all",
		},
		RelatedExternalID: v1.ExternalID{
			ConfigType: ConfigTypeAppInstallation,
			ExternalID: externalConfigID,
		},
		Relationship: RelationshipGitHubOrganizationAppInstallation,
	}}

	for _, repo := range installed.Repositories {
		relationships = append(relationships, v1.RelationshipResult{
			ConfigExternalID: v1.ExternalID{
				ConfigType: ConfigTypeAppInstallation,
				ExternalID: externalConfigID,
			},
			RelatedExternalID: v1.ExternalID{
				ConfigType: ConfigTypeRepository,
				ExternalID: githubRepositoryExternalID(input.Owner, repo),
			},
			Relationship: RelationshipGitHubAppInstallationRepository,
		})
	}

	return v1.ScrapeResult{
		BaseScraper: input.BaseScraper,
		Type:        ConfigTypeAppInstallation,
		ID:          externalConfigID,
		Name:        installation.GetAppSlug(),
		ConfigClass: "AppInstallation",
		Config: appInstallationConfig{
			Installation: sanitizeInstallation(installation),
			Permissions:  permissions,
		},
		Tags: v1.JSONStringMap{
			"owner": input.Owner,
			"app":   installation.GetAppSlug(),
		},
		CreatedAt:           installation.CreatedAt.GetTime(),
		Properties:          properties,
		RelationshipResults: relationships,
	}
}

// installationPermissionMap flattens the permission struct into its wire form.
// InstallationPermissions is a flat struct of *string with omitempty json tags,
// so a round trip yields exactly the permissions GitHub granted.
func installationPermissionMap(permissions *github.InstallationPermissions) (map[string]string, error) {
	if permissions == nil {
		return nil, nil
	}

	encoded, err := json.Marshal(permissions)
	if err != nil {
		return nil, fmt.Errorf("failed to encode installation permissions: %w", err)
	}

	var flattened map[string]string
	if err := json.Unmarshal(encoded, &flattened); err != nil {
		return nil, fmt.Errorf("failed to decode installation permissions: %w", err)
	}

	return flattened, nil
}

// effectiveInstallationRole collapses an installation's permission set into the
// repository role vocabulary shared with collaborators and teams, so apps show
// up alongside users in the access view.
func effectiveInstallationRole(permissions map[string]string) (string, error) {
	var write bool
	var read bool

	for permission, level := range permissions {
		if strings.HasPrefix(permission, nonRepositoryPermissionPrefix) {
			continue
		}
		if _, ok := nonRepositoryPermissions[permission]; ok {
			continue
		}

		switch strings.ToLower(strings.TrimSpace(level)) {
		case "admin":
			return "admin", nil
		case "write":
			if permission == "administration" {
				return "admin", nil
			}
			write = true
		case "read":
			read = true
		}
	}

	switch {
	case write:
		return "write", nil
	case read:
		return "read", nil
	default:
		return "", fmt.Errorf("missing effective repository role")
	}
}

func githubAppAlias(installation *github.Installation) (string, error) {
	if installation.GetAppID() == 0 {
		return "", fmt.Errorf("missing stable github app ID")
	}
	if strings.TrimSpace(installation.GetAppSlug()) == "" {
		return "", fmt.Errorf("missing github app slug for app ID %d", installation.GetAppID())
	}
	return fmt.Sprintf("github://app/%d", installation.GetAppID()), nil
}

func sanitizeInstallation(installation *github.Installation) *github.Installation {
	if installation == nil {
		return nil
	}

	return &github.Installation{
		ID:                  installation.ID,
		NodeID:              installation.NodeID,
		AppID:               installation.AppID,
		AppSlug:             installation.AppSlug,
		TargetID:            installation.TargetID,
		TargetType:          installation.TargetType,
		Account:             sanitizeActor(installation.Account),
		HTMLURL:             installation.HTMLURL,
		RepositorySelection: installation.RepositorySelection,
		Events:              installation.Events,
		SingleFileName:      installation.SingleFileName,
		SingleFilePaths:     installation.SingleFilePaths,
		SuspendedBy:         sanitizeActor(installation.SuspendedBy),
		SuspendedAt:         installation.SuspendedAt,
		CreatedAt:           installation.CreatedAt,
		UpdatedAt:           installation.UpdatedAt,
	}
}
