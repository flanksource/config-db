package aws

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	v1 "github.com/flanksource/config-db/api/v1"
	dutymodels "github.com/flanksource/duty/models"
	"github.com/lib/pq"
)

// trustResolution is what resolveTrustPolicy returns for a single IAM role.
// All UUIDs are left zero — SaveResults resolves principals from Aliases.
//
// Access is modelled as: "principal P has role R (the IAM role) on
// account A." So ConfigExternalID points to the AWS::::Account config
// item, and ExternalRoleAliases carry the IAM role ARN. The role itself
// is emitted as an ExternalRole with Name=roleName so UI/reviewers see
// the role's friendly name, not an ARN.
type trustResolution struct {
	Users    []dutymodels.ExternalUser
	Groups   []dutymodels.ExternalGroup
	Roles    []dutymodels.ExternalRole
	Accesses []v1.ExternalConfigAccess
	Warnings []string

	// account is the target account the access is scoped to; set by
	// resolveTrustPolicy and read by appendAccess.
	account string
	roleARN string
}

// resolveTrustPolicy classifies each Principal entry in a role's trust policy
// and emits the corresponding external entities and config-access linkages.
//
// Rules:
//   - Effect=Allow only. Effect=Deny statements are skipped (no Deny
//     simulation; a Deny without matching Allow means no access anyway).
//   - NotPrincipal statements are skipped.
//   - Principal="*" is skipped unless the statement has a Condition (the
//     condition usually binds it to an OIDC aud/sub or an external ID);
//     unconditional "*" is surfaced as a Warning and skipped.
//   - Conditions are recorded as a one-line string in ConfigAccess.Source so
//     reviewers can see the gate, but do not alter emission.
func resolveTrustPolicy(roleARN, accountID string, doc trustPolicyDoc) trustResolution {
	res := trustResolution{account: accountID, roleARN: roleARN}
	seenUser := map[string]bool{}
	seenGroup := map[string]bool{}

	// The IAM role itself is the permission (ExternalRole). The access row
	// attaches principal → role → account. Role is keyed by its ARN so the
	// same role referenced across multiple trust policies dedupes naturally.
	res.Roles = append(res.Roles, dutymodels.ExternalRole{
		Aliases:  pq.StringArray{roleARN},
		Name:     lastSegment(roleARN),
		Tenant:   accountID,
		RoleType: "IAMRole",
	})

	for i, stmt := range doc.Statement {
		if !strings.EqualFold(stmt.Effect, "Allow") {
			continue
		}
		if !stmt.NotPrincipal.isEmpty() {
			continue
		}
		if stmt.Principal.isEmpty() {
			continue
		}
		source := buildSource(roleARN, stmt, i)
		hasCondition := len(stmt.Condition) > 0 && string(stmt.Condition) != "null"

		if stmt.Principal.Wildcard {
			if !hasCondition {
				res.Warnings = append(res.Warnings,
					fmt.Sprintf("role %s statement %d has unconditional Principal: '*' (skipped)", roleARN, i))
				continue
			}
			res.addFederated(roleARN, "*", source, seenUser)
			continue
		}

		for _, arn := range stmt.Principal.AWS {
			res.addAWSPrincipal(roleARN, accountID, arn, source, seenUser, seenGroup)
		}
		for _, svc := range stmt.Principal.Service {
			res.addService(roleARN, svc, source, seenUser)
		}
		for _, fed := range stmt.Principal.Federated {
			res.addFederated(roleARN, fed, source, seenUser)
		}
		for _, cu := range stmt.Principal.CanonicalUser {
			res.addCanonicalUser(roleARN, cu, source, seenUser)
		}
	}

	return res
}

// addAWSPrincipal handles "AWS" principals: user/role ARNs, account root,
// and bare account IDs (normalized to arn:aws:iam::N:root).
func (r *trustResolution) addAWSPrincipal(roleARN, accountID, principal, source string,
	seenUser, seenGroup map[string]bool) {
	alias, account, kind := classifyAWSPrincipal(principal, accountID)
	switch kind {
	case "user":
		if seenUser[alias] {
			r.appendAccess(roleARN, &v1.ExternalConfigAccess{
				ExternalUserAliases: []string{alias},
				Source:              strPtr(source),
			})
			return
		}
		seenUser[alias] = true
		r.Users = append(r.Users, dutymodels.ExternalUser{
			Aliases:  pq.StringArray{alias},
			Name:     lastSegment(alias),
			Tenant:   account,
			UserType: "IAMUser",
		})
		r.appendAccess(roleARN, &v1.ExternalConfigAccess{
			ExternalUserAliases: []string{alias},
			Source:              strPtr(source),
		})
	case "role":
		// A role-chained principal (role A trusts role B) is a *subject*,
		// not a permission. Emit as ExternalUser with UserType=IAMRole so
		// the principal slot stays free for the synthetic AssumeRole.
		if !seenUser[alias] {
			seenUser[alias] = true
			r.Users = append(r.Users, dutymodels.ExternalUser{
				Aliases:  pq.StringArray{alias},
				Name:     lastSegment(alias),
				Tenant:   account,
				UserType: "IAMRole",
			})
		}
		r.appendAccess(roleARN, &v1.ExternalConfigAccess{
			ExternalUserAliases: []string{alias},
			Source:              strPtr(source),
		})
	case "account":
		if !seenGroup[alias] {
			seenGroup[alias] = true
			r.Groups = append(r.Groups, dutymodels.ExternalGroup{
				Aliases:   pq.StringArray{alias},
				Name:      lastSegment(alias),
				Tenant:    account,
				GroupType: "AWSAccount",
			})
		}
		r.appendAccess(roleARN, &v1.ExternalConfigAccess{
			ExternalGroupAliases: []string{alias},
			Source:               strPtr(source),
		})
	}
}

func (r *trustResolution) addService(roleARN, service, source string, seenUser map[string]bool) {
	alias := "aws-service:" + service
	if !seenUser[alias] {
		seenUser[alias] = true
		r.Users = append(r.Users, dutymodels.ExternalUser{
			Aliases:  pq.StringArray{alias},
			Name:     service,
			UserType: "AWSService",
		})
	}
	r.appendAccess(roleARN, &v1.ExternalConfigAccess{
		ExternalUserAliases: []string{alias},
		Source:              strPtr(source),
	})
}

func (r *trustResolution) addFederated(roleARN, federated, source string, seenUser map[string]bool) {
	alias := federated
	name, userType := classifyFederated(federated)
	if !seenUser[alias] {
		seenUser[alias] = true
		r.Users = append(r.Users, dutymodels.ExternalUser{
			Aliases:  pq.StringArray{alias},
			Name:     name,
			Tenant:   accountFromARN(federated),
			UserType: userType,
		})
	}
	r.appendAccess(roleARN, &v1.ExternalConfigAccess{
		ExternalUserAliases: []string{alias},
		Source:              strPtr(source),
	})
}

// classifyFederated returns (displayName, userType) for a Principal.Federated
// entry. OIDC and SAML providers are distinguished by their ARN resource
// prefix; non-ARN values (service identifiers like "accounts.google.com" or
// "cognito-identity.amazonaws.com") fall through as the generic "Federated"
// type so new provider forms surface rather than silently miscategorise.
func classifyFederated(federated string) (string, string) {
	if strings.HasPrefix(federated, "arn:") {
		parts := strings.SplitN(federated, ":", 6)
		if len(parts) == 6 {
			resource := parts[5]
			switch {
			case strings.HasPrefix(resource, "oidc-provider/"):
				return strings.TrimPrefix(resource, "oidc-provider/"), "OIDC"
			case strings.HasPrefix(resource, "saml-provider/"):
				return strings.TrimPrefix(resource, "saml-provider/"), "SAML"
			}
		}
		return lastSegment(federated), "Federated"
	}
	return federated, "Federated"
}

func (r *trustResolution) addCanonicalUser(roleARN, id, source string, seenUser map[string]bool) {
	alias := "aws-canonical-user:" + id
	if !seenUser[alias] {
		seenUser[alias] = true
		r.Users = append(r.Users, dutymodels.ExternalUser{
			Aliases:  pq.StringArray{alias},
			Name:     id,
			UserType: "CanonicalUser",
		})
	}
	r.appendAccess(roleARN, &v1.ExternalConfigAccess{
		ExternalUserAliases: []string{alias},
		Source:              strPtr(source),
	})
}

func (r *trustResolution) appendAccess(_ string, a *v1.ExternalConfigAccess) {
	a.ConfigExternalID = v1.ExternalID{ConfigType: v1.AWSAccount, ExternalID: r.account}
	a.ExternalRoleAliases = []string{r.roleARN}
	r.Accesses = append(r.Accesses, *a)
}

// classifyAWSPrincipal returns (canonicalAlias, accountID, kind).
// kind is one of: "user", "role", "account".
// Bare account IDs are normalized to arn:aws:iam::N:root.
// Unknown shapes (assumed-role session ARNs, etc.) are treated as "role"
// with the session ARN as the alias — imprecise but auditable.
func classifyAWSPrincipal(principal, defaultAccount string) (alias, account, kind string) {
	if isAllDigits(principal) {
		arn := fmt.Sprintf("arn:aws:iam::%s:root", principal)
		return arn, principal, "account"
	}
	if !strings.HasPrefix(principal, "arn:") {
		return principal, defaultAccount, "role"
	}
	parts := strings.SplitN(principal, ":", 6)
	if len(parts) < 6 {
		return principal, accountFromARN(principal), "role"
	}
	account = parts[4]
	resource := parts[5]
	switch {
	case resource == "root":
		return principal, account, "account"
	case strings.HasPrefix(resource, "user/"):
		return principal, account, "user"
	case strings.HasPrefix(resource, "role/"):
		return principal, account, "role"
	case strings.HasPrefix(resource, "assumed-role/"):
		return principal, account, "role"
	default:
		return principal, account, "role"
	}
}

func buildSource(roleARN string, stmt trustStatement, index int) string {
	parts := []string{fmt.Sprintf("trust:%s#%d", roleARN, index)}
	if stmt.Sid != "" {
		parts = append(parts, "sid="+stmt.Sid)
	}
	if len(stmt.Condition) > 0 && string(stmt.Condition) != "null" {
		parts = append(parts, "condition="+summarizeCondition(stmt.Condition))
	}
	return strings.Join(parts, "; ")
}

func summarizeCondition(raw json.RawMessage) string {
	var cond map[string]map[string]any
	if err := json.Unmarshal(raw, &cond); err != nil {
		return "unparseable"
	}
	var keys []string
	for op, fields := range cond {
		for field := range fields {
			keys = append(keys, op+":"+field)
		}
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func lastSegment(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func accountFromARN(arn string) string {
	if !strings.HasPrefix(arn, "arn:") {
		return ""
	}
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 5 {
		return ""
	}
	return parts[4]
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

