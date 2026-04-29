package scrapeui

import (
	"testing"

	"github.com/flanksource/config-db/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStaticServerPreservesReplayFields(t *testing.T) {
	lastSummary := &v1.ScrapeSummary{
		ConfigTypes: map[string]v1.ConfigTypeScrapeSummary{
			"Kubernetes::Pod": {Unchanged: 3},
		},
	}
	input := Snapshot{
		Results: v1.FullScrapeResults{
			Configs: []v1.ScrapeResult{{ID: "pod-1", Name: "pod-1", Type: "Kubernetes::Pod"}},
		},
		Issues: []ScrapeIssue{{Type: "warning", Message: "watch permissions missing"}},
		SaveSummary: &SaveSummary{
			ConfigTypes: map[string]TypeSummary{"Kubernetes::Pod": {Added: 1}},
		},
		Snapshots:         map[string]*v1.ScrapeSnapshotPair{"pods": {}},
		ScrapeSpec:        map[string]any{"kubernetes": []any{}},
		Logs:              "saved logs",
		BuildInfo:         &BuildInfo{Version: "dev", Commit: "abc", Date: "today"},
		LastScrapeSummary: lastSummary,
	}

	got := NewStaticServer(input).snapshot()

	require.Len(t, got.Results.Configs, 1)
	assert.Equal(t, input.Issues, got.Issues)
	assert.Equal(t, input.SaveSummary, got.SaveSummary)
	assert.Equal(t, input.Snapshots, got.Snapshots)
	assert.Equal(t, input.ScrapeSpec, got.ScrapeSpec)
	assert.Equal(t, "saved logs", got.Logs)
	assert.Equal(t, input.BuildInfo, got.BuildInfo)
	assert.Equal(t, lastSummary, got.LastScrapeSummary)
	assert.True(t, got.Done)
}

