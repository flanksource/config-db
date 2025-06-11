package gcp

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/logging/logadmin"
	"github.com/flanksource/commons/collections/set"
	"github.com/flanksource/duty/models"
	"github.com/samber/lo"
	"google.golang.org/api/iterator"
	"google.golang.org/genproto/googleapis/cloud/audit"

	v1 "github.com/flanksource/config-db/api/v1"
)

// Always ignore these as we can't link them to any config item.
var defaultExcludeAuditLogResourceTypes = []string{
	"api",
	"audited_resource",
	"service_account",
}

var histBuckets = []float64{1, 10, 100, 1000, 5000, 10000}

const defaultAuditLogMaxDuration = 7 * 24 * time.Hour

var auditLogsLastTimestampPerScraper = sync.Map{}

func auditLogFilter(ctx *GCPContext, beginTime time.Time, project string, auditLogs v1.GCPAuditLogs) (string, error) {
	// If the scraper isn't running for the first time,
	// then we discard the maxDuration configuration
	// and resume from the last saved audit log.
	var lastSavedAuditLog models.ConfigAccessLog
	if err := ctx.DB().Where("scraper_id = ? ", ctx.ScrapeConfig().GetPersistedID().String()).
		Order("created_at DESC").
		Limit(1).
		Find(&lastSavedAuditLog).Error; err != nil {
		return "", fmt.Errorf("failed to get last saved audit log: %w", err)
	}

	filters := []string{
		fmt.Sprintf(`logName="projects/%s/logs/cloudaudit.googleapis.com%%2Factivity"`, project),

		// exclude k8s cluster audit logs from kubernetes service account
		// because there are hundreds and thousands of these and we can't link them to any IAM Principal.
		`(NOT (protoPayload.authenticationInfo.principalEmail:"system:" AND resource.type = "k8s_cluster"))`,
	}

	if len(auditLogs.IncludeTypes) > 0 {
		quotedIncludeTypes := lo.Map(auditLogs.IncludeTypes, func(t string, _ int) string {
			return fmt.Sprintf(`"%s"`, t)
		})
		filters = append(filters, fmt.Sprintf(`resource.type = (%s)`,
			strings.Join(quotedIncludeTypes, " OR "),
		))
	}

	quotedExcludeTypes := lo.Map(append(auditLogs.ExcludeTypes, defaultExcludeAuditLogResourceTypes...), func(t string, _ int) string {
		return fmt.Sprintf(`"%s"`, t)
	})
	filters = append(filters, fmt.Sprintf(`NOT resource.type = (%s)`,
		strings.Join(quotedExcludeTypes, " OR "),
	))

	if lastTimestamp, ok := auditLogsLastTimestampPerScraper.Load(lo.FromPtr(ctx.ScrapeConfig().GetPersistedID())); ok {
		startTime := lastTimestamp.(time.Time)
		endTime := beginTime
		filters = append(filters, fmt.Sprintf(`timestamp>="%s" AND timestamp<="%s"`,
			startTime.Format(time.RFC3339),
			endTime.Format(time.RFC3339),
		))
	} else if !lastSavedAuditLog.CreatedAt.IsZero() {
		filters = append(filters, fmt.Sprintf(`timestamp>="%s"`, lastSavedAuditLog.CreatedAt.Format(time.RFC3339)))
	} else if auditLogs.MaxDuration != "" {
		duration, err := time.ParseDuration(auditLogs.MaxDuration)
		if err != nil {
			return "", fmt.Errorf("failed to parse audit logs duration: %w", err)
		}

		startTime := beginTime.Add(-duration)
		endTime := beginTime
		opt := fmt.Sprintf(`timestamp>="%s" AND timestamp<="%s"`,
			startTime.Format(time.RFC3339),
			endTime.Format(time.RFC3339),
		)

		filters = append(filters, opt)
	} else {
		startTime := beginTime.Add(-defaultAuditLogMaxDuration)
		endTime := beginTime

		opt := fmt.Sprintf(`timestamp>="%s" AND timestamp<="%s"`,
			startTime.Format(time.RFC3339),
			endTime.Format(time.RFC3339),
		)

		filters = append(filters, opt)
	}

	// FIXME:
	// var externalUsers []models.ExternalUser
	// if err := ctx.DB().Select("email").Where("email IS NOT NULL").
	// 	Where("deleted_at IS NULL").
	// 	Where("scraper_id = ?", ctx.ScrapeConfig().GetPersistedID().String()).Find(&externalUsers).Error; err != nil {
	// 	return "", fmt.Errorf("failed to get external users: %w", err)
	// }

	// if len(externalUsers) > 0 {
	// 	// Audit logs can have entries for non-IAM principals.
	// 	// Example: Kubernetes service accounts.
	// 	//
	// 	// We only want to fetch audit logs for the external users that we have saved in the database.
	// 	filters = append(filters, fmt.Sprintf(`protoPayload.authenticationInfo.principalEmail = (%s)`,
	// 		strings.Join(lo.Map(externalUsers, func(e models.ExternalUser, _ int) string {
	// 			return fmt.Sprintf("%q", lo.FromPtr(e.Email))
	// 		}), " OR "),
	// 	))
	// }

	return strings.Join(filters, " AND "), nil
}

func (gcp Scraper) FetchAuditLogs(ctx *GCPContext, config v1.GCP) (v1.ScrapeResults, error) {
	beginTime := time.Now()

	adminClient, err := logadmin.NewClient(ctx, config.Project, ctx.ClientOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create logging admin client: %w", err)
	}
	defer func() {
		if err := adminClient.Close(); err != nil {
			ctx.Warnf("gcp audit logs: failed to close logging admin client: %v", err)
		}
	}()

	var (
		configAccessLogs []v1.ExternalConfigAccessLog
		configAccesses   []v1.ExternalConfigAccess
	)

	var unhandledResourceTypes = set.New[string]()

	filter, err := auditLogFilter(ctx, beginTime, config.Project, config.AuditLogs)
	if err != nil {
		return nil, fmt.Errorf("failed to create audit log filter: %w", err)
	}

	ctx.Logger.V(2).Infof("fetching audit logs with filter: %s", filter)

	it := adminClient.Entries(ctx, logadmin.Filter(filter), logadmin.PageSize(1000))
	for {
		start := time.Now()
		entry, err := it.Next()
		if err == iterator.Done {
			break
		} else if err != nil {
			return nil, fmt.Errorf("failed to list access log entries: %w", err)
		}
		ctx.Histogram("gcp_audit_log_entry_duration", histBuckets,
			"scraper_id", ctx.ScrapeConfig().GetPersistedID().String(),
			"project", config.Project,
		).Record(time.Duration(time.Since(start).Milliseconds()))

		if entry.Payload == nil {
			continue
		}

		auditLog, ok := entry.Payload.(*audit.AuditLog)
		if !ok {
			continue
		}

		if auditLog.AuthenticationInfo == nil {
			continue
		}

		var resourceID v1.ExternalID
		switch entry.Resource.Type {
		case "gcs_bucket":
			resourceID.ExternalID = fmt.Sprintf("//storage.googleapis.com/%s", entry.Resource.Labels["bucket_name"])
			resourceID.ConfigType = "GCP::Storage::Bucket"

		case "k8s_cluster", "k8s_container", "gke_cluster":
			resourceID.ExternalID = fmt.Sprintf("//container.googleapis.com/projects/%s/locations/%s/clusters/%s",
				entry.Resource.Labels["project_id"],
				entry.Resource.Labels["location"],
				entry.Resource.Labels["cluster_name"],
			)
			resourceID.ConfigType = "GCP::Container::Cluster"

		case "project":
			resourceID.ExternalID = fmt.Sprintf("//cloudresourcemanager.googleapis.com/%s", entry.Resource.Labels["project_id"])
			resourceID.ConfigType = "GCP::Compute::Project"

		case "gce_instance":
			if entry.Resource.Labels["instance_id"] == "" {
				continue // e.g. when method=v1.compute.instances.list
			}

			resourceID.ExternalID = fmt.Sprintf("//compute.googleapis.com/%s", auditLog.ResourceName)
			resourceID.ConfigType = "GCP::Compute::Instance"

		default:
			// NOTE: every resource type must be manually handled here.
			//
			// Because we need to form the external ID for the resource.
			// Example: An audit log for GCS bucket contains ResourceName: projects/_/buckets/lastline-artifacts
			// whereas the external ID that we save is //storage.googleapis.com/lastline-artifacts
			unhandledResourceTypes.Add(entry.Resource.Type)

			continue
		}

		principalEmail := auditLog.AuthenticationInfo.PrincipalEmail
		if principalEmail == "" || resourceID.IsEmpty() {
			continue
		}

		configAccessLogs = append(configAccessLogs, v1.ExternalConfigAccessLog{
			ConfigAccessLog: models.ConfigAccessLog{
				ExternalUserID: generateConsistentID(principalEmail),
				ScraperID:      *ctx.ScrapeConfig().GetPersistedID(),
				CreatedAt:      entry.Timestamp,
			},
			ConfigExternalID: resourceID,
		})

		configAccesses = append(configAccesses, v1.ExternalConfigAccess{
			ConfigAccess: models.ConfigAccess{
				ID:             generateConsistentID(principalEmail + resourceID.String()).String(), // FIXME:
				ExternalUserID: lo.ToPtr(generateConsistentID(principalEmail)),
				ScraperID:      ctx.ScrapeConfig().GetPersistedID(),
				CreatedAt:      entry.Timestamp,
			},
			ConfigExternalID: resourceID,
		})
	}

	if len(unhandledResourceTypes) > 0 {
		ctx.Warnf("gcp audit logs: unhandled resource types: %v", unhandledResourceTypes.ToSlice())
	}

	auditLogsLastTimestampPerScraper.Store(lo.FromPtr(ctx.ScrapeConfig().GetPersistedID()), beginTime)

	return v1.ScrapeResults{{
		BaseScraper:      config.BaseScraper,
		ConfigAccessLogs: configAccessLogs,
		ConfigAccess:     configAccesses,
	}}, nil
}
