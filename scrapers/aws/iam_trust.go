package aws

import (
	"encoding/json"
	"net/url"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
)

// decodeTrustPolicy parses a URL-encoded AssumeRolePolicyDocument into a
// typed trustPolicyDoc. Returns the zero value on error (caller logs).
func decodeTrustPolicy(encoded string) (trustPolicyDoc, error) {
	var doc trustPolicyDoc
	decoded, err := url.QueryUnescape(encoded)
	if err != nil {
		return doc, err
	}
	if err := json.Unmarshal([]byte(decoded), &doc); err != nil {
		return doc, err
	}
	return doc, nil
}

// attachTrustAccess runs the trust-policy resolver for a role and attaches
// the resulting ExternalUsers/Groups/Roles/ConfigAccess to the provided
// ScrapeResult in-place.
func attachTrustAccess(sr *v1.ScrapeResult, roleARN, accountID, encodedDoc string) {
	doc, err := decodeTrustPolicy(encodedDoc)
	if err != nil {
		logger.Warnf("failed to decode trust policy for %s: %v", roleARN, err)
		return
	}
	res := resolveTrustPolicy(roleARN, accountID, doc)
	for _, w := range res.Warnings {
		logger.Warnf("%s", w)
	}
	sr.ExternalUsers = append(sr.ExternalUsers, res.Users...)
	sr.ExternalGroups = append(sr.ExternalGroups, res.Groups...)
	sr.ExternalRoles = append(sr.ExternalRoles, res.Roles...)
	sr.ConfigAccess = append(sr.ConfigAccess, res.Accesses...)
}
