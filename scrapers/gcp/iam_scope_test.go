package gcp

import (
	"fmt"

	"cloud.google.com/go/asset/apiv1/assetpb"
	"cloud.google.com/go/iam/apiv1/iampb"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "github.com/flanksource/config-db/api/v1"
)

var _ = Describe("scopeFor", func() {
	It("tenants and roots roles by organization when one is known", func() {
		scope := scopeFor("projects/gcp-proj-1", "1234")
		Expect(scope.Tenant).To(Equal("1234"))
		Expect(scope.Root).To(Equal(v1.ConfigExternalKey{
			Type:       "GCP::Organization",
			ExternalID: "//cloudresourcemanager.googleapis.com/organizations/1234",
		}))
	})

	It("falls back to the project when no organization is known", func() {
		scope := scopeFor("projects/gcp-proj-1", "")
		Expect(scope.Tenant).To(Equal("gcp-proj-1"))
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

var _ = Describe("fetchResourceManagerHierarchy", func() {
	It("rejects unsupported roots before creating an API client", func() {
		_, err := fetchResourceManagerHierarchy(&GCPContext{}, v1.GCP{}, "folders/1234")
		Expect(err).To(MatchError(`unsupported GCP resource hierarchy root "folders/1234"`))
	})
})

var _ = Describe("newRoleConfig", func() {
	orgScope := scopeFor("projects/gcp-proj-1", "1234")

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
			Type:       "GCP::Organization",
			ExternalID: "//cloudresourcemanager.googleapis.com/organizations/9999",
		}))
		Expect(role.Config).To(HaveKeyWithValue("organization", "9999"))
	})

	It("parents a predefined role consistently to the organization", func() {
		role := newRoleConfig("roles/storage.admin", orgScope)
		Expect(role.Parents).To(ConsistOf(orgScope.Root))
		Expect(role.Config).ToNot(HaveKey("project"))
		Expect(role.Config).ToNot(HaveKey("organization"))
	})

	It("never emits an empty parent external id", func() {
		for _, id := range []string{"roles/viewer", "organizations/1234/roles/x", "projects/p/roles/y"} {
			role := newRoleConfig(id, orgScope)
			for _, parent := range role.Parents {
				Expect(parent.ExternalID).ToNot(BeEmpty(), "role %s produced a dangling parent", id)
			}
		}
	})
})

var _ = Describe("coalesceIAMRoleConfigs", func() {
	It("merges a predefined role emitted by multiple project roots", func() {
		scope := scopeFor("projects/gcp-proj-1", "1234")
		roleA := newRoleConfig("roles/viewer", scope)
		roleA.RelationshipResults = append(roleA.RelationshipResults, v1.RelationshipResult{
			ConfigExternalID:  v1.ExternalID{ConfigType: v1.IAMRole, ExternalID: "roles/viewer"},
			RelatedExternalID: v1.ExternalID{ConfigType: v1.GCSBucket, ExternalID: "bucket-a"},
			Relationship:      "IAMBinding",
		})
		roleB := newRoleConfig("roles/viewer", scope)
		roleB.Config.(map[string]any)["title"] = "Viewer"
		roleB.RelationshipResults = append(roleB.RelationshipResults, v1.RelationshipResult{
			ConfigExternalID:  v1.ExternalID{ConfigType: v1.IAMRole, ExternalID: "roles/viewer"},
			RelatedExternalID: v1.ExternalID{ConfigType: v1.GCSBucket, ExternalID: "bucket-b"},
			Relationship:      "IAMBinding",
		})

		results := coalesceIAMRoleConfigs(v1.ScrapeResults{roleA, roleB})

		Expect(results).To(HaveLen(1))
		Expect(results[0].Parents).To(ConsistOf(scope.Root))
		Expect(results[0].RelationshipResults).To(HaveLen(2))
		Expect(results[0].Config).To(HaveKeyWithValue("title", "Viewer"))
		Expect(roleA.Config).ToNot(HaveKey("title"), "coalescing must not mutate its input maps")
	})

	It("leaves unrelated and error results untouched", func() {
		errResult := v1.ScrapeResult{Error: fmt.Errorf("failed")}
		bucket := v1.ScrapeResult{ID: "bucket", Type: v1.GCSBucket}

		Expect(coalesceIAMRoleConfigs(v1.ScrapeResults{errResult, bucket})).To(Equal(
			v1.ScrapeResults{errResult, bucket},
		))
	})
})

var _ = Describe("buildIAMAccess tenant", func() {
	It("stamps the organization tenant on every external identity", func() {
		scope := scopeFor("projects/gcp-proj-1", "1234")

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
