package aws

import (
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/backup"
	backupTypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/samber/lo"

	v1 "github.com/flanksource/config-db/api/v1"
)

const (
	IncludeAWSBackups = "Backups"
)

func (aws Scraper) awsBackups(ctx *AWSContext, config v1.AWS, results *v1.ScrapeResults) {
	if !config.Includes(IncludeAWSBackups) {
		return
	}

	ctx.Logger.V(2).Infof("scraping AWS Backup")

	backupClient := backup.NewFromConfig(*ctx.Session, getEndpointResolver[backup.Options](config))

	if err := aws.scrapeBackupVaults(ctx, config, backupClient, results); err != nil {
		results.Errorf(err, "failed to scrape backup vaults")
	}

	if err := aws.scrapeBackupPlans(ctx, config, backupClient, results); err != nil {
		results.Errorf(err, "failed to scrape backup plans")
	}

	if err := aws.scrapeRecoveryPoints(ctx, config, backupClient, results); err != nil {
		results.Errorf(err, "failed to scrape recovery points")
	}

	if err := aws.scrapeBackupJobs(ctx, config, backupClient, results); err != nil {
		results.Errorf(err, "failed to scrape backup jobs")
	}

	if err := aws.scrapeRestoreJobs(ctx, config, backupClient, results); err != nil {
		results.Errorf(err, "failed to scrape restore jobs")
	}
}

func resourceTypeToConfigType(resourceType string) string {
	switch resourceType {
	case "RDS":
		return v1.AWSRDSInstance
	case "EBS":
		return v1.AWSEBSVolume
	case "EC2":
		return v1.AWSEC2Instance
	case "EFS":
		return v1.AWSEFSFileSystem
	case "S3":
		return v1.AWSS3Bucket
	case "DynamoDB":
		return v1.AWSDynamoDBTable
	}
	return ""
}

// awsBackupJobOutcome maps an AWS BackupJobState to the canonical v1 change
// type, the typed Status, and the per-state Severity. skip=true means the
// state is in-progress or otherwise not a persistable change.
func awsBackupJobOutcome(s backupTypes.BackupJobState) (changeType string, status types.Status, severity models.Severity, skip bool) {
	switch s {
	case backupTypes.BackupJobStateCompleted:
		return types.ChangeTypeBackupCompleted, types.StatusCompleted, models.SeverityInfo, false
	case backupTypes.BackupJobStateFailed:
		return types.ChangeTypeBackupFailed, types.StatusFailed, models.SeverityHigh, false
	case backupTypes.BackupJobStateAborted, backupTypes.BackupJobStateExpired, backupTypes.BackupJobStatePartial:
		return types.ChangeTypeBackupFailed, types.Status(lo.PascalCase(string(s))), models.SeverityMedium, false
	default:
		// Created / Pending / Running / Aborting — in-progress, skip
		return "", "", "", true
	}
}

// awsRestoreJobOutcome maps an AWS RestoreJobStatus to the canonical v1
// change type (always BackupRestored for terminal states) and typed Status.
func awsRestoreJobOutcome(s backupTypes.RestoreJobStatus) (changeType string, status types.Status, severity models.Severity, skip bool) {
	switch s {
	case backupTypes.RestoreJobStatusCompleted:
		return types.ChangeTypeBackupRestored, types.StatusCompleted, models.SeverityInfo, false
	case backupTypes.RestoreJobStatusFailed:
		return types.ChangeTypeBackupRestored, types.StatusFailed, models.SeverityHigh, false
	case backupTypes.RestoreJobStatusAborted:
		return types.ChangeTypeBackupRestored, types.Status("Aborted"), models.SeverityMedium, false
	default:
		return "", "", "", true
	}
}

// awsRecoveryPointOutcome maps a RecoveryPointStatus to the canonical v1
// change type and typed Status. distinctKey=true signals that the caller
// must use a separate ExternalChangeID (ARN+"-deleted") so the deletion
// event is recorded alongside the historical BackupCompleted row rather
// than overwriting it.
func awsRecoveryPointOutcome(s backupTypes.RecoveryPointStatus) (changeType string, status types.Status, severity models.Severity, distinctKey, skip bool) {
	switch s {
	case backupTypes.RecoveryPointStatusCompleted:
		return types.ChangeTypeBackupCompleted, types.StatusCompleted, models.SeverityInfo, false, false
	case backupTypes.RecoveryPointStatusPartial:
		return types.ChangeTypeBackupFailed, types.Status("Partial"), models.SeverityMedium, false, false
	case backupTypes.RecoveryPointStatusExpired:
		return types.ChangeTypeBackupFailed, types.Status("Expired"), models.SeverityLow, false, false
	case backupTypes.RecoveryPointStatusDeleting:
		return types.ChangeTypeBackupDeleted, types.Status("Deleted"), models.SeverityInfo, true, false
	default:
		return "", "", "", false, true
	}
}

func (aws Scraper) scrapeBackupVaults(ctx *AWSContext, config v1.AWS, client *backup.Client, results *v1.ScrapeResults) error {
	ctx.Logger.V(3).Infof("scraping backup vaults")

	paginator := backup.NewListBackupVaultsPaginator(client, &backup.ListBackupVaultsInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("listing backup vaults: %w", err)
		}

		for _, vault := range output.BackupVaultList {
			arn := lo.FromPtr(vault.BackupVaultArn)
			name := lo.FromPtr(vault.BackupVaultName)
			if config.ShouldExclude(v1.AWSBackupVault, name, nil) {
				continue
			}

			*results = append(*results, v1.ScrapeResult{
				Type:        v1.AWSBackupVault,
				ConfigClass: "Backup",
				BaseScraper: config.BaseScraper,
				Config:      vault,
				Name:        name,
				ID:          name,
				Aliases:     []string{arn},
				Tags:        v1.JSONStringMap{"region": ctx.Session.Region},
			})
		}
	}

	return nil
}

func (aws Scraper) scrapeBackupPlans(ctx *AWSContext, config v1.AWS, client *backup.Client, results *v1.ScrapeResults) error {
	ctx.Logger.V(3).Infof("scraping backup plans")

	paginator := backup.NewListBackupPlansPaginator(client, &backup.ListBackupPlansInput{})
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("listing backup plans: %w", err)
		}

		for _, plan := range output.BackupPlansList {
			arn := lo.FromPtr(plan.BackupPlanArn)
			planID := lo.FromPtr(plan.BackupPlanId)
			name := lo.FromPtr(plan.BackupPlanName)
			if config.ShouldExclude(v1.AWSBackupPlan, name, nil) {
				continue
			}

			// Get full plan details including rules
			detail, err := client.GetBackupPlan(ctx, &backup.GetBackupPlanInput{
				BackupPlanId: plan.BackupPlanId,
			})

			var planConfig any = plan
			var relationships v1.RelationshipResults
			if err == nil && detail.BackupPlan != nil {
				planConfig = detail.BackupPlan
				// Create relationships to target vaults
				for _, rule := range detail.BackupPlan.Rules {
					if rule.TargetBackupVaultName != nil {
						relationships = append(relationships, v1.RelationshipResult{
							ConfigExternalID: v1.ExternalID{
								ExternalID: planID,
								ConfigType: v1.AWSBackupPlan,
							},
							RelatedExternalID: v1.ExternalID{
								ExternalID: *rule.TargetBackupVaultName,
								ConfigType: v1.AWSBackupVault,
							},
							Relationship: "BackupPlanVault",
						})
					}
				}
			}

			*results = append(*results, v1.ScrapeResult{
				Type:                v1.AWSBackupPlan,
				ConfigClass:         "Backup",
				BaseScraper:         config.BaseScraper,
				Config:              planConfig,
				Name:                name,
				ID:                  planID,
				Aliases:             []string{arn},
				Tags:                v1.JSONStringMap{"region": ctx.Session.Region},
				RelationshipResults: relationships,
			})
		}
	}

	return nil
}

func (aws Scraper) scrapeBackupJobs(ctx *AWSContext, config v1.AWS, client *backup.Client, results *v1.ScrapeResults) error {
	ctx.Logger.V(3).Infof("scraping backup jobs")

	endTime := time.Now()
	startTime := endTime.Add(-30 * 24 * time.Hour)
	input := &backup.ListBackupJobsInput{
		ByCreatedAfter:  &startTime,
		ByCreatedBefore: &endTime,
	}
	paginator := backup.NewListBackupJobsPaginator(client, input)

	var changes []v1.ChangeResult

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list backup jobs: %w", err)
		}

		for _, job := range output.BackupJobs {
			if cr, ok := backupJobToChange(ctx, job); ok {
				changes = append(changes, cr)
			}
		}
	}

	result := v1.NewScrapeResult(config.BaseScraper)
	result.Changes = changes
	*results = append(*results, *result)

	return nil
}

// backupJobToChange transforms an AWS BackupJob into a ChangeResult with a
// canonical change_type (BackupCompleted / BackupFailed) and a typed
// types.Backup payload in Details, with the full raw job under details.raw.
// Returns ok=false for in-progress job states.
func backupJobToChange(ctx *AWSContext, job backupTypes.BackupJob) (v1.ChangeResult, bool) {
	changeType, status, severity, skip := awsBackupJobOutcome(job.State)
	if skip {
		return v1.ChangeResult{}, false
	}

	typed := types.Backup{
		Event: types.Event{
			ID:        lo.FromPtr(job.BackupJobId),
			Timestamp: timeToRFC3339(job.CreationDate),
			Properties: backupJobProperties(job),
		},
		BackupType:   types.BackupTypeSnapshot,
		Status:       status,
		Size:         bytesToString(job.BackupSizeInBytes),
		EndTimestamp: timeToRFC3339(job.CompletionDate),
		CreatedBy:    recoveryPointCreatedBy(job.CreatedBy, lo.FromPtr(job.IamRoleArn)),
		Environment:  awsEnvironment(ctx),
	}

	return v1.ChangeResult{
		ConfigType:       resourceTypeToConfigType(lo.FromPtr(job.ResourceType)),
		ExternalID:       lo.FromPtr(job.ResourceArn),
		ExternalChangeID: lo.FromPtr(job.BackupJobId),
		ChangeType:       changeType,
		Source:           SourceAWSBackup,
		Severity:         string(severity),
		Summary: fmt.Sprintf("%s %s backup %s",
			lo.FromPtr(job.ResourceType), lo.FromPtr(job.ResourceName), strings.ToLower(string(job.State))),
		CreatedAt: job.CreationDate,
		ScraperID: "all",
		Details:   v1.ChangeDetailsWithRaw(typed, job),
	}, true
}

func (aws Scraper) scrapeRestoreJobs(ctx *AWSContext, config v1.AWS, client *backup.Client, results *v1.ScrapeResults) error {
	ctx.Logger.V(3).Infof("scraping restore jobs")

	endTime := time.Now()
	startTime := endTime.Add(-30 * 24 * time.Hour)
	input := &backup.ListRestoreJobsInput{
		ByCreatedAfter:  &startTime,
		ByCreatedBefore: &endTime,
	}

	var changes []v1.ChangeResult
	paginator := backup.NewListRestoreJobsPaginator(client, input)
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list restore jobs: %w", err)
		}

		for _, job := range output.RestoreJobs {
			if cr, ok := restoreJobToChange(ctx, job); ok {
				changes = append(changes, cr)
			}
		}
	}

	result := v1.NewScrapeResult(config.BaseScraper)
	result.Changes = changes
	*results = append(*results, *result)

	return nil
}

// restoreJobToChange transforms an AWS RestoreJob into a ChangeResult with
// change_type=BackupRestored and a typed types.Restore payload, full raw job
// under details.raw. Returns ok=false for in-progress job states.
func restoreJobToChange(ctx *AWSContext, job backupTypes.RestoreJobsListMember) (v1.ChangeResult, bool) {
	changeType, status, severity, skip := awsRestoreJobOutcome(job.Status)
	if skip {
		return v1.ChangeResult{}, false
	}

	typed := types.Restore{
		Event: types.Event{
			ID:        lo.FromPtr(job.RestoreJobId),
			Timestamp: timeToRFC3339(job.CreationDate),
		},
		Status: status,
		To:     awsEnvironment(ctx),
	}

	return v1.ChangeResult{
		ConfigType:       resourceTypeToConfigType(lo.FromPtr(job.ResourceType)),
		Source:           SourceAWSBackup,
		ExternalChangeID: lo.FromPtr(job.RestoreJobId),
		ChangeType:       changeType,
		Severity:         string(severity),
		Summary:          fmt.Sprintf("%s restore %s", lo.FromPtr(job.ResourceType), strings.ToLower(string(job.Status))),
		CreatedAt:        job.CreationDate,
		ScraperID:        "all",
		ExternalID:       lo.FromPtr(job.CreatedResourceArn),
		Details:          v1.ChangeDetailsWithRaw(typed, job),
	}, true
}

func (aws Scraper) scrapeRecoveryPoints(ctx *AWSContext, config v1.AWS, client *backup.Client, results *v1.ScrapeResults) error {
	ctx.Logger.V(3).Infof("scraping recovery points")

	vaultsInput := &backup.ListBackupVaultsInput{}
	vaultsPaginator := backup.NewListBackupVaultsPaginator(client, vaultsInput)
	for vaultsPaginator.HasMorePages() {
		vaultsOutput, err := vaultsPaginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list backup vaults for recovery points: %w", err)
		}

		for _, vault := range vaultsOutput.BackupVaultList {
			if err := aws.scrapeRecoveryPointsForVault(ctx, config, client, vault, results); err != nil {
				ctx.Logger.Errorf("failed to scrape recovery points for vault %s: %v", lo.FromPtr(vault.BackupVaultName), err)
			}
		}
	}

	return nil
}

func (aws Scraper) scrapeRecoveryPointsForVault(ctx *AWSContext, config v1.AWS, client *backup.Client, vault backupTypes.BackupVaultListMember, results *v1.ScrapeResults) error {
	ctx.Logger.V(3).Infof("scraping recovery points for vault %s", lo.FromPtr(vault.BackupVaultName))

	input := &backup.ListRecoveryPointsByBackupVaultInput{
		BackupVaultName: vault.BackupVaultName,
	}
	paginator := backup.NewListRecoveryPointsByBackupVaultPaginator(client, input)

	var changes []v1.ChangeResult
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list recovery points for vault %s: %w", lo.FromPtr(vault.BackupVaultName), err)
		}

		for _, recoveryPoint := range output.RecoveryPoints {
			if cr, ok := recoveryPointToChange(ctx, vault, recoveryPoint); ok {
				changes = append(changes, cr)
			}
		}
	}

	result := v1.NewScrapeResult(config.BaseScraper)
	result.Changes = changes
	*results = append(*results, *result)

	return nil
}

// recoveryPointToChange transforms a RecoveryPointByBackupVault into a
// ChangeResult. Completed points emit BackupCompleted; partial/expired/
// deleting emit BackupFailed with reduced severity. In-progress (pending/
// running) return ok=false.
func recoveryPointToChange(ctx *AWSContext, vault backupTypes.BackupVaultListMember, rp backupTypes.RecoveryPointByBackupVault) (v1.ChangeResult, bool) {
	changeType, status, severity, distinctKey, skip := awsRecoveryPointOutcome(rp.Status)
	if skip {
		return v1.ChangeResult{}, false
	}

	arn := lo.FromPtr(rp.RecoveryPointArn)
	externalChangeID := arn
	if distinctKey {
		externalChangeID = arn + "-deleted"
	}

	typed := types.Backup{
		Event: types.Event{
			ID:        arn,
			Timestamp: timeToRFC3339(rp.CreationDate),
			Properties: recoveryPointProperties(rp, vault),
		},
		BackupType:   types.BackupTypeSnapshot,
		Status:       status,
		Size:         bytesToString(rp.BackupSizeInBytes),
		EndTimestamp: timeToRFC3339(rp.CompletionDate),
		CreatedBy:    recoveryPointCreatedBy(rp.CreatedBy, lo.FromPtr(rp.IamRoleArn)),
		Environment:  awsEnvironment(ctx),
	}

	return v1.ChangeResult{
		ConfigType:       resourceTypeToConfigType(lo.FromPtr(rp.ResourceType)),
		ExternalID:       lo.FromPtr(rp.ResourceArn),
		Source:           SourceAWSBackup,
		ExternalChangeID: externalChangeID,
		ChangeType:       changeType,
		Severity:         string(severity),
		Summary:          fmt.Sprintf("Recovery point %s in vault %s", strings.ToLower(string(rp.Status)), lo.FromPtr(vault.BackupVaultName)),
		CreatedAt:        rp.CreationDate,
		ScraperID:        "all",
		Details:          v1.ChangeDetailsWithRaw(typed, rp),
	}, true
}

func timeToRFC3339(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

func bytesToString(b *int64) string {
	if b == nil {
		return ""
	}
	return fmt.Sprintf("%d", *b)
}
