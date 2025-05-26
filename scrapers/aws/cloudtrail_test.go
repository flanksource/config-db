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
	g := gomega.NewWithT(t)

	var eventRaw = `userIdentity:
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
    webIdFederationData: {}`

	var eventMap map[string]any
	err := yaml.Unmarshal([]byte(eventRaw), &eventMap)
	g.Expect(err).To(gomega.Succeed())

	eventJSON, err := json.Marshal(eventMap)
	g.Expect(err).To(gomega.Succeed())

	event := types.Event{
		CloudTrailEvent: lo.ToPtr(string(eventJSON)),
	}

	change, err := cloudtrailEventToChange(event, types.Resource{})
	g.Expect(err).To(gomega.Succeed())
	g.Expect(change).To(gomega.Not(gomega.BeNil()))
	g.Expect(*change.CreatedBy).To(gomega.Equal("arn:aws:sts::4324:assumed-role/Administrators/john"))
}
