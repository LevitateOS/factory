package control

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// finishFromRun finishes one assigned assignment from its settled latest
// run: a recorded MCP finish owns the result, and without one the run
// outcome alone settles it.
func finishFromRun(ctx context.Context, db *store.Store, a factory.Assignment, run factory.Run, now time.Time) error {
	current, err := db.Assignment(ctx, a.ID)
	if err != nil {
		return err
	}
	if current.Stage != factory.AssignmentAssigned {
		return nil
	}
	if handled, err := settleRecordedMCPBeforeRun(ctx, db, current, run, now); handled || err != nil {
		return err
	}
	finished, err := finishAssignmentFromRun(current, run, now)
	if err != nil {
		return err
	}
	return db.FinishAssignment(ctx, finished)
}

// A durable authenticated terminal owns the assignment result even when native
// retirement arrives before the periodic MCP drive. A pending final checkpoint
// keeps its consumer; the run end cannot replace it with an incomplete result.
func settleRecordedMCPBeforeRun(ctx context.Context, db *store.Store, a factory.Assignment, run factory.Run, now time.Time) (bool, error) {
	if run.ID != a.Run || run.ProjectID != a.ProjectID || run.Role != a.Role || run.InputSHA != a.SourceCommit {
		return false, nil
	}
	_, found, err := db.TerminalMCPProposal(ctx, a.ID)
	if err != nil || !found {
		return found, err
	}
	_, err = SettleMCPTerminalProposal(ctx, db, a.ID, now)
	return true, err
}

func finishAssignmentFromRun(a factory.Assignment, run factory.Run, now time.Time) (factory.Assignment, error) {
	if a.Stage != factory.AssignmentAssigned {
		return a, nil
	}
	result, outcome, reason := deriveUnfinishedRunResult(a, run, run.Summary, now.Unix())
	a.Stage, a.Outcome, a.Reason = factory.AssignmentFinished, outcome, reason
	a.FinishedUnix = now.Unix()
	a.Result = &result
	if err := a.Validate(); err != nil {
		return factory.Assignment{}, err
	}
	return a, nil
}

// recordConfirmedUsage appends one settled run's confirmed consumption in
// whole minutes, rounding up. The first write wins; replays reuse it.
func recordConfirmedUsage(ctx context.Context, db *store.Store, a factory.Assignment, run factory.Run, now time.Time) error {
	minutes := 0
	if elapsed := now.Sub(run.Started); elapsed > 0 {
		minutes = int((elapsed + time.Minute - time.Nanosecond) / time.Minute)
	}
	return db.RecordRunUsage(ctx, factory.Usage{
		RunID: run.ID, Repository: a.Repository, Connection: a.Connection,
		Minutes: minutes, StartedAt: run.Started.UTC(), EndedAt: now.UTC(),
	})
}

// refusePhasedAttempt maps one invalid phased run to a synthesized
// blocked result.
func refusePhasedAttempt(a factory.Assignment, recordedUnix int64, detail string) (factory.AssignmentResult, factory.Outcome, string) {
	phased := synthesizePhased(a, "blocked", detail, recordedUnix)
	return phased, factory.NeedsHuman, factory.AssignReasonNoReport
}

// deriveUnfinishedRunResult maps one settled run that recorded no MCP
// finish to its result. The finish tools are the only result channel:
// a cancelled or failed run keeps its observed outcome, and a run that
// ended cleanly without calling its finish tool settles incomplete
// (blocked for attention) with a reason naming the tool it never called.
func deriveUnfinishedRunResult(a factory.Assignment, run factory.Run, summary string, recordedUnix int64) (factory.AssignmentResult, factory.Outcome, string) {
	if run.ID != a.Run || run.Role != a.Role || run.InputSHA != a.SourceCommit {
		return refusePhasedAttempt(a, recordedUnix, "settled run does not match its assignment")
	}
	if !factory.ValidPhaseRole(a.Phase, a.Role) {
		return refusePhasedAttempt(a, recordedUnix, "assignment role does not match its execution phase")
	}
	switch run.Outcome {
	case factory.Succeeded:
	case factory.Cancelled:
		cancelled := synthesizePhased(a, "cancelled", boundSummary(summary, "run cancelled"), recordedUnix)
		return cancelled, factory.Cancelled, factory.AssignReasonCancelled
	default:
		failed := synthesizePhased(a, "failed", boundSummary(summary, "run failed"), recordedUnix)
		return failed, factory.Failed, factory.AssignReasonRunFailed
	}
	return refusePhasedAttempt(a, recordedUnix, unfinishedRunReason(a.Phase))
}

// unfinishedRunReason names the finish call a cleanly ended run never made.
func unfinishedRunReason(phase string) string {
	finish := "its finish tool"
	if profile, err := mcpProfileForPhase(phase); err == nil {
		finish = string(factory.MCPFinishToolOf(profile))
	}
	return "incomplete: the run ended without calling " + finish + " or " + string(factory.MCPToolFinishIncomplete) + ", so no result was recorded"
}

// buildPhasedCoderResult builds one successful coder run's recorded
// result from its bound phase result.
func buildPhasedCoderResult(a factory.Assignment, parsed factory.PhaseResult, recordedUnix int64) (factory.AssignmentResult, factory.Outcome, string) {
	result := factory.AssignmentResult{
		AssignmentID: a.ID, RunID: a.Run, Phase: a.Phase, PhaseResult: &parsed,
		Summary: parsed.Summary, Findings: []string{},
		Reported: true, RecordedUnix: recordedUnix,
	}
	switch parsed.Status {
	case "completed":
		result.Status = "completed"
		if a.Phase == project.ExecutionImplement && parsed.Output != nil && parsed.Output.Implementation != nil {
			result.Candidate = parsed.Output.Implementation.Candidate
		}
		// A completed correction nominates its resulting tree: the
		// retained candidate binds the report's nomination so the
		// correction consumer advances exactly the accounted tree. The
		// nomination is not checkpoint proof or publish authorization.
		if a.Phase == project.ExecutionCorrect && parsed.Output != nil && parsed.Output.Correction != nil {
			result.Candidate = parsed.Output.Correction.NewCandidate
		}
	case "blocked":
		result.Status = "blocked"
	case "failed":
		result.Status = "failed"
		return result, factory.Failed, factory.AssignReasonRunFailed
	case "cancelled":
		result.Status = "cancelled"
		return result, factory.Cancelled, factory.AssignReasonCancelled
	default:
		return refusePhasedAttempt(a, recordedUnix, "phase result carries an unknown status")
	}
	if err := result.ValidateForAssignment(a.Role, a.SourceCommit); err != nil {
		return refusePhasedAttempt(a, recordedUnix, "phase result does not match its assignment role")
	}
	if result.PhaseResult != nil {
		if err := result.ValidatePhaseBinding(a.Role, a.Phase); err != nil {
			return refusePhasedAttempt(a, recordedUnix, "phased result does not bind its assignment phase")
		}
	}
	outcome := factory.Outcome(factory.Succeeded)
	reason := factory.AssignReasonReported
	if parsed.Status == "blocked" {
		outcome, reason = factory.NeedsHuman, factory.AssignReasonNoReport
	}
	return result, outcome, reason
}

// checkDecomposeSemantic checks one reported decompose result against its
// assignment's recorded staged input. A missing or corrupt recorded input
// refuses: there is no legacy compatibility path around the staged
// contract. The staged input must answer the funded subject triple, every
// inspected source must still be the admitted source commit, and the answer
// must assess through AdmitIntentBoundResult — revision/operation binding
// plus full candidate coverage and graph validation, or attention-only
// admission.
func checkDecomposeSemantic(a factory.Assignment) error {
	in, staged, err := ParseStagedPlanningInput(a.Prompt)
	if err != nil || !staged {
		return errors.New("staged planning input is missing or corrupt")
	}
	subject := a.Subject.Intent
	if subject == nil || in.Head.Digest() != subject.ScopeDigest || in.Head.ID != subject.RevisionID ||
		in.Head.Intent != subject.IntentID || in.Head.Owner != subject.OwnerID {
		return errors.New("staged input answers another funded operation")
	}
	if a.Result == nil || a.Result.PhaseResult == nil || a.Result.PhaseResult.Output == nil ||
		a.Result.PhaseResult.Output.Decomposition == nil {
		return errors.New("staged answer carries no decomposition")
	}
	for _, ref := range in.Inspected {
		if ref.Commit != a.SourceCommit {
			return errors.New("staged answer inspects another source commit")
		}
	}
	return AdmitIntentBoundResult(a.Subject, a.Phase, in, *a.Result.PhaseResult.Output.Decomposition)
}

// synthesizePhased binds one supervisor-derived result to a phased
// assignment, carrying the typed phase result for non-completed terminal
// states. Planning and other completed states never synthesize: only a
// reported v2 payload completes a phase.
func synthesizePhased(a factory.Assignment, status, summary string, recordedUnix int64) factory.AssignmentResult {
	phased := factory.ResultSynthesized(a.ID, a.Run, status, summary, recordedUnix)
	phaseStatus := status
	switch status {
	case "failed", "cancelled", "blocked":
	default:
		phaseStatus = "blocked"
		phased.Status = "blocked"
	}
	phased.Phase, phased.PhaseResult = a.Phase, &factory.PhaseResult{
		Version: factory.PhaseResultVersion, Phase: a.Phase,
		Status: phaseStatus, Summary: summary,
	}
	if err := phased.ValidatePhaseBinding(a.Role, a.Phase); err != nil {
		legacy := factory.ResultSynthesized(a.ID, a.Run, status, summary, recordedUnix)
		return legacy
	}
	return phased
}

func boundSummary(summary, fallback string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return fallback
	}
	if len(summary) > 4096 {
		summary = summary[:4096-12] + "…[truncated]"
	}
	return summary
}

// AccountSettledRun accounts one run the supervisor just settled: it
// records confirmed usage, consumes the assignment's reservation and
// finishes the assignment from the settled outcome. Runs without an
// assignment replay silently. An MCP finish may precede native retirement;
// its usage still records without replacing that finished domain result. The
// settle path calls this best-effort; recovery replays anything it
// misses, so a false return never loses accounting.
func AccountSettledRun(ctx context.Context, db *store.Store, run factory.Run, now time.Time) (factory.Assignment, bool) {
	if db == nil || !run.Reconciled {
		return factory.Assignment{}, false
	}
	a, err := db.AssignmentByRun(ctx, run.ID)
	if err != nil {
		return factory.Assignment{}, false
	}
	if a.Stage == factory.AssignmentFinished {
		if err := db.AccountFinishedMCPRun(ctx, run.ID); err != nil {
			return factory.Assignment{}, false
		}
		// Preserve the caller contract: no new domain finish occurred.
		return factory.Assignment{}, false
	}
	if a.Stage != factory.AssignmentAssigned {
		return factory.Assignment{}, false
	}
	if err := recordConfirmedUsage(ctx, db, a, run, now); err != nil {
		return factory.Assignment{}, false
	}
	if err := db.ConsumeReservation(ctx, a.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return factory.Assignment{}, false
	}
	if handled, err := settleRecordedMCPBeforeRun(ctx, db, a, run, now); handled || err != nil {
		if err != nil {
			return factory.Assignment{}, false
		}
		finished, err := db.Assignment(ctx, a.ID)
		return finished, err == nil && finished.Stage == factory.AssignmentFinished
	}
	finished, err := finishAssignmentFromRun(a, run, now)
	if err != nil {
		return factory.Assignment{}, false
	}
	a = finished
	if err := db.FinishAssignment(ctx, a); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return a, true
		}
		return factory.Assignment{}, false
	}
	return a, true
}
