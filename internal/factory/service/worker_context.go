package service

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/control"
)

// WorkerTerminalSource reads the existing proposal journal without settling it.
type WorkerTerminalSource interface {
	TerminalMCPProposal(context.Context, string) (factory.MCPWorkStatus, bool, error)
}

// WorkerContext reads already authenticated/admitted worker context. Transport
// owns per-call capability admission/rate slots; sources supply canonical records.
// Loading or projecting context never judges, dispatches or changes a journal.
type WorkerContext struct {
	Terminal    WorkerTerminalSource
	Assignments WorkerAssignmentSource
	Runs        WorkerRunSource
	Priority    WorkerPriorityJudgmentSource
	Current     WorkerCurrentContextSource
	Now         func() time.Time
}

// WorkerContextRead is internal application input, not a response containing raw
// assignment/prompt data. Projection exposes only the existing worker contract.
type WorkerContextRead struct {
	Capability   factory.MCPCapability
	Terminal     factory.MCPWorkStatus
	Frozen       bool
	Assignment   factory.Assignment
	Parent       factory.Assignment
	ParentLoaded bool
}

func (s WorkerContext) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Load retains the existing private-parent visibility and incomplete lineage.
func (s WorkerContext) Load(ctx context.Context, capability factory.MCPCapability) (WorkerContextRead, error) {
	read := WorkerContextRead{Capability: capability}
	if s.Terminal == nil || s.Assignments == nil {
		return WorkerContextRead{}, errors.New("worker context sources unavailable")
	}
	var err error
	read.Terminal, read.Frozen, err = s.Terminal.TerminalMCPProposal(ctx, capability.AssignmentID)
	if err != nil {
		return WorkerContextRead{}, err
	}
	read.Assignment, err = s.Assignments.Assignment(ctx, capability.AssignmentID)
	if err != nil {
		return WorkerContextRead{}, err
	}
	if read.Assignment.ParentAssignment != "" {
		if parent, err := s.Assignments.Assignment(ctx, read.Assignment.ParentAssignment); err == nil && factory.ContextParentVisible(read.Assignment, parent) {
			read.Parent, read.ParentLoaded = parent, true
		}
	}
	if err := ctx.Err(); err != nil {
		return WorkerContextRead{}, err
	}
	return read, nil
}

// WorkerAssignmentSource loads the recorded assignment behind a
// capability for current-context projection. *store.Store implements
// it; tests stub it.
type WorkerAssignmentSource interface {
	Assignment(ctx context.Context, id string) (factory.Assignment, error)
}

// WorkerPriorityJudgmentSource reads the prepared context choice.
// *store.Store implements it; tests stub it.
type WorkerPriorityJudgmentSource interface {
	PriorityJudgment(context.Context, string) (factory.PriorityReceipt, error)
}

// WorkerRunSource loads recorded run rows behind an assignment
// for bounded evidence projection. *store.Store implements it;
// tests stub it.
type WorkerRunSource interface {
	FactoryRun(ctx context.Context, id string) (factory.Run, error)
}

type WorkerCurrentContextSource interface {
	FactoryCurrentContext(context.Context, factory.MCPCapability, time.Time) (factory.CurrentContext, error)
}

// Foreground is read-only. The classifier's recorded choice picks
// the foregrounded history; a missing, failed or unsure choice serves current
// context rather than holding the worker.
func (s WorkerContext) Foreground(ctx context.Context, read WorkerContextRead) (string, string, string) {
	attemptEligible, lineageEligible := factory.BundleEligibility(read.Assignment, read.ParentLoaded)
	if resolution := read.Capability.ContextCurrentResolution; resolution != nil {
		source := s.Current
		if source == nil {
			return factory.ContextBundleSnapshotHold, "required_current_unavailable", read.Capability.ContextSelectionSnapshot
		}
		current, err := source.FactoryCurrentContext(ctx, read.Capability, s.now())
		if err != nil || current.Evidence.Availability != "complete" {
			return factory.ContextBundleSnapshotHold, "required_current_unavailable", read.Capability.ContextSelectionSnapshot
		}
		digest, err := factory.CurrentRequiredSemanticDigest(current.Role)
		if err != nil || resolution.ValidateFor(read.Capability) != nil || digest != resolution.RequiredDigest {
			return factory.ContextBundleSnapshotHold, "context_selection_stale", read.Capability.ContextSelectionSnapshot
		}
		return factory.ContextBundleCurrent, "", read.Capability.ContextSelectionSnapshot
	}
	priority := s.Priority
	if priority == nil {
		priority, _ = s.Terminal.(WorkerPriorityJudgmentSource)
	}
	fingerprint := read.Capability.ContextSelectionSnapshot
	if priority == nil || !factory.ValidDigest(fingerprint) {
		return factory.ContextBundleCurrent, "", ""
	}
	receipt, err := priority.PriorityJudgment(ctx, fingerprint)
	if err != nil {
		return factory.ContextBundleCurrent, "", ""
	}
	bundle, reason := control.ContextBundleFromReceipt(receipt, read.Capability, read.Assignment, attemptEligible, lineageEligible)
	if bundle == factory.ContextBundleSnapshotHold || reason != "" {
		return factory.ContextBundleCurrent, "", fingerprint
	}
	return bundle, "", fingerprint
}

// Projection renders one read's projection with its eligible
// attempt and lineage sections.
func (read WorkerContextRead) Projection(foregrounded string) map[string]any {
	attemptEligible, lineageEligible := factory.BundleEligibility(read.Assignment, read.ParentLoaded)
	capability, assignment := read.Capability, read.Assignment
	serveAttempt := attemptEligible && (foregrounded == factory.ContextBundleAttempt || foregrounded == factory.ContextBundleLineage)
	serveLineage := lineageEligible && foregrounded == factory.ContextBundleLineage
	bundle := factory.ContextBundleCurrent
	body := map[string]any{
		"assignment":            capability.AssignmentID,
		"run":                   capability.RunID,
		"project":               capability.Project,
		"identity_source":       "authenticated_worker_context",
		"prompt_sha":            assignment.PromptSHA,
		"source_commit":         assignment.SourceCommit,
		"preparation":           assignment.Preparation,
		"profile":               string(capability.Profile),
		"phase":                 factory.MCPPhaseOf(capability.Profile),
		"subject":               capability.Subject,
		"repository":            capability.Repository,
		"generation":            capability.Generation,
		"capability":            capability.State,
		"deadline":              capability.DeadlineUnix,
		"terminal":              read.Frozen,
		"limits":                map[string]any{"page_bytes": factory.MCPMaxPageBytes, "page_refs": factory.MCPMaxPageRefs, "result_bytes": factory.MCPMaxResultPayloadBytes},
		"source_digest":         capability.SourceDigest,
		"scope_digest":          capability.ScopeDigest(),
		"replies_tool":          string(factory.MCPToolInbox),
		"discussion_guidance":   "Read factory.inbox before finishing. A recorded role invitation grants only its exact discussion context and one useful reply through factory.reply with parent_id set to the invited post ID. It does not expand your assignment, execution authority or budget. Skip redundant contributions, explain concrete relevant evidence, and preserve the source identity. Do not invite other roles recursively.",
		"work_post":             map[string]any{"kind": "record", "id": assignment.ID},
		"library_tools":         []string{"factory.search_library", "factory.read_library_item"},
		"library_guidance":      factory.LibraryReadGuidance,
		"consultation_guidance": "System One questions and actual results are automatically recorded in your work post. Never include credentials or private key material. For substantive decisions prepare three independent, counterbalanced questions using the same facts and alternatives before reading any answer, then make three separate calls with the same consultation_group identity. A questions batch is one call and must not be described as three independent calls. Compare the results, then reply to the returned consultation.post_id with your interpretation, decision and next action, or use factory.record_decision with that parent_id for durable decisions. Your explanation must remain distinct from JEV's actual answer. Judgments do not replace native checks or authorization. Read your inbox and effective project decisions/knowledge through the library tools before adopting a change; preserve cited item IDs/revisions and source revision.",
	}
	if assignment.Issue > 0 {
		body["issue"] = assignment.Issue
	}
	if assignment.ActorID > 0 {
		body["actor_id"] = assignment.ActorID
	}
	if serveAttempt {
		body["attempt"] = map[string]any{"run_history": factory.AttemptContinuity(assignment)}
		bundle = factory.ContextBundleAttempt
	}
	if serveLineage {
		lineage := map[string]any{"parent_assignment": assignment.ParentAssignment}
		if read.Parent.Outcome != "" {
			lineage["parent_outcome"] = string(read.Parent.Outcome)
		}
		if read.Parent.Reason != "" {
			lineage["parent_reason"] = read.Parent.Reason
		}
		body["lineage"] = lineage
		bundle = factory.ContextBundleLineage
	} else if assignment.ParentAssignment != "" {
		body["lineage_incomplete"] = true
	}
	body["bundle"] = bundle
	if read.Frozen {
		body["terminal_submission"] = read.Terminal.SubmissionID
		body["terminal_state"] = read.Terminal.State
	}
	if assignment.Outcome != "" {
		body["outcome"] = string(assignment.Outcome)
	}
	if assignment.Reason != "" {
		body["reason"] = assignment.Reason
	}
	return body
}

// Evidence projects the admitted assignment's bounded run history. Missing
// sources and rows are errors; future collections remain explicitly incomplete.
func (s WorkerContext) Evidence(ctx context.Context, capability factory.MCPCapability) (map[string]any, error) {
	if s.Assignments == nil {
		return nil, errors.New("worker assignment source unavailable")
	}
	// Bounded run projection over the recorded assignment's own
	// attempt: opaque run identities with role and outcome only —
	// never summaries, admissions, identity bindings or prompt-derived
	// content. A missing assignment refuses instead of serving
	// unscoped rows, and an unresolvable recorded run refuses rather
	// than silently dropping it. Plan/review/inventory pages plus
	// pagination arrive with FW40, so evidence stays honestly
	// incomplete.
	assignment, err := s.Assignments.Assignment(ctx, capability.AssignmentID)
	if err != nil {
		return nil, err
	}
	history := assignment.RunHistory
	truncated := false
	if len(history) > factory.MCPMaxPageRefs {
		history = history[:factory.MCPMaxPageRefs]
		truncated = true
	}
	runs := make([]any, 0, len(history))
	for _, id := range history {
		if s.Runs == nil {
			return nil, errors.New("evidence run source unwired")
		}
		run, err := s.Runs.FactoryRun(ctx, id)
		if err != nil {
			return nil, err
		}
		projected := map[string]any{"id": run.ID, "role": run.Role}
		if run.Outcome != "" {
			projected["outcome"] = string(run.Outcome)
		}
		runs = append(runs, projected)
	}
	body := map[string]any{
		"assignment": capability.AssignmentID,
		"runs":       runs,
		"complete":   false,
		"reason":     "plan/review/inventory pages plus pagination arrive with FW40",
	}
	if truncated {
		body["runs_truncated"] = true
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return body, nil
}
