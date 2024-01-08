package kubernetes

import "testing"

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
