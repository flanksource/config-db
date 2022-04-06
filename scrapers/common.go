package scrapers

import (
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/scrapers/aws"
	"github.com/flanksource/confighub/scrapers/file"
)

// All is the scrappers registry
var All = []v1.Scraper{
	aws.AWSScraper{},
	file.JSONScrapper{},
}
