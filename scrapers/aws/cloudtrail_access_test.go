package aws

import (
	"encoding/json"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	v1 "github.com/flanksource/config-db/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"gopkg.in/yaml.v3"
)

// ctEventFromYAML decodes an inline YAML CloudTrail event into the struct
// the production code expects (same pattern as cloudtrail_test.go).
func ctEventFromYAML(raw string) CloudTrailEvent {
	var m map[string]any
	ExpectWithOffset(1, yaml.Unmarshal([]byte(raw), &m)).To(Succeed())
	j, err := json.Marshal(m)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	var ev CloudTrailEvent
	ExpectWithOffset(1, ev.FromJSON(string(j))).To(Succeed())
	return ev
}

func ctEventT(raw string, when time.Time, name string) types.Event {
	var m map[string]any
	ExpectWithOffset(1, yaml.Unmarshal([]byte(raw), &m)).To(Succeed())
	j, err := json.Marshal(m)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return types.Event{
		CloudTrailEvent: lo.ToPtr(string(j)),
		EventTime:       &when,
		EventName:       lo.ToPtr(name),
	}
}

var _ = Describe("extractCaller", func() {
	It("returns IAMUser identity with MFA flag", func() {
		ct := ctEventFromYAML(`---
userIdentity:
  type: IAMUser
  arn: arn:aws:iam::111111111111:user/alice
  userName: alice
  accountId: "111111111111"
  sessionContext:
    attributes:
      mfaAuthenticated: "true"
`)
		c, err := extractCaller(ct)
		Expect(err).ToNot(HaveOccurred())
		Expect(c).ToNot(BeNil())
		Expect(c.User.Name).To(Equal("alice"))
		Expect(c.User.Tenant).To(Equal("111111111111"))
		Expect(c.User.UserType).To(Equal("IAMUser"))
		Expect(c.MFA).To(BeTrue())
	})

	It("returns AssumedRole identity via sessionIssuer", func() {
		ct := ctEventFromYAML(`---
userIdentity:
  type: AssumedRole
  arn: arn:aws:sts::222222222222:assumed-role/DeployRole/session-1
  accountId: "222222222222"
  sessionContext:
    attributes:
      mfaAuthenticated: "false"
    sessionIssuer:
      arn: arn:aws:iam::222222222222:role/DeployRole
      userName: DeployRole
`)
		c, err := extractCaller(ct)
		Expect(err).ToNot(HaveOccurred())
		Expect(c.User.Name).To(Equal("DeployRole"))
		Expect(c.User.UserType).To(Equal("AssumedRole"))
		Expect(c.MFA).To(BeFalse())
	})

	It("returns nil for AWSService callers", func() {
		ct := ctEventFromYAML(`---
userIdentity:
  type: AWSService
  invokedBy: ec2.amazonaws.com
`)
		c, err := extractCaller(ct)
		Expect(err).ToNot(HaveOccurred())
		Expect(c).To(BeNil())
	})

	It("returns an error when caller ARN is missing", func() {
		ct := ctEventFromYAML(`---
userIdentity:
  type: IAMUser
  userName: alice
`)
		_, err := extractCaller(ct)
		Expect(err).To(HaveOccurred())
	})

	It("handles Root / FederatedUser via the default branch", func() {
		ct := ctEventFromYAML(`---
userIdentity:
  type: Root
  arn: arn:aws:iam::333333333333:root
  accountId: "333333333333"
`)
		c, err := extractCaller(ct)
		Expect(err).ToNot(HaveOccurred())
		Expect(c.User.Tenant).To(Equal("333333333333"))
		Expect(c.User.UserType).To(Equal("Root"))
	})
})

var _ = Describe("accessLogAggregator", func() {
	const raw = `---
userIdentity:
  type: IAMUser
  arn: arn:aws:iam::111111111111:user/alice
  userName: alice
  accountId: "111111111111"
  sessionContext:
    attributes:
      mfaAuthenticated: "false"
requestParameters:
  roleArn: arn:aws:iam::111111111111:role/DeployRole
`
	const rawMFA = `---
userIdentity:
  type: IAMUser
  arn: arn:aws:iam::111111111111:user/alice
  userName: alice
  accountId: "111111111111"
  sessionContext:
    attributes:
      mfaAuthenticated: "true"
requestParameters:
  roleArn: arn:aws:iam::111111111111:role/DeployRole
`

	day1a := time.Date(2026, 4, 21, 8, 0, 0, 0, time.UTC)
	day1b := time.Date(2026, 4, 21, 14, 30, 0, 0, time.UTC)
	day1c := time.Date(2026, 4, 21, 22, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 4, 22, 1, 0, 0, 0, time.UTC)

	It("collapses 3 events for the same (role,user,day) into one log with count=3", func() {
		agg := newAccessLogAggregator()
		for _, t := range []time.Time{day1a, day1b, day1c} {
			Expect(agg.addAssumeRole(ctEventT(raw, t, "AssumeRole"), ctEventFromYAML(raw))).To(Succeed())
		}

		sr := agg.flush()
		Expect(sr.ExternalUsers).To(HaveLen(1))
		Expect(sr.ConfigAccessLogs).To(HaveLen(1))
		log := sr.ConfigAccessLogs[0]
		Expect(log.Count).ToNot(BeNil())
		Expect(*log.Count).To(Equal(3))
		Expect(log.MFA).To(BeFalse())
		Expect(log.CreatedAt).To(Equal(day1c))
		Expect(log.ConfigExternalID.ConfigType).To(Equal(v1.AWSIAMRole))
		Expect(log.ConfigExternalID.ExternalID).To(Equal("arn:aws:iam::111111111111:role/DeployRole"))
	})

	It("OR-accumulates MFA across events in the same bucket", func() {
		agg := newAccessLogAggregator()
		Expect(agg.addAssumeRole(ctEventT(raw, day1a, "AssumeRole"), ctEventFromYAML(raw))).To(Succeed())
		Expect(agg.addAssumeRole(ctEventT(rawMFA, day1b, "AssumeRole"), ctEventFromYAML(rawMFA))).To(Succeed())

		sr := agg.flush()
		Expect(sr.ConfigAccessLogs).To(HaveLen(1))
		Expect(sr.ConfigAccessLogs[0].MFA).To(BeTrue())
		Expect(*sr.ConfigAccessLogs[0].Count).To(Equal(2))
	})

	It("splits buckets across UTC days", func() {
		agg := newAccessLogAggregator()
		Expect(agg.addAssumeRole(ctEventT(raw, day1a, "AssumeRole"), ctEventFromYAML(raw))).To(Succeed())
		Expect(agg.addAssumeRole(ctEventT(raw, day2, "AssumeRole"), ctEventFromYAML(raw))).To(Succeed())

		sr := agg.flush()
		Expect(sr.ConfigAccessLogs).To(HaveLen(2))
		Expect(sr.ExternalUsers).To(HaveLen(1))
	})
})

var _ = Describe("isAssumeRoleEvent", func() {
	It("matches AssumeRole variants including WebIdentity and SAML", func() {
		Expect(isAssumeRoleEvent("AssumeRole")).To(BeTrue())
		Expect(isAssumeRoleEvent("AssumeRoleWithSAML")).To(BeTrue())
		Expect(isAssumeRoleEvent("AssumeRoleWithWebIdentity")).To(BeTrue())
	})

	It("does not match other STS events", func() {
		Expect(isAssumeRoleEvent("GetCallerIdentity")).To(BeFalse())
		Expect(isAssumeRoleEvent("")).To(BeFalse())
	})
})
