package aws

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/aws/smithy-go/ptr"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/samber/lo"
)

func lookupEvents(ctx *AWSContext, input *cloudtrail.LookupEventsInput, c chan<- types.Event, config v1.AWS) error {
	defer close(c)

	ctx.Logger.V(3).Infof("Looking up events from %v", input.StartTime)
	CloudTrail := cloudtrail.NewFromConfig(*ctx.Session, getEndpointResolver[cloudtrail.Options](config))

	var total int
	for {
		events, err := CloudTrail.LookupEvents(ctx, input)
		if err != nil {
			return err
		}

		total += len(events.Events)
		ctx.Logger.V(3).Infof("fetched %d cloudtrail events so far", total)

		for _, event := range events.Events {
			c <- event
		}

		if events.NextToken == nil {
			break
		}

		input.NextToken = events.NextToken
	}

	ctx.Logger.V(1).Infof("fetched %d cloudtrail events in total", total)
	return nil
}

var LastEventTime = sync.Map{}

type CloudTrailEvent struct {
	UserIdentity struct {
		Type           string `json:"type"`
		Arn            string `json:"arn"`
		Username       string `json:"userName"`
		SessionContext struct {
			SessionIssuer struct {
				Username string `json:"userName"`
				Arn      string `json:"arn"`
			} `json:"sessionIssuer"`
		} `json:"sessionContext"`
	} `json:"userIdentity"`
}

func (t *CloudTrailEvent) FromJSON(j string) error {
	return json.Unmarshal([]byte(j), t)
}

func (aws Scraper) cloudtrail(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if config.Excludes("cloudtrail") {
		return
	}

	ctx.Logger.V(2).Infof("scraping cloudtrail")

	if len(config.CloudTrail.Exclude) == 0 {
		config.CloudTrail.Exclude = []string{"AssumeRole"}
	}

	var lastEventKey = ctx.Session.Region + *ctx.Caller.Account
	c := make(chan types.Event)
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		count := 0
		ignored := 0
		var maxTime time.Time
		for event := range c {
			if event.EventTime != nil && event.EventTime.After(maxTime) {
				maxTime = *event.EventTime
			}
			count++
			if containsAny(config.CloudTrail.Exclude, *event.EventName) {
				ignored++
				continue
			}

			// If there's no resource, we add an empty resource as
			// we still want to have a change representing it.
			if len(event.Resources) == 0 {
				event.Resources = []types.Resource{{}}
			}

			for _, resource := range event.Resources {
				change, err := cloudtrailEventToChange(event, resource)
				if err != nil {
					results.Errorf(err, "failed to convert cloudtrail event to change")
					ignored++
					continue
				}

				change.Source = fmt.Sprintf("AWS::CloudTrail::%s:%s", ctx.Session.Region, *ctx.Caller.Account)
				results.AddChange(config.BaseScraper, *change)
			}
		}

		LastEventTime.Store(lastEventKey, maxTime)
		ctx.Logger.V(3).Infof("processed %d cloudtrail events, changes=%d ignored=%d", count, len(*results), ignored)
		wg.Done()
	}()

	start := time.Now().Add(-1 * config.CloudTrail.GetMaxAge()).UTC()
	if lastEventTime, ok := LastEventTime.Load(lastEventKey); ok {
		start = lastEventTime.(time.Time)
	}
	err := lookupEvents(ctx, &cloudtrail.LookupEventsInput{
		StartTime: &start,
		LookupAttributes: []types.LookupAttribute{
			{
				AttributeKey:   types.LookupAttributeKeyReadOnly,
				AttributeValue: strPtr("false"),
			},
		},
	}, c, config)

	if err != nil {
		results.Errorf(err, "Failed to describe cloudtrail events")
	}
	wg.Wait()
}

func containsAny(a []string, v string) bool {
	for _, e := range a {
		if strings.HasPrefix(v, e) {
			return true
		}
	}
	return false
}

func cloudtrailEventToChange(event types.Event, resource types.Resource) (*v1.ChangeResult, error) {
	change := &v1.ChangeResult{
		CreatedAt:        event.EventTime,
		ExternalChangeID: lo.FromPtr(event.EventId),
		ChangeType:       lo.FromPtr(event.EventName),
		Details:          v1.NewJSON(lo.FromPtr(event.CloudTrailEvent)),
	}

	var cloudtrailEvent CloudTrailEvent
	if err := cloudtrailEvent.FromJSON(ptr.ToString(event.CloudTrailEvent)); err != nil {
		return nil, fmt.Errorf("error parsing cloudtrail event: %w", err)
	}

	change.CreatedBy = lo.ToPtr(
		lo.CoalesceOrEmpty(
			cloudtrailEvent.UserIdentity.Arn,
			cloudtrailEvent.UserIdentity.Username,
			cloudtrailEvent.UserIdentity.SessionContext.SessionIssuer.Arn,
			cloudtrailEvent.UserIdentity.SessionContext.SessionIssuer.Username,
			lo.FromPtr(event.Username),
		),
	)

	if resource.ResourceName != nil {
		change.ExternalID = *resource.ResourceName
	}
	if resource.ResourceType != nil {
		change.ConfigType = *resource.ResourceType
	}

	return change, nil
}
