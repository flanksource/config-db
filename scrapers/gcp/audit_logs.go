package gcp

import (
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/bigquery"
	"github.com/flanksource/commons/duration"
	"github.com/flanksource/duty/models"
	"github.com/samber/lo"
	"google.golang.org/api/iterator"
	"k8s.io/utils/set"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
)

const auditLogDefaultTimeRange = time.Hour * 24 * 30 // 30 days

type BigQueryRow struct {
	Email          string    `bigquery:"email"`
	Permission     string    `bigquery:"permission"`
	PermissionType string    `bigquery:"permission_type"`
	Timestamp      time.Time `bigquery:"timestamp"`
}

func buildAuditLogQuery(auditLogs v1.GCPAuditLogs) (string, []bigquery.QueryParameter, error) {
	if auditLogs.Dataset == "" {
		return "", nil, fmt.Errorf("dataset is required for BigQuery audit logs")
	}

	var cteConditions []string
	var outerConditions []string
	var cteArgs []any
	var outerArgs []any

	timeRange := auditLogDefaultTimeRange
	if auditLogs.Since != "" {
		dur, err := duration.ParseDuration(auditLogs.Since)
		if err != nil {
			return "", nil, fmt.Errorf("invalid time range '%s': %w", auditLogs.Since, err)
		}
		timeRange = time.Duration(dur)
	}

	cteConditions = append(cteConditions, fmt.Sprintf("timestamp >= '%s'", utils.Now().Add(-timeRange).Format(time.DateOnly)))
	cteConditions = append(cteConditions, "ARRAY_LENGTH(proto_payload.audit_log.authorization_info) > 0")

	if len(auditLogs.UserAgents) > 0 {
		condSQL, args, err := auditLogs.UserAgents.SQLClause("proto_payload.audit_log.request_metadata.caller_supplied_user_agent")
		if err != nil {
			return "", nil, fmt.Errorf("failed to build user agent conditions: %w", err)
		}
		if condSQL != "" {
			cteConditions = append(cteConditions, condSQL)
			cteArgs = append(cteArgs, args...)
		}
	}

	if len(auditLogs.PrincipalEmails) > 0 {
		condSQL, args, err := auditLogs.PrincipalEmails.SQLClause("proto_payload.audit_log.authentication_info.principal_email")
		if err != nil {
			return "", nil, fmt.Errorf("failed to build principal email conditions: %w", err)
		}
		if condSQL != "" {
			cteConditions = append(cteConditions, condSQL)
			cteArgs = append(cteArgs, args...)
		}
	}

	if len(auditLogs.Permissions) > 0 {
		condSQL, args, err := auditLogs.Permissions.SQLClause("permission")
		if err != nil {
			return "", nil, fmt.Errorf("failed to build permission conditions: %w", err)
		}
		if condSQL != "" {
			outerConditions = append(outerConditions, condSQL)
			outerArgs = append(outerArgs, args...)
		}
	}

	if len(auditLogs.ServiceNames) > 0 {
		condSQL, args, err := auditLogs.ServiceNames.SQLClause("service_name")
		if err != nil {
			return "", nil, fmt.Errorf("failed to build service name conditions: %w", err)
		}
		if condSQL != "" {
			outerConditions = append(outerConditions, condSQL)
			outerArgs = append(outerArgs, args...)
		}
	}

	cteWhereClause := strings.Join(cteConditions, " AND ")
	outerWhereClause := ""
	if len(outerConditions) > 0 {
		outerWhereClause = "WHERE " + strings.Join(outerConditions, " AND ")
	}

	finalQuery := fmt.Sprintf(`
WITH auth as (
  select  
    timestamp,
    proto_payload.audit_log,
    proto_payload.audit_log.service_name as service_name,
    proto_payload.audit_log.authentication_info.principal_email as email,  
    proto_payload.audit_log.authorization_info[0].permission_type AS permission_type,
    proto_payload.audit_log.authorization_info[0].permission AS permission
  FROM `+"`%s`"+`
  Where %s
) 

SELECT email, permission, permission_type, max(timestamp) as timestamp
from auth 
%s
group by email, permission, permission_type
`, auditLogs.Dataset, cteWhereClause, outerWhereClause)

	finalArgs := append(cteArgs, outerArgs...)
	args := lo.Map(finalArgs, func(arg any, _ int) bigquery.QueryParameter {
		return bigquery.QueryParameter{Value: arg}
	})

	return finalQuery, args, nil
}

// FetchAuditLogs fetches external roles and config accesses from BigQuery audit logs
func (gcp Scraper) FetchAuditLogs(ctx *GCPContext, config v1.GCP) (v1.ScrapeResults, error) {
	bqClient, err := bigquery.NewClient(ctx, config.Project, ctx.ClientOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create BigQuery client: %w", err)
	}
	defer func() {
		if err := bqClient.Close(); err != nil {
			ctx.Warnf("gcp audit logs: failed to close BigQuery client: %v", err)
		}
	}()

	var (
		// Keep track of permissions to create external roles
		uniquePermissions = set.New[string]()
		configAccesses    []v1.ExternalConfigAccess
	)

	query, params, err := buildAuditLogQuery(config.AuditLogs)
	if err != nil {
		return nil, fmt.Errorf("failed to build audit log query: %w", err)
	}

	q := bqClient.Query(query)
	q.Parameters = params
	it, err := q.Read(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to execute BigQuery query: %w", err)
	}

	for {
		var row BigQueryRow
		if err := it.Next(&row); err == iterator.Done {
			break
		} else if err != nil {
			return nil, fmt.Errorf("failed to read BigQuery row: %w", err)
		}

		// All the audit logs are attached to the project config.
		var resourceID v1.ExternalID
		resourceID.ExternalID = fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s", config.Project)
		resourceID.ConfigType = "GCP::Compute::Project"

		uniquePermissions.Insert(row.Permission)

		configAccesses = append(configAccesses, v1.ExternalConfigAccess{
			ConfigAccess: models.ConfigAccess{
				ID:             generateConsistentID(fmt.Sprintf("%s::%s::%s::%s", config.Project, row.Email, row.Permission, row.PermissionType)).String(), // one record per email, permission, and permission type
				ExternalUserID: lo.ToPtr(generateConsistentID(row.Email)),
				ExternalRoleID: lo.ToPtr(generateConsistentID(row.Permission)),
				ScraperID:      ctx.ScrapeConfig().GetPersistedID(),
				CreatedAt:      row.Timestamp,
			},
			ConfigExternalID: resourceID,
		})
	}

	externalRoles := lo.Map(uniquePermissions.UnsortedList(), func(permission string, _ int) models.ExternalRole {
		return models.ExternalRole{
			ID:        generateConsistentID(permission),
			Name:      permission,
			ScraperID: ctx.ScrapeConfig().GetPersistedID(),
		}
	})

	return v1.ScrapeResults{{
		BaseScraper:   config.BaseScraper,
		ExternalRoles: externalRoles,
		ConfigAccess:  configAccesses,
	}}, nil
}
