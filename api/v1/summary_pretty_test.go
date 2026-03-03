package v1

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrettyShort(t *testing.T) {
	tests := []struct {
		name     string
		summary  ScrapeSummary
		expected string
	}{
		{
			name:     "empty summary",
			summary:  ScrapeSummary{},
			expected: "no changes",
		},
		{
			name: "configs only",
			summary: ScrapeSummary{
				ConfigTypes: map[string]ConfigTypeScrapeSummary{
					"AWS::EC2::Instance": {Added: 5, Updated: 12, Unchanged: 100},
				},
			},
			expected: "configs(+5/~12/=100)",
		},
		{
			name: "configs with changes and deduped",
			summary: ScrapeSummary{
				ConfigTypes: map[string]ConfigTypeScrapeSummary{
					"AWS::EC2::Instance": {Added: 5, Updated: 12, Unchanged: 100, Changes: 8, Deduped: 3},
				},
			},
			expected: "configs(+5/~12/=100) changes(+8/dedup=3)",
		},
		{
			name: "full summary with entities",
			summary: ScrapeSummary{
				ConfigTypes: map[string]ConfigTypeScrapeSummary{
					"AWS::EC2::Instance": {Added: 1},
				},
				ExternalUsers:  EntitySummary{Scraped: 50, Saved: 50},
				ExternalGroups: EntitySummary{Scraped: 10, Saved: 10},
				ExternalRoles:  EntitySummary{Scraped: 5, Saved: 5},
				ConfigAccess:   EntitySummary{Scraped: 200, Saved: 200},
				AccessLogs:     EntitySummary{Scraped: 1000, Saved: 1000},
			},
			expected: "configs(+1/~0/=0) users=50 groups=10 roles=5 access=200 logs=1000",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.summary.PrettyShort())
		})
	}
}

func TestUnmarshalJSON_BackwardCompat(t *testing.T) {
	t.Run("old map format", func(t *testing.T) {
		oldJSON := `{
			"AWS::EC2::Instance": {"added": 3, "updated": 5, "unchanged": 10},
			"Kubernetes::Pod": {"added": 1}
		}`
		var s ScrapeSummary
		require.NoError(t, json.Unmarshal([]byte(oldJSON), &s))
		assert.Equal(t, 3, s.ConfigTypes["AWS::EC2::Instance"].Added)
		assert.Equal(t, 5, s.ConfigTypes["AWS::EC2::Instance"].Updated)
		assert.Equal(t, 10, s.ConfigTypes["AWS::EC2::Instance"].Unchanged)
		assert.Equal(t, 1, s.ConfigTypes["Kubernetes::Pod"].Added)
	})

	t.Run("new struct format", func(t *testing.T) {
		newJSON := `{
			"config_types": {
				"AWS::EC2::Instance": {"added": 3, "updated": 5}
			},
			"external_users": {"scraped": 50, "saved": 48, "skipped": 2}
		}`
		var s ScrapeSummary
		require.NoError(t, json.Unmarshal([]byte(newJSON), &s))
		assert.Equal(t, 3, s.ConfigTypes["AWS::EC2::Instance"].Added)
		assert.Equal(t, 5, s.ConfigTypes["AWS::EC2::Instance"].Updated)
		assert.Equal(t, 50, s.ExternalUsers.Scraped)
		assert.Equal(t, 48, s.ExternalUsers.Saved)
		assert.Equal(t, 2, s.ExternalUsers.Skipped)
	})

	t.Run("roundtrip new format", func(t *testing.T) {
		original := ScrapeSummary{
			ConfigTypes: map[string]ConfigTypeScrapeSummary{
				"AWS::EC2::Instance": {Added: 3, Changes: 10, Deduped: 2},
			},
			ExternalUsers: EntitySummary{Scraped: 50, Saved: 48},
		}
		data, err := json.Marshal(original)
		require.NoError(t, err)

		var decoded ScrapeSummary
		require.NoError(t, json.Unmarshal(data, &decoded))
		assert.Equal(t, original.ConfigTypes["AWS::EC2::Instance"], decoded.ConfigTypes["AWS::EC2::Instance"])
		assert.Equal(t, original.ExternalUsers, decoded.ExternalUsers)
	})
}

func TestMergeWithChangesAndDeduped(t *testing.T) {
	a := ConfigTypeScrapeSummary{Added: 1, Updated: 2, Changes: 5, Deduped: 3}
	b := ConfigTypeScrapeSummary{Added: 3, Updated: 4, Changes: 10, Deduped: 7}
	merged := a.Merge(b)
	assert.Equal(t, 4, merged.Added)
	assert.Equal(t, 6, merged.Updated)
	assert.Equal(t, 15, merged.Changes)
	assert.Equal(t, 10, merged.Deduped)
}

func TestScrapeSummaryMerge(t *testing.T) {
	a := ScrapeSummary{
		ConfigTypes: map[string]ConfigTypeScrapeSummary{
			"AWS::EC2::Instance": {Added: 1, Changes: 5},
		},
		ExternalUsers: EntitySummary{Scraped: 10, Saved: 8, Deleted: 1},
	}
	b := ScrapeSummary{
		ConfigTypes: map[string]ConfigTypeScrapeSummary{
			"AWS::EC2::Instance": {Added: 2, Changes: 3},
			"Kubernetes::Pod":    {Added: 5},
		},
		ExternalUsers: EntitySummary{Scraped: 20, Saved: 18, Deleted: 2},
	}
	a.Merge(b)

	assert.Equal(t, 3, a.ConfigTypes["AWS::EC2::Instance"].Added)
	assert.Equal(t, 8, a.ConfigTypes["AWS::EC2::Instance"].Changes)
	assert.Equal(t, 5, a.ConfigTypes["Kubernetes::Pod"].Added)
	assert.Equal(t, 30, a.ExternalUsers.Scraped)
	assert.Equal(t, 26, a.ExternalUsers.Saved)
	assert.Equal(t, 3, a.ExternalUsers.Deleted)
}

func TestEntitySummary_IsEmpty(t *testing.T) {
	assert.True(t, EntitySummary{}.IsEmpty())
	assert.False(t, EntitySummary{Scraped: 1}.IsEmpty())
	assert.False(t, EntitySummary{Deleted: 1}.IsEmpty())
}

func TestTotalsWithNewFields(t *testing.T) {
	s := ScrapeSummary{
		ConfigTypes: map[string]ConfigTypeScrapeSummary{
			"A": {Added: 1, Updated: 2, Unchanged: 3, Changes: 10, Deduped: 5},
			"B": {Added: 4, Updated: 5, Unchanged: 6, Changes: 20, Deduped: 8},
		},
	}
	totals := s.Totals()
	assert.Equal(t, 5, totals.Added)
	assert.Equal(t, 7, totals.Updated)
	assert.Equal(t, 9, totals.Unchanged)
	assert.Equal(t, 30, totals.Changes)
	assert.Equal(t, 13, totals.Deduped)
}

func TestHasUpdates(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		assert.False(t, ScrapeSummary{}.HasUpdates())
	})
	t.Run("config updates", func(t *testing.T) {
		s := ScrapeSummary{ConfigTypes: map[string]ConfigTypeScrapeSummary{"A": {Added: 1}}}
		assert.True(t, s.HasUpdates())
	})
	t.Run("entity updates only", func(t *testing.T) {
		s := ScrapeSummary{ExternalUsers: EntitySummary{Saved: 5}}
		assert.True(t, s.HasUpdates())
	})
}
