package v1

import "time"

// EntityWindowCounts captures total + windowed updated/deleted counts for one
// entity type or group. Windows are anchored to the run's start time, not to
// capture time, so the "Last" bucket still means "touched during this run"
// even when the after-snapshot is captured minutes later.
//
// The row's updated_at / deleted_at column drives all buckets. Tables without
// one or both columns leave those fields at zero.
//
//	Last:  updated_at/deleted_at >= runStart
//	Hour:  >= runStart - 1h
//	Day:   >= runStart - 24h
//	Week:  >= runStart - 7d
type EntityWindowCounts struct {
	Total       int `json:"total"`
	UpdatedLast int `json:"updated_last"`
	UpdatedHour int `json:"updated_hour"`
	UpdatedDay  int `json:"updated_day"`
	UpdatedWeek int `json:"updated_week"`
	DeletedLast int `json:"deleted_last"`
	DeletedHour int `json:"deleted_hour"`
	DeletedDay  int `json:"deleted_day"`
	DeletedWeek int `json:"deleted_week"`

	LastCreatedAt *time.Time `json:"last_created_at,omitempty"`
	LastUpdatedAt *time.Time `json:"last_updated_at,omitempty"`
}

// IsZero reports whether all fields are zero.
func (e EntityWindowCounts) IsZero() bool {
	return e.Total == 0 &&
		e.UpdatedLast == 0 && e.UpdatedHour == 0 && e.UpdatedDay == 0 && e.UpdatedWeek == 0 &&
		e.DeletedLast == 0 && e.DeletedHour == 0 && e.DeletedDay == 0 && e.DeletedWeek == 0 &&
		e.LastCreatedAt == nil && e.LastUpdatedAt == nil
}

// Sub returns e - other, field-wise. Timestamps are taken from e (the "after" side).
func (e EntityWindowCounts) Sub(other EntityWindowCounts) EntityWindowCounts {
	return EntityWindowCounts{
		Total:         e.Total - other.Total,
		UpdatedLast:   e.UpdatedLast - other.UpdatedLast,
		UpdatedHour:   e.UpdatedHour - other.UpdatedHour,
		UpdatedDay:    e.UpdatedDay - other.UpdatedDay,
		UpdatedWeek:   e.UpdatedWeek - other.UpdatedWeek,
		DeletedLast:   e.DeletedLast - other.DeletedLast,
		DeletedHour:   e.DeletedHour - other.DeletedHour,
		DeletedDay:    e.DeletedDay - other.DeletedDay,
		DeletedWeek:   e.DeletedWeek - other.DeletedWeek,
		LastCreatedAt: e.LastCreatedAt,
		LastUpdatedAt: e.LastUpdatedAt,
	}
}

// ScrapeSnapshot is a point-in-time snapshot of global DB state relevant to a
// scrape run. Captured once before the scrape starts and once after it
// completes; the pair drives before/after/diff rendering in the scrapeui and
// in CLI pretty output.
type ScrapeSnapshot struct {
	CapturedAt   time.Time `json:"captured_at"`
	RunStartedAt time.Time `json:"run_started_at"`

	// PerScraper groups config_items by their scraper. The key is the scraper
	// name when available, falling back to the scraper_id string, or
	// "unscoped" for scraperless items.
	PerScraper map[string]EntityWindowCounts `json:"per_scraper"`

	// PerConfigType groups config_items globally (across all scrapers) by the
	// type column (e.g. "Kubernetes::Pod", "AWS::EC2::Instance").
	PerConfigType map[string]EntityWindowCounts `json:"per_config_type"`

	ExternalUsers      EntityWindowCounts `json:"external_users"`
	ExternalGroups     EntityWindowCounts `json:"external_groups"`
	ExternalRoles      EntityWindowCounts `json:"external_roles"`
	ExternalUserGroups EntityWindowCounts `json:"external_user_groups"`
	ConfigAccess       EntityWindowCounts `json:"config_access"`
	ConfigAccessLogs   EntityWindowCounts `json:"config_access_logs"`
}

// ScrapeSnapshotPair holds the before and after captures plus the computed diff.
type ScrapeSnapshotPair struct {
	Before *ScrapeSnapshot    `json:"before,omitempty"`
	After  *ScrapeSnapshot    `json:"after,omitempty"`
	Diff   ScrapeSnapshotDiff `json:"diff"`
}

// ScrapeSnapshotDiff is After - Before, field-wise. Per-scraper and per-type
// maps use the union of keys from both sides: a key present only in Before
// produces a negative-Total entry, and a key only in After produces a
// positive-Total entry.
type ScrapeSnapshotDiff struct {
	PerScraper         map[string]EntityWindowCounts `json:"per_scraper,omitempty"`
	PerConfigType      map[string]EntityWindowCounts `json:"per_config_type,omitempty"`
	ExternalUsers      EntityWindowCounts            `json:"external_users"`
	ExternalGroups     EntityWindowCounts            `json:"external_groups"`
	ExternalRoles      EntityWindowCounts            `json:"external_roles"`
	ExternalUserGroups EntityWindowCounts            `json:"external_user_groups"`
	ConfigAccess       EntityWindowCounts            `json:"config_access"`
	ConfigAccessLogs   EntityWindowCounts            `json:"config_access_logs"`
}

// DiffSnapshots computes After - Before field-wise. Nil snapshots are treated
// as zero-valued: if only one side is non-nil the diff equals that side (or
// its negation for Before-only).
func DiffSnapshots(before, after *ScrapeSnapshot) ScrapeSnapshotDiff {
	var b, a ScrapeSnapshot
	if before != nil {
		b = *before
	}
	if after != nil {
		a = *after
	}
	return ScrapeSnapshotDiff{
		PerScraper:         diffMaps(b.PerScraper, a.PerScraper),
		PerConfigType:      diffMaps(b.PerConfigType, a.PerConfigType),
		ExternalUsers:      a.ExternalUsers.Sub(b.ExternalUsers),
		ExternalGroups:     a.ExternalGroups.Sub(b.ExternalGroups),
		ExternalRoles:      a.ExternalRoles.Sub(b.ExternalRoles),
		ExternalUserGroups: a.ExternalUserGroups.Sub(b.ExternalUserGroups),
		ConfigAccess:       a.ConfigAccess.Sub(b.ConfigAccess),
		ConfigAccessLogs:   a.ConfigAccessLogs.Sub(b.ConfigAccessLogs),
	}
}

// diffMaps returns after - before over the union of keys. Only non-zero
// resulting entries are included, to keep the diff compact.
func diffMaps(before, after map[string]EntityWindowCounts) map[string]EntityWindowCounts {
	if len(before) == 0 && len(after) == 0 {
		return nil
	}
	keys := make(map[string]struct{}, len(before)+len(after))
	for k := range before {
		keys[k] = struct{}{}
	}
	for k := range after {
		keys[k] = struct{}{}
	}
	out := make(map[string]EntityWindowCounts, len(keys))
	for k := range keys {
		delta := after[k].Sub(before[k])
		if !delta.IsZero() {
			out[k] = delta
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
