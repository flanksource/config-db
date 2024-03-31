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
	"github.com/samber/lo"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/is-healthy/pkg/health"
	"github.com/flanksource/ketall"
	"github.com/flanksource/ketall/options"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type KubernetesScraper struct {
}

const ConfigTypePrefix = "Kubernetes::"

func (kubernetes KubernetesScraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.Kubernetes) > 0
}

func (kubernetes KubernetesScraper) IncrementalScrape(ctx api.ScrapeContext, config v1.Kubernetes, events []v1.KubernetesEvent) v1.ScrapeResults {
	ctx.DutyContext().Tracef("incrementally scraping resources from %d events", len(events))

	var (
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
		resource := event.InvolvedObject
		cacheKey := fmt.Sprintf("%s/%s/%s", resource.Namespace, resource.Kind, resource.Name)
		if _, ok := seenObjects[cacheKey]; !ok {
			ctx.DutyContext().Logger.V(5).Infof("ketone namespace=%s name=%s kind=%s", resource.Namespace, resource.Name, resource.Kind)
			obj, err := ketall.KetOne(ctx, resource.Name, resource.Namespace, resource.Kind, options.NewDefaultCmdOptions())
			if err != nil {
				ctx.DutyContext().Errorf("failed to get resource (Kind=%s, Name=%s, Namespace=%s): %v", resource.Kind, resource.Name, resource.Namespace, err)
				continue
			} else if obj != nil {
				seenObjects[cacheKey] = struct{}{} // mark it as seen so we don't run ketall.KetOne again (in this run)
				objects = append(objects, obj)
			}
		}
	}

	ctx.DutyContext().Tracef("found %d objects for %d ids", len(objects), len(events))
	if len(objects) == 0 {
		return nil
	}

	return extractResults(ctx.DutyContext(), config, objects, false)
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
		objs := ketall.KetAll(opts)

		extracted := extractResults(ctx.DutyContext(), config, objs, true)
		results = append(results, extracted...)
	}

	return results
}

// extractResults extracts scrape results from the given list of kuberenetes objects.
//   - withCluster: if true, will create & add a scrape result for the kubernetes cluster.
func extractResults(ctx context.Context, config v1.Kubernetes, objs []*unstructured.Unstructured, withCluster bool) v1.ScrapeResults {
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
		Tags:        make(v1.JSONStringMap),
		ID:          clusterID,
	}

	resourceIDMap := getResourceIDsFromObjs(objs)
	resourceIDMap[""]["Cluster"] = make(map[string]string)
	resourceIDMap[""]["Cluster"][config.ClusterName] = clusterID
	resourceIDMap[""]["Cluster"]["selfRef"] = clusterID // For shorthand

	for _, obj := range objs {
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
			tags          = make(map[string]string)
		)

		if obj.GetNamespace() != "" {
			tags["namespace"] = obj.GetNamespace()
		}

		if obj.GetLabels() != nil {
			tags = obj.GetLabels()
		}

		if obj.GetKind() == "Node" {
			if clusterName, ok := obj.GetLabels()["kubernetes.azure.com/cluster"]; ok {
				// kubernetes.azure.com/cluster doesn't actually contain the
				// AKS cluster name - it contains the node resource group.
				// The cluster name isn't available in the node.
				cluster.Tags["aks-nodeResourceGroup"] = clusterName
			}

			if spec, ok := obj.Object["spec"].(map[string]interface{}); ok {
				if providerID, ok := spec["providerID"].(string); ok {
					subID, vmScaleSetID := parseAzureURI(providerID)
					if subID != "" {
						globalLabels["azure/subscription-id"] = subID
					}
					if vmScaleSetID != "" {
						tags["azure/vm-scale-set"] = vmScaleSetID
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
							awsRoleARN  = getContainerEnv(spec, "AWS_ROLE_ARN")
							vpcID       = getContainerEnv(spec, "VPC_ID")
							clusterName = getContainerEnv(spec, "CLUSTER_NAME")
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

						if clusterName != "" {
							if clusterScrapeResult, ok := cluster.Config.(map[string]any); ok {
								clusterScrapeResult["cluster-name"] = clusterName
							}

							cluster.Tags["eks-cluster-name"] = clusterName
						}
					}
				}
			}
		}

		if obj.GetNamespace() != "" {
			relationships = append(relationships, v1.RelationshipResult{
				ConfigExternalID:  v1.ExternalID{ExternalID: []string{fmt.Sprintf("Kubernetes/Namespace//%s", obj.GetNamespace())}, ConfigType: ConfigTypePrefix + "Namespace"},
				RelatedExternalID: v1.ExternalID{ExternalID: []string{string(obj.GetUID())}, ConfigType: ConfigTypePrefix + obj.GetKind()},
				Relationship:      "Namespace" + obj.GetKind(),
			})
		}

		for _, ownerRef := range obj.GetOwnerReferences() {
			relationships = append(relationships, v1.RelationshipResult{
				ConfigExternalID:  v1.ExternalID{ExternalID: []string{string(ownerRef.UID)}, ConfigType: ConfigTypePrefix + ownerRef.Kind},
				RelatedExternalID: v1.ExternalID{ExternalID: []string{string(obj.GetUID())}, ConfigType: ConfigTypePrefix + obj.GetKind()},
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
						RelatedExternalID: v1.ExternalID{ExternalID: []string{string(obj.GetUID())}, ConfigType: ConfigTypePrefix + obj.GetKind()},
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

				globalLabels["aws/account-id"] = accountID

				if clusterScrapeResult, ok := cluster.Config.(map[string]any); ok {
					clusterScrapeResult["aws-auth"] = cm
					clusterScrapeResult["account-id"] = accountID
				}
			}
		}

		tags["cluster"] = config.ClusterName
		tags["apiVersion"] = obj.GetAPIVersion()
		if obj.GetNamespace() != "" {
			tags["namespace"] = obj.GetNamespace()
		}

		if obj.GetKind() == "Service" {
			if spec, ok := obj.Object["spec"].(map[string]any); ok {
				if serviceType, ok := spec["type"].(string); ok {
					tags["service-type"] = serviceType

					if serviceType == "LoadBalancer" {
						if status, ok := obj.Object["status"].(map[string]any); ok {
							if lb, ok := status["loadBalancer"].(map[string]any); ok {
								if ingresses, ok := lb["ingress"].([]any); ok {
									for _, ing := range ingresses {
										if ingress, ok := ing.(map[string]any); ok {
											if hostname, ok := ingress["hostname"].(string); ok && hostname != "" {
												tags["hostname"] = hostname
											}

											if ip, ok := ingress["ip"].(string); ok && ip != "" {
												tags["ip"] = ip
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
		var status, description string
		if healthStatus, err := health.GetResourceHealth(obj, nil); err == nil && healthStatus != nil {
			status = string(healthStatus.Status)
			description = healthStatus.Message
		}

		createdAt := obj.GetCreationTimestamp().Time
		var deletedAt *time.Time
		var deleteReason v1.ConfigDeleteReason
		if !obj.GetDeletionTimestamp().IsZero() {
			deletedAt = &obj.GetDeletionTimestamp().Time
			deleteReason = v1.DeletedReasonFromAttribute
		}

		// Evicted Pods must be considered deleted
		if obj.GetKind() == "Pod" && status == string(health.HealthStatusDegraded) {
			objStatus := obj.Object["status"].(map[string]any)
			if val, ok := objStatus["reason"].(string); ok && val == "Evicted" {
				// Use time.Now() as default and try to parse the evict time
				timeNow := time.Now()
				deletedAt = &timeNow
				if evictTime, err := time.Parse(time.RFC3339, objStatus["startTime"].(string)); err != nil {
					deletedAt = &evictTime
				}
			}
		}

		configObj, err := cleanKubernetesObject(obj.Object)
		if err != nil {
			return results.Errorf(err, "failed to clean kubernetes object")
		}

		parentType, parentExternalID := getKubernetesParent(obj, resourceIDMap)
		results = append(results, v1.ScrapeResult{
			BaseScraper:         config.BaseScraper,
			Name:                obj.GetName(),
			Namespace:           obj.GetNamespace(),
			ConfigClass:         obj.GetKind(),
			Type:                ConfigTypePrefix + obj.GetKind(),
			Status:              status,
			Description:         description,
			CreatedAt:           &createdAt,
			DeletedAt:           deletedAt,
			DeleteReason:        deleteReason,
			Config:              configObj,
			ConfigID:            lo.ToPtr(string(obj.GetUID())),
			ID:                  string(obj.GetUID()),
			Tags:                stripLabels(tags, "-hash"),
			Aliases:             getKubernetesAlias(obj),
			ParentExternalID:    parentExternalID,
			ParentType:          ConfigTypePrefix + parentType,
			RelationshipResults: relationships,
		})
	}

	results = append(results, changeResults...)
	if withCluster {
		results = append([]v1.ScrapeResult{cluster}, results...)
	}

	for i := range results {
		results[i].Tags = collections.MergeMap(map[string]string(results[i].Tags), globalLabels)

		switch results[i].Type {
		case ConfigTypePrefix + "Node":
			results[i].Tags = collections.MergeMap(map[string]string(results[i].Tags), labelsForAllNode)
			if l, ok := labelsPerNode[results[i].Name]; ok {
				results[i].Tags = collections.MergeMap(map[string]string(results[i].Tags), l)
			}
		}
	}

	return results
}

func getKubernetesParent(obj *unstructured.Unstructured, resourceIDMap map[string]map[string]map[string]string) (string, string) {
	var parentExternalID, parentConfigType string

	// This will work for pods and replicasets
	if len(obj.GetOwnerReferences()) > 0 {
		ref := obj.GetOwnerReferences()[0]
		if obj.GetKind() == "Pod" {
			// We want pod's parents as Deployments
			if ref.Kind == "ReplicaSet" {
				deployName := extractDeployNameFromReplicaSet(ref.Name)
				parentConfigType = "Deployment"
				parentExternalID = resourceIDMap[obj.GetNamespace()]["Deployment"][deployName]
				return parentConfigType, parentExternalID
			}
		}
		parentConfigType = ref.Kind
		parentExternalID = string(ref.UID)
		return parentConfigType, parentExternalID
	}

	if obj.GetNamespace() != "" {
		parentConfigType = "Namespace"
		parentExternalID = resourceIDMap[""]["Namespace"][obj.GetNamespace()]
		return parentConfigType, parentExternalID
	}

	// Everything which is not namespaced should be mapped to cluster
	parentConfigType = "Cluster"
	parentExternalID = resourceIDMap[""]["Cluster"]["selfRef"]
	return parentConfigType, parentExternalID
}

func getKubernetesAlias(obj *unstructured.Unstructured) []string {
	return []string{strings.Join([]string{"Kubernetes", obj.GetKind(), obj.GetNamespace(), obj.GetName()}, "/")}
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
