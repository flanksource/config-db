package aws

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/flanksource/commons/hash"
	"github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"gopkg.in/yaml.v3"
)

func TestAWS(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "AWS Suite")
}

var _ = Describe("CloudTrailEventToChange", func() {
	type testCase struct {
		name               string
		eventRaw           string
		eventSource        string
		expectedCreatedBy  string
		expectedExternalID string
		expectedConfigType string
	}

	DescribeTable("extracting created_by from events",
		func(tc testCase) {
			var eventMap map[string]any
			Expect(yaml.Unmarshal([]byte(tc.eventRaw), &eventMap)).To(Succeed())

			eventJSON, err := json.Marshal(eventMap)
			Expect(err).ToNot(HaveOccurred())

			event := types.Event{
				CloudTrailEvent: lo.ToPtr(string(eventJSON)),
			}
			if tc.eventSource != "" {
				event.EventSource = lo.ToPtr(tc.eventSource)
			}

			change, err := cloudtrailEventToChange(event, types.Resource{})
			Expect(err).ToNot(HaveOccurred())
			Expect(change).ToNot(BeNil())
			Expect(*change.CreatedBy).To(Equal(tc.expectedCreatedBy))
			if tc.expectedExternalID != "" {
				Expect(change.ExternalID).To(Equal(tc.expectedExternalID))
			}
			if tc.expectedConfigType != "" {
				Expect(change.ConfigType).To(Equal(tc.expectedConfigType))
			}
		},
		Entry("Assumed Role", testCase{
			name: "Assumed Role",
			eventRaw: `---
userIdentity:
  arn: arn:aws:sts::4324:assumed-role/Administrators/john
  type: AssumedRole
  accountId: "324"
  sessionContext:
    attributes:
      creationDate: 2025-05-16T15:59:19Z
      mfaAuthenticated: "true"
    sessionIssuer:
      arn: arn:aws:iam::21321:role/Administrators
      type: Role
      userName: Administrators
      accountId: "213213"
    webIdFederationData: {}`,
			expectedCreatedBy: "john",
		}),
		Entry("Assumed Role with Principal ID", testCase{
			name: "Assumed Role with Principal ID",
			eventRaw: `---
userIdentity:
  arn: arn:aws:sts::123123123123:assumed-role/jenkinsmaster/i-069a0636b94872504
  type: AssumedRole
  accountId: "123123123123"
  accessKeyId: ASIA3WOC7GPYGA5RWXKL
  principalId: AROA3WOC7GPYMPZ5VPCER:i-069a0636b94872504
  sessionContext:
    attributes:
      creationDate: 2025-05-29T11:19:25Z
      mfaAuthenticated: "false"
    sessionIssuer:
      arn: arn:aws:iam::123123123123:role/jenkinsmaster
      type: Role
      userName: jenkinsmaster
      accountId: "123123123123"
      principalId: AROA3WOC7GPYMPZ5VPCER
    ec2RoleDelivery: "2.0"`,
			expectedCreatedBy: "jenkinsmaster",
		}),
		Entry("Assumed Role with Principal ID 2", testCase{
			name: "Assumed Role with Principal ID 2",
			eventRaw: `---
userIdentity:
  arn: arn:aws:sts::789789789789:assumed-role/AWSBackupDefaultServiceRole/AWSBackup-AWSBackupDefaultServiceRole
  type: AssumedRole
  accountId: "789789789789"
  invokedBy: backup.amazonaws.com
  accessKeyId: ASIA3EQTD5CGATJTYKVG
  principalId: AROA3EQTD5CGIBKBX7GCI:AWSBackup-AWSBackupDefaultServiceRole
  sessionContext:
    attributes:
      creationDate: 2025-05-29T01:47:08Z
      mfaAuthenticated: "false"
    sessionIssuer:
      arn: arn:aws:iam::789789789789:role/service-role/AWSBackupDefaultServiceRole
      type: Role
      userName: AWSBackupDefaultServiceRole
      accountId: "789789789789"
      principalId: AROA3EQTD5CGIBKBX7GCI`,
			expectedCreatedBy: "AWSBackupDefaultServiceRole",
		}),
		Entry("Assumed Role with Invoker", testCase{
			name: "Assumed Role with Invoker",
			eventRaw: `---
userIdentity:
  arn: arn:aws:sts::123123123123:assumed-role/ifs-mgmt-mon-eks20231002071117908400000007/1747815213169517497
  type: AssumedRole
  accountId: "123123123123"
  invokedBy: eks.amazonaws.com
  accessKeyId: ASIA3WOC7GPYM4CGP3VW
  principalId: AROA3WOC7GPYK3NTHEJFW:1747815213169517497
  sessionContext:
    attributes:
      creationDate: 2025-05-29T12:26:15Z
      mfaAuthenticated: "false"
    sessionIssuer:
      arn: arn:aws:iam::123123123123:role/ifs-mgmt-mon-eks20231002071117908400000007
      type: Role
      userName: ifs-mgmt-mon-eks20231002071117908400000007
      accountId: "123123123123"
      principalId: AROA3WOC7GPYK3NTHEJFW`,
			expectedCreatedBy: "ifs-mgmt-mon-eks20231002071117908400000007",
		}),
		Entry("IAM User", testCase{
			name: "IAM User",
			eventRaw: `---
userIdentity:
  arn: arn:aws:iam::789789789789:user/Engineering/AdityaThebe
  type: IAMUser
  userName: AdityaThebe
  accountId: "789789789789"
  accessKeyId: ASIA3EQTD5CGGCE542GG
  principalId: AIDA3EQTD5CGBLB2GPBIR
  sessionContext:
    attributes:
      creationDate: 2025-05-28T06:01:57Z
      mfaAuthenticated: "false"
`,
			expectedCreatedBy: "AdityaThebe",
		}),
		Entry("Root User", testCase{
			name: "Root User",
			eventRaw: `---
userIdentity:
  type: Root
  principalId: '789789789789'
  arn: arn:aws:iam::789789789789:root
  accountId: '789789789789'
  accessKeyId: AKIAIOSFODNN7EXAMPLE
  sessionContext:
    attributes:
      creationDate: '2025-05-22T13:35:48Z'
      mfaAuthenticated: 'true'
`,
			expectedCreatedBy: "arn:aws:iam::789789789789:root",
		}),
		Entry("ECR PutImage with ARN resource", testCase{
			name: "ECR PutImage with ARN resource",
			eventRaw: `---
userIdentity:
  type: IAMUser
  userName: github-actions-ecr
resources:
  - accountId: "765618022540"
    ARN: arn:aws:ecr-public::765618022540:repository/incident-commander
`,
			eventSource:        "ecr-public.amazonaws.com",
			expectedCreatedBy:  "github-actions-ecr",
			expectedExternalID: "arn:aws:ecr-public::765618022540:repository/incident-commander",
			expectedConfigType: "AWS::ECR::Repository",
		}),
		Entry("CloudWatch Logs CreateLogStream from request parameters", testCase{
			name: "CloudWatch Logs CreateLogStream from request parameters",
			eventRaw: `---
awsRegion: us-east-1
recipientAccountId: "765618022540"
userIdentity:
  type: IAMUser
  userName: github-actions-ecr
requestParameters:
  logGroupName: "/aws/ecs/containerinsights/demo-dev-cluster/performance"
  logStreamName: "FargateTelemetry-2681"
`,
			eventSource:        "logs.amazonaws.com",
			expectedCreatedBy:  "github-actions-ecr",
			expectedExternalID: "arn:aws:logs:us-east-1:765618022540:log-group:/aws/ecs/containerinsights/demo-dev-cluster/performance:log-stream:FargateTelemetry-2681",
			expectedConfigType: "AWS::Logs::LogStream",
		}),
	)
})

var _ = Describe("CloudTrailAssumeRoleToAccessLog", func() {
	eventTime := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	type testCase struct {
		name                  string
		eventRaw              string
		expectedUserName      string
		expectedUserARN       string
		expectedUserAccountID string
		expectedUserType      string
		expectedRoleARN       string
		expectedConfigType    string
	}

	DescribeTable("extracting access logs from AssumeRole events",
		func(tc testCase) {
			var eventMap map[string]any
			Expect(yaml.Unmarshal([]byte(tc.eventRaw), &eventMap)).To(Succeed())

			eventJSON, err := json.Marshal(eventMap)
			Expect(err).ToNot(HaveOccurred())

			event := types.Event{
				CloudTrailEvent: lo.ToPtr(string(eventJSON)),
				EventTime:       &eventTime,
				EventName:       lo.ToPtr("AssumeRole"),
			}

			result, err := cloudtrailAssumeRoleToAccessLog(event)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).ToNot(BeNil())

			Expect(result.ExternalUsers).To(HaveLen(1))
			user := result.ExternalUsers[0]
			Expect(user.Name).To(Equal(tc.expectedUserName))
			Expect(user.Tenant).To(Equal(tc.expectedUserAccountID))
			Expect(user.UserType).To(Equal(tc.expectedUserType))
			Expect(user.Aliases).To(ContainElement(tc.expectedUserARN))

			expectedUserID, err := hash.DeterministicUUID(pq.StringArray{tc.expectedUserARN})
			Expect(err).ToNot(HaveOccurred())
			Expect(user.ID).To(Equal(expectedUserID))

			Expect(result.ConfigAccessLogs).To(HaveLen(1))
			accessLog := result.ConfigAccessLogs[0]
			Expect(accessLog.ConfigExternalID.ExternalID).To(Equal(tc.expectedRoleARN))
			Expect(accessLog.ConfigExternalID.ConfigType).To(Equal(tc.expectedConfigType))
			Expect(accessLog.ExternalUserID).To(Equal(expectedUserID))
			Expect(accessLog.CreatedAt).To(Equal(eventTime))
		},
		Entry("IAM user assumes role", testCase{
			name: "IAM user assumes role",
			eventRaw: `---
userIdentity:
  type: IAMUser
  arn: arn:aws:iam::123456789012:user/admin
  userName: admin
  accountId: "123456789012"
  principalId: AIDAEXAMPLE123
requestParameters:
  roleArn: arn:aws:iam::123456789012:role/MyRole
  roleSessionName: my-session
resources:
  - ARN: arn:aws:iam::123456789012:role/MyRole
    accountId: "123456789012"
`,
			expectedUserName:      "admin",
			expectedUserARN:       "arn:aws:iam::123456789012:user/admin",
			expectedUserAccountID: "123456789012",
			expectedUserType:      "IAMUser",
			expectedRoleARN:       "arn:aws:iam::123456789012:role/MyRole",
			expectedConfigType:    "AWS::IAM::Role",
		}),
		Entry("AssumedRole assumes another role (role chaining)", testCase{
			name: "AssumedRole assumes another role (role chaining)",
			eventRaw: `---
userIdentity:
  type: AssumedRole
  arn: arn:aws:sts::123456789012:assumed-role/IntermediateRole/session1
  accountId: "123456789012"
  principalId: AROAEXAMPLE:session1
  sessionContext:
    sessionIssuer:
      arn: arn:aws:iam::123456789012:role/IntermediateRole
      userName: IntermediateRole
      accountId: "123456789012"
requestParameters:
  roleArn: arn:aws:iam::987654321098:role/TargetRole
resources:
  - ARN: arn:aws:iam::987654321098:role/TargetRole
    accountId: "987654321098"
`,
			expectedUserName:      "IntermediateRole",
			expectedUserARN:       "arn:aws:iam::123456789012:role/IntermediateRole",
			expectedUserAccountID: "123456789012",
			expectedUserType:      "AssumedRole",
			expectedRoleARN:       "arn:aws:iam::987654321098:role/TargetRole",
			expectedConfigType:    "AWS::IAM::Role",
		}),
	)
})
