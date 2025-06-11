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
	var instances []instanceInfo
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

			instances = append(instances, instanceInfo{name: instanceName, selfLink: instanceSelfLink})
		}
	}

	if len(instances) == 0 {
		return nil, nil
	}

	sqlService, err := sqladmin.NewService(ctx, ctx.ClientOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create SQL admin service: %w", err)
	}

	var allChanges []v1.ChangeResult

	for _, instance := range instances {
		if backupChanges, err := gcp.scrapeBackupRuns(ctx, config, sqlService, instance.name, instance.selfLink); err != nil {
			ctx.Logger.Errorf("failed to scrape backup runs for instance %s: %v", instance.name, err)
		} else {
			allChanges = append(allChanges, backupChanges...)
		}
	}

	if operationChanges, err := gcp.scrapeOperations(ctx, config, sqlService, instances); err != nil {
		ctx.Logger.Errorf("failed to scrape operations for project %s: %v", config.Project, err)
	} else {
		allChanges = append(allChanges, operationChanges...)
	}

	var scrapeResults v1.ScrapeResults
	if len(allChanges) > 0 {
		result := v1.NewScrapeResult(config.BaseScraper)
		result.Changes = allChanges
		scrapeResults = append(scrapeResults, *result)
	}

	return scrapeResults, nil
}

type instanceInfo struct {
	name     string
	selfLink string
}

// scrapeBackupRuns scrapes Cloud SQL backup runs for a specific instance
func (gcp Scraper) scrapeBackupRuns(ctx *GCPContext, config v1.GCP, service *sqladmin.Service, instanceName string, instanceSelfLink string) ([]v1.ChangeResult, error) {
	ctx.Logger.V(3).Infof("scraping backup runs for Cloud SQL instance %s", instanceName)

	var allBackupRuns []*sqladmin.BackupRun
	backupRunsCall := service.BackupRuns.List(config.Project, instanceName)
	err := backupRunsCall.Pages(ctx, func(page *sqladmin.BackupRunsListResponse) error {
		allBackupRuns = append(allBackupRuns, page.Items...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list backup runs for instance %s: %w", instanceName, err)
	}

	var changes []v1.ChangeResult
	for _, backupRun := range allBackupRuns {
		startTime, err := time.Parse(time.RFC3339, backupRun.StartTime)
		if err != nil {
			ctx.Logger.V(2).Infof("failed to parse backup run start time for instance %s, backup ID %d: %v", instanceName, backupRun.Id, err)
			continue
		}

		changeType := fmt.Sprintf("Backup%s", lo.PascalCase(backupRun.Status))
		severity := mapCloudSQLOperationSeverity(backupRun.Status)

		changeResult := v1.ChangeResult{
			ConfigType:       v1.CloudSQLInstance,
			ExternalID:       instanceSelfLink,
			ExternalChangeID: fmt.Sprintf("%d", backupRun.Id),
			ChangeType:       changeType,
			Source:           "SQLAdmin",
			Summary:          fmt.Sprintf("%s %s", lo.PascalCase(backupRun.Type), lo.PascalCase(backupRun.BackupKind)), // eg: Automated Snapshot
			CreatedAt:        &startTime,
			Severity:         severity,
			Details: map[string]any{
				"backupRun": backupRun,
				"status":    lo.PascalCase(backupRun.Status),
			},
		}

		changes = append(changes, changeResult)
	}

	return changes, nil
}

// scrapeOperations scrapes Cloud SQL import/export operations for all instances
func (gcp Scraper) scrapeOperations(ctx *GCPContext, config v1.GCP, service *sqladmin.Service, instances []instanceInfo) ([]v1.ChangeResult, error) {
	ctx.Logger.V(3).Infof("scraping operations for project %s", config.Project)

	instanceMap := make(map[string]string) // instanceName -> selfLink
	for _, instance := range instances {
		instanceMap[instance.name] = instance.selfLink
	}

	var changes []v1.ChangeResult

	operationsCall := service.Operations.List(config.Project)
	err := operationsCall.Pages(ctx, func(operationsResp *sqladmin.OperationsListResponse) error {
		for _, operation := range operationsResp.Items {
			if operation.OperationType != "IMPORT" && operation.OperationType != "EXPORT" {
				continue
			}

			instanceSelfLink, exists := instanceMap[operation.TargetId]
			if !exists {
				continue
			}

			startTime, err := time.Parse(time.RFC3339, operation.StartTime)
			if err != nil {
				ctx.Logger.V(2).Infof("failed to parse operation start time for instance %s, operation %s: %v", operation.TargetId, operation.Name, err)
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
				Summary:          fmt.Sprintf("Cloud SQL %s %s for instance %s", strings.ToLower(operation.OperationType), strings.ToLower(operation.Status), operation.TargetId),
				CreatedAt:        &startTime,
				Severity:         severity,
				Details: map[string]any{
					"operation": operation,
					"status":    operation.Status,
					"instance":  operation.TargetId,
					"type":      operation.OperationType,
				},
			}

			changes = append(changes, changeResult)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list operations: %w", err)
	}

	return changes, nil
}

// mapCloudSQLOperationSeverity maps Cloud SQL operation status to severity levels
func mapCloudSQLOperationSeverity(status string) string {
	switch strings.ToUpper(status) {
	case "PENDING", "RUNNING", "DONE", "SUCCESSFUL":
		return string(models.SeverityInfo)
	case "FAILED", "ERROR":
		return string(models.SeverityHigh)
	case "CANCELLED", "ABORTED":
		return string(models.SeverityMedium)
	default:
		return string(models.SeverityLow)
	}
}
