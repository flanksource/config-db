package azure

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAzure(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Azure Suite")
}

var _ = Describe("extractResourceGroup", func() {
	DescribeTable("extracts resource group from Azure resource ID",
		func(input, expected string) {
			Expect(extractResourceGroup(input)).To(Equal(expected))
		},
		Entry("simple resource group",
			"/subscriptions/0cd017bb-aa54-5121-b21f-ecf8daee0624/resourcegroups/mc_demo_demo_francecentral",
			"mc_demo_demo_francecentral"),
		Entry("crossplane",
			"/subscriptions/0cd017bb-aa54-5121-b21f-ecf8daee0624/resourcegroups/crossplane",
			"crossplane"),
		Entry("with nested provider path",
			"/subscriptions/0cd017bb-aa54-5121-b21f-ecf8daee0624/resourcegroups/crossplane/providers/microsoft.containerservice/managedclusters/workload-prod-eu-01",
			"crossplane"),
		Entry("storage account",
			"/subscriptions/0cd017bb-aa54-5121-b21f-ecf8daee0624/resourcegroups/internal-prod/providers/microsoft.storage/storageaccounts/flanksourcebackups",
			"internal-prod"),
		Entry("load balancer",
			"/subscriptions/0cd017bb-aa54-5121-b21f-ecf8daee0624/resourcegroups/mc_crossplane_crossplane-master_northeurope/providers/microsoft.network/loadbalancers/kubernetes",
			"mc_crossplane_crossplane-master_northeurope"),
		Entry("empty string", "", ""),
		Entry("no resource group segment", "/subscriptions/123", ""),
		Entry("wrong segment name", "/subscriptions/456/notresourcegroup/test", ""),
	)
})
