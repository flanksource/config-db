package devops

import (
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/scrapers/azure"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// hasReleasePendingApproval returns true if any non-automated pre-deploy approval
// is still pending.
func hasReleasePendingApproval(approvals []ReleaseApproval) bool {
	for _, a := range approvals {
		if !a.IsAutomated && a.Status == "pending" {
			return true
		}
	}
	return false
}

// approvalSummary converts non-automated release approvals into a compact slice
// suitable for embedding in change details.  Automated approvals are skipped.
func approvalSummary(approvals []ReleaseApproval) []map[string]any {
	var out []map[string]any
	for _, a := range approvals {
		if a.IsAutomated || a.Status == "skipped" {
			continue
		}
		entry := map[string]any{"status": a.Status}
		if a.Approver != nil && a.Approver.UniqueName != "" {
			entry["approver"] = a.Approver.UniqueName
		}
		if a.ApprovedBy != nil && a.ApprovedBy.UniqueName != "" {
			entry["approvedBy"] = a.ApprovedBy.UniqueName
		}
		if a.Comments != "" {
			entry["comments"] = a.Comments
		}
		out = append(out, entry)
	}
	return out
}

// releaseDisplayName builds a display name that includes the folder path.
// ADO paths use backslash separators and always start with "\".
// Root path "\" is omitted; sub-folders are included as a prefix.
// e.g. path="\Production" name="Deploy" → "Production / Deploy"
//
//	path="\" name="Deploy" → "Deploy"
func releaseDisplayName(def ReleaseDefinition) string {
	folder := strings.ReplaceAll(strings.Trim(def.Path, `\`), `\`, "/")
	if folder == "" {
		return def.Name
	}
	return folder + " / " + def.Name
}

// releaseEnvStatusToChangeType maps EnvironmentStatus values (from the ADO releases API)
// to ChangeType constants. "failed" and "notDeployed" are DeploymentStatus values and
// do not appear as top-level env.Status.
var releaseEnvStatusToChangeType = map[string]string{
	"succeeded":          ChangeTypeSucceeded,
	"partiallySucceeded": ChangeTypeFailed,
	"canceled":           ChangeTypeCancelled,
	"rejected":           ChangeTypeFailed,
	"inProgress":         ChangeTypeInProgress,
	"queued":             ChangeTypeInProgress,
	"scheduled":          ChangeTypeInProgress,
}

func ReleaseExternalID(organization, project string, definitionID int) string {
	return fmt.Sprintf("azuredevops://%s/%s/release/%d", organization, project, definitionID)
}

func (ado AzureDevopsScraper) scrapeReleases(
	ctx api.ScrapeContext,
	client *AzureDevopsClient,
	releaseClient *AzureDevopsReleaseClient,
	config v1.AzureDevops,
	project Project,
	cutoff time.Time,
) v1.ScrapeResults {
	var results v1.ScrapeResults

	definitions, err := releaseClient.GetReleaseDefinitions(ctx, project.Name)
	if err != nil {
		return results.Errorf(err, "failed to get release definitions for %s", project.Name)
	}
	ctx.Logger.V(2).Infof("scraping releases for project=%s definitions=%d filter=%v cutoff=%s",
		project.Name, len(definitions), config.Releases, cutoff.Format(time.RFC3339))

	for _, def := range definitions {
		if !collections.MatchItems(def.Name, config.Releases...) {
			ctx.Logger.V(3).Infof("skipping release %q (no match)", def.Name)
			continue
		}
		defJSON, err := releaseClient.GetReleaseDefinition(ctx, project.Name, def.ID)
		if err != nil {
			return results.Errorf(err, "failed to get release definition %d", def.ID)
		}
		releases, err := releaseClient.GetReleases(ctx, project.Name, def.ID)
		if err != nil {
			return results.Errorf(err, "failed to get releases for definition %d in project %s", def.ID, project.Name)
		}
		result := buildReleaseResult(ctx, config, project, def, defJSON, releases, cutoff)

		if config.Permissions != nil && config.Permissions.Enabled {
			releaseKey := fmt.Sprintf("release/%s/%s/%d", config.Organization, project.Name, def.ID)
			if shouldFetchPermissions(releaseKey, parsePermissionsInterval(config.Permissions.RateLimit)) {
				result.ConfigAccess, result.ExternalRoles = ado.fetchReleasePermissions(ctx, client, config, project, def.ID, result.ID)
				markPermissionsFetched(releaseKey)
			}
		}

		results = append(results, result)
	}
	return results
}

func (ado AzureDevopsScraper) fetchReleasePermissions(
	ctx api.ScrapeContext,
	client *AzureDevopsClient,
	config v1.AzureDevops,
	project Project,
	definitionID int,
	releaseExternalID string,
) ([]v1.ExternalConfigAccess, []dutyModels.ExternalRole) {
	acls, err := client.GetReleasePermissions(ctx, project.ID, definitionID)
	if err != nil {
		ctx.Logger.V(4).Infof("failed to get release permissions for %s/%d: %v", project.Name, definitionID, err)
		return nil, nil
	}

	perms := ParseReleasePermissions(acls)
	if len(perms) == 0 {
		return nil, nil
	}

	var descriptors []string
	for _, p := range perms {
		descriptors = append(descriptors, p.IdentityDescriptor)
	}

	identities, err := client.GetIdentitiesByDescriptor(ctx, descriptors)
	if err != nil {
		ctx.Logger.V(4).Infof("failed to resolve identities for release %s/%d: %v", project.Name, definitionID, err)
		return nil, nil
	}

	identityMap := BuildIdentityMap(identities)

	roleIDs := make(map[string]uuid.UUID)
	var roles []dutyModels.ExternalRole
	var configAccess []v1.ExternalConfigAccess

	for _, perm := range perms {
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
			aliases := append(DescriptorAliases(identity.Descriptor), identity.SubjectDescriptor)
			aliases = append(aliases, DescriptorAliases(identity.SubjectDescriptor)...)
			// No ID — the SQL merge resolves this group against the AAD scraper's
			// authoritative record by alias overlap. AAD takes precedence.
			ctx.AddGroup(dutyModels.ExternalGroup{
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

		resolvedRoles := ResolveRoles("Release", perm.Permissions, config.Permissions.Roles)
		for _, roleName := range resolvedRoles {
			if _, exists := roleIDs[roleName]; !exists {
				roleID := azure.RoleID(ctx.ScraperID(), roleName)
				roleIDs[roleName] = roleID
				roles = append(roles, dutyModels.ExternalRole{
					ID:       roleID,
					Name:     roleName,
					RoleType: "AzureDevOps",
					Tenant:   config.Organization,
				})
			}

			roleID := roleIDs[roleName]
			access := v1.ExternalConfigAccess{
				ConfigExternalID: v1.ExternalID{ConfigType: ReleaseType, ExternalID: releaseExternalID},
				ExternalRoleID:   &roleID,
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

func addExternalEntity(ctx api.ScrapeContext, identity *IdentityRef, organization string) {
	if identity == nil || identity.UniqueName == "" {
		return
	}
	if identity.IsContainer {
		ctx.AddGroup(dutyModels.ExternalGroup{
			Name:      identity.DisplayName,
			Aliases:   pq.StringArray{identity.UniqueName, identity.ID},
			Tenant:    organization,
			GroupType: "AzureDevOps",
		})
		return
	}
	email := identity.UniqueName
	ctx.AddUser(dutyModels.ExternalUser{
		Name:     identity.DisplayName,
		Email:    &email,
		Aliases:  pq.StringArray{email, identity.ID},
		Tenant:   organization,
		UserType: "AzureDevOps",
	})
}

func findUserByAlias(users []dutyModels.ExternalUser, alias string) *dutyModels.ExternalUser {
	for i, u := range users {
		for _, a := range u.Aliases {
			if a == alias {
				return &users[i]
			}
		}
	}
	return nil
}

func deploymentAccessLog(identity *IdentityRef, configExternalID v1.ExternalID, createdAt time.Time, envName string, users []dutyModels.ExternalUser) *v1.ExternalConfigAccessLog {
	if identity == nil || identity.UniqueName == "" {
		return nil
	}
	user := findUserByAlias(users, identity.UniqueName)
	if user == nil {
		return nil
	}
	return &v1.ExternalConfigAccessLog{
		ConfigAccessLog: dutyModels.ConfigAccessLog{
			CreatedAt: createdAt,
			Properties: map[string]any{
				"role":        "Deployment",
				"environment": envName,
			},
		},
		ExternalUserAliases: user.Aliases,
		ConfigExternalID:    configExternalID,
	}
}

func approvalAccessLog(a ReleaseApproval, configExternalID v1.ExternalID, envName string, createdAt time.Time, users []dutyModels.ExternalUser) *v1.ExternalConfigAccessLog {
	if a.IsAutomated || a.Status == "pending" || a.Status == "skipped" {
		return nil
	}
	actor := a.ApprovedBy
	if actor == nil {
		actor = a.Approver
	}
	if actor == nil || actor.UniqueName == "" {
		return nil
	}
	user := findUserByAlias(users, actor.UniqueName)
	if user == nil {
		return nil
	}
	props := map[string]any{
		"role":        "DeploymentApproval",
		"status":      a.Status,
		"environment": envName,
	}
	if a.Comments != "" {
		props["comments"] = a.Comments
	}
	return &v1.ExternalConfigAccessLog{
		ConfigAccessLog: dutyModels.ConfigAccessLog{
			CreatedAt:  createdAt,
			Properties: props,
		},
		ExternalUserAliases: user.Aliases,
		ConfigExternalID:    configExternalID,
	}
}

func releaseApprovalChanges(approvals []ReleaseApproval, release Release, envName, externalID, source, baseExternalChangeID string) []v1.ChangeResult {
	var out []v1.ChangeResult
	for _, a := range approvals {
		if a.IsAutomated || a.Status == "skipped" || a.Status == "pending" {
			continue
		}
		var changeType string
		switch a.Status {
		case "approved":
			changeType = ChangeTypeApproved
		case "rejected":
			changeType = ChangeTypeRejected
		default:
			continue
		}

		approver := a.ApprovedBy
		if approver == nil {
			approver = a.Approver
		}
		var createdBy *string
		approverName := ""
		if approver != nil && approver.UniqueName != "" {
			createdBy = &approver.UniqueName
			approverName = approver.UniqueName
		}

		severity := "info"
		if changeType == ChangeTypeRejected {
			severity = "high"
		}

		summary := fmt.Sprintf("%s / %s - %s by %s", release.Name, envName, changeType, approverName)
		if a.Comments != "" {
			summary += ": " + a.Comments
		}

		createdAt := release.CreatedOn
		out = append(out, v1.ChangeResult{
			ChangeType:       changeType,
			CreatedAt:        &createdAt,
			CreatedBy:        createdBy,
			Severity:         severity,
			ExternalID:       externalID,
			ConfigType:       ReleaseType,
			Source:           source,
			Summary:          summary,
			ExternalChangeID: fmt.Sprintf("%s/approval/%d", baseExternalChangeID, a.ID),
		})
	}
	return out
}

func buildReleaseResult(ctx api.ScrapeContext, config v1.AzureDevops, project Project, def ReleaseDefinition, defJSON map[string]any, releases []Release, cutoff time.Time) v1.ScrapeResult {
	var result v1.ScrapeResult

	releaseID := ReleaseExternalID(config.Organization, project.Name, def.ID)
	configExternalID := v1.ExternalID{
		ConfigType: ReleaseType,
		ExternalID: releaseID,
	}

	for _, release := range releases {
		if release.CreatedOn.Before(cutoff) {
			continue
		}

		if config.Permissions != nil && config.Permissions.Enabled {
			addExternalEntity(ctx, release.CreatedBy, config.Organization)
		}

		for _, env := range release.Environments {
			changeType, ok := releaseEnvStatusToChangeType[env.Status]
			if !ok {
				continue
			}
			if changeType == ChangeTypeInProgress && hasReleasePendingApproval(env.PreDeployApprovals) {
				continue
			}

			var createdBy *string
			if release.CreatedBy != nil && release.CreatedBy.UniqueName != "" {
				createdBy = &release.CreatedBy.UniqueName
			}

			details := map[string]any{
				"releaseId":   release.ID,
				"releaseName": release.Name,
				"environment": env.Name,
				"status":      env.Status,
			}
			if createdBy != nil {
				details["createdBy"] = *createdBy
			}
			webURL := release.Links["web"].Href
			if webURL != "" {
				details["url"] = webURL
			}
			if pre := approvalSummary(env.PreDeployApprovals); len(pre) > 0 {
				details["preDeployApprovals"] = pre
			}
			if post := approvalSummary(env.PostDeployApprovals); len(post) > 0 {
				details["postDeployApprovals"] = post
			}

			if len(env.DeploySteps) > 0 {
				details["deploySteps"] = env.DeploySteps
			}
			if release.Reason != "" {
				details["reason"] = release.Reason
			}
			if release.Description != "" {
				details["description"] = release.Description
			}
			if env.TriggerReason != "" {
				details["triggerReason"] = env.TriggerReason
			}
			if vars := flattenVariables(release.Variables); len(vars) > 0 {
				details["variables"] = vars
			}
			if envVars := flattenVariables(env.Variables); len(envVars) > 0 {
				details["environmentVariables"] = envVars
			}
			if len(release.Artifacts) > 0 {
				details["artifacts"] = summarizeArtifacts(release.Artifacts)
			}

			createdAt := release.CreatedOn
			result.Changes = append(result.Changes, v1.ChangeResult{
				ChangeType:       env.Name,
				CreatedAt:        &createdAt,
				CreatedBy:        createdBy,
				ExternalID:       releaseID,
				ConfigType:       ReleaseType,
				Source:           "AzureDevops/release/" + configExternalID.ExternalID,
				Summary:          fmt.Sprintf("%s / %s", release.Name, env.Name),
				Details:          details,
				ExternalChangeID: fmt.Sprintf("%s/%s/release/%d/%d/%d", config.Organization, project.Name, def.ID, release.ID, env.ID),
			})

			result.Changes = append(result.Changes, releaseApprovalChanges(
				append(env.PreDeployApprovals, env.PostDeployApprovals...),
				release, env.Name, configExternalID.ExternalID,
				"AzureDevops/release/"+configExternalID.ExternalID,
				fmt.Sprintf("%s/%s/release/%d/%d/%d", config.Organization, project.Name, def.ID, release.ID, env.ID),
			)...)

			if config.Permissions != nil && config.Permissions.Enabled {
				if log := deploymentAccessLog(release.CreatedBy, configExternalID, release.CreatedOn, env.Name, ctx.Users()); log != nil {
					result.ConfigAccessLogs = append(result.ConfigAccessLogs, *log)
				}

				for _, a := range append(env.PreDeployApprovals, env.PostDeployApprovals...) {
					addExternalEntity(ctx, a.ApprovedBy, config.Organization)
					addExternalEntity(ctx, a.Approver, config.Organization)
					if log := approvalAccessLog(a, configExternalID, env.Name, release.CreatedOn, ctx.Users()); log != nil {
						result.ConfigAccessLogs = append(result.ConfigAccessLogs, *log)
					}
				}
			}
		}
	}

	result.BaseScraper = config.BaseScraper
	result.ConfigClass = "Deployment"
	if defJSON != nil {
		result.Config = defJSON
	} else {
		result.Config = map[string]any{
			"id":           def.ID,
			"name":         def.Name,
			"path":         def.Path,
			"project":      project.Name,
			"organization": config.Organization,
		}
	}
	result.Type = ReleaseType
	result.ID = releaseID
	result.Name = releaseDisplayName(def)
	result.Aliases = nil
	return result
}

func flattenVariables(vars map[string]ConfigurationVariable) map[string]string {
	if len(vars) == 0 {
		return nil
	}
	out := make(map[string]string, len(vars))
	for k, v := range vars {
		if !v.IsSecret {
			out[k] = v.Value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func summarizeArtifacts(artifacts []ReleaseArtifact) []map[string]any {
	var out []map[string]any
	for _, a := range artifacts {
		entry := map[string]any{
			"type":  a.Type,
			"alias": a.Alias,
		}
		if ref, ok := a.DefinitionReference["definition"]; ok && ref.Name != "" {
			entry["definition"] = ref.Name
		}
		if ref, ok := a.DefinitionReference["version"]; ok && ref.Name != "" {
			entry["version"] = ref.Name
		}
		if ref, ok := a.DefinitionReference["branch"]; ok && ref.Name != "" {
			entry["branch"] = ref.Name
		}
		if a.IsPrimary {
			entry["isPrimary"] = true
		}
		out = append(out, entry)
	}
	return out
}
