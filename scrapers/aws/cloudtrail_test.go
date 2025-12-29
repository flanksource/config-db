package aws

import (
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/onsi/gomega"
	"github.com/samber/lo"
	"gopkg.in/yaml.v3"
)

func TestCloudTrailEventToChange(t *testing.T) {
	tests := []struct {
		name               string
		eventRaw           string
		eventSource        string
		expectedCreatedBy  string
		expectedExternalID string
		expectedConfigType string
	}{
		{
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
		},
		{
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
		},
		{
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
		},
		{
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
		},
		{
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
		},
		{
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
		},
		{
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
		},
		{
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
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := gomega.NewWithT(t)

			var eventMap map[string]any
			err := yaml.Unmarshal([]byte(tt.eventRaw), &eventMap)
			g.Expect(err).To(gomega.Succeed())

			eventJSON, err := json.Marshal(eventMap)
			g.Expect(err).To(gomega.Succeed())

			event := types.Event{
				CloudTrailEvent: lo.ToPtr(string(eventJSON)),
			}
			if tt.eventSource != "" {
				event.EventSource = lo.ToPtr(tt.eventSource)
			}

			change, err := cloudtrailEventToChange(event, types.Resource{})
			g.Expect(err).To(gomega.Succeed())
			g.Expect(change).To(gomega.Not(gomega.BeNil()))
			g.Expect(*change.CreatedBy).To(gomega.Equal(tt.expectedCreatedBy))
			if tt.expectedExternalID != "" {
				g.Expect(change.ExternalID).To(gomega.Equal(tt.expectedExternalID))
			}
			if tt.expectedConfigType != "" {
				g.Expect(change.ConfigType).To(gomega.Equal(tt.expectedConfigType))
			}
		})
	}
}
