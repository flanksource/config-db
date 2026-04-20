package devops

import (
	"encoding/json"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutyCtx "github.com/flanksource/duty/context"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
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

	It("emits Promotion envelope with canonical ChangeType and raw deploySteps", func() {
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
		change := result.Changes[0]
		Expect(change.ChangeType).To(Equal(types.ChangeTypeDeployment))
		Expect(change.Details["kind"]).To(Equal("Promotion/v1"))

		to := change.Details["to"].(map[string]any)
		Expect(to["name"]).To(Equal("Production"))
		Expect(to["stage"]).To(Equal(string(types.EnvironmentStageProduction)))
		Expect(to["identifier"]).To(Equal("10"))
		Expect(change.Details["version"]).To(Equal("Release-1"))

		raw := change.Details["raw"].(map[string]any)
		Expect(raw["status"]).To(Equal("succeeded"))
		Expect(raw["deploySteps"]).ToNot(BeNil())
	})

	It("carries reason, triggerReason, variables, and artifacts through Promotion envelope", func() {
		createdOn := time.Now().Add(-1 * time.Hour)
		cutoff := createdOn.Add(-1 * time.Hour)

		def := makeDef(1, "Deploy", `\`)
		env := ReleaseEnvironment{
			ID: 10, Name: "Production", Status: "succeeded",
			TriggerReason: "After successful deployment of Staging",
			Variables: map[string]ConfigurationVariable{
				"envVar": {Value: "envVal"},
			},
		}
		release := Release{
			ID: 100, Name: "Release-1", CreatedOn: createdOn,
			Reason:      "continuousIntegration",
			Description: "CI triggered release",
			Variables: map[string]ConfigurationVariable{
				"releaseVar": {Value: "relVal"},
				"secret":     {Value: "hidden", IsSecret: true},
			},
			Artifacts: []ReleaseArtifact{{
				Type: "Build", Alias: "_build", IsPrimary: true,
				DefinitionReference: map[string]ArtifactSourceRef{
					"definition": {Name: "my-pipeline"},
					"version":    {Name: "20240101.1"},
					"branch":     {Name: "refs/heads/main"},
				},
			}},
			Environments: []ReleaseEnvironment{env},
		}

		result := buildReleaseResult(
			testCtx(),
			v1.AzureDevops{Organization: "org"},
			Project{Name: "MyProject"},
			def, nil, []Release{release}, cutoff,
		)

		Expect(result.Changes).To(HaveLen(1))
		details := result.Changes[0].Details

		properties := details["properties"].(map[string]any)
		Expect(properties["reason"]).To(Equal("continuousIntegration"))
		Expect(properties["description"]).To(Equal("CI triggered release"))
		Expect(properties["triggerReason"]).To(Equal("After successful deployment of Staging"))

		Expect(details["artifact"]).To(Equal("my-pipeline@20240101.1 (refs/heads/main)"))
		source := details["source"].(map[string]any)
		git := source["git"].(map[string]any)
		Expect(git["branch"]).To(Equal("refs/heads/main"))
		Expect(git["version"]).To(Equal("20240101.1"))

		raw := details["raw"].(map[string]any)
		Expect(raw["variables"]).To(HaveKeyWithValue("releaseVar", "relVal"))
		Expect(raw["variables"]).ToNot(HaveKey("secret"))
		Expect(raw["environmentVariables"]).To(HaveKeyWithValue("envVar", "envVal"))
		rawArtifacts := raw["artifacts"].([]any)
		Expect(rawArtifacts).To(HaveLen(1))
		Expect(rawArtifacts[0].(map[string]any)["definition"]).To(Equal("my-pipeline"))
		Expect(rawArtifacts[0].(map[string]any)["isPrimary"]).To(BeTrue())
	})

	It("emits typed Approval envelope that round-trips through UnmarshalChangeDetails", func() {
		createdOn := time.Now().Add(-1 * time.Hour)
		cutoff := createdOn.Add(-1 * time.Hour)

		approver := identityRef("alice@org.com", "Alice", "alice-id")
		def := makeDef(1, "Deploy", `\`)
		env := ReleaseEnvironment{
			ID: 10, Name: "Staging", Status: "succeeded",
			PreDeployApprovals: []ReleaseApproval{{
				ID: 42, ApprovalType: "preDeploy", Status: "approved",
				ApprovedBy: approver, Comments: "LGTM",
			}},
		}
		release := makeRelease(100, nil, []ReleaseEnvironment{env}, createdOn)

		result := buildReleaseResult(
			testCtx(),
			v1.AzureDevops{Organization: "org"},
			Project{Name: "MyProject"},
			def, nil, []Release{release}, cutoff,
		)

		Expect(result.Changes).To(HaveLen(2))
		approvalChange := result.Changes[1]
		Expect(approvalChange.ChangeType).To(Equal(types.ChangeTypeApproved))
		Expect(approvalChange.Details["kind"]).To(Equal("Approval/v1"))
		Expect(approvalChange.Details["stage"]).To(Equal(string(types.ApprovalStagePreDeployment)))
		Expect(approvalChange.Details["status"]).To(Equal(string(types.ApprovalStatusApproved)))

		approverMap := approvalChange.Details["approver"].(map[string]any)
		Expect(approverMap["id"]).To(Equal("alice@org.com"))
		Expect(approverMap["comment"]).To(Equal("LGTM"))

		raw := approvalChange.Details["raw"].(map[string]any)
		Expect(raw["comments"]).To(Equal("LGTM"))

		payload, err := json.Marshal(approvalChange.Details)
		Expect(err).ToNot(HaveOccurred())
		decoded, err := types.UnmarshalChangeDetails(payload)
		Expect(err).ToNot(HaveOccurred())
		typedApproval, ok := decoded.(types.Approval)
		Expect(ok).To(BeTrue())
		Expect(typedApproval.Stage).To(Equal(types.ApprovalStagePreDeployment))
		Expect(typedApproval.Status).To(Equal(types.ApprovalStatusApproved))
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

var _ = Describe("flattenVariables", func() {
	It("returns nil for empty map", func() {
		Expect(flattenVariables(nil)).To(BeNil())
		Expect(flattenVariables(map[string]ConfigurationVariable{})).To(BeNil())
	})

	It("filters out secret variables", func() {
		vars := map[string]ConfigurationVariable{
			"env":    {Value: "prod"},
			"secret": {Value: "hunter2", IsSecret: true},
			"region": {Value: "us-east-1"},
		}
		result := flattenVariables(vars)
		Expect(result).To(Equal(map[string]string{"env": "prod", "region": "us-east-1"}))
	})

	It("returns nil when all variables are secret", func() {
		vars := map[string]ConfigurationVariable{
			"password": {Value: "secret", IsSecret: true},
		}
		Expect(flattenVariables(vars)).To(BeNil())
	})
})

var _ = Describe("summarizeArtifacts", func() {
	It("extracts definition, version, and branch from definitionReference", func() {
		artifacts := []ReleaseArtifact{{
			Type:      "Build",
			Alias:     "_my-build",
			IsPrimary: true,
			DefinitionReference: map[string]ArtifactSourceRef{
				"definition": {ID: "42", Name: "my-pipeline"},
				"version":    {ID: "123", Name: "20240101.1"},
				"branch":     {ID: "", Name: "refs/heads/main"},
			},
		}}
		result := summarizeArtifacts(artifacts)
		Expect(result).To(HaveLen(1))
		Expect(result[0]).To(Equal(map[string]any{
			"type":       "Build",
			"alias":      "_my-build",
			"isPrimary":  true,
			"definition": "my-pipeline",
			"version":    "20240101.1",
			"branch":     "refs/heads/main",
		}))
	})

	It("omits empty definitionReference fields", func() {
		artifacts := []ReleaseArtifact{{
			Type:  "Git",
			Alias: "_repo",
		}}
		result := summarizeArtifacts(artifacts)
		Expect(result).To(HaveLen(1))
		Expect(result[0]).To(Equal(map[string]any{
			"type":  "Git",
			"alias": "_repo",
		}))
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

var _ = Describe("extractApprovalPolicies", func() {
	const (
		org          = "test-org"
		releaseExtID = "azuredevops://test-org/Proj/release/42"
	)
	var ado AzureDevopsScraper

	config := v1.AzureDevops{Organization: org}

	// Minimal group descriptor — DescriptorAliases returns at least the input
	// form when the descriptor can't be decoded to a SID, which is enough to
	// assert the access entry carries the group identifier.
	groupDescriptor := "vssgp.opaque-group-descriptor"

	buildEnv := func(name string, pre, post []map[string]any) map[string]any {
		env := map[string]any{"name": name}
		if pre != nil {
			env["preDeployApprovals"] = map[string]any{"approvals": toAnySlice(pre)}
		}
		if post != nil {
			env["postDeployApprovals"] = map[string]any{"approvals": toAnySlice(post)}
		}
		return env
	}

	automated := map[string]any{"id": 1, "isAutomated": true}
	userApproval := map[string]any{
		"id":          2,
		"isAutomated": false,
		"approver": map[string]any{
			"id":          "user-id",
			"displayName": "Alice",
			"uniqueName":  "alice@org.com",
			"descriptor":  "aad.user-descriptor",
		},
	}
	groupApproval := map[string]any{
		"id":          3,
		"isAutomated": false,
		"approver": map[string]any{
			"id":          "group-id",
			"displayName": "Release Managers",
			"uniqueName":  "vstfs:///Classification/TeamProject/release-managers",
			"descriptor":  groupDescriptor,
			"isContainer": true,
		},
	}

	It("skips automated approvals", func() {
		defJSON := map[string]any{"environments": []any{
			buildEnv("Dev", []map[string]any{automated}, []map[string]any{automated}),
		}}
		access, roles := ado.extractApprovalPolicies(testCtx(), config, defJSON, releaseExtID)
		Expect(access).To(BeEmpty())
		Expect(roles).To(BeEmpty())
	})

	It("emits user access with env-scoped pre-deploy role", func() {
		defJSON := map[string]any{"environments": []any{
			buildEnv("Dev", []map[string]any{userApproval}, nil),
		}}
		access, roles := ado.extractApprovalPolicies(testCtx(), config, defJSON, releaseExtID)

		Expect(roles).To(HaveLen(1))
		Expect(roles[0].Name).To(Equal("Approver:Dev:Pre"))
		Expect(roles[0].RoleType).To(Equal("AzureDevOps"))
		Expect(roles[0].Tenant).To(Equal(org))

		Expect(access).To(HaveLen(1))
		entry := access[0]
		Expect(entry.ConfigExternalID.ConfigType).To(Equal(ReleaseType))
		Expect(entry.ConfigExternalID.ExternalID).To(Equal(releaseExtID))
		Expect(entry.ExternalRoleID).To(Equal(&roles[0].ID))
		Expect(entry.ExternalUserAliases).To(ConsistOf("alice@org.com"))
		Expect(entry.ExternalGroupAliases).To(BeEmpty())
	})

	It("emits group access with env-scoped post-deploy role", func() {
		defJSON := map[string]any{"environments": []any{
			buildEnv("Prod", nil, []map[string]any{groupApproval}),
		}}
		access, roles := ado.extractApprovalPolicies(testCtx(), config, defJSON, releaseExtID)

		Expect(roles).To(HaveLen(1))
		Expect(roles[0].Name).To(Equal("Approver:Prod:Post"))

		Expect(access).To(HaveLen(1))
		Expect(access[0].ExternalUserAliases).To(BeEmpty())
		Expect(access[0].ExternalGroupAliases).ToNot(BeEmpty())
		Expect(access[0].ExternalGroupAliases).To(ContainElement(groupDescriptor))
	})

	It("produces a distinct role per environment", func() {
		defJSON := map[string]any{"environments": []any{
			buildEnv("Dev", []map[string]any{userApproval}, nil),
			buildEnv("Staging", []map[string]any{groupApproval}, nil),
		}}
		access, roles := ado.extractApprovalPolicies(testCtx(), config, defJSON, releaseExtID)

		Expect(access).To(HaveLen(2))
		Expect(roles).To(HaveLen(2),
			"different envs must resolve to different roles so authorisation boundaries aren't collapsed")
		names := []string{roles[0].Name, roles[1].Name}
		Expect(names).To(ConsistOf("Approver:Dev:Pre", "Approver:Staging:Pre"))
	})

	It("dedups the role within a single env+phase", func() {
		defJSON := map[string]any{"environments": []any{
			buildEnv("Dev", []map[string]any{userApproval, groupApproval}, nil),
		}}
		access, roles := ado.extractApprovalPolicies(testCtx(), config, defJSON, releaseExtID)

		Expect(access).To(HaveLen(2))
		Expect(roles).To(HaveLen(1),
			"two approvers on the same env+phase must share one ExternalRole")
	})

	It("skips environments without a name", func() {
		defJSON := map[string]any{"environments": []any{
			map[string]any{"preDeployApprovals": map[string]any{"approvals": []any{userApproval}}},
		}}
		access, roles := ado.extractApprovalPolicies(testCtx(), config, defJSON, releaseExtID)
		Expect(access).To(BeEmpty())
		Expect(roles).To(BeEmpty())
	})

	It("returns empty when environments is missing or malformed", func() {
		access, roles := ado.extractApprovalPolicies(testCtx(), config, map[string]any{}, releaseExtID)
		Expect(access).To(BeEmpty())
		Expect(roles).To(BeEmpty())

		access, roles = ado.extractApprovalPolicies(testCtx(), config, map[string]any{"environments": "not-a-slice"}, releaseExtID)
		Expect(access).To(BeEmpty())
		Expect(roles).To(BeEmpty())
	})
})

var _ = Describe("mergeRolesByID", func() {
	It("appends roles whose UUID is not already present", func() {
		a := dutyModels.ExternalRole{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Name: "A"}
		b := dutyModels.ExternalRole{ID: uuid.MustParse("22222222-2222-2222-2222-222222222222"), Name: "B"}
		merged := mergeRolesByID([]dutyModels.ExternalRole{a}, []dutyModels.ExternalRole{b})
		Expect(merged).To(HaveLen(2))
	})

	It("drops duplicates by UUID", func() {
		id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
		a := dutyModels.ExternalRole{ID: id, Name: "A"}
		dup := dutyModels.ExternalRole{ID: id, Name: "A-alt"}
		merged := mergeRolesByID([]dutyModels.ExternalRole{a}, []dutyModels.ExternalRole{dup})
		Expect(merged).To(HaveLen(1))
		Expect(merged[0].Name).To(Equal("A"), "existing entry wins")
	})
})

func toAnySlice(in []map[string]any) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}
