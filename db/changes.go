package db

import (
	"fmt"
	"time"

	"github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/db/models"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
)

var changeCacheByFingerprint = cache.New(time.Hour, time.Hour)

func changeFingeprintCacheKey(configID, fingerprint string) string {
	return fmt.Sprintf("%s:%s", configID, fingerprint)
}

func InitChangeFingerprintCache(ctx api.ScrapeContext, window time.Duration) error {
	var changes []*models.ConfigChange
	if err := ctx.DB().Where("fingerprint IS NOT NULL").Where("NOW() - created_at <= ?", window).Find(&changes).Error; err != nil {
		return err
	}

	ctx.Logger.Debugf("initializing changes cache with %d changes", len(changes))

	for _, c := range changes {
		key := changeFingeprintCacheKey(c.ConfigID, *c.Fingerprint)
		changeCacheByFingerprint.Set(key, c, time.Until(c.CreatedAt.Add(window)))
	}

	return nil
}

func dedupChanges(window time.Duration, changes []*models.ConfigChange) ([]*models.ConfigChange, []*models.ConfigChange) {
	if len(changes) == 0 {
		return nil, nil
	}

	var nonDuped []*models.ConfigChange
	var fingerprinted = map[string]*models.ConfigChange{}
	for _, change := range changes {
		if change.Fingerprint == nil {
			nonDuped = append(nonDuped, change)
			continue
		}

		key := changeFingeprintCacheKey(change.ConfigID, *change.Fingerprint)
		if v, ok := changeCacheByFingerprint.Get(key); !ok {
			changeCacheByFingerprint.Set(key, change, window)
			fingerprinted[change.ID] = change
		} else {
			existingChange := v.(*models.ConfigChange)

			change.ID = existingChange.ID
			change.Count += existingChange.Count
			change.FirstObserved = lo.CoalesceOrEmpty(existingChange.FirstObserved, &existingChange.CreatedAt)
			changeCacheByFingerprint.Set(key, change, window)

			fingerprinted[change.ID] = change
		}
	}

	var deduped = nonDuped
	for _, v := range fingerprinted {
		if v.Count == 1 {
			nonDuped = append(nonDuped, v)
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
