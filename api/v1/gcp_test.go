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
		Entry("include list with only audit logs",
			GCP{Include: []string{IncludeAuditLogs}}, IncludeAuditLogs, true),
		Entry("include list with multiple features",
			GCP{Include: []string{IncludeIAMPolicy, IncludeAuditLogs}}, IncludeIAMPolicy, true),
		Entry("case-insensitive feature matching - mixed case",
			GCP{Include: []string{"AsSets", "AuDitLoGs"}}, "auditlogs", true),
	)

	DescribeTable("GetAssetTypes",
		func(config GCP, expected []string) {
			Expect(config.GetAssetTypes()).To(Equal(expected))
		},
		Entry("empty asset types and include - returns empty",
			GCP{}, nil),
		Entry("include with asset types",
			GCP{Include: []string{"storage.googleapis.com/Bucket", "compute.googleapis.com/Instance"}},
			[]string{"storage.googleapis.com/Bucket", "compute.googleapis.com/Instance"}),
	)
})
