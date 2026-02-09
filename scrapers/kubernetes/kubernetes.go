package kubernetes

import (
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Jeffail/gabs/v2"
	"github.com/flanksource/commons/collections"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/is-healthy/pkg/health"
	"github.com/flanksource/is-healthy/pkg/lua"
	"github.com/google/uuid"
	"github.com/samber/lo"
	coreV1 "k8s.io/api/core/v1"
	discoveryV1 "k8s.io/api/discovery/v1"
	netV1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
)

const ConfigTypePrefix = "Kubernetes::"

const (
	KindPod         = "Pod"
	KindReplicaSet  = "ReplicaSet"
	KindStatefulSet = "StatefulSet"
	KindDeployment  = "Deployment"
	KindDaemonSet   = "DaemonSet"
	KindJob         = "Job"
)

var ResourceIDMapPerCluster PerClusterResourceIDMap

func GetConfigType(obj *unstructured.Unstructured) string {
	apiVersion := obj.GetAPIVersion()
	if strings.Contains(apiVersion, ".upbound.io") || strings.Contains(apiVersion, ".crossplane.io") {
		return "Crossplane::" + obj.GetKind()
	}

	if strings.HasSuffix(apiVersion, ".flanksource.com/v1") {
		return api.MissionControlConfigTypePrefix + obj.GetKind()
	}

	return ConfigTypePrefix + obj.GetKind()
}

type KubernetesScraper struct{}

func (KubernetesScraper) CanScrape(configs v1.ScraperSpec) bool {
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

func (kubernetes KubernetesScraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var (
		results v1.ScrapeResults
		err     error
	)

	for _, config := range ctx.ScrapeConfig().Spec.Kubernetes {
		if config.ClusterName == "" {
			return results.Errorf(err, "clusterName missing from kubernetes configuration")
		}

		if scraperID := ctx.ScraperID(); scraperID != "" {
			// (ClusterName, ScraperID) should always be unique
			var scraperIDs []string
			if err := ctx.DB().Model(&models.ConfigItem{}).Select("scraper_id").
				Where("name = ? AND type = 'Kubernetes::Cluster' AND deleted_at IS NULL AND scraper_id IS NOT NULL", config.ClusterName).
				Find(&scraperIDs).Error; err != nil {
				return results.Errorf(err, "error querying db for scraper_id with cluster name: %s", config.ClusterName)
			}

			if len(scraperIDs) > 1 {
				return results.Errorf(fmt.Errorf("multiple scraper_ids[%s] found with cluster name: %s", strings.Join(scraperIDs, ","), config.ClusterName), "")
			}

			if len(scraperIDs) == 1 && lo.FirstOrEmpty(scraperIDs) != scraperID {
				return results.Errorf(fmt.Errorf("scraper_id[%s] already exists with cluster name: %s", strings.Join(scraperIDs, ","), config.ClusterName), "")
			}
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

var IgnoredConfigsCache = sync.Map{}

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

	// Initialize RBAC extractor for config access tracking
	rbac := newRBACExtractor(clusterName, ctx.ScrapeConfig().GetPersistedID())
	rbac.indexObjects(objs)
	var roleBindings []*unstructured.Unstructured

	ctx.Load(objs)
	if ctx.IsIncrementalScrape() {
		// On incremental scrape, we do not have all the data in the resource ID map.
		// we use it from the cached resource id map.
		ctx.resourceIDMap.data = ResourceIDMapPerCluster.MergeAndUpdate(
			string(ctx.ScrapeConfig().GetUID()),
			ctx.resourceIDMap.data,
		)
	} else {
		ResourceIDMapPerCluster.Swap(string(ctx.ScrapeConfig().GetUID()), ctx.resourceIDMap.data)
	}

	for _, obj := range objs {
		tags := ctx.config.Tags

		if ignore, err := ctx.IsIgnored(obj); err != nil {
			ctx.Warnf("failed to ignore obj[%s]: %v", obj.GetName(), err)
			continue
		} else if ignore {
			ctx.Counter("kubernetes_scraper_ignored", "source", "scrape", "kind", obj.GetKind(), "scraper_id", ctx.ScraperID()).Add(1)

			IgnoredConfigsCache.Store(obj.GetUID(), struct{}{})
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
				ctx.Counter("kubernetes_scraper_unmatched",
					"source", "scrape",
					"kind", event.InvolvedObject.Kind,
					"reason", "unmarshal_error",
					"scraper_id", ctx.ScraperID(),
				).Add(1)

				ctx.Errorf("failed to parse event: %v", err)
				return nil
			}

			if event.InvolvedObject == nil {
				ctx.Counter("kubernetes_scraper_unmatched",
					"source", "scrape",
					"kind", "",
					"reason", "involved_object_nil",
					"scraper_id", ctx.ScraperID(),
				).Add(1)

				continue
			}

			if ctx.config.Event.Exclusions.Filter(event) {
				ctx.Counter("kubernetes_scraper_changes_excluded",
					"source", "scrape",
					"change_type", event.Reason,
					"kind", event.InvolvedObject.Kind,
					"scraper_id", ctx.ScraperID(),
				).Add(1)

				if ctx.logExclusions {
					ctx.Tracef("excluding event object %s/%s/%s: %s",
						event.InvolvedObject.Namespace, event.InvolvedObject.Name,
						event.InvolvedObject.Kind, event.Reason)
				}

				continue
			}

			uid, err := ctx.FindInvolvedConfigID(event)
			if err != nil {
				ctx.Counter("kubernetes_scraper_unmatched",
					"source", "scrape",
					"kind", event.InvolvedObject.Kind,
					"reason", "find_error",
					"kind", obj.GetKind(),
					"scraper_id", ctx.ScraperID(),
				).Add(1)

				results.Errorf(err, "could not find config for event")
				continue
			} else if uid == uuid.Nil {
				if ctx.logExclusions {
					ctx.Counter("kubernetes_scraper_excluded",
						"source", "scrape",
						"kind", obj.GetKind(),
						"scraper_id", ctx.ScraperID()).Add(1)
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
				ctx.Counter("kubernetes_scraper_changes_ignored",
					"source", "scrape",
					"change_type", change.ChangeType,
					"kind", event.InvolvedObject.Kind,
					"scraper_id", ctx.ScraperID(),
				).Add(1)

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
			aliases       = []string{KubernetesAlias(ctx.ClusterName(), obj.GetKind(), obj.GetNamespace(), obj.GetName())}
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
			maps.Copy(labels, _labels)
		}

		if obj.GetKind() == "EndpointSlice" {
			var endpointSlice discoveryV1.EndpointSlice
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &endpointSlice); err != nil {
				return results.Errorf(err, "failed to unmarshal endpoint slice (%s/%s)", obj.GetUID(), obj.GetName())
			}

			for _, endpoint := range endpointSlice.Endpoints {
				if endpoint.TargetRef != nil {
					if endpoint.TargetRef.Kind != "Service" {
						// Get the service name from the kubernetes.io/service-name label
						serviceName := obj.GetName()
						if svcName, ok := endpointSlice.Labels["kubernetes.io/service-name"]; ok {
							serviceName = svcName
						}

						relationships = append(relationships, v1.RelationshipResult{
							ConfigExternalID: v1.ExternalID{
								ExternalID: KubernetesAlias(ctx.ClusterName(), "Service", obj.GetNamespace(), serviceName),
								ConfigType: ConfigTypePrefix + "Service",
							},
							RelatedConfigID: string(endpoint.TargetRef.UID),
							Relationship:    fmt.Sprintf("Service%s", endpoint.TargetRef.Kind),
						})
					}
				}
			}
		}

		switch obj.GetKind() {
		case KindPod:
			nodeName := getString(obj, "spec", "nodeName")

			if nodeName != "" {
				nodeID := ctx.GetID("", "Node", nodeName)
				nodeExternalID := lo.CoalesceOrEmpty(nodeID, KubernetesAlias(ctx.ClusterName(), "Node", "", nodeName))

				labels["node"] = nodeName

				relationships = append(relationships, v1.RelationshipResult{
					RelatedConfigID: string(obj.GetUID()),
					Relationship:    "NodePod",
				}.WithConfig(
					ctx.GetID("", "Node", nodeName),
					v1.ExternalID{ExternalID: nodeExternalID, ConfigType: ConfigTypePrefix + "Node"},
				))
			}

		case "Node":
			providerID := getString(obj, "spec", "providerID")
			aliases = append(aliases, providerID)

		case "Service":
			var svc coreV1.Service
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &svc); err != nil {
				return results.Errorf(err, "failed to convert service to corev1.Service: %v", err)
			}

			for _, ingress := range svc.Status.LoadBalancer.Ingress {
				if ingress.Hostname != "" {
					aliases = append(aliases, lbServiceAlias(ingress.Hostname))
					labels["hostname"] = ingress.Hostname

					if strings.HasSuffix(ingress.Hostname, "elb.amazonaws.com") {
						relationships = append(relationships, v1.RelationshipResult{
							ConfigID:          string(obj.GetUID()),
							RelatedExternalID: v1.ExternalID{ExternalID: ingress.Hostname, ConfigType: v1.AWSLoadBalancer, ScraperID: "all"},
						})
					}
				}

				if ingress.IP != "" {
					aliases = append(aliases, lbServiceAlias(ingress.IP))
					labels["ip"] = ingress.IP
				}
			}

		case "Ingress":
			var ingress netV1.Ingress
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &ingress); err != nil {
				return results.Errorf(err, "failed to convert ingress to netV1.Ingress: %v", err)
			}

			// Link to Parent LoadBalancer Service
			for _, ing := range ingress.Status.LoadBalancer.Ingress {
				if ing.Hostname != "" {
					labels["hostname"] = ing.Hostname

					rel := v1.RelationshipResult{Relationship: "LBServiceIngress"}.
						WithRelated(string(obj.GetUID()), v1.ExternalID{}).
						WithConfig("", v1.ExternalID{
							ConfigType: ConfigTypePrefix + "Service",
							ExternalID: lbServiceAlias(ing.Hostname),
						})
					relationships = append(relationships, rel)
				}

				if ing.IP != "" {
					labels["ip"] = ing.IP

					rel := v1.RelationshipResult{Relationship: "LBServiceIngress"}.
						WithRelated(string(obj.GetUID()), v1.ExternalID{}).
						WithConfig("", v1.ExternalID{
							ConfigType: ConfigTypePrefix + "Service",
							ExternalID: lbServiceAlias(ing.IP),
						})
					relationships = append(relationships, rel)
				}
			}

			// Link ingress to to target service
			for _, rule := range ingress.Spec.Rules {
				for _, path := range rule.HTTP.Paths {
					if service := path.Backend.Service.Name; service != "" {
						relationships = append(relationships, v1.RelationshipResult{
							ConfigID:     string(obj.GetUID()),
							Relationship: "IngressService",
							RelatedExternalID: v1.ExternalID{
								ConfigType: ConfigTypePrefix + "Service",
								ExternalID: KubernetesAlias(ctx.ClusterName(), "Service", obj.GetNamespace(), service),
							},
						})
					}
				}
			}

		case "ClusterRole", "Role":
			rbac.processRole(obj)

		case "ClusterRoleBinding", "RoleBinding":
			// Store bindings for later processing after all roles are processed
			roleBindings = append(roleBindings, obj)
		}

		if obj.GetNamespace() != "" {
			relationships = append(relationships, v1.RelationshipResult{
				RelatedConfigID: string(obj.GetUID()),
				Relationship:    "Namespace" + obj.GetKind(),
			}.WithConfig(
				ctx.GetID("", "Namespace", obj.GetNamespace()),
				v1.ExternalID{
					ExternalID: KubernetesAlias(ctx.ClusterName(), "Namespace", "", obj.GetNamespace()),
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

			switch ownerRef.Kind {
			case KindReplicaSet:
				labels["deployment"] = extractDeployNameFromReplicaSet(ownerRef.Name)
			case KindDeployment:
				labels["deployment"] = ownerRef.Name
			case KindStatefulSet:
				labels["statefulset"] = ownerRef.Name
			case KindDaemonSet:
				labels["daemonset"] = ownerRef.Name
			case KindJob:
				labels["job"] = ownerRef.Name
			}
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
						v1.ExternalID{ExternalID: string(obj.GetUID()), ConfigType: GetConfigType(obj)},
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

		// Add health metadata
		resourceHealth, err := health.GetResourceHealth(obj, lua.ResourceHealthOverrides{})
		if err != nil {
			ctx.Errorf("failed to get resource health: %v", err)
			resourceHealth = &health.HealthStatus{}
		}

		var deletedAt *time.Time
		var deleteReason v1.ConfigDeleteReason
		// Evicted Pods must be considered deleted
		if obj.GetKind() == KindPod {
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
		allAliases := append(aliases, AliasLookupHooks(ctx, obj)...)
		props := PropertyLookupHooks(ctx, obj)
		results = append(results, v1.ScrapeResult{
			BaseScraper:         ctx.config.BaseScraper,
			Name:                obj.GetName(),
			ConfigClass:         obj.GetKind(),
			Type:                GetConfigType(obj),
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
			Aliases:             allAliases,
			Parents:             parents,
			Children:            children,
			RelationshipResults: relationships,
			Properties:          props,
		})
	}

	// Process role bindings after all roles have been processed
	for _, binding := range roleBindings {
		rbac.processRoleBinding(binding)
	}

	// Append RBAC results (ExternalRoles, ExternalUsers, ExternalGroups, ConfigAccess)
	rbacResult := rbac.results(ctx.config.BaseScraper)
	results = append(results, rbacResult)

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
			parentExternalID = KubernetesAlias(ctx.ClusterName(), "Namespace", "", obj.GetNamespace())
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
		if obj.GetKind() == KindPod && lo.Contains(ctx.config.Exclusions.Kinds, "ReplicaSet") && ref.Kind == "ReplicaSet" {
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

	if obj.GetKind() == "EndpointSlice" {
		// EndpointSlices are linked to their Service via the kubernetes.io/service-name label
		serviceName := obj.GetName()
		if svcName, ok := obj.GetLabels()["kubernetes.io/service-name"]; ok {
			serviceName = svcName
		}

		allParents = append([]v1.ConfigExternalKey{{
			Type:       ConfigTypePrefix + "Service",
			ExternalID: KubernetesAlias(ctx.ClusterName(), "Service", obj.GetNamespace(), serviceName),
		}}, allParents...)
	}

	return allParents
}

func lbServiceAlias(hostOrIP string) string {
	return fmt.Sprintf("Kubernetes/LoadBalancerService/%s", hostOrIP)
}

func KubernetesAlias(cluster, kind, namespace, name string) string {
	return strings.Join([]string{"Kubernetes", cluster, kind, namespace, name}, "/")
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

	o.Delete("metadata", "labels", "batch.kubernetes.io/controller-uid")
	o.Delete("metadata", "labels", "batch.kubernetes.io/job-name")
	o.Delete("metadata", "labels", "controller-uid")
	o.Delete("metadata", "labels", "job-name")
	o.Delete("metadata", "labels", "kubernetes.io/created-for/pv/name")
	o.Delete("metadata", "labels", "kubernetes.io/created-for/pvc/name")
	o.Delete("metadata", "labels", "kubernetes.io/created-for/pvc/namespace")
	o.Delete("metadata", "labels", "kubernetes.io/metadata.name")
	o.Delete("metadata", "labels", "name")
	o.Delete("metadata", "labels", "pod-template-generation")
	o.Delete("metadata", "labels", "statefulset.kubernetes.io/pod-name")

	o.Delete("status", "artifact", "lastUpdateTime")
	o.Delete("status", "observedGeneration")
	o.Delete("status", "lastTransitionTime")

	if cData := o.S("status", "conditions").Data(); cData != nil {
		if conditions, ok := cData.([]any); ok {
			// sort conditions to prevent unnecessary diffs
			sort.Slice(conditions, func(i, j int) bool {
				return gabs.Wrap(conditions[i]).S("type").Data().(string) < gabs.Wrap(conditions[j]).S("type").Data().(string)
			})

			if _, err := o.Set(conditions, "status", "conditions"); err != nil {
				return nil, fmt.Errorf("failed to set sorted status.conditions: %w", err)
			}
		}
	}

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
