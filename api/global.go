package api

import (
	goctx "context"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/google/uuid"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var KubernetesClient *kubernetes.Clientset
var KubernetesRestConfig *rest.Config
var Namespace string

func NewScrapeContext(scraper v1.ScrapeConfig, id *uuid.UUID) *v1.ScrapeContext {
	return &v1.ScrapeContext{
		Context:              goctx.Background(),
		ScrapeConfig:         scraper,
		ScraperID:            id,
		Namespace:            Namespace,
		Kubernetes:           KubernetesClient,
		KubernetesRestConfig: KubernetesRestConfig,
		DB:                   db.DefaultDB(),
	}
}
