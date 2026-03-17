package devops

import (
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutyCtx "github.com/flanksource/duty/context"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func identityRef(uniqueName, displayName, id string) *IdentityRef {
	return &IdentityRef{UniqueName: uniqueName, DisplayName: displayName, ID: id}
}

func testCtx() api.ScrapeContext {
	return api.NewScrapeContext(dutyCtx.New())
}

func makeUsers(identities ...*IdentityRef) []dutyModels.ExternalUser {
	ctx := testCtx()
	for _, id := range identities {
		addExternalEntity(ctx, id, "test-org")
	}
	return ctx.Users()
}

func externalID(configType, id string) v1.ExternalID {
	return v1.ExternalID{ConfigType: configType, ExternalID: id}
}

func makeDef(id int, name, path string) ReleaseDefinition {
	return ReleaseDefinition{ID: id, Name: name, Path: path}
}

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

var _ = Describe("ScrapeContext.AddUser", func() {
	It("adds a new user", func() {
		ctx := testCtx()
		email := "alice@org.com"
		ctx.AddUser(dutyModels.ExternalUser{
			Name:     "Alice",
			Email:    &email,
			Aliases:  pq.StringArray{email, "alice-id"},
			Tenant:   "my-org",
			UserType: "AzureDevOps",
		})

		users := ctx.Users()
		Expect(users).To(HaveLen(1))
		Expect(users[0].Name).To(Equal("Alice"))
		Expect(users[0].Tenant).To(Equal("my-org"))
		Expect(users[0].ID).To(Equal(uuid.Nil))
	})

	It("deduplicates by alias overlap", func() {
		ctx := testCtx()
		email := "alice@org.com"
		ctx.AddUser(dutyModels.ExternalUser{
			Name:    "Alice",
			Email:   &email,
			Aliases: pq.StringArray{email, "alice-id"},
		})
		ctx.AddUser(dutyModels.ExternalUser{
			Name:    "Alice",
			Email:   &email,
			Aliases: pq.StringArray{email, "alice-id"},
		})

		Expect(ctx.Users()).To(HaveLen(1))
	})

	It("merges new aliases on overlap", func() {
		ctx := testCtx()
		email := "alice@org.com"
		ctx.AddUser(dutyModels.ExternalUser{
			Name:    "Alice",
			Aliases: pq.StringArray{email},
		})
		ctx.AddUser(dutyModels.ExternalUser{
			Name:    "Alice",
			Aliases: pq.StringArray{email, "new-alias"},
		})

		users := ctx.Users()
		Expect(users).To(HaveLen(1))
		Expect(users[0].Aliases).To(ContainElements(email, "new-alias"))
	})
})

var _ = Describe("ScrapeContext.AddGroup", func() {
	It("adds a new group", func() {
		ctx := testCtx()
		ctx.AddGroup(dutyModels.ExternalGroup{
			Name:      "QA Team",
			Aliases:   pq.StringArray{`[OIPA]\QA`, "group-id-1"},
			Tenant:    "org",
			GroupType: "AzureDevOps",
		})

		groups := ctx.Groups()
		Expect(groups).To(HaveLen(1))
		Expect(groups[0].Name).To(Equal("QA Team"))
		Expect(groups[0].GroupType).To(Equal("AzureDevOps"))
		Expect(groups[0].Aliases).To(ContainElements(`[OIPA]\QA`, "group-id-1"))
	})

	It("deduplicates by alias overlap", func() {
		ctx := testCtx()
		ctx.AddGroup(dutyModels.ExternalGroup{
			Name:    "QA Team",
			Aliases: pq.StringArray{`[OIPA]\QA`, "group-id-1"},
		})
		ctx.AddGroup(dutyModels.ExternalGroup{
			Name:    "QA Team",
			Aliases: pq.StringArray{`[OIPA]\QA`},
		})

		Expect(ctx.Groups()).To(HaveLen(1))
	})
})

var _ = Describe("addExternalEntity", func() {
	It("routes container identities to groups", func() {
		ctx := testCtx()
		container := &IdentityRef{
			UniqueName:  `[OIPA]\QA`,
			DisplayName: "QA Team",
			ID:          "group-id-1",
			IsContainer: true,
		}

		addExternalEntity(ctx, container, "org")

		Expect(ctx.Users()).To(BeEmpty())
		Expect(ctx.Groups()).To(HaveLen(1))
		Expect(ctx.Groups()[0].Name).To(Equal("QA Team"))
	})

	It("routes non-container identities to users", func() {
		ctx := testCtx()
		identity := identityRef("alice@org.com", "Alice", "alice-id")

		addExternalEntity(ctx, identity, "org")

		Expect(ctx.Groups()).To(BeEmpty())
		Expect(ctx.Users()).To(HaveLen(1))
		Expect(ctx.Users()[0].Name).To(Equal("Alice"))
	})

	It("skips nil or empty identity", func() {
		ctx := testCtx()

		addExternalEntity(ctx, nil, "org")
		addExternalEntity(ctx, &IdentityRef{UniqueName: ""}, "org")

		Expect(ctx.Users()).To(BeEmpty())
		Expect(ctx.Groups()).To(BeEmpty())
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
		log := deploymentAccessLog(nil, externalID(ReleaseType, "proj/1"), time.Now(), "Staging", nil)
		Expect(log).To(BeNil())
	})

	It("returns nil for unknown identity", func() {
		identity := identityRef("unknown@org.com", "Unknown", "uid")
		log := deploymentAccessLog(identity, externalID(ReleaseType, "proj/1"), time.Now(), "Staging", nil)
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
		Expect(log.ExternalUserAliases).ToNot(BeEmpty(),
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
			testCtx(),
			v1.AzureDevops{Organization: "test-org", Permissions: &v1.AzureDevopsPermissions{Enabled: true}},
			Project{Name: "MyProject"},
			def,
			nil,
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
			testCtx(),
			v1.AzureDevops{Organization: "test-org", Permissions: &v1.AzureDevopsPermissions{Enabled: true}},
			Project{Name: "MyProject"},
			def,
			nil,
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
			testCtx(),
			v1.AzureDevops{Organization: "test-org"},
			Project{Name: "MyProject"},
			def,
			nil,
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
			testCtx(),
			v1.AzureDevops{Organization: "org"},
			Project{Name: "MyProject"},
			def,
			nil,
			[]Release{release},
			cutoff,
		)

		Expect(result.Changes).To(BeEmpty())
		Expect(result.ConfigAccessLogs).To(BeEmpty())
	})

	It("uses environment name as ChangeType and includes deploySteps in details", func() {
		createdOn := time.Now().Add(-1 * time.Hour)
		cutoff := createdOn.Add(-1 * time.Hour)
		queuedOn := createdOn.Add(5 * time.Minute)

		def := makeDef(1, "Deploy", `\`)
		env := ReleaseEnvironment{
			ID: 10, Name: "Production", Status: "succeeded",
			DeploySteps: []DeployStep{
				{ID: 1, Status: "succeeded", Attempt: 1, QueuedOn: &queuedOn},
			},
		}
		release := makeRelease(100, nil, []ReleaseEnvironment{env}, createdOn)

		result := buildReleaseResult(
			testCtx(),
			v1.AzureDevops{Organization: "org"},
			Project{Name: "MyProject"},
			def,
			nil,
			[]Release{release},
			cutoff,
		)

		Expect(result.Changes).To(HaveLen(1))
		Expect(result.Changes[0].ChangeType).To(Equal("Production"))
		Expect(result.Changes[0].Details["status"]).To(Equal("succeeded"))
		Expect(result.Changes[0].Details["deploySteps"]).ToNot(BeNil())
	})

	It("uses defJSON as config when provided", func() {
		createdOn := time.Now().Add(-1 * time.Hour)
		cutoff := createdOn.Add(-1 * time.Hour)

		def := makeDef(1, "Deploy", `\`)
		release := makeRelease(100, nil, []ReleaseEnvironment{
			{ID: 10, Name: "Staging", Status: "succeeded"},
		}, createdOn)

		defJSON := map[string]any{
			"id":           1,
			"name":         "Deploy",
			"environments": []any{"env1"},
			"triggers":     []any{"trigger1"},
		}

		result := buildReleaseResult(
			testCtx(),
			v1.AzureDevops{Organization: "org"},
			Project{Name: "MyProject"},
			def,
			defJSON,
			[]Release{release},
			cutoff,
		)

		Expect(result.Config).To(Equal(defJSON))
	})
})

var _ = Describe("releaseApprovalChanges", func() {
	release := Release{ID: 1, Name: "Release-1", CreatedOn: time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC)}

	It("skips automated, skipped, and pending approvals", func() {
		approvals := []ReleaseApproval{
			{ID: 1, IsAutomated: true, Status: "approved"},
			{ID: 2, Status: "skipped"},
			{ID: 3, Status: "pending"},
		}
		Expect(releaseApprovalChanges(approvals, release, "Staging", "proj/1", "src", "base")).To(BeEmpty())
	})

	It("emits Approved change with correct fields", func() {
		approvals := []ReleaseApproval{{
			ID:         10,
			Status:     "approved",
			ApprovedBy: &IdentityRef{UniqueName: "bob@example.com"},
			Comments:   "Looks good",
		}}

		changes := releaseApprovalChanges(approvals, release, "Staging", "proj/1", "src", "base")
		Expect(changes).To(HaveLen(1))

		ch := changes[0]
		Expect(ch.ChangeType).To(Equal(ChangeTypeApproved))
		Expect(*ch.CreatedBy).To(Equal("bob@example.com"))
		Expect(ch.ExternalID).To(Equal("proj/1"))
		Expect(ch.ConfigType).To(Equal(ReleaseType))
		Expect(ch.Severity).To(Equal("info"))
		Expect(ch.Summary).To(ContainSubstring("Release-1"))
		Expect(ch.Summary).To(ContainSubstring("Staging"))
		Expect(ch.Summary).To(ContainSubstring("bob@example.com"))
		Expect(ch.Summary).To(ContainSubstring("Looks good"))
		Expect(ch.ExternalChangeID).To(Equal("base/approval/10"))
	})

	It("emits Rejected change with high severity", func() {
		approvals := []ReleaseApproval{{
			ID:       20,
			Status:   "rejected",
			Approver: &IdentityRef{UniqueName: "carol@example.com"},
		}}

		changes := releaseApprovalChanges(approvals, release, "Prod", "proj/1", "src", "base")
		Expect(changes).To(HaveLen(1))
		Expect(changes[0].ChangeType).To(Equal(ChangeTypeRejected))
		Expect(changes[0].Severity).To(Equal("high"))
		Expect(*changes[0].CreatedBy).To(Equal("carol@example.com"))
	})

	It("prefers ApprovedBy over Approver", func() {
		approvals := []ReleaseApproval{{
			ID:         30,
			Status:     "approved",
			Approver:   &IdentityRef{UniqueName: "assigned@example.com"},
			ApprovedBy: &IdentityRef{UniqueName: "actual@example.com"},
		}}

		changes := releaseApprovalChanges(approvals, release, "Staging", "proj/1", "src", "base")
		Expect(changes).To(HaveLen(1))
		Expect(*changes[0].CreatedBy).To(Equal("actual@example.com"))
	})
})
