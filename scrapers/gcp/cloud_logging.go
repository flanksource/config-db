package gcp

import (
	"fmt"
	"time"

	"cloud.google.com/go/logging/logadmin"
	v1 "github.com/flanksource/config-db/api/v1"
	"google.golang.org/api/iterator"
)

func (gcp Scraper) AuditLogs(ctx *GCPContext, config v1.GCP, results *v1.ScrapeResults) {
	if !config.Includes("AuditLog") {
		return
	}

	adminClient, err := logadmin.NewClient(ctx, ctx.ProjectID)
	if err != nil {
		results.Errorf(err, "failed to create logging admin client")
		return
	}
	defer adminClient.Close() // nolint:errcheck

	// Define the time range for the logs
	startTime := time.Now().Add(-24 * time.Hour) // Last 24 hours
	endTime := time.Now()

	// Define the filter for audit logs
	filter := fmt.Sprintf(`logName="projects/%s/logs/cloudaudit.googleapis.com%%2Factivity" AND timestamp>="%s" AND timestamp<="%s"`,
		ctx.ProjectID, startTime.Format(time.RFC3339), endTime.Format(time.RFC3339))

	it := adminClient.Entries(ctx, logadmin.Filter(filter))

	for {
		entry, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			results.Errorf(err, "failed to list audit log entries")
			return
		}

		// Extract relevant information from the log entry
		resourceName := entry.Resource.Labels["resource_name"]
		if resourceName == "" {
			continue
		}

		change := v1.ChangeResult{

			// Timestamp: entry.Timestamp,
			// Message:   entry.TextPayload,
			// Details:   entry,
		}

		// Find the corresponding configuration item and attach the change
		for i, result := range *results {
			if result.ID == resourceName {
				(*results)[i].Changes = append((*results)[i].Changes, change)
				break
			}
		}
	}
}
