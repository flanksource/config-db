package github

import (
	"context"
	"fmt"
	"strings"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/google/go-github/v73/github"
	"github.com/lib/pq"
)

const repositoryPermissionSource = "github-repository-permission"

var repositoryPermissionPrecedence = []string{"admin", "maintain", "push", "triage", "pull"}

type repositoryAccess struct {
	Users  []models.ExternalUser
	Groups []models.ExternalGroup
	Roles  []models.ExternalRole
	Access []v1.ExternalConfigAccess
}

type repositoryAccessInput struct {
	Owner         string
	Repository    string
	Collaborators []*github.User
	Teams         []*github.Team
}

type repositoryAccessFetchOptions struct {
	Client     *GitHubClient
	Repository *github.Repository
}

type repositoryAccessBuilder struct {
	owner        string
	repositoryID string
	result       repositoryAccess
	seenUsers    map[string]struct{}
	seenGroups   map[string]struct{}
	seenRoles    map[string]struct{}
	seenAccess   map[string]struct{}
}

type repositoryPrincipal struct {
	role       string
	userAlias  string
	groupAlias string
}

func fetchRepositoryAccess(ctx context.Context, options repositoryAccessFetchOptions) (repositoryAccess, error) {
	collaborators, err := options.Client.ListRepositoryCollaborators(ctx)
	if err != nil {
		return repositoryAccess{}, err
	}

	var teams []*github.Team
	if repositoryOrganizationOwner(options.Repository) != nil {
		teams, err = options.Client.ListRepositoryTeams(ctx)
		if err != nil {
			return repositoryAccess{}, err
		}
	}

	return buildRepositoryAccess(repositoryAccessInput{
		Owner:         options.Client.owner,
		Repository:    options.Client.repo,
		Collaborators: collaborators,
		Teams:         teams,
	})
}

func (c *GitHubClient) ListRepositoryCollaborators(ctx context.Context) ([]*github.User, error) {
	var collaborators []*github.User
	options := &github.ListCollaboratorsOptions{
		Affiliation: "all",
		ListOptions: github.ListOptions{PerPage: 100},
	}

	for {
		page, response, err := c.Client.Repositories.ListCollaborators(ctx, c.owner, c.repo, options)
		if err != nil {
			return nil, fmt.Errorf("failed to list collaborators for github repository %s/%s: %w", c.owner, c.repo, err)
		}
		collaborators = append(collaborators, page...)
		if response.NextPage == 0 {
			return collaborators, nil
		}
		options.Page = response.NextPage
	}
}

func (c *GitHubClient) ListRepositoryTeams(ctx context.Context) ([]*github.Team, error) {
	var teams []*github.Team
	options := &github.ListOptions{PerPage: 100}

	for {
		page, response, err := c.Client.Repositories.ListTeams(ctx, c.owner, c.repo, options)
		if err != nil {
			return nil, fmt.Errorf("failed to list teams for github repository %s/%s: %w", c.owner, c.repo, err)
		}
		teams = append(teams, page...)
		if response.NextPage == 0 {
			return teams, nil
		}
		options.Page = response.NextPage
	}
}

func buildRepositoryAccess(input repositoryAccessInput) (repositoryAccess, error) {
	builder := repositoryAccessBuilder{
		owner:        input.Owner,
		repositoryID: githubRepositoryExternalID(input.Owner, input.Repository),
		seenUsers:    make(map[string]struct{}),
		seenGroups:   make(map[string]struct{}),
		seenRoles:    make(map[string]struct{}),
		seenAccess:   make(map[string]struct{}),
	}
	for _, collaborator := range input.Collaborators {
		if err := builder.addCollaborator(collaborator); err != nil {
			return repositoryAccess{}, fmt.Errorf("github repository %s/%s: %w", input.Owner, input.Repository, err)
		}
	}
	for _, team := range input.Teams {
		if err := builder.addTeam(team); err != nil {
			return repositoryAccess{}, fmt.Errorf("github repository %s/%s: %w", input.Owner, input.Repository, err)
		}
	}
	return builder.result, nil
}

func (b *repositoryAccessBuilder) addCollaborator(collaborator *github.User) error {
	if collaborator == nil {
		return fmt.Errorf("returned a nil collaborator")
	}
	alias, err := githubUserAlias(collaborator)
	if err != nil {
		return fmt.Errorf("invalid github collaborator %q: %w", collaborator.GetLogin(), err)
	}
	role, err := effectiveRepositoryRole(collaborator.GetRoleName(), collaborator.Permissions)
	if err != nil {
		return fmt.Errorf("github collaborator %q: %w", collaborator.GetLogin(), err)
	}
	if _, ok := b.seenUsers[alias]; !ok {
		b.seenUsers[alias] = struct{}{}
		user := models.ExternalUser{
			Aliases:  pq.StringArray{alias},
			Name:     collaborator.GetLogin(),
			Tenant:   b.owner,
			UserType: githubUserType(collaborator.GetType()),
		}
		if email := strings.TrimSpace(collaborator.GetEmail()); email != "" {
			user.Email = github.Ptr(email)
		}
		b.result.Users = append(b.result.Users, user)
	}
	b.addAccess(repositoryPrincipal{role: role, userAlias: alias})
	return nil
}

func (b *repositoryAccessBuilder) addTeam(team *github.Team) error {
	if team == nil {
		return fmt.Errorf("returned a nil team")
	}
	alias, err := githubTeamAlias(team)
	if err != nil {
		return fmt.Errorf("invalid github team %q: %w", team.GetName(), err)
	}
	role, err := effectiveRepositoryRole(team.GetPermission(), team.Permissions)
	if err != nil {
		return fmt.Errorf("github team %q: %w", team.GetName(), err)
	}
	if _, ok := b.seenGroups[alias]; !ok {
		b.seenGroups[alias] = struct{}{}
		b.result.Groups = append(b.result.Groups, models.ExternalGroup{
			Tenant:    b.owner,
			Aliases:   pq.StringArray{alias},
			Name:      team.GetName(),
			GroupType: "GitHub::Team",
		})
	}
	b.addAccess(repositoryPrincipal{role: role, groupAlias: alias})
	return nil
}

func (b *repositoryAccessBuilder) addAccess(principal repositoryPrincipal) {
	roleAlias := githubRepositoryRoleAlias(b.owner, principal.role)
	if _, ok := b.seenRoles[roleAlias]; !ok {
		b.seenRoles[roleAlias] = struct{}{}
		b.result.Roles = append(b.result.Roles, models.ExternalRole{
			Tenant:   b.owner,
			Aliases:  pq.StringArray{roleAlias},
			RoleType: "GitHub::Repository",
			Name:     principal.role,
		})
	}

	principalAlias := principal.userAlias
	if principalAlias == "" {
		principalAlias = principal.groupAlias
	}
	accessKey := principalAlias + "\x00" + roleAlias
	if _, ok := b.seenAccess[accessKey]; ok {
		return
	}
	b.seenAccess[accessKey] = struct{}{}

	b.result.Access = append(b.result.Access, v1.ExternalConfigAccess{
		Source:               github.Ptr(repositoryPermissionSource),
		ConfigExternalID:     v1.ExternalID{ConfigType: ConfigTypeRepository, ExternalID: b.repositoryID},
		ExternalUserAliases:  nonEmptyAlias(principal.userAlias),
		ExternalGroupAliases: nonEmptyAlias(principal.groupAlias),
		ExternalRoleAliases:  []string{roleAlias},
	})
}

func githubUserAlias(user *github.User) (string, error) {
	if user.GetID() == 0 {
		return "", fmt.Errorf("missing stable github user ID")
	}
	if strings.TrimSpace(user.GetLogin()) == "" {
		return "", fmt.Errorf("missing github login for user ID %d", user.GetID())
	}
	return fmt.Sprintf("github://user/%d", user.GetID()), nil
}

func githubTeamAlias(team *github.Team) (string, error) {
	if team.GetID() == 0 {
		return "", fmt.Errorf("missing stable github team ID")
	}
	if strings.TrimSpace(team.GetName()) == "" {
		return "", fmt.Errorf("missing github team name for team ID %d", team.GetID())
	}
	return fmt.Sprintf("github://team/%d", team.GetID()), nil
}

func githubRepositoryRoleAlias(owner, role string) string {
	return fmt.Sprintf(
		"github://repository-role/%s/%s",
		strings.ToLower(strings.TrimSpace(owner)),
		strings.ToLower(strings.TrimSpace(role)),
	)
}

func githubUserType(userType string) string {
	if userType = strings.TrimSpace(userType); userType != "" {
		return "GitHub::" + userType
	}
	return "GitHub::User"
}

func effectiveRepositoryRole(roleName string, permissions map[string]bool) (string, error) {
	if roleName = strings.TrimSpace(roleName); roleName != "" {
		return canonicalRepositoryRole(roleName), nil
	}
	for _, permission := range repositoryPermissionPrecedence {
		if permissions[permission] {
			return canonicalRepositoryRole(permission), nil
		}
	}
	return "", fmt.Errorf("missing effective repository role")
}

func canonicalRepositoryRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "pull":
		return "read"
	case "push":
		return "write"
	default:
		return strings.TrimSpace(role)
	}
}

func nonEmptyAlias(alias string) []string {
	if alias == "" {
		return nil
	}
	return []string{alias}
}
