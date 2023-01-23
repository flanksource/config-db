package scrapers

import (
	"context"
	"fmt"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/utils/kube"
	"github.com/robfig/cron/v3"
)

var cronManger *cron.Cron
var DefaultSchedule string
var cronIDFunctionMap map[string]cron.EntryID

func RunScraper(scraper v1.ConfigScraper) error {
	kommonsClient, err := kube.NewKommonsClient()
	if err != nil {
		return fmt.Errorf("failed to get kubernetes client: %v", err)
	}

	ctx := &v1.ScrapeContext{Context: context.Background(), Kommons: kommonsClient, Scraper: &scraper}
	var results []v1.ScrapeResult
	if results, err = Run(ctx, scraper); err != nil {
		return fmt.Errorf("Failed to run scraper %s: %v", scraper, err)
	}
	if err = db.SaveResults(ctx, results); err != nil {
		//FIXME cache results to save to db later
		return fmt.Errorf("Failed to update db: %v", err)
	}
	return nil
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

	// If id already exists, remove the old cron job
	if entryID, exists := cronIDFunctionMap[id]; exists {
		cronManger.Remove(entryID)
	}
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

func init() {
	cronIDFunctionMap = make(map[string]cron.EntryID)
	cronManger = cron.New()
	cronManger.Start()
}
