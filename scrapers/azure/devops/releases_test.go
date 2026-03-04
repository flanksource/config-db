package devops

import (
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func identityRef(uniqueName, displayName, id string) *IdentityRef {
	return &IdentityRef{UniqueName: uniqueName, DisplayName: displayName, ID: id}
}

func makeUsers(identities ...*IdentityRef) map[string]dutyModels.ExternalUser {
	m := make(map[string]dutyModels.ExternalUser)
	for _, id := range identities {
		ensureExternalUser(id, "test-org", m)
	}
	return m
}

func externalID(configType, id string) v1.ExternalID {
	return v1.ExternalID{ConfigType: configType, ExternalID: id}
}

func makeDef(id int, name, path string) ReleaseDefinition {
	return ReleaseDefinition{ID: id, Name: name, Path: path}
}

// findAccessLog searches for an access log matching the given property key-value pairs.
func findAccessLog(logs []v1.ExternalConfigAccessLog, props map[string]string) *v1.ExternalConfigAccessLog {
	for i, log := range logs {
		match := true
		for k, v := range props {
			if log.ConfigAccessLog.Properties[k] != v {
				match = false
				break
			}
		}
		if match {
			return &logs[i]
		}
	}
	return nil
}

func makeRelease(id int, createdBy *IdentityRef, envs []ReleaseEnvironment, createdOn time.Time) Release {
	return Release{
		ID:           id,
		Name:         "Release-1",
		CreatedOn:    createdOn,
		CreatedBy:    createdBy,
		Environments: envs,
	}
}

var _ = Describe("ensureExternalUser", func() {
	It("adds a new user", func() {
		users := make(map[string]dutyModels.ExternalUser)
		identity := identityRef("alice@org.com", "Alice", "alice-id")

		ensureExternalUser(identity, "my-org", users)

		u, ok := users["alice@org.com"]
		Expect(ok).To(BeTrue())
		Expect(u.Name).To(Equal("Alice"))
		Expect(u.Tenant).To(Equal("my-org"))
	})

	It("skips nil or empty identity", func() {
		users := make(map[string]dutyModels.ExternalUser)

		ensureExternalUser(nil, "org", users)
		ensureExternalUser(&IdentityRef{UniqueName: ""}, "org", users)

		Expect(users).To(BeEmpty())
	})

	It("is idempotent on duplicate", func() {
		users := make(map[string]dutyModels.ExternalUser)
		identity := identityRef("alice@org.com", "Alice", "alice-id")

		ensureExternalUser(identity, "org", users)
		ensureExternalUser(identity, "org", users)

		Expect(users).To(HaveLen(1))
	})
})

var _ = Describe("deploymentAccessLog", func() {
	It("returns a log with correct properties for a known identity", func() {
		identity := identityRef("alice@org.com", "Alice", "alice-id")
		users := makeUsers(identity)
		eid := externalID(ReleaseType, "proj/42")
		createdAt := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)

		log := deploymentAccessLog(identity, eid, createdAt, "Production", users)

		Expect(log).ToNot(BeNil())
		Expect(log.ConfigExternalID.ExternalID).To(Equal(eid.ExternalID))
		Expect(log.ConfigExternalID.ConfigType).To(Equal(eid.ConfigType))
		Expect(log.ConfigAccessLog.CreatedAt).To(Equal(createdAt))
		Expect(log.ConfigAccessLog.Properties["role"]).To(Equal("Deployment"))
		Expect(log.ConfigAccessLog.Properties["environment"]).To(Equal("Production"))
	})

	It("returns nil for nil identity", func() {
		users := make(map[string]dutyModels.ExternalUser)
		log := deploymentAccessLog(nil, externalID(ReleaseType, "proj/1"), time.Now(), "Staging", users)
		Expect(log).To(BeNil())
	})

	It("returns nil for unknown identity", func() {
		identity := identityRef("unknown@org.com", "Unknown", "uid")
		log := deploymentAccessLog(identity, externalID(ReleaseType, "proj/1"), time.Now(), "Staging", map[string]dutyModels.ExternalUser{})
		Expect(log).To(BeNil())
	})
})

var _ = Describe("approvalAccessLog", func() {
	It("includes comment for approved approval", func() {
		approver := identityRef("bob@org.com", "Bob", "bob-id")
		users := makeUsers(approver)
		eid := externalID(ReleaseType, "proj/42")
		createdAt := time.Date(2024, 2, 1, 9, 0, 0, 0, time.UTC)

		a := ReleaseApproval{
			Status:     "approved",
			ApprovedBy: approver,
			Comments:   "LGTM",
		}

		log := approvalAccessLog(a, eid, "Prod", createdAt, users)

		Expect(log).ToNot(BeNil())
		Expect(log.ConfigAccessLog.Properties["role"]).To(Equal("DeploymentApproval"))
		Expect(log.ConfigAccessLog.Properties["status"]).To(Equal("approved"))
		Expect(log.ConfigAccessLog.Properties["comments"]).To(Equal("LGTM"))
		Expect(log.ConfigAccessLog.Properties["environment"]).To(Equal("Prod"))
	})

	It("omits comments key for rejected approval with no comment", func() {
		approver := identityRef("carol@org.com", "Carol", "carol-id")
		users := makeUsers(approver)

		a := ReleaseApproval{
			Status:     "rejected",
			ApprovedBy: approver,
		}

		log := approvalAccessLog(a, externalID(ReleaseType, "proj/1"), "Staging", time.Now(), users)

		Expect(log).ToNot(BeNil())
		_, hasComment := log.ConfigAccessLog.Properties["comments"]
		Expect(hasComment).To(BeFalse())
	})

	It("returns nil for pending approval", func() {
		approver := identityRef("dave@org.com", "Dave", "dave-id")
		users := makeUsers(approver)

		a := ReleaseApproval{Status: "pending", Approver: approver}
		log := approvalAccessLog(a, externalID(ReleaseType, "proj/1"), "Staging", time.Now(), users)
		Expect(log).To(BeNil())
	})

	It("returns nil for automated approval", func() {
		approver := identityRef("auto@org.com", "Auto", "auto-id")
		users := makeUsers(approver)

		a := ReleaseApproval{Status: "approved", IsAutomated: true, ApprovedBy: approver}
		log := approvalAccessLog(a, externalID(ReleaseType, "proj/1"), "Staging", time.Now(), users)
		Expect(log).To(BeNil())
	})

	It("returns nil for skipped approval", func() {
		approver := identityRef("eve@org.com", "Eve", "eve-id")
		users := makeUsers(approver)

		a := ReleaseApproval{Status: "skipped", ApprovedBy: approver}
		log := approvalAccessLog(a, externalID(ReleaseType, "proj/1"), "Staging", time.Now(), users)
		Expect(log).To(BeNil())
	})

	It("falls back to Approver when ApprovedBy is nil", func() {
		approver := identityRef("fallback@org.com", "Fallback", "fb-id")
		users := makeUsers(approver)

		a := ReleaseApproval{
			Status:   "approved",
			Approver: approver,
		}

		log := approvalAccessLog(a, externalID(ReleaseType, "proj/1"), "Staging", time.Now(), users)
		Expect(log).ToNot(BeNil())
		Expect(log.ConfigAccessLog.ExternalUserID).ToNot(Equal(uuid.Nil),
			"fallback identity should be attributed in the access log")
	})
})

var _ = Describe("buildReleaseResult", func() {
	It("emits deployment access log", func() {
		trigger := identityRef("alice@org.com", "Alice", "alice-id")
		createdOn := time.Now().Add(-1 * time.Hour)
		cutoff := createdOn.Add(-1 * time.Hour)

		def := makeDef(1, "Deploy", `\`)
		env := ReleaseEnvironment{ID: 10, Name: "Production", Status: "succeeded"}
		release := makeRelease(100, trigger, []ReleaseEnvironment{env}, createdOn)

		result := buildReleaseResult(
			v1.AzureDevops{Organization: "test-org"},
			Project{Name: "MyProject"},
			def,
			[]Release{release},
			cutoff,
		)

		Expect(result.ConfigAccessLogs).ToNot(BeEmpty())
		log := findAccessLog(result.ConfigAccessLogs, map[string]string{"role": "Deployment", "environment": "Production"})
		Expect(log).ToNot(BeNil(), "no Deployment access log for Production env")
	})

	It("emits approval access log", func() {
		trigger := identityRef("alice@org.com", "Alice", "alice-id")
		approver := identityRef("bob@org.com", "Bob", "bob-id")
		createdOn := time.Now().Add(-1 * time.Hour)
		cutoff := createdOn.Add(-1 * time.Hour)

		def := makeDef(1, "Deploy", `\`)
		env := ReleaseEnvironment{
			ID:     10,
			Name:   "Staging",
			Status: "succeeded",
			PreDeployApprovals: []ReleaseApproval{
				{Status: "approved", ApprovedBy: approver, Comments: "OK"},
			},
		}
		release := makeRelease(100, trigger, []ReleaseEnvironment{env}, createdOn)

		result := buildReleaseResult(
			v1.AzureDevops{Organization: "test-org"},
			Project{Name: "MyProject"},
			def,
			[]Release{release},
			cutoff,
		)

		approvalLog := findAccessLog(result.ConfigAccessLogs, map[string]string{"role": "DeploymentApproval", "status": "approved"})
		Expect(approvalLog).ToNot(BeNil(), "no DeploymentApproval access log")
	})

	It("skips pending approval environment", func() {
		trigger := identityRef("alice@org.com", "Alice", "alice-id")
		createdOn := time.Now().Add(-1 * time.Hour)
		cutoff := createdOn.Add(-1 * time.Hour)

		def := makeDef(1, "Deploy", `\`)
		env := ReleaseEnvironment{
			ID:     10,
			Name:   "Prod",
			Status: "inProgress",
			PreDeployApprovals: []ReleaseApproval{
				{Status: "pending", IsAutomated: false},
			},
		}
		release := makeRelease(100, trigger, []ReleaseEnvironment{env}, createdOn)

		result := buildReleaseResult(
			v1.AzureDevops{Organization: "test-org"},
			Project{Name: "MyProject"},
			def,
			[]Release{release},
			cutoff,
		)

		Expect(result.Changes).To(BeEmpty())
		for _, log := range result.ConfigAccessLogs {
			Expect(log.ConfigAccessLog.Properties["role"]).ToNot(Equal("DeploymentApproval"))
		}
	})

	It("excludes stale releases", func() {
		cutoff := time.Now()
		createdOn := cutoff.Add(-1 * time.Hour)

		def := makeDef(1, "Deploy", `\`)
		env := ReleaseEnvironment{ID: 10, Name: "Prod", Status: "succeeded"}
		release := makeRelease(100, nil, []ReleaseEnvironment{env}, createdOn)

		result := buildReleaseResult(
			v1.AzureDevops{Organization: "org"},
			Project{Name: "MyProject"},
			def,
			[]Release{release},
			cutoff,
		)

		Expect(result.Changes).To(BeEmpty())
		Expect(result.ConfigAccessLogs).To(BeEmpty())
	})
})
