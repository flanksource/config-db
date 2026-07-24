package github

import (
	"context"
	"fmt"
	"strings"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/google/go-github/v73/github"
	"github.com/lib/pq"
)

const (
	organizationMembershipSource = "github-org-membership"
	teamPermissionSource         = "github-team-permission"
)

type organizationRBAC struct {
	Users      []models.ExternalUser
	Groups     []models.ExternalGroup
	Roles      []models.ExternalRole
	UserGroups []v1.ExternalUserGroup
	Access     []v1.ExternalConfigAccess
}

// organizationMember pairs a member with their organization role, which
// ListMembers only conveys through the role filter used to fetch them.
type organizationMember struct {
	User *github.User
	Role string
}

type organizationTeam struct {
	Team    *github.Team
	Members []*github.User
	Repos   []*github.Repository
}

type organizationRBACInput struct {
	Owner   string
	Members []organizationMember
	Teams   []organizationTeam

	// Repositories restricts team grants to the repositories this scrape
	// produces config items for, keyed by lowercased name.
	Repositories map[string]v1.GitHubRepository
}

func scrapeOrganizationRBAC(ctx api.ScrapeContext, scrape *organizationScrape) (organizationRBAC, v1.ScrapeResults) {
	var errs v1.ScrapeResults
	org := scrape.name()

	members, err := scrape.client.ListOrganizationMembers(ctx, org)
	if err != nil {
		errs.Errorf(err, "failed to list members of GitHub organization %s", org)
		return organizationRBAC{}, errs
	}

	teams, err := scrape.client.ListOrganizationTeams(ctx, org)
	if err != nil {
		errs.Errorf(err, "failed to list teams of GitHub organization %s", org)
		return organizationRBAC{}, errs
	}

	input := organizationRBACInput{Owner: org, Members: members, Repositories: scrape.repositories}
	for _, team := range teams {
		slug := team.GetSlug()
		if slug == "" {
			errs.Errorf(fmt.Errorf("missing slug"), "invalid team %q in GitHub organization %s", team.GetName(), org)
			continue
		}

		teamMembers, err := scrape.client.ListTeamMembers(ctx, org, slug)
		if err != nil {
			errs.Errorf(err, "failed to list members of GitHub team %s/%s", org, slug)
			continue
		}

		teamRepos, err := scrape.client.ListTeamRepositories(ctx, org, slug)
		if err != nil {
			errs.Errorf(err, "failed to list repositories of GitHub team %s/%s", org, slug)
			continue
		}

		input.Teams = append(input.Teams, organizationTeam{Team: team, Members: teamMembers, Repos: teamRepos})
	}

	rbac, err := buildOrganizationRBAC(input)
	if err != nil {
		errs.Errorf(err, "failed to map RBAC for GitHub organization %s", org)
		return organizationRBAC{}, errs
	}

	return rbac, errs
}

// ListOrganizationMembers lists owners and non-owners separately: the members
// endpoint does not report a member's role, so the role filter is the only way
// to learn it without an N+1 membership lookup per member.
func (c *GitHubClient) ListOrganizationMembers(ctx context.Context, org string) ([]organizationMember, error) {
	var members []organizationMember

	for _, role := range []string{"admin", "member"} {
		options := &github.ListMembersOptions{Role: role, ListOptions: github.ListOptions{PerPage: 100}}
		for {
			page, response, err := c.Client.Organizations.ListMembers(ctx, org, options)
			if err != nil {
				return nil, fmt.Errorf("failed to list %s members of github organization %s: %w", role, org, err)
			}
			for _, user := range page {
				members = append(members, organizationMember{User: user, Role: role})
			}
			if response.NextPage == 0 {
				break
			}
			options.Page = response.NextPage
		}
	}

	return members, nil
}

func (c *GitHubClient) ListOrganizationTeams(ctx context.Context, org string) ([]*github.Team, error) {
	var teams []*github.Team
	options := &github.ListOptions{PerPage: 100}

	for {
		page, response, err := c.Client.Teams.ListTeams(ctx, org, options)
		if err != nil {
			return nil, fmt.Errorf("failed to list teams of github organization %s: %w", org, err)
		}
		teams = append(teams, page...)
		if response.NextPage == 0 {
			return teams, nil
		}
		options.Page = response.NextPage
	}
}

func (c *GitHubClient) ListTeamMembers(ctx context.Context, org, slug string) ([]*github.User, error) {
	var members []*github.User
	options := &github.TeamListTeamMembersOptions{ListOptions: github.ListOptions{PerPage: 100}}

	for {
		page, response, err := c.Client.Teams.ListTeamMembersBySlug(ctx, org, slug, options)
		if err != nil {
			return nil, fmt.Errorf("failed to list members of github team %s/%s: %w", org, slug, err)
		}
		members = append(members, page...)
		if response.NextPage == 0 {
			return members, nil
		}
		options.Page = response.NextPage
	}
}

func (c *GitHubClient) ListTeamRepositories(ctx context.Context, org, slug string) ([]*github.Repository, error) {
	var repositories []*github.Repository
	options := &github.ListOptions{PerPage: 100}

	for {
		page, response, err := c.Client.Teams.ListTeamReposBySlug(ctx, org, slug, options)
		if err != nil {
			return nil, fmt.Errorf("failed to list repositories of github team %s/%s: %w", org, slug, err)
		}
		repositories = append(repositories, page...)
		if response.NextPage == 0 {
			return repositories, nil
		}
		options.Page = response.NextPage
	}
}

func buildOrganizationRBAC(input organizationRBACInput) (organizationRBAC, error) {
	builder := organizationRBACBuilder{
		owner:            input.Owner,
		organizationID:   githubOrganizationExternalID(input.Owner),
		repositories:     input.Repositories,
		seenUsers:        make(map[string]struct{}),
		seenGroups:       make(map[string]struct{}),
		seenRoles:        make(map[string]struct{}),
		seenUserGroups:   make(map[string]struct{}),
		seenOrgAccess:    make(map[string]struct{}),
		seenRepoAccesses: make(map[string]struct{}),
	}

	for _, member := range input.Members {
		if err := builder.addMember(member); err != nil {
			return organizationRBAC{}, fmt.Errorf("github organization %s: %w", input.Owner, err)
		}
	}

	effectiveMembers, err := effectiveTeamMembers(input.Teams)
	if err != nil {
		return organizationRBAC{}, fmt.Errorf("github organization %s: %w", input.Owner, err)
	}

	for _, team := range input.Teams {
		if err := builder.addTeam(team, effectiveMembers[team.Team.GetID()]); err != nil {
			return organizationRBAC{}, fmt.Errorf("github organization %s: %w", input.Owner, err)
		}
	}

	return builder.result, nil
}

// effectiveTeamMembers resolves each team's member set including the members it
// inherits from its ancestors: GitHub grants a parent team's members implicit
// membership of every descendant team. The hierarchy is flattened here because
// config_access_unwrapped joins external_user_groups a single level only, so a
// nested group would drop the inherited grants.
func effectiveTeamMembers(teams []organizationTeam) (map[int64][]*github.User, error) {
	byID := make(map[int64]organizationTeam, len(teams))
	for _, team := range teams {
		if team.Team == nil || team.Team.GetID() == 0 {
			return nil, fmt.Errorf("returned a team without a stable ID")
		}
		byID[team.Team.GetID()] = team
	}

	effective := make(map[int64][]*github.User, len(teams))
	for _, team := range teams {
		var members []*github.User
		seenUsers := make(map[int64]struct{})
		visited := make(map[int64]struct{})

		for current := team; ; {
			id := current.Team.GetID()
			if _, ok := visited[id]; ok {
				return nil, fmt.Errorf("team %q is part of a parent cycle", current.Team.GetName())
			}
			visited[id] = struct{}{}

			for _, member := range current.Members {
				if _, ok := seenUsers[member.GetID()]; ok {
					continue
				}
				seenUsers[member.GetID()] = struct{}{}
				members = append(members, member)
			}

			parentID := current.Team.GetParent().GetID()
			if parentID == 0 {
				break
			}
			parent, ok := byID[parentID]
			if !ok {
				break
			}
			current = parent
		}

		effective[team.Team.GetID()] = members
	}

	return effective, nil
}

type organizationRBACBuilder struct {
	owner          string
	organizationID string
	repositories   map[string]v1.GitHubRepository
	result         organizationRBAC

	seenUsers        map[string]struct{}
	seenGroups       map[string]struct{}
	seenRoles        map[string]struct{}
	seenUserGroups   map[string]struct{}
	seenOrgAccess    map[string]struct{}
	seenRepoAccesses map[string]struct{}
}

func (b *organizationRBACBuilder) addMember(member organizationMember) error {
	alias, err := b.addUser(member.User)
	if err != nil {
		return err
	}

	roleAlias := b.addRole(organizationRoleAlias(b.owner, member.Role), "GitHub::Organization", member.Role)
	if _, ok := b.seenOrgAccess[alias]; ok {
		return nil
	}
	b.seenOrgAccess[alias] = struct{}{}

	b.result.Access = append(b.result.Access, v1.ExternalConfigAccess{
		Source: github.Ptr(organizationMembershipSource),
		// The organization config is scraper-less, so it is only resolvable
		// with an explicit "all" scraper.
		ConfigExternalID: v1.ExternalID{
			ConfigType: ConfigTypeOrganization,
			ExternalID: b.organizationID,
			ScraperID:  "all",
		},
		ExternalUserAliases: []string{alias},
		ExternalRoleAliases: []string{roleAlias},
	})

	return nil
}

func (b *organizationRBACBuilder) addTeam(team organizationTeam, members []*github.User) error {
	if team.Team == nil {
		return fmt.Errorf("returned a nil team")
	}

	groupAlias, err := githubTeamAlias(team.Team)
	if err != nil {
		return fmt.Errorf("invalid github team %q: %w", team.Team.GetName(), err)
	}

	if _, ok := b.seenGroups[groupAlias]; !ok {
		b.seenGroups[groupAlias] = struct{}{}
		b.result.Groups = append(b.result.Groups, models.ExternalGroup{
			Tenant:    b.owner,
			Aliases:   pq.StringArray{groupAlias},
			Name:      team.Team.GetName(),
			GroupType: "GitHub::Team",
		})
	}

	for _, member := range members {
		userAlias, err := b.addUser(member)
		if err != nil {
			return fmt.Errorf("github team %q: %w", team.Team.GetName(), err)
		}

		key := userAlias + "\x00" + groupAlias
		if _, ok := b.seenUserGroups[key]; ok {
			continue
		}
		b.seenUserGroups[key] = struct{}{}

		b.result.UserGroups = append(b.result.UserGroups, v1.ExternalUserGroup{
			ExternalUserAliases:  []string{userAlias},
			ExternalGroupAliases: []string{groupAlias},
		})
	}

	for _, repo := range team.Repos {
		name := repo.GetName()
		if _, ok := b.repositories[strings.ToLower(name)]; !ok {
			continue
		}

		role, err := effectiveRepositoryRole(repo.GetRoleName(), repo.GetPermissions())
		if err != nil {
			return fmt.Errorf("github team %q on repository %q: %w", team.Team.GetName(), name, err)
		}

		roleAlias := b.addRole(githubRepositoryRoleAlias(b.owner, role), "GitHub::Repository", role)
		key := groupAlias + "\x00" + roleAlias + "\x00" + strings.ToLower(name)
		if _, ok := b.seenRepoAccesses[key]; ok {
			continue
		}
		b.seenRepoAccesses[key] = struct{}{}

		b.result.Access = append(b.result.Access, v1.ExternalConfigAccess{
			Source: github.Ptr(teamPermissionSource),
			ConfigExternalID: v1.ExternalID{
				ConfigType: ConfigTypeRepository,
				ExternalID: githubRepositoryExternalID(b.owner, name),
			},
			ExternalGroupAliases: []string{groupAlias},
			ExternalRoleAliases:  []string{roleAlias},
		})
	}

	return nil
}

func (b *organizationRBACBuilder) addUser(user *github.User) (string, error) {
	if user == nil {
		return "", fmt.Errorf("returned a nil user")
	}

	alias, err := githubUserAlias(user)
	if err != nil {
		return "", fmt.Errorf("invalid github user %q: %w", user.GetLogin(), err)
	}

	if _, ok := b.seenUsers[alias]; ok {
		return alias, nil
	}
	b.seenUsers[alias] = struct{}{}

	external := models.ExternalUser{
		Aliases:  pq.StringArray{alias},
		Name:     user.GetLogin(),
		Tenant:   b.owner,
		UserType: githubUserType(user.GetType()),
	}
	if email := strings.TrimSpace(user.GetEmail()); email != "" {
		external.Email = github.Ptr(email)
	}
	b.result.Users = append(b.result.Users, external)

	return alias, nil
}

func (b *organizationRBACBuilder) addRole(alias, roleType, name string) string {
	if _, ok := b.seenRoles[alias]; ok {
		return alias
	}
	b.seenRoles[alias] = struct{}{}

	b.result.Roles = append(b.result.Roles, models.ExternalRole{
		Tenant:   b.owner,
		Aliases:  pq.StringArray{alias},
		RoleType: roleType,
		Name:     name,
	})

	return alias
}

func organizationRoleAlias(owner, role string) string {
	return fmt.Sprintf(
		"github://org-role/%s/%s",
		strings.ToLower(strings.TrimSpace(owner)),
		strings.ToLower(strings.TrimSpace(role)),
	)
}
