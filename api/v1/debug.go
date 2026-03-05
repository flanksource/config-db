// ABOUTME: Renders debug output combining scrape results, HAR entries, and logs
// ABOUTME: into a single Pretty()-compatible structure for HTML/terminal display.
package v1

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/flanksource/clicky"
	"github.com/flanksource/clicky/api"
	"github.com/flanksource/commons/har"
	"sigs.k8s.io/yaml"
)

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// +kubebuilder:object:generate=false
type DebugResult struct {
	Results FullScrapeResults
	HAR     []har.Entry
	Logs    string
}

func (d DebugResult) Pretty() api.Text {
	t := clicky.Text("")

	// Scrape Results
	t = t.Append("Scrape Results", "font-bold text-lg").NewLine()
	t = t.Append(fmt.Sprintf("%d configs scraped", len(d.Results.Configs))).NewLine()
	t = t.NewLine()

	for _, config := range d.Results.Configs {
		t = t.Add(configDebugYAML(config)).NewLine()
	}

	// Changes
	if len(d.Results.Changes) > 0 {
		t = t.Append(fmt.Sprintf("Changes (%d)", len(d.Results.Changes)), "font-bold text-lg").NewLine()
		for _, change := range d.Results.Changes {
			t = t.Append(fmt.Sprintf("  %s: %s", change.ChangeType, change.Summary)).NewLine()
		}
		t = t.NewLine()
	}

	// Analysis
	if len(d.Results.Analysis) > 0 {
		t = t.Append(fmt.Sprintf("Analysis (%d)", len(d.Results.Analysis)), "font-bold text-lg").NewLine()
		for _, a := range d.Results.Analysis {
			t = t.Append(fmt.Sprintf("  [%s] %s: %s", a.Severity, a.AnalysisType, a.Summary)).NewLine()
		}
		t = t.NewLine()
	}

	// External Entities
	t = t.Add(externalEntitiesSection(d.Results))

	// HAR
	if len(d.HAR) > 0 {
		t = t.Append(fmt.Sprintf("HTTP Traffic (%d requests)", len(d.HAR)), "font-bold text-lg").NewLine()
		for _, entry := range d.HAR {
			status := fmt.Sprintf("%d %s", entry.Response.Status, entry.Response.StatusText)
			timing := fmt.Sprintf("%.0fms", entry.Time)
			t = t.Append(fmt.Sprintf("  %s %s → %s (%s)",
				entry.Request.Method, entry.Request.URL, status, timing)).NewLine()
		}
		t = t.NewLine()
	}

	// Logs
	if d.Logs != "" {
		t = t.Append("Logs", "font-bold text-lg").NewLine()
		for _, line := range strings.Split(stripANSI(d.Logs), "\n") {
			t = t.Add(colorLogLine(line)).NewLine()
		}
	}

	return t
}

// configDebugYAML renders ID/Name/Type as a visible header, with all other
// details (aliases, labels, tags, changes, config YAML) in a collapsible section.
func configDebugYAML(s ScrapeResult) api.Text {
	t := clicky.Text("")

	// Header line: always visible
	header := fmt.Sprintf("ID=%s|Name=%s|Type=%s", s.ID, s.Name, s.Type)

	// Build collapsible details
	details := clicky.Text("")

	if s.Status != "" {
		details = details.Append("Status: ", "text-muted").Append(s.Status).NewLine()
	}
	if s.Health != "" {
		details = details.Append("Health: ", "text-muted").Append(string(s.Health)).NewLine()
	}
	if s.Error != nil {
		details = details.Append("Error: ", "text-red-500").Append(s.Error.Error()).NewLine()
	}
	if len(s.Aliases) > 0 {
		details = details.Append("Aliases: ", "text-muted").Append(strings.Join(s.Aliases, ", ")).NewLine()
	}
	if s.Source != "" {
		details = details.Append("Source: ", "text-muted").Append(s.Source).NewLine()
	}

	if len(s.Labels) > 0 {
		details = details.Append("Labels: ", "text-muted").NewLine()
		for k, v := range s.Labels {
			details = details.Append(fmt.Sprintf("  %s: %s", k, v)).NewLine()
		}
	}
	if len(s.Tags) > 0 {
		details = details.Append("Tags: ", "text-muted").NewLine()
		for k, v := range s.Tags {
			details = details.Append(fmt.Sprintf("  %s: %s", k, v)).NewLine()
		}
	}
	if len(s.Properties) > 0 {
		details = details.Append("Properties: ", "text-muted").NewLine()
		for k, v := range s.Properties.AsMap() {
			details = details.Append(fmt.Sprintf("  %s: %v", k, v)).NewLine()
		}
	}

	if len(s.Changes) > 0 {
		details = details.Append("Changes:", "text-muted").NewLine()
		for _, change := range s.Changes {
			details = details.Append(fmt.Sprintf("  - %s: %s", change.ChangeType, change.Summary)).NewLine()
		}
	}

	if s.Config != nil {
		details = details.Append("Config:", "text-muted").NewLine()
		yamlBytes, err := yaml.Marshal(s.Config)
		if err == nil {
			details = details.Add(clicky.CodeBlock("yaml", string(yamlBytes)))
		} else {
			details = details.Append(fmt.Sprintf("%v", s.Config))
		}
		details = details.NewLine()
	}

	t = t.Add(clicky.Collapsed(header, details))
	return t
}

func stripANSI(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

// colorLogLine highlights the log level prefix (INF, ERR, WRN, DBG, TRC) with appropriate colors.
func colorLogLine(line string) api.Text {
	t := clicky.Text("")
	for _, prefix := range []struct {
		tag   string
		style string
	}{
		{"ERR", "text-red-500"},
		{"INF", "text-green-500"},
		{"WRN", "text-yellow-500"},
		{"DBG", "text-blue-500"},
		{"TRC", "text-gray-500"},
	} {
		if idx := strings.Index(line, prefix.tag); idx >= 0 {
			end := idx + len(prefix.tag)
			t = t.Append(line[:idx])
			t = t.Append(prefix.tag, prefix.style)
			t = t.Append(line[end:])
			return t
		}
	}
	t = t.Append(line)
	return t
}

func externalEntitiesSection(r FullScrapeResults) api.Text {
	t := clicky.Text("")

	hasExternal := len(r.ExternalUsers) > 0 || len(r.ExternalGroups) > 0 ||
		len(r.ExternalRoles) > 0 || len(r.ExternalUserGroups) > 0 ||
		len(r.ConfigAccess) > 0 || len(r.ConfigAccessLogs) > 0

	if !hasExternal {
		return t
	}

	t = t.Append("External Entities", "font-bold text-lg").NewLine()

	if len(r.ExternalUsers) > 0 {
		t = t.Append(fmt.Sprintf("  Users: %d", len(r.ExternalUsers))).NewLine()
		for _, u := range r.ExternalUsers {
			t = t.Append(fmt.Sprintf("    - %s (%s)", u.Name, u.Tenant), "text-muted").NewLine()
		}
	}

	if len(r.ExternalGroups) > 0 {
		t = t.Append(fmt.Sprintf("  Groups: %d", len(r.ExternalGroups))).NewLine()
		for _, g := range r.ExternalGroups {
			t = t.Append(fmt.Sprintf("    - %s (%s)", g.Name, g.Tenant), "text-muted").NewLine()
		}
	}

	if len(r.ExternalRoles) > 0 {
		t = t.Append(fmt.Sprintf("  Roles: %d", len(r.ExternalRoles))).NewLine()
		for _, role := range r.ExternalRoles {
			t = t.Append(fmt.Sprintf("    - %s (%s)", role.Name, role.Tenant), "text-muted").NewLine()
		}
	}

	if len(r.ExternalUserGroups) > 0 {
		t = t.Append(fmt.Sprintf("  User-Group Mappings: %d", len(r.ExternalUserGroups))).NewLine()
	}

	if len(r.ConfigAccess) > 0 {
		t = t.Append(fmt.Sprintf("  Config Access: %d", len(r.ConfigAccess))).NewLine()
	}

	if len(r.ConfigAccessLogs) > 0 {
		t = t.Append(fmt.Sprintf("  Access Logs: %d", len(r.ConfigAccessLogs))).NewLine()
	}

	t = t.NewLine()
	return t
}
