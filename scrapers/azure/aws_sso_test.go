package azure

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("parseAWSSSOAppRoleValue", func() {
	DescribeTable("returns expected (roleARN, samlARN, ok) for AppRole value strings",
		func(value, wantRole, wantSAML string, wantOK bool) {
			role, saml, ok := parseAWSSSOAppRoleValue(value)
			Expect(ok).To(Equal(wantOK))
			Expect(role).To(Equal(wantRole))
			Expect(saml).To(Equal(wantSAML))
		},
		Entry("role then saml-provider",
			"arn:aws:iam::963567256330:role/deploy,arn:aws:iam::963567256330:saml-provider/customer-saml",
			"arn:aws:iam::963567256330:role/deploy",
			"arn:aws:iam::963567256330:saml-provider/customer-saml",
			true),
		Entry("saml-provider then role",
			"arn:aws:iam::963567256330:saml-provider/customer-saml,arn:aws:iam::963567256330:role/deploy",
			"arn:aws:iam::963567256330:role/deploy",
			"arn:aws:iam::963567256330:saml-provider/customer-saml",
			true),
		Entry("tolerates whitespace around ARNs",
			"  arn:aws:iam::111111111111:role/read ,  arn:aws:iam::111111111111:saml-provider/okta ",
			"arn:aws:iam::111111111111:role/read",
			"arn:aws:iam::111111111111:saml-provider/okta",
			true),
		Entry("empty string", "", "", "", false),
		Entry("null-like whitespace", "   ", "", "", false),
		Entry("single ARN only",
			"arn:aws:iam::963567256330:role/deploy",
			"", "", false),
		Entry("three comma-separated ARNs",
			"arn:aws:iam::963567256330:role/a,arn:aws:iam::963567256330:saml-provider/s,arn:aws:iam::963567256330:role/b",
			"", "", false),
		Entry("two role ARNs (no saml-provider)",
			"arn:aws:iam::963567256330:role/a,arn:aws:iam::963567256330:role/b",
			"", "", false),
		Entry("two saml-provider ARNs (no role)",
			"arn:aws:iam::963567256330:saml-provider/a,arn:aws:iam::963567256330:saml-provider/b",
			"", "", false),
		Entry("non-AWS ARN",
			"arn:aws:s3:::bucket,arn:aws:iam::963567256330:saml-provider/s",
			"", "", false),
		Entry("account mismatch between role and saml-provider",
			"arn:aws:iam::111111111111:role/deploy,arn:aws:iam::222222222222:saml-provider/customer-saml",
			"", "", false),
		Entry("empty role resource suffix",
			"arn:aws:iam::963567256330:role/,arn:aws:iam::963567256330:saml-provider/s",
			"", "", false),
		Entry("empty saml-provider resource suffix",
			"arn:aws:iam::963567256330:role/a,arn:aws:iam::963567256330:saml-provider/",
			"", "", false),
		Entry("non-digit account",
			"arn:aws:iam::notanaccount:role/a,arn:aws:iam::notanaccount:saml-provider/s",
			"", "", false),
	)
})
