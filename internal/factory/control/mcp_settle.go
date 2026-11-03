package control

// MCP terminal-proposal settlement.
//
// The MCP handler checks each finish with the mapping used here. The controller
// then passes the posted result to FinishAssignment and marks the post accepted.
// Builder and Fixer finishes wait for their native final checkpoint. The
// assignment's actual state decides whether a refused write was already settled.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// MCPTerminalSettleStore is the narrow settlement joint. *store.Store
// implements it; tests stub it.
type MCPTerminalSettleStore interface {
	FactoryRun(ctx context.Context, id string) (factory.Run, error)
	Assignment(ctx context.Context, id string) (factory.Assignment, error)
	TerminalMCPSubmission(ctx context.Context, assignmentID string) (factory.MCPSubmission, bool, error)
	TerminalMCPProposal(ctx context.Context, assignmentID string) (factory.MCPWorkStatus, bool, error)
	AdvanceMCPWork(ctx context.Context, assignmentID, submissionID, expectedState string, status factory.MCPWorkStatus) error
	ResolveArtifactDigest(ctx context.Context, repository int64, assignment, digest string) (string, error)
	FinishAssignment(ctx context.Context, a factory.Assignment) error
}

// checkSettledArtifactRefs resolves every artifact a finished
// verification report cites against complete artifacts of the
// finishing assignment. The read spends nothing and publishes
// nothing; an unknown, foreign or unfinished digest fences the
// finish for human attention instead of recording a bogus citation.
func checkSettledArtifactRefs(ctx context.Context, st MCPTerminalSettleStore, finished factory.Assignment) error {
	if finished.Result == nil || finished.Result.PhaseResult == nil ||
		finished.Result.PhaseResult.Output == nil || finished.Result.PhaseResult.Output.Verification == nil {
		return nil
	}
	for _, ref := range finished.Result.PhaseResult.Output.Verification.ArtifactRefs {
		if _, err := st.ResolveArtifactDigest(ctx, finished.Repository, finished.ID, ref); err != nil {
			return errors.New("verification artifact is not a complete artifact of this assignment")
		}
	}
	return nil
}

// mcpDriveStore adds the recorded-terminal work list the coordinator
// drive enumerates. *store.Store implements it; tests stub it.
type mcpDriveStore interface {
	MCPTerminalSettleStore
	RecordedTerminalMCPAssignments(ctx context.Context, limit int) ([]string, error)
}

var _ mcpDriveStore = (*store.Store)(nil)

var _ MCPTerminalSettleStore = (*store.Store)(nil)

// mcpSettleNote bounds the status reason the settler records.
const mcpSettleNoteChars = 200

// SettleMCPTerminalProposal settles one assignment's recorded terminal
// proposal. It reports settled=true only when the assignment finished
// through this proposal and the status advanced to accepted. A
// Builder or Fixer finish waits (settled=false, nil error) for its
// native final checkpoint; anything else either has no terminal yet or
// already settled.
func SettleMCPTerminalProposal(ctx context.Context, st MCPTerminalSettleStore, assignmentID string, now time.Time) (settled bool, err error) {
	if st == nil || !factory.ValidID(assignmentID) {
		return false, errors.New("mcp settlement lacks its assignment")
	}
	assigned, err := st.Assignment(ctx, assignmentID)
	if err != nil {
		return false, err
	}
	submission, ok, err := st.TerminalMCPSubmission(ctx, assignmentID)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	// A consumed status stays settled: only a recorded terminal
	// still owns its assignment finish. The FinishAssignment CAS
	// below remains the exactly-once guard against races.
	status, ok, err := st.TerminalMCPProposal(ctx, assignmentID)
	if err != nil {
		return false, err
	}
	if !ok || status.State != factory.MCPWorkRecorded {
		return false, nil
	}
	if assigned.Stage != factory.AssignmentAssigned {
		return advanceSettledTerminal(ctx, st, submission, "assignment already finished; proposal recorded for inspection")
	}
	// Admission already checked the posted result. Mapping errors propagate
	// without replacing the worker's report with a synthetic result.
	finished, note, err := mapMCPTerminalFinish(assigned, submission, now)
	if err != nil {
		return false, err
	}
	ready, refusal, err := checkpointTerminalReady(ctx, st, finished)
	if err != nil {
		return false, err
	}
	if refusal != "" {
		finished = fenceMCPTerminal(assigned, submission, now, refusal)
		note = "native final-candidate evidence refused"
	} else if !ready {
		return false, nil
	}
	// Cited artifacts revalidate before persistence: the fence only
	// shape-checked the refs, so an unknown, foreign or unfinished
	// digest fences here with the proposal preserved for inspection.
	if err := checkSettledArtifactRefs(ctx, st, finished); err != nil {
		finished = fenceMCPTerminal(assigned, submission, now, err.Error())
		note = "unresolvable verification artifact fenced for human attention"
	}
	if err := st.FinishAssignment(ctx, finished); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// factory.ErrNotFound is ambiguous: a concurrent foreign finish
			// consumes the assignment, but a missing allowance or a
			// stale input rollback refuses while the assignment stays
			// assigned. Settle only a genuinely terminal re-read;
			// still-assigned work keeps its recorded status for a
			// later pass instead of stranding as accepted.
			current, rerr := st.Assignment(ctx, assignmentID)
			if rerr != nil {
				return false, rerr
			}
			if current.Stage != factory.AssignmentFinished {
				return false, fmt.Errorf("mcp terminal finish refused: %w", err)
			}
			return advanceSettledTerminal(ctx, st, submission, "assignment finished during settlement")
		}
		return false, err
	}
	return advanceSettledTerminal(ctx, st, submission, note)
}

// advanceSettledTerminal advances one terminal status
// recorded→accepted with its settlement note. A stale guard means a
// concurrent settler already consumed the proposal.
func advanceSettledTerminal(ctx context.Context, st MCPTerminalSettleStore, submission factory.MCPSubmission, note string) (bool, error) {
	if len(note) > mcpSettleNoteChars {
		note = note[:mcpSettleNoteChars]
	}
	status := factory.MCPWorkStatus{
		SubmissionID: submission.SubmissionID, Tool: submission.Tool,
		State:  factory.MCPWorkAccepted,
		Reason: note,
	}
	if err := st.AdvanceMCPWork(ctx, submission.AssignmentID, submission.SubmissionID, factory.MCPWorkRecorded, status); err != nil {
		if errors.Is(err, store.ErrStaleRevision) || errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// mapMCPTerminalFinish maps one terminal proposal to its finished
// assignment, or refuses it with the defect the worker must correct. The
// MCP handler runs it before recording (CheckMCPTerminalFinish), so a
// recorded proposal normally maps; a mapping error remains an error.
func mapMCPTerminalFinish(assigned factory.Assignment, submission factory.MCPSubmission, now time.Time) (factory.Assignment, string, error) {
	if assigned.Stage != factory.AssignmentAssigned {
		return factory.Assignment{}, "", errors.New("the assignment is no longer assigned")
	}
	var built factory.Assignment
	var note string
	var err error
	switch submission.Tool {
	case factory.MCPToolFinishIncomplete:
		payload, derr := decodeMCPTerminalPayload(submission)
		if derr != nil {
			return factory.Assignment{}, "", fmt.Errorf("finish payload is not a JSON object: %w", derr)
		}
		built, note = settleIncomplete(assigned, submission, payload, now), "worker-reported terminal settled"
	case factory.MCPToolFinishImplement:
		var fields map[string]json.RawMessage
		if derr := json.Unmarshal(submission.Payload, &fields); derr != nil {
			return factory.Assignment{}, "", fmt.Errorf("finish payload is not a JSON object: %w", derr)
		}
		built, note, err = settleImplementation(assigned, submission, fields, now)
	default:
		built, note, err = mapFW40Terminal(assigned, submission, now)
	}
	return built, note, err
}

// CheckMCPTerminalFinish runs the settlement mapping for one new finish
// before anything is recorded. A refusal reaches the worker while it can
// still correct the payload and finish again.
func CheckMCPTerminalFinish(assigned factory.Assignment, submission factory.MCPSubmission, now time.Time) error {
	if !factory.MCPIsTerminal(submission.Tool) {
		return nil
	}
	if _, _, err := mapMCPTerminalFinish(assigned, submission, now); err != nil {
		return fmt.Errorf("%s refused, nothing recorded: %w; correct it and finish again", submission.Tool, err)
	}
	return nil
}

// decodeMCPTerminalPayload decodes one recorded terminal payload to
// its string fields. Non-string values refuse: the settler never
// coerces worker prose into settlement facts.
func decodeMCPTerminalPayload(submission factory.MCPSubmission) (map[string]string, error) {
	var raw map[string]any
	if err := json.Unmarshal(submission.Payload, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(raw))
	for key, value := range raw {
		text, isString := value.(string)
		if !isString {
			continue
		}
		out[key] = text
	}
	return out, nil
}

// mcpTerminalSummary bounds one worker text field into a result
// summary naming its tool and submission.
func mcpTerminalSummary(tool factory.MCPTool, submissionID, text, fallback string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		text = fallback
	}
	summary := "mcp " + string(tool) + " " + submissionID + ": " + text
	if len(summary) > 16<<10 {
		summary = summary[:16<<10]
	}
	return summary
}

// fenceMCPTerminal finishes one assignment as blocked for human
// attention when its terminal proposal cannot settle: the recorded
// proposal stays inspectable through the accepted status.
func fenceMCPTerminal(assigned factory.Assignment, submission factory.MCPSubmission, now time.Time, detail string) factory.Assignment {
	finished := assigned
	result := synthesizePhased(assigned, "blocked",
		mcpTerminalSummary(submission.Tool, submission.SubmissionID, detail, "terminal needs human attention"), now.Unix())
	finished.Stage, finished.Outcome, finished.Reason = factory.AssignmentFinished, factory.NeedsHuman, factory.AssignReasonNoReport
	finished.FinishedUnix = now.Unix()
	finished.Result = &result
	return finished
}

// settleIncomplete settles a worker-reported terminal state: blocked
// fences for attention, failed records the failed run, cancelled
// records the cancellation.
func settleIncomplete(assigned factory.Assignment, submission factory.MCPSubmission, payload map[string]string, now time.Time) factory.Assignment {
	finished := assigned
	var status string
	switch payload["outcome"] {
	case "failed":
		status = "failed"
		finished.Outcome, finished.Reason = factory.Failed, factory.AssignReasonRunFailed
	case "cancelled":
		status = "cancelled"
		finished.Outcome, finished.Reason = factory.Cancelled, factory.AssignReasonCancelled
	default:
		status = "blocked"
		finished.Outcome, finished.Reason = factory.NeedsHuman, factory.AssignReasonNoReport
	}
	result := factory.ResultSynthesized(assigned.ID, assigned.Run, status,
		mcpTerminalSummary(submission.Tool, submission.SubmissionID, payload["cause"], "worker reports "+status), now.Unix())
	finished.Stage = factory.AssignmentFinished
	finished.FinishedUnix = now.Unix()
	finished.Result = &result
	return finished
}

// driveMCPTerminals settles one bounded page of recorded terminal
// proposals and reports how many settled. Individual failures keep
// their recorded status for a later pass; parked proposals stay
// listed and visible. Best effort like the other progress drives.
func driveMCPTerminals(ctx context.Context, st mcpDriveStore, now time.Time) int {
	if st == nil {
		return 0
	}
	ids, err := st.RecordedTerminalMCPAssignments(ctx, MaxDispatchRecovery)
	if err != nil {
		return 0
	}
	settled := 0
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		if done, err := SettleMCPTerminalProposal(ctx, st, id, now); err == nil && done {
			settled++
		}
	}
	return settled
}

// settleMCPTerminals drives recorded MCP terminal settlement on the
// coordinator's store: the Start and progress-pass hook.
func (c *Coordinator) settleMCPTerminals(ctx context.Context) int {
	if c == nil || c.Store == nil {
		return 0
	}
	settled := driveMCPTerminals(ctx, c.Store, time.Now())
	c.driveAppVerifications(ctx)
	return settled
}
