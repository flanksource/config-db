package kubernetes

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Jeffail/gabs/v2"
	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty/context"

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

func (kubernetes KubernetesScraper) IncrementalScrape(ctx api.ScrapeContext, config v1.Kubernetes, resources []*v1.InvolvedObject) v1.ScrapeResults {
	logger.Debugf("Scraping %d resources", len(resources))

	var objects []*unstructured.Unstructured
	for _, resource := range resources {
		logger.WithValues("name", resource.Name).WithValues("namespace", resource.Namespace).WithValues("kind", resource.Kind).Debugf("ketOne")
		obj, err := ketall.KetOne(ctx, resource.Name, resource.Namespace, resource.Kind, options.NewDefaultCmdOptions())
		if err != nil {
			logger.Errorf("failed to get resource (Kind=%s, Name=%s, Namespace=%s): %v", resource.Kind, resource.Name, resource.Namespace, err)
			continue
		} else if obj == nil {
			logger.WithValues("name", resource.Name).WithValues("namespace", resource.Namespace).WithValues("kind", resource.Kind).Debugf("resource from event not found")
			continue
		}

		objects = append(objects, obj)
	}

	logger.Debugf("Found %d objects for %d ids", len(objects), len(resources))

	return extractResults(ctx.DutyContext(), config, objects)
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

		// Add Cluster object first
		clusterID := "Kubernetes/Cluster/" + config.ClusterName
		results = append(results, v1.ScrapeResult{
			BaseScraper: config.BaseScraper,
			Name:        config.ClusterName,
			ConfigClass: "Cluster",
			Type:        ConfigTypePrefix + "Cluster",
			Config:      make(map[string]any),
			ID:          clusterID,
		})

		extracted := extractResults(ctx.DutyContext(), config, objs)
		results = append(results, extracted...)
	}

	return results
}

func extractResults(ctx context.Context, config v1.Kubernetes, objs []*unstructured.Unstructured) v1.ScrapeResults {
	var (
		results       v1.ScrapeResults
		changeResults v1.ScrapeResults
	)

	clusterID := "Kubernetes/Cluster/" + config.ClusterName

	resourceIDMap := getResourceIDsFromObjs(objs)
	resourceIDMap[""]["Cluster"] = make(map[string]string)
	resourceIDMap[""]["Cluster"][config.ClusterName] = clusterID
	resourceIDMap[""]["Cluster"]["selfRef"] = clusterID // For shorthand

	// Labels that are added to the kubernetes nodes once all the objects are visited
	var labelsToAddToNode = map[string]map[string]string{}

	for _, obj := range objs {
		if string(obj.GetUID()) == "" {
			logger.Warnf("Found kubernetes object with no resource ID: %s/%s/%s", obj.GetKind(), obj.GetNamespace(), obj.GetName())
			continue
		}

		if obj.GetKind() == "Event" {
			var event v1.KubernetesEvent
			if err := event.FromObjMap(obj.Object); err != nil {
				logger.Errorf("failed to parse event: %v", err)
				return nil
			}

			if event.InvolvedObject == nil {
				continue
			}

			if config.Event.Exclusions.Filter(event) {
				logger.WithValues("name", event.InvolvedObject.Name).WithValues("namespace", event.InvolvedObject.Namespace).WithValues("reason", event.Reason).Debugf("excluding event object")
				continue
			}

			change := getChangeFromEvent(event, config.Event.SeverityKeywords)
			if change != nil {
				changeResults = append(changeResults, v1.ScrapeResult{
					Changes: []v1.ChangeResult{*change},
				})
			}

			// this is all we need from an event object
			continue
		}

		var relationships v1.RelationshipResults
		if obj.GetKind() == "Pod" {
			spec := obj.Object["spec"].(map[string]interface{})
			var nodeName string
			if spec["nodeName"] != nil {
				nodeName = spec["nodeName"].(string)
				nodeID := resourceIDMap[""]["Node"][nodeName]
				relationships = append(relationships, v1.RelationshipResult{
					ConfigExternalID: v1.ExternalID{
						ExternalID: []string{string(obj.GetUID())},
						ConfigType: ConfigTypePrefix + "Pod",
					},
					RelatedExternalID: v1.ExternalID{
						ExternalID: []string{nodeID},
						ConfigType: ConfigTypePrefix + "Node",
					},
					Relationship: "NodePod",
				})
			}

			if obj.GetLabels()["app.kubernetes.io/name"] == "aws-node" {
				for _, ownerRef := range obj.GetOwnerReferences() {
					if ownerRef.Kind == "DaemonSet" && ownerRef.Name == "aws-node" {
						var (
							awsRoleARN  = getContainerEnv(spec, "AWS_ROLE_ARN")
							vpcID       = getContainerEnv(spec, "VPC_ID")
							clusterName = getContainerEnv(spec, "CLUSTER_NAME")
							awsRegion   = getContainerEnv(spec, "AWS_REGION")
						)

						if awsRoleARN != "" {
							relationships = append(relationships, v1.RelationshipResult{
								ConfigExternalID:  v1.ExternalID{ExternalID: []string{string(obj.GetUID())}, ConfigType: ConfigTypePrefix + "Pod"},
								RelatedExternalID: v1.ExternalID{ExternalID: []string{awsRoleARN}, ConfigType: "AWS::IAM::Role"},
								Relationship:      "IAMRoleNode",
							})
						}

						if vpcID != "" {
							labelsToAddToNode[nodeName] = map[string]string{
								"vpc-id": vpcID,
							}

							if clusterScrapeResult, ok := results[0].Config.(map[string]any); ok {
								clusterScrapeResult["vpc-id"] = vpcID
							}

							relationships = append(relationships, v1.RelationshipResult{
								ConfigExternalID:  v1.ExternalID{ExternalID: []string{string(obj.GetUID())}, ConfigType: ConfigTypePrefix + "Pod"},
								RelatedExternalID: v1.ExternalID{ExternalID: []string{fmt.Sprintf("Kubernetes/Node//%s", nodeName)}, ConfigType: ConfigTypePrefix + "Node"},
								Relationship:      "NodePod",
							})
						}

						if clusterName != "" {
							if clusterScrapeResult, ok := results[0].Config.(map[string]any); ok {
								clusterScrapeResult["cluster-name"] = clusterName
							}

							awsAccountID := extractAccountIDFromARN(awsRoleARN)
							if awsAccountID != "" && awsRegion != "" {
								relationships = append(relationships, v1.RelationshipResult{
									ConfigExternalID:  v1.ExternalID{ExternalID: []string{clusterID}, ConfigType: ConfigTypePrefix + "Cluster"},
									RelatedExternalID: v1.ExternalID{ExternalID: []string{fmt.Sprintf("arn:aws:eks:%s:%s:cluster/%s", awsRegion, awsAccountID, clusterName)}, ConfigType: "AWS::EKS::Cluster"},
									Relationship:      "EKSClusterKubernetesCluster",
								})
							}
						}
					}
				}
			}
		}

		if obj.GetKind() == "Node" {
			if clusterName, ok := obj.GetLabels()["alpha.eksctl.io/cluster-name"]; ok {
				if clusterScrapeResult, ok := results[0].Config.(map[string]any); ok {
					clusterScrapeResult["cluster-name"] = clusterName
				}

				relationships = append(relationships, v1.RelationshipResult{
					ConfigExternalID: v1.ExternalID{
						ExternalID: []string{string(obj.GetUID())},
						ConfigType: ConfigTypePrefix + "Node",
					},
					RelatedExternalID: v1.ExternalID{
						ExternalID: []string{clusterName},
						ConfigType: "AWS::EKS::Cluster",
					},
					Relationship: "EKSClusterNode",
				})
			}

			if spec, ok := obj.Object["spec"].(map[string]any); ok {
				if providerID, ok := spec["providerID"].(string); ok {
					// providerID is expected to be in the format "aws:///eu-west-1a/i-06ec81231075dd597"
					splits := strings.Split(providerID, "/")
					if strings.HasPrefix(providerID, "aws:///") && len(splits) > 0 && strings.HasPrefix(splits[len(splits)-1], "i-") {
						ec2InstanceID := splits[len(splits)-1]
						relationships = append(relationships, v1.RelationshipResult{
							ConfigExternalID:  v1.ExternalID{ExternalID: []string{string(obj.GetUID())}, ConfigType: ConfigTypePrefix + "Node"},
							RelatedExternalID: v1.ExternalID{ExternalID: []string{ec2InstanceID}, ConfigType: "AWS::EC2::Instance"},
							Relationship:      "EC2InstanceNode",
						})
					}
				}
			}
		}

		if obj.GetNamespace() != "" {
			relationships = append(relationships, v1.RelationshipResult{
				ConfigExternalID: v1.ExternalID{
					ExternalID: []string{string(obj.GetUID())},
					ConfigType: ConfigTypePrefix + obj.GetKind(),
				},
				RelatedExternalID: v1.ExternalID{
					ExternalID: []string{fmt.Sprintf("Kubernetes/Namespace//%s", obj.GetNamespace())},
					ConfigType: ConfigTypePrefix + "Namespace",
				},
				Relationship: "Namespace" + obj.GetKind(),
			})
		}

		for _, ownerRef := range obj.GetOwnerReferences() {
			rel := v1.RelationshipResult{
				ConfigExternalID: v1.ExternalID{
					ExternalID: []string{string(obj.GetUID())},
					ConfigType: ConfigTypePrefix + obj.GetKind(),
				},
				RelatedExternalID: v1.ExternalID{
					ExternalID: []string{string(ownerRef.UID)},
					ConfigType: ConfigTypePrefix + ownerRef.Kind,
				},
				Relationship: ownerRef.Kind + obj.GetKind(),
			}
			relationships = append(relationships, rel)
		}

		for _, f := range config.Relationships {
			env := map[string]any{}
			if spec, ok := obj.Object["spec"].(map[string]any); ok {
				env["spec"] = spec
			} else {
				env["spec"] = map[string]any{}
			}

			kind, err := f.Kind.Eval(obj.GetLabels(), env)
			if err != nil {
				return results.Errorf(err, "failed to evaluate kind: %v for config relationship", f.Kind)
			}

			name, err := f.Name.Eval(obj.GetLabels(), env)
			if err != nil {
				return results.Errorf(err, "failed to evaluate name: %v for config relationship", f.Name)
			}

			namespace, err := f.Namespace.Eval(obj.GetLabels(), env)
			if err != nil {
				return results.Errorf(err, "failed to evaluate namespace: %v for config relationship", f.Namespace)
			}

			linkedConfigItemIDs, err := db.FindConfigIDsByNamespaceNameClass(ctx, namespace, name, kind)
			if err != nil {
				return results.Errorf(err, "failed to get linked config items: name=%s, namespace=%s", name, namespace)
			}

			for _, id := range linkedConfigItemIDs {
				rel := v1.RelationshipResult{
					ConfigExternalID: v1.ExternalID{
						ExternalID: []string{string(obj.GetUID())},
						ConfigType: ConfigTypePrefix + obj.GetKind(),
					},
					RelatedConfigID: id.String(),
					Relationship:    kind + obj.GetKind(),
				}
				relationships = append(relationships, rel)
			}
		}

		if obj.GetKind() == "ConfigMap" && obj.GetName() == "aws-auth" {
			cm, ok := obj.Object["data"].(map[string]any)
			if ok {
				var accountID string
				if mapRolesYAML, ok := cm["mapRoles"].(string); ok {
					accountID = extractAccountIDFromARN(mapRolesYAML)
				}

				if clusterScrapeResult, ok := results[0].Config.(map[string]any); ok {
					clusterScrapeResult["aws-auth"] = cm
					clusterScrapeResult["account-id"] = accountID
				}
			}
		}

		tags := make(map[string]string)
		if obj.GetLabels() != nil {
			tags = obj.GetLabels()
		}
		tags["cluster"] = config.ClusterName
		tags["apiVersion"] = obj.GetAPIVersion()
		if obj.GetNamespace() != "" {
			tags["namespace"] = obj.GetNamespace()
		}
		tags["cluster"] = config.ClusterName

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
			Config:              cleanKubernetesObject(obj.Object),
			ID:                  string(obj.GetUID()),
			Tags:                stripLabels(tags, "-hash"),
			Aliases:             getKubernetesAlias(obj),
			ParentExternalID:    parentExternalID,
			ParentType:          ConfigTypePrefix + parentType,
			RelationshipResults: relationships,
		})
	}

	if len(labelsToAddToNode) != 0 {
		for i := range results {
			if labels, ok := labelsToAddToNode[results[i].Name]; ok {
				results[i].Tags = collections.MergeMap(map[string]string(results[i].Tags), labels)
			}
		}
	}

	results = append(results, changeResults...)
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
		val, err := ctx.GetEnvValueFromCache(*config.Kubeconfig)
		if err != nil {
			return nil, err
		}

		opts.GenericCliFlags.KubeConfig = &val
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
func cleanKubernetesObject(obj map[string]any) string {
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

	return o.String()
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
