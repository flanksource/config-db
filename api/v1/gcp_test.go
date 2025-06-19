package v1

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGCP_Includes(t *testing.T) {
	tests := []struct {
		name     string
		config   GCP
		feature  string
		expected bool
	}{
		{
			name:     "empty include list - audit logs enabled for backwards compatibility",
			config:   GCP{},
			feature:  IncludeAuditLogs,
			expected: true,
		},
		{
			name:     "empty include list - IAM policy enabled for backwards compatibility",
			config:   GCP{},
			feature:  IncludeIAMPolicy,
			expected: true,
		},
		{
			name: "include list with only audit logs",
			config: GCP{
				Include: []string{IncludeAuditLogs},
			},
			feature:  IncludeAuditLogs,
			expected: true,
		},
		{
			name: "include list with multiple features",
			config: GCP{
				Include: []string{IncludeIAMPolicy, IncludeAuditLogs},
			},
			feature:  IncludeIAMPolicy,
			expected: true,
		},
		{
			name: "case-insensitive feature matching - mixed case",
			config: GCP{
				Include: []string{"AsSets", "AuDitLoGs"},
			},
			feature:  "auditlogs",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.Includes(tt.feature)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGCP_GetAssetTypes(t *testing.T) {
	tests := []struct {
		name     string
		config   GCP
		expected []string
	}{
		{
			name:     "empty asset types and include - returns empty",
			config:   GCP{},
			expected: nil,
		},
		{
			name: "include with asset types",
			config: GCP{
				Include: []string{"storage.googleapis.com/Bucket", "compute.googleapis.com/Instance"},
			},
			expected: []string{"storage.googleapis.com/Bucket", "compute.googleapis.com/Instance"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GetAssetTypes()
			assert.Equal(t, tt.expected, result)
		})
	}
}
