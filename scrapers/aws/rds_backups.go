package aws

import (
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdsTypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/samber/lo"

	v1 "github.com/flanksource/config-db/api/v1"
)

// rdsSnapshotTypes lists the RDS snapshot types that feed the backup
// lifecycle. The three values correspond to the only non-error arguments
// accepted by rds:DescribeDBSnapshots.SnapshotType (see aws cli
// InvalidParameterValue: "must be one of: public, shared, manual, awsbackup,
// automated"). "public" and "shared" are excluded because they are
// cross-account references, not owned backups.
var rdsSnapshotTypes = []string{"automated", "manual", "awsbackup"}

// rdsBackups polls DescribeDBSnapshots for the three owned snapshot types
// and emits one ConfigChange per snapshot. The snapshot ARN is used as the
// ExternalChangeID so that the status transition creating → available is
// upserted onto the same config_changes row via the ON CONFLICT clause in
// ConfigChange.BeforeCreate.
func (aws Scraper) rdsBackups(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) error {
	if !config.Includes(IncludeRDSBackups) {
		return nil
	}

	ctx.Logger.V(2).Infof("scraping RDS backups (snapshots)")

	client := rds.NewFromConfig(*ctx.Session, getEndpointResolver[rds.Options](config))

	var changes []v1.ChangeResult
	for _, snapshotType := range rdsSnapshotTypes {
		paginator := rds.NewDescribeDBSnapshotsPaginator(client, &rds.DescribeDBSnapshotsInput{
			SnapshotType: lo.ToPtr(snapshotType),
		})
		for paginator.HasMorePages() {
			out, err := paginator.NextPage(ctx)
			if err != nil {
				return fmt.Errorf("failed to list RDS snapshots (type=%s): %w", snapshotType, err)
			}
			for _, s := range out.DBSnapshots {
				if cr, ok := rdsSnapshotToChange(ctx, s); ok {
					changes = append(changes, cr)
				}
			}
		}
	}

	if len(changes) == 0 {
		return nil
	}

	result := v1.NewScrapeResult(config.BaseScraper)
	result.Changes = changes
	*results = append(*results, *result)
	return nil
}

// rdsSnapshotToChange maps a DescribeDBSnapshots result to a ChangeResult.
// Returns ok=false for statuses that should not produce a row (unknown
// states). The ExternalChangeID is the snapshot ARN for lifecycle
// transitions (creating/available/failed) and ARN+"-deleted" for the
// terminal deletion event — the suffix keeps the deletion as its own row
// instead of overwriting the historical BackupCompleted record.
func rdsSnapshotToChange(ctx *AWSContext, s rdsTypes.DBSnapshot) (v1.ChangeResult, bool) {
	changeType, status, severity, distinctKey, skip := rdsSnapshotOutcome(s.Status)
	if skip {
		return v1.ChangeResult{}, false
	}

	arn := lo.FromPtr(s.DBSnapshotArn)
	externalChangeID := arn
	if distinctKey {
		externalChangeID = arn + "-deleted"
	}

	endTimestamp := ""
	if status == types.StatusCompleted {
		endTimestamp = timeToRFC3339(s.SnapshotCreateTime)
	}

	typed := types.Backup{
		Event: types.Event{
			ID:         arn,
			Timestamp:  timeToRFC3339(s.SnapshotCreateTime),
			Tags:       rdsTagsToMap(s.TagList),
			Properties: rdsSnapshotProperties(s),
		},
		BackupType:   types.BackupTypeSnapshot,
		Status:       status,
		EndTimestamp: endTimestamp,
		Environment:  awsEnvironment(ctx),
		CreatedBy:    rdsSnapshotCreatedBy(s),
	}

	return v1.ChangeResult{
		ConfigType:       v1.AWSRDSInstance,
		ExternalID:       lo.FromPtr(s.DBInstanceIdentifier),
		ExternalChangeID: externalChangeID,
		ChangeType:       changeType,
		Source:           SourceRDSBackups,
		Severity:         string(severity),
		Summary: fmt.Sprintf("%s snapshot %s %s",
			lo.FromPtr(s.SnapshotType), lo.FromPtr(s.DBSnapshotIdentifier), strings.ToLower(lo.FromPtr(s.Status))),
		CreatedAt: s.SnapshotCreateTime,
		ScraperID: "all",
		Details:   v1.ChangeDetailsWithRaw(typed, s),
	}, true
}

// rdsSnapshotOutcome maps DBSnapshot.Status to the canonical change_type,
// typed Status, severity, a distinctKey flag indicating whether the event
// should be emitted on its own ExternalChangeID (rather than the snapshot
// ARN), and a skip flag for unknown/transient states.
func rdsSnapshotOutcome(s *string) (changeType string, status types.Status, severity models.Severity, distinctKey, skip bool) {
	switch strings.ToLower(lo.FromPtr(s)) {
	case "creating":
		return types.ChangeTypeBackupStarted, types.StatusRunning, models.SeverityInfo, false, false
	case "available":
		return types.ChangeTypeBackupCompleted, types.StatusCompleted, models.SeverityInfo, false, false
	case "failed":
		return types.ChangeTypeBackupFailed, types.StatusFailed, models.SeverityHigh, false, false
	case "deleting", "deleted":
		return types.ChangeTypeBackupDeleted, types.Status("Deleted"), models.SeverityInfo, true, false
	default:
		return "", "", "", false, true
	}
}
