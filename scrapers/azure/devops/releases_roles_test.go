package devops

import (
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	dutyModels "github.com/flanksource/duty/models"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func approverMap(identity *IdentityRef, isAutomated bool) map[string]any {
	return map[string]any{
		"isAutomated": isAutomated,
		"approver": map[string]any{
			"id":          identity.ID,
			"displayName": identity.DisplayName,
			"uniqueName":  identity.UniqueName,
			"isContainer": identity.IsContainer,
		},
	}
}

func defJSONWithEnvs(envs ...map[string]any) map[string]any {
	envsAny := make([]any, 0, len(envs))
	for _, e := range envs {
		envsAny = append(envsAny, e)
	}
	return map[string]any{"environments": envsAny}
}

func envWithApprovals(name string, pre, post []map[string]any) map[string]any {
	env := map[string]any{"name": name}
	toAny := func(in []map[string]any) []any {
		if in == nil {
			return nil
		}
		out := make([]any, 0, len(in))
		for _, m := range in {
			out = append(out, m)
		}
		return out
	}
	if pre != nil {
		env["preDeployApprovals"] = map[string]any{"approvals": toAny(pre)}
	}
	if post != nil {
		env["postDeployApprovals"] = map[string]any{"approvals": toAny(post)}
	}
	return env
}

func findRole(roles []dutyModels.ExternalRole, name string) *dutyModels.ExternalRole {
	for i, r := range roles {
		if r.Name == name {
			return &roles[i]
		}
	}
	return nil
}

func accessesForRoleAlias(accesses []v1.ExternalConfigAccess, alias string) []v1.ExternalConfigAccess {
	var out []v1.ExternalConfigAccess
	for _, a := range accesses {
		for _, r := range a.ExternalRoleAliases {
			if r == alias {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

var _ = Describe("pickRoleNames", func() {
	It("uses {Env}Approver when only pre", func() {
		pre, post := pickRoleNames("Production", true, false)
		Expect(pre).To(Equal("ProductionApprover"))
		Expect(post).To(Equal(""))
	})

	It("uses {Env}Approver when only post", func() {
		pre, post := pickRoleNames("Production", false, true)
		Expect(pre).To(Equal(""))
		Expect(post).To(Equal("ProductionApprover"))
	})

	It("splits when both", func() {
		pre, post := pickRoleNames("Production", true, true)
		Expect(pre).To(Equal("ProductionPreApprover"))
		Expect(post).To(Equal("ProductionPostApprover"))
	})

	It("returns empty when neither", func() {
		pre, post := pickRoleNames("Production", false, false)
		Expect(pre).To(Equal(""))
		Expect(post).To(Equal(""))
	})
})

var _ = Describe("release definition approver roles", func() {
	var (
		config           v1.AzureDevops
		configExternalID v1.ExternalID
		def              ReleaseDefinition
	)

	BeforeEach(func() {
		config = v1.AzureDevops{
			Organization: "test-org",
			Permissions:  &v1.AzureDevopsPermissions{Enabled: true},
		}
		def = makeDef(42, "Deploy", `\`)
		configExternalID = v1.ExternalID{
			ConfigType: ReleaseType,
			ExternalID: ReleaseExternalID("test-org", "MyProject", def.ID),
		}
	})

	It("emits {Env}Approver when only pre-approvers present", func() {
		alice := identityRef("alice@org.com", "Alice", "alice-id")
		defJSON := defJSONWithEnvs(envWithApprovals("Production",
			[]map[string]any{approverMap(alice, false)}, nil))

		roles, access := emitReleaseDefinitionRoles(testCtx(), config, def, defJSON, configExternalID)

		Expect(roles).To(HaveLen(1))
		Expect(roles[0].Name).To(Equal("ProductionApprover"))
		Expect(roles[0].RoleType).To(Equal("AzureDevOps"))
		Expect(roles[0].Tenant).To(Equal("test-org"))
		Expect(roles[0].Aliases).To(HaveLen(1))
		Expect(roles[0].Aliases[0]).To(ContainSubstring("/env/Production/ProductionApprover"))

		Expect(access).To(HaveLen(1))
		Expect(access[0].ConfigExternalID).To(Equal(configExternalID))
		Expect(access[0].ExternalRoleAliases).To(ConsistOf(roles[0].Aliases[0]))
		Expect(access[0].ExternalUserAliases).To(ContainElement("alice@org.com"))
		Expect(access[0].ExternalGroupAliases).To(BeEmpty())
	})

	It("emits {Env}Approver when only post-approvers present", func() {
		bob := identityRef("bob@org.com", "Bob", "bob-id")
		defJSON := defJSONWithEnvs(envWithApprovals("Production", nil,
			[]map[string]any{approverMap(bob, false)}))

		roles, access := emitReleaseDefinitionRoles(testCtx(), config, def, defJSON, configExternalID)

		Expect(roles).To(HaveLen(1))
		Expect(roles[0].Name).To(Equal("ProductionApprover"))

		Expect(access).To(HaveLen(1))
		Expect(access[0].ExternalUserAliases).To(ContainElement("bob@org.com"))
	})

	It("splits role names when both pre and post approvers exist", func() {
		alice := identityRef("alice@org.com", "Alice", "alice-id")
		qa := &IdentityRef{ID: "qa-id", DisplayName: "QA Team", UniqueName: "qa-team", IsContainer: true}

		defJSON := defJSONWithEnvs(envWithApprovals("Production",
			[]map[string]any{approverMap(alice, false)},
			[]map[string]any{approverMap(qa, false)}))

		roles, access := emitReleaseDefinitionRoles(testCtx(), config, def, defJSON, configExternalID)

		Expect(roles).To(HaveLen(2))
		pre := findRole(roles, "ProductionPreApprover")
		post := findRole(roles, "ProductionPostApprover")
		Expect(pre).ToNot(BeNil())
		Expect(post).ToNot(BeNil())
		Expect(pre.ID).ToNot(Equal(post.ID))

		preAccess := accessesForRoleAlias(access, pre.Aliases[0])
		Expect(preAccess).To(HaveLen(1))
		Expect(preAccess[0].ExternalUserAliases).To(ContainElement("alice@org.com"))
		Expect(preAccess[0].ExternalGroupAliases).To(BeEmpty())

		postAccess := accessesForRoleAlias(access, post.Aliases[0])
		Expect(postAccess).To(HaveLen(1))
		Expect(postAccess[0].ExternalGroupAliases).To(ContainElement("qa-team"))
		Expect(postAccess[0].ExternalUserAliases).To(BeEmpty())
	})

	It("skips automated approvals", func() {
		alice := identityRef("alice@org.com", "Alice", "alice-id")
		defJSON := defJSONWithEnvs(envWithApprovals("Production",
			[]map[string]any{approverMap(alice, true)}, nil))

		roles, access := emitReleaseDefinitionRoles(testCtx(), config, def, defJSON, configExternalID)

		Expect(roles).To(BeEmpty())
		Expect(access).To(BeEmpty())
	})

	It("dedupes duplicate approvers on the same side", func() {
		alice := identityRef("alice@org.com", "Alice", "alice-id")
		defJSON := defJSONWithEnvs(envWithApprovals("Production",
			[]map[string]any{approverMap(alice, false), approverMap(alice, false)}, nil))

		roles, access := emitReleaseDefinitionRoles(testCtx(), config, def, defJSON, configExternalID)

		Expect(roles).To(HaveLen(1))
		Expect(access).To(HaveLen(1))
	})

	It("handles malformed defJSON without panicking", func() {
		cases := []map[string]any{
			nil,
			{},
			{"environments": "not-a-slice"},
			{"environments": []any{"not-a-map"}},
			{"environments": []any{map[string]any{"name": ""}}},
			{"environments": []any{map[string]any{"name": "Prod", "preDeployApprovals": "bad"}}},
			{"environments": []any{map[string]any{"name": "Prod", "preDeployApprovals": map[string]any{"approvals": "bad"}}}},
			{"environments": []any{map[string]any{"name": "Prod", "preDeployApprovals": map[string]any{"approvals": []any{map[string]any{}}}}}},
		}
		for _, c := range cases {
			roles, access := emitReleaseDefinitionRoles(testCtx(), config, def, c, configExternalID)
			Expect(roles).To(BeEmpty())
			Expect(access).To(BeEmpty())
		}
	})

	It("does not emit roles when permissions are disabled (via buildReleaseResult)", func() {
		alice := identityRef("alice@org.com", "Alice", "alice-id")
		defJSON := defJSONWithEnvs(envWithApprovals("Production",
			[]map[string]any{approverMap(alice, false)}, nil))

		createdOn := time.Now().Add(-1 * time.Hour)
		cutoff := createdOn.Add(-1 * time.Hour)
		env := ReleaseEnvironment{ID: 10, Name: "Production", Status: "succeeded"}
		release := makeRelease(100, nil, []ReleaseEnvironment{env}, createdOn)

		disabled := v1.AzureDevops{Organization: "test-org"}
		result := buildReleaseResult(testCtx(), disabled, Project{Name: "MyProject"}, def, defJSON, []Release{release}, cutoff)

		Expect(result.ExternalRoles).To(BeEmpty())
		Expect(result.ConfigAccess).To(BeEmpty())
	})

	It("is wired into buildReleaseResult when permissions are enabled", func() {
		alice := identityRef("alice@org.com", "Alice", "alice-id")
		defJSON := defJSONWithEnvs(envWithApprovals("Production",
			[]map[string]any{approverMap(alice, false)}, nil))

		createdOn := time.Now().Add(-1 * time.Hour)
		cutoff := createdOn.Add(-1 * time.Hour)
		env := ReleaseEnvironment{ID: 10, Name: "Production", Status: "succeeded"}
		release := makeRelease(100, nil, []ReleaseEnvironment{env}, createdOn)

		result := buildReleaseResult(testCtx(), config, Project{Name: "MyProject"}, def, defJSON, []Release{release}, cutoff)

		Expect(result.ExternalRoles).To(HaveLen(1))
		Expect(result.ExternalRoles[0].Name).To(Equal("ProductionApprover"))

		roleAlias := result.ExternalRoles[0].Aliases[0]
		roleAccess := accessesForRoleAlias(result.ConfigAccess, roleAlias)
		Expect(roleAccess).To(HaveLen(1))
		Expect(roleAccess[0].ExternalUserAliases).To(ContainElement("alice@org.com"))
	})
})
