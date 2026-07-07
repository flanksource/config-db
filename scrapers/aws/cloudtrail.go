package aws

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
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

var cloudTrailS3Cursors sync.Map

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

type cloudtrailLogFile struct {
	Records []json.RawMessage `json:"Records"`
}

type cloudtrailS3Cursor struct {
	LastKey      string     `json:"last_key,omitempty"`
	LastModified *time.Time `json:"last_modified,omitempty"`
}

type cloudtrailS3Object struct {
	Key          string
	LastModified *time.Time
}

func (aws Scraper) cloudtrail(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if config.Excludes("cloudtrail") {
		return
	}

	if config.CloudTrail.SourceType() == "s3" {
		aws.cloudtrailS3(ctx, config, results)
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
			ignoredEvent, err := aws.processCloudTrailEvent(ctx, config, results, aggregator, event)
			if err != nil {
				results.Errorf(err, "failed to convert cloudtrail event to change")
				ignored++
				continue
			}
			if ignoredEvent {
				ignored++
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

func (aws Scraper) processCloudTrailEvent(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults, aggregator *accessLogAggregator, event types.Event) (bool, error) {
	eventName := lo.FromPtr(event.EventName)
	if containsAny(config.CloudTrail.Exclude, eventName) {
		return true, nil
	}

	if isAssumeRoleEvent(eventName) {
		var ctEvent CloudTrailEvent
		if err := ctEvent.FromJSON(ptr.ToString(event.CloudTrailEvent)); err != nil {
			ctx.Logger.V(2).Infof("failed to parse AssumeRole event: %v", err)
			return true, nil
		}
		if err := aggregator.addAssumeRole(event, ctEvent); err != nil {
			ctx.Logger.V(2).Infof("failed to aggregate AssumeRole event: %v", err)
			return true, nil
		}
		return false, nil
	}

	if lo.FromPtr(event.ReadOnly) == "true" {
		return false, nil
	}

	if len(event.Resources) == 0 {
		event.Resources = []types.Resource{{}}
	}

	for _, resource := range event.Resources {
		change, err := cloudtrailEventToChange(event, resource)
		if err != nil {
			return false, err
		}

		change.Source = fmt.Sprintf("AWS::CloudTrail::%s:%s", ctx.Session.Region, *ctx.Caller.Account)
		results.AddChange(config.BaseScraper, *change)
	}
	return false, nil
}

// cloudtrailS3 reads CloudTrail log files from S3 and emits the same changes/access logs as the API path.
func (aws Scraper) cloudtrailS3(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	s3Config := config.CloudTrail.S3
	if s3Config == nil || s3Config.Bucket == "" {
		results.Errorf(fmt.Errorf("cloudtrail.s3.bucket is required when cloudtrail source is s3"), "failed to scrape cloudtrail s3")
		return
	}
	if ctx.Session.Region == "" {
		results.Errorf(fmt.Errorf("aws region is required to read cloudtrail logs from s3"), "failed to scrape cloudtrail s3")
		return
	}

	rootPrefix := cloudtrailS3RootPrefix(ctx, config.CloudTrail)
	cursorKey := cloudtrailS3CursorKey(s3Config.Bucket, ctx.Session.Region, rootPrefix)
	cursor, _ := cloudtrailS3CursorFromMemory(cursorKey)

	session := ctx.Session.Copy()
	if s3Config.BucketRegion != "" {
		session.Region = s3Config.BucketRegion
	}
	client := s3.NewFromConfig(session, getEndpointResolver[s3.Options](config))

	now := time.Now().UTC()
	start := now.Add(-config.CloudTrail.GetMaxAge()).UTC()
	if cursor.LastModified != nil {
		start = cursor.LastModified.UTC()
	}

	ctx.Logger.V(2).Infof("scraping cloudtrail from s3 bucket=%s prefix=%s region=%s", s3Config.Bucket, rootPrefix, ctx.Session.Region)
	objects, err := aws.listCloudTrailS3Objects(ctx, client, s3Config.Bucket, rootPrefix, ctx.Session.Region, start, now)
	if err != nil {
		results.Errorf(err, "failed to list cloudtrail s3 logs")
		return
	}

	aggregator := newAccessLogAggregator()
	count := 0
	ignored := 0
	processedFiles := 0
	latest := cursor
	for _, object := range objects {
		if !cloudtrailS3ObjectAfterCursor(object, cursor) {
			continue
		}

		events, err := aws.readCloudTrailS3Object(ctx, client, s3Config.Bucket, object.Key)
		if err != nil {
			results.Errorf(err, "failed to read cloudtrail s3 object %s", object.Key)
			return
		}

		for _, event := range events {
			count++
			ignoredEvent, err := aws.processCloudTrailEvent(ctx, config, results, aggregator, event)
			if err != nil {
				results.Errorf(err, "failed to convert cloudtrail s3 event from %s", object.Key)
				return
			}
			if ignoredEvent {
				ignored++
			}
		}

		processedFiles++
		latest.LastKey = object.Key
		if object.LastModified != nil {
			lastModified := object.LastModified.UTC()
			latest.LastModified = &lastModified
		}
	}

	if !aggregator.isEmpty() {
		*results = append(*results, aggregator.flush())
	}
	if processedFiles > 0 {
		cloudTrailS3Cursors.Store(cursorKey, latest)
	}
	ctx.Logger.V(3).Infof("processed %d cloudtrail s3 events from %d files, changes=%d ignored=%d", count, processedFiles, len(*results), ignored)
}

func (aws Scraper) listCloudTrailS3Objects(ctx *AWSContext, client *s3.Client, bucket, rootPrefix, region string, start, end time.Time) ([]cloudtrailS3Object, error) {
	var objects []cloudtrailS3Object
	for _, prefix := range cloudtrailS3DatePrefixes(rootPrefix, region, start, end) {
		paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
			Bucket: lo.ToPtr(bucket),
			Prefix: lo.ToPtr(prefix),
		})
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, object := range page.Contents {
				key := lo.FromPtr(object.Key)
				if key == "" || !strings.HasSuffix(key, ".json.gz") {
					continue
				}
				objects = append(objects, cloudtrailS3Object{Key: key, LastModified: object.LastModified})
			}
		}
	}

	sort.Slice(objects, func(i, j int) bool {
		return objects[i].Key < objects[j].Key
	})
	return objects, nil
}

func (aws Scraper) readCloudTrailS3Object(ctx *AWSContext, client *s3.Client, bucket, key string) ([]types.Event, error) {
	object, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: lo.ToPtr(bucket), Key: lo.ToPtr(key)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = object.Body.Close() }()

	events, err := decodeCloudTrailS3LogFile(object.Body)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", key, err)
	}
	return events, nil
}

// decodeCloudTrailS3LogFile expands one gzipped CloudTrail log envelope into SDK-like events.
func decodeCloudTrailS3LogFile(r io.Reader) ([]types.Event, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()

	var logFile cloudtrailLogFile
	if err := json.NewDecoder(gz).Decode(&logFile); err != nil {
		return nil, err
	}

	events := make([]types.Event, 0, len(logFile.Records))
	for i, raw := range logFile.Records {
		event, err := cloudtrailRawRecordToEvent(raw)
		if err != nil {
			return nil, fmt.Errorf("record %d: %w", i, err)
		}
		events = append(events, event)
	}
	return events, nil
}

// cloudtrailRawRecordToEvent adapts S3 CloudTrail JSON records to the LookupEvents shape used downstream.
func cloudtrailRawRecordToEvent(raw json.RawMessage) (types.Event, error) {
	var record struct {
		EventID     string            `json:"eventID"`
		EventName   string            `json:"eventName"`
		EventSource string            `json:"eventSource"`
		EventTime   *time.Time        `json:"eventTime"`
		ReadOnly    any               `json:"readOnly"`
		Resources   []json.RawMessage `json:"resources"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return types.Event{}, err
	}

	event := types.Event{
		CloudTrailEvent: lo.ToPtr(string(raw)),
		EventId:         lo.ToPtr(record.EventID),
		EventName:       lo.ToPtr(record.EventName),
		EventSource:     lo.ToPtr(record.EventSource),
		ReadOnly:        cloudtrailReadOnly(record.ReadOnly),
	}
	if record.EventTime != nil {
		eventTime := record.EventTime.UTC()
		event.EventTime = &eventTime
	}

	for _, rawResource := range record.Resources {
		resource := cloudtrailRawResourceToSDKResource(rawResource)
		event.Resources = append(event.Resources, resource)
	}
	return event, nil
}

func cloudtrailRawResourceToSDKResource(raw json.RawMessage) types.Resource {
	var resource struct {
		ARN          string `json:"ARN"`
		ResourceName string `json:"resourceName"`
		ResourceType string `json:"resourceType"`
		Type         string `json:"type"`
	}
	_ = json.Unmarshal(raw, &resource)

	name := resource.ResourceName
	if name == "" {
		name = resource.ARN
	}
	resourceType := resource.ResourceType
	if resourceType == "" {
		resourceType = resource.Type
	}
	return types.Resource{ResourceName: lo.ToPtr(name), ResourceType: lo.ToPtr(resourceType)}
}

func cloudtrailReadOnly(v any) *string {
	switch value := v.(type) {
	case bool:
		return lo.ToPtr(strconv.FormatBool(value))
	case string:
		if value == "" {
			return nil
		}
		return lo.ToPtr(value)
	default:
		return nil
	}
}

func cloudtrailS3RootPrefix(ctx *AWSContext, config v1.CloudTrail) string {
	if config.S3 != nil && config.S3.Prefix != "" {
		return strings.Trim(config.S3.Prefix, "/")
	}
	return fmt.Sprintf("AWSLogs/%s/CloudTrail", lo.FromPtr(ctx.Caller.Account))
}

func cloudtrailS3CursorKey(bucket, region, prefix string) string {
	sum := sha256.Sum256([]byte(prefix))
	return fmt.Sprintf("aws.cloudtrail.s3.%s.%s.%s", bucket, region, hex.EncodeToString(sum[:])[:12])
}

func cloudtrailS3CursorFromMemory(cursorKey string) (cloudtrailS3Cursor, bool) {
	raw, ok := cloudTrailS3Cursors.Load(cursorKey)
	if !ok {
		return cloudtrailS3Cursor{}, false
	}
	cursor, ok := raw.(cloudtrailS3Cursor)
	return cursor, ok
}

func cloudtrailS3DatePrefixes(rootPrefix, region string, start, end time.Time) []string {
	rootPrefix = strings.Trim(rootPrefix, "/")
	startDay := time.Date(start.UTC().Year(), start.UTC().Month(), start.UTC().Day(), 0, 0, 0, 0, time.UTC)
	endDay := time.Date(end.UTC().Year(), end.UTC().Month(), end.UTC().Day(), 0, 0, 0, 0, time.UTC)
	prefixes := []string{}
	for day := startDay; !day.After(endDay); day = day.AddDate(0, 0, 1) {
		prefixes = append(prefixes, fmt.Sprintf("%s/%s/%04d/%02d/%02d/", rootPrefix, region, day.Year(), day.Month(), day.Day()))
	}
	return prefixes
}

func cloudtrailS3ObjectAfterCursor(object cloudtrailS3Object, cursor cloudtrailS3Cursor) bool {
	if cursor.LastKey != "" {
		return object.Key > cursor.LastKey
	}
	if cursor.LastModified != nil && object.LastModified != nil {
		return object.LastModified.After(*cursor.LastModified)
	}
	return true
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
