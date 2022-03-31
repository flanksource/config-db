package scrapers

import (
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/scrapers/aws"
	"github.com/flanksource/confighub/scrapers/file"
)

var All = []v1.Scraper{
	aws.AWSScraper{},
	file.FScrapper{},
}
