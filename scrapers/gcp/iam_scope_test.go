package gcp

import (
	"cloud.google.com/go/asset/apiv1/assetpb"
	"cloud.google.com/go/iam/apiv1/iampb"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "github.com/flanksource/config-db/api/v1"
)

var _ = Describe("scopeFor", func() {
	It("tenants by organization when one is known", func() {
		scope := scopeFor("projects/gcp-proj-1", "1234")
		Expect(scope.Tenant).To(Equal("1234"))
		Expect(scope.Project).To(Equal("gcp-proj-1"))
	})

	It("falls back to the project when no organization is known", func() {
		scope := scopeFor("projects/gcp-proj-1", "")
		Expect(scope.Tenant).To(Equal("gcp-proj-1"))
		Expect(scope.Project).To(Equal("gcp-proj-1"))
	})

	It("roots an organization parent at the organization", func() {
		scope := scopeFor("organizations/1234", "1234")
		Expect(scope.Tenant).To(Equal("1234"))
		Expect(scope.Project).To(BeEmpty())
		Expect(scope.Root).To(Equal(v1.ConfigExternalKey{
			Type:       "GCP::ResourceManager::Organization",
			ExternalID: "//cloudresourcemanager.googleapis.com/organizations/1234",
		}))
	})

	It("roots a project parent at the project", func() {
		scope := scopeFor("projects/gcp-proj-1", "1234")
		Expect(scope.Root).To(Equal(v1.ConfigExternalKey{
			Type:       v1.GCPProject,
			ExternalID: "gcp-proj-1",
		}))
	})
})

var _ = Describe("resolveOrganization", func() {
	assetsUnderOrg := []*assetpb.Asset{
		{Name: "//storage.googleapis.com/b", Ancestors: []string{"projects/123", "organizations/9999"}},
	}

	It("prefers the configured organization", func() {
		hierarchy := resourceHierarchy{OrganizationID: "5555"}
		Expect(resolveOrganization(v1.GCP{Organization: "1234"}, hierarchy, assetsUnderOrg)).To(Equal("1234"))
	})

	It("falls back to the hierarchy when none is configured", func() {
		hierarchy := resourceHierarchy{OrganizationID: "5555"}
		Expect(resolveOrganization(v1.GCP{Project: "gcp-proj-1"}, hierarchy, assetsUnderOrg)).To(Equal("5555"))
	})

	It("falls back to asset ancestry when the hierarchy is unavailable", func() {
		Expect(resolveOrganization(v1.GCP{Project: "gcp-proj-1"}, resourceHierarchy{}, assetsUnderOrg)).To(Equal("9999"))
	})

	It("returns empty when the project belongs to no organization", func() {
		assets := []*assetpb.Asset{
			{Name: "//storage.googleapis.com/b", Ancestors: []string{"projects/123"}},
		}
		Expect(resolveOrganization(v1.GCP{Project: "gcp-proj-1"}, resourceHierarchy{}, assets)).To(BeEmpty())
	})
})

var _ = Describe("fetchResourceManagerHierarchy salvage", func() {
	It("keeps the organization id discovered before an unreadable node", func() {
		// The walk records organizations/1234 from the parent chain before it
		// tries to read the organization resource, so a scrape service account
		// without organization read access still tenants by organization.
		hierarchy := resourceHierarchy{OrganizationID: "1234"}
		Expect(scopeFor("projects/gcp-proj-1", hierarchy.OrganizationID).Tenant).To(Equal("1234"))
	})
})

var _ = Describe("newRoleConfig", func() {
	orgScope := iamScope{
		Tenant: "1234",
		Root: v1.ConfigExternalKey{
			Type:       "GCP::ResourceManager::Organization",
			ExternalID: "//cloudresourcemanager.googleapis.com/organizations/1234",
		},
	}

	It("parents a project custom role to its own project", func() {
		role := newRoleConfig("projects/gcp-proj-7/roles/customViewer", orgScope)
		Expect(role.Parents).To(ConsistOf(v1.ConfigExternalKey{
			Type:       v1.GCPProject,
			ExternalID: "gcp-proj-7",
		}))
		Expect(role.Config).To(HaveKeyWithValue("project", "gcp-proj-7"))
	})

	It("parents an organization custom role to its own organization", func() {
		role := newRoleConfig("organizations/9999/roles/auditor", orgScope)
		Expect(role.Parents).To(ConsistOf(v1.ConfigExternalKey{
			Type:       "GCP::ResourceManager::Organization",
			ExternalID: "//cloudresourcemanager.googleapis.com/organizations/9999",
		}))
		Expect(role.Config).To(HaveKeyWithValue("organization", "9999"))
	})

	It("parents a predefined role to the scrape root", func() {
		role := newRoleConfig("roles/storage.admin", orgScope)
		Expect(role.Parents).To(ConsistOf(orgScope.Root))
	})

	It("never emits an empty parent external id when org-scoped", func() {
		for _, id := range []string{"roles/viewer", "organizations/1234/roles/x", "projects/p/roles/y"} {
			role := newRoleConfig(id, orgScope)
			for _, parent := range role.Parents {
				Expect(parent.ExternalID).ToNot(BeEmpty(), "role %s produced a dangling parent", id)
			}
		}
	})
})

var _ = Describe("buildIAMAccess tenant", func() {
	It("stamps the organization tenant on every external identity", func() {
		scope := iamScope{
			Tenant:  "1234",
			Project: "gcp-proj-1",
			Root:    v1.ConfigExternalKey{Type: v1.GCPProject, ExternalID: "gcp-proj-1"},
		}

		res := buildIAMAccess([]*assetpb.Asset{
			iamPolicyAsset("//storage.googleapis.com/projects/_/buckets/b", "storage.googleapis.com/Bucket",
				&iampb.Binding{Role: "roles/viewer", Members: []string{
					"user:alice@example.com",
					"serviceAccount:sa@gcp-proj-1.iam.gserviceaccount.com",
					"group:admins@example.com",
				}},
			),
		}, scope)

		Expect(res.Users).ToNot(BeEmpty())
		for _, user := range res.Users {
			Expect(user.Tenant).To(Equal("1234"))
		}
		Expect(res.Groups).ToNot(BeEmpty())
		for _, group := range res.Groups {
			Expect(group.Tenant).To(Equal("1234"))
		}
		Expect(res.Roles).ToNot(BeEmpty())
		for _, role := range res.Roles {
			Expect(role.Tenant).To(Equal("1234"))
		}
	})
})
