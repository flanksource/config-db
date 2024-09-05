package db

import (
	"fmt"
	"time"

	"github.com/flanksource/duty/context"
	"github.com/patrickmn/go-cache"

	"github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/db/models"
)

var changeCacheByFingerprint = cache.New(time.Hour, time.Hour)

func changeFingeprintCacheKey(configID, fingerprint string) string {
	return fmt.Sprintf("%s:%s", configID, fingerprint)
}

func InitChangeFingerprintCache(ctx context.Context, window time.Duration) error {
	var changes []*models.ConfigChange
	if err := ctx.DB().Where("fingerprint IS NOT NULL").Where("NOW() - created_at <= ?", window).Find(&changes).Error; err != nil {
		return err
	}

	ctx.Logger.Debugf("initializing changes cache with %d changes", len(changes))

	for _, c := range changes {
		key := changeFingeprintCacheKey(c.ConfigID, *c.Fingerprint)
		changeCacheByFingerprint.Set(key, c.ID, time.Until(c.CreatedAt.Add(window)))
	}

	return nil
}

func dedupChanges(window time.Duration, changes []*models.ConfigChange) ([]*models.ConfigChange, []models.ConfigChangeUpdate) {
	if len(changes) == 0 {
		return nil, nil
	}

	var nonDuped []*models.ConfigChange
	var fingerprinted = map[string]models.ConfigChangeUpdate{}

	for _, change := range changes {
		if change.Fingerprint == nil {
			nonDuped = append(nonDuped, change)
			continue
		}

		key := changeFingeprintCacheKey(change.ConfigID, *change.Fingerprint)
		if existingChangeID, ok := changeCacheByFingerprint.Get(key); !ok {
			changeCacheByFingerprint.Set(key, change.ID, window)
			fingerprinted[change.ID] = models.ConfigChangeUpdate{Change: change, CountIncrement: 0}
		} else {
			change.ID = existingChangeID.(string)
			changeCacheByFingerprint.Set(key, change.ID, window) // Refresh the cache expiry

			if existing, ok := fingerprinted[change.ID]; ok {
				fingerprinted[change.ID] = models.ConfigChangeUpdate{Change: change, CountIncrement: existing.CountIncrement + 1}
			} else {
				fingerprinted[change.ID] = models.ConfigChangeUpdate{Change: change, CountIncrement: 1}
			}
		}
	}

	var deduped []models.ConfigChangeUpdate
	for _, v := range fingerprinted {
		if v.CountIncrement == 0 {
			nonDuped = append(nonDuped, v.Change)
		} else {
			deduped = append(deduped, v)
		}
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
