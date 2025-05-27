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
		name              string
		eventRaw          string
		expectedCreatedBy string
	}{
		{
			name: "Assumed Role",
			eventRaw: `userIdentity:
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
			expectedCreatedBy: "arn:aws:sts::4324:assumed-role/Administrators/john",
		},
		{
			name: "IAM User",
			eventRaw: `---
eventVersion: '1.09'
userIdentity:
  type: IAMUser
  principalId: AIDA3EQTD5CGJVYRKJJRK
  arn: arn:aws:iam::765618022540:user/github-actions-ecr
  accountId: '765618022540'
  accessKeyId: AKIAIOSFODNN7EXAMPLE
  userName: github-actions-ecr
  sessionContext:
    attributes:
      creationDate: '2025-05-27T06:45:53Z'
      mfaAuthenticated: 'false'
  invokedBy: ecr-public.amazonaws.com
eventTime: '2025-05-27T06:54:28Z'
eventSource: ecr-public.amazonaws.com
eventName: UploadLayerPart
awsRegion: us-east-1
sourceIPAddress: ecr-public.amazonaws.com
userAgent: ecr-public.amazonaws.com
requestParameters:
  registryId: k4y9r6y5
  repositoryName: canary-checker
  uploadId: a65c7aca-9a1e-41a3-b7e5-826c82d250cf
  partFirstByte: 0
  partLastByte: 12110
responseElements:
  registryId: '765618022540'
  repositoryName: canary-checker
  uploadId: a65c7aca-9a1e-41a3-b7e5-826c82d250cf
  lastByteReceived: 12110
requestID: 0ca02483-63f4-48b5-be80-e96d9b6be0e2
eventID: 9bee5a29-4a1b-43dc-90b3-4c730c81097c
readOnly: false
resources:
- accountId: '765618022540'
  ARN: arn:aws:ecr-public::765618022540:repository/canary-checker
eventType: AwsApiCall
managementEvent: true
recipientAccountId: '765618022540'
eventCategory: Management
`,
			expectedCreatedBy: "arn:aws:iam::765618022540:user/github-actions-ecr",
		},
		{
			name: "Root User",
			eventRaw: `---
eventVersion: '1.10'
userIdentity:
  type: Root
  principalId: '765618022540'
  arn: arn:aws:iam::765618022540:root
  accountId: '765618022540'
  accessKeyId: AKIAIOSFODNN7EXAMPLE
  sessionContext:
    attributes:
      creationDate: '2025-05-22T13:35:48Z'
      mfaAuthenticated: 'true'
eventTime: '2025-05-22T13:45:18Z'
eventSource: ec2.amazonaws.com
eventName: DeleteVolume
awsRegion: eu-west-1
sourceIPAddress: 5.29.11.163
userAgent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML,
  likeGecko) Chrome/135.0.00 Safari/537.36
requestParameters:
  volumeId: vol-0651e3786cd54eb7d
  reportVolumeFailure: false
responseElements:
  requestId: 253bfaf9-df88-42c8-9f3f-bf860295438
  _return: true
requestID: 253bfaf9-df88-42c8-9f3f-bf8602954381
eventID: 18ad8fc0-3d6c-45a2-a2b6-6940ed293960
readOnly: false
eventType: AwsApiCall
managementEvent: true
recipientAccountId: '765618022540'
eventCategory: Management
tlsDetails:
  tlsVersion: TLSv1.3
  cipherSuite: TLS_AES_128_GCM_SHA256
  clientProvidedHostHeader: ec2.eu-west-1.amazonaws.com
sessionCredentialFromConsole: 'true'
`,
			expectedCreatedBy: "arn:aws:iam::765618022540:root",
		},
		{
			name: "Service Account",
			eventRaw: `---
userIdentity:
  type: AssumedRole
  principalId: AROABC123DEFGHIJKLMN:my-service
  arn: arn:aws:sts::123456789012:assumed-role/MyServiceRole/my-service
  accountId: "123456789012"
  sessionContext:
    attributes:
      creationDate: 2025-05-16T15:59:19Z
      mfaAuthenticated: "false"
    sessionIssuer:
      arn: arn:aws:iam::123456789012:role/MyServiceRole
      type: Role
      userName: MyServiceRole
      accountId: "123456789012"`,
			expectedCreatedBy: "arn:aws:sts::123456789012:assumed-role/MyServiceRole/my-service",
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

			change, err := cloudtrailEventToChange(event, types.Resource{})
			g.Expect(err).To(gomega.Succeed())
			g.Expect(change).To(gomega.Not(gomega.BeNil()))
			g.Expect(*change.CreatedBy).To(gomega.Equal(tt.expectedCreatedBy))
		})
	}
}
