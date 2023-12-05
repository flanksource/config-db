package azure

import "testing"

func TestExtractResourceGroup(t *testing.T) {
	tests := []struct {
		input          string
		expectedOutput string
	}{
		// Valid input cases
		{"/subscriptions/0cd017bb-aa54-5121-b21f-ecf8daee0624/resourcegroups/mc_demo_demo_francecentral", "mc_demo_demo_francecentral"},
		{"/subscriptions/0cd017bb-aa54-5121-b21f-ecf8daee0624/resourcegroups/crossplane", "crossplane"},
		{"/subscriptions/0cd017bb-aa54-5121-b21f-ecf8daee0624/resourcegroups/crossplane/providers/microsoft.containerservice/managedclusters/workload-prod-eu-01", "crossplane"},
		{"/subscriptions/0cd017bb-aa54-5121-b21f-ecf8daee0624/resourcegroups/internal-prod/providers/microsoft.storage/storageaccounts/flanksourcebackups", "internal-prod"},
		{"/subscriptions/0cd017bb-aa54-5121-b21f-ecf8daee0624/resourcegroups/mc_crossplane_crossplane-master_northeurope/providers/microsoft.network/loadbalancers/kubernetes", "mc_crossplane_crossplane-master_northeurope"},

		// Invalid input cases
		{"", ""},
		{"/subscriptions/123", ""},
		{"/subscriptions/456/notresourcegroup/test", ""},
	}

	for _, test := range tests {
		result := extractResourceGroup(test.input)
		if result != test.expectedOutput {
			t.Errorf("Input: %s, Expected: %s, Got: %s", test.input, test.expectedOutput, result)
		}
	}
}
