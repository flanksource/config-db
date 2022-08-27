package scrapers

import (
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/scrapers/aws"
	"github.com/flanksource/confighub/scrapers/file"
	"github.com/flanksource/confighub/scrapers/ical"
)

// All is the scrappers registry
var All = []v1.Scraper{
	aws.Scraper{},
	file.FileScrapper{},
	ical.ICalScrapper{},
}
