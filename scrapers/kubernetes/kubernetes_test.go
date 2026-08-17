package kubernetes

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestKubernetes(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Kubernetes Suite")
}

var _ = Describe("extractAccountIDFromARN", func() {
	DescribeTable("extracts account ID from ARN strings",
		func(input, expected string) {
			Expect(extractAccountIDFromARN(input)).To(Equal(expected))
		},
		Entry("valid ARN", `- groups:\n  - system:masters\n  rolearn: arn:aws:iam::123456789012:role/kubernetes-admin\n  username: admin\n- groups:\n  - system:bootstrappers\n  - system:nodes\n  rolearn: arn:aws:iam::123456789012:role/eksctl-mission-control-demo-clust-NodeInstanceRole-VRLF7VBIVK3M\n  username: system:node:{{EC2PrivateDNSName}}\n`, "123456789012"),
	)
})

var _ = Describe("GetConfigType", func() {
	DescribeTable("distinguishes conflicting Kubernetes kinds",
		func(apiVersion, kind, expected string) {
			obj := &unstructured.Unstructured{}
			obj.SetAPIVersion(apiVersion)
			obj.SetKind(kind)
			Expect(GetConfigType(obj)).To(Equal(expected))
		},
		Entry("core node", "v1", "Node", "Kubernetes::Node"),
		Entry("Longhorn node", "longhorn.io/v1beta2", "Node", "Kubernetes::LonghornNode"),
		Entry("CNPG cluster", "postgresql.cnpg.io/v1", "Cluster", "Kubernetes::CNPGCluster"),
		Entry("Crossplane resource", "s3.aws.upbound.io/v1beta1", "Bucket", "Crossplane::Bucket"),
		Entry("Flanksource resource", "configs.flanksource.com/v1", "ScrapeConfig", "MissionControl::ScrapeConfig"),
	)
})

var _ = Describe("parseAzureURI", func() {
	DescribeTable("extracts subscription ID and scale set ID",
		func(provider, expectedSubID, expectedScaleSetID string) {
			subID, scaleSetID := parseAzureURI(provider)
			Expect(subID).To(Equal(expectedSubID))
			Expect(scaleSetID).To(Equal(expectedScaleSetID))
		},
		Entry("valid",
			"azure:///subscriptions/3da0f5ee-405a-4dd4-a408-a635799995ea/resourceGroups/mc_demo_demo_francecentral/providers/Microsoft.Compute/virtualMachineScaleSets/aks-pool1-37358073-vmss/virtualMachines/9",
			"3da0f5ee-405a-4dd4-a408-a635799995ea",
			"aks-pool1-37358073-vmss",
		),
		Entry("invalid provider prefix",
			"aws:///subscriptions/3da0f5ee-405a-4dd4-a408-a635799995ea/resourceGroups/mc_demo_demo_francecentral/providers/Microsoft.Compute/virtualMachineScaleSets/aks-pool1-37358073-vmss/virtualMachines/9",
			"",
			"",
		),
		Entry("absent scale set",
			"azure:///subscriptions/3da0f5ee-405a-4dd4-a408-a635799995ea/resourceGroups/mc_demo_demo_francecentral/providers/",
			"3da0f5ee-405a-4dd4-a408-a635799995ea",
			"",
		),
	)
})
