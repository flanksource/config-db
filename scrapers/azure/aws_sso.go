package azure

import (
	"strings"
)

// parseAWSSSOAppRoleValue parses the `value` field of an Azure AppRole in the
// Microsoft AWS-Single-Account-Access gallery app pattern:
//
//	arn:aws:iam::<account>:role/<role>,arn:aws:iam::<account>:saml-provider/<provider>
//
// The two ARNs may appear in either order. On any parse failure it returns
// ok=false so arbitrary `value` strings used by non-AWS Enterprise Apps are
// silently ignored rather than misinterpreted.
func parseAWSSSOAppRoleValue(value string) (roleARN, samlARN string, ok bool) {
	parts := strings.Split(strings.TrimSpace(value), ",")
	if len(parts) != 2 {
		return "", "", false
	}

	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}

	roleAcct, isRole0 := awsIAMResourceAccount(parts[0], "role/")
	samlAcct, isSAML1 := awsIAMResourceAccount(parts[1], "saml-provider/")
	if isRole0 && isSAML1 && roleAcct == samlAcct {
		return parts[0], parts[1], true
	}

	roleAcct, isRole1 := awsIAMResourceAccount(parts[1], "role/")
	samlAcct, isSAML0 := awsIAMResourceAccount(parts[0], "saml-provider/")
	if isRole1 && isSAML0 && roleAcct == samlAcct {
		return parts[1], parts[0], true
	}

	return "", "", false
}

// awsIAMResourceAccount validates that s is an IAM ARN whose resource starts
// with the given prefix and returns the account-id segment. Returns ok=false
// if the ARN doesn't match.
func awsIAMResourceAccount(s, resourcePrefix string) (account string, ok bool) {
	const arnPrefix = "arn:aws:iam::"
	if !strings.HasPrefix(s, arnPrefix) {
		return "", false
	}
	rest := s[len(arnPrefix):]
	idx := strings.Index(rest, ":")
	if idx <= 0 {
		return "", false
	}
	account = rest[:idx]
	if !isAllDigits(account) {
		return "", false
	}
	resource := rest[idx+1:]
	if !strings.HasPrefix(resource, resourcePrefix) {
		return "", false
	}
	if len(resource) == len(resourcePrefix) {
		return "", false
	}
	return account, true
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
