package aws

import (
	"encoding/json"

	v1 "github.com/flanksource/config-db/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

var _ = Describe("resolveTrustPolicy", func() {
	const roleARN = "arn:aws:iam::111111111111:role/TargetRole"
	const roleAccount = "111111111111"

	parse := func(s string) trustPolicyDoc {
		var doc trustPolicyDoc
		Expect(json.Unmarshal([]byte(s), &doc)).To(Succeed())
		return doc
	}

	userAliases := func(r trustResolution) [][]string {
		return lo.Map(r.Accesses, func(a v1.ExternalConfigAccess, _ int) []string {
			return a.ExternalUserAliases
		})
	}
	// Access is scoped to the AWS account (ConfigExternalID=AWSAccount/account).
	// The IAM role itself is the permission, emitted as ExternalRole with
	// alias=roleARN and Name=last segment. Principals — including role-chained
	// ones — go in ExternalUserAliases / ExternalGroupAliases.
	groupAliases := func(r trustResolution) [][]string {
		return lo.Map(r.Accesses, func(a v1.ExternalConfigAccess, _ int) []string {
			return a.ExternalGroupAliases
		})
	}

	It("emits an ExternalUser for an IAM user principal", func() {
		doc := parse(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::222222222222:user/alice"},"Action":"sts:AssumeRole"}]}`)
		r := resolveTrustPolicy(roleARN, roleAccount, doc)
		Expect(r.Users).To(HaveLen(1))
		Expect(r.Users[0].Name).To(Equal("alice"))
		Expect(r.Users[0].Tenant).To(Equal("222222222222"))
		Expect(r.Users[0].UserType).To(Equal("IAMUser"))
		Expect(userAliases(r)).To(Equal([][]string{{"arn:aws:iam::222222222222:user/alice"}}))
		Expect(r.Accesses[0].ConfigExternalID.ConfigType).To(Equal(v1.AWSAccount))
		Expect(r.Accesses[0].ConfigExternalID.ExternalID).To(Equal(roleAccount))
		Expect(r.Accesses[0].ExternalRoleAliases).To(Equal([]string{roleARN}))
	})

	It("emits an ExternalUser with UserType=IAMRole for a role-chained principal", func() {
		doc := parse(`{"Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::333333333333:role/UpstreamRole"},"Action":"sts:AssumeRole"}]}`)
		r := resolveTrustPolicy(roleARN, roleAccount, doc)
		Expect(r.Users).To(HaveLen(1))
		Expect(r.Users[0].Name).To(Equal("UpstreamRole"))
		Expect(r.Users[0].Tenant).To(Equal("333333333333"))
		Expect(r.Users[0].UserType).To(Equal("IAMRole"))
		Expect(userAliases(r)).To(Equal([][]string{{"arn:aws:iam::333333333333:role/UpstreamRole"}}))
	})

	It("emits the IAM role as an ExternalRole and scopes access to the account", func() {
		doc := parse(`{"Statement":[
			{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::222222222222:user/alice"}},
			{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"}}
		]}`)
		r := resolveTrustPolicy(roleARN, roleAccount, doc)
		Expect(r.Roles).To(HaveLen(1))
		Expect(r.Roles[0].Name).To(Equal("TargetRole"))
		Expect(r.Roles[0].RoleType).To(Equal("IAMRole"))
		Expect(r.Roles[0].Tenant).To(Equal(roleAccount))
		Expect([]string(r.Roles[0].Aliases)).To(Equal([]string{roleARN}))
		for _, a := range r.Accesses {
			Expect(a.ConfigExternalID.ConfigType).To(Equal(v1.AWSAccount))
			Expect(a.ConfigExternalID.ExternalID).To(Equal(roleAccount))
			Expect(a.ExternalRoleAliases).To(Equal([]string{roleARN}))
		}
	})

	It("emits an ExternalGroup for :root cross-account trust", func() {
		doc := parse(`{"Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::444444444444:root"}}]}`)
		r := resolveTrustPolicy(roleARN, roleAccount, doc)
		Expect(r.Groups).To(HaveLen(1))
		Expect(r.Groups[0].GroupType).To(Equal("AWSAccount"))
		Expect(r.Groups[0].Tenant).To(Equal("444444444444"))
		Expect(groupAliases(r)).To(Equal([][]string{{"arn:aws:iam::444444444444:root"}}))
	})

	It("normalizes bare account IDs to :root", func() {
		doc := parse(`{"Statement":[{"Effect":"Allow","Principal":{"AWS":"555555555555"}}]}`)
		r := resolveTrustPolicy(roleARN, roleAccount, doc)
		Expect(r.Groups).To(HaveLen(1))
		Expect(groupAliases(r)).To(Equal([][]string{{"arn:aws:iam::555555555555:root"}}))
	})

	It("emits an ExternalUser for Service principals", func() {
		doc := parse(`{"Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"}}]}`)
		r := resolveTrustPolicy(roleARN, roleAccount, doc)
		Expect(r.Users).To(HaveLen(1))
		Expect(r.Users[0].UserType).To(Equal("AWSService"))
		Expect(r.Users[0].Name).To(Equal("ec2.amazonaws.com"))
		Expect(userAliases(r)).To(Equal([][]string{{"aws-service:ec2.amazonaws.com"}}))
	})

	It("emits an ExternalUser with UserType=OIDC for OIDC Federated principals", func() {
		doc := parse(`{
			"Statement":[{
				"Effect":"Allow",
				"Principal":{"Federated":"arn:aws:iam::111111111111:oidc-provider/token.actions.githubusercontent.com"},
				"Action":"sts:AssumeRoleWithWebIdentity",
				"Condition":{"StringEquals":{"token.actions.githubusercontent.com:sub":"repo:flanksource/config-db:ref:refs/heads/main"}}
			}]
		}`)
		r := resolveTrustPolicy(roleARN, roleAccount, doc)
		Expect(r.Users).To(HaveLen(1))
		Expect(r.Users[0].UserType).To(Equal("OIDC"))
		Expect(r.Users[0].Name).To(Equal("token.actions.githubusercontent.com"))
		Expect(*r.Accesses[0].Source).To(ContainSubstring("condition=StringEquals:token.actions.githubusercontent.com:sub"))
	})

	It("emits an ExternalUser with UserType=SAML for SAML Federated principals", func() {
		doc := parse(`{"Statement":[{"Effect":"Allow","Principal":{"Federated":"arn:aws:iam::111111111111:saml-provider/Okta"}}]}`)
		r := resolveTrustPolicy(roleARN, roleAccount, doc)
		Expect(r.Users).To(HaveLen(1))
		Expect(r.Users[0].UserType).To(Equal("SAML"))
		Expect(r.Users[0].Name).To(Equal("Okta"))
	})

	It("falls back to UserType=Federated for non-ARN service identifiers", func() {
		doc := parse(`{"Statement":[{"Effect":"Allow","Principal":{"Federated":"accounts.google.com"}}]}`)
		r := resolveTrustPolicy(roleARN, roleAccount, doc)
		Expect(r.Users).To(HaveLen(1))
		Expect(r.Users[0].UserType).To(Equal("Federated"))
		Expect(r.Users[0].Name).To(Equal("accounts.google.com"))
	})

	It("skips unconditional Principal: '*' with a warning", func() {
		doc := parse(`{"Statement":[{"Effect":"Allow","Principal":"*"}]}`)
		r := resolveTrustPolicy(roleARN, roleAccount, doc)
		Expect(r.Users).To(BeEmpty())
		Expect(r.Accesses).To(BeEmpty())
		Expect(r.Warnings).To(HaveLen(1))
		Expect(r.Warnings[0]).To(ContainSubstring("unconditional"))
	})

	It("emits a Federated user for Principal: '*' with a condition", func() {
		doc := parse(`{"Statement":[{"Effect":"Allow","Principal":"*","Condition":{"StringEquals":{"sts:ExternalId":"abc"}}}]}`)
		r := resolveTrustPolicy(roleARN, roleAccount, doc)
		Expect(r.Users).To(HaveLen(1))
		Expect(r.Users[0].UserType).To(Equal("Federated"))
		Expect(r.Warnings).To(BeEmpty())
	})

	It("skips Effect=Deny statements", func() {
		doc := parse(`{"Statement":[{"Effect":"Deny","Principal":{"AWS":"arn:aws:iam::222222222222:user/alice"}}]}`)
		r := resolveTrustPolicy(roleARN, roleAccount, doc)
		Expect(r.Accesses).To(BeEmpty())
	})

	It("skips NotPrincipal statements", func() {
		doc := parse(`{"Statement":[{"Effect":"Allow","NotPrincipal":{"AWS":"arn:aws:iam::222222222222:user/alice"}}]}`)
		r := resolveTrustPolicy(roleARN, roleAccount, doc)
		Expect(r.Accesses).To(BeEmpty())
	})

	It("handles Principal values given as arrays", func() {
		doc := parse(`{"Statement":[{"Effect":"Allow","Principal":{"AWS":["arn:aws:iam::222222222222:user/alice","arn:aws:iam::222222222222:user/bob"]}}]}`)
		r := resolveTrustPolicy(roleARN, roleAccount, doc)
		Expect(r.Users).To(HaveLen(2))
		Expect(r.Accesses).To(HaveLen(2))
	})

	It("dedupes repeated principals across statements but emits one access per statement", func() {
		doc := parse(`{"Statement":[
			{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::222222222222:user/alice"}},
			{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::222222222222:user/alice"},"Condition":{"Bool":{"aws:MultiFactorAuthPresent":"true"}}}
		]}`)
		r := resolveTrustPolicy(roleARN, roleAccount, doc)
		Expect(r.Users).To(HaveLen(1))
		Expect(r.Accesses).To(HaveLen(2))
		Expect(*r.Accesses[1].Source).To(ContainSubstring("condition=Bool:aws:MultiFactorAuthPresent"))
	})
})
