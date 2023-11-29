package kubernetes

import (
	"fmt"
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

func (kubernetes KubernetesScraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var (
		results       v1.ScrapeResults
		changeResults v1.ScrapeResults

		err error
	)

	for _, config := range ctx.ScrapeConfig().Spec.Kubernetes {
		if config.ClusterName == "" {
			logger.Fatalf("clusterName missing from kubernetes configuration")
		}

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

		opts := options.NewDefaultCmdOptions()
		opts, err = updateOptions(ctx.DutyContext(), opts, config)
		if err != nil {
			return results.Errorf(err, "error setting up kube config")
		}
		objs := ketall.KetAll(opts)

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
				reason, _ := obj.Object["reason"].(string)
				if utils.MatchItems(reason, config.Event.Exclusions...) {
					logger.Debugf("excluding event object for reason [%s].", reason)
					continue
				}

				change := getChangeFromEvent(obj, config.Event.SeverityKeywords)
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

				if kind != obj.GetKind() {
					continue // Try matching another relationship
				}

				name, err := f.Name.Eval(obj.GetLabels(), env)
				if err != nil {
					return results.Errorf(err, "failed to evaluate name: %v for config relationship", f.Name)
				}

				namespace, err := f.Namespace.Eval(obj.GetLabels(), env)
				if err != nil {
					return results.Errorf(err, "failed to evaluate namespace: %v for config relationship", f.Namespace)
				}

				linkedConfigItemIDs, err := db.FindConfigIDsByNamespaceName(ctx.DutyContext(), namespace, name)
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
				Tags:                stripLabels(convertStringInterfaceMapToStringMap(tags), "-hash"),
				Aliases:             getKubernetesAlias(obj),
				ParentExternalID:    parentExternalID,
				ParentType:          ConfigTypePrefix + parentType,
				RelationshipResults: relationships,
			})
		}
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

func updateOptions(ctx context.Context, opts *options.KetallOptions, config v1.Kubernetes) (*options.KetallOptions, error) {
	opts.AllowIncomplete = config.AllowIncomplete
	opts.Namespace = config.Namespace
	opts.Scope = config.Scope
	opts.Selector = config.Selector
	opts.FieldSelector = config.FieldSelector
	opts.UseCache = config.UseCache
	opts.MaxInflight = config.MaxInflight
	opts.Exclusions = config.Exclusions
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
