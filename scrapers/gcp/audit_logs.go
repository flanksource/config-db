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

var auditLogsLastTimestampPerScraper = sync.Map{}

func auditLogFilter(ctx *GCPContext, beginTime time.Time, auditLogs v1.GCPAuditLogs) (string, error) {
	var filters []string
	if len(auditLogs.IncludeTypes) > 0 {
		quotedIncludeTypes := lo.Map(auditLogs.IncludeTypes, func(t string, _ int) string {
			return fmt.Sprintf(`"%s"`, t)
		})
		filters = append(filters, fmt.Sprintf(`resource.type = (%s)`,
			strings.Join(quotedIncludeTypes, " OR "),
		))
	}

	if len(auditLogs.ExcludeTypes) > 0 {
		quotedExcludeTypes := lo.Map(auditLogs.ExcludeTypes, func(t string, _ int) string {
			return fmt.Sprintf(`"%s"`, t)
		})
		filters = append(filters, fmt.Sprintf(`NOT resource.type = (%s)`,
			strings.Join(quotedExcludeTypes, " OR "),
		))
	}

	if lastTimestamp, ok := auditLogsLastTimestampPerScraper.Load(ctx.ScrapeConfig().GetPersistedID()); ok {
		startTime := lastTimestamp.(time.Time)
		endTime := beginTime
		filters = append(filters, fmt.Sprintf(`timestamp>="%s" AND timestamp<="%s"`,
			startTime.Format(time.RFC3339),
			endTime.Format(time.RFC3339),
		))
	} else if auditLogs.Duration != "" {
		duration, err := time.ParseDuration(auditLogs.Duration)
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
		duration := 24 * time.Hour
		startTime := beginTime.Add(-duration)
		endTime := beginTime
		opt := fmt.Sprintf(`timestamp>="%s" AND timestamp<="%s"`,
			startTime.Format(time.RFC3339),
			endTime.Format(time.RFC3339),
		)

		filters = append(filters, opt)
	}

	return strings.Join(filters, " AND "), nil
}

func (gcp Scraper) FetchAuditLogs(ctx *GCPContext, config v1.GCP) (v1.ScrapeResults, error) {
	beginTime := time.Now()

	adminClient, err := logadmin.NewClient(ctx, config.Project, ctx.ClientOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create logging admin client: %w", err)
	}
	defer adminClient.Close()

	var configAccessLogs []v1.ExternalConfigAccessLog
	var unhandledResourceTypes = set.New[string]()

	filter, err := auditLogFilter(ctx, beginTime, config.AuditLogs)
	if err != nil {
		return nil, fmt.Errorf("failed to create audit log filter: %w", err)
	}

	it := adminClient.Entries(ctx, logadmin.Filter(filter))
	for {
		entry, err := it.Next()
		if err == iterator.Done {
			break
		} else if err != nil {
			return nil, fmt.Errorf("failed to list access log entries: %w", err)
		}

		if entry.Payload == nil {
			continue
		}

		auditLog, ok := entry.Payload.(*audit.AuditLog)
		if !ok {
			// Cloudsql_database has payload of type string
			// e.g. "2025-06-05 15:57:58.017 UTC [110445]: [1-1] db=,user= LOG:  automatic analyze of table \"org_2nc9weutlwyjbmjuazzbtgeoadw.public.job_history\"\nI/O timings: read: 0.000 ms, write: 0.000 ms\navg read rate: 0.000 MB/s, avg write rate: 5.580 MB/s\nbuffer usage: 640 hits, 0 misses, 5 dirtied\nsystem usage: CPU: user: 0.00 s, system: 0.00 s, elapsed: 0.00 s"
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

		case "k8s_cluster", "k8s_container":
			resourceID.ExternalID = fmt.Sprintf("//container.googleapis.com/%s", entry.Resource.Labels["cluster_name"])
			resourceID.ConfigType = "GCP::Container::Cluster"

		case "project":
			resourceID.ExternalID = fmt.Sprintf("//cloudresourcemanager.googleapis.com/%s", entry.Resource.Labels["project_id"])
			resourceID.ConfigType = "GCP::Cloudresourcemanager::Project"

		case "gce_instance":
			resourceID.ExternalID = fmt.Sprintf("//compute.googleapis.com/%s", auditLog.ResourceName)
			resourceID.ConfigType = "GCP::Compute::Instance"

		default:
			// NOTE: every resource type must be manually handled here.
			// because we need to form the external ID for the resource.
			// Example: An audit log for GCS bucket contains ResourceName: projects/_/buckets/lastline-artifacts
			// whereas the external ID that we save is //storage.googleapis.com/lastline-artifacts
			unhandledResourceTypes.Add(entry.Resource.Type)

			continue
		}

		principalEmail := auditLog.AuthenticationInfo.PrincipalEmail
		if principalEmail == "" || resourceID.IsEmpty() {
			continue
		}

		accessLog := models.ConfigAccessLog{
			ExternalUserID: generateConsistentID(principalEmail),
			ScraperID:      *ctx.ScrapeConfig().GetPersistedID(),
			CreatedAt:      entry.Timestamp,
		}

		configAccessLogs = append(configAccessLogs, v1.ExternalConfigAccessLog{
			ConfigAccessLog:  accessLog,
			ConfigExternalID: resourceID,
		})
	}

	if len(unhandledResourceTypes) > 0 {
		ctx.Warnf("gcp audit logs: unhandled resource types: %v", unhandledResourceTypes.ToSlice())
	}

	if len(configAccessLogs) > 0 {
		return v1.ScrapeResults{{
			BaseScraper:      config.BaseScraper,
			ConfigAccessLogs: configAccessLogs,
		}}, nil
	}

	auditLogsLastTimestampPerScraper.Store(ctx.ScrapeConfig().GetPersistedID(), beginTime)

	return nil, nil
}
