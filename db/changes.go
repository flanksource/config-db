package db

import (
	"sync"
	"time"

	sw "github.com/RussellLuo/slidingwindow"
	"github.com/google/uuid"

	"github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/db/models"
	"github.com/flanksource/config-db/pkg/ratelimit"
)

const (
	rateLimitWindow    = time.Hour * 4
	maxChangesInWindow = 100
)

func GetWorkflowRunCount(ctx api.ScrapeContext, workflowID string) (int64, error) {
	var count int64
	err := ctx.DB().Table("config_changes").
		Where("config_id = (?)", ctx.DB().Table("config_items").Select("id").Where("? = ANY(external_id)", workflowID)).
		Count(&count).
		Error
	return count, err
}

var (
	scraperLocks       = sync.Map{}
	configRateLimiters = map[string]*sw.Limiter{}
)

func rateLimitChanges(ctx api.ScrapeContext, newChanges []*models.ConfigChange) ([]*models.ConfigChange, error) {
	if len(newChanges) == 0 {
		return nil, nil
	}

	lock, loaded := scraperLocks.LoadOrStore(ctx.ScrapeConfig().GetPersistedID(), &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()

	window := ctx.Properties().Duration("changes.max.window", rateLimitWindow)
	max := ctx.Properties().Int("changes.max.count", maxChangesInWindow)

	if !loaded {
		// populate the rate limit window for the scraper
		query := `SELECT config_id, COUNT(*), min(created_at) FROM config_changes 
		WHERE change_type != 'TooManyChanges'
		AND NOW() - created_at <= ? GROUP BY config_id`
		rows, err := ctx.DB().Raw(query, window).Rows()
		if err != nil {
			return nil, err
		}

		for rows.Next() {
			var configID string
			var count int
			var earliest time.Time
			if err := rows.Scan(&configID, &count, &earliest); err != nil {
				return nil, err
			}

			rateLimiter, _ := sw.NewLimiter(window, int64(max), func() (sw.Window, sw.StopFunc) {
				win, stopper := ratelimit.NewLocalWindow()
				if count > 0 {
					win.SetStart(earliest)
					win.AddCount(int64(count))
				}
				return win, stopper
			})
			configRateLimiters[configID] = rateLimiter
		}
	}

	passingNewChanges := make([]*models.ConfigChange, 0, len(newChanges))
	rateLimited := map[string]struct{}{}
	for _, change := range newChanges {
		rateLimiter, ok := configRateLimiters[change.ConfigID]
		if !ok {
			rl, _ := sw.NewLimiter(window, int64(max), func() (sw.Window, sw.StopFunc) {
				return sw.NewLocalWindow()
			})
			configRateLimiters[change.ConfigID] = rl
			rateLimiter = rl
		}

		if !rateLimiter.Allow() {
			ctx.Logger.V(2).Infof("change rate limited (config=%s)", change.ConfigID)
			rateLimited[change.ConfigID] = struct{}{}
			continue
		}

		passingNewChanges = append(passingNewChanges, change)
	}

	// For all the rate limited configs, we add a new "TooManyChanges" change
	for configID := range rateLimited {
		passingNewChanges = append(passingNewChanges, &models.ConfigChange{
			ConfigID:         configID,
			Summary:          "Changes on this config has been rate limited",
			ChangeType:       "TooManyChanges",
			ExternalChangeId: uuid.New().String(),
		})
	}

	return passingNewChanges, nil
}
