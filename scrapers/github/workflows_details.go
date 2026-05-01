package github

import (
	"time"

	"github.com/flanksource/duty/types"
)

// workflowRunStatus maps GitHub workflow run status+conclusion to a canonical
// types.Status. Non-completed runs are reported as Running (GitHub uses
// "queued" / "in_progress" / "waiting" here). Completed runs use the
// conclusion.
func workflowRunStatus(status, conclusion string) types.Status {
	if status != "completed" {
		return types.StatusRunning
	}
	switch conclusion {
	case "success":
		return types.StatusCompleted
	case "failure":
		return types.StatusFailed
	case "cancelled":
		return types.StatusFailed
	case "timed_out":
		return types.StatusTimeout
	case "skipped", "neutral", "stale":
		return types.StatusCompleted
	}
	return types.StatusPending
}

// buildWorkflowPipelineRun builds the canonical PipelineRun envelope for a
// GitHub Actions run. The run's HTML URL is carried on Event.URL and the head
// branch / run number are folded into Event.Properties so downstream
// consumers can filter without peeking into details.raw.
func buildWorkflowPipelineRun(run Run, externalChangeID string) types.PipelineRun {
	return types.PipelineRun{
		Event: types.Event{
			ID:         externalChangeID,
			URL:        run.GetHTMLURL(),
			Timestamp:  run.GetCreatedAt().UTC().Format(time.RFC3339),
			Properties: workflowRunProperties(run),
		},
		Status: workflowRunStatus(run.GetStatus(), run.GetConclusion()),
	}
}

func workflowRunProperties(run Run) map[string]string {
	out := map[string]string{}
	if branch := run.GetHeadBranch(); branch != "" {
		out["branch"] = branch
	}
	if sha := run.GetHeadSHA(); sha != "" {
		out["head_sha"] = sha
	}
	if event := run.GetEvent(); event != "" {
		out["trigger"] = event
	}
	if conclusion := run.GetConclusion(); conclusion != "" {
		out["conclusion"] = conclusion
	}
	if status := run.GetStatus(); status != "" {
		out["status"] = status
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
