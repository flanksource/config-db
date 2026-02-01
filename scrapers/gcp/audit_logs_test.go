package gcp

import (
	"testing"
	"time"

	"github.com/flanksource/duty/types"
	"github.com/onsi/gomega"

	v1 "github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/utils"
)

func TestBuildAuditLogQuery_SpecificSQL(t *testing.T) {
	g := gomega.NewWithT(t)

	mockTime := time.Date(2025, 6, 19, 12, 0, 0, 0, time.UTC)
	restore := utils.MockTime(mockTime)
	defer restore()

	// Test case for specific SQL query with filtering conditions
	auditLogs := v1.GCPAuditLogs{
		Dataset: "default._AllLogs",
		Since:   "7d",
		UserAgents: types.MatchExpressions{
			"!kube-controller-manager/*",
			"!cloud-controller-manager/*",
		},
		PrincipalEmails: types.MatchExpressions{
			"!system:node:*",
			"!*@container-engine-robot.iam.gserviceaccount.com",
			"!*@cloudservices.gserviceaccount.com",
		},
		ServiceNames: types.MatchExpressions{
			"!k8s.io",
		},
		Permissions: types.MatchExpressions{
			"!*.list",
			"!*.head",
			"!*.getIamPolicy",
			"!*.listIamPolicy",
			"!*.get",
			"!io.k8s.coordination.v1.leases.*",
			"!io.k8s.authentication.v1.tokenreview*",
			"!io.k8s.authorization.v1.*subjectaccessreview*",
		},
	}

	query, params, err := buildAuditLogQuery(auditLogs)
	g.Expect(err).To(gomega.BeNil(), "failed to build audit log query")

	expectedQuery := `
WITH auth as (
  select  
    timestamp,
    proto_payload.audit_log,
    proto_payload.audit_log.service_name as service_name,
    proto_payload.audit_log.authentication_info.principal_email as email,  
    proto_payload.audit_log.authorization_info[0].permission_type AS permission_type,
    proto_payload.audit_log.authorization_info[0].permission AS permission
  FROM ` + "`default._AllLogs`" + `
  Where timestamp >= '2025-06-12' AND ARRAY_LENGTH(proto_payload.audit_log.authorization_info) > 0 AND (proto_payload.audit_log.request_metadata.caller_supplied_user_agent NOT LIKE ? AND proto_payload.audit_log.request_metadata.caller_supplied_user_agent NOT LIKE ?) AND (proto_payload.audit_log.authentication_info.principal_email NOT LIKE ? AND proto_payload.audit_log.authentication_info.principal_email NOT LIKE ? AND proto_payload.audit_log.authentication_info.principal_email NOT LIKE ?)
) 

SELECT email, permission, permission_type, max(timestamp) as timestamp
from auth 
WHERE (permission NOT LIKE ? AND permission NOT LIKE ? AND permission NOT LIKE ? AND permission NOT LIKE ? AND permission NOT LIKE ? AND permission NOT LIKE ? AND permission NOT LIKE ? AND permission NOT LIKE ?) AND (service_name <> ?)
group by email, permission, permission_type
`
	g.Expect(query).To(gomega.Equal(expectedQuery), "query mismatch")

	expectedParams := []any{
		"kube-controller-manager/%",
		"cloud-controller-manager/%",
		"system:node:%",
		"%@container-engine-robot.iam.gserviceaccount.com",
		"%@cloudservices.gserviceaccount.com",
		"%.list",
		"%.head",
		"%.getIamPolicy",
		"%.listIamPolicy",
		"%.get",
		"io.k8s.coordination.v1.leases.%",
		"io.k8s.authentication.v1.tokenreview%",
		"io.k8s.authorization.v1.*subjectaccessreview%",
		"k8s.io",
	}

	g.Expect(len(params)).To(gomega.Equal(len(expectedParams)), "parameter count mismatch")
	for i, expectedParam := range expectedParams {
		g.Expect(params[i].Value).To(gomega.Equal(expectedParam), "parameter mismatch at index %d", i)
	}
}
