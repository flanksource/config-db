package api

import (
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/upstream"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var (
	KubernetesClient     kubernetes.Interface
	KubernetesRestConfig *rest.Config
	Namespace            string
	DefaultContext       ScrapeContext

	UpstreamConfig upstream.UpstreamConfig
)

type Scraper interface {
	Scrape(ctx ScrapeContext) v1.ScrapeResults
	CanScrape(config v1.ScraperSpec) bool
}
