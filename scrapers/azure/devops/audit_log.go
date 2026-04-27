package devops

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	commonsHTTP "github.com/flanksource/commons/http"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

// auditLogLastFetchTime tracks the last audit log fetch time per organization.
var auditLogLastFetchTime sync.Map

type AuditLogEntry struct {
	ID               string         `json:"id"`
	CorrelationID    string         `json:"correlationId"`
	Timestamp        time.Time      `json:"timestamp"`
	ActionID         string         `json:"actionId"`
	Area             string         `json:"area"`
	Category         string         `json:"category"`
	CategoryDisplay  string         `json:"categoryDisplayName"`
	ActorDisplayName string         `json:"actorDisplayName"`
	ActorUserId      string         `json:"actorUserId"`
	Details          string         `json:"details"`
	Data             map[string]any `json:"data"`
	ProjectID        string         `json:"projectId"`
	ProjectName      string         `json:"projectName"`
	ScopeType        string         `json:"scopeType"`
	ScopeDisplayName string         `json:"scopeDisplayName"`
	IPAddress        string         `json:"ipAddress"`
}

type AzureDevopsAuditClient struct {
	*commonsHTTP.Client
	api.ScrapeContext
}

func NewAzureDevopsAuditClient(ctx api.ScrapeContext, ado v1.AzureDevops) (*AzureDevopsAuditClient, error) {
	org, token, err := resolveOrgAndToken(ctx, &ado)
	if err != nil {
		return nil, err
	}
	client := commonsHTTP.NewClient().
		BaseURL(fmt.Sprintf("https://auditservice.dev.azure.com/%s", org)).
		Auth(org, token)
	client = ctx.ConfigureHTTPClient(client, "azure.devops")
	return &AzureDevopsAuditClient{ScrapeContext: ctx, Client: client}, nil
}

func (c *AzureDevopsAuditClient) GetAuditLog(ctx context.Context, startTime, endTime time.Time) ([]AuditLogEntry, error) {
	resp, err := c.Client.R(ctx).
		QueryParam("format", "json").
		QueryParam("api-version", "7.1-preview.1").
		QueryParam("startTime", startTime.UTC().Format(time.RFC3339)).
		QueryParam("endTime", endTime.UTC().Format(time.RFC3339)).
		Get("/_apis/audit/downloadlog")
	if err != nil {
		return nil, fmt.Errorf("failed to download audit log: %w", err)
	}
	if !resp.IsOK() {
		body, _ := resp.AsString()
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, body)
	}

	body, err := resp.AsString()
	if err != nil {
		return nil, fmt.Errorf("failed to read audit log response: %w", err)
	}

	// Strip UTF-8 BOM that Azure DevOps includes in download responses
	body = strings.TrimPrefix(body, "\xef\xbb\xbf")

	var entries []AuditLogEntry
	if err := json.Unmarshal([]byte(body), &entries); err != nil {
		return nil, fmt.Errorf("failed to decode audit log: %w", err)
	}

	c.Logger.V(3).Infof("audit log download: %d entries", len(entries))
	return entries, nil
}

func mapAuditEntryToConfig(org string, entry AuditLogEntry) (externalID, configType string) {
	project := entry.ProjectName

	switch entry.Area {
	case "Pipelines":
		if id, ok := dataInt(entry.Data, "PipelineId"); ok && project != "" {
			return PipelineExternalID(org, project, id), PipelineType
		}
	case "Release":
		if id, ok := dataInt(entry.Data, "PipelineId"); ok && project != "" {
			return ReleaseExternalID(org, project, id), ReleaseType
		}
		if id, ok := dataInt(entry.Data, "ReleaseDefinitionId"); ok && project != "" {
			return ReleaseExternalID(org, project, id), ReleaseType
		}
	case "Git", "Policy", "Permissions":
		if repoID, ok := entry.Data["RepoId"].(string); ok && project != "" {
			return RepositoryExternalID(org, project, repoID), RepositoryType
		}
		if repoID, ok := entry.Data["RepositoryIdFromToken"].(string); ok {
			p := project
			if p == "" {
				if pn, ok := entry.Data["ProjectNameFromToken"].(string); ok {
					p = pn
				}
			}
			if p != "" {
				return RepositoryExternalID(org, p, repoID), RepositoryType
			}
		}
	}

	return "", ""
}

func dataInt(data map[string]any, key string) (int, bool) {
	v, ok := data[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case string:
		i, err := strconv.Atoi(n)
		return i, err == nil
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	}
	return 0, false
}

func isExcluded(actionID string, exclusions []string) bool {
	for _, prefix := range exclusions {
		if strings.HasPrefix(actionID, prefix) {
			return true
		}
	}
	return false
}

func auditSeverity(category, area string) string {
	switch category {
	case "remove":
		return "high"
	}
	switch area {
	case "Security", "Token":
		return "medium"
	}
	return "info"
}

func (ado AzureDevopsScraper) scrapeAuditLog(ctx api.ScrapeContext, config v1.AzureDevops) v1.ScrapeResults {
	maxAge := resolveMaxAge(config, ctx)
	startTime := time.Now().Add(-maxAge)
	if v, ok := auditLogLastFetchTime.Load(config.Organization); ok {
		if last, ok := v.(time.Time); ok && last.After(startTime) {
			startTime = last
		}
	}
	endTime := time.Now()

	auditClient, err := NewAzureDevopsAuditClient(ctx, config)
	if err != nil {
		var results v1.ScrapeResults
		results.Errorf(err, "failed to create audit log client for %s", config.Organization)
		return results
	}

	ctx.Logger.V(3).Infof("[%s] querying audit log from %s to %s", config.Organization, startTime.UTC().Format(time.RFC3339), endTime.UTC().Format(time.RFC3339))

	entries, err := auditClient.GetAuditLog(ctx, startTime, endTime)
	if err != nil {
		var results v1.ScrapeResults
		results.Errorf(err, "failed to fetch audit log for %s", config.Organization)
		return results
	}

	ctx.Logger.V(3).Infof("[%s] fetched %d audit log entries", config.Organization, len(entries))

	exclusions := config.AuditLog.Exclusions

	// Group by correlationId
	grouped := map[string][]AuditLogEntry{}
	var groupOrder []string
	var latestTimestamp time.Time

	for _, entry := range entries {
		if isExcluded(entry.ActionID, exclusions) {
			continue
		}
		if entry.Timestamp.After(latestTimestamp) {
			latestTimestamp = entry.Timestamp
		}
		cid := entry.CorrelationID
		if cid == "" {
			cid = entry.ID
		}
		if _, exists := grouped[cid]; !exists {
			groupOrder = append(groupOrder, cid)
		}
		grouped[cid] = append(grouped[cid], entry)
	}

	var results v1.ScrapeResults
	for _, cid := range groupOrder {
		group := grouped[cid]
		primary := group[0]

		externalID, configType := mapAuditEntryToConfig(config.Organization, primary)

		createdAt := primary.Timestamp
		for _, e := range group[1:] {
			if e.Timestamp.Before(createdAt) {
				createdAt = e.Timestamp
			}
		}

		var createdBy *string
		if primary.ActorDisplayName != "" {
			createdBy = &primary.ActorDisplayName
		}

		change := v1.ChangeResult{
			ChangeType:       primary.ActionID,
			ExternalChangeID: primary.ID,
			ExternalID:       externalID,
			ConfigType:       configType,
			CreatedAt:        &createdAt,
			CreatedBy:        createdBy,
			Summary:          primary.Details,
			Details:          v1.NewJSON(primary),
			Source:           "AzureDevops/AuditLog",
			Severity:         auditSeverity(primary.Category, primary.Area),
		}

		results = append(results, v1.ScrapeResult{
			BaseScraper: config.BaseScraper,
			Changes:     []v1.ChangeResult{change},
		})
	}

	if !latestTimestamp.IsZero() {
		auditLogLastFetchTime.Store(config.Organization, latestTimestamp)
	}

	return results
}
