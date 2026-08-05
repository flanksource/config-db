package gcp

import (
	"errors"
	"fmt"
	"testing"

	"cloud.google.com/go/asset/apiv1/assetpb"
	"cloud.google.com/go/iam/apiv1/iampb"
	"github.com/flanksource/config-db/api"
	dutyCtx "github.com/flanksource/duty/context"
	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/samber/lo"
	cloudidentity "google.golang.org/api/cloudidentity/v1"
	cloudresourcemanager "google.golang.org/api/cloudresourcemanager/v3"
	expr "google.golang.org/genproto/googleapis/type/expr"

	v1 "github.com/flanksource/config-db/api/v1"
)

func TestParseGCPMember(t *testing.T) {
	g := gomega.NewWithT(t)

	tests := []struct {
		member  string
		typ     string
		alias   string
		isGroup bool
		found   bool
	}{
		{"user:alice@example.com", "User", "alice@example.com", false, true},
		{"serviceAccount:sa@p.iam.gserviceaccount.com", "ServiceAccount", "sa@p.iam.gserviceaccount.com", false, true},
		{"group:admins@example.com", "Group", "admins@example.com", true, true},
		{"domain:example.com", "Domain", "domain:example.com", true, true},
		{"allUsers", "AllUsers", "allUsers", true, true},
		{"allAuthenticatedUsers", "AllAuthenticatedUsers", "allAuthenticatedUsers", true, true},
		{"deleted:user:bob@example.com?uid=123", "", "", false, false},
		{"", "", "", false, false},
	}

	for _, tc := range tests {
		p, found := parseGCPMember(tc.member)
		g.Expect(found).To(gomega.Equal(tc.found), "found mismatch for %q", tc.member)
		if !tc.found {
			continue
		}
		g.Expect(p.Type).To(gomega.Equal(tc.typ), "type mismatch for %q", tc.member)
		g.Expect(p.Alias).To(gomega.Equal(tc.alias), "alias mismatch for %q", tc.member)
		g.Expect(p.IsGroup).To(gomega.Equal(tc.isGroup), "isGroup mismatch for %q", tc.member)
	}
}

func TestRoleScope(t *testing.T) {
	g := gomega.NewWithT(t)

	tests := []struct {
		role  string
		scope string
	}{
		{"roles/owner", "predefined"},
		{"roles/storage.admin", "predefined"},
		{"projects/my-project/roles/customViewer", "project"},
		{"organizations/1234567890/roles/customAuditor", "organization"},
		{"billingAccounts/ABCD/roles/nope", "unknown"},
		{"", "unknown"},
	}

	for _, tc := range tests {
		g.Expect(roleScope(tc.role)).To(gomega.Equal(tc.scope), "scope mismatch for %q", tc.role)
	}
}

func member(email, typ string) *cloudidentity.Membership {
	return &cloudidentity.Membership{
		PreferredMemberKey: &cloudidentity.EntityKey{Id: email},
		Type:               typ,
	}
}

func TestTraverseGroup(t *testing.T) {
	g := gomega.NewWithT(t)

	// admins -> {alice (user), sa (service account), devs (nested group)}
	// devs   -> {bob (user), admins (cycle back)}
	directory := map[string][]*cloudidentity.Membership{
		"admins@example.com": {
			member("alice@example.com", "USER"),
			member("sa@p.iam.gserviceaccount.com", "SERVICE_ACCOUNT"),
			member("devs@example.com", "GROUP"),
		},
		"devs@example.com": {
			member("bob@example.com", "USER"),
			member("admins@example.com", "GROUP"), // cycle
		},
	}

	fetchCount := map[string]int{}
	fetch := func(email string) ([]*cloudidentity.Membership, error) {
		fetchCount[email]++
		m, ok := directory[email]
		if !ok {
			return nil, fmt.Errorf("group %s not found", email)
		}
		return m, nil
	}

	var warnings []string
	warn := func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) }

	leaves, nested, err := traverseGroup("admins@example.com", fetch, warn)
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// Transitive leaves are flattened onto the root; bob (in nested devs) is included.
	got := map[string]string{}
	for _, l := range leaves {
		got[l.email] = l.userType
	}
	g.Expect(got).To(gomega.Equal(map[string]string{
		"alice@example.com":            "User",
		"sa@p.iam.gserviceaccount.com": "ServiceAccount",
		"bob@example.com":              "User",
	}))

	g.Expect(nested).To(gomega.ContainElement("devs@example.com"))
	// The cycle back to the root must not trigger a re-fetch (visited-set guard).
	g.Expect(fetchCount["admins@example.com"]).To(gomega.Equal(1))
	g.Expect(fetchCount["devs@example.com"]).To(gomega.Equal(1))
	g.Expect(warnings).To(gomega.BeEmpty())
}

func TestTraverseGroupNestedFetchError(t *testing.T) {
	g := gomega.NewWithT(t)

	directory := map[string][]*cloudidentity.Membership{
		"team@example.com": {
			member("carol@example.com", "USER"),
			member("ghost@example.com", "GROUP"), // fetch will fail
		},
	}
	fetch := func(email string) ([]*cloudidentity.Membership, error) {
		if m, ok := directory[email]; ok {
			return m, nil
		}
		return nil, fmt.Errorf("group %s not found", email)
	}
	var warnings []string
	warn := func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) }

	leaves, nested, err := traverseGroup("team@example.com", fetch, warn)

	// A nested-group failure is tolerated: the reachable member still resolves.
	g.Expect(err).ToNot(gomega.HaveOccurred())
	g.Expect(leaves).To(gomega.HaveLen(1))
	g.Expect(leaves[0].email).To(gomega.Equal("carol@example.com"))
	g.Expect(nested).To(gomega.ContainElement("ghost@example.com"))
	g.Expect(warnings).To(gomega.HaveLen(1))

	// The root group failing to resolve propagates as an error.
	_, _, err = traverseGroup("missing@example.com", fetch, warn)
	g.Expect(err).To(gomega.HaveOccurred())
}

// projectIAMScope is the scope of a project-rooted scrape with no reachable
// organization, which is what most of these tests exercise.
func projectIAMScope(project string) iamScope {
	return scopeFor(v1.ProjectPrefix+project, "")
}

func iamPolicyAsset(name, assetType string, bindings ...*iampb.Binding) *assetpb.Asset {
	return &assetpb.Asset{
		Name:      name,
		AssetType: assetType,
		IamPolicy: &iampb.Policy{Bindings: bindings},
	}
}

// findRoleConfig returns the GCP::IAMRole ScrapeResult whose external id equals role.
func findRoleConfig(res []v1.ScrapeResult, role string) *v1.ScrapeResult {
	for i := range res {
		if res[i].Type == v1.IAMRole && res[i].ID == role {
			return &res[i]
		}
	}
	return nil
}

// accessKeyed returns the access row for (resource, principalAlias, role),
// matching either the user- or group-alias slot.
func accessKeyed(accesses []v1.ExternalConfigAccess, resource v1.ExternalID, principalAlias, role string) *v1.ExternalConfigAccess {
	for i := range accesses {
		a := &accesses[i]
		if a.ConfigExternalID.ConfigType != resource.ConfigType ||
			a.ConfigExternalID.ExternalID != resource.ExternalID ||
			len(a.ExternalRoleAliases) != 1 ||
			a.ExternalRoleAliases[0] != role {
			continue
		}
		if len(a.ExternalUserAliases) == 1 && a.ExternalUserAliases[0] == principalAlias {
			return a
		}
		if len(a.ExternalGroupAliases) == 1 && a.ExternalGroupAliases[0] == principalAlias {
			return a
		}
	}
	return nil
}

var _ = Describe("buildIAMAccess", func() {
	It("keeps one config access per GCP resource, principal, and role", func() {
		const (
			project   = "my-project"
			principal = "worker@my-project.iam.gserviceaccount.com"
			role      = "roles/viewer"
		)
		projectName := "//cloudresourcemanager.googleapis.com/projects/my-project"
		bucketName := "//storage.googleapis.com/projects/_/buckets/my-bucket"

		result := buildIAMAccess([]*assetpb.Asset{
			iamPolicyAsset(projectName, "cloudresourcemanager.googleapis.com/Project",
				&iampb.Binding{Role: role, Members: []string{"serviceAccount:" + principal}},
			),
			iamPolicyAsset(bucketName, "storage.googleapis.com/Bucket",
				&iampb.Binding{Role: role, Members: []string{
					"serviceAccount:" + principal,
					"serviceAccount:" + principal,
				}},
			),
		}, projectIAMScope(project))

		source := iamPolicySource
		gomega.Expect(result.Access).To(gomega.ConsistOf(
			v1.ExternalConfigAccess{
				ConfigExternalID:    v1.ExternalID{ConfigType: "GCP::ResourceManager::Project", ExternalID: projectName},
				ExternalUserAliases: []string{principal},
				ExternalRoleAliases: []string{role},
				Source:              &source,
			},
			v1.ExternalConfigAccess{
				ConfigExternalID:    v1.ExternalID{ConfigType: v1.GCSBucket, ExternalID: bucketName},
				ExternalUserAliases: []string{principal},
				ExternalRoleAliases: []string{role},
				Source:              &source,
			},
		))
	})

	It("omits conditional bindings from effective access", func() {
		result := buildIAMAccess([]*assetpb.Asset{
			iamPolicyAsset("//storage.googleapis.com/projects/_/buckets/conditional", "storage.googleapis.com/Bucket",
				&iampb.Binding{
					Role:      "roles/storage.objectViewer",
					Members:   []string{"user:alice@example.com", "group:admins@example.com"},
					Condition: &expr.Expr{Expression: "request.time < timestamp('2030-01-01T00:00:00Z')"},
				},
			),
		}, projectIAMScope("my-project"))

		gomega.Expect(result.SkippedConditionalBindings).To(gomega.Equal(1))
		gomega.Expect(result.RoleConfigs).To(gomega.HaveLen(1), "the role definition remains discoverable")
		gomega.Expect(result.Roles).To(gomega.HaveLen(1))
		gomega.Expect(result.RoleConfigs[0].RelationshipResults).To(gomega.BeEmpty())
		gomega.Expect(result.Access).To(gomega.BeEmpty())
		gomega.Expect(result.Users).To(gomega.BeEmpty())
		gomega.Expect(result.Groups).To(gomega.BeEmpty())
		gomega.Expect(result.GroupEmails).To(gomega.BeEmpty())
	})

	It("still emits an unconditional sibling after skipping the same conditional tuple", func() {
		conditional := &iampb.Binding{
			Role:      "roles/viewer",
			Members:   []string{"user:alice@example.com"},
			Condition: &expr.Expr{Expression: "resource.name.startsWith('projects/prefix')"},
		}
		unconditional := &iampb.Binding{
			Role:    "roles/viewer",
			Members: []string{"user:alice@example.com"},
		}

		result := buildIAMAccess([]*assetpb.Asset{
			iamPolicyAsset("//storage.googleapis.com/projects/_/buckets/mixed", "storage.googleapis.com/Bucket",
				conditional, unconditional,
			),
		}, projectIAMScope("my-project"))

		gomega.Expect(result.SkippedConditionalBindings).To(gomega.Equal(1))
		gomega.Expect(result.Access).To(gomega.HaveLen(1))
		gomega.Expect(result.Users).To(gomega.HaveLen(1))
		gomega.Expect(result.RoleConfigs).To(gomega.HaveLen(1))
		gomega.Expect(result.RoleConfigs[0].RelationshipResults).To(gomega.HaveLen(1))
	})
})

var _ = Describe("fetch IAM policies", func() {
	It("reports omitted conditional bindings as a bounded warning", func() {
		ctx := &GCPContext{ScrapeContext: api.NewScrapeContext(dutyCtx.New())}
		iamPolicy, err := (Scraper{}).fetchIAMPolicies(ctx, v1.GCP{Project: "app-prod"}, "projects/app-prod", iamPolicyFetchers{
			listAssets: func(*GCPContext, v1.GCP, string) ([]*assetpb.Asset, error) {
				return []*assetpb.Asset{iamPolicyAsset(
					"//storage.googleapis.com/projects/_/buckets/conditional",
					"storage.googleapis.com/Bucket",
					&iampb.Binding{
						Role:      "roles/viewer",
						Members:   []string{"user:alice@example.com"},
						Condition: &expr.Expr{Title: "temporary", Expression: "request.time < timestamp('2030-01-01T00:00:00Z')"},
					},
				)}, nil
			},
			fetchHierarchy: func(*GCPContext, v1.GCP, string) (resourceHierarchy, error) {
				return resourceHierarchy{Project: &cloudresourcemanager.Project{Name: "projects/123", ProjectId: "app-prod"}}, nil
			},
			enrichRoles: func(*GCPContext, []v1.ScrapeResult) {},
		})

		gomega.Expect(err).ToNot(gomega.HaveOccurred())
		var warnings []v1.Warning
		var accesses []v1.ExternalConfigAccess
		for _, result := range iamPolicy.Results {
			warnings = append(warnings, result.Warnings...)
			accesses = append(accesses, result.ConfigAccess...)
		}
		gomega.Expect(accesses).To(gomega.BeEmpty())
		gomega.Expect(warnings).To(gomega.ConsistOf(v1.Warning{
			Error: "conditional GCP IAM bindings cannot be modeled and were omitted from effective access",
			Count: 1,
		}))
	})

	DescribeTable("preserves asset policies when hierarchy discovery is unavailable",
		func(project *cloudresourcemanager.Project, hierarchyErr error) {
			const (
				projectID = "app-prod"
				bucketID  = "//storage.googleapis.com/projects/_/buckets/audit-logs"
				role      = "roles/storage.objectViewer"
				principal = "alice@example.com"
			)

			ctx := &GCPContext{ScrapeContext: api.NewScrapeContext(dutyCtx.New())}
			iamPolicy, err := (Scraper{}).fetchIAMPolicies(ctx, v1.GCP{Project: projectID}, v1.ProjectPrefix+projectID, iamPolicyFetchers{
				listAssets: func(*GCPContext, v1.GCP, string) ([]*assetpb.Asset, error) {
					return []*assetpb.Asset{iamPolicyAsset(
						bucketID,
						"storage.googleapis.com/Bucket",
						&iampb.Binding{Role: role, Members: []string{"user:" + principal}},
					)}, nil
				},
				fetchHierarchy: func(*GCPContext, v1.GCP, string) (resourceHierarchy, error) {
					return resourceHierarchy{Project: project}, hierarchyErr
				},
				enrichRoles: func(*GCPContext, []v1.ScrapeResult) {},
			})

			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			gomega.Expect(iamPolicy.GroupEmails).To(gomega.BeEmpty())
			// No organization is reachable here, so the tenant stays the project.
			gomega.Expect(iamPolicy.Scope.Tenant).To(gomega.Equal(projectID))

			// An unreadable hierarchy is surfaced as a scraper error, not just a log
			// line, because it silently costs the organization and folder configs.
			hierarchyErrors := lo.Filter(iamPolicy.Results, func(r v1.ScrapeResult, _ int) bool {
				return r.Error != nil
			})
			gomega.Expect(hierarchyErrors).To(gomega.HaveLen(1))
			gomega.Expect(hierarchyErrors[0].Error.Error()).To(gomega.ContainSubstring("resource hierarchy"))

			source := iamPolicySource
			accesses := lo.FlatMap(iamPolicy.Results, func(r v1.ScrapeResult, _ int) []v1.ExternalConfigAccess {
				return r.ConfigAccess
			})
			gomega.Expect(accesses).To(gomega.Equal([]v1.ExternalConfigAccess{{
				ConfigExternalID:    v1.ExternalID{ConfigType: v1.GCSBucket, ExternalID: bucketID},
				ExternalUserAliases: []string{principal},
				ExternalRoleAliases: []string{role},
				Source:              &source,
			}}))
		},
		Entry("hierarchy fetch fails", nil, errors.New("permission denied")),
		Entry("hierarchy response is invalid", &cloudresourcemanager.Project{}, nil),
	)
})

var _ = Describe("buildResourceManagerHierarchy", func() {
	It("emits the project ancestor chain and resource-scoped IAM policies", func() {
		const (
			projectID     = "app-prod"
			projectNumber = "123456789"
			folderID      = "456789123"
			organization  = "789123456"
			principal     = "moshe@example.com"
		)

		project := &cloudresourcemanager.Project{
			Name:      "projects/" + projectNumber,
			ProjectId: projectID,
			Parent:    "folders/" + folderID,
		}
		nodes := []resourceManagerNode{
			{
				Resource: &cloudresourcemanager.Folder{
					Name:        "folders/" + folderID,
					DisplayName: "Production",
					Parent:      "organizations/" + organization,
					State:       "ACTIVE",
				},
				Policy: &cloudresourcemanager.Policy{Bindings: []*cloudresourcemanager.Binding{
					{Role: "roles/resourcemanager.folderViewer", Members: []string{"user:" + principal}},
				}},
			},
			{
				Resource: &cloudresourcemanager.Organization{
					Name:        "organizations/" + organization,
					DisplayName: "example.com",
					State:       "ACTIVE",
				},
				Policy: &cloudresourcemanager.Policy{Bindings: []*cloudresourcemanager.Binding{
					{Role: "roles/resourcemanager.organizationAdmin", Members: []string{"user:" + principal}},
				}},
			},
		}

		configs, policies, err := buildResourceManagerHierarchy(project, nodes, v1.BaseScraper{})
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
		gomega.Expect(configs).To(gomega.HaveLen(2))
		gomega.Expect(policies).To(gomega.HaveLen(2))

		folderExternalID := "//cloudresourcemanager.googleapis.com/folders/" + folderID
		organizationExternalID := "//cloudresourcemanager.googleapis.com/organizations/" + organization

		gomega.Expect(configs[0].ID).To(gomega.Equal("folders/" + folderID))
		gomega.Expect(configs[0].Name).To(gomega.Equal("Production"))
		gomega.Expect(configs[0].Type).To(gomega.Equal("GCP::ResourceManager::Folder"))
		gomega.Expect(configs[0].Aliases).To(gomega.Equal([]string{folderExternalID}))
		gomega.Expect(configs[0].Parents).To(gomega.Equal([]v1.ConfigExternalKey{{
			Type:       "GCP::ResourceManager::Organization",
			ExternalID: organizationExternalID,
		}}))
		gomega.Expect(configs[0].Children).To(gomega.Equal([]v1.ConfigExternalKey{{
			Type:       "GCP::ResourceManager::Project",
			ExternalID: "//cloudresourcemanager.googleapis.com/projects/" + projectNumber,
		}}))
		gomega.Expect(configs[1].ID).To(gomega.Equal("organizations/" + organization))
		gomega.Expect(configs[1].Name).To(gomega.Equal("example.com"))
		gomega.Expect(configs[1].Type).To(gomega.Equal("GCP::ResourceManager::Organization"))
		gomega.Expect(configs[1].Aliases).To(gomega.Equal([]string{organizationExternalID}))
		gomega.Expect(configs[1].Children).To(gomega.BeEmpty())

		access := buildIAMAccess(policies, projectIAMScope(projectID))
		gomega.Expect(accessKeyed(access.Access,
			v1.ExternalID{ConfigType: "GCP::ResourceManager::Folder", ExternalID: folderExternalID},
			principal, "roles/resourcemanager.folderViewer",
		)).ToNot(gomega.BeNil())
		gomega.Expect(accessKeyed(access.Access,
			v1.ExternalID{ConfigType: "GCP::ResourceManager::Organization", ExternalID: organizationExternalID},
			principal, "roles/resourcemanager.organizationAdmin",
		)).ToNot(gomega.BeNil())
	})

	It("preserves IAM conditions while converting Resource Manager policies", func() {
		policy := resourceManagerIAMPolicy(&cloudresourcemanager.Policy{
			Version: 3,
			Bindings: []*cloudresourcemanager.Binding{{
				Role:    "roles/viewer",
				Members: []string{"user:alice@example.com"},
				Condition: &cloudresourcemanager.Expr{
					Title:       "temporary",
					Description: "expires at the end of the migration",
					Expression:  "request.time < timestamp('2030-01-01T00:00:00Z')",
					Location:    "policy.yaml:12",
				},
			}},
		})

		gomega.Expect(policy.Version).To(gomega.Equal(int32(3)))
		gomega.Expect(policy.Bindings).To(gomega.HaveLen(1))
		gomega.Expect(policy.Bindings[0].Condition).ToNot(gomega.BeNil())
		gomega.Expect(policy.Bindings[0].Condition.Title).To(gomega.Equal("temporary"))
		gomega.Expect(policy.Bindings[0].Condition.Description).To(gomega.Equal("expires at the end of the migration"))
		gomega.Expect(policy.Bindings[0].Condition.Expression).To(gomega.Equal("request.time < timestamp('2030-01-01T00:00:00Z')"))
		gomega.Expect(policy.Bindings[0].Condition.Location).To(gomega.Equal("policy.yaml:12"))

		access := buildIAMAccess([]*assetpb.Asset{{
			Name:      "//cloudresourcemanager.googleapis.com/organizations/1234",
			AssetType: organizationAssetType,
			IamPolicy: policy,
		}}, scopeFor("organizations/1234", "1234"))
		gomega.Expect(access.SkippedConditionalBindings).To(gomega.Equal(1))
		gomega.Expect(access.Access).To(gomega.BeEmpty())
	})
})

func TestBuildIAMAccess(t *testing.T) {
	g := gomega.NewWithT(t)

	const project = "my-project"
	const (
		roleOwner   = "roles/owner"
		roleStorage = "roles/storage.admin"
		roleCustom  = "projects/my-project/roles/customViewer"
	)
	projectName := "//cloudresourcemanager.googleapis.com/projects/my-project"
	bucketName := "//storage.googleapis.com/projects/_/buckets/my-bucket"
	projectResource := v1.ExternalID{ConfigType: "GCP::ResourceManager::Project", ExternalID: projectName}
	bucketResource := v1.ExternalID{ConfigType: v1.GCSBucket, ExternalID: bucketName}

	assets := []*assetpb.Asset{
		iamPolicyAsset(projectName, "cloudresourcemanager.googleapis.com/Project",
			&iampb.Binding{Role: roleOwner, Members: []string{
				"user:alice@example.com",
				"group:admins@example.com",
				"domain:example.com",
			}},
			&iampb.Binding{Role: roleStorage, Members: []string{
				"user:alice@example.com",
				"serviceAccount:sa@my-project.iam.gserviceaccount.com",
			}},
			&iampb.Binding{Role: roleCustom, Members: []string{
				"user:carol@example.com",
			}},
		),
		iamPolicyAsset(bucketName, "storage.googleapis.com/Bucket",
			// alice+storage.admin repeats across resources -> separate access row.
			&iampb.Binding{Role: roleStorage, Members: []string{
				"user:alice@example.com",
			}},
		),
	}

	res := buildIAMAccess(assets, projectIAMScope(project))

	// One GCP::IAMRole config item per distinct bound role.
	g.Expect(res.RoleConfigs).To(gomega.HaveLen(3), "expected one config item per distinct role")
	for _, role := range []string{roleOwner, roleStorage, roleCustom} {
		rc := findRoleConfig(res.RoleConfigs, role)
		g.Expect(rc).ToNot(gomega.BeNil(), "missing role config for %s", role)
		g.Expect(rc.Config).ToNot(gomega.BeNil(), "role config %s must carry a config body", role)
		g.Expect(rc.Aliases).To(gomega.ContainElement(role))
		g.Expect(rc.Parents).To(gomega.ContainElement(
			v1.ConfigExternalKey{Type: v1.GCPProject, ExternalID: project},
		))
	}

	// storage.admin is bound on the project and the bucket -> 2 deduped edges.
	storageRC := findRoleConfig(res.RoleConfigs, roleStorage)
	g.Expect(storageRC.RelationshipResults).To(gomega.HaveLen(2))
	relatedTypes := map[string]string{}
	for _, rel := range storageRC.RelationshipResults {
		g.Expect(rel.ConfigExternalID).To(gomega.Equal(v1.ExternalID{ConfigType: v1.IAMRole, ExternalID: roleStorage}))
		g.Expect(rel.Relationship).To(gomega.Equal("IAMBinding"))
		relatedTypes[rel.RelatedExternalID.ExternalID] = rel.RelatedExternalID.ConfigType
	}
	g.Expect(relatedTypes[bucketName]).To(gomega.Equal(v1.GCSBucket))
	g.Expect(relatedTypes[projectName]).To(gomega.Equal("GCP::ResourceManager::Project"))

	// owner bound only on the project -> single edge.
	ownerRC := findRoleConfig(res.RoleConfigs, roleOwner)
	g.Expect(ownerRC.RelationshipResults).To(gomega.HaveLen(1))

	// Exactly one access row per (resource, principal, role).
	g.Expect(res.Access).To(gomega.HaveLen(7))
	for _, tc := range []struct {
		resource        v1.ExternalID
		principal, role string
	}{
		{projectResource, "alice@example.com", roleOwner},
		{projectResource, "admins@example.com", roleOwner},
		{projectResource, "domain:example.com", roleOwner},
		{projectResource, "alice@example.com", roleStorage},
		{projectResource, "sa@my-project.iam.gserviceaccount.com", roleStorage},
		{projectResource, "carol@example.com", roleCustom},
		{bucketResource, "alice@example.com", roleStorage},
	} {
		a := accessKeyed(res.Access, tc.resource, tc.principal, tc.role)
		g.Expect(a).ToNot(gomega.BeNil(), "missing access (%s -> %s on %s)", tc.principal, tc.role, tc.resource.ExternalID)
		g.Expect(a.ExternalRoleAliases).To(gomega.Equal([]string{tc.role}))
		g.Expect(a.ConfigExternalID).To(gomega.Equal(tc.resource))
	}

	// group: and domain: land as ExternalGroups, not ExternalUsers.
	groupAliases := map[string]string{}
	for _, gr := range res.Groups {
		groupAliases[gr.Aliases[0]] = gr.GroupType
	}
	g.Expect(groupAliases).To(gomega.HaveKeyWithValue("admins@example.com", "Group"))
	g.Expect(groupAliases).To(gomega.HaveKeyWithValue("domain:example.com", "Domain"))

	userAliases := map[string]string{}
	for _, u := range res.Users {
		userAliases[u.Aliases[0]] = u.UserType
	}
	g.Expect(userAliases).To(gomega.HaveKeyWithValue("alice@example.com", "User"))
	g.Expect(userAliases).To(gomega.HaveKeyWithValue("sa@my-project.iam.gserviceaccount.com", "ServiceAccount"))
	g.Expect(userAliases).To(gomega.HaveKeyWithValue("carol@example.com", "User"))
	g.Expect(userAliases).ToNot(gomega.HaveKey("admins@example.com"))

	// Only real Google groups are candidates for membership expansion.
	g.Expect(res.GroupEmails).To(gomega.ConsistOf("admins@example.com"))
}
