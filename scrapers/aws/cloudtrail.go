package aws

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/aws/smithy-go/ptr"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/samber/lo"

	v1 "github.com/flanksource/config-db/api/v1"
)

func lookupEvents(ctx *AWSContext, input *cloudtrail.LookupEventsInput, c chan<- types.Event, config v1.AWS) error {
	defer close(c)

	ctx.Logger.V(3).Infof("Looking up events from %v", input.StartTime)
	CloudTrail := cloudtrail.NewFromConfig(*ctx.Session, getEndpointResolver[cloudtrail.Options](config), func(o *cloudtrail.Options) {
		o.Retryer = retry.NewStandard(func(so *retry.StandardOptions) {
			// Exponential backoff on rate limits: 1s, 2s, 4s, 8s, 16s, 32s, 60s
			so.MaxAttempts = 7 // 1 initial attempt + 6 retries
			so.MaxBackoff = 60 * time.Second
		})
	})

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
	AWSRegion          string `json:"awsRegion"`
	RecipientAccountID string `json:"recipientAccountId"`
	UserIdentity       struct {
		Type           string `json:"type"`
		Arn            string `json:"arn"`
		Username       string `json:"userName"`
		PrincipalID    string `json:"principalId"`
		AccountID      string `json:"accountId"`
		InvokedBy      string `json:"invokedBy"`
		SessionContext struct {
			Attributes struct {
				MfaAuthenticated string `json:"mfaAuthenticated"`
			} `json:"attributes"`
			SessionIssuer struct {
				Username string `json:"userName"`
				Arn      string `json:"arn"`
			} `json:"sessionIssuer"`
		} `json:"sessionContext"`
	} `json:"userIdentity"`
	RequestParameters struct {
		LogGroupName  string `json:"logGroupName"`
		LogStreamName string `json:"logStreamName"`
		RoleArn       string `json:"roleArn"`
	} `json:"requestParameters"`
	Resources []struct {
		ARN       string `json:"ARN"`
		AccountID string `json:"accountId"`
	} `json:"resources"`
}

func (t *CloudTrailEvent) FromJSON(j string) error {
	return json.Unmarshal([]byte(j), t)
}

func (aws Scraper) cloudtrail(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if config.Excludes("cloudtrail") {
		return
	}

	ctx.Logger.V(2).Infof("scraping cloudtrail")

	var lastEventKey = ctx.Session.Region + *ctx.Caller.Account
	c := make(chan types.Event)
	aggregator := newAccessLogAggregator()
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

			if isAssumeRoleEvent(lo.FromPtr(event.EventName)) {
				var ctEvent CloudTrailEvent
				if err := ctEvent.FromJSON(ptr.ToString(event.CloudTrailEvent)); err != nil {
					ctx.Logger.V(2).Infof("failed to parse AssumeRole event: %v", err)
					ignored++
					continue
				}
				if err := aggregator.addAssumeRole(event, ctEvent); err != nil {
					ctx.Logger.V(2).Infof("failed to aggregate AssumeRole event: %v", err)
					ignored++
				}
				continue
			}

			// Ignore ReadOnly events other than AssumeRole
			if lo.FromPtr(event.ReadOnly) == "true" {
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

		if !aggregator.isEmpty() {
			*results = append(*results, aggregator.flush())
		}
		LastEventTime.Store(lastEventKey, maxTime)
		ctx.Logger.V(3).Infof("processed %d cloudtrail events, changes=%d ignored=%d", count, len(*results), ignored)
		wg.Done()
	}()

	start := time.Now().Add(-1 * config.CloudTrail.GetMaxAge()).UTC()
	if lastEventTime, ok := LastEventTime.Load(lastEventKey); ok {
		start = lastEventTime.(time.Time)
	}

	err := lookupEvents(ctx, &cloudtrail.LookupEventsInput{StartTime: &start}, c, config)

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
	rawEventJSON := lo.FromPtr(event.CloudTrailEvent)
	eventName := lo.FromPtr(event.EventName)

	change := &v1.ChangeResult{
		CreatedAt:        event.EventTime,
		ExternalChangeID: lo.FromPtr(event.EventId),
		ChangeType:       eventName,
		Details:          v1.NewJSON(rawEventJSON),
	}

	if canonicalType, typedDetails, ok := classifyBackupEvent(eventName, rawEventJSON); ok {
		change.ChangeType = canonicalType
		change.Details = typedDetails
	}

	var cloudtrailEvent CloudTrailEvent
	if err := cloudtrailEvent.FromJSON(ptr.ToString(event.CloudTrailEvent)); err != nil {
		return nil, fmt.Errorf("error parsing cloudtrail event: %w", err)
	}

	switch cloudtrailEvent.UserIdentity.Type {
	case "AssumedRole":
		if cloudtrailEvent.UserIdentity.PrincipalID != "" {
			change.CreatedBy = lo.ToPtr(cloudtrailEvent.UserIdentity.SessionContext.SessionIssuer.Username)
		} else {
			splits := strings.Split(cloudtrailEvent.UserIdentity.Arn, "/")
			name := splits[len(splits)-1]
			change.CreatedBy = lo.ToPtr(name)
		}
	case "IAMUser":
		change.CreatedBy = lo.ToPtr(cloudtrailEvent.UserIdentity.Username)
	default:
		change.CreatedBy = lo.ToPtr(cloudtrailEvent.UserIdentity.Arn)
	}

	if resource.ResourceName != nil {
		change.ExternalID = *resource.ResourceName
	}
	if resource.ResourceType != nil {
		change.ConfigType = *resource.ResourceType
	}

	for _, resource := range cloudtrailEvent.Resources {
		if resource.ARN == "" {
			continue
		}
		change.ExternalID = resource.ARN
		if change.ConfigType == "" || !strings.HasPrefix(change.ConfigType, "AWS::") {
			change.ConfigType = cloudtrailEventToConfigType(resource.ARN, ptr.ToString(event.EventSource))
		}
		break
	}

	// CloudWatch Logs events often omit resource ARNs, so derive the log stream ARN from request parameters.
	if change.ExternalID == "" && ptr.ToString(event.EventSource) == "logs.amazonaws.com" {
		if arn := cloudwatchLogStreamARN(cloudtrailEvent); arn != "" {
			change.ExternalID = arn
			if change.ConfigType == "" || !strings.HasPrefix(change.ConfigType, "AWS::") {
				change.ConfigType = "AWS::Logs::LogStream"
			}
		}
	}

	return change, nil
}

// cloudwatchLogStreamARN builds a log stream ARN from CloudTrail request parameters.
// CloudTrail often omits ARNs for logs events, so we derive them from region/account/logGroup/logStream.
func cloudwatchLogStreamARN(event CloudTrailEvent) string {
	logGroup := event.RequestParameters.LogGroupName
	logStream := event.RequestParameters.LogStreamName
	if logGroup == "" || logStream == "" {
		return ""
	}

	region := event.AWSRegion
	accountID := event.RecipientAccountID
	if accountID == "" {
		accountID = event.UserIdentity.AccountID
	}
	if region == "" || accountID == "" {
		return ""
	}

	return fmt.Sprintf("arn:aws:logs:%s:%s:log-group:%s:log-stream:%s", region, accountID, logGroup, logStream)
}

// cloudtrailAssumeRoleToAccessLog converts a single AssumeRole* CloudTrail
// event into a ScrapeResult with one ExternalUser and one ConfigAccessLog.
// Retained as a thin wrapper over the aggregator's primitives so tests can
// exercise the per-event shape in isolation. The production cloudtrail()
// loop uses the aggregator directly for count/MFA accumulation.
func cloudtrailAssumeRoleToAccessLog(event types.Event) (*v1.ScrapeResult, error) {
	var ctEvent CloudTrailEvent
	if err := ctEvent.FromJSON(ptr.ToString(event.CloudTrailEvent)); err != nil {
		return nil, fmt.Errorf("error parsing cloudtrail event: %w", err)
	}
	roleARN := assumeRoleTargetARN(ctEvent)
	if roleARN == "" {
		return nil, fmt.Errorf("AssumeRole event has no role ARN")
	}
	caller, err := extractCaller(ctEvent)
	if err != nil {
		return nil, err
	}
	if caller == nil {
		return nil, nil
	}

	var eventTime time.Time
	if event.EventTime != nil {
		eventTime = *event.EventTime
	}

	return &v1.ScrapeResult{
		ExternalUsers: []dutyModels.ExternalUser{caller.User},
		ConfigAccessLogs: []v1.ExternalConfigAccessLog{{
			ConfigAccessLog: dutyModels.ConfigAccessLog{
				ExternalUserID: caller.User.ID,
				CreatedAt:      eventTime,
				MFA:            caller.MFA,
			},
			ConfigExternalID: v1.ExternalID{
				ConfigType: v1.AWSIAMRole,
				ExternalID: roleARN,
			},
		}},
	}, nil
}

func cloudtrailEventToConfigType(resourceARN, eventSource string) string {
	service := ""
	if resourceARN != "" {
		parts := strings.SplitN(resourceARN, ":", 6)
		if len(parts) >= 3 {
			service = parts[2]
		}
	}

	if service == "" && eventSource != "" {
		service = strings.TrimSuffix(eventSource, ".amazonaws.com")
	}

	switch service {
	case "ecr", "ecr-public":
		return "AWS::ECR::Repository"
	}

	return ""
}
