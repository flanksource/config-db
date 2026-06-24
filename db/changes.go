package db

import (
	"time"

	"github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/db/models"
	"github.com/flanksource/duty/context"
	dutyModels "github.com/flanksource/duty/models"
)

func InitChangeFingerprintCache(ctx context.Context, window time.Duration) error {
	return dutyModels.InitChangeFingerprintCache(ctx.DB(), window)
}

// configChangeUpdate keeps the rest of config-db working with its local
// ConfigChange model while duty owns the dedupe decision/result shape.
type configChangeUpdate struct {
	Change         *models.ConfigChange
	CountIncrement int
	FirstInBatch   bool
}

// dedupChanges is a temporary compatibility shim around duty's ConfigChange
// deduper.
//
// duty now owns fingerprint cache initialization and dedupe behavior, but
// config-db still uses its local db/models.ConfigChange in the scrape pipeline.
// Until that model is fully replaced with duty/models.ConfigChange, this helper
// projects the fields required by duty's deduper (ID, ConfigID, Fingerprint),
// calls dutyModels.DedupConfigChanges, then maps the dedupe decisions back onto
// the original config-db change objects.
func dedupChanges(window time.Duration, changes []*models.ConfigChange) ([]*models.ConfigChange, []configChangeUpdate) {
	dutyChanges := make([]*dutyModels.ConfigChange, 0, len(changes))
	originals := make(map[*dutyModels.ConfigChange]*models.ConfigChange, len(changes))

	for _, change := range changes {
		dutyChange := &dutyModels.ConfigChange{
			ID:          change.ID,
			ConfigID:    change.ConfigID,
			Fingerprint: change.Fingerprint,
		}
		dutyChanges = append(dutyChanges, dutyChange)
		originals[dutyChange] = change
	}

	nonDupedDuty, dedupedDuty := dutyModels.DedupConfigChanges(window, dutyChanges)

	nonDuped := make([]*models.ConfigChange, 0, len(nonDupedDuty))
	for _, dutyChange := range nonDupedDuty {
		change := originals[dutyChange]
		change.ID = dutyChange.ID
		nonDuped = append(nonDuped, change)
	}

	deduped := make([]configChangeUpdate, 0, len(dedupedDuty))
	for _, dutyUpdate := range dedupedDuty {
		change := originals[dutyUpdate.Change]
		change.ID = dutyUpdate.Change.ID
		deduped = append(deduped, configChangeUpdate{
			Change:         change,
			CountIncrement: dutyUpdate.CountIncrement,
			FirstInBatch:   dutyUpdate.FirstInBatch,
		})
	}

	return nonDuped, deduped
}

func GetWorkflowRunCount(ctx api.ScrapeContext, workflowID string) (int64, error) {
	var count int64
	err := ctx.DB().Table("config_changes").
		Where("config_id = (?)", ctx.DB().Table("config_items").Select("id").Where("? = ANY(external_id)", workflowID)).
		Count(&count).
		Error
	return count, err
}
