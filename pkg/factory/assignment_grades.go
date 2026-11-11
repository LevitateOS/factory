package factory

// Grades of a finished assignment's result. Each method is a pure
// conjunction of the stated stored fields; none adds validation.
//
// The grades nest: Finished, Succeeded, Report, PhaseReport, PhaseOutput.
// They agree with the Reason-based grade (SucceededReported) because of a
// fact established where outcomes are derived (control dispatch_result.go,
// mcp_settle.go, mcp_fw40.go): Succeeded is only ever recorded with reason
// harness_reported, and a reported "blocked" status is recorded as
// needs-human with reason harness_no_report. Callers that test less than
// Report rely on that fact; the methods do not check it.

// Finished reports Stage == AssignmentFinished.
func (a Assignment) Finished() bool { return a.Stage == AssignmentFinished }

// Succeeded reports Finished && Outcome == Succeeded.
func (a Assignment) Succeeded() bool { return a.Finished() && a.Outcome == Succeeded }

// SucceededReported reports Succeeded && Reason == AssignReasonReported.
func (a Assignment) SucceededReported() bool {
	return a.Succeeded() && a.Reason == AssignReasonReported
}

// Report returns the result when Succeeded && Result != nil &&
// Result.Reported.
func (a Assignment) Report() (*AssignmentResult, bool) {
	if !a.Succeeded() || a.Result == nil || !a.Result.Reported {
		return nil, false
	}
	return a.Result, true
}

// PhaseReport returns the phase result when Report && PhaseResult != nil.
func (a Assignment) PhaseReport() (*PhaseResult, bool) {
	r, ok := a.Report()
	if !ok || r.PhaseResult == nil {
		return nil, false
	}
	return r.PhaseResult, true
}

// PhaseOutput returns the typed output when PhaseReport && Output != nil.
func (a Assignment) PhaseOutput() (*PhaseOutput, bool) {
	r, ok := a.PhaseReport()
	if !ok || r.Output == nil {
		return nil, false
	}
	return r.Output, true
}

// PlanOutcome is how a planning (decompose) assignment's recorded attempt
// reads to a status view or a plan choice.
type PlanOutcome string

const (
	PlanPending   PlanOutcome = "pending"
	PlanFailed    PlanOutcome = "failed"
	PlanCancelled PlanOutcome = "cancelled"
	PlanCompleted PlanOutcome = "completed"
	PlanAttention PlanOutcome = "attention"
)

// PlanOutcome classifies the attempt: pending until finished with a
// result; failed or cancelled by outcome; completed when the result is
// reported with a phase result whose status is "completed" (the output may
// still be absent: callers decide what that means); attention otherwise.
// The result is non-nil for every value except PlanPending.
func (a Assignment) PlanOutcome() PlanOutcome {
	switch {
	case !a.Finished() || a.Result == nil:
		return PlanPending
	case a.Outcome == Failed:
		return PlanFailed
	case a.Outcome == Cancelled:
		return PlanCancelled
	case a.Result.Reported && a.Result.PhaseResult != nil && a.Result.PhaseResult.Status == "completed":
		return PlanCompleted
	default:
		return PlanAttention
	}
}
