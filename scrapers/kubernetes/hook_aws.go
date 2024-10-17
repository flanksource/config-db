package kubernetes

import (
	"regexp"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var arnRegexp = regexp.MustCompile(`arn:aws:iam::(\d+):role/`)

func extractAccountIDFromARN(input string) string {
	matches := arnRegexp.FindStringSubmatch(input)
	if len(matches) >= 2 {
		return matches[1]
	}

	return ""
}

type AWS struct{}

func init() {
	onObjectHooks = append(onObjectHooks, AWS{})
}

func (aws AWS) OnObject(ctx *KubernetesContext, obj *unstructured.Unstructured) (bool, map[string]string, error) {
	if obj.GetKind() == "ConfigMap" && obj.GetName() == "aws-auth" {
		cm, ok := obj.Object["data"].(map[string]any)
		if ok {
			var accountID string
			if mapRolesYAML, ok := cm["mapRoles"].(string); ok {
				accountID = extractAccountIDFromARN(mapRolesYAML)
			}

			ctx.cluster.Tags.Append("account", accountID)

			if clusterScrapeResult, ok := ctx.cluster.Config.(map[string]any); ok {
				clusterScrapeResult["aws-auth"] = cm
				clusterScrapeResult["account-id"] = accountID
			}
		}
	}
	if obj.GetKind() == "Pod" && obj.GetLabels()["app.kubernetes.io/name"] == "aws-node" {
		nodeName := getString(obj, "spec", "nodeName")
		spec := obj.Object["spec"].(map[string]interface{})
		for _, ownerRef := range obj.GetOwnerReferences() {
			if ownerRef.Kind == "DaemonSet" && ownerRef.Name == "aws-node" {
				var (
					awsRoleARN     = getContainerEnv(spec, "AWS_ROLE_ARN")
					vpcID          = getContainerEnv(spec, "VPC_ID")
					awsClusterName = getContainerEnv(spec, "CLUSTER_NAME")
				)

				ctx.labelsPerNode[nodeName] = make(map[string]string)

				if awsRoleARN != "" {
					ctx.labelsPerNode[nodeName]["aws/iam-role"] = awsRoleARN
				}

				if vpcID != "" {
					ctx.labelsForAllNode["aws/vpc-id"] = vpcID

					if clusterScrapeResult, ok := ctx.cluster.Config.(map[string]any); ok {
						clusterScrapeResult["vpc-id"] = vpcID
					}
				}

				if awsClusterName != "" {
					if clusterScrapeResult, ok := ctx.cluster.Config.(map[string]any); ok {
						clusterScrapeResult["cluster-name"] = awsClusterName
					}
				}
			}
		}
	}
	return false, nil, nil
}
