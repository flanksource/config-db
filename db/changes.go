package db

import (
	"sync"
	"time"

	sw "github.com/RussellLuo/slidingwindow"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/db/models"
	"github.com/flanksource/config-db/pkg/ratelimit"
)

const (
	rateLimitWindow    = time.Hour * 4
	maxChangesInWindow = 100

	ChangeTypeTooManyChanges = "TooManyChanges"
)

var configChangesCache = cache.New(time.Hour*24, time.Hour*24)

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

	// List of configs that have been rate limited.
	// It's used to avoid inserting mutliple "TooManyChanges" changes for the same config.
	rateLimitedConfigsPerScraper = sync.Map{}
)

func rateLimitChanges(ctx api.ScrapeContext, newChanges []*models.ConfigChange) ([]*models.ConfigChange, []string, error) {
	if len(newChanges) == 0 || ctx.ScrapeConfig().GetPersistedID() == nil {
		return newChanges, nil, nil
	}

	window := ctx.Properties().Duration("changes.max.window", rateLimitWindow)
	max := ctx.Properties().Int("changes.max.count", maxChangesInWindow)
	scraperID := ctx.ScrapeConfig().GetPersistedID().String()

	lock, loaded := scraperLocks.LoadOrStore(scraperID, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()

	_rateLimitedConfigs, _ := rateLimitedConfigsPerScraper.LoadOrStore(scraperID, map[string]struct{}{})
	rateLimitedConfigs := _rateLimitedConfigs.(map[string]struct{})

	if !loaded {
		// This is the initial sync of the rate limiter with the database.
		// Happens only once for each scrape config.
		if err := syncWindow(ctx, max, window); err != nil {
			return nil, nil, err
		}

		if rlConfigs, err := syncCurrentlyRateLimitedConfigs(ctx, window); err != nil {
			return nil, nil, err
		} else {
			rateLimitedConfigs = rlConfigs
		}
	}

	rateLimitedThisRun := map[string]struct{}{}
	passingNewChanges := make([]*models.ConfigChange, 0, len(newChanges))
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
			ctx.Logger.V(1).Infof("change rate limited (config=%s, external_id=%s, type=%s)", change.ConfigID, change.ExternalChangeId, change.ChangeType)
			rateLimitedThisRun[change.ConfigID] = struct{}{}
			continue
		} else {
			delete(rateLimitedConfigs, change.ConfigID)
		}

		passingNewChanges = append(passingNewChanges, change)
	}

	var newlyRateLimited []string
	for configID := range rateLimitedThisRun {
		if _, ok := rateLimitedConfigs[configID]; !ok {
			newlyRateLimited = append(newlyRateLimited, configID)
		}
	}

	rateLimitedConfigs = collections.MergeMap(rateLimitedConfigs, rateLimitedThisRun)
	rateLimitedConfigsPerScraper.Store(scraperID, rateLimitedConfigs)

	return passingNewChanges, newlyRateLimited, nil
}

func syncCurrentlyRateLimitedConfigs(ctx api.ScrapeContext, window time.Duration) (map[string]struct{}, error) {
	query := `WITH latest_changes AS (
		SELECT
			DISTINCT ON (config_id) config_id,
			change_type
		FROM
			config_changes
		LEFT JOIN config_items ON config_items.id = config_changes.config_id
			WHERE
				scraper_id = ?
				AND NOW() - config_changes.created_at <= ?
		ORDER BY
			config_id,
			config_changes.created_at DESC
		) SELECT config_id FROM latest_changes WHERE change_type = ?`
	rows, err := ctx.DB().Raw(query, ctx.ScrapeConfig().GetPersistedID(), window, ChangeTypeTooManyChanges).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	output := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}

		ctx.Logger.V(1).Infof("config %s is currently found to be rate limited", id)
		output[id] = struct{}{}
	}

	return output, rows.Err()
}

// syncWindow syncs the rate limit window for the scraper with the changes in the db.
func syncWindow(ctx api.ScrapeContext, max int, window time.Duration) error {
	query := `SELECT
			config_id,
			COUNT(*),
			MIN(config_changes.created_at) AS min_created_at
		FROM
			config_changes
		LEFT JOIN config_items ON config_items.id = config_changes.config_id
		WHERE
			scraper_id = ?
			AND change_type != ?
			AND NOW() - config_changes.created_at <= ?
		GROUP BY
			config_id`
	rows, err := ctx.DB().Raw(query, ctx.ScrapeConfig().GetPersistedID(), ChangeTypeTooManyChanges, window).Rows()
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var configID string
		var count int
		var earliest time.Time
		if err := rows.Scan(&configID, &count, &earliest); err != nil {
			return err
		}
		ctx.Logger.V(3).Infof("config %s has %d changes in the last %s", configID, count, window)

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

	return rows.Err()
}

// filterOutPersistedChanges returns only those changes that weren't seen in the db.
func filterOutPersistedChanges(ctx api.ScrapeContext, changes []*models.ConfigChange) ([]*models.ConfigChange, error) {
	// use cache to filter out ones that we've already seen before
	changes = lo.Filter(changes, func(c *models.ConfigChange, _ int) bool {
		_, found := configChangesCache.Get(c.ConfigID + c.ExternalChangeId)
		if found {
			_ = found
		}
		return !found
	})

	if len(changes) == 0 {
		return nil, nil
	}

	query := `SELECT config_id, external_change_id
  FROM config_changes
  WHERE (config_id, external_change_id) IN ?`
	args := lo.Map(changes, func(c *models.ConfigChange, _ int) []string {
		return []string{c.ConfigID, c.ExternalChangeId}
	})

	rows, err := ctx.DB().Raw(query, args).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	existing := make(map[string]struct{})
	for rows.Next() {
		var configID, externalChangeID string
		if err := rows.Scan(&configID, &externalChangeID); err != nil {
			return nil, err
		}

		configChangesCache.SetDefault(configID+externalChangeID, struct{}{})
		existing[configID+externalChangeID] = struct{}{}
	}

	newOnes := lo.Filter(changes, func(c *models.ConfigChange, _ int) bool {
		_, found := existing[c.ConfigID+c.ExternalChangeId]
		return !found
	})

	if len(newOnes) > 0 {
		_ = query
	}

	return newOnes, nil
}
