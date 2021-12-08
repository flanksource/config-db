package scrapers

import (
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/scrapers/aws"
)

var All = []v1.Scraper{
	aws.AWSScraper{},
}
