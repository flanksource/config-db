package devops

import (
	"fmt"

	"github.com/flanksource/commons/hash"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/lib/pq"
)

// emitReleaseDefinitionRoles parses approvers from the release-definition JSON,
// registers ExternalUsers / ExternalGroups on ctx, and returns (roles, accesses)
// to attach to the release-definition ScrapeResult.
//
// Role names follow: if an environment has approvers on only one side (pre or
// post) → {EnvName}Approver; if both sides have approvers → {EnvName}PreApprover
// and {EnvName}PostApprover.
func emitReleaseDefinitionRoles(
	ctx api.ScrapeContext,
	config v1.AzureDevops,
	def ReleaseDefinition,
	defJSON map[string]any,
	configExternalID v1.ExternalID,
) ([]dutyModels.ExternalRole, []v1.ExternalConfigAccess) {
	if defJSON == nil {
		return nil, nil
	}

	envsRaw, ok := defJSON["environments"].([]any)
	if !ok {
		return nil, nil
	}

	var (
		roles    []dutyModels.ExternalRole
		accesses []v1.ExternalConfigAccess
		seenRole = map[string]struct{}{}
		seenBind = map[string]struct{}{}
	)

	for _, envRaw := range envsRaw {
		env, ok := envRaw.(map[string]any)
		if !ok {
			continue
		}
		envName, _ := env["name"].(string)
		if envName == "" {
			continue
		}

		preApprovers := definitionApprovers(env, "preDeployApprovals")
		postApprovers := definitionApprovers(env, "postDeployApprovals")

		preRole, postRole := pickRoleNames(envName, len(preApprovers) > 0, len(postApprovers) > 0)

		if preRole != "" {
			r, a := materializeRole(ctx, config, def, envName, preRole, preApprovers, configExternalID, seenRole, seenBind)
			roles = append(roles, r...)
			accesses = append(accesses, a...)
		}
		if postRole != "" {
			r, a := materializeRole(ctx, config, def, envName, postRole, postApprovers, configExternalID, seenRole, seenBind)
			roles = append(roles, r...)
			accesses = append(accesses, a...)
		}
	}

	return roles, accesses
}

// materializeRole registers identities, emits the role once, and returns
// ExternalConfigAccess bindings for each approver, deduping repeats.
func materializeRole(
	ctx api.ScrapeContext,
	config v1.AzureDevops,
	def ReleaseDefinition,
	envName, roleName string,
	approvers []*IdentityRef,
	configExternalID v1.ExternalID,
	seenRole, seenBind map[string]struct{},
) ([]dutyModels.ExternalRole, []v1.ExternalConfigAccess) {
	if len(approvers) == 0 {
		return nil, nil
	}

	alias := definitionRoleAlias(config.Organization, def.ID, envName, roleName)

	var roles []dutyModels.ExternalRole
	if _, ok := seenRole[alias]; !ok {
		roleID, err := hash.DeterministicUUID(pq.StringArray{alias})
		if err == nil {
			roles = append(roles, dutyModels.ExternalRole{
				ID:       roleID,
				Name:     roleName,
				RoleType: "AzureDevOps",
				Tenant:   config.Organization,
				Aliases:  pq.StringArray{alias},
			})
			seenRole[alias] = struct{}{}
		}
	}

	var accesses []v1.ExternalConfigAccess
	for _, identity := range approvers {
		addExternalEntity(ctx, identity, config.Organization)

		bindKey := alias + "|" + identity.ID + "|" + identity.UniqueName
		if _, ok := seenBind[bindKey]; ok {
			continue
		}
		seenBind[bindKey] = struct{}{}

		access := v1.ExternalConfigAccess{
			ConfigExternalID:    configExternalID,
			ExternalRoleAliases: []string{alias},
		}
		if identity.IsContainer {
			access.ExternalGroupAliases = identityAliasList(identity)
		} else {
			access.ExternalUserAliases = identityAliasList(identity)
		}
		accesses = append(accesses, access)
	}
	return roles, accesses
}

// definitionApprovers extracts non-automated approver IdentityRefs from a
// classic release definition environment block (preDeployApprovals or
// postDeployApprovals). The block shape is {"approvals": [{"approver": {...}, "isAutomated": bool}]}.
func definitionApprovers(envMap map[string]any, key string) []*IdentityRef {
	block, ok := envMap[key].(map[string]any)
	if !ok {
		return nil
	}
	approvalsRaw, ok := block["approvals"].([]any)
	if !ok {
		return nil
	}

	var out []*IdentityRef
	for _, raw := range approvalsRaw {
		approval, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if automated, _ := approval["isAutomated"].(bool); automated {
			continue
		}
		approverRaw, ok := approval["approver"].(map[string]any)
		if !ok {
			continue
		}
		identity := identityFromMap(approverRaw)
		if identity == nil || identity.UniqueName == "" {
			continue
		}
		out = append(out, identity)
	}
	return out
}

func identityFromMap(m map[string]any) *IdentityRef {
	id, _ := m["id"].(string)
	displayName, _ := m["displayName"].(string)
	uniqueName, _ := m["uniqueName"].(string)
	descriptor, _ := m["descriptor"].(string)
	isContainer, _ := m["isContainer"].(bool)
	return &IdentityRef{
		ID:          id,
		DisplayName: displayName,
		UniqueName:  uniqueName,
		Descriptor:  descriptor,
		IsContainer: isContainer,
	}
}

func identityAliasList(identity *IdentityRef) []string {
	aliases := []string{identity.UniqueName}
	if identity.ID != "" {
		aliases = append(aliases, identity.ID)
	}
	return aliases
}

func pickRoleNames(envName string, hasPre, hasPost bool) (string, string) {
	switch {
	case hasPre && hasPost:
		return envName + "PreApprover", envName + "PostApprover"
	case hasPre:
		return envName + "Approver", ""
	case hasPost:
		return "", envName + "Approver"
	default:
		return "", ""
	}
}

func definitionRoleAlias(org string, defID int, envName, roleName string) string {
	return fmt.Sprintf("azuredevops://%s/release/%d/env/%s/%s", org, defID, envName, roleName)
}
