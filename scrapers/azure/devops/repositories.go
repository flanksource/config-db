package devops

import (
	"fmt"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/hash"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

const RepositoryType = "AzureDevops::Repository"

func RepositoryExternalID(organization, project, repoID string) string {
	return fmt.Sprintf("azuredevops://%s/%s/repository/%s", organization, project, repoID)
}

func (ado AzureDevopsScraper) scrapeRepositories(
	ctx api.ScrapeContext,
	client *AzureDevopsClient,
	config v1.AzureDevops,
	project Project,
) v1.ScrapeResults {
	repos, err := client.GetRepositories(ctx, project.Name)
	if err != nil {
		var results v1.ScrapeResults
		results.Errorf(err, "failed to get repositories for %s", project.Name)
		return results
	}

	ctx.Logger.V(3).Infof("[%s] found %d repositories", project.Name, len(repos))

	var results v1.ScrapeResults
	for _, repo := range repos {
		if !collections.MatchItems(repo.Name, config.Repositories...) {
			continue
		}

		id := RepositoryExternalID(config.Organization, project.Name, repo.ID)

		configData := map[string]any{
			"id":            repo.ID,
			"name":          repo.Name,
			"defaultBranch": repo.DefaultBranch,
			"remoteUrl":     repo.RemoteURL,
			"sshUrl":        repo.SSHURL,
			"size":          repo.Size,
			"isDisabled":    repo.IsDisabled,
			"isFork":        repo.IsFork,
			"project":       project.Name,
			"organization":  config.Organization,
		}

		var properties types.Properties
		if repo.WebURL != "" {
			properties = append(properties, &types.Property{
				Name:  "Source",
				Links: []types.Link{{Type: "source", URL: repo.WebURL}},
			})
		}

		result := v1.ScrapeResult{
			BaseScraper: config.BaseScraper,
			ConfigClass: "Repository",
			Config:      configData,
			Type:        RepositoryType,
			ID:          id,
			Name:        repo.Name,
			Properties:  properties,
		}

		if config.Permissions != nil && config.Permissions.Enabled {
			repoKey := fmt.Sprintf("repo/%s/%s/%s", config.Organization, project.Name, repo.ID)
			if shouldFetchPermissions(repoKey, parsePermissionsInterval(config.Permissions.RateLimit)) {
				result.ConfigAccess, result.ExternalRoles = ado.fetchRepoPermissions(ctx, client, config, project, repo, id)
				markPermissionsFetched(repoKey)
			}
		}

		results = append(results, result)
	}

	return results
}

func (ado AzureDevopsScraper) fetchRepoPermissions(
	ctx api.ScrapeContext,
	client *AzureDevopsClient,
	config v1.AzureDevops,
	project Project,
	repo GitRepository,
	repoExternalID string,
) ([]v1.ExternalConfigAccess, []dutyModels.ExternalRole) {
	acls, err := client.GetRepositoryPermissions(ctx, project.ID, repo.ID)
	if err != nil {
		ctx.Logger.Warnf("failed to get permissions for repo %s/%s: %v", project.Name, repo.Name, err)
		return nil, nil
	}

	gitPerms := ParseGitPermissions(acls)
	if len(gitPerms) == 0 {
		return nil, nil
	}

	var descriptors []string
	for _, p := range gitPerms {
		descriptors = append(descriptors, p.IdentityDescriptor)
	}

	identities, err := client.GetIdentitiesByDescriptor(ctx, descriptors)
	if err != nil {
		ctx.Logger.Warnf("failed to resolve identities for repo %s/%s: %v", project.Name, repo.Name, err)
		return nil, nil
	}

	identityMap := BuildIdentityMap(identities)

	var roleMapping map[string][]string
	if config.Permissions != nil {
		roleMapping = config.Permissions.Roles
	}

	roleIDs := make(map[string]uuid.UUID)
	var roles []dutyModels.ExternalRole
	var configAccess []v1.ExternalConfigAccess

	for _, perm := range gitPerms {
		identity, ok := identityMap[perm.IdentityDescriptor]
		if !ok {
			continue
		}

		name := ResolvedIdentityName(identity, project.Name)
		email := emailFromIdentity(identity)
		if name == "" && email == "" {
			continue
		}

		if identity.IsContainer {
			groupID, err := DescriptorID(identity.Descriptor)
			if err != nil {
				continue
			}
			aliases := append(DescriptorAliases(identity.Descriptor), identity.SubjectDescriptor)
			aliases = append(aliases, DescriptorAliases(identity.SubjectDescriptor)...)
			ctx.AddGroup(dutyModels.ExternalGroup{
				ID:        groupID,
				Name:      name,
				Aliases:   pq.StringArray(aliases),
				Tenant:    config.Organization,
				GroupType: "AzureDevOps",
			})
		} else {
			ctx.AddUser(dutyModels.ExternalUser{
				Name:     name,
				Email:    &email,
				Aliases:  pq.StringArray{email, identity.Descriptor, identity.SubjectDescriptor},
				Tenant:   config.Organization,
				UserType: "AzureDevOps",
			})
		}

		resolvedRoles := ResolveGitRoles(perm.Permissions, roleMapping)

		for _, roleName := range resolvedRoles {
			if _, exists := roleIDs[roleName]; !exists {
				roleID, err := hash.DeterministicUUID(pq.StringArray{roleName})
				if err != nil {
					continue
				}
				roleIDs[roleName] = roleID
				roles = append(roles, dutyModels.ExternalRole{
					ID:       roleID,
					Name:     roleName,
					RoleType: "AzureDevOps",
					Tenant:   config.Organization,
				})
			}

			access := v1.ExternalConfigAccess{
				ConfigExternalID:    v1.ExternalID{ConfigType: RepositoryType, ExternalID: repoExternalID},
				ExternalRoleAliases: []string{roleName},
			}
			if identity.IsContainer {
				access.ExternalGroupAliases = DescriptorAliases(identity.Descriptor)
			} else {
				access.ExternalUserAliases = []string{email}
			}
			configAccess = append(configAccess, access)
		}
	}

	return configAccess, roles
}

func emailFromIdentity(identity ResolvedIdentity) string {
	if mail, ok := identity.Properties["Mail"]; ok && mail.Value != "" {
		return mail.Value
	}
	return identity.ProviderDisplayName
}
