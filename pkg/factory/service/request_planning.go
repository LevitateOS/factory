package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// RecordedPlanGenerator adopts the reviewed recorded plan through the existing
// funded choice, source/preparation currency and conditional graph workflow.
type RecordedPlanGenerator interface {
	GenerateAcceptedIntentFromRecordedPlan(context.Context, string, string, int64) (factory.IntentGeneration, *control.DispatchWait, error)
}

var _ RecordedPlanGenerator = (*control.Coordinator)(nil)

// RequestPlanning owns admitted planning/generation commands. Hosts wire only
// available capabilities and supply current authority through Requests. The
// service neither detaches cancellation nor starts a worker on construction.
type RequestPlanning struct {
	Requests  Requests
	Planner   PostPlanTransport
	Generator RecordedPlanGenerator
}

var (
	ErrRequestNotDraft           = errors.New("only draft direction starts a planning run")
	ErrRequestNotAccepted        = errors.New("review the product direction before generating work")
	ErrPlanningOperationInvalid  = errors.New("unknown planning operation")
	ErrPlanningUnavailable       = errors.New("no enrolled model account can answer the staged prompt")
	ErrPlanningInputsUnavailable = errors.New("draft planning inputs are unavailable")
	ErrGraphUnavailable          = errors.New("native issue creation needs an enrolled graph actor")
)

// PlanningLaunch names the durable admitted assignment/run. A false
// LaunchConfirmed means this response does not confirm a native start;
// the recorded status remains authoritative during recovery.
type PlanningLaunch struct {
	AssignmentID    string `json:"assignment_id"`
	RunID           string `json:"run_id"`
	RevisionID      string `json:"revision_id"`
	LaunchConfirmed *bool  `json:"launch_confirmed,omitempty"`
}

type PlanningResult struct {
	Launch *PlanningLaunch
	Waits  []control.DispatchWait
}

func (s RequestPlanning) Plan(ctx context.Context, actor collaboration.ActorID, intent, revision, operation string) (PlanningResult, error) {
	var empty PlanningResult
	current, err := s.Requests.Revision(ctx, actor, intent)
	if err != nil {
		return empty, err
	}
	if current.ID != revision {
		return empty, store.ErrStaleRevision
	}
	if current.State != factory.IntentDraft {
		return empty, ErrRequestNotDraft
	}
	if err := s.Requests.authorizeWrite(ctx, actor, current.Repository); err != nil {
		return empty, err
	}
	if !factory.ValidIntentPlanningOperation(operation) {
		return empty, ErrPlanningOperationInvalid
	}
	if s.Planner == nil {
		return empty, ErrPlanningUnavailable
	}
	// A fresh coordinator may see the protected session as busy before it
	// reaches dispatch. Reuse the captured attempt first instead of preparing
	// new inputs or hiding the funded identity behind a repository-full wait.
	if attempt, err := s.Requests.Store.IntentPlanAttempt(ctx, current.Intent, current.ID); err == nil {
		if attempt.Validate() != nil || attempt.Subject.Intent == nil || attempt.Subject.Intent.OwnerID != int64(actor) || attempt.Subject.Intent.IntentID != current.Intent || attempt.Subject.Intent.RevisionID != current.ID || attempt.Subject.Intent.ScopeDigest != current.Digest() || attempt.PlanningContext == nil || attempt.PlanningContext.Operation != operation {
			return empty, store.ErrCommandConflict
		}
		confirmed := false
		return PlanningResult{Launch: &PlanningLaunch{AssignmentID: attempt.ID, RunID: attempt.Run, RevisionID: current.ID, LaunchConfirmed: &confirmed}}, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return empty, err
	}
	plan, prep, wait, err := s.Planner.AssembleIntentPlanningInput(ctx, current, operation)
	if err != nil {
		return empty, errors.Join(ErrPlanningInputsUnavailable, err)
	}
	if wait != nil {
		return PlanningResult{Waits: []control.DispatchWait{*wait}}, nil
	}
	report := s.Planner.DispatchIntentPlan(ctx, current.Intent, int64(actor), operation, plan, prep.SourceCommit, prep.ID)
	if len(report.Errors) != 0 {
		failure := report.Errors[0]
		if failure.Reason == control.DispatchErrPromptBound {
			return empty, errors.Join(control.ErrIntentPlanningPromptBound, errors.New(failure.Detail))
		}
		if failure.Reason == control.DispatchErrHost || failure.Reason == control.DispatchErrFence {
			a, readErr := s.Requests.Store.IntentPlanAttempt(ctx, current.Intent, current.ID)
			if readErr == nil && a.Validate() == nil && a.Subject.Intent != nil && a.Subject.Intent.OwnerID == int64(actor) && a.Subject.Intent.IntentID == current.Intent && a.Subject.Intent.RevisionID == current.ID && a.Subject.Intent.ScopeDigest == current.Digest() && a.SourceCommit == prep.SourceCommit && a.Preparation == prep.ID && a.PlanningContext != nil && a.PlanningContext.Operation == operation {
				confirmed := false
				return PlanningResult{Launch: &PlanningLaunch{AssignmentID: a.ID, RunID: a.Run, RevisionID: current.ID, LaunchConfirmed: &confirmed}}, nil
			}
		}
		return empty, errors.New(failure.Detail)
	}
	if len(report.Launched) != 1 {
		return PlanningResult{Waits: report.Waits}, nil
	}
	return PlanningResult{Launch: &PlanningLaunch{AssignmentID: report.Launched[0].AssignmentID, RunID: report.Launched[0].RunID, RevisionID: current.ID}}, nil
}

func (s RequestPlanning) Generate(ctx context.Context, actor collaboration.ActorID, intent, revision string) (factory.IntentGeneration, *control.DispatchWait, error) {
	var empty factory.IntentGeneration
	if !project.ValidDecisionID(revision) {
		return empty, nil, ErrRequestInputInvalid
	}
	current, err := s.Requests.Revision(ctx, actor, intent)
	if err != nil {
		if errors.Is(err, ErrRequestNotFound) {
			return empty, nil, err
		}
		return empty, nil, errors.Join(ErrRequestUnavailable, err)
	}
	if current.ID != revision {
		return empty, nil, store.ErrStaleRevision
	}
	if current.State != factory.IntentAccepted {
		return empty, nil, ErrRequestNotAccepted
	}
	if err := s.Requests.authorizeWrite(ctx, actor, current.Repository); err != nil {
		return empty, nil, err
	}
	if s.Generator == nil {
		return empty, nil, ErrGraphUnavailable
	}
	return s.Generator.GenerateAcceptedIntentFromRecordedPlan(ctx, current.Intent, revision, int64(actor))
}
