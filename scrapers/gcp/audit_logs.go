package gcp

import (
	"fmt"
	"slices"
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
	ProjectID      string    `bigquery:"project_id"`
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
    proto_payload.audit_log.service_name as service_name,
    proto_payload.audit_log.authentication_info.principal_email as email,
    authorization.permission_type AS permission_type,
    authorization.permission AS permission,
    COALESCE(
      NULLIF(JSON_VALUE(TO_JSON(resource.labels), '$.project_id'), ''),
      REGEXP_EXTRACT(authorization.resource, r'(?:^|/)projects/([^/]+)'),
      REGEXP_EXTRACT(log_name, r'(?:^|/)projects/([^/]+)'),
      ''
    ) AS project_id
  FROM `+"`%s`"+`,
  UNNEST(proto_payload.audit_log.authorization_info) AS authorization
  Where %s
) 

SELECT email, permission, permission_type, project_id, max(timestamp) as timestamp
from auth 
%s
group by email, permission, permission_type, project_id
`, auditLogs.Dataset, cteWhereClause, outerWhereClause)

	finalArgs := append(cteArgs, outerArgs...)
	args := lo.Map(finalArgs, func(arg any, _ int) bigquery.QueryParameter {
		return bigquery.QueryParameter{Value: arg}
	})

	return finalQuery, args, nil
}

// auditLogAffectedProject resolves the project affected by a log row. Project
// roots restrict an aggregated sink to the selected scrape scope; an
// organization root means every affected project is allowed. Rows without an
// affected project are skipped rather than attributed to the dataset project.
func auditLogAffectedProject(row BigQueryRow, parents []string) (string, bool) {
	var scopedProjects []string
	for _, parent := range parents {
		if project := projectFromParent(parent); project != "" {
			scopedProjects = append(scopedProjects, project)
		}
	}

	project := strings.TrimPrefix(row.ProjectID, v1.ProjectPrefix)
	if project == "" {
		return "", false
	}
	if len(scopedProjects) > 0 && !slices.Contains(scopedProjects, project) {
		return "", false
	}
	return project, true
}

// FetchAuditLogs fetches external roles and config accesses from BigQuery audit
// logs. datasetProject is only the BigQuery transport/billing project; each row
// is attached to the project affected by the underlying audit event.
func (gcp Scraper) FetchAuditLogs(ctx *GCPContext, config v1.GCP, datasetProject string, parents []string) (v1.ScrapeResults, error) {
	bqClient, err := bigquery.NewClient(ctx, datasetProject, ctx.ClientOpts...)
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

		affectedProject, ok := auditLogAffectedProject(row, parents)
		if !ok {
			ctx.Warnf("gcp audit logs: skipping access with unresolved or out-of-scope project %q", row.ProjectID)
			continue
		}

		resourceID := v1.ExternalID{
			ExternalID: fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s", affectedProject),
			ConfigType: v1.GCPProject,
		}

		uniquePermissions.Insert(row.Permission)

		configAccesses = append(configAccesses, v1.ExternalConfigAccess{
			ID:               generateConsistentID(fmt.Sprintf("%s::%s::%s::%s", affectedProject, row.Email, row.Permission, row.PermissionType)).String(),
			ExternalUserID:   lo.ToPtr(generateConsistentID(row.Email)),
			ExternalRoleID:   lo.ToPtr(generateConsistentID(row.Permission)),
			OwnerScraperID:   ctx.ScrapeConfig().GetPersistedID(),
			CreatedAt:        row.Timestamp,
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
