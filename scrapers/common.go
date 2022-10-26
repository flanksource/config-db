package scrapers

import (
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/scrapers/aws"
	"github.com/flanksource/config-db/scrapers/file"
	"github.com/flanksource/config-db/scrapers/kubernetes"
)

// All is the scrappers registry
var All = []v1.Scraper{
	aws.Scraper{},
	aws.CostScraper{},
	file.FileScrapper{},
	kubernetes.KubernetesScrapper{},
	kubernetes.KubernetesFileScrapper{},
}
