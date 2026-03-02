package devops

// Run state constants matching ADO API values
const (
	RunStateInProgress = "inProgress"
	RunStateCancelling = "cancelling"
	RunStateCompleted  = "completed"

	RunResultSucceeded = "succeeded"
	RunResultFailed    = "failed"
	RunResultCanceled  = "canceled"
	RunResultTimedOut  = "timedOut"
)

// ChangeType values for pipeline run lifecycle states
const (
	ChangeTypeInProgress      = "InProgress"
	ChangeTypePendingApproval = "PendingApproval"
	ChangeTypeCancelling      = "Cancelling"
	ChangeTypeSucceeded       = "Succeeded"
	ChangeTypeFailed          = "Failed"
	ChangeTypeCancelled       = "Cancelled"
	ChangeTypeTimedOut        = "TimedOut"
)

// terminalChangeTypes is the set of ChangeType values that represent terminal states.
var terminalChangeTypes = map[string]struct{}{
	ChangeTypeSucceeded: {},
	ChangeTypeFailed:    {},
	ChangeTypeCancelled: {},
	ChangeTypeTimedOut:  {},
}

func isTerminalRun(run Run) bool {
	if run.State != RunStateCompleted {
		return false
	}
	switch run.Result {
	case RunResultSucceeded, RunResultFailed, RunResultCanceled, RunResultTimedOut:
		return true
	}
	return false
}

// runChangeType returns the ChangeType for a run, given whether it has pending approvals.
func runChangeType(run Run, hasPendingApproval bool) string {
	switch run.State {
	case RunStateCancelling:
		return ChangeTypeCancelling
	case RunStateCompleted:
		switch run.Result {
		case RunResultSucceeded:
			return ChangeTypeSucceeded
		case RunResultFailed:
			return ChangeTypeFailed
		case RunResultCanceled:
			return ChangeTypeCancelled
		case RunResultTimedOut:
			return ChangeTypeTimedOut
		}
	case RunStateInProgress:
		if hasPendingApproval {
			return ChangeTypePendingApproval
		}
		return ChangeTypeInProgress
	}
	return ChangeTypeInProgress
}
