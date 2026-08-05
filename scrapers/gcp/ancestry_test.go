package gcp

import (
	"cloud.google.com/go/asset/apiv1/assetpb"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/structpb"
)

func projectAsset(name string, fields map[string]any) *assetpb.Asset {
	data, err := structpb.NewStruct(fields)
	Expect(err).ToNot(HaveOccurred())
	return &assetpb.Asset{
		Name:      name,
		AssetType: projectAssetType,
		Resource:  &assetpb.Resource{Data: data},
	}
}

var _ = Describe("projectNumberFromAncestors", func() {
	DescribeTable("extracts the owning project number",
		func(ancestors []string, expected string) {
			Expect(projectNumberFromAncestors(ancestors)).To(Equal(expected))
		},
		Entry("full ancestry", []string{"projects/123456789", "folders/5432", "organizations/1234"}, "123456789"),
		Entry("project directly under org", []string{"projects/123456789", "organizations/1234"}, "123456789"),
		Entry("the project asset itself", []string{"projects/123456789"}, "123456789"),
		Entry("folder has no owning project", []string{"folders/5432", "organizations/1234"}, ""),
		Entry("org has no owning project", []string{"organizations/1234"}, ""),
		Entry("no ancestors", nil, ""),
	)
})

var _ = Describe("projectResolver", func() {
	It("maps an asset to the project id of its ancestor project", func() {
		resolver := newProjectResolver("")
		resolver.record(projectAsset(
			"//cloudresourcemanager.googleapis.com/projects/123456789",
			map[string]any{"projectId": "gcp-proj-1", "name": "projects/123456789"},
		))

		Expect(resolver.resolve([]string{"projects/123456789", "organizations/1234"})).To(Equal("gcp-proj-1"))
	})

	It("reads the project number from projectNumber when present", func() {
		resolver := newProjectResolver("")
		resolver.record(projectAsset(
			"//cloudresourcemanager.googleapis.com/projects/whatever",
			map[string]any{"projectId": "gcp-proj-2", "projectNumber": "987654321"},
		))

		Expect(resolver.resolve([]string{"projects/987654321"})).To(Equal("gcp-proj-2"))
	})

	It("falls back to the configured project when the number is unknown", func() {
		resolver := newProjectResolver("gcp-proj-fallback")
		Expect(resolver.resolve([]string{"projects/000000000"})).To(Equal("gcp-proj-fallback"))
	})

	It("falls back to the bare project number when org-scoped and the project asset is missing", func() {
		resolver := newProjectResolver("")
		Expect(resolver.resolve([]string{"projects/123456789"})).To(Equal("123456789"))
	})

	It("returns the fallback when the asset has no ancestor project", func() {
		resolver := newProjectResolver("gcp-proj-fallback")
		Expect(resolver.resolve([]string{"organizations/1234"})).To(Equal("gcp-proj-fallback"))
	})

	It("ignores non-project assets", func() {
		resolver := newProjectResolver("")
		resolver.record(&assetpb.Asset{
			Name:      "//compute.googleapis.com/projects/gcp-proj-1/zones/us-east1-b/instances/vm",
			AssetType: "compute.googleapis.com/Instance",
		})

		Expect(resolver.resolve([]string{"projects/123456789"})).To(Equal("123456789"))
	})
})

var _ = Describe("resourceManagerParent", func() {
	It("links a folder to its parent folder", func() {
		parent, err := resourceManagerParent([]string{"folders/5432", "folders/999", "organizations/1234"})
		Expect(err).ToNot(HaveOccurred())
		Expect(parent).ToNot(BeNil())
		Expect(parent.Type).To(Equal("GCP::ResourceManager::Folder"))
		Expect(parent.ExternalID).To(Equal("//cloudresourcemanager.googleapis.com/folders/999"))
	})

	It("links a folder directly under the organization", func() {
		parent, err := resourceManagerParent([]string{"folders/5432", "organizations/1234"})
		Expect(err).ToNot(HaveOccurred())
		Expect(parent).ToNot(BeNil())
		Expect(parent.Type).To(Equal("GCP::ResourceManager::Organization"))
		Expect(parent.ExternalID).To(Equal("//cloudresourcemanager.googleapis.com/organizations/1234"))
	})

	It("links a project to its parent folder", func() {
		parent, err := resourceManagerParent([]string{"projects/123456789", "folders/5432", "organizations/1234"})
		Expect(err).ToNot(HaveOccurred())
		Expect(parent).ToNot(BeNil())
		Expect(parent.Type).To(Equal("GCP::ResourceManager::Folder"))
		Expect(parent.ExternalID).To(Equal("//cloudresourcemanager.googleapis.com/folders/5432"))
	})

	It("returns no parent for the organization itself", func() {
		parent, err := resourceManagerParent([]string{"organizations/1234"})
		Expect(err).ToNot(HaveOccurred())
		Expect(parent).To(BeNil())
	})

	It("returns no parent when ancestry is missing", func() {
		parent, err := resourceManagerParent(nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(parent).To(BeNil())
	})
})

var _ = Describe("organizationFromAncestors", func() {
	DescribeTable("finds the organization in an ancestry chain",
		func(ancestors []string, expected string) {
			Expect(organizationFromAncestors(ancestors)).To(Equal(expected))
		},
		Entry("full chain", []string{"projects/123", "folders/5432", "organizations/1234"}, "1234"),
		Entry("project directly under org", []string{"projects/123", "organizations/1234"}, "1234"),
		Entry("no organization", []string{"projects/123"}, ""),
		Entry("no ancestors", nil, ""),
	)
})

var _ = Describe("organizationFromAssets", func() {
	It("finds the organization from any listed asset", func() {
		assets := []*assetpb.Asset{
			{Name: "//storage.googleapis.com/b", Ancestors: []string{"projects/123"}},
			{Name: "//compute.googleapis.com/vm", Ancestors: []string{"projects/123", "organizations/1234"}},
		}

		Expect(organizationFromAssets(assets)).To(Equal("1234"))
	})

	It("returns empty when no asset has an organization ancestor", func() {
		assets := []*assetpb.Asset{
			{Name: "//storage.googleapis.com/b", Ancestors: []string{"projects/123"}},
		}

		Expect(organizationFromAssets(assets)).To(BeEmpty())
	})
})

var _ = Describe("isResourceManagerNode", func() {
	DescribeTable("recognises the resource hierarchy asset types",
		func(assetType string, expected bool) {
			Expect(isResourceManagerNode(assetType)).To(Equal(expected))
		},
		Entry("project", "cloudresourcemanager.googleapis.com/Project", true),
		Entry("folder", "cloudresourcemanager.googleapis.com/Folder", true),
		Entry("organization", "cloudresourcemanager.googleapis.com/Organization", true),
		Entry("compute instance", "compute.googleapis.com/Instance", false),
		Entry("apigee organization", "apigee.googleapis.com/Organization", false),
	)
})
