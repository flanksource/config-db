package cmd

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/cmd/scrapeui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuildRunSnapshotOutputIncludesReplayFields(t *testing.T) {
	started := time.Unix(100, 0)
	rawResults := v1.ScrapeResults{{
		ID:   "pod-1",
		Name: "pod-1",
		Type: "Kubernetes::Pod",
		Config: map[string]any{
			"kind": "Pod",
		},
	}}
	all := v1.MergeScrapeResults(rawResults)
	summary := &v1.ScrapeSummary{
		ConfigTypes: map[string]v1.ConfigTypeScrapeSummary{
			"Kubernetes::Pod": {Added: 1},
		},
	}
	lastSummary := &v1.ScrapeSummary{
		ConfigTypes: map[string]v1.ConfigTypeScrapeSummary{
			"Kubernetes::Pod": {Unchanged: 3},
		},
	}
	snapshots := map[string]*v1.ScrapeSnapshotPair{
		"pods": {Diff: v1.ScrapeSnapshotDiff{}},
	}
	spec := map[string]any{"kubernetes": []any{map[string]any{"cluster": "dev"}}}

	snapshot := buildRunSnapshotOutput(
		all,
		rawResults,
		summary,
		lastSummary,
		snapshots,
		[]v1.ScrapeConfig{{ObjectMeta: metav1.ObjectMeta{Name: "pods"}}},
		[]scrapeui.ScraperProgress{{
			Name:        "pods",
			Status:      scrapeui.ScraperComplete,
			StartedAt:   &started,
			DurationSec: 1.5,
			ResultCount: 1,
		}},
		spec,
		nil,
		nil,
		"captured logs",
	)

	require.Len(t, snapshot.Results.Configs, 1)
	assert.Equal(t, "captured logs", snapshot.Logs)
	assert.Equal(t, spec, snapshot.ScrapeSpec)
	assert.Equal(t, snapshots, snapshot.Snapshots)
	require.NotNil(t, snapshot.SaveSummary)
	assert.Equal(t, 1, snapshot.SaveSummary.ConfigTypes["Kubernetes::Pod"].Added)
	assert.Equal(t, lastSummary, snapshot.LastScrapeSummary)
	assert.True(t, snapshot.Done)
	assert.Equal(t, started.UnixMilli(), snapshot.StartedAt)

	data, err := json.Marshal(snapshot)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"logs"`)
	assert.Contains(t, string(data), `"scrape_spec"`)
	assert.Contains(t, string(data), `"snapshots"`)
	assert.Contains(t, string(data), `"save_summary"`)
	assert.Contains(t, string(data), `"last_scrape_summary"`)
}
