package kubernetes

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Jeffail/gabs/v2"
	"github.com/flanksource/commons/collections"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/utils/kube"
	"github.com/flanksource/is-healthy/pkg/health"
	"github.com/flanksource/is-healthy/pkg/lua"
	"github.com/flanksource/ketall"
	"github.com/flanksource/ketall/options"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type KubernetesScraper struct {
}

const ConfigTypePrefix = "Kubernetes::"

func getConfigTypePrefix(apiVersion string) string {
	if strings.Contains(apiVersion, ".upbound.io") || strings.Contains(apiVersion, ".crossplane.io") {
		return "Crossplane::"
	}

	return ConfigTypePrefix
}

func (kubernetes KubernetesScraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.Kubernetes) > 0
}

func (kubernetes KubernetesScraper) IncrementalScrape(ctx api.ScrapeContext, config v1.Kubernetes, events []v1.KubernetesEvent) v1.ScrapeResults {
	if len(events) == 0 {
		return nil
	}
	ctx.DutyContext().Logger.V(4).Infof("incrementally scraping resources from %d events", len(events))

	var r v1.ScrapeResults
	var err error
	var restConfig *rest.Config
	if config.Kubeconfig != nil {
		ctx, restConfig, err = applyKubeconfig(ctx, *config.Kubeconfig)
		if err != nil {
			return r.Errorf(err, "failed to apply custom kube config")
		}
	} else {
		restConfig, err = kube.DefaultRestConfig()
		if err != nil {
			return r.Errorf(err, "failed to get default rest config")
		}
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
				kclient, err = kube.GetClientByGroupVersionKind(restConfig, resource.APIVersion, resource.Kind)
				if err != nil {
					continue
				}
				kindClientCache[resource.APIVersion+resource.Kind] = kclient
			}

			ctx.DutyContext().Logger.V(5).Infof("fetching resource namespace=%s name=%s kind=%s apiVersion=%s", resource.Namespace, resource.Name, resource.Kind, resource.APIVersion)
			obj, err := kclient.Namespace(resource.Namespace).Get(ctx, resource.Name, metav1.GetOptions{})
			if err != nil {
				ctx.DutyContext().Errorf("failed to get resource (Kind=%s, Name=%s, Namespace=%s): %v", resource.Kind, resource.Name, resource.Namespace, err)
				continue
			} else if obj != nil {
				seenObjects[cacheKey] = struct{}{} // mark it as seen so we don't run ketall.KetOne again (in this run)
				objects = append(objects, obj)
			}
		}
	}

	ctx.DutyContext().Logger.V(3).Infof("found %d objects for %d events", len(objects)-len(events), len(events))
	if len(objects) == 0 {
		return nil
	}

	return ExtractResults(ctx.DutyContext(), config, objects, false)
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

		opts := options.NewDefaultCmdOptions()
		opts, err = updateOptions(ctx.DutyContext(), opts, config)
		if err != nil {
			return results.Errorf(err, "error setting up kube config")
		}

		objs, err := ketall.KetAll(ctx, opts)
		if err != nil {
			return results.Errorf(err, "failed to fetch resources")
		}

		extracted := ExtractResults(ctx.DutyContext(), config, objs, true)
		results = append(results, extracted...)
	}

	return results
}

// ExtractResults extracts scrape results from the given list of kuberenetes objects.
//   - withCluster: if true, will create & add a scrape result for the kubernetes cluster.
func ExtractResults(ctx context.Context, config v1.Kubernetes, objs []*unstructured.Unstructured, withCluster bool) v1.ScrapeResults {
	var (
		results       v1.ScrapeResults
		changeResults v1.ScrapeResults

		// Labels that are added to the kubernetes nodes once all the objects are visited
		labelsPerNode = map[string]map[string]string{}

		// labelsForAllNode are common labels applicable to all the nodes in the cluster
		labelsForAllNode = map[string]string{}

		// globalLabels are common labels for any kubernetes resource
		globalLabels = map[string]string{}
	)

	clusterID := "Kubernetes/Cluster/" + config.ClusterName
	cluster := v1.ScrapeResult{
		BaseScraper: config.BaseScraper,
		Name:        config.ClusterName,
		ConfigClass: "Cluster",
		Type:        ConfigTypePrefix + "Cluster",
		Config:      make(map[string]any),
		Labels:      make(v1.JSONStringMap),
		ID:          clusterID,
		Tags:        v1.Tags{{Name: "cluster", Value: config.ClusterName}},
	}

	resourceIDMap := getResourceIDsFromObjs(objs)
	resourceIDMap[""]["Cluster"] = make(map[string]string)
	resourceIDMap[""]["Cluster"][config.ClusterName] = clusterID
	resourceIDMap[""]["Cluster"]["selfRef"] = clusterID // For shorthand

	for _, obj := range objs {
		tags := config.Tags

		if config.Exclusions.Filter(obj.GetName(), obj.GetNamespace(), obj.GetKind(), obj.GetLabels()) {
			ctx.Tracef("excluding object: %s/%s/%s", obj.GetKind(), obj.GetNamespace(), obj.GetName())
			continue
		}

		if string(obj.GetUID()) == "" {
			ctx.Warnf("Found kubernetes object with no resource ID: %s/%s/%s", obj.GetKind(), obj.GetNamespace(), obj.GetName())
			continue
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

			if config.Event.Exclusions.Filter(event) {
				ctx.Logger.V(4).Infof("excluding event object %s/%s/%s: %s",
					event.InvolvedObject.Namespace, event.InvolvedObject.Name,
					event.InvolvedObject.Kind, event.Reason)
				continue
			}

			change := getChangeFromEvent(event, config.Event.SeverityKeywords)
			if change != nil {
				changeResults = append(changeResults, v1.ScrapeResult{
					BaseScraper: config.BaseScraper,
					Changes:     []v1.ChangeResult{*change},
				})
			}

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

		if obj.GetKind() == "Node" {
			if clusterName, ok := obj.GetLabels()["kubernetes.azure.com/cluster"]; ok {
				// kubernetes.azure.com/cluster doesn't actually contain the
				// AKS cluster name - it contains the node resource group.
				// The cluster name isn't available in the node.
				cluster.Labels["aks-nodeResourceGroup"] = clusterName
			}

			if spec, ok := obj.Object["spec"].(map[string]interface{}); ok {
				if providerID, ok := spec["providerID"].(string); ok {
					subID, vmScaleSetID := parseAzureURI(providerID)
					if subID != "" {
						globalLabels["azure/subscription-id"] = subID
					}
					if vmScaleSetID != "" {
						labels["azure/vm-scale-set"] = vmScaleSetID
					}
				}
			}
		}

		if obj.GetKind() == "Pod" {
			spec := obj.Object["spec"].(map[string]interface{})
			var nodeName string
			if spec["nodeName"] != nil {
				nodeName = spec["nodeName"].(string)
				nodeID := resourceIDMap[""]["Node"][nodeName]
				relationships = append(relationships, v1.RelationshipResult{
					ConfigExternalID:  v1.ExternalID{ExternalID: []string{nodeID}, ConfigType: ConfigTypePrefix + "Node"},
					RelatedExternalID: v1.ExternalID{ExternalID: []string{string(obj.GetUID())}, ConfigType: ConfigTypePrefix + "Pod"},
					Relationship:      "NodePod",
				})
			}

			if obj.GetLabels()["app.kubernetes.io/name"] == "aws-node" {
				for _, ownerRef := range obj.GetOwnerReferences() {
					if ownerRef.Kind == "DaemonSet" && ownerRef.Name == "aws-node" {
						var (
							awsRoleARN     = getContainerEnv(spec, "AWS_ROLE_ARN")
							vpcID          = getContainerEnv(spec, "VPC_ID")
							awsClusterName = getContainerEnv(spec, "CLUSTER_NAME")
						)

						labelsPerNode[nodeName] = make(map[string]string)

						if awsRoleARN != "" {
							labelsPerNode[nodeName]["aws/iam-role"] = awsRoleARN
						}

						if vpcID != "" {
							labelsForAllNode["aws/vpc-id"] = vpcID

							if clusterScrapeResult, ok := cluster.Config.(map[string]any); ok {
								clusterScrapeResult["vpc-id"] = vpcID
							}
						}

						if awsClusterName != "" {
							if clusterScrapeResult, ok := cluster.Config.(map[string]any); ok {
								clusterScrapeResult["cluster-name"] = awsClusterName
							}
						}
					}
				}
			}
		}

		if obj.GetNamespace() != "" {
			relationships = append(relationships, v1.RelationshipResult{
				ConfigExternalID:  v1.ExternalID{ExternalID: []string{fmt.Sprintf("Kubernetes/Namespace//%s", obj.GetNamespace())}, ConfigType: ConfigTypePrefix + "Namespace"},
				RelatedExternalID: v1.ExternalID{ExternalID: []string{string(obj.GetUID())}, ConfigType: getConfigTypePrefix(obj.GetAPIVersion()) + obj.GetKind()},
				Relationship:      "Namespace" + obj.GetKind(),
			})
		}

		for _, ownerRef := range obj.GetOwnerReferences() {
			relationships = append(relationships, v1.RelationshipResult{
				ConfigExternalID:  v1.ExternalID{ExternalID: []string{string(ownerRef.UID)}, ConfigType: getConfigTypePrefix(ownerRef.APIVersion) + ownerRef.Kind},
				RelatedExternalID: v1.ExternalID{ExternalID: []string{string(obj.GetUID())}, ConfigType: getConfigTypePrefix(obj.GetAPIVersion()) + obj.GetKind()},
				Relationship:      ownerRef.Kind + obj.GetKind(),
			})
		}

		for _, f := range config.Relationships {
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
				linkedConfigItemIDs, err := db.FindConfigIDsByNamespaceNameClass(ctx, selector.Namespace, selector.Name, selector.Kind)
				if err != nil {
					return results.Errorf(err, "failed to get linked config items by kubernetes selector(%v)", selector)
				}

				for _, id := range linkedConfigItemIDs {
					rel := v1.RelationshipResult{
						RelatedExternalID: v1.ExternalID{ExternalID: []string{string(obj.GetUID())}, ConfigType: getConfigTypePrefix(obj.GetKind()) + obj.GetKind()},
						ConfigID:          id.String(),
					}
					relationships = append(relationships, rel)
				}
			}
		}

		if obj.GetKind() == "ConfigMap" && obj.GetName() == "aws-auth" {
			cm, ok := obj.Object["data"].(map[string]any)
			if ok {
				var accountID string
				if mapRolesYAML, ok := cm["mapRoles"].(string); ok {
					accountID = extractAccountIDFromARN(mapRolesYAML)
				}

				tags.Append("account", accountID)

				if clusterScrapeResult, ok := cluster.Config.(map[string]any); ok {
					clusterScrapeResult["aws-auth"] = cm
					clusterScrapeResult["account-id"] = accountID
				}
			}
		}

		if cluster.Name != "" {
			tags.Append("cluster", cluster.Name)
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

		parents := getKubernetesParent(obj, config.Exclusions, resourceIDMap)
		results = append(results, v1.ScrapeResult{
			BaseScraper:         config.BaseScraper,
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
			Aliases:             []string{getKubernetesAlias(obj.GetKind(), obj.GetNamespace(), obj.GetName())},
			Parents:             parents,
			RelationshipResults: relationships,
		})
	}

	results = append(results, changeResults...)
	if withCluster {
		results = append([]v1.ScrapeResult{cluster}, results...)
	}

	for i := range results {
		results[i].Labels = collections.MergeMap(map[string]string(results[i].Labels), globalLabels)

		switch results[i].Type {
		case ConfigTypePrefix + "Node":
			results[i].Labels = collections.MergeMap(map[string]string(results[i].Labels), labelsForAllNode)
			if l, ok := labelsPerNode[results[i].Name]; ok {
				results[i].Labels = collections.MergeMap(map[string]string(results[i].Labels), l)
			}
		}
	}

	return results
}

// getKubernetesParent returns a list of potential parents in order.
// Example: For a Pod the parents would be [Replicaset, Namespace, Cluster]
func getKubernetesParent(obj *unstructured.Unstructured, exclusions v1.KubernetesExclusionConfig, resourceIDMap map[string]map[string]map[string]string) []v1.ConfigExternalKey {
	var allParents []v1.ConfigExternalKey
	allParents = append(allParents, v1.ConfigExternalKey{
		Type:       ConfigTypePrefix + "Cluster",
		ExternalID: resourceIDMap[""]["Cluster"]["selfRef"],
	})

	if obj.GetNamespace() != "" {
		parentExternalID := resourceIDMap[""]["Namespace"][obj.GetNamespace()]
		if parentExternalID == "" {
			// An incremental scraper maynot have the Namespace object.
			// We can instead use the alias as the external id.
			parentExternalID = getKubernetesAlias("Namespace", "", obj.GetNamespace())
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
		if obj.GetKind() == "Pod" && lo.Contains(exclusions.Kinds, "ReplicaSet") && ref.Kind == "ReplicaSet" {
			deployName := extractDeployNameFromReplicaSet(ref.Name)
			parentExternalID := resourceIDMap[obj.GetNamespace()]["Deployment"][deployName]
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

	return allParents
}

func getKubernetesAlias(kind, namespace, name string) string {
	return strings.Join([]string{"Kubernetes", kind, namespace, name}, "/")
}

func updateOptions(ctx context.Context, opts *options.KetallOptions, config v1.Kubernetes) (*options.KetallOptions, error) {
	opts.AllowIncomplete = config.AllowIncomplete
	opts.Namespace = config.Namespace
	opts.Scope = config.Scope
	opts.Selector = config.Selector
	opts.FieldSelector = config.FieldSelector
	opts.UseCache = config.UseCache
	opts.MaxInflight = config.MaxInflight
	opts.Exclusions = config.Exclusions.List()
	opts.Since = config.Since
	if config.Kubeconfig != nil {
		val, err := ctx.GetEnvValueFromCache(*config.Kubeconfig, ctx.GetNamespace())
		if err != nil {
			return nil, err
		}

		if strings.HasPrefix(val, "/") {
			opts.Flags.ConfigFlags.KubeConfig = &val
		} else {
			clientCfg, err := clientcmd.NewClientConfigFromBytes([]byte(val))
			if err != nil {
				return nil, fmt.Errorf("failed to create client config: %w", err)
			}

			restConfig, err := clientCfg.ClientConfig()
			if err != nil {
				return nil, err
			}

			opts.Flags.KubeConfig = restConfig
		}
	}

	return opts, nil
}

func extractDeployNameFromReplicaSet(rs string) string {
	split := strings.Split(rs, "-")
	split = split[:len(split)-1]
	return strings.Join(split, "-")
}

func getResourceIDsFromObjs(objs []*unstructured.Unstructured) map[string]map[string]map[string]string {
	// {Namespace: {Kind: {Name: ID}}}
	resourceIDMap := make(map[string]map[string]map[string]string)
	resourceIDMap[""] = make(map[string]map[string]string)

	for _, obj := range objs {
		if collections.Contains([]string{"Namespace", "Deployment", "Node"}, obj.GetKind()) {
			if resourceIDMap[obj.GetNamespace()] == nil {
				resourceIDMap[obj.GetNamespace()] = make(map[string]map[string]string)
			}
			if resourceIDMap[obj.GetNamespace()][obj.GetKind()] == nil {
				resourceIDMap[obj.GetNamespace()][obj.GetKind()] = make(map[string]string)
			}
			resourceIDMap[obj.GetNamespace()][obj.GetKind()][obj.GetName()] = string(obj.GetUID())
		}
	}

	return resourceIDMap
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

var arnRegexp = regexp.MustCompile(`arn:aws:iam::(\d+):role/`)

func extractAccountIDFromARN(input string) string {
	matches := arnRegexp.FindStringSubmatch(input)
	if len(matches) >= 2 {
		return matches[1]
	}

	return ""
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

func parseAzureURI(uri string) (string, string) {
	if !strings.HasPrefix(uri, "azure:///subscriptions/") {
		return "", ""
	}

	parts := strings.Split(uri, "/")
	var subscriptionID, vmScaleSetID string
	for i := 0; i < len(parts); i++ {
		if parts[i] == "subscriptions" && i+1 < len(parts) {
			subscriptionID = parts[i+1]
		}

		if parts[i] == "virtualMachineScaleSets" && i+1 < len(parts) {
			vmScaleSetID = parts[i+1]
			break
		}
	}

	return subscriptionID, vmScaleSetID
}
