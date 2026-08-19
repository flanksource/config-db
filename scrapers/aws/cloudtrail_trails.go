package aws

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	cloudtrailTypes "github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/flanksource/duty/types"
	"github.com/samber/lo"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
)

const IncludeCloudTrailTrails = "CloudTrailTrails"

func (aws Scraper) cloudtrailTrails(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes(IncludeCloudTrailTrails) {
		return
	}

	ctx.Logger.V(2).Infof("scraping CloudTrail trails")
	client := cloudtrail.NewFromConfig(*ctx.Session, getEndpointResolver[cloudtrail.Options](config))
	described, err := client.DescribeTrails(ctx, &cloudtrail.DescribeTrailsInput{
		IncludeShadowTrails: lo.ToPtr(false),
	})
	if err != nil {
		results.Errorf(err, "failed to describe CloudTrail trails")
		return
	}

	for _, trail := range described.TrailList {
		name := lo.FromPtr(trail.Name)
		trailARN := lo.FromPtr(trail.TrailARN)
		if name == "" || trailARN == "" {
			results.Errorf(fmt.Errorf("trail is missing its name or ARN"), "failed to scrape CloudTrail trail")
			continue
		}

		tagOutput, err := client.ListTags(ctx, &cloudtrail.ListTagsInput{ResourceIdList: []string{trailARN}})
		if err != nil {
			results.Errorf(err, "failed to list tags for CloudTrail trail %s", name)
			continue
		}
		labels := make(map[string]string)
		for _, resource := range tagOutput.ResourceTagList {
			for _, tag := range resource.TagsList {
				labels[lo.FromPtr(tag.Key)] = lo.FromPtr(tag.Value)
			}
		}
		if config.ShouldExclude(v1.AWSCloudTrailTrail, name, labels) {
			continue
		}

		statusOutput, err := client.GetTrailStatus(ctx, &cloudtrail.GetTrailStatusInput{Name: &trailARN})
		if err != nil {
			results.Errorf(err, "failed to get status for CloudTrail trail %s", name)
			continue
		}

		eventSelectorOutput, err := client.GetEventSelectors(ctx, &cloudtrail.GetEventSelectorsInput{TrailName: &trailARN})
		if err != nil {
			results.Errorf(err, "failed to get event selectors for CloudTrail trail %s", name)
			continue
		}

		var insightSelectorOutput *cloudtrail.GetInsightSelectorsOutput
		if lo.FromPtr(trail.HasInsightSelectors) {
			insightSelectorOutput, err = client.GetInsightSelectors(ctx, &cloudtrail.GetInsightSelectorsInput{TrailName: &trailARN})
			if err != nil {
				results.Errorf(err, "failed to get insight selectors for CloudTrail trail %s", name)
				continue
			}
		}

		trailConfig, err := cloudTrailTrailConfig(trail, statusOutput, eventSelectorOutput, insightSelectorOutput)
		if err != nil {
			results.Errorf(err, "failed to build config for CloudTrail trail %s", name)
			continue
		}

		status := ""
		if statusOutput.IsLogging != nil {
			if *statusOutput.IsLogging {
				status = "Logging"
			} else {
				status = "Stopped"
			}
		}

		*results = append(*results, v1.ScrapeResult{
			Type:        v1.AWSCloudTrailTrail,
			BaseScraper: config.BaseScraper,
			Config:      trailConfig,
			ConfigClass: "AuditTrail",
			Name:        name,
			ID:          trailARN,
			Aliases:     []string{trailARN},
			Labels:      labels,
			Status:      status,
			Parents:     []v1.ConfigExternalKey{{Type: v1.AWSAccount, ExternalID: lo.FromPtr(ctx.Caller.Account)}},
			Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSCloudTrailTrail, name, nil)},
		})
	}
}

// cloudTrailTrailConfig persists stable trail settings without the rolling
// delivery timestamps returned by GetTrailStatus, which would create a diff on
// nearly every scrape.
func cloudTrailTrailConfig(
	trail cloudtrailTypes.Trail,
	status *cloudtrail.GetTrailStatusOutput,
	events *cloudtrail.GetEventSelectorsOutput,
	insights *cloudtrail.GetInsightSelectorsOutput,
) (map[string]any, error) {
	config, err := utils.ToJSONMap(trail)
	if err != nil {
		return nil, err
	}

	if status != nil && status.IsLogging != nil {
		config["IsLogging"] = *status.IsLogging
	}
	if events != nil {
		if len(events.EventSelectors) > 0 {
			config["EventSelectors"] = events.EventSelectors
		}
		if len(events.AdvancedEventSelectors) > 0 {
			config["AdvancedEventSelectors"] = events.AdvancedEventSelectors
		}
	}
	if insights != nil {
		if len(insights.InsightSelectors) > 0 {
			config["InsightSelectors"] = insights.InsightSelectors
		}
		if insights.InsightsDestination != nil {
			config["InsightsDestination"] = *insights.InsightsDestination
		}
	}

	return config, nil
}
