package kubernetes

import (
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

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

type Azure struct{}

func init() {
	onObjectHooks = append(onObjectHooks, Azure{})
}

func (azure Azure) OnObject(ctx *KubernetesContext, obj *unstructured.Unstructured) (bool, map[string]string, error) {
	labels := make(map[string]string)
	if obj.GetKind() == "Node" {
		if clusterName, ok := obj.GetLabels()["kubernetes.azure.com/cluster"]; ok {
			// kubernetes.azure.com/cluster doesn't actually contain the
			// AKS cluster name - it contains the node resource group.
			// The cluster name isn't available in the node.
			ctx.cluster.Labels["aks-nodeResourceGroup"] = clusterName
		}

		if spec, ok := obj.Object["spec"].(map[string]interface{}); ok {
			if providerID, ok := spec["providerID"].(string); ok {
				subID, vmScaleSetID := parseAzureURI(providerID)
				if subID != "" {
					ctx.globalLabels["azure/subscription-id"] = subID
				}
				if vmScaleSetID != "" {
					labels["azure/vm-scale-set"] = vmScaleSetID
				}
			}
		}
	}
	return false, labels, nil
}
