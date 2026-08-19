package scrapers

import (
	"strings"
	"testing"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutyCtx "github.com/flanksource/duty/context"
)

// cloudTrailEventJSON is the raw CloudTrail record for the row below, exactly as
// ClickHouse returns it in the `event_json` column: a JSON string, not an object.
const cloudTrailEventJSON = `{"eventVersion":"1.11","userIdentity":{"type":"IAMUser","principalId":"AIDAEXAMPLEPRINCIPAL","arn":"arn:aws:iam::123456789012:user/example","accountId":"123456789012","userName":"example"},"eventTime":"2026-08-18T18:02:15Z","eventSource":"s3.amazonaws.com","eventName":"PutBucketTagging","awsRegion":"us-east-1","requestParameters":{"bucketName":"config-db-cloudtrail-test","Host":"config-db-cloudtrail-test.s3.us-east-1.amazonaws.com"},"responseElements":null,"eventID":"be0a8e39-f721-406c-8c87-67654f447465","readOnly":false,"resources":[{"accountId":"123456789012","type":"AWS::S3::Bucket","ARN":"arn:aws:s3:::config-db-cloudtrail-test"}],"eventType":"AwsApiCall","managementEvent":true,"recipientAccountId":"123456789012","eventCategory":"Management"}`

// cloudTrailRow is one row of the CloudTrail scrape query's result set.
//
// Every value is a string because QuerySQLContext scans all columns as
// sql.NullString, so `event_json` stays a JSON string rather than a nested map.
func cloudTrailRow() map[string]any {
	return map[string]any{
		"event_id":      "be0a8e39-f721-406c-8c87-67654f447465",
		"event_name":    "PutBucketTagging",
		"event_time":    "2026-08-18T18:02:15Z",
		"aws_region":    "us-east-1",
		"account_id":    "123456789012",
		"resource_name": "config-db-cloudtrail-test",
		"resource_type": "AWS::S3::Bucket",
		"event_json":    cloudTrailEventJSON,
	}
}

// changeTransform reshapes a query row into a standalone change against the
// config the CloudTrail event acted on.
const changeTransform = `
[{
  "changes": [{
    "external_id":        config.resource_name,
    "config_type":        config.resource_type,
    "scraper_id":         "all",
    "external_change_id": config.event_id,
    "change_type":        config.event_name,
    "created_at":         config.event_time,
    "severity":           "info",
    "source":             "CloudTrail"
  }]
}].toJSON()
`

func clickhouseScrapeContext(t *testing.T, full bool, base v1.BaseScraper) api.ScrapeContext {
	t.Helper()

	scrapeConfig := v1.ScrapeConfig{
		Spec: v1.ScraperSpec{
			Full: full,
			Clickhouse: []v1.Clickhouse{
				{BaseScraper: base, Query: "SELECT 1"},
			},
		},
	}
	return api.NewScrapeContext(dutyCtx.New()).WithScrapeConfig(&scrapeConfig)
}

func TestClickhouseCloudTrailRowToChange(t *testing.T) {
	base := v1.BaseScraper{
		CustomScraperBase: v1.CustomScraperBase{ID: "None", Type: "None"},
	}
	base.Transform.Expression = changeTransform

	ctx := clickhouseScrapeContext(t, true, base)
	results := processScrapeResult(ctx, v1.ScrapeResult{BaseScraper: base, Config: cloudTrailRow()})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if err := results[0].Error; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	changes := results[0].Changes
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	change := changes[0]
	for _, tc := range []struct{ field, got, want string }{
		{"ExternalID", change.ExternalID, "config-db-cloudtrail-test"},
		{"ConfigType", change.ConfigType, "AWS::S3::Bucket"},
		{"ScraperID", change.ScraperID, "all"},
		{"ExternalChangeID", change.ExternalChangeID, "be0a8e39-f721-406c-8c87-67654f447465"},
		{"ChangeType", change.ChangeType, "PutBucketTagging"},
		{"Severity", change.Severity, "info"},
		{"Source", change.Source, "CloudTrail"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.field, tc.got, tc.want)
		}
	}

	if change.CreatedAt == nil {
		t.Error("CreatedAt is nil")
	} else if got := change.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"); got != "2026-08-18T18:02:15Z" {
		t.Errorf("CreatedAt = %s, want 2026-08-18T18:02:15Z", got)
	}
}

// A row with no transform produces a config item and no changes, no matter that
// `full` is set: full mode only lifts an explicit `changes` key out of the config.
func TestClickhouseCloudTrailRowWithoutTransform(t *testing.T) {
	base := v1.BaseScraper{
		CustomScraperBase: v1.CustomScraperBase{
			ID:   "$.event_id",
			Name: "$.event_name",
			Type: "AWS::CloudTrail::Event",
		},
	}

	ctx := clickhouseScrapeContext(t, true, base)
	results := processScrapeResult(ctx, v1.ScrapeResult{BaseScraper: base, Config: cloudTrailRow()})

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if err := results[0].Error; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(results[0].Changes); got != 0 {
		t.Errorf("expected 0 changes, got %d", got)
	}
	if got := results[0].ID; got != "be0a8e39-f721-406c-8c87-67654f447465" {
		t.Errorf("ID = %q, want the event id", got)
	}
	if got := results[0].Type; got != "AWS::CloudTrail::Event" {
		t.Errorf("Type = %q, want AWS::CloudTrail::Event", got)
	}
}

// A changes-only transform needs placeholder id/type on the spec. extractAttributes
// runs before ExtractFullMode, and ScrapeResult.Changes is `json:"-"`, so at that
// point the `changes` key still lives only in .Config and cannot satisfy the id
// check. Without the placeholders the whole result errors out and the changes are
// silently dropped.
func TestClickhouseCloudTrailChangesNeedPlaceholderID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		typ     string
		wantErr string
	}{
		{
			name:    "id selector no longer resolves against the transformed config",
			id:      "$.event_id",
			typ:     "AWS::CloudTrail::Event",
			wantErr: "failed to extract attributes: $.event_id not found",
		},
		{
			name:    "no id at all",
			wantErr: "failed to extract attributes: no id defined",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := v1.BaseScraper{
				CustomScraperBase: v1.CustomScraperBase{ID: test.id, Type: test.typ},
			}
			base.Transform.Expression = changeTransform

			ctx := clickhouseScrapeContext(t, true, base)
			results := processScrapeResult(ctx, v1.ScrapeResult{BaseScraper: base, Config: cloudTrailRow()})

			if len(results) != 1 {
				t.Fatalf("expected 1 result, got %d", len(results))
			}
			if results[0].Error == nil {
				t.Fatalf("expected an error, got none")
			}
			if got := results[0].Error.Error(); !strings.Contains(got, test.wantErr) {
				t.Errorf("error = %q, want it to contain %q", got, test.wantErr)
			}
			if got := len(results[0].Changes); got != 0 {
				t.Errorf("expected the changes to be dropped, got %d", got)
			}
		})
	}
}
