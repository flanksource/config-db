package v1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GCP", func() {
	DescribeTable("Includes",
		func(config GCP, feature string, expected bool) {
			Expect(config.Includes(feature)).To(Equal(expected))
		},
		Entry("empty include list - audit logs enabled for backwards compatibility",
			GCP{}, IncludeAuditLogs, true),
		Entry("empty include list - IAM policy enabled for backwards compatibility",
			GCP{}, IncludeIAMPolicy, true),
		Entry("empty include list - group membership expansion enabled by default",
			GCP{}, IncludeGroupMembers, true),
		Entry("explicit asset-type include disables default group membership",
			GCP{Include: []string{"storage.googleapis.com/Bucket"}}, IncludeGroupMembers, false),
		Entry("include list with only audit logs",
			GCP{Include: []string{IncludeAuditLogs}}, IncludeAuditLogs, true),
		Entry("include list with multiple features",
			GCP{Include: []string{IncludeIAMPolicy, IncludeAuditLogs}}, IncludeIAMPolicy, true),
		Entry("case-insensitive feature matching - mixed case",
			GCP{Include: []string{"AsSets", "AuDitLoGs"}}, "auditlogs", true),
	)

	// A narrowed list always gains the project. The hierarchy pass links to that config
	// item rather than emitting one, so leaving it out means nothing creates it and every
	// parent edge and cost root dangles.
	DescribeTable("GetAssetTypes",
		func(config GCP, expected []string) {
			if len(expected) == 0 {
				Expect(config.GetAssetTypes()).To(BeEmpty())
				return
			}
			Expect(config.GetAssetTypes()).To(ConsistOf(expected))
		},
		Entry("not narrowed, so everything is scraped and there is nothing to add",
			GCP{}, nil),
		Entry("narrowed to asset types",
			GCP{Include: []string{"storage.googleapis.com/Bucket", "compute.googleapis.com/Instance"}},
			[]string{"storage.googleapis.com/Bucket", "compute.googleapis.com/Instance", ProjectAssetType}),
		Entry("narrowed to feature flags, which would otherwise skip the asset pass entirely",
			GCP{Include: []string{IncludeIAMPolicy}},
			[]string{ProjectAssetType}),
		Entry("already asked for, so not duplicated",
			GCP{Include: []string{ProjectAssetType}},
			[]string{ProjectAssetType}),
	)

	DescribeTable("ConfiguredProjects",
		func(config GCP, expected []string) {
			Expect(config.ConfiguredProjects()).To(Equal(expected))
		},
		Entry("nothing configured", GCP{}, nil),
		Entry("singular project alias", GCP{Project: "gcp-proj-1"}, []string{"gcp-proj-1"}),
		Entry("qualified names are trimmed",
			GCP{Projects: []string{"projects/gcp-proj-1", "gcp-proj-2"}},
			[]string{"gcp-proj-1", "gcp-proj-2"}),
		Entry("alias merges with the list",
			GCP{Project: "gcp-proj-1", Projects: []string{"gcp-proj-2"}},
			[]string{"gcp-proj-2", "gcp-proj-1"}),
		Entry("alias already in the list is not duplicated",
			GCP{Project: "gcp-proj-1", Projects: []string{"projects/gcp-proj-1"}},
			[]string{"gcp-proj-1"}),
		Entry("blank entries are dropped",
			GCP{Projects: []string{"", "gcp-proj-1"}},
			[]string{"gcp-proj-1"}),
	)

	DescribeTable("Validate",
		func(config GCP, expectErr bool) {
			if expectErr {
				Expect(config.Validate()).To(HaveOccurred())
				return
			}
			Expect(config.Validate()).ToNot(HaveOccurred())
		},
		Entry("organization only", GCP{Organization: "1234567890"}, false),
		Entry("projects only", GCP{Projects: []string{"gcp-proj-1"}}, false),
		Entry("singular project only", GCP{Project: "gcp-proj-1"}, false),
		Entry("organization and projects", GCP{Organization: "1234567890", Projects: []string{"gcp-proj-1"}}, false),
		Entry("neither", GCP{}, true),
	)

	DescribeTable("IsOrgScoped",
		func(config GCP, expected bool) {
			Expect(config.IsOrgScoped()).To(Equal(expected))
		},
		Entry("projects only", GCP{Project: "gcp-proj-1"}, false),
		Entry("organization set", GCP{Organization: "1234567890"}, true),
		Entry("organization and projects", GCP{Organization: "1234567890", Projects: []string{"p"}}, true),
	)

	DescribeTable("OrganizationID",
		func(config GCP, expected string) {
			Expect(config.OrganizationID()).To(Equal(expected))
		},
		Entry("bare organization number", GCP{Organization: "1234567890"}, "1234567890"),
		Entry("organization already qualified", GCP{Organization: "organizations/1234567890"}, "1234567890"),
		Entry("no organization", GCP{Project: "gcp-proj-1"}, ""),
	)
})
