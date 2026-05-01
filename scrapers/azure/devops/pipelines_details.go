package devops

import (
	"time"

	"github.com/flanksource/duty/types"
)

// runStateToStatus maps ADO Run.State / Run.Result to the canonical
// types.Status used by types.PipelineRun. Completed runs use their Result,
// in-progress runs report Running, and cancelling maps to Running as well
// (the cancel has been requested but the run is still active).
func runStateToStatus(state, result string) types.Status {
	switch state {
	case RunStateInProgress:
		return types.StatusRunning
	case RunStateCancelling:
		return types.StatusRunning
	case RunStateCompleted:
		switch result {
		case RunResultSucceeded:
			return types.StatusCompleted
		case RunResultFailed:
			return types.StatusFailed
		case RunResultCanceled:
			return types.StatusFailed
		case RunResultTimedOut:
			return types.StatusTimeout
		}
	}
	return types.StatusPending
}

// pipelineApprovalStatusToTyped maps an ADO ApprovalStep.Status ("approved",
// "rejected", "pending", ...) to the canonical ApprovalStatus.
func pipelineApprovalStatusToTyped(status string) types.ApprovalStatus {
	switch status {
	case "approved":
		return types.ApprovalStatusApproved
	case "rejected":
		return types.ApprovalStatusRejected
	case "pending":
		return types.ApprovalStatusPending
	}
	return ""
}

// buildPipelineRunDetail constructs the canonical PipelineRun envelope for a
// pipeline run. The run's unique run ID is carried on Event.ID and the web
// URL on Event.URL; template parameters and user variables are flattened into
// Event.Properties so downstream consumers can filter on them.
func buildPipelineRunDetail(run Run, externalChangeID string) types.PipelineRun {
	return types.PipelineRun{
		Event: types.Event{
			ID:         externalChangeID,
			URL:        run.Links["web"].Href,
			Timestamp:  run.CreatedDate.UTC().Format(time.RFC3339),
			Properties: pipelineRunProperties(run),
			Tags:       run.GetTags(),
		},
		Status: runStateToStatus(run.State, run.Result),
	}
}

func pipelineRunProperties(run Run) map[string]string {
	out := map[string]string{}
	if run.State != "" {
		out["state"] = run.State
	}
	if run.Result != "" {
		out["result"] = run.Result
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
