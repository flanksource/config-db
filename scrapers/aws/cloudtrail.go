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
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
)

func lookupEvents(ctx *AWSContext, input *cloudtrail.LookupEventsInput, c chan<- types.Event) error {
	defer close(c)

	ctx.Logger.V(3).Infof("Looking up events from %v", input.StartTime)
	CloudTrail := cloudtrail.NewFromConfig(*ctx.Session)

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
		Arn            string `json:"arn"`
		Username       string `json:"userName"`
		SessionContext struct {
			SessionIssuer struct {
				Username string `json:"userName"`
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

			for _, resource := range event.Resources {
				change := v1.ChangeResult{
					CreatedAt:        event.EventTime,
					ExternalChangeID: *event.EventId,
					ChangeType:       *event.EventName,
					Details:          v1.NewJSON(*event.CloudTrailEvent),
					Source:           fmt.Sprintf("AWS::CloudTrail::%s:%s", ctx.Session.Region, *ctx.Caller.Account),
				}

				var cloudtrailEvent CloudTrailEvent
				if err := cloudtrailEvent.FromJSON(ptr.ToString(event.CloudTrailEvent)); err != nil {
					logger.Warnf("error parsing cloudtrail event: %v", err)
					ignored++
					continue
				}

				if cloudtrailEvent.UserIdentity.Username != "" {
					change.CreatedBy = &cloudtrailEvent.UserIdentity.Username
				} else if cloudtrailEvent.UserIdentity.SessionContext.SessionIssuer.Username != "" {
					change.CreatedBy = &cloudtrailEvent.UserIdentity.SessionContext.SessionIssuer.Username
				} else if event.Username != nil {
					change.CreatedBy = event.Username
				}

				change.Details["Event"] = *event.CloudTrailEvent
				if resource.ResourceName != nil {
					change.ExternalID = *resource.ResourceName
				}
				if resource.ResourceType != nil {
					change.ConfigType = *resource.ResourceType
				}

				results.AddChange(config.BaseScraper, change)
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
