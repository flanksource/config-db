package azure

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("shouldEmitAssignment", func() {
	DescribeTable("returns expected emit decision for (filter, displayName, hasRole)",
		func(filter []string, displayName string, hasRole bool, want bool) {
			Expect(shouldEmitAssignment(filter, displayName, hasRole)).To(Equal(want))
		},
		Entry("no filter, role present → emit",
			nil, "Admin", true, true),
		Entry("no filter, role absent → emit (preserves member alias fallback)",
			nil, "", false, true),
		Entry("allow-list match → emit",
			[]string{"Admin"}, "Admin", true, true),
		Entry("allow-list miss → skip",
			[]string{"Admin"}, "Reader", true, false),
		Entry("multi-entry allow-list match → emit",
			[]string{"Admin", "Reader"}, "Reader", true, true),
		Entry("wildcard with exclusion → matched exclusion skips",
			[]string{"*", "!Guest"}, "Guest", true, false),
		Entry("wildcard with exclusion → unmatched exclusion emits",
			[]string{"*", "!Guest"}, "Admin", true, true),
		Entry("filter set, role absent → skip (null-role drop rule)",
			[]string{"Admin"}, "", false, false),
		Entry("filter set, role hasRole=true but displayName empty → skip",
			[]string{"Admin"}, "", true, false),
	)
})

var _ = Describe("splitRoleFilter", func() {
	DescribeTable("parses comma-separated role filter strings",
		func(in string, want []string) {
			Expect(splitRoleFilter(in)).To(Equal(want))
		},
		Entry("empty string → nil",
			"", []string(nil)),
		Entry("single role",
			"Admin", []string{"Admin"}),
		Entry("multiple roles with whitespace and exclusion (whitespace preserved; MatchItems trims)",
			"Admin, Reader , !Guest", []string{"Admin", " Reader ", " !Guest"}),
		Entry("only commas/whitespace → nil",
			" , , ", []string(nil)),
	)
})
