package v1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("IsConnectionRef", func() {
	DescribeTable("recognizes duty connection references",
		func(input string, want bool) {
			Expect(IsConnectionRef(input)).To(Equal(want))
		},
		Entry("connection url with namespace", "connection://default/my-pg", true),
		Entry("connection url without namespace", "connection://my-pg", true),
		Entry("uuid", "550e8400-e29b-41d4-a716-446655440000", true),
		Entry("raw postgres url", "postgres://user:pass@host:5432/db", false),
		Entry("raw postgresql url", "postgresql://user:pass@host:5432/db", false),
		Entry("empty string", "", false),
		Entry("plain hostname", "my-host", false),
	)
})
