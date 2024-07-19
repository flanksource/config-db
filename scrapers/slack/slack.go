package slack

import (
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

type Scraper struct{}

func (s Scraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.Slack) > 0
}

func (s Scraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults
	return results
}
