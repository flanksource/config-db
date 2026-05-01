package cmd

import (
	"encoding/json"
	"testing"

	"github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/cmd/scrapeui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUISnapshotReadsSnapshotShape(t *testing.T) {
	input := scrapeui.Snapshot{
		Results: v1.FullScrapeResults{
			Configs: []v1.ScrapeResult{{ID: "pod-1", Name: "pod-1", Type: "Kubernetes::Pod"}},
		},
		Logs:       "saved logs",
		ScrapeSpec: map[string]any{"kind": "snapshot"},
		SaveSummary: &scrapeui.SaveSummary{
			ConfigTypes: map[string]scrapeui.TypeSummary{"Kubernetes::Pod": {Added: 1}},
		},
		Snapshots: map[string]*v1.ScrapeSnapshotPair{"pods": {}},
	}
	data, err := json.Marshal(input)
	require.NoError(t, err)

	got, err := parseUISnapshot(data)
	require.NoError(t, err)

	require.Len(t, got.Results.Configs, 1)
	assert.Equal(t, "saved logs", got.Logs)
	assert.Equal(t, input.ScrapeSpec, got.ScrapeSpec)
	assert.Equal(t, input.SaveSummary, got.SaveSummary)
	assert.Equal(t, input.Snapshots, got.Snapshots)
}

func TestParseUISnapshotReadsLegacyFlatShape(t *testing.T) {
	input := jsonResults{
		Configs: []v1.ScrapeResult{{ID: "pod-1", Name: "pod-1", Type: "Kubernetes::Pod"}},
		HAR:     nil,
	}
	data, err := json.Marshal(input)
	require.NoError(t, err)

	got, err := parseUISnapshot(data)
	require.NoError(t, err)

	require.Len(t, got.Results.Configs, 1)
	assert.Equal(t, "pod-1", got.Results.Configs[0].ID)
	assert.Empty(t, got.Logs)
	assert.Nil(t, got.ScrapeSpec)
}
