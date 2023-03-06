package scrapers

import (
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/robfig/cron/v3"
)

var (
	cronManger        *cron.Cron
	DefaultSchedule   string
	cronIDFunctionMap map[string]cron.EntryID
)

func AddFuncToCron(schedule string, fn func()) error {
	_, err := cronManger.AddFunc(schedule, fn)
	return err
}

func AddToCron(scraper v1.ConfigScraper, id string) {
	fn := func() {
		if err := RunScraper(scraper); err != nil {
			logger.Errorf("Error running scraper: %v", err)
		}
	}
	schedule := scraper.Schedule
	if schedule == "" {
		schedule = DefaultSchedule
	}

	// Remove existing cronjob
	RemoveFromCron(id)

	// Schedule a new job
	entryID, err := cronManger.AddFunc(schedule, fn)
	if err != nil {
		logger.Errorf("Failed to schedule cron using :%v", err)
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
