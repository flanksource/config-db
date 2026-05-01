package aws

import (
	"net/url"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("IAM role permissions helpers", func() {
	DescribeTable("isAWSManagedPolicyARN",
		func(policyARN string, want bool) {
			Expect(isAWSManagedPolicyARN(policyARN)).To(Equal(want))
		},
		Entry("AWS managed policy",
			"arn:aws:iam::aws:policy/ReadOnlyAccess",
			true),
		Entry("AWS service-role managed policy",
			"arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole",
			true),
		Entry("AWS managed policy in another partition",
			"arn:aws-us-gov:iam::aws:policy/ReadOnlyAccess",
			true),
		Entry("customer managed policy",
			"arn:aws:iam::111111111111:policy/TeamDeploy",
			false),
	)

	It("decodes URL-encoded IAM policy documents", func() {
		encoded := url.QueryEscape(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject"],"Resource":"*"}]}`)

		doc, err := parseIAMPolicyDocument(encoded)
		Expect(err).NotTo(HaveOccurred())
		Expect(doc["Version"]).To(Equal("2012-10-17"))

		statements, ok := doc["Statement"].([]any)
		Expect(ok).To(BeTrue())
		Expect(statements).To(HaveLen(1))
		statement, ok := statements[0].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(statement["Effect"]).To(Equal("Allow"))
	})

	It("reports empty permissions only when no policy classes are present", func() {
		Expect(iamRolePermissions{}.IsEmpty()).To(BeTrue())
		Expect(iamRolePermissions{AWSManagedPolicies: []string{"ReadOnlyAccess"}}.IsEmpty()).To(BeFalse())
		Expect(iamRolePermissions{CustomerManagedPolicies: []iamManagedPolicyDocument{{PolicyName: "TeamDeploy"}}}.IsEmpty()).To(BeFalse())
		Expect(iamRolePermissions{InlinePolicies: []iamInlinePolicyDocument{{PolicyName: "InlineDeploy"}}}.IsEmpty()).To(BeFalse())
	})
})
