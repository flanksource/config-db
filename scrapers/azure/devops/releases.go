package devops

import (
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/hash"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutyModels "github.com/flanksource/duty/models"
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
	folder := strings.Trim(def.Path, `\`)
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

func (ado AzureDevopsScraper) scrapeReleases(
	ctx api.ScrapeContext,
	releaseClient *AzureDevopsReleaseClient,
	config v1.AzureDevops,
	project Project,
	cutoff time.Time,
) v1.ScrapeResults {
	definitions, err := releaseClient.GetReleaseDefinitions(ctx, project.Name)
	if err != nil {
		var errs v1.ScrapeResults
		errs.Errorf(err, "failed to get release definitions for %s", project.Name)
		return errs
	}
	ctx.Logger.V(1).Infof("[releases] project=%s definitions=%d filter=%v cutoff=%s",
		project.Name, len(definitions), config.Releases, cutoff.Format(time.RFC3339))

	var results v1.ScrapeResults
	for _, def := range definitions {
		if !collections.MatchItems(def.Name, config.Releases...) {
			ctx.Logger.V(3).Infof("[releases] skipping definition %q (no match)", def.Name)
			continue
		}
		releases, err := releaseClient.GetReleases(ctx, project.Name, def.ID)
		if err != nil {
			ctx.Logger.V(4).Infof("failed to get releases for %s/%s: %v", project.Name, def.Name, err)
			continue
		}
		ctx.Logger.V(1).Infof("[releases] definition=%q releases=%d", def.Name, len(releases))
		result := buildReleaseResult(config, project, def, releases, cutoff)
		ctx.Logger.V(1).Infof("[releases] definition=%q changes=%d", def.Name, len(result.Changes))
		results = append(results, result)
	}
	return results
}

// ensureExternalUser adds an ExternalUser for the given ADO identity to the map if not already present.
// Returns the user UUID or zero on error.
func ensureExternalUser(identity *IdentityRef, organization string, users map[string]dutyModels.ExternalUser) {
	if identity == nil || identity.UniqueName == "" {
		return
	}
	email := identity.UniqueName
	if _, exists := users[email]; exists {
		return
	}
	userID, err := hash.DeterministicUUID(pq.StringArray{email})
	if err != nil {
		return
	}
	users[email] = dutyModels.ExternalUser{
		ID:        userID,
		Name:      identity.DisplayName,
		Email:     &email,
		Aliases:   pq.StringArray{email, identity.ID},
		AccountID: organization,
		UserType:  "AzureDevOps",
	}
}

// deploymentAccessLog builds a Deployment access log entry for a release trigger.
func deploymentAccessLog(identity *IdentityRef, configExternalID v1.ExternalID, createdAt time.Time, envName string, users map[string]dutyModels.ExternalUser) *v1.ExternalConfigAccessLog {
	if identity == nil || identity.UniqueName == "" {
		return nil
	}
	user, ok := users[identity.UniqueName]
	if !ok {
		return nil
	}
	return &v1.ExternalConfigAccessLog{
		ConfigAccessLog: dutyModels.ConfigAccessLog{
			ExternalUserID: user.ID,
			CreatedAt:      createdAt,
			Properties: map[string]any{
				"role":        "Deployment",
				"environment": envName,
			},
		},
		ConfigExternalID: configExternalID,
	}
}

// approvalAccessLog builds a DeploymentApproval access log entry for a resolved approval.
// Returns nil for automated or pending approvals.
func approvalAccessLog(a ReleaseApproval, configExternalID v1.ExternalID, envName string, createdAt time.Time, users map[string]dutyModels.ExternalUser) *v1.ExternalConfigAccessLog {
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
	user, ok := users[actor.UniqueName]
	if !ok {
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
			ExternalUserID: user.ID,
			CreatedAt:      createdAt,
			Properties:     props,
		},
		ConfigExternalID: configExternalID,
	}
}

func buildReleaseResult(config v1.AzureDevops, project Project, def ReleaseDefinition, releases []Release, cutoff time.Time) v1.ScrapeResult {
	externalUsers := make(map[string]dutyModels.ExternalUser)
	var accessLogs []v1.ExternalConfigAccessLog
	var changes []v1.ChangeResult

	configExternalID := v1.ExternalID{
		ConfigType: ReleaseType,
		ExternalID: fmt.Sprintf("%s/%d", project.Name, def.ID),
	}

	for _, release := range releases {
		// The ADO list-releases API does not populate env.createdOn/modifiedOn —
		// they always return as zero. Filter by release.CreatedOn instead.
		if release.CreatedOn.Before(cutoff) {
			continue
		}

		ensureExternalUser(release.CreatedBy, config.Organization, externalUsers)

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
				details["webUrl"] = webURL
			}
			if pre := approvalSummary(env.PreDeployApprovals); len(pre) > 0 {
				details["preDeployApprovals"] = pre
			}
			if post := approvalSummary(env.PostDeployApprovals); len(post) > 0 {
				details["postDeployApprovals"] = post
			}

			changes = append(changes, v1.ChangeResult{
				ChangeType:       changeType,
				CreatedAt:        &release.CreatedOn,
				CreatedBy:        createdBy,
				ExternalID:       fmt.Sprintf("%s/%d", project.Name, def.ID),
				ConfigType:       ReleaseType,
				Source:           webURL,
				Summary:          fmt.Sprintf("%s / %s", release.Name, env.Name),
				Details:          details,
				ExternalChangeID: fmt.Sprintf("%s/release/%d/%d/%d", project.Name, def.ID, release.ID, env.ID),
			})

			if log := deploymentAccessLog(release.CreatedBy, configExternalID, release.CreatedOn, env.Name, externalUsers); log != nil {
				accessLogs = append(accessLogs, *log)
			}

			for _, a := range append(env.PreDeployApprovals, env.PostDeployApprovals...) {
				ensureExternalUser(a.ApprovedBy, config.Organization, externalUsers)
				ensureExternalUser(a.Approver, config.Organization, externalUsers)
				if log := approvalAccessLog(a, configExternalID, env.Name, release.CreatedOn, externalUsers); log != nil {
					accessLogs = append(accessLogs, *log)
				}
			}
		}
	}

	users := make([]dutyModels.ExternalUser, 0, len(externalUsers))
	for _, u := range externalUsers {
		users = append(users, u)
	}

	return v1.ScrapeResult{
		ConfigClass: "Deployment",
		Config: map[string]any{
			"id":           def.ID,
			"name":         def.Name,
			"path":         def.Path,
			"project":      project.Name,
			"organization": config.Organization,
		},
		Type:             ReleaseType,
		ID:               fmt.Sprintf("%s/%d", project.Name, def.ID),
		Name:             releaseDisplayName(def),
		Changes:          changes,
		Aliases:          []string{fmt.Sprintf("%s/release/%d", project.Name, def.ID)},
		ExternalUsers:    users,
		ConfigAccessLogs: accessLogs,
	}
}
