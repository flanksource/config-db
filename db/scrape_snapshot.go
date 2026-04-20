package db

import (
	"fmt"
	"strings"
	"time"

	v1 "github.com/flanksource/config-db/api/v1"

	"github.com/flanksource/config-db/api"
)

// CaptureScrapeSnapshot captures a point-in-time snapshot of every entity
// table touched by a scrape. runStart anchors the "Last" bucket: rows whose
// updated_at or deleted_at is >= runStart count as "Last" even when the
// after-snapshot is captured minutes later.
//
// Errors are returned but callers typically log-and-continue — the snapshot
// is observability, not load-bearing on the scrape itself.
func CaptureScrapeSnapshot(ctx api.ScrapeContext, runStart time.Time) (*v1.ScrapeSnapshot, error) {
	snap := &v1.ScrapeSnapshot{
		CapturedAt:    time.Now(),
		RunStartedAt:  runStart,
		PerScraper:    map[string]v1.EntityWindowCounts{},
		PerConfigType: map[string]v1.EntityWindowCounts{},
	}

	perScraper, err := queryGrouped(ctx, runStart, configItemsPerScraperSQL)
	if err != nil {
		return nil, fmt.Errorf("per-scraper config_items: %w", err)
	}
	snap.PerScraper = perScraper

	perType, err := queryGrouped(ctx, runStart, configItemsPerTypeSQL)
	if err != nil {
		return nil, fmt.Errorf("per-type config_items: %w", err)
	}
	snap.PerConfigType = perType

	type single struct {
		sql    string
		target *v1.EntityWindowCounts
		label  string
	}
	singles := []single{
		{externalUsersSQL, &snap.ExternalUsers, "external_users"},
		{externalGroupsSQL, &snap.ExternalGroups, "external_groups"},
		{externalRolesSQL, &snap.ExternalRoles, "external_roles"},
		{externalUserGroupsSQL, &snap.ExternalUserGroups, "external_user_groups"},
		{configAccessSQL, &snap.ConfigAccess, "config_access"},
		{configAccessLogsSQL, &snap.ConfigAccessLogs, "config_access_logs"},
	}
	for _, s := range singles {
		counts, err := querySingle(ctx, runStart, s.sql)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.label, err)
		}
		*s.target = counts
	}

	return snap, nil
}

// queryArgs returns runStart repeated once per `?` placeholder found in sql,
// so gorm.Raw's positional substitution binds every placeholder to the same
// anchor timestamp in a single call.
func queryArgs(sql string, runStart time.Time) []any {
	n := strings.Count(sql, "?")
	args := make([]any, n)
	for i := range args {
		args[i] = runStart
	}
	return args
}

// queryGrouped runs a GROUP BY query returning (key, windowed counts) rows and
// assembles the result map. The SQL must select the grouping key as the first
// column and the 9 count columns in the EntityWindowCounts field order, and
// use exactly 9 `?` placeholders bound to runStart.
func queryGrouped(ctx api.ScrapeContext, runStart time.Time, sql string) (map[string]v1.EntityWindowCounts, error) {
	type row struct {
		Key           string     `gorm:"column:key"`
		Total         int        `gorm:"column:total"`
		UpdatedLast   int        `gorm:"column:updated_last"`
		UpdatedHour   int        `gorm:"column:updated_hour"`
		UpdatedDay    int        `gorm:"column:updated_day"`
		UpdatedWeek   int        `gorm:"column:updated_week"`
		DeletedLast   int        `gorm:"column:deleted_last"`
		DeletedHour   int        `gorm:"column:deleted_hour"`
		DeletedDay    int        `gorm:"column:deleted_day"`
		DeletedWeek   int        `gorm:"column:deleted_week"`
		LastCreatedAt *time.Time `gorm:"column:last_created_at"`
		LastUpdatedAt *time.Time `gorm:"column:last_updated_at"`
	}
	var rows []row
	if err := ctx.DB().Raw(sql, queryArgs(sql, runStart)...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]v1.EntityWindowCounts, len(rows))
	for _, r := range rows {
		out[r.Key] = v1.EntityWindowCounts{
			Total:         r.Total,
			UpdatedLast:   r.UpdatedLast,
			UpdatedHour:   r.UpdatedHour,
			UpdatedDay:    r.UpdatedDay,
			UpdatedWeek:   r.UpdatedWeek,
			DeletedLast:   r.DeletedLast,
			DeletedHour:   r.DeletedHour,
			DeletedDay:    r.DeletedDay,
			DeletedWeek:   r.DeletedWeek,
			LastCreatedAt: r.LastCreatedAt,
			LastUpdatedAt: r.LastUpdatedAt,
		}
	}
	return out, nil
}

// querySingle runs a non-grouped aggregate query returning one row with the 9
// count columns (same column ordering as queryGrouped).
func querySingle(ctx api.ScrapeContext, runStart time.Time, sql string) (v1.EntityWindowCounts, error) {
	var row struct {
		Total         int        `gorm:"column:total"`
		UpdatedLast   int        `gorm:"column:updated_last"`
		UpdatedHour   int        `gorm:"column:updated_hour"`
		UpdatedDay    int        `gorm:"column:updated_day"`
		UpdatedWeek   int        `gorm:"column:updated_week"`
		DeletedLast   int        `gorm:"column:deleted_last"`
		DeletedHour   int        `gorm:"column:deleted_hour"`
		DeletedDay    int        `gorm:"column:deleted_day"`
		DeletedWeek   int        `gorm:"column:deleted_week"`
		LastCreatedAt *time.Time `gorm:"column:last_created_at"`
		LastUpdatedAt *time.Time `gorm:"column:last_updated_at"`
	}
	if err := ctx.DB().Raw(sql, queryArgs(sql, runStart)...).Scan(&row).Error; err != nil {
		return v1.EntityWindowCounts{}, err
	}
	return v1.EntityWindowCounts{
		Total:         row.Total,
		UpdatedLast:   row.UpdatedLast,
		UpdatedHour:   row.UpdatedHour,
		UpdatedDay:    row.UpdatedDay,
		UpdatedWeek:   row.UpdatedWeek,
		DeletedLast:   row.DeletedLast,
		DeletedHour:   row.DeletedHour,
		DeletedDay:    row.DeletedDay,
		DeletedWeek:   row.DeletedWeek,
		LastCreatedAt: row.LastCreatedAt,
		LastUpdatedAt: row.LastUpdatedAt,
	}, nil
}

// The SQL below uses COUNT(*) FILTER (WHERE ...) so every bucket is computed
// in a single table scan. For tables missing updated_at or deleted_at, the
// corresponding aggregates are replaced with literal 0 — no belt-and-braces
// recomputation, consistent with the rest of the pipeline.

const configItemsPerScraperSQL = `
SELECT
  COALESCE(cs.name, ci.scraper_id::text, 'unscoped') AS key,
  COUNT(*) FILTER (WHERE ci.deleted_at IS NULL)                                AS total,
  COUNT(*) FILTER (WHERE ci.updated_at >= ?::timestamptz)                      AS updated_last,
  COUNT(*) FILTER (WHERE ci.updated_at >= ?::timestamptz - interval '1 hour')  AS updated_hour,
  COUNT(*) FILTER (WHERE ci.updated_at >= ?::timestamptz - interval '1 day')   AS updated_day,
  COUNT(*) FILTER (WHERE ci.updated_at >= ?::timestamptz - interval '7 days')  AS updated_week,
  COUNT(*) FILTER (WHERE ci.deleted_at >= ?::timestamptz)                      AS deleted_last,
  COUNT(*) FILTER (WHERE ci.deleted_at >= ?::timestamptz - interval '1 hour')  AS deleted_hour,
  COUNT(*) FILTER (WHERE ci.deleted_at >= ?::timestamptz - interval '1 day')   AS deleted_day,
  COUNT(*) FILTER (WHERE ci.deleted_at >= ?::timestamptz - interval '7 days')  AS deleted_week,
  MAX(ci.created_at) AS last_created_at,
  MAX(ci.updated_at) AS last_updated_at
FROM config_items ci
LEFT JOIN config_scrapers cs ON cs.id = ci.scraper_id
GROUP BY key
`

const configItemsPerTypeSQL = `
SELECT
  ci.type AS key,
  COUNT(*) FILTER (WHERE ci.deleted_at IS NULL)                                AS total,
  COUNT(*) FILTER (WHERE ci.updated_at >= ?::timestamptz)                      AS updated_last,
  COUNT(*) FILTER (WHERE ci.updated_at >= ?::timestamptz - interval '1 hour')  AS updated_hour,
  COUNT(*) FILTER (WHERE ci.updated_at >= ?::timestamptz - interval '1 day')   AS updated_day,
  COUNT(*) FILTER (WHERE ci.updated_at >= ?::timestamptz - interval '7 days')  AS updated_week,
  COUNT(*) FILTER (WHERE ci.deleted_at >= ?::timestamptz)                      AS deleted_last,
  COUNT(*) FILTER (WHERE ci.deleted_at >= ?::timestamptz - interval '1 hour')  AS deleted_hour,
  COUNT(*) FILTER (WHERE ci.deleted_at >= ?::timestamptz - interval '1 day')   AS deleted_day,
  COUNT(*) FILTER (WHERE ci.deleted_at >= ?::timestamptz - interval '7 days')  AS deleted_week,
  MAX(ci.created_at) AS last_created_at,
  MAX(ci.updated_at) AS last_updated_at
FROM config_items ci
WHERE ci.type IS NOT NULL
GROUP BY ci.type
`

const externalUsersSQL = `
SELECT
  COUNT(*) FILTER (WHERE deleted_at IS NULL)                                AS total,
  COUNT(*) FILTER (WHERE updated_at >= ?::timestamptz)                      AS updated_last,
  COUNT(*) FILTER (WHERE updated_at >= ?::timestamptz - interval '1 hour')  AS updated_hour,
  COUNT(*) FILTER (WHERE updated_at >= ?::timestamptz - interval '1 day')   AS updated_day,
  COUNT(*) FILTER (WHERE updated_at >= ?::timestamptz - interval '7 days')  AS updated_week,
  COUNT(*) FILTER (WHERE deleted_at >= ?::timestamptz)                      AS deleted_last,
  COUNT(*) FILTER (WHERE deleted_at >= ?::timestamptz - interval '1 hour')  AS deleted_hour,
  COUNT(*) FILTER (WHERE deleted_at >= ?::timestamptz - interval '1 day')   AS deleted_day,
  COUNT(*) FILTER (WHERE deleted_at >= ?::timestamptz - interval '7 days')  AS deleted_week,
  MAX(created_at) AS last_created_at,
  MAX(updated_at) AS last_updated_at
FROM external_users
`

const externalGroupsSQL = `
SELECT
  COUNT(*) FILTER (WHERE deleted_at IS NULL)                                AS total,
  COUNT(*) FILTER (WHERE updated_at >= ?::timestamptz)                      AS updated_last,
  COUNT(*) FILTER (WHERE updated_at >= ?::timestamptz - interval '1 hour')  AS updated_hour,
  COUNT(*) FILTER (WHERE updated_at >= ?::timestamptz - interval '1 day')   AS updated_day,
  COUNT(*) FILTER (WHERE updated_at >= ?::timestamptz - interval '7 days')  AS updated_week,
  COUNT(*) FILTER (WHERE deleted_at >= ?::timestamptz)                      AS deleted_last,
  COUNT(*) FILTER (WHERE deleted_at >= ?::timestamptz - interval '1 hour')  AS deleted_hour,
  COUNT(*) FILTER (WHERE deleted_at >= ?::timestamptz - interval '1 day')   AS deleted_day,
  COUNT(*) FILTER (WHERE deleted_at >= ?::timestamptz - interval '7 days')  AS deleted_week,
  MAX(created_at) AS last_created_at,
  MAX(updated_at) AS last_updated_at
FROM external_groups
`

const externalRolesSQL = `
SELECT
  COUNT(*) FILTER (WHERE deleted_at IS NULL)                                AS total,
  COUNT(*) FILTER (WHERE updated_at >= ?::timestamptz)                      AS updated_last,
  COUNT(*) FILTER (WHERE updated_at >= ?::timestamptz - interval '1 hour')  AS updated_hour,
  COUNT(*) FILTER (WHERE updated_at >= ?::timestamptz - interval '1 day')   AS updated_day,
  COUNT(*) FILTER (WHERE updated_at >= ?::timestamptz - interval '7 days')  AS updated_week,
  COUNT(*) FILTER (WHERE deleted_at >= ?::timestamptz)                      AS deleted_last,
  COUNT(*) FILTER (WHERE deleted_at >= ?::timestamptz - interval '1 hour')  AS deleted_hour,
  COUNT(*) FILTER (WHERE deleted_at >= ?::timestamptz - interval '1 day')   AS deleted_day,
  COUNT(*) FILTER (WHERE deleted_at >= ?::timestamptz - interval '7 days')  AS deleted_week,
  MAX(created_at) AS last_created_at,
  MAX(updated_at) AS last_updated_at
FROM external_roles
`

// external_user_groups has deleted_at but no updated_at.
const externalUserGroupsSQL = `
SELECT
  COUNT(*) FILTER (WHERE deleted_at IS NULL)                                AS total,
  0                                                                         AS updated_last,
  0                                                                         AS updated_hour,
  0                                                                         AS updated_day,
  0                                                                         AS updated_week,
  COUNT(*) FILTER (WHERE deleted_at >= ?::timestamptz)                      AS deleted_last,
  COUNT(*) FILTER (WHERE deleted_at >= ?::timestamptz - interval '1 hour')  AS deleted_hour,
  COUNT(*) FILTER (WHERE deleted_at >= ?::timestamptz - interval '1 day')   AS deleted_day,
  COUNT(*) FILTER (WHERE deleted_at >= ?::timestamptz - interval '7 days')  AS deleted_week,
  NULL::timestamptz AS last_created_at,
  NULL::timestamptz AS last_updated_at
FROM external_user_groups
`

// config_access has deleted_at and created_at but no updated_at.
const configAccessSQL = `
SELECT
  COUNT(*) FILTER (WHERE deleted_at IS NULL)                                AS total,
  0                                                                         AS updated_last,
  0                                                                         AS updated_hour,
  0                                                                         AS updated_day,
  0                                                                         AS updated_week,
  COUNT(*) FILTER (WHERE deleted_at >= ?::timestamptz)                      AS deleted_last,
  COUNT(*) FILTER (WHERE deleted_at >= ?::timestamptz - interval '1 hour')  AS deleted_hour,
  COUNT(*) FILTER (WHERE deleted_at >= ?::timestamptz - interval '1 day')   AS deleted_day,
  COUNT(*) FILTER (WHERE deleted_at >= ?::timestamptz - interval '7 days')  AS deleted_week,
  MAX(created_at) AS last_created_at,
  NULL::timestamptz AS last_updated_at
FROM config_access
`

// config_access_logs has created_at but no updated_at or deleted_at.
const configAccessLogsSQL = `
SELECT
  COUNT(*) AS total,
  0 AS updated_last,
  0 AS updated_hour,
  0 AS updated_day,
  0 AS updated_week,
  0 AS deleted_last,
  0 AS deleted_hour,
  0 AS deleted_day,
  0 AS deleted_week,
  MAX(created_at) AS last_created_at,
  NULL::timestamptz AS last_updated_at
FROM config_access_logs
`
