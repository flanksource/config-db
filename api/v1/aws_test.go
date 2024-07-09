package v1

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAWS_Includes(t *testing.T) {
	tests := []struct {
		name     string
		config   AWS
		resource string
		want     bool
	}{
		{
			name:     "empty include list, not in default exclusions",
			config:   AWS{},
			resource: "ec2",
			want:     true,
		},
		{
			name:     "empty include list, in default exclusions",
			config:   AWS{},
			resource: "ECSTASKDEFINITION",
			want:     false,
		},
		{
			name:     "explicit inclusion of default exclusion",
			config:   AWS{Include: []string{"EcsTaskDefinition"}},
			resource: "ECSTASKDEFINITION",
			want:     true,
		},
		{
			name: "non-empty include list, resource included",
			config: AWS{
				Include: []string{"s3", "ec2", "rds"},
			},
			resource: "ec2",
			want:     true,
		},
		{
			name: "non-empty include list, resource not included",
			config: AWS{
				Include: []string{"s3", "ec2", "rds"},
			},
			resource: "lambda",
			want:     false,
		},
		{
			name: "case-insensitive include",
			config: AWS{
				Include: []string{"S3", "EC2", "RDS"},
			},
			resource: "ec2",
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.Includes(tt.resource)
			assert.Equal(t, tt.want, got)
		})
	}
}
