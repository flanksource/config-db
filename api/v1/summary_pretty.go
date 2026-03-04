package v1

import (
	"fmt"
	"sort"
	"strings"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/samber/lo"
)

func (s ScrapeSummary) PrettyShort() string {
	var parts []string

	totals := s.Totals()
	if totals.Added > 0 || totals.Updated > 0 || totals.Unchanged > 0 {
		parts = append(parts, fmt.Sprintf("configs(+%d/~%d/=%d)", totals.Added, totals.Updated, totals.Unchanged))
	}
	if totals.Changes > 0 || totals.Deduped > 0 {
		cp := fmt.Sprintf("changes(+%d", totals.Changes)
		if totals.Deduped > 0 {
			cp += fmt.Sprintf("/dedup=%d", totals.Deduped)
		}
		cp += ")"
		parts = append(parts, cp)
	}
	if !s.ExternalUsers.IsEmpty() {
		parts = append(parts, fmt.Sprintf("users=%d", s.ExternalUsers.Saved))
	}
	if !s.ExternalGroups.IsEmpty() {
		parts = append(parts, fmt.Sprintf("groups=%d", s.ExternalGroups.Saved))
	}
	if !s.ExternalRoles.IsEmpty() {
		parts = append(parts, fmt.Sprintf("roles=%d", s.ExternalRoles.Saved))
	}
	if !s.ConfigAccess.IsEmpty() {
		parts = append(parts, fmt.Sprintf("access=%d", s.ConfigAccess.Saved))
	}
	if !s.AccessLogs.IsEmpty() {
		parts = append(parts, fmt.Sprintf("logs=%d", s.AccessLogs.Saved))
	}
	if len(parts) == 0 {
		return "no changes"
	}
	return strings.Join(parts, " ")
}

func (s ScrapeSummary) Pretty() api.Text {
	t := clicky.Text("")

	types := lo.Keys(s.ConfigTypes)
	sort.Strings(types)

	for _, configType := range types {
		v := s.ConfigTypes[configType]
		hasChangeTotals := false
		if v.Change != nil {
			ignored, orphaned, fkErrors := v.Change.Totals()
			hasChangeTotals = ignored > 0 || orphaned > 0 || fkErrors > 0
		}
		if v.Added == 0 && v.Updated == 0 && v.Unchanged == 0 && v.Changes == 0 && v.Deduped == 0 && !hasChangeTotals && len(v.Warnings) == 0 {
			continue
		}
		t = t.Append(configType, "font-bold")
		t = t.Append(fmt.Sprintf(" +%d ~%d =%d", v.Added, v.Updated, v.Unchanged), "text-muted")
		if v.Changes > 0 || v.Deduped > 0 {
			t = t.Append(fmt.Sprintf(" changes=%d dedup=%d", v.Changes, v.Deduped))
		}
		if v.Change != nil {
			ignored, orphaned, fkErrors := v.Change.Totals()
			if ignored > 0 || orphaned > 0 || fkErrors > 0 {
				t = t.Append(fmt.Sprintf(" ignored=%d orphaned=%d fk_errors=%d", ignored, orphaned, fkErrors), "text-warning")
			}
		}
		for _, w := range v.Warnings {
			t = t.Append(fmt.Sprintf(" warning: %s", w), "text-warning")
		}
		t = t.NewLine()
	}

	appendEntity := func(label string, e EntitySummary) {
		if e.IsEmpty() {
			return
		}
		t = t.Append(label, "font-bold").Append(fmt.Sprintf(" %s", e), "text-muted").NewLine()
	}

	appendEntity("External Users:", s.ExternalUsers)
	appendEntity("External Groups:", s.ExternalGroups)
	appendEntity("External Roles:", s.ExternalRoles)
	appendEntity("Config Access:", s.ConfigAccess)
	appendEntity("Access Logs:", s.AccessLogs)

	return t
}
