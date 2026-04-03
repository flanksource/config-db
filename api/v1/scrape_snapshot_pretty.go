package v1

import (
	"fmt"
	"sort"
	"strings"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
)

// PrettyShort returns a one-line summary for terminal/log output, omitting zero values.
func (s *ScrapeSnapshot) PrettyShort() string {
	if s == nil {
		return "no snapshot"
	}
	configTotal := 0
	for _, v := range s.PerConfigType {
		configTotal += v.Total
	}
	var parts []string
	add := func(label string, n int) {
		if n != 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", label, n))
		}
	}
	add("configs", configTotal)
	add("users", s.ExternalUsers.Total)
	add("groups", s.ExternalGroups.Total)
	add("roles", s.ExternalRoles.Total)
	add("user_groups", s.ExternalUserGroups.Total)
	add("access", s.ConfigAccess.Total)
	add("access_logs", s.ConfigAccessLogs.Total)
	if len(parts) == 0 {
		return "empty"
	}
	return strings.Join(parts, " ")
}

// Pretty renders the full snapshot using the clicky Text API.
func (s *ScrapeSnapshot) Pretty() api.Text {
	if s == nil {
		return clicky.Text("")
	}
	t := clicky.Text("")

	if len(s.PerScraper) > 0 {
		t = t.Append("Configs — Per Scraper", "font-bold").NewLine()
		for _, k := range sortedKeys(s.PerScraper) {
			t = appendGroupRow(t, k, s.PerScraper[k])
		}
	}

	if len(s.PerConfigType) > 0 {
		t = t.Append("Configs — Per Type", "font-bold").NewLine()
		for _, k := range sortedKeys(s.PerConfigType) {
			t = appendGroupRow(t, k, s.PerConfigType[k])
		}
	}

	for _, e := range []struct {
		label  string
		counts EntityWindowCounts
	}{
		{"External Users", s.ExternalUsers},
		{"External Groups", s.ExternalGroups},
		{"External Roles", s.ExternalRoles},
		{"External User Groups", s.ExternalUserGroups},
		{"Config Access", s.ConfigAccess},
		{"Access Logs", s.ConfigAccessLogs},
	} {
		if !e.counts.IsZero() {
			t = appendEntityRow(t, e.label, e.counts)
		}
	}

	return t
}

// PrettyShort returns a one-line summary of the diff. Only entity totals are
// shown; per-scraper / per-type detail lives in the full Pretty output.
func (d ScrapeSnapshotDiff) PrettyShort() string {
	var parts []string
	configDelta := 0
	for _, v := range d.PerConfigType {
		configDelta += v.Total
	}
	if configDelta != 0 {
		parts = append(parts, fmt.Sprintf("configs=%s", signed(configDelta)))
	}
	addIfNonZero := func(label string, delta int) {
		if delta != 0 {
			parts = append(parts, fmt.Sprintf("%s=%s", label, signed(delta)))
		}
	}
	addIfNonZero("users", d.ExternalUsers.Total)
	addIfNonZero("groups", d.ExternalGroups.Total)
	addIfNonZero("roles", d.ExternalRoles.Total)
	addIfNonZero("user_groups", d.ExternalUserGroups.Total)
	addIfNonZero("access", d.ConfigAccess.Total)
	addIfNonZero("access_logs", d.ConfigAccessLogs.Total)
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, " ")
}

// Pretty renders the diff. Only rows with at least one non-zero counter are
// shown. Positive totals get a green class, negative totals get red.
func (d ScrapeSnapshotDiff) Pretty() api.Text {
	t := clicky.Text("")

	if len(d.PerScraper) > 0 {
		t = t.Append("Configs — Per Scraper", "font-bold").NewLine()
		for _, k := range sortedKeys(d.PerScraper) {
			t = appendDiffRow(t, k, d.PerScraper[k])
		}
	}

	if len(d.PerConfigType) > 0 {
		t = t.Append("Configs — Per Type", "font-bold").NewLine()
		for _, k := range sortedKeys(d.PerConfigType) {
			t = appendDiffRow(t, k, d.PerConfigType[k])
		}
	}

	if !d.ExternalUsers.IsZero() {
		t = appendDiffRow(t, "External Users", d.ExternalUsers)
	}
	if !d.ExternalGroups.IsZero() {
		t = appendDiffRow(t, "External Groups", d.ExternalGroups)
	}
	if !d.ExternalRoles.IsZero() {
		t = appendDiffRow(t, "External Roles", d.ExternalRoles)
	}
	if !d.ExternalUserGroups.IsZero() {
		t = appendDiffRow(t, "External User Groups", d.ExternalUserGroups)
	}
	if !d.ConfigAccess.IsZero() {
		t = appendDiffRow(t, "Config Access", d.ConfigAccess)
	}
	if !d.ConfigAccessLogs.IsZero() {
		t = appendDiffRow(t, "Access Logs", d.ConfigAccessLogs)
	}

	return t
}

// Pretty renders Before / After / Diff stacked, with the diff surfaced first
// since it's usually the most useful view.
func (p *ScrapeSnapshotPair) Pretty() api.Text {
	if p == nil {
		return clicky.Text("")
	}
	t := clicky.Text("")
	t = t.Append("Snapshot Diff", "font-bold text-lg").NewLine()
	t = t.Add(p.Diff.Pretty())
	if p.After != nil {
		t = t.NewLine().Append("After", "font-bold text-lg").NewLine()
		t = t.Add(p.After.Pretty())
	}
	if p.Before != nil {
		t = t.NewLine().Append("Before", "font-bold text-lg").NewLine()
		t = t.Add(p.Before.Pretty())
	}
	return t
}

func sortedKeys(m map[string]EntityWindowCounts) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func appendWindowCounts(t api.Text, c EntityWindowCounts) api.Text {
	t = t.Append(fmt.Sprintf("total=%d", c.Total), "text-muted")
	if c.UpdatedLast != 0 || c.UpdatedHour != 0 || c.UpdatedDay != 0 || c.UpdatedWeek != 0 {
		t = t.Append(fmt.Sprintf(" upd(L=%d H=%d D=%d W=%d)", c.UpdatedLast, c.UpdatedHour, c.UpdatedDay, c.UpdatedWeek))
	}
	if c.DeletedLast != 0 || c.DeletedHour != 0 || c.DeletedDay != 0 || c.DeletedWeek != 0 {
		t = t.Append(fmt.Sprintf(" del(L=%d H=%d D=%d W=%d)", c.DeletedLast, c.DeletedHour, c.DeletedDay, c.DeletedWeek))
	}
	if c.LastCreatedAt != nil {
		t = t.Append(fmt.Sprintf(" created=%s", c.LastCreatedAt.Format("15:04:05")), "text-muted")
	}
	if c.LastUpdatedAt != nil {
		t = t.Append(fmt.Sprintf(" updated=%s", c.LastUpdatedAt.Format("15:04:05")), "text-muted")
	}
	return t
}

func appendGroupRow(t api.Text, label string, c EntityWindowCounts) api.Text {
	t = t.Append("  " + label + " ")
	return appendWindowCounts(t, c).NewLine()
}

func appendEntityRow(t api.Text, label string, c EntityWindowCounts) api.Text {
	t = t.Append(label+": ", "font-bold")
	return appendWindowCounts(t, c).NewLine()
}

func appendDiffRow(t api.Text, label string, c EntityWindowCounts) api.Text {
	t = t.Append("  " + label + " ")
	t = t.Append(fmt.Sprintf("total=%s", signed(c.Total)), totalClass(c.Total))
	if c.UpdatedLast != 0 || c.UpdatedHour != 0 || c.UpdatedDay != 0 || c.UpdatedWeek != 0 {
		t = t.Append(fmt.Sprintf(
			" upd(L=%s H=%s D=%s W=%s)",
			signed(c.UpdatedLast), signed(c.UpdatedHour), signed(c.UpdatedDay), signed(c.UpdatedWeek),
		))
	}
	if c.DeletedLast != 0 || c.DeletedHour != 0 || c.DeletedDay != 0 || c.DeletedWeek != 0 {
		t = t.Append(fmt.Sprintf(
			" del(L=%s H=%s D=%s W=%s)",
			signed(c.DeletedLast), signed(c.DeletedHour), signed(c.DeletedDay), signed(c.DeletedWeek),
		))
	}
	return t.NewLine()
}

func signed(n int) string {
	if n >= 0 {
		return fmt.Sprintf("+%d", n)
	}
	return fmt.Sprintf("%d", n)
}

func totalClass(n int) string {
	switch {
	case n > 0:
		return "text-green-500"
	case n < 0:
		return "text-red-500"
	default:
		return "text-muted"
	}
}
