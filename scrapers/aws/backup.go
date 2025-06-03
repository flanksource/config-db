package aws

import (
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/backup"
	backupTypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/flanksource/duty/models"
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

	// if err := aws.scrapeRecoveryPoints(ctx, config, backupClient, results); err != nil {
	// 	results.Errorf(err, "failed to scrape recovery points")
	// }

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
	}

	return ""
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

	changes := []v1.ChangeResult{}

	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list backup jobs: %w", err)
		}

		for _, job := range output.BackupJobs {
			changeType := fmt.Sprintf("Backup%s", lo.PascalCase(string(job.State)))

			changeResult := v1.ChangeResult{
				ConfigType:       resourceTypeToConfigType(lo.FromPtr(job.ResourceType)),
				ExternalID:       lo.FromPtr(job.ResourceArn),
				ExternalChangeID: lo.FromPtr(job.BackupJobId),
				ChangeType:       changeType,
				Source:           SourceAWSBackup,
				Summary:          fmt.Sprintf("%s %s backup %s", lo.FromPtr(job.ResourceType), lo.FromPtr(job.ResourceName), strings.ToLower(string(job.State))),
				CreatedAt:        job.CreationDate,
				Details: map[string]any{
					"job":    job,
					"status": lo.PascalCase(string(job.State)),
				},
			}

			switch job.State {
			case backupTypes.BackupJobStateCreated:
				changeResult.Severity = string(models.SeverityInfo)
			case backupTypes.BackupJobStatePending:
				changeResult.Severity = string(models.SeverityInfo)
			case backupTypes.BackupJobStateRunning:
				changeResult.Severity = string(models.SeverityInfo)
			case backupTypes.BackupJobStateAborting:
				changeResult.Severity = string(models.SeverityLow)
			case backupTypes.BackupJobStateAborted:
				changeResult.Severity = string(models.SeverityLow)
			case backupTypes.BackupJobStateCompleted:
				changeResult.Severity = string(models.SeverityInfo)
			case backupTypes.BackupJobStateFailed:
				changeResult.Severity = string(models.SeverityHigh)
			case backupTypes.BackupJobStateExpired:
				changeResult.Severity = string(models.SeverityMedium)
			case backupTypes.BackupJobStatePartial:
				changeResult.Severity = string(models.SeverityMedium)
			}

			changes = append(changes, changeResult)
		}
	}

	result := v1.NewScrapeResult(config.BaseScraper)
	result.Changes = changes
	*results = append(*results, *result)

	return nil
}

func (aws Scraper) scrapeRestoreJobs(ctx *AWSContext, config v1.AWS, client *backup.Client, results *v1.ScrapeResults) error {
	ctx.Logger.V(3).Infof("scraping restore jobs")

	endTime := time.Now()
	startTime := endTime.Add(-30 * 24 * time.Hour)
	input := &backup.ListRestoreJobsInput{
		ByCreatedAfter:  &startTime,
		ByCreatedBefore: &endTime,
	}

	changes := []v1.ChangeResult{}
	paginator := backup.NewListRestoreJobsPaginator(client, input)
	for paginator.HasMorePages() {
		output, err := paginator.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list restore jobs: %w", err)
		}

		for _, job := range output.RestoreJobs {
			changeResult := v1.ChangeResult{
				ConfigType:       resourceTypeToConfigType(lo.FromPtr(job.ResourceType)),
				Source:           "AWS Backup",
				ExternalChangeID: lo.FromPtr(job.RestoreJobId),
				ChangeType:       fmt.Sprintf("Restore%s", lo.PascalCase(string(job.Status))),
				Severity:         string(models.SeverityInfo),
				Summary:          fmt.Sprintf("%s restore %s", lo.FromPtr(job.ResourceType), strings.ToLower(string(job.Status))),
				CreatedAt:        job.CreationDate,
				Details: map[string]any{
					"job":    job,
					"status": lo.PascalCase(string(job.Status)),
				},
			}

			switch job.Status {
			case backupTypes.RestoreJobStatusAborted:
				changeResult.Severity = string(models.SeverityMedium)
			case backupTypes.RestoreJobStatusFailed:
				changeResult.Severity = string(models.SeverityHigh)
			case backupTypes.RestoreJobStatusPending:
				changeResult.Severity = string(models.SeverityInfo)
			case backupTypes.RestoreJobStatusRunning:
				changeResult.Severity = string(models.SeverityInfo)
			case backupTypes.RestoreJobStatusCompleted:
				changeResult.Severity = string(models.SeverityInfo)
			}

			changeResult.ExternalID = lo.FromPtr(job.CreatedResourceArn)
			switch lo.FromPtr(job.ResourceType) {
			case "RDS":
				changeResult.ConfigType = v1.AWSRDSInstance
			}

			changes = append(changes, changeResult)
		}
	}

	result := v1.NewScrapeResult(config.BaseScraper)
	result.Changes = changes
	*results = append(*results, *result)

	return nil
}

// func (aws Scraper) scrapeRecoveryPoints(ctx *AWSContext, config v1.AWS, client *backup.Client, results *v1.ScrapeResults) error {
// 	ctx.Logger.V(3).Infof("scraping recovery points")

// 	vaultsInput := &backup.ListBackupVaultsInput{}
// 	vaultsPaginator := backup.NewListBackupVaultsPaginator(client, vaultsInput)
// 	for vaultsPaginator.HasMorePages() {
// 		vaultsOutput, err := vaultsPaginator.NextPage(ctx)
// 		if err != nil {
// 			return fmt.Errorf("failed to list backup vaults for recovery points: %w", err)
// 		}

// 		for _, vault := range vaultsOutput.BackupVaultList {
// 			if err := aws.scrapeRecoveryPointsForVault(ctx, config, client, vault, results); err != nil {
// 				ctx.Logger.Errorf("failed to scrape recovery points for vault %s: %v", lo.FromPtr(vault.BackupVaultName), err)
// 			}
// 		}
// 	}

// 	return nil
// }

// func (aws Scraper) scrapeRecoveryPointsForVault(ctx *AWSContext, config v1.AWS, client *backup.Client, vault backupTypes.BackupVaultListMember, results *v1.ScrapeResults) error {
// 	ctx.Logger.V(3).Infof("scraping recovery points for vault %s", lo.FromPtr(vault.BackupVaultName))

// 	input := &backup.ListRecoveryPointsByBackupVaultInput{
// 		BackupVaultName: vault.BackupVaultName,
// 	}
// 	paginator := backup.NewListRecoveryPointsByBackupVaultPaginator(client, input)

// 	for paginator.HasMorePages() {
// 		output, err := paginator.NextPage(ctx)
// 		if err != nil {
// 			return fmt.Errorf("failed to list recovery points for vault %s: %w", lo.FromPtr(vault.BackupVaultName), err)
// 		}

// 		for _, recoveryPoint := range output.RecoveryPoints {
// 			*results = append(*results, v1.ScrapeResult{
// 				Type:        v1.AWSBackupRecoveryPoint,
// 				BaseScraper: config.BaseScraper,
// 				Config:      recoveryPoint,
// 				ConfigClass: "BackupRecoveryPoint",
// 				Name:        lo.FromPtr(recoveryPoint.RecoveryPointArn),
// 				ID:          lo.FromPtr(recoveryPoint.RecoveryPointArn),
// 				Status:      lo.PascalCase(string(recoveryPoint.Status)),
// 				CreatedAt:   recoveryPoint.CreationDate,
// 				Properties:  []*types.Property{getConsoleLink(ctx.Session.Region, v1.AWSBackupRecoveryPoint, lo.FromPtr(recoveryPoint.RecoveryPointArn), nil)},
// 				Tags:        []v1.Tag{{Name: "region", Value: ctx.Session.Region}},
// 			})
// 		}
// 	}

// 	return nil
// }
