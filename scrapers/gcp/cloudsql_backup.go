package gcp

import (
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/duty/models"
	"github.com/samber/lo"
	"google.golang.org/api/sqladmin/v1"
	"google.golang.org/protobuf/types/known/structpb"

	v1 "github.com/flanksource/config-db/api/v1"
)

// scrapeCloudSQLBackupsForAllInstances finds Cloud SQL instances in the results and scrapes their backups
func (gcp Scraper) scrapeCloudSQLBackupsForAllInstances(ctx *GCPContext, config v1.GCP, results v1.ScrapeResults) (v1.ScrapeResults, error) {
	var allBackupResults v1.ScrapeResults
	for _, result := range results {
		if strings.Contains(result.Type, v1.CloudSQLInstance) {
			instanceName := result.Name
			instanceSelfLink := ""

			// Try to get the self link from the config.
			// This will be used as the external ID to link back to the SQL instance config item.
			if result.Config != nil {
				if configStruct, ok := result.Config.(*structpb.Struct); ok {
					if selfLinkField, exists := configStruct.Fields["selfLink"]; exists {
						instanceSelfLink = selfLinkField.GetStringValue()
					}
				}
			}

			if instanceSelfLink == "" {
				instanceSelfLink = result.ID
			}

			ctx.Logger.V(3).Infof("Found Cloud SQL instance: %s (type: %s)", instanceName, result.Type)

			backupResults, err := gcp.scrapeCloudSQLBackups(ctx, config, instanceName, instanceSelfLink)
			if err != nil {
				return nil, fmt.Errorf("failed to scrape backups for Cloud SQL instance %s: %v", instanceName, err)
			}

			allBackupResults = append(allBackupResults, backupResults...)
		}
	}

	return allBackupResults, nil
}

// scrapeCloudSQLBackups scrapes Cloud SQL backup operations for a specific instance
func (gcp Scraper) scrapeCloudSQLBackups(ctx *GCPContext, config v1.GCP, instanceName string, instanceSelfLink string) (v1.ScrapeResults, error) {
	var results v1.ScrapeResults

	ctx.Logger.V(2).Infof("scraping Cloud SQL backups for instance %s", instanceName)

	sqlService, err := sqladmin.NewService(ctx, ctx.ClientOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create SQL admin service: %w", err)
	}

	var changes []v1.ChangeResult

	if backupChanges, err := gcp.scrapeBackupRuns(ctx, config, sqlService, instanceName, instanceSelfLink); err != nil {
		ctx.Logger.Errorf("failed to scrape backup runs for instance %s: %v", instanceName, err)
	} else {
		changes = append(changes, backupChanges...)
	}

	if operationChanges, err := gcp.scrapeOperations(ctx, config, sqlService, instanceName, instanceSelfLink); err != nil {
		ctx.Logger.Errorf("failed to scrape operations for instance %s: %v", instanceName, err)
	} else {
		changes = append(changes, operationChanges...)
	}

	if len(changes) > 0 {
		result := v1.NewScrapeResult(config.BaseScraper)
		result.Changes = changes
		results = append(results, *result)
	}

	return results, nil
}

// scrapeBackupRuns scrapes Cloud SQL backup runs for a specific instance
func (gcp Scraper) scrapeBackupRuns(ctx *GCPContext, config v1.GCP, service *sqladmin.Service, instanceName string, instanceSelfLink string) ([]v1.ChangeResult, error) {
	ctx.Logger.V(3).Infof("scraping backup runs for Cloud SQL instance %s", instanceName)

	backupRunsCall := service.BackupRuns.List(config.Project, instanceName)
	backupRunsResp, err := backupRunsCall.Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list backup runs for instance %s: %w", instanceName, err)
	}

	var changes []v1.ChangeResult
	for _, backupRun := range backupRunsResp.Items {
		startTime, err := time.Parse(time.RFC3339, backupRun.StartTime)
		if err != nil {
			continue
		}

		changeType := fmt.Sprintf("Backup%s", lo.PascalCase(backupRun.Status))
		severity := mapCloudSQLOperationSeverity(backupRun.Status)

		changeResult := v1.ChangeResult{
			ConfigType:       v1.CloudSQLInstance,
			ExternalID:       instanceSelfLink,
			ExternalChangeID: fmt.Sprintf("%d", backupRun.Id),
			ChangeType:       changeType,
			Source:           "GCP Cloud SQL",
			Summary:          fmt.Sprintf("Cloud SQL backup %s for instance %s", strings.ToLower(backupRun.Status), instanceName),
			CreatedAt:        &startTime,
			Severity:         severity,
			Details: map[string]any{
				"backupRun": backupRun,
				"status":    backupRun.Status,
				"instance":  instanceName,
				"type":      backupRun.Type,
			},
		}

		changes = append(changes, changeResult)
	}

	return changes, nil
}

// scrapeOperations scrapes Cloud SQL import/export operations for a specific instance
func (gcp Scraper) scrapeOperations(ctx *GCPContext, config v1.GCP, service *sqladmin.Service, instanceName string, instanceSelfLink string) ([]v1.ChangeResult, error) {
	ctx.Logger.V(3).Infof("scraping operations for Cloud SQL instance %s", instanceName)

	var changes []v1.ChangeResult

	operationsCall := service.Operations.List(config.Project)
	operationsResp, err := operationsCall.Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list operations: %w", err)
	}

	for _, operation := range operationsResp.Items {
		if operation.OperationType != "IMPORT" && operation.OperationType != "EXPORT" {
			continue
		}

		if operation.TargetId != instanceName {
			continue
		}

		// Only include operations within the lookback period
		startTime, err := time.Parse(time.RFC3339, operation.StartTime)
		if err != nil {
			continue
		}

		changeType := fmt.Sprintf("%s%s", lo.PascalCase(operation.OperationType), lo.PascalCase(operation.Status))
		severity := mapCloudSQLOperationSeverity(operation.Status)

		changeResult := v1.ChangeResult{
			ConfigType:       v1.CloudSQLInstance,
			ExternalID:       instanceSelfLink,
			ExternalChangeID: operation.Name,
			ChangeType:       changeType,
			Source:           "GCP Cloud SQL",
			Summary:          fmt.Sprintf("Cloud SQL %s %s for instance %s", strings.ToLower(operation.OperationType), strings.ToLower(operation.Status), instanceName),
			CreatedAt:        &startTime,
			Severity:         severity,
			Details: map[string]any{
				"operation": operation,
				"status":    operation.Status,
				"instance":  instanceName,
				"type":      operation.OperationType,
			},
		}

		changes = append(changes, changeResult)
	}

	return changes, nil
}

// mapCloudSQLOperationSeverity maps Cloud SQL operation status to severity levels
func mapCloudSQLOperationSeverity(status string) string {
	switch strings.ToUpper(status) {
	case "PENDING", "RUNNING":
		return string(models.SeverityInfo)
	case "DONE", "SUCCESSFUL":
		return string(models.SeverityInfo)
	case "FAILED", "ERROR":
		return string(models.SeverityHigh)
	case "CANCELLED", "ABORTED":
		return string(models.SeverityMedium)
	default:
		return string(models.SeverityLow)
	}
}
