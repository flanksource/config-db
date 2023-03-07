package scrapers

import (
	"context"
	"fmt"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/utils/kube"
)

func RunScraper(scraper v1.ConfigScraper) error {
	kommonsClient, err := kube.NewKommonsClient()
	if err != nil {
		return fmt.Errorf("failed to get kubernetes client: %v", err)
	}

	if err != nil {
		return fmt.Errorf("failed to generate id: %v", err)
	}
	id := scraper.ID
	ctx := &v1.ScrapeContext{Context: context.Background(), Kommons: kommonsClient, Scraper: &scraper, ScraperID: id}
	var results []v1.ScrapeResult
	if results, err = Run(ctx, scraper); err != nil {
		return fmt.Errorf("failed to run scraper %v: %v", scraper, err)
	}

	if err = db.SaveResults(ctx, results); err != nil {
		//FIXME cache results to save to db later
		return fmt.Errorf("failed to update db: %v", err)
	}

	if err = DeleteStaleConfigItems(id); err != nil {
		return fmt.Errorf("error deleting stale config items: %v", err)
	}

	return nil
}
