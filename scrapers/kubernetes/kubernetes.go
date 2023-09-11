package kubernetes

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Jeffail/gabs/v2"
	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
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

type ItemID struct {
	Name      string
	Namespace string
	Kind      string
}

func (t ItemID) Encode() string {
	return fmt.Sprintf("%s/%s/%s", t.Name, t.Namespace, t.Kind)
}

func DecodeItemID(encoded string) ItemID {
	parts := strings.Split(encoded, "/")
	return ItemID{
		Name:      parts[0],
		Namespace: parts[1],
		Kind:      parts[2],
	}
}

func (kubernetes KubernetesScraper) ScrapeSome(ctx *v1.ScrapeContext, configIndex int, ids []string) v1.ScrapeResults {
	config := ctx.ScrapeConfig.Spec.Kubernetes[configIndex]
	var objects []*unstructured.Unstructured
	for _, id := range ids {
		itemID := DecodeItemID(id)
		obj, err := ketall.KetOne(ctx, itemID.Name, itemID.Namespace, itemID.Kind, options.NewDefaultCmdOptions())
		if err != nil {
			logger.Errorf("failed to get resource (Kind=%s, Name=%s, Namespace=%s): %v", itemID.Kind, itemID.Name, itemID.Namespace, err)
			continue
		} else if obj == nil {
			logger.Debugf("resource not found (Kind=%s, Name=%s, Namespace=%s)", itemID.Kind, itemID.Name, itemID.Namespace)
			continue
		}

		objects = append(objects, obj)
	}

	logger.Debugf("Found %d objects for %d ids", len(objects), len(ids))

	return extractResults(config, objects)
}

func (kubernetes KubernetesScraper) Scrape(ctx *v1.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults

	for _, config := range ctx.ScrapeConfig.Spec.Kubernetes {
		if config.ClusterName == "" {
			logger.Fatalf("clusterName missing from kubernetes configuration")
		}

		opts := options.NewDefaultCmdOptions()
		opts = updateOptions(opts, config)
		objs := ketall.KetAll(opts)

		// Add Cluster object first
		clusterID := "Kubernetes/Cluster/" + config.ClusterName
		results = append(results, v1.ScrapeResult{
			BaseScraper: config.BaseScraper,
			Name:        config.ClusterName,
			ConfigClass: "Cluster",
			Type:        ConfigTypePrefix + "Cluster",
			Config:      make(map[string]string),
			ID:          clusterID,
		})

		extracted := extractResults(config, objs)
		results = append(results, extracted...)
	}

	return results
}

func extractResults(config v1.Kubernetes, objs []*unstructured.Unstructured) v1.ScrapeResults {
	var (
		results       v1.ScrapeResults
		changeResults v1.ScrapeResults
	)

	clusterID := "Kubernetes/Cluster/" + config.ClusterName

	resourceIDMap := getResourceIDsFromObjs(objs)
	resourceIDMap[""]["Cluster"] = make(map[string]string)
	resourceIDMap[""]["Cluster"][config.ClusterName] = clusterID
	resourceIDMap[""]["Cluster"]["selfRef"] = clusterID // For shorthand

	for _, obj := range objs {
		if string(obj.GetUID()) == "" {
			logger.Warnf("Found kubernetes object with no resource ID: %s/%s/%s", obj.GetKind(), obj.GetNamespace(), obj.GetName())
			continue
		}

		if obj.GetKind() == "Event" {
			var event Event
			if err := event.FromObjMap(obj.Object); err != nil {
				logger.Errorf("failed to parse event: %v", err)
				return nil
			}

			if utils.MatchItems(event.Reason, config.Event.Exclusions...) {
				logger.Debugf("excluding event object for reason [%s].", event.Reason)
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
			if spec["nodeName"] != nil {
				nodeName := spec["nodeName"].(string)
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
		}

		obj.SetManagedFields(nil)
		annotations := obj.GetAnnotations()
		if annotations != nil {
			delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
		}
		obj.SetAnnotations(annotations)
		metadata := obj.Object["metadata"].(map[string]interface{})
		tags := make(map[string]interface{})
		if metadata["labels"] != nil {
			tags = metadata["labels"].(map[string]interface{})
		}
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
			Config:              cleanKubernetesObject(obj.Object),
			ID:                  string(obj.GetUID()),
			Tags:                stripLabels(convertStringInterfaceMapToStringMap(tags), "-hash"),
			Aliases:             getKubernetesAlias(obj),
			ParentExternalID:    parentExternalID,
			ParentType:          ConfigTypePrefix + parentType,
			RelationshipResults: relationships,
		})
	}

	results = append(results, changeResults...)
	return results
}

func convertStringInterfaceMapToStringMap(input map[string]interface{}) map[string]string {
	output := make(map[string]string)
	for key, value := range input {
		output[key] = fmt.Sprintf("%v", value)
	}
	return output
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

func updateOptions(opts *options.KetallOptions, config v1.Kubernetes) *options.KetallOptions {
	opts.AllowIncomplete = config.AllowIncomplete
	opts.Namespace = config.Namespace
	opts.Scope = config.Scope
	opts.Selector = config.Selector
	opts.FieldSelector = config.FieldSelector
	opts.UseCache = config.UseCache
	opts.MaxInflight = config.MaxInflight
	opts.Exclusions = config.Exclusions
	opts.Since = config.Since
	//TODO: update kubeconfig reference if provided by user
	// if config.Kubeconfig != nil {
	// 	opts.Kubeconfig = config.Kubeconfig.GetValue()
	// }
	return opts
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
