package scrapers

import (
	"sync"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/robfig/cron/v3"
)

var (
	cronManger        *cron.Cron
	DefaultSchedule   string
	cronIDFunctionMap map[string]cron.EntryID

	// concurrentJobLocks keeps track of the currently running jobs.
	concurrentJobLocks sync.Map
)

// AtomicRunner wraps the given function, identified by a unique ID,
// with a mutex so that the function executes only once at a time, preventing concurrent executions.
func AtomicRunner(id string, fn func()) func() {
	return func() {
		val, _ := concurrentJobLocks.LoadOrStore(id, &sync.Mutex{})
		lock, ok := val.(*sync.Mutex)
		if !ok {
			logger.Warnf("expected mutex but got %T for scraper(id=%s)", lock, id)
			return
		}

		if !lock.TryLock() {
			logger.Debugf("scraper (id=%s) is already running. skipping this run ...", id)
			return
		}
		defer lock.Unlock()

		fn()
	}
}

func AddToCron(scrapeConfig v1.ScrapeConfig) {
	fn := func() {
		ctx := api.DefaultContext.WithScrapeConfig(&scrapeConfig)
		if _, err := RunScraper(ctx); err != nil {
			logger.Errorf("failed to run scraper %s: %v", ctx.ScrapeConfig().GetUID(), err)
		}
	}

	AddFuncToCron(string(scrapeConfig.GetUID()), scrapeConfig.Spec.Schedule, fn)
}

func AddFuncToCron(id, schedule string, fn func()) {
	if schedule == "" {
		schedule = DefaultSchedule
	}

	// Remove existing cronjob
	RemoveFromCron(id)

	// Schedule a new job
	entryID, err := cronManger.AddFunc(schedule, AtomicRunner(id, fn))
	if err != nil {
		logger.Errorf("Failed to schedule cron using: %v", err)
		return
	}

	// Add non empty ids to the map
	if id != "" {
		cronIDFunctionMap[id] = entryID
	}
}

func RemoveFromCron(id string) {
	if entryID, exists := cronIDFunctionMap[id]; exists {
		cronManger.Remove(entryID)
	}
}

func init() {
	cronIDFunctionMap = make(map[string]cron.EntryID)
	cronManger = cron.New()
	cronManger.Start()
}
