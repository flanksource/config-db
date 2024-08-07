package changes

import (
	"github.com/flanksource/config-db/db/models"
	"github.com/samber/lo"
)

// DedupGroup represents a set of config changes with the same fingprint.
type DedupGroup struct {
	// Changes in order of creation date.
	//
	// May contain existing changes (from db) and newly extracted changes.
	Changes []*models.ConfigChange
}

// Dedup will return all the older changes that need to be removed from db
// and merge the first and last changes.
func (t *DedupGroup) Dedup() (*models.ConfigChange, []string) {
	if len(t.Changes) == 0 {
		return nil, nil
	} else if len(t.Changes) == 1 {
		return t.Changes[0], nil
	}

	oldestChange := t.Changes[0]
	latestChange := t.Changes[len(t.Changes)-1]

	var toDelete []string
	var count int
	for i, c := range t.Changes {
		count += lo.CoalesceOrEmpty(c.Count, 1)

		if i != len(t.Changes) && c.ID != "" {
			toDelete = append(toDelete, c.ID)
		}
	}

	latestChange.Count = count

	if oldestChange.FirstObserved != nil {
		latestChange.FirstObserved = oldestChange.FirstObserved
	} else {
		latestChange.FirstObserved = &oldestChange.CreatedAt
	}

	return latestChange, toDelete
}

func GroupChanges(changes []*models.ConfigChange) ([]*models.ConfigChange, []DedupGroup) {
	var fingerprintLess []*models.ConfigChange

	groups := map[string][]*models.ConfigChange{}
	for _, c := range changes {
		if c.Fingerprint == nil {
			fingerprintLess = append(fingerprintLess, c)
			continue
		}

		groups[*c.Fingerprint] = append(groups[*c.Fingerprint], c)
	}

	var dedupGroups []DedupGroup
	for _, changes := range groups {
		dedupGroups = append(dedupGroups, DedupGroup{
			Changes: changes,
		})
	}

	return fingerprintLess, dedupGroups
}
