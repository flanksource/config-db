package kubernetes

import (
	"fmt"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type KubernetesContext struct {
	api.ScrapeContext
	config v1.Kubernetes
	// Labels that are added to the kubernetes nodes once all the objects are visited
	labelsPerNode map[string]map[string]string

	// labelsForAllNode are common labels applicable to all the nodes in the cluster
	labelsForAllNode map[string]string

	// globalLabels are common labels for any kubernetes resource
	globalLabels                               map[string]string
	logSkipped, logExclusions, logNoResourceId bool
	cluster                                    v1.ScrapeResult
	resourceIDMap                              *ResourceIDMapContainer
	exclusionByType                            map[string]string
	exclusionBySeverity                        map[string]string
}

func newKubernetesContext(ctx api.ScrapeContext, isIncremental bool, config v1.Kubernetes) *KubernetesContext {
	if isIncremental {
		ctx = ctx.AsIncrementalScrape()
	}

	return &KubernetesContext{
		ScrapeContext: ctx,
		config:        config,
		cluster: v1.ScrapeResult{
			BaseScraper: config.BaseScraper,
			Name:        config.ClusterName,
			ConfigClass: "Cluster",
			Type:        ConfigTypePrefix + "Cluster",
			Config:      make(map[string]any),
			Labels:      make(v1.JSONStringMap),
			ID:          "Kubernetes/Cluster/" + config.ClusterName,
			Tags:        v1.Tags{{Name: "cluster", Value: config.ClusterName}},
		},
		labelsPerNode:    make(map[string]map[string]string),
		labelsForAllNode: make(map[string]string),
		globalLabels:     make(map[string]string),

		logExclusions:   ctx.PropertyOn(false, "log.exclusions"),
		logSkipped:      ctx.PropertyOn(false, "log.skipped"),
		logNoResourceId: ctx.PropertyOn(true, "log.noResourceId"),

		exclusionByType:     make(map[string]string),
		exclusionBySeverity: make(map[string]string),
	}
}

func (ctx *KubernetesContext) Load(objs []*unstructured.Unstructured) {
	ctx.resourceIDMap = NewResourceIDMap(objs)

	ctx.resourceIDMap.Set("", "Cluster", ctx.cluster.Name, ctx.cluster.ID)
	ctx.resourceIDMap.Set("", "Cluster", "selfRef", ctx.cluster.ID) // For shorthand

	for _, obj := range objs {
		ctx.exclusionByType[string(obj.GetUID())] = obj.GetAnnotations()[v1.AnnotationIgnoreChangeByType]
		ctx.exclusionBySeverity[string(obj.GetUID())] = obj.GetAnnotations()[v1.AnnotationIgnoreChangeBySeverity]
	}
}

func (ctx *KubernetesContext) GetID(namespace, kind, name string) string {
	id := ctx.resourceIDMap.Get(namespace, kind, name)
	if id == "" && !ctx.logNoResourceId {
		ctx.ScrapeContext.Logger.Warnf("No ID found for %s %s/%s", namespace, kind, name)
	}
	return id
}

func (ctx *KubernetesContext) FindInvolvedConfigID(event v1.KubernetesEvent) (uuid.UUID, error) {
	if id, err := uuid.Parse(string(event.InvolvedObject.UID)); err == nil {
		return id, nil
	}
	ids, err := db.FindConfigIDsByNamespaceNameClass(ctx.DutyContext(), ctx.cluster.Name, event.InvolvedObject.Namespace, event.InvolvedObject.Name, event.InvolvedObject.Kind)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to get config IDs for object %s/%s/%s", event.InvolvedObject.Namespace, event.InvolvedObject.Name, event.InvolvedObject.Kind)
	} else if len(ids) == 0 {
		if ctx.logSkipped {
			ctx.Tracef("skipping event (reason=%s, message=%s) because the involved object ID is not a valid UUID: %s", event.Reason, event.Message, event.InvolvedObject.UID)
		}
		return uuid.Nil, nil
	}
	return ids[0], nil
}

func (ctx *KubernetesContext) ClusterName() string {
	return ctx.cluster.Name
}
