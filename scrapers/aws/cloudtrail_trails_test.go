package aws

import (
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	cloudtrailTypes "github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/samber/lo"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CloudTrail trail config", func() {
	It("includes logging and selector settings without volatile delivery timestamps", func() {
		trail := cloudtrailTypes.Trail{
			Name:     lo.ToPtr("audit"),
			TrailARN: lo.ToPtr("arn:aws:cloudtrail:eu-west-1:111111111111:trail/audit"),
		}
		status := &cloudtrail.GetTrailStatusOutput{
			IsLogging:          lo.ToPtr(true),
			LatestDeliveryTime: lo.ToPtr(time.Now()),
		}
		events := &cloudtrail.GetEventSelectorsOutput{
			EventSelectors: []cloudtrailTypes.EventSelector{{
				IncludeManagementEvents: lo.ToPtr(true),
				ReadWriteType:           cloudtrailTypes.ReadWriteTypeAll,
			}},
		}
		insights := &cloudtrail.GetInsightSelectorsOutput{
			InsightSelectors: []cloudtrailTypes.InsightSelector{{
				InsightType: cloudtrailTypes.InsightTypeApiCallRateInsight,
			}},
		}

		config, err := cloudTrailTrailConfig(trail, status, events, insights)

		Expect(err).NotTo(HaveOccurred())
		Expect(config).To(HaveKeyWithValue("Name", "audit"))
		Expect(config).To(HaveKeyWithValue("IsLogging", true))
		Expect(config).To(HaveKeyWithValue("EventSelectors", events.EventSelectors))
		Expect(config).To(HaveKeyWithValue("InsightSelectors", insights.InsightSelectors))
		Expect(config).NotTo(HaveKey("LatestDeliveryTime"))
	})
})
