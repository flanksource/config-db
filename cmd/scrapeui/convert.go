package scrapeui

import (
	"strings"

	v1 "github.com/flanksource/config-db/api/v1"
)

func MergeResults(results []v1.ScrapeResult) v1.FullScrapeResults {
	return v1.MergeScrapeResults(v1.ScrapeResults(results))
}

func ConvertSaveSummary(s *v1.ScrapeSummary) *SaveSummary {
	if s == nil {
		return nil
	}
	ss := &SaveSummary{
		ConfigTypes: make(map[string]TypeSummary, len(s.ConfigTypes)),
	}
	for k, v := range s.ConfigTypes {
		ss.ConfigTypes[k] = TypeSummary{
			Added:     v.Added,
			Updated:   v.Updated,
			Unchanged: v.Unchanged,
			Changes:   v.Changes,
		}
	}
	return ss
}

func BuildCounts(results v1.FullScrapeResults) Counts {
	c := Counts{
		Configs:        len(results.Configs),
		Changes:        len(results.Changes),
		Analysis:       len(results.Analysis),
		Relationships:  len(results.Relationships),
		ExternalUsers:  len(results.ExternalUsers),
		ExternalGroups: len(results.ExternalGroups),
		ExternalRoles:  len(results.ExternalRoles),
		ConfigAccess:   len(results.ConfigAccess),
		AccessLogs:     len(results.ConfigAccessLogs),
	}
	for _, r := range results.Configs {
		if r.Error != nil {
			c.Errors++
		}
	}
	return c
}

func ScraperName(name string) string {
	if name == "" {
		return "unnamed"
	}
	parts := strings.Split(name, "/")
	return parts[len(parts)-1]
}
