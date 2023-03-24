package scrapers

import (
	"context"
	"fmt"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/utils/kube"
	"github.com/google/uuid"
)

func RunScraper(scraper v1.ConfigScraper) error {
	kommonsClient, err := kube.NewKommonsClient()
	if err != nil {
		return fmt.Errorf("failed to get kubernetes client: %v", err)
	}

	if err != nil {
		return fmt.Errorf("failed to generate id: %v", err)
	}
	id, err := uuid.Parse(scraper.ID)
	if err != nil {
		return fmt.Errorf("failed to parse uuid[%s]: %v", scraper.ID, err)
	}
	ctx := &v1.ScrapeContext{Context: context.Background(), Kommons: kommonsClient, Scraper: &scraper, ScraperID: &id}
	var results []v1.ScrapeResult
	var scraperErr, dbErr error
	if results, scraperErr = Run(ctx, scraper); scraperErr != nil {
		return fmt.Errorf("failed to run scraper %v: %v", scraper, scraperErr)
	}

	if dbErr = db.SaveResults(ctx, results); dbErr != nil {
		//FIXME cache results to save to db later
		return fmt.Errorf("failed to update db: %v", dbErr)
	}

	// If error in any of the scrape results, don't delete old items
	var errInResults = false
	for _, r := range results {
		if r.Error != nil {
			errInResults = true
			break
		}
	}
	if scraperErr == nil && dbErr == nil && len(results) > 0 && !errInResults {
		if err = DeleteStaleConfigItems(id); err != nil {
			return fmt.Errorf("error deleting stale config items: %v", err)
		}
	}

	return nil
}
