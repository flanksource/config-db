package api

import (
	"context"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var KubernetesClient *kubernetes.Clientset
var KubernetesRestConfig *rest.Config
var Namespace string

func NewScrapeContext(ctx context.Context, scraper v1.ScrapeConfig) *v1.ScrapeContext {
	return &v1.ScrapeContext{
		Context:              ctx,
		ScrapeConfig:         scraper,
		Namespace:            Namespace,
		Kubernetes:           KubernetesClient,
		KubernetesRestConfig: KubernetesRestConfig,
		DB:                   db.DefaultDB(),
	}
}
