package devops

import (
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/duty/types"
)

// envNameToStage maps an ADO environment display name to a canonical
// EnvironmentStage using a case-insensitive prefix match. Returns "" for
// unknown names, which the JSON marshaler will then omit.
func envNameToStage(name string) types.EnvironmentStage {
	n := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.HasPrefix(n, "prod"):
		return types.EnvironmentStageProduction
	case strings.HasPrefix(n, "stag"):
		return types.EnvironmentStageStaging
	case strings.HasPrefix(n, "uat"):
		return types.EnvironmentStageUAT
	case strings.HasPrefix(n, "qa"):
		return types.EnvironmentStageQA
	case strings.HasPrefix(n, "dev"):
		return types.EnvironmentStageDevelopment
	}
	return ""
}

// approvalTypeToStage converts an ADO approvalType value ("preDeploy",
// "postDeploy") into a canonical ApprovalStage.
func approvalTypeToStage(adoType string) types.ApprovalStage {
	switch adoType {
	case "preDeploy":
		return types.ApprovalStagePreDeployment
	case "postDeploy":
		return types.ApprovalStagePostDeployment
	}
	return ""
}

// approvalStatusToTyped maps an ADO approval status string to the canonical
// ApprovalStatus. Skipped maps to Expired (closest semantic match: the slot
// was discarded without a decision).
func approvalStatusToTyped(status string) types.ApprovalStatus {
	switch status {
	case "approved":
		return types.ApprovalStatusApproved
	case "rejected":
		return types.ApprovalStatusRejected
	case "pending":
		return types.ApprovalStatusPending
	case "skipped":
		return types.ApprovalStatusExpired
	}
	return ""
}

// identityRefToTyped converts an ADO IdentityRef into a types.Identity. Returns
// nil for a missing or empty ref so the marshaler omits the field entirely.
// The optional comment (e.g. approval reason) is carried through.
func identityRefToTyped(ref *IdentityRef, comment string) *types.Identity {
	if ref == nil || (ref.UniqueName == "" && ref.ID == "" && ref.DisplayName == "") {
		return nil
	}
	identityType := types.IdentityTypeUser
	if ref.IsContainer {
		identityType = types.IdentityTypeGroup
	}
	id := ref.UniqueName
	if id == "" {
		id = ref.ID
	}
	return &types.Identity{
		ID:      id,
		Type:    identityType,
		Name:    ref.DisplayName,
		Comment: comment,
	}
}

// approvalsToTyped maps a slice of ReleaseApproval into []types.Approval,
// matching the filter semantics of approvalSummary: automated and skipped
// entries are dropped.
func approvalsToTyped(list []ReleaseApproval, stage types.ApprovalStage, ts time.Time) []types.Approval {
	var out []types.Approval
	for _, a := range list {
		if a.IsAutomated || a.Status == "skipped" {
			continue
		}
		actor := a.ApprovedBy
		if actor == nil {
			actor = a.Approver
		}
		out = append(out, types.Approval{
			Event: types.Event{
				Timestamp: ts.UTC().Format(time.RFC3339),
			},
			Approver: identityRefToTyped(actor, a.Comments),
			Stage:    stage,
			Status:   approvalStatusToTyped(a.Status),
		})
	}
	return out
}

// primaryArtifactSummary builds a human-readable one-liner for the primary
// artifact (or the first artifact if none is flagged primary). Empty string
// when there are no artifacts.
func primaryArtifactSummary(artifacts []ReleaseArtifact) string {
	art := pickPrimaryArtifact(artifacts)
	if art == nil {
		return ""
	}
	definition := art.DefinitionReference["definition"].Name
	version := art.DefinitionReference["version"].Name
	branch := art.DefinitionReference["branch"].Name

	parts := definition
	if version != "" {
		parts = fmt.Sprintf("%s@%s", parts, version)
	}
	if branch != "" {
		parts = fmt.Sprintf("%s (%s)", parts, branch)
	}
	return parts
}

// primaryArtifactSource populates a types.Source.Git from the primary artifact
// when a branch or version is known. Returns the zero value otherwise so the
// marshaler omits the field.
func primaryArtifactSource(artifacts []ReleaseArtifact) types.Source {
	art := pickPrimaryArtifact(artifacts)
	if art == nil {
		return types.Source{}
	}
	branch := art.DefinitionReference["branch"].Name
	version := art.DefinitionReference["version"].Name
	if branch == "" && version == "" {
		return types.Source{}
	}
	return types.Source{
		Git: &types.GitSource{
			Branch:  branch,
			Version: version,
		},
	}
}

func pickPrimaryArtifact(artifacts []ReleaseArtifact) *ReleaseArtifact {
	if len(artifacts) == 0 {
		return nil
	}
	for i := range artifacts {
		if artifacts[i].IsPrimary {
			return &artifacts[i]
		}
	}
	return &artifacts[0]
}

// buildReleaseEventProperties captures ADO-specific reason/description fields
// as string properties on the Event so they survive round-tripping without
// being promoted to first-class fields on Promotion.
func buildReleaseEventProperties(r Release, env ReleaseEnvironment) map[string]string {
	out := map[string]string{}
	if r.Reason != "" {
		out["reason"] = r.Reason
	}
	if r.Description != "" {
		out["description"] = r.Description
	}
	if env.TriggerReason != "" {
		out["triggerReason"] = env.TriggerReason
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// buildReleaseRaw preserves the full ADO-shaped payload under details.raw so
// no provider-specific data is lost after the switch to the canonical
// Promotion envelope. Consumers that need deploySteps, variables, or the full
// artifact list can still reach them.
func buildReleaseRaw(r Release, env ReleaseEnvironment, createdBy *string) map[string]any {
	raw := map[string]any{
		"releaseId":   r.ID,
		"releaseName": r.Name,
		"environment": env.Name,
		"status":      env.Status,
	}
	if createdBy != nil {
		raw["createdBy"] = *createdBy
	}
	if webURL := r.Links["web"].Href; webURL != "" {
		raw["url"] = webURL
	}
	if pre := approvalSummary(env.PreDeployApprovals); len(pre) > 0 {
		raw["preDeployApprovals"] = pre
	}
	if post := approvalSummary(env.PostDeployApprovals); len(post) > 0 {
		raw["postDeployApprovals"] = post
	}
	if len(env.DeploySteps) > 0 {
		raw["deploySteps"] = env.DeploySteps
	}
	if r.Reason != "" {
		raw["reason"] = r.Reason
	}
	if r.Description != "" {
		raw["description"] = r.Description
	}
	if env.TriggerReason != "" {
		raw["triggerReason"] = env.TriggerReason
	}
	if vars := flattenVariables(r.Variables); len(vars) > 0 {
		raw["variables"] = vars
	}
	if envVars := flattenVariables(env.Variables); len(envVars) > 0 {
		raw["environmentVariables"] = envVars
	}
	if len(r.Artifacts) > 0 {
		raw["artifacts"] = summarizeArtifacts(r.Artifacts)
	}
	return raw
}
