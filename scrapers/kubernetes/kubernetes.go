package kubernetes

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Jeffail/gabs/v2"
	"github.com/flanksource/commons/collections"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/samber/lo"
	coreV1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/is-healthy/pkg/health"
	"github.com/flanksource/is-healthy/pkg/lua"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const ConfigTypePrefix = "Kubernetes::"

var resourceIDMapPerCluster PerClusterResourceIDMap

func getConfigTypePrefix(apiVersion string) string {
	if strings.Contains(apiVersion, ".upbound.io") || strings.Contains(apiVersion, ".crossplane.io") {
		return "Crossplane::"
	}

	if strings.HasSuffix(apiVersion, ".flanksource.com/v1") {
		return api.MissionControlConfigTypePrefix
	}

	return ConfigTypePrefix
}

type KubernetesScraper struct{}

func (kubernetes KubernetesScraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.Kubernetes) > 0
}

func getString(obj *unstructured.Unstructured, path ...string) string {
	s, _, _ := unstructured.NestedString(obj.Object, path...)
	return s
}

func (kubernetes KubernetesScraper) IncrementalScrape(
	ctx api.ScrapeContext,
	config v1.Kubernetes,
	objects []*unstructured.Unstructured,
) v1.ScrapeResults {
	return ExtractResults(newKubernetesContext(ctx, true, config), objects)
}

func (kubernetes KubernetesScraper) IncrementalEventScrape(
	ctx api.ScrapeContext,
	config v1.Kubernetes,
	events []v1.KubernetesEvent,
) v1.ScrapeResults {
	if len(events) == 0 {
		return nil
	}
	ctx.DutyContext().Logger.V(4).Infof("incrementally scraping resources from %d events", len(events))

	var r v1.ScrapeResults
	var err error
	if config.Kubeconfig != nil {
		c, err := ctx.WithKubeconfig(*config.Kubeconfig)
		if err != nil {
			return r.Errorf(err, "failed to apply custom kube config")
		}
		ctx.Context = *c
	}

	var (
		kindClientCache   = map[string]dynamic.NamespaceableResourceInterface{}
		resourcesWatching = lo.Map(config.Watch, func(k v1.KubernetesResourceToWatch, _ int) string { return k.Kind })

		// seenObjects helps in avoiding fetching the same object in this run.
		seenObjects = make(map[string]struct{})
		objects     = make([]*unstructured.Unstructured, 0, len(events))
	)

	for _, event := range events {
		if eventObj, err := event.ToUnstructured(); err != nil {
			ctx.DutyContext().Errorf("failed to convert event to unstructured: %v", err)
			continue
		} else {
			objects = append(objects, eventObj)
		}

		// Add the involved object
		if lo.Contains(resourcesWatching, event.InvolvedObject.Kind) {
			// If we're already watching the resource then we don't need to fetch it again.
			continue
		}

		resource := event.InvolvedObject
		cacheKey := fmt.Sprintf("%s/%s/%s", resource.Namespace, resource.Kind, resource.Name)
		if _, ok := seenObjects[cacheKey]; !ok {
			kclient, ok := kindClientCache[resource.APIVersion+resource.Kind]
			if !ok {
				gv, _ := schema.ParseGroupVersion(resource.APIVersion)
				kclient, err = ctx.KubernetesDynamicClient().GetClientByGroupVersionKind(gv.Group, gv.Version, resource.Kind)
				if err != nil {
					ctx.Errorf("failed to get dynamic client for (%s/%s)", gv, resource.Kind)
					continue
				}

				kindClientCache[resource.APIVersion+resource.Kind] = kclient
			}

			ctx.DutyContext().
				Logger.V(5).
				Infof("fetching resource namespace=%s name=%s kind=%s apiVersion=%s", resource.Namespace, resource.Name, resource.Kind, resource.APIVersion)

			obj, err := kclient.Namespace(resource.Namespace).Get(ctx, resource.Name, metav1.GetOptions{})
			if err != nil {
				ctx.DutyContext().Logger.Warnf(
					"failed to get resource (Kind=%s, Name=%s, Namespace=%s): %v",
					resource.Kind,
					resource.Name,
					resource.Namespace,
					err,
				)
				continue
			} else if obj != nil {
				seenObjects[cacheKey] = struct{}{} // mark it as seen so we don't run ketall.KetOne again (in this run)
				objects = append(objects, obj)
			}
		}
	}

	ctx.DutyContext().Logger.V(4).Infof("found %d objects for %d events", len(objects)-len(events), len(events))
	if len(objects) == 0 {
		return nil
	}

	return ExtractResults(newKubernetesContext(ctx, true, config), objects)
}

func (kubernetes KubernetesScraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var (
		results v1.ScrapeResults
		err     error
	)

	for _, config := range ctx.ScrapeConfig().Spec.Kubernetes {
		if config.ClusterName == "" {
			return results.Errorf(err, "clusterName missing from kubernetes configuration")
		}

		objs, err := scrape(ctx, config)
		if err != nil {
			return results.Errorf(err, "error running ketall")
		}

		extracted := ExtractResults(newKubernetesContext(ctx, false, config), objs)
		results = append(results, extracted...)
	}

	return results
}

// ExtractResults extracts scrape results from the given list of kuberenetes objects.
//   - withCluster: if true, will create & add a scrape result for the kubernetes cluster.
func ExtractResults(ctx *KubernetesContext, objs []*unstructured.Unstructured) v1.ScrapeResults {
	var (
		results       v1.ScrapeResults
		changeResults v1.ScrapeResults
	)

	clusterName := ctx.config.ClusterName
	cluster := v1.ScrapeResult{
		BaseScraper: ctx.config.BaseScraper,
		Name:        clusterName,
		ConfigClass: "Cluster",
		Type:        ConfigTypePrefix + "Cluster",
		Config:      make(map[string]any),
		Labels:      make(v1.JSONStringMap),
		ID:          "Kubernetes/Cluster/" + clusterName,
		Tags:        v1.Tags{{Name: "cluster", Value: clusterName}},
	}

	results = append(results, cluster)

	ctx.Load(objs)
	if ctx.IsIncrementalScrape() {
		// On incremental scrape, we do not have all the data in the resource ID map.
		// we use it from the cached resource id map.
		ctx.resourceIDMap.data = resourceIDMapPerCluster.MergeAndUpdate(
			string(ctx.ScrapeConfig().GetUID()),
			ctx.resourceIDMap.data,
		)
	} else {
		resourceIDMapPerCluster.Swap(string(ctx.ScrapeConfig().GetUID()), ctx.resourceIDMap.data)
	}

	for _, obj := range objs {
		tags := ctx.config.Tags

		if ignore, err := ctx.IsIgnored(obj); err != nil {
			ctx.Warnf("failed to ignore obj[%s]: %v", obj.GetName(), err)
			continue
		} else if ignore {
			continue
		}

		if val, ok := obj.GetAnnotations()[v1.AnnotationCustomTags]; ok {
			for k, v := range collections.SelectorToMap(val) {
				tags = append(tags, v1.Tag{Name: k, Value: v})
			}
		}

		if obj.GetKind() == "Event" {
			var event v1.KubernetesEvent
			if err := event.FromObjMap(obj.Object); err != nil {
				ctx.Errorf("failed to parse event: %v", err)
				return nil
			}

			if event.InvolvedObject == nil {
				continue
			}

			if ctx.config.Event.Exclusions.Filter(event) {
				if ctx.logExclusions {
					ctx.Tracef("excluding event object %s/%s/%s: %s",
						event.InvolvedObject.Namespace, event.InvolvedObject.Name,
						event.InvolvedObject.Kind, event.Reason)
				}

				continue
			}

			uid, err := ctx.FindInvolvedConfigID(event)
			if err != nil {
				results.Errorf(err, "")
				continue
			}
			if uid == uuid.Nil {
				if ctx.logExclusions {
					ctx.Tracef("excluding event object %s/%s/%s: involved config[%s] not found",
						event.InvolvedObject.Namespace, event.InvolvedObject.Name,
						event.InvolvedObject.Kind, event.InvolvedObject.UID)
				}
				continue
			}

			event.InvolvedObject.UID = types.UID(uid.String())

			change := getChangeFromEvent(event, ctx.config.Event.SeverityKeywords)
			if change == nil {
				continue
			}
			if ignore, err := ctx.IgnoreChange(*change, event); err != nil {
				results.Errorf(err, "Failed to determine if change should be ignored: %v", err)
				continue
			} else if ignore {
				continue
			}

			changeResults = append(changeResults, v1.ScrapeResult{
				BaseScraper: ctx.config.BaseScraper,
				Changes:     []v1.ChangeResult{*change},
			})

			// this is all we need from an event object
			continue
		}

		var (
			relationships v1.RelationshipResults
			labels        = make(map[string]string)
		)

		if obj.GetLabels() != nil {
			labels = obj.GetLabels()
		}

		// save the annotations as labels to speed up ignore checks later on
		if v, ok := obj.GetAnnotations()[v1.AnnotationIgnoreChangeBySeverity]; ok {
			labels[v1.AnnotationIgnoreChangeBySeverity] = v
		}

		if v, ok := obj.GetAnnotations()[v1.AnnotationIgnoreChangeByType]; ok {
			labels[v1.AnnotationIgnoreChangeByType] = v
		}

		if skip, _labels, err := OnObjectHooks(ctx, obj); err != nil {
			results.Errorf(err, "")
		} else if skip {
			continue
		} else {
			for k, v := range _labels {
				labels[k] = v
			}
		}

		if obj.GetKind() == "Endpoints" {
			var endpoint coreV1.Endpoints
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &endpoint); err != nil {
				return results.Errorf(err, "failed to unmarshal endpoint (%s/%s)", obj.GetUID(), obj.GetName())
			}

			for _, subset := range endpoint.Subsets {
				for _, address := range subset.Addresses {
					if address.TargetRef != nil {
						if address.TargetRef.Kind != "Service" {
							relationships = append(relationships, v1.RelationshipResult{
								ConfigExternalID: v1.ExternalID{
									ExternalID: alias("Service", obj.GetNamespace(), obj.GetName()),
									ConfigType: ConfigTypePrefix + "Service",
								},
								RelatedConfigID: string(address.TargetRef.UID),
								Relationship:    fmt.Sprintf("Service%s", address.TargetRef.Kind),
							})
						}
					}
				}
			}
		}

		if obj.GetKind() == "Pod" {
			nodeName := getString(obj, "spec", "nodeName")

			if nodeName != "" {
				nodeID := ctx.GetID("", "Node", nodeName)
				nodeExternalID := lo.CoalesceOrEmpty(nodeID, alias("Node", "", nodeName))

				relationships = append(relationships, v1.RelationshipResult{
					RelatedConfigID: string(obj.GetUID()),
					Relationship:    "NodePod",
				}.WithConfig(
					ctx.GetID("", "Node", nodeName),
					v1.ExternalID{ExternalID: nodeExternalID, ConfigType: ConfigTypePrefix + "Node"},
				))
			}
		}

		if obj.GetNamespace() != "" {
			relationships = append(relationships, v1.RelationshipResult{
				RelatedConfigID: string(obj.GetUID()),
				Relationship:    "Namespace" + obj.GetKind(),
			}.WithConfig(
				ctx.GetID("", "Namespace", obj.GetNamespace()),
				v1.ExternalID{
					ExternalID: alias("Namespace", "", obj.GetNamespace()),
					ConfigType: ConfigTypePrefix + "Namespace",
				},
			))
		}

		for _, ownerRef := range obj.GetOwnerReferences() {
			relationships = append(relationships, v1.RelationshipResult{
				ConfigID:        string(ownerRef.UID),
				RelatedConfigID: string(obj.GetUID()),
				Relationship:    ownerRef.Kind + obj.GetKind(),
			})
		}

		for _, f := range ctx.config.Relationships {
			env := map[string]any{
				"metadata": obj.Object["metadata"],
			}
			if spec, ok := obj.Object["spec"].(map[string]any); ok {
				env["spec"] = spec
			} else {
				env["spec"] = map[string]any{}
			}

			if selector, err := f.Eval(obj.GetLabels(), env); err != nil {
				return results.Errorf(err, "failed to evaluate selector: %v for config relationship", f)
			} else if selector != nil {
				linkedConfigItemIDs, err := db.FindConfigIDsByNamespaceNameClass(ctx.DutyContext(), ctx.cluster.Name, selector.Namespace, selector.Name, selector.Kind)
				if err != nil {
					return results.Errorf(err, "failed to get linked config items by kubernetes selector(%v)", selector)
				}

				for _, id := range linkedConfigItemIDs {
					rel := v1.RelationshipResult{
						ConfigID: id.String(),
					}.WithRelated(
						ctx.GetID(obj.GetNamespace(), obj.GetKind(), obj.GetName()),
						v1.ExternalID{ExternalID: string(obj.GetUID()), ConfigType: getConfigTypePrefix(obj.GetAPIVersion()) + obj.GetKind()},
					)

					relationships = append(relationships, rel)
				}
			}
		}

		if ctx.cluster.Name != "" {
			tags.Append("cluster", ctx.cluster.Name)
		}
		if obj.GetNamespace() != "" {
			tags.Append("namespace", obj.GetNamespace())
		}

		labels["apiVersion"] = obj.GetAPIVersion()

		if obj.GetKind() == "Service" {
			if spec, ok := obj.Object["spec"].(map[string]any); ok {
				if serviceType, ok := spec["type"].(string); ok {
					labels["service-type"] = serviceType

					if serviceType == "LoadBalancer" {
						if status, ok := obj.Object["status"].(map[string]any); ok {
							if lb, ok := status["loadBalancer"].(map[string]any); ok {
								if ingresses, ok := lb["ingress"].([]any); ok {
									for _, ing := range ingresses {
										if ingress, ok := ing.(map[string]any); ok {
											if hostname, ok := ingress["hostname"].(string); ok && hostname != "" {
												labels["hostname"] = hostname

												if strings.HasSuffix(hostname, "elb.amazonaws.com") {
													relationships = append(relationships, v1.RelationshipResult{
														ConfigID:          string(obj.GetUID()),
														RelatedExternalID: v1.ExternalID{ExternalID: hostname, ConfigType: v1.AWSLoadBalancer, ScraperID: "all"},
													})
												}
											}

											if ip, ok := ingress["ip"].(string); ok && ip != "" {
												labels["ip"] = ip
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}

		// Add health metadata
		resourceHealth, err := health.GetResourceHealth(obj, lua.ResourceHealthOverrides{})
		if err != nil {
			ctx.Errorf("failed to get resource health: %v", err)
			resourceHealth = &health.HealthStatus{}
		}

		var deletedAt *time.Time
		var deleteReason v1.ConfigDeleteReason
		// Evicted Pods must be considered deleted
		if obj.GetKind() == "Pod" {
			if objStatus, ok := obj.Object["status"].(map[string]any); ok {
				if val, ok := objStatus["reason"].(string); ok && val == "Evicted" {
					// Use time.Now() as default and try to parse the evict time
					deletedAt = lo.ToPtr(time.Now())
					if evictTime, err := time.Parse(time.RFC3339, objStatus["startTime"].(string)); err != nil {
						deletedAt = &evictTime
					}
				}
			}
		}

		configObj, err := cleanKubernetesObject(obj.Object)
		if err != nil {
			return results.Errorf(err, "failed to clean kubernetes object")
		}

		parents := getKubernetesParent(ctx, obj)
		children := ChildLookupHooks(ctx, obj)
		results = append(results, v1.ScrapeResult{
			BaseScraper:         ctx.config.BaseScraper,
			Name:                obj.GetName(),
			ConfigClass:         obj.GetKind(),
			Type:                getConfigTypePrefix(obj.GetAPIVersion()) + obj.GetKind(),
			Status:              string(resourceHealth.Status),
			Health:              models.Health(resourceHealth.Health),
			Ready:               resourceHealth.Ready,
			Description:         resourceHealth.Message,
			CreatedAt:           lo.ToPtr(obj.GetCreationTimestamp().Time),
			DeletedAt:           deletedAt,
			DeleteReason:        deleteReason,
			Config:              configObj,
			ConfigID:            lo.ToPtr(string(obj.GetUID())),
			ID:                  string(obj.GetUID()),
			Labels:              stripLabels(labels, "-hash"),
			Tags:                tags,
			Aliases:             []string{alias(obj.GetKind(), obj.GetNamespace(), obj.GetName())},
			Parents:             parents,
			Children:            children,
			RelationshipResults: relationships,
		})
	}

	results = append(results, changeResults...)
	if ctx.IsIncrementalScrape() {
		results = append([]v1.ScrapeResult{ctx.cluster}, results...)
	}

	for i := range results {
		results[i].Labels = collections.MergeMap(map[string]string(results[i].Labels), ctx.globalLabels)

		switch results[i].Type {
		case ConfigTypePrefix + "Node":
			results[i].Labels = collections.MergeMap(map[string]string(results[i].Labels), ctx.labelsForAllNode)
			if l, ok := ctx.labelsPerNode[results[i].Name]; ok {
				results[i].Labels = collections.MergeMap(map[string]string(results[i].Labels), l)
			}
		}
	}

	return results
}

// getKubernetesParent returns a list of potential parents in order.
// Example: For a Pod the parents would be [Replicaset, Namespace, Cluster]
func getKubernetesParent(ctx *KubernetesContext, obj *unstructured.Unstructured) []v1.ConfigExternalKey {
	var allParents []v1.ConfigExternalKey
	allParents = append(allParents, v1.ConfigExternalKey{
		Type:       ConfigTypePrefix + "Cluster",
		ExternalID: ctx.GetID("", "Cluster", "selfRef"),
	})

	if obj.GetNamespace() != "" {
		parentExternalID := ctx.GetID("", "Namespace", obj.GetNamespace())
		if parentExternalID == "" {
			// An incremental scraper may not have the Namespace object.
			// We can instead use the alias as the external id.
			parentExternalID = alias("Namespace", "", obj.GetNamespace())
		}

		allParents = append([]v1.ConfigExternalKey{{
			Type:       ConfigTypePrefix + "Namespace",
			ExternalID: parentExternalID,
		}}, allParents...)
	}

	if len(obj.GetOwnerReferences()) > 0 {
		ref := obj.GetOwnerReferences()[0]

		// If ReplicaSet is excluded then we want the pod's direct parent to
		// be its Deployment
		if obj.GetKind() == "Pod" && lo.Contains(ctx.config.Exclusions.Kinds, "ReplicaSet") && ref.Kind == "ReplicaSet" {
			deployName := extractDeployNameFromReplicaSet(ref.Name)
			parentExternalID := ctx.GetID(obj.GetNamespace(), "Deployment", deployName)
			allParents = append([]v1.ConfigExternalKey{{
				Type:       ConfigTypePrefix + "Deployment",
				ExternalID: parentExternalID,
			}}, allParents...)
		} else {
			allParents = append([]v1.ConfigExternalKey{{
				Type:       ConfigTypePrefix + ref.Kind,
				ExternalID: string(ref.UID),
			}}, allParents...)
		}
	}

	allParents = append(ParentLookupHooks(ctx, obj), allParents...)

	return allParents
}

func alias(kind, namespace, name string) string {
	return strings.Join([]string{"Kubernetes", kind, namespace, name}, "/")
}

func extractDeployNameFromReplicaSet(rs string) string {
	split := strings.Split(rs, "-")
	split = split[:len(split)-1]
	return strings.Join(split, "-")
}

//nolint:errcheck
func cleanKubernetesObject(obj map[string]any) (map[string]any, error) {
	o := gabs.Wrap(obj)
	o.Delete("metadata", "generation")
	o.Delete("metadata", "resourceVersion")
	o.Delete("metadata", "annotations", "control-plane.alpha.kubernetes.io/leader")
	o.Delete("metadata", "annotations", "kubectl.kubernetes.io/last-applied-configuration")
	o.Delete("metadata", "managedFields")

	o.Delete("metadata", "labels", "controller-uid")
	o.Delete("metadata", "labels", "batch.kubernetes.io/job-name")
	o.Delete("metadata", "labels", "job-name")
	o.Delete("metadata", "labels", "batch.kubernetes.io/controller-uid")
	o.Delete("metadata", "labels", "kubernetes.io/metadata.name")
	o.Delete("metadata", "labels", "pod-template-generation")

	o.Delete("status", "artifact", "lastUpdateTime")
	o.Delete("status", "observedGeneration")
	o.Delete("status", "lastTransitionTime")

	c, _ := o.ArrayCount("status", "conditions")
	for i := 0; i < c; i += 1 {
		o.Delete("status", "conditions", strconv.Itoa(i), "lastTransitionTime")
		o.Delete("status", "conditions", strconv.Itoa(i), "lastHeartbeatTime")
		o.Delete("status", "conditions", strconv.Itoa(i), "lastUpdateTime")
		o.Delete("status", "conditions", strconv.Itoa(i), "observedGeneration")
	}

	// Canary CRD
	o.Delete("status", "lastCheck")
	o.Delete("status", "lastTransitionedTime")
	o.Delete("status", "latency1h")
	o.Delete("status", "uptime1h")

	for k := range o.Search("status", "checkStatus").ChildrenMap() {
		o.Delete("status", "checkStatus", k, "lastTransitionedTime")
		o.Delete("status", "checkStatus", k, "latency1h")
		o.Delete("status", "checkStatus", k, "uptime1h")
	}

	var output map[string]any
	return output, json.Unmarshal(o.Bytes(), &output)
}

// getContainerEnv returns the value of the given environment var
// on the first component it finds.
func getContainerEnv(podSpec map[string]any, envName string) string {
	if containers, ok := podSpec["containers"].([]any); ok {
		for _, container := range containers {
			if envsRaw, ok := container.(map[string]any)["env"]; ok {
				if envs, ok := envsRaw.([]any); ok {
					for _, envRaw := range envs {
						if env, ok := envRaw.(map[string]any); ok {
							if env["name"] == envName {
								return env["value"].(string)
							}
						}
					}
				}
			}
		}
	}

	return ""
}
