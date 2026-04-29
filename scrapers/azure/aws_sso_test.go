package azure

import (
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/google/uuid"
	msgraphModels "github.com/microsoftgraph/msgraph-sdk-go/models"

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
		Entry("customer AWS SSO app role value",
			"arn:aws:iam::507476443755:role/AWSManagedServicesAdminRole,arn:aws:iam::507476443755:saml-provider/customer-saml",
			"arn:aws:iam::507476443755:role/AWSManagedServicesAdminRole",
			"arn:aws:iam::507476443755:saml-provider/customer-saml",
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

var _ = Describe("awsIAMRoleName", func() {
	DescribeTable("returns the IAM role name from an ARN",
		func(arn, want string) {
			Expect(awsIAMRoleName(arn)).To(Equal(want))
		},
		Entry("simple role ARN",
			"arn:aws:iam::507476443755:role/AWSManagedServicesAdminRole",
			"AWSManagedServicesAdminRole"),
		Entry("path-qualified role ARN",
			"arn:aws:iam::507476443755:role/service-role/AWSManagedServicesAdminRole",
			"AWSManagedServicesAdminRole"),
		Entry("non-role ARN falls back to input",
			"arn:aws:iam::507476443755:saml-provider/customer-saml",
			"arn:aws:iam::507476443755:saml-provider/customer-saml"),
	)
})

var _ = Describe("appendAWSSSOAssignment", func() {
	It("scopes config access to the AWS account and uses the IAM role as the external role", func() {
		scraperID := uuid.New()
		spID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
		principalID := uuid.New()
		link := awsSSOLink{
			AccountID: "507476443755",
			RoleName:  "AWSManagedServicesAdminRole",
			RoleARN:   "arn:aws:iam::507476443755:role/AWSManagedServicesAdminRole",
			SAMLARN:   "arn:aws:iam::507476443755:saml-provider/customer-saml",
		}

		var result v1.ScrapeResult
		emitted := map[string]struct{}{}
		appendAWSSSOAssignment(&result, v1.ExternalConfigAccess{ScraperID: &scraperID, ExternalUserID: &principalID}, "assignment-1", spID, link, "AWSOMASharedInsuranceNonProdAdminRole", &scraperID, emitted)
		appendAWSSSOAssignment(&result, v1.ExternalConfigAccess{ScraperID: &scraperID, ExternalUserID: &principalID}, "assignment-2", spID, link, "AWSOMASharedInsuranceNonProdAdminRole", &scraperID, emitted)

		Expect(result.ExternalRoles).To(HaveLen(1))
		Expect(result.ExternalRoles[0].Tenant).To(Equal("507476443755"))
		Expect(result.ExternalRoles[0].RoleType).To(Equal("IAMRole"))
		Expect(result.ExternalRoles[0].Name).To(Equal("AWSManagedServicesAdminRole"))
		Expect([]string(result.ExternalRoles[0].Aliases)).To(Equal([]string{link.RoleARN}))

		Expect(result.ConfigAccess).To(HaveLen(2))
		for _, access := range result.ConfigAccess {
			Expect(access.ConfigExternalID.ConfigType).To(Equal(v1.AWSAccount))
			Expect(access.ConfigExternalID.ExternalID).To(Equal("507476443755"))
			Expect(access.ConfigExternalID.ScraperID).To(Equal("all"))
			Expect(access.ExternalRoleAliases).To(Equal([]string{link.RoleARN}))
			Expect(access.ExternalUserID).NotTo(BeNil())
			Expect(*access.ExternalUserID).To(Equal(principalID))
			Expect(access.Source).NotTo(BeNil())
			Expect(*access.Source).To(ContainSubstring(link.SAMLARN))
		}
	})
})

var _ = Describe("AWS SSO Entra group fan-out helpers", func() {
	It("builds a Graph id-in filter with quoted GUID string literals", func() {
		Expect(graphIDInFilter([]string{
			"11111111-1111-1111-1111-111111111111",
			"22222222-2222-2222-2222-222222222222",
		})).To(Equal("id in ('11111111-1111-1111-1111-111111111111','22222222-2222-2222-2222-222222222222')"))
	})

	It("chunks group ids for Graph in-filter requests", func() {
		Expect(chunkStrings([]string{"a", "b", "c", "d", "e"}, 2)).To(Equal([][]string{{"a", "b"}, {"c", "d"}, {"e"}}))
		Expect(chunkStrings([]string{"a"}, 0)).To(BeNil())
	})

	It("extracts Entra user aliases from selected Graph user fields", func() {
		user := msgraphModels.NewUser()
		user.SetMail(strPtr("alice@example.com"))
		user.SetUserPrincipalName(strPtr("alice@tenant.example"))
		user.SetOnPremisesSamAccountName(strPtr("alice.sam"))
		user.SetMailNickname(strPtr("alice"))
		user.SetEmployeeId(strPtr("E123"))

		Expect(azureUserAliases(user)).To(Equal([]string{
			"alice@example.com",
			"alice@tenant.example",
			"alice.sam",
			"alice",
			"E123",
			`OMCORE\alice`,
		}))
	})
})

func strPtr(s string) *string {
	return &s
}
