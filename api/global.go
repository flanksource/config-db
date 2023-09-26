package api

import (
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/upstream"
	"github.com/google/uuid"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var (
	KubernetesClient     *kubernetes.Clientset
	KubernetesRestConfig *rest.Config
	Namespace            string
	DefaultContext       ScrapeContext

	// the derived agent id from the agentName
	AgentID        uuid.UUID
	UpstreamConfig upstream.UpstreamConfig
)

type Scraper interface {
	Scrape(ctx ScrapeContext) v1.ScrapeResults
	CanScrape(config v1.ScraperSpec) bool
}
