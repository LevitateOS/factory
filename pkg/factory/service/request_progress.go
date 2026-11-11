package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/store"
)

// PlanningStatus is the minimal scoped draft-planning status for
// one current revision: pending while the run is outstanding, the
// reported shape once it settles, and none before the first attempt.
// Copy stays about planning results, never source hashes.
type PlanningStatus struct {
	Revision     string                   `json:"revision"`
	State        string                   `json:"state"`
	AssignmentID string                   `json:"assignment_id,omitempty"`
	RunID        string                   `json:"run_id,omitempty"`
	Summary      string                   `json:"summary,omitempty"`
	Candidates   int                      `json:"candidates,omitempty"`
	Attention    string                   `json:"attention,omitempty"`
	Walkthroughs *factory.ProposalContent `json:"walkthroughs,omitempty"`
	Review       *Exploration             `json:"review,omitempty"`
}

func renderIntentPlanOutcome(a factory.Assignment) PlanningStatus {
	view := PlanningStatus{AssignmentID: a.ID, RunID: a.Run}
	switch a.PlanOutcome() {
	case factory.PlanPending:
		view.State = "pending"
	case factory.PlanFailed:
		view.State, view.Summary = "failed", a.Result.Summary
	case factory.PlanCancelled:
		view.State, view.Summary = "cancelled", a.Result.Summary
	case factory.PlanCompleted:
		view.State, view.Summary = "completed", a.Result.Summary
		if out := a.Result.PhaseResult.Output; out != nil && out.Decomposition != nil {
			view.Candidates = len(out.Decomposition.Candidates)
			view.Walkthroughs = out.Decomposition.Walkthroughs
		}
	default: // factory.PlanAttention
		view.State, view.Summary = "attention", a.Result.Summary
		if out := a.Result.PhaseResult; out != nil && out.Output != nil && out.Output.Decomposition != nil {
			view.Attention = out.Output.Decomposition.Attention
		}
	}
	return view
}

func (s RequestExploration) planningHead(ctx context.Context, actor collaboration.ActorID, intent string) (factory.IntentRevision, error) {
	head, err := s.Requests.Revision(ctx, actor, intent)
	if err != nil {
		return factory.IntentRevision{}, err
	}
	if head.State != factory.IntentDraft {
		return factory.IntentRevision{}, ErrRequestNotDraft
	}
	return head, nil
}

// PlanningReceipt is a recorded attempt, not confirmation of a native start.
// Operation is absent only when this private draft has no recorded attempt.
type PlanningReceipt struct {
	Intent    string `json:"intent"`
	Operation string `json:"operation,omitempty"`
	PlanningStatus
}

// SavedPlanning reads one draft revision's recorded attempt independently of
// the head. It neither wires a chooser nor attempts review, funding or launch.
func (s Requests) SavedPlanning(ctx context.Context, actor collaboration.ActorID, intent, revision string) (PlanningReceipt, error) {
	var empty PlanningReceipt
	selected, err := s.SavedRevision(ctx, actor, intent, revision)
	if err != nil {
		return empty, err
	}
	if selected.State != factory.IntentDraft {
		return empty, ErrRequestNotFound
	}
	attempt, err := s.Store.IntentPlanAttempt(ctx, selected.Intent, selected.ID)
	if errors.Is(err, store.ErrNotFound) {
		return PlanningReceipt{Intent: selected.Intent, PlanningStatus: PlanningStatus{Revision: selected.ID, State: "none"}}, nil
	}
	if err != nil {
		return empty, err
	}
	if attempt.Validate() != nil || attempt.Subject.Intent == nil || attempt.Subject.Intent.OwnerID != int64(actor) || attempt.Subject.Intent.IntentID != selected.Intent || attempt.Subject.Intent.RevisionID != selected.ID || attempt.Subject.Intent.ScopeDigest != selected.Digest() || attempt.PlanningContext == nil || !factory.ValidIntentPlanningOperation(attempt.PlanningContext.Operation) {
		return empty, ErrRequestNotFound
	}
	view := renderIntentPlanOutcome(attempt)
	view.Revision = selected.ID
	if view.Walkthroughs != nil {
		if err := factory.ValidatePlanningWalkthroughs(selected, *view.Walkthroughs); err != nil {
			return empty, errors.Join(ErrPlanningWalkthroughInvalid, err)
		}
	}
	return PlanningReceipt{Intent: selected.Intent, Operation: attempt.PlanningContext.Operation, PlanningStatus: view}, nil
}

func (s RequestExploration) Status(ctx context.Context, actor collaboration.ActorID, intent string) (PlanningStatus, error) {
	var empty PlanningStatus
	head, err := s.planningHead(ctx, actor, intent)
	if err != nil {
		return empty, err
	}
	attempt, err := s.Requests.Store.IntentPlanAttempt(ctx, head.Intent, head.ID)
	if errors.Is(err, store.ErrNotFound) {
		return PlanningStatus{Revision: head.ID, State: "none"}, nil
	}
	if err != nil {
		return empty, err
	}
	if attempt.Subject.Intent == nil || attempt.Subject.Intent.OwnerID != head.Owner {
		return empty, ErrRequestNotFound
	}
	view := renderIntentPlanOutcome(attempt)
	view.Revision = head.ID
	if view.Walkthroughs != nil {
		if err := factory.ValidatePlanningWalkthroughs(head, *view.Walkthroughs); err != nil {
			return empty, errors.Join(ErrPlanningWalkthroughInvalid, err)
		}
		// No chooser is reachable from a status read, even if the command
		// instance has one. Missing advice remains absent without reserving credit.
		replay := &control.Coordinator{Store: s.Requests.Store}
		view.Review, _ = walkthroughAdvice(ctx, replay, head, *view.Walkthroughs)
	}
	return view, nil
}

func walkthroughAdvice(ctx context.Context, coord *control.Coordinator, head factory.IntentRevision, content factory.ProposalContent) (*Exploration, error) {
	recommendation, err := coord.JudgeConsolidation(ctx, head, content)
	if err != nil {
		return nil, err
	}
	consistency, err := coord.JudgeConsistency(ctx, head, content, recommendation)
	if err != nil {
		return nil, err
	}
	return &Exploration{Content: content, Challenges: []factory.AssumptionChallenge{}, Settled: []string{}, Recommendation: &recommendation, Consistency: &consistency}, nil
}

// ReviewWalkthrough records advice about this exact completed planner result
// without advancing the draft head or adopting any native work.
func (s RequestExploration) ReviewWalkthrough(ctx context.Context, actor collaboration.ActorID, intent, revision string) (*Exploration, error) {
	head, err := s.planningHead(ctx, actor, intent)
	if err != nil {
		return nil, err
	}
	if head.ID != revision {
		return nil, store.ErrStaleRevision
	}
	if err := s.Requests.authorizeWrite(ctx, actor, head.Repository); err != nil {
		return nil, err
	}
	attempt, err := s.Requests.Store.IntentPlanAttempt(ctx, head.Intent, head.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrPlanningWalkthroughUnavailable
	}
	if err != nil {
		return nil, err
	}
	if attempt.Subject.Intent == nil || attempt.Subject.Intent.OwnerID != head.Owner {
		return nil, ErrRequestNotFound
	}
	view := renderIntentPlanOutcome(attempt)
	if view.State != "completed" || view.Walkthroughs == nil {
		return nil, ErrPlanningWalkthroughUnavailable
	}
	if err := factory.ValidatePlanningWalkthroughs(head, *view.Walkthroughs); err != nil {
		return nil, errors.Join(ErrPlanningWalkthroughInvalid, err)
	}
	chooser := s.Chooser
	if s.RetryCommand != "" {
		retry, ok := chooser.(interface {
			RetryFailedJudgment(string) control.ProductJudgment
		})
		if !factory.ValidID(s.RetryCommand) || !ok {
			return nil, ErrRequestInputInvalid
		}
		chooser = retry.RetryFailedJudgment(s.RetryCommand)
	}
	coord := &control.Coordinator{Store: s.Requests.Store, Planning: &control.PlanningJudgments{Chooser: chooser}}
	review, err := walkthroughAdvice(ctx, coord, head, *view.Walkthroughs)
	if err != nil {
		return nil, err
	}
	current, err := s.Requests.Revision(ctx, actor, head.Intent)
	if err != nil {
		return nil, err
	}
	if current.ID != head.ID {
		return nil, store.ErrStaleRevision
	}
	return review, nil
}

func (s Requests) Generation(ctx context.Context, actor collaboration.ActorID, intent string) (*factory.IntentGeneration, string, error) {
	head, err := s.Revision(ctx, actor, intent)
	if err != nil {
		return nil, "", err
	}
	generation, err := s.Store.IntentGeneration(ctx, head.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, head.ID, nil
	}
	if err != nil {
		return nil, "", err
	}
	return &generation, head.ID, nil
}

// SavedGeneration reads recorded work creation for the selected immutable
// acceptance, independently of the current head. It never drives graph work.
func (s Requests) SavedGeneration(ctx context.Context, actor collaboration.ActorID, intent, revision string) (*factory.IntentGeneration, error) {
	selected, err := s.SavedRevision(ctx, actor, intent, revision)
	if err != nil {
		return nil, err
	}
	if selected.State != factory.IntentAccepted {
		return nil, ErrRequestNotFound
	}
	generation, err := s.Store.IntentGeneration(ctx, selected.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if generation.IntentID != selected.Intent || generation.RevisionID != selected.ID {
		return nil, ErrRequestNotFound
	}
	return &generation, nil
}
