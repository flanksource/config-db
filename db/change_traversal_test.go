package db

import (
	gocontext "context"
	"fmt"
	"testing"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
	dutycontext "github.com/flanksource/duty/context"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestScrapeContext() api.ScrapeContext {
	ctx := dutycontext.NewContext(gocontext.Background())
	return api.NewScrapeContext(ctx)
}

func insertTestConfigItems(ctx api.ScrapeContext, items ...models.ConfigItem) {
	for _, item := range items {
		ctx.TempCache().Insert(item)
	}
}

func TestFindAncestor(t *testing.T) {
	project := models.ConfigItem{
		ID:   "project-1",
		Type: "AzureDevops::Project",
		Name: lo.ToPtr("MyProject"),
	}
	pipeline := models.ConfigItem{
		ID:       "pipeline-1",
		Type:     "AzureDevops::Pipeline",
		Name:     lo.ToPtr("DeployPipeline"),
		ParentID: lo.ToPtr("project-1"),
	}
	pipelineRun := models.ConfigItem{
		ID:       "run-1",
		Type:     "AzureDevops::PipelineRun",
		Name:     lo.ToPtr("Run #42"),
		ParentID: lo.ToPtr("pipeline-1"),
	}

	tests := []struct {
		name         string
		source       *models.ConfigItem
		ancestorType string
		expectID     string
		expectNil    bool
	}{
		{
			name:         "find ancestor by type - skips intermediate",
			source:       &pipelineRun,
			ancestorType: "AzureDevops::Project",
			expectID:     "project-1",
		},
		{
			name:         "find immediate parent by type",
			source:       &pipelineRun,
			ancestorType: "AzureDevops::Pipeline",
			expectID:     "pipeline-1",
		},
		{
			name:     "no type - returns immediate parent",
			source:   &pipelineRun,
			expectID: "pipeline-1",
		},
		{
			name:         "no matching ancestor type",
			source:       &pipelineRun,
			ancestorType: "Kubernetes::Namespace",
			expectNil:    true,
		},
		{
			name:      "root config - no parent",
			source:    &project,
			expectNil: true,
		},
		{
			name:      "nil source",
			source:    nil,
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := newTestScrapeContext()
			insertTestConfigItems(ctx, project, pipeline, pipelineRun)

			result, err := findAncestor(ctx, tt.source, tt.ancestorType)
			require.NoError(t, err)

			if tt.expectNil {
				assert.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.Equal(t, tt.expectID, result.ID)
			}
		})
	}
}

func TestFindAncestorDepthLimit(t *testing.T) {
	ctx := newTestScrapeContext()

	for i := 0; i <= maxTraversalDepth+5; i++ {
		ci := models.ConfigItem{
			ID:   fmt.Sprintf("item-%d", i),
			Type: "Generic::Item",
			Name: lo.ToPtr(fmt.Sprintf("Item %d", i)),
		}
		if i > 0 {
			ci.ParentID = lo.ToPtr(fmt.Sprintf("item-%d", i-1))
		}
		ctx.TempCache().Insert(ci)
	}

	root := models.ConfigItem{
		ID:   "item-0",
		Type: "Generic::Root",
		Name: lo.ToPtr("Root"),
	}
	ctx.TempCache().Insert(root)

	leaf := models.ConfigItem{
		ID:       fmt.Sprintf("item-%d", maxTraversalDepth+5),
		Type:     "Generic::Item",
		ParentID: lo.ToPtr(fmt.Sprintf("item-%d", maxTraversalDepth+4)),
	}

	result, err := findAncestor(ctx, &leaf, "Generic::Root")
	require.NoError(t, err)
	assert.Nil(t, result, "should not find root beyond depth limit")
}

func TestProcessMoveUpCopyUp(t *testing.T) {
	namespace := models.ConfigItem{
		ID:   "ns-1",
		Type: "Kubernetes::Namespace",
		Name: lo.ToPtr("production"),
	}
	deployment := models.ConfigItem{
		ID:       "deploy-1",
		Type:     "Kubernetes::Deployment",
		Name:     lo.ToPtr("my-app"),
		ParentID: lo.ToPtr("ns-1"),
	}
	pod := models.ConfigItem{
		ID:       "pod-1",
		Type:     "Kubernetes::Pod",
		Name:     lo.ToPtr("my-app-abc123"),
		ParentID: lo.ToPtr("deploy-1"),
	}

	t.Run("move-up redirects change to ancestor", func(t *testing.T) {
		ctx := newTestScrapeContext()
		insertTestConfigItems(ctx, namespace, deployment, pod)

		result := &v1.ScrapeResult{
			Changes: []v1.ChangeResult{
				{
					ChangeType:       "diff",
					ExternalChangeID: "change-1",
					Action:           v1.MoveUp,
					AncestorType:     "Kubernetes::Namespace",
				},
			},
		}

		additional := processMoveUpCopyUp(ctx, result, &pod)
		assert.Empty(t, additional)
		assert.Equal(t, "ns-1", result.Changes[0].ConfigID)
		assert.Equal(t, v1.ChangeAction(""), result.Changes[0].Action)
	})

	t.Run("move-up without type goes to immediate parent", func(t *testing.T) {
		ctx := newTestScrapeContext()
		insertTestConfigItems(ctx, namespace, deployment, pod)

		result := &v1.ScrapeResult{
			Changes: []v1.ChangeResult{
				{
					ChangeType:       "diff",
					ExternalChangeID: "change-2",
					Action:           v1.MoveUp,
				},
			},
		}

		additional := processMoveUpCopyUp(ctx, result, &pod)
		assert.Empty(t, additional)
		assert.Equal(t, "deploy-1", result.Changes[0].ConfigID)
	})

	t.Run("copy-up creates duplicate on ancestor", func(t *testing.T) {
		ctx := newTestScrapeContext()
		insertTestConfigItems(ctx, namespace, deployment, pod)

		result := &v1.ScrapeResult{
			Changes: []v1.ChangeResult{
				{
					ChangeType:       "diff",
					ExternalChangeID: "change-3",
					Summary:          "something changed",
					Action:           v1.CopyUp,
					AncestorType:     "Kubernetes::Namespace",
				},
			},
		}

		additional := processMoveUpCopyUp(ctx, result, &pod)

		assert.Equal(t, v1.ChangeAction(""), result.Changes[0].Action)
		assert.Equal(t, "", result.Changes[0].ConfigID)

		require.Len(t, additional, 1)
		assert.Equal(t, "ns-1", additional[0].ConfigID)
		assert.Equal(t, "something changed", additional[0].Summary)
		assert.Equal(t, "change-3:copy-up:ns-1", additional[0].ExternalChangeID)
	})

	t.Run("move-up with no matching ancestor leaves change unchanged", func(t *testing.T) {
		ctx := newTestScrapeContext()
		insertTestConfigItems(ctx, namespace, deployment, pod)

		result := &v1.ScrapeResult{
			Changes: []v1.ChangeResult{
				{
					ChangeType:       "diff",
					ExternalChangeID: "change-4",
					Action:           v1.MoveUp,
					AncestorType:     "NonExistent::Type",
				},
			},
		}

		additional := processMoveUpCopyUp(ctx, result, &pod)
		assert.Empty(t, additional)
		assert.Equal(t, "", result.Changes[0].ConfigID)
		assert.Equal(t, v1.ChangeAction(""), result.Changes[0].Action)
	})

	t.Run("move-up on root config with no parent", func(t *testing.T) {
		ctx := newTestScrapeContext()
		insertTestConfigItems(ctx, namespace)

		result := &v1.ScrapeResult{
			Changes: []v1.ChangeResult{
				{
					ChangeType:       "diff",
					ExternalChangeID: "change-5",
					Action:           v1.MoveUp,
				},
			},
		}

		additional := processMoveUpCopyUp(ctx, result, &namespace)
		assert.Empty(t, additional)
		assert.Equal(t, "", result.Changes[0].ConfigID)
	})

	t.Run("non move-up/copy-up actions are not affected", func(t *testing.T) {
		ctx := newTestScrapeContext()
		insertTestConfigItems(ctx, namespace, deployment, pod)

		result := &v1.ScrapeResult{
			Changes: []v1.ChangeResult{
				{ChangeType: "diff", Action: v1.Ignore},
				{ChangeType: "diff", Action: v1.Delete},
				{ChangeType: "diff", Action: ""},
			},
		}

		additional := processMoveUpCopyUp(ctx, result, &pod)
		assert.Empty(t, additional)
		assert.Equal(t, v1.Ignore, result.Changes[0].Action)
		assert.Equal(t, v1.Delete, result.Changes[1].Action)
		assert.Equal(t, v1.ChangeAction(""), result.Changes[2].Action)
	})
}

func TestApplyCopyMove(t *testing.T) {
	t.Run("copy to single target", func(t *testing.T) {
		change := &v1.ChangeResult{
			ChangeType:       "Deployment",
			ExternalChangeID: "change-1",
			Summary:          "deployed v2",
			Action:           v1.Copy,
		}

		additional := applyCopyMove(change, []string{"target-1"}, v1.Copy)

		assert.Equal(t, v1.ChangeAction(""), change.Action)
		assert.Equal(t, "", change.ConfigID)
		require.Len(t, additional, 1)
		assert.Equal(t, "target-1", additional[0].ConfigID)
		assert.Equal(t, "change-1:copy:target-1", additional[0].ExternalChangeID)
		assert.Equal(t, "deployed v2", additional[0].Summary)
	})

	t.Run("move to single target", func(t *testing.T) {
		change := &v1.ChangeResult{
			ChangeType:       "Deployment",
			ExternalChangeID: "change-2",
			Summary:          "deployed v3",
			Action:           v1.Move,
		}

		additional := applyCopyMove(change, []string{"target-1"}, v1.Move)

		assert.Equal(t, v1.ChangeAction(""), change.Action)
		assert.Equal(t, "target-1", change.ConfigID)
		assert.Empty(t, additional)
	})

	t.Run("move to multiple targets", func(t *testing.T) {
		change := &v1.ChangeResult{
			ChangeType:       "Deployment",
			ExternalChangeID: "change-3",
			Action:           v1.Move,
		}

		additional := applyCopyMove(change, []string{"target-1", "target-2", "target-3"}, v1.Move)

		assert.Equal(t, "target-1", change.ConfigID)
		assert.Equal(t, v1.ChangeAction(""), change.Action)

		require.Len(t, additional, 2)
		assert.Equal(t, "target-2", additional[0].ConfigID)
		assert.Equal(t, "change-3:copy:target-2", additional[0].ExternalChangeID)
		assert.Equal(t, "target-3", additional[1].ConfigID)
		assert.Equal(t, "change-3:copy:target-3", additional[1].ExternalChangeID)
	})

	t.Run("copy to multiple targets", func(t *testing.T) {
		change := &v1.ChangeResult{
			ChangeType:       "Deployment",
			ExternalChangeID: "change-4",
			Action:           v1.Copy,
		}

		additional := applyCopyMove(change, []string{"target-1", "target-2"}, v1.Copy)

		assert.Equal(t, v1.ChangeAction(""), change.Action)
		assert.Equal(t, "", change.ConfigID)

		require.Len(t, additional, 2)
		assert.Equal(t, "target-1", additional[0].ConfigID)
		assert.Equal(t, "change-4:copy:target-1", additional[0].ExternalChangeID)
		assert.Equal(t, "target-2", additional[1].ConfigID)
		assert.Equal(t, "change-4:copy:target-2", additional[1].ExternalChangeID)
	})

	t.Run("empty targets returns nil", func(t *testing.T) {
		change := &v1.ChangeResult{
			ChangeType: "Deployment",
			Action:     v1.Copy,
		}

		additional := applyCopyMove(change, nil, v1.Copy)
		assert.Empty(t, additional)
		assert.Equal(t, v1.ChangeAction(""), change.Action)
	})

	t.Run("external change ID uniqueness", func(t *testing.T) {
		change := &v1.ChangeResult{
			ChangeType:       "Deployment",
			ExternalChangeID: "original",
			Action:           v1.Copy,
		}

		additional := applyCopyMove(change, []string{"t1", "t2", "t3"}, v1.Copy)

		seen := make(map[string]bool)
		for _, a := range additional {
			assert.False(t, seen[a.ExternalChangeID], "duplicate ExternalChangeID: %s", a.ExternalChangeID)
			seen[a.ExternalChangeID] = true
		}
		assert.Len(t, seen, 3)
	})
}
