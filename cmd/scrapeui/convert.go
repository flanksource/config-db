package scrapeui

import (
	"strings"

	v1 "github.com/flanksource/config-db/api/v1"
	duty "github.com/flanksource/duty"
	"github.com/flanksource/duty/models"
)

func MergeResults(results []v1.ScrapeResult) v1.FullScrapeResults {
	for i := range results {
		if results[i].Resolved != nil && results[i].Action == "" {
			results[i].Action = results[i].Resolved.Action
		}
	}
	return v1.MergeScrapeResults(v1.ScrapeResults(results))
}

// BuildUIRelationships creates frontend-friendly relationships from scrape results.
// It uses external IDs and resolved names (from Resolved.Relationships) so the
// frontend can match relationships to config items by external ID.
// It also resolves RelationshipSelectors in-memory against the scraped configs.
func BuildUIRelationships(results []v1.ScrapeResult) []UIRelationship {
	nameByExternalID := map[string]string{}
	for _, r := range results {
		if r.Name != "" {
			nameByExternalID[r.ID] = r.Name
		}
	}

	var out []UIRelationship
	for _, r := range results {
		if r.Resolved != nil {
			for _, ref := range r.Resolved.Relationships {
				rel := UIRelationship{
					ConfigExternalID:  externalIDOrFallback(ref.Query.ConfigExternalID.ExternalID, ref.Query.ConfigID, r.ID),
					RelatedExternalID: externalIDOrFallback(ref.Query.RelatedExternalID.ExternalID, ref.Query.RelatedConfigID, ""),
					Relation:          ref.Query.Relationship,
					ConfigName:        ref.ConfigName,
					RelatedName:       ref.RelatedName,
				}
				if rel.ConfigName == "" {
					rel.ConfigName = nameByExternalID[rel.ConfigExternalID]
				}
				if rel.RelatedName == "" {
					rel.RelatedName = nameByExternalID[rel.RelatedExternalID]
				}
				out = append(out, rel)
			}
			continue
		}

		// Use RelationshipResults directly (pre-DB-save)
		for _, rr := range r.RelationshipResults {
			rel := UIRelationship{
				ConfigExternalID:  externalIDOrFallback(rr.ConfigExternalID.ExternalID, rr.ConfigID, r.ID),
				RelatedExternalID: externalIDOrFallback(rr.RelatedExternalID.ExternalID, rr.RelatedConfigID, ""),
				Relation:          rr.Relationship,
			}
			rel.ConfigName = nameByExternalID[rel.ConfigExternalID]
			rel.RelatedName = nameByExternalID[rel.RelatedExternalID]
			out = append(out, rel)
		}

		// Resolve RelationshipSelectors in-memory against scraped configs
		for _, dr := range r.RelationshipSelectors {
			for _, match := range matchSelector(dr.Selector, results) {
				rel := UIRelationship{Relation: dr.Selector.Type}
				if dr.Parent {
					rel.ConfigExternalID = match.ID
					rel.ConfigName = match.Name
					rel.RelatedExternalID = r.ID
					rel.RelatedName = r.Name
				} else {
					rel.ConfigExternalID = r.ID
					rel.ConfigName = r.Name
					rel.RelatedExternalID = match.ID
					rel.RelatedName = match.Name
				}
				out = append(out, rel)
			}
		}
	}
	return out
}

// matchSelector finds configs matching a RelationshipSelector in-memory.
func matchSelector(sel duty.RelationshipSelector, configs []v1.ScrapeResult) []v1.ScrapeResult {
	var matches []v1.ScrapeResult
	for _, c := range configs {
		if sel.Type != "" && c.Type != sel.Type {
			continue
		}
		if sel.Name != "" && c.Name != sel.Name {
			continue
		}
		if sel.ExternalID != "" && c.ID != sel.ExternalID {
			continue
		}
		if sel.Namespace != "" {
			ns, _ := c.Tags["namespace"]
			if ns != sel.Namespace {
				continue
			}
		}
		if len(sel.Labels) > 0 {
			if !matchLabels(sel.Labels, c.Labels) {
				continue
			}
		}
		matches = append(matches, c)
	}
	return matches
}

func matchLabels(required, actual map[string]string) bool {
	for k, v := range required {
		if actual[k] != v {
			return false
		}
	}
	return true
}

func externalIDOrFallback(externalID, configID, fallback string) string {
	if externalID != "" {
		return externalID
	}
	if configID != "" {
		return configID
	}
	return fallback
}

// BuildUIRelationshipsFromDB converts DB-resolved ConfigRelationships back to
// UI relationships by mapping internal UUIDs to external IDs via the config list.
func BuildUIRelationshipsFromDB(rels []models.ConfigRelationship, configs []v1.ScrapeResult) []UIRelationship {
	if len(rels) == 0 {
		return nil
	}

	// Build UUID → external ID index.
	// After DB save, ScrapeResult.ConfigID is set to the internal UUID.
	type configRef struct {
		externalID string
		name       string
	}
	byUUID := map[string]configRef{}
	for _, c := range configs {
		if c.ConfigID != nil && *c.ConfigID != "" {
			byUUID[*c.ConfigID] = configRef{externalID: c.ID, name: c.Name}
		}
	}

	var out []UIRelationship
	for _, r := range rels {
		cfgRef := byUUID[r.ConfigID]
		relRef := byUUID[r.RelatedID]
		if cfgRef.externalID == "" && relRef.externalID == "" {
			continue // can't resolve either side
		}
		out = append(out, UIRelationship{
			ConfigExternalID:  cfgRef.externalID,
			RelatedExternalID: relRef.externalID,
			Relation:          r.Relation,
			ConfigName:        cfgRef.name,
			RelatedName:       relRef.name,
		})
	}
	return out
}

// BuildConfigMeta extracts resolved parent paths and locations from scrape results.
// It resolves parent external IDs to display names using the config list.
func BuildConfigMeta(results []v1.ScrapeResult) map[string]ConfigMeta {
	// Build name index for resolving parent references
	nameByExtID := map[string]string{}
	for _, r := range results {
		if r.Name != "" {
			nameByExtID[r.ID] = r.Name
		}
	}

	meta := map[string]ConfigMeta{}
	for _, r := range results {
		m := ConfigMeta{}
		if r.Resolved != nil {
			for _, p := range r.Resolved.Parents {
				name := p.Name
				if name == "" {
					name = nameByExtID[p.Query.ExternalID]
				}
				if name == "" {
					name = p.Query.ExternalID
				}
				if name != "" {
					m.Parents = append(m.Parents, name)
				}
			}
		} else {
			for _, p := range r.Parents {
				if p.ExternalID == "" {
					continue
				}
				name := nameByExtID[p.ExternalID]
				if name == "" {
					name = p.ExternalID
				}
				m.Parents = append(m.Parents, name)
			}
		}
		if len(r.Locations) > 0 {
			m.Location = strings.Join(r.Locations, ", ")
		}
		if len(m.Parents) > 0 || m.Location != "" {
			meta[r.ID] = m
		}
	}
	return meta
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

func BuildCounts(results v1.FullScrapeResults, uiRels []UIRelationship) Counts {
	c := Counts{
		Configs:        len(results.Configs),
		Changes:        len(results.Changes),
		Analysis:       len(results.Analysis),
		Relationships:  len(uiRels),
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
