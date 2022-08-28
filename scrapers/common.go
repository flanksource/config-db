package scrapers

import (
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/scrapers/aws"
	"github.com/flanksource/config-db/scrapers/file"
)

// All is the scrappers registry
var All = []v1.Scraper{
	aws.Scraper{},
	file.FileScrapper{},
}
