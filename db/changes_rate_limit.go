package db

import (
	"sync"
	"time"

	"github.com/flanksource/commons/collections"
	sw "github.com/flanksource/slidingwindow"

	"github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/db/models"
)

const (
	rateLimitWindow    = time.Hour * 4
	maxChangesInWindow = 100

	ChangeTypeTooManyChanges = "TooManyChanges"
)

var (
	scraperLocks       = sync.Map{}
	configRateLimiters = map[string]*sw.Limiter{}

	// List of configs that are currently in being rate limited.
	// It's used to avoid inserting multiple "TooManyChanges" changes for the same config.
	currentlyRateLimitedConfigsPerScraper = sync.Map{}
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

	_rateLimitedConfigs, _ := currentlyRateLimitedConfigsPerScraper.LoadOrStore(scraperID, map[string]struct{}{})
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

	var rateLimitedConfigsThisRun = make(map[string]struct{})
	var passingNewChanges = make([]*models.ConfigChange, 0, len(newChanges))
	for _, change := range newChanges {
		if _, ok := rateLimitedConfigs[change.ConfigID]; ok {
			rateLimitedConfigsThisRun[change.ConfigID] = struct{}{}
		} else {
			passingNewChanges = append(passingNewChanges, change)
		}
	}

	// Find those changes that were rate limited only this run but
	// weren't previously in the rate limited state.
	var newlyRateLimited []string
	for configID := range rateLimitedConfigsThisRun {
		if _, ok := rateLimitedConfigs[configID]; !ok {
			newlyRateLimited = append(newlyRateLimited, configID)
		}
	}

	rateLimitedConfigs = collections.MergeMap(rateLimitedConfigs, rateLimitedConfigsThisRun)
	currentlyRateLimitedConfigsPerScraper.Store(scraperID, rateLimitedConfigs)

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

		ctx.Logger.V(3).Infof("config %s is currently found to be rate limited", id)
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
			win, stopper := sw.NewLocalWindow()
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
