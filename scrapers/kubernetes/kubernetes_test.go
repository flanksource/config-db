package kubernetes

import (
	"testing"
)

func Test_extractAccountIDFromARN(t *testing.T) {
	type args struct {
		input string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "xx",
			args: args{input: `- groups:\n  - system:masters\n  rolearn: arn:aws:iam::123456789:role/kubernetes-admin\n  username: admin\n- groups:\n  - system:bootstrappers\n  - system:nodes\n  rolearn: arn:aws:iam::123456789:role/eksctl-mission-control-demo-clust-NodeInstanceRole-VRLF7VBIVK3M\n  username: system:node:{{EC2PrivateDNSName}}\n`},
			want: "123456789",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractAccountIDFromARN(tt.args.input); got != tt.want {
				t.Errorf("extractAccountIDFromARN() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_extractAzureSubscriptionIDFromProvider(t *testing.T) {
	tests := []struct {
		name       string
		provider   string
		subID      string
		scaleSetID string
	}{
		{
			name:       "valid",
			provider:   "azure:///subscriptions/3da0f5ee-405a-4dd4-a408-a635799995ea/resourceGroups/mc_demo_demo_francecentral/providers/Microsoft.Compute/virtualMachineScaleSets/aks-pool1-37358073-vmss/virtualMachines/9",
			subID:      "3da0f5ee-405a-4dd4-a408-a635799995ea",
			scaleSetID: "aks-pool1-37358073-vmss",
		},
		{
			name:       "invalid",
			provider:   "aws:///subscriptions/3da0f5ee-405a-4dd4-a408-a635799995ea/resourceGroups/mc_demo_demo_francecentral/providers/Microsoft.Compute/virtualMachineScaleSets/aks-pool1-37358073-vmss/virtualMachines/9",
			subID:      "",
			scaleSetID: "",
		},
		{
			name:       "absent scale set",
			provider:   "azure:///subscriptions/3da0f5ee-405a-4dd4-a408-a635799995ea/resourceGroups/mc_demo_demo_francecentral/providers/",
			subID:      "3da0f5ee-405a-4dd4-a408-a635799995ea",
			scaleSetID: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subID, scaleSetID := parseAzureURI(tt.provider)
			if subID != tt.subID {
				t.Errorf("got = %v, want %v", subID, tt.subID)
			}

			if scaleSetID != tt.scaleSetID {
				t.Errorf("got = %v, want %v", scaleSetID, tt.scaleSetID)
			}
		})
	}
}
