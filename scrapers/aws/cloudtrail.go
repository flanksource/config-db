package aws

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/aws/smithy-go/ptr"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
)

func lookupEvents(ctx *AWSContext, input *cloudtrail.LookupEventsInput, c chan types.Event) error {
	logger.Debugf("Looking up events from %s", input.StartTime)
	CloudTrail := cloudtrail.NewFromConfig(*ctx.Session)
	defer func() {
		close(c)
	}()
	events, err := CloudTrail.LookupEvents(ctx, input)
	if err != nil {
		return err
	}
	for _, event := range events.Events {
		c <- event
	}
	for events.NextToken != nil {
		input.NextToken = events.NextToken
		events, err = CloudTrail.LookupEvents(ctx, input)
		if err != nil {
			return err
		}
		for _, event := range events.Events {
			c <- event
		}
	}
	return nil
}

var LastEventTime = sync.Map{}

func (aws Scraper) cloudtrail(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if config.Excludes("cloudtrail") {
		return
	}
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

			for _, resource := range event.Resources {
				change := v1.ChangeResult{
					CreatedAt:        event.EventTime,
					ExternalChangeID: *event.EventId,
					ChangeType:       *event.EventName,
					Details:          v1.NewJSON(*event.CloudTrailEvent),
					Source:           fmt.Sprintf("AWS::CloudTrail::%s:%s", ctx.Session.Region, *ctx.Caller.Account),
				}

				if resource.ResourceName != nil {
					change.ExternalID = *resource.ResourceName
				}
				if resource.ResourceType != nil {
					change.ExternalType = *resource.ResourceType
				}

				results.AddChange(change)
			}
		}
		LastEventTime.Store(lastEventKey, maxTime)
		logger.Infof("Processed %d events, changes=%d ignored=%d", count, len(*results), ignored)
		wg.Done()
	}()

	start := time.Now().Add(-1 * config.CloudTrail.GetMaxAge()).UTC()
	if lastEventTime, ok := LastEventTime.Load(lastEventKey); ok {
		start = lastEventTime.(time.Time)
	}
	err := lookupEvents(ctx, &cloudtrail.LookupEventsInput{
		StartTime:  &start,
		MaxResults: ptr.Int32(1000),
		LookupAttributes: []types.LookupAttribute{
			{
				AttributeKey:   types.LookupAttributeKeyReadOnly,
				AttributeValue: strPtr("false"),
			},
		},
	}, c)

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
