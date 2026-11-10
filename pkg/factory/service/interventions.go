package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/store"
)

type RepositoryCommand struct {
	ExternalPull *factory.ExternalPullAdoption `json:"external_pull,omitempty"`
	ExternalSelection *factory.ExternalPullSelection `json:"external_selection,omitempty"`
	CommandID  string                          `json:"command_id"`
	Action     string                          `json:"action"`
	Completion *factory.CompletionRetryRequest `json:"completion,omitempty"`
}

type RunCommand struct {
	ReviewRetry *factory.ReviewRetry `json:"review_retry,omitempty"`
	MergePreparation *factory.MergePreparation         `json:"merge_preparation,omitempty"`
	Funding          *factory.RetainedCandidateFunding `json:"funding,omitempty"`
	CommandID        string                            `json:"command_id,omitempty"`
	Action           string                            `json:"action"`
	Context          *factory.ContextCurrentRequest    `json:"context,omitempty"`
	Publication      string                            `json:"publication,omitempty"`
}

// InterventionWriter is the existing coordinator's settlement and workflow
// owner. Admission here neither substitutes a host effect nor creates a journal.
type InterventionWriter interface {
	PauseRepository(context.Context, string, int64) (factory.PauseResult, error)
	ResumeRepository(context.Context, int64) (factory.ResumeResult, error)
	RetryCompletion(context.Context, string, string, int64, factory.CompletionRetryRequest) (factory.CompletionRetryDecision, error)
	Stop(context.Context, string) (control.StopResult, error)
	RetryRun(context.Context, string, string, string) (factory.RetryDecision, error)
	UseCurrentContext(context.Context, string, string, factory.ContextCurrentRequest) (factory.ContextCurrentResolution, error)
	RequestDeliveryRepair(context.Context, string, int64, string) (factory.DeliveryRepairResult, error)
	RecoverRunWork(context.Context, string, execution.RecoveryTarget) (factory.TakeoverRecord, error)
}

type Interventions struct {
	Store          *store.Store
	Writer         InterventionWriter
	WriteAccess    WorkVisibility
	RunRepository  func(context.Context, collaboration.ActorID, factory.Run) (collaboration.RepositoryID, error)
	RecoveryTarget func(context.Context, collaboration.ActorID, collaboration.RepositoryID, factory.Run) (execution.RecoveryTarget, error)
}

var (
	ErrInterventionUnavailable    = errors.New("Factory intervention runtime is unavailable")
	ErrInterventionInvalid        = errors.New("Factory intervention scope is invalid")
	ErrInterventionAction         = errors.New("Factory intervention action is invalid")
	ErrInterventionCommand        = errors.New("Factory intervention command identity is invalid")
	ErrInterventionPayload        = errors.New("Factory intervention payload does not match its action")
	ErrInterventionAccess         = errors.New("current repository execution authority is required")
	ErrInterventionRecoveryTarget = errors.New("current recovery destination authority is required")
)

func (s Interventions) admit(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.WriteAccess(ctx, actor, repository); err != nil {
		return errors.Join(ErrInterventionAccess, err)
	}
	return ctx.Err()
}

// Repository returns the owning coordinator's concrete receipt unchanged. The
// selected action determines its type; there is no second response envelope.
func (s Interventions) Repository(ctx context.Context, actor collaboration.ActorID, principal string, repository collaboration.RepositoryID, in RepositoryCommand) (any, error) {
	if actor < 1 || repository < 1 || principal == "" || len(principal) > 128 {
		return nil, ErrInterventionInvalid
	}
	if !factory.ValidRepositoryAction(in.Action) {
		return nil, ErrInterventionAction
	}
	if in.Action == factory.ActionCompletionRetry || in.Action == factory.CommandAdoptExternalPull {
		if !factory.ValidID(in.CommandID) {
			return nil, ErrInterventionCommand
		}
	} else if in.CommandID != "" {
		return nil, ErrInterventionPayload
	}
	if (in.Action == factory.ActionCompletionRetry) != (in.Completion != nil) || (in.Completion != nil && in.Completion.Validate() != nil) {
		return nil, ErrInterventionPayload
	}
	if (in.Action==factory.CommandAdoptExternalPull)!=(in.ExternalPull!=nil) || (in.Action==factory.ActionInspectExternalPull)!=(in.ExternalSelection!=nil) ||
		in.ExternalPull!=nil && (in.ExternalPull.Validate()!=nil || in.ExternalPull.Repository!=int64(repository)) || in.ExternalSelection!=nil && (in.ExternalSelection.Issue<1 || in.ExternalSelection.Number<1) { return nil,ErrInterventionPayload }
	if s.Writer == nil || s.WriteAccess == nil {
		return nil, ErrInterventionUnavailable
	}
	if err := s.admit(ctx, actor, repository); err != nil {
		return nil, err
	}
	switch in.Action {
	case factory.ActionInspectExternalPull,factory.CommandAdoptExternalPull:
		writer,ok:=s.Writer.(interface {
			ExternalPullOffer(context.Context,int64,factory.ExternalPullSelection) (factory.ExternalPullAdoption,error)
			AdoptExternalPull(context.Context,string,string,factory.ExternalPullAdoption) (factory.ExternalPullAdmission,error)
		})
		if !ok { return nil,ErrInterventionUnavailable }
		if in.Action==factory.ActionInspectExternalPull { return writer.ExternalPullOffer(ctx,int64(repository),*in.ExternalSelection) }
		return writer.AdoptExternalPull(ctx,in.CommandID,principal,*in.ExternalPull)
	case factory.ActionPause:
		return s.Writer.PauseRepository(ctx, principal, int64(repository))
	case factory.ActionResume:
		return s.Writer.ResumeRepository(ctx, int64(repository))
	default:
		return s.Writer.RetryCompletion(ctx, in.CommandID, principal, int64(repository), *in.Completion)
	}
}

// Run resolves the stored run through an explicit runtime scope, independently
// checks current repository authority, and admits a recovery target for takeover.
// Manual runs without a display binding remain usable in their admitted runtime.
func (s Interventions) Run(ctx context.Context, actor collaboration.ActorID, principal, runID string, in RunCommand) (any, error) {
	if actor < 1 || principal == "" || len(principal) > 128 {
		return nil, ErrInterventionInvalid
	}
	if !factory.ValidID(runID) {
		return nil, factory.ErrNotFound
	}
	publicationAction := in.Action == factory.CommandRetryReview || in.Action == factory.CommandPublishRetained || in.Action == factory.CommandFundRetained || in.Action == factory.CommandPrepareMerge
	if !factory.ValidRunAction(in.Action) && in.Action != factory.ActionContextCurrent && !publicationAction {
		return nil, ErrInterventionAction
	}
	if (in.Action == factory.ActionRetry || publicationAction) && !factory.ValidID(in.CommandID) {
		return nil, ErrInterventionCommand
	}
	if (in.Action == factory.ActionContextCurrent) != (in.Context != nil) {
		return nil, ErrInterventionPayload
	}
	if (in.Action == factory.CommandRetryReview) != (in.ReviewRetry != nil) ||
		in.ReviewRetry != nil && (in.ReviewRetry.Validate() != nil || in.ReviewRetry.Publication != in.Publication || in.ReviewRetry.Run != runID) {
		return nil, ErrInterventionPayload
	}
	if publicationAction != (in.Publication != "") || in.Publication != "" && !factory.ValidID(in.Publication) || (in.Action == factory.CommandFundRetained) != (in.Funding != nil) || in.Funding != nil && (in.Funding.Validate() != nil || in.Funding.Publication != in.Publication) || (in.Action == factory.CommandPrepareMerge) != (in.MergePreparation != nil) || in.MergePreparation != nil && (in.MergePreparation.Validate() != nil || in.MergePreparation.Publication != in.Publication) {
		return nil, ErrInterventionPayload
	}
	if s.Store == nil || s.Writer == nil || s.WriteAccess == nil || s.RunRepository == nil {
		return nil, ErrInterventionUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	run, err := s.Store.FactoryRun(ctx, runID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, factory.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if run.ID != runID {
		return nil, ErrWorkJourneyScope
	}
	repository, err := s.RunRepository(ctx, actor, run)
	if err != nil {
		return nil, err
	}
	if repository < 1 {
		return nil, ErrInterventionUnavailable
	}
	if err := s.admit(ctx, actor, repository); err != nil {
		return nil, err
	}
	if binding, err := s.Store.FactoryRunView(ctx, runID); err == nil {
		if binding.RunID != runID || binding.Repository != int64(repository) {
			return nil, ErrWorkJourneyScope
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	var target execution.RecoveryTarget
	if in.Action == factory.ActionTakeover {
		if s.RecoveryTarget == nil {
			return nil, ErrInterventionUnavailable
		}
		target, err = s.RecoveryTarget(ctx, actor, repository, run)
		if err != nil {
			return nil, err
		}
		if target.Member == "" && target.Native == nil {
			return nil, ErrInterventionRecoveryTarget
		}
	}
	// Runtime binding, saved display scope and destination admission can outlive
	// the authority that admitted them. Recheck immediately before effects.
	if err := s.admit(ctx, actor, repository); err != nil {
		return nil, err
	}
	switch in.Action {
	case factory.CommandRetryReview:
		writer, ok := s.Writer.(interface {
			RetryUnsentReview(context.Context, string, string, factory.ReviewRetry) (any, error)
		})
		if !ok { return nil, ErrInterventionUnavailable }
		publication, err := s.Store.PublicationByID(ctx, in.Publication)
		if err != nil || publication.Repository != int64(repository) { return nil, ErrWorkJourneyScope }
		return writer.RetryUnsentReview(ctx, in.CommandID, principal, *in.ReviewRetry)
	case factory.CommandPrepareMerge:
		writer, ok := s.Writer.(interface {
			PrepareUnsentMerge(context.Context, string, string, factory.MergePreparation) (factory.MergePreparationReceipt, error)
		})
		if !ok {
			return nil, ErrInterventionUnavailable
		}
		publication, err := s.Store.PublicationByID(ctx, in.Publication)
		if err != nil || publication.Run != runID || publication.Repository != int64(repository) {
			return nil, ErrWorkJourneyScope
		}
		return writer.PrepareUnsentMerge(ctx, in.CommandID, principal, *in.MergePreparation)
	case factory.CommandFundRetained:
		writer, ok := s.Writer.(interface {
			FundRetainedCandidate(context.Context, string, string, factory.RetainedCandidateFunding) (factory.RetainedCandidateFundingReceipt, error)
		})
		if !ok {
			return nil, ErrInterventionUnavailable
		}
		publication, err := s.Store.PublicationByID(ctx, in.Publication)
		if err != nil || publication.Run != runID || publication.Repository != int64(repository) {
			return nil, ErrWorkJourneyScope
		}
		return writer.FundRetainedCandidate(ctx, in.CommandID, principal, *in.Funding)
	case factory.CommandPublishRetained:
		writer, ok := s.Writer.(interface {
			PublishRetainedCandidate(context.Context, string, string, string) (factory.Publication, error)
		})
		if !ok {
			return nil, ErrInterventionUnavailable
		}
		publication, err := s.Store.PublicationByID(ctx, in.Publication)
		if err != nil || publication.Run != runID || publication.Repository != int64(repository) {
			return nil, ErrWorkJourneyScope
		}
		return writer.PublishRetainedCandidate(ctx, in.CommandID, principal, in.Publication)
	case factory.ActionStop:
		return s.Writer.Stop(ctx, runID)
	case factory.ActionRetry:
		return s.Writer.RetryRun(ctx, in.CommandID, principal, runID)
	case factory.ActionContextCurrent:
		return s.Writer.UseCurrentContext(ctx, principal, runID, *in.Context)
	case factory.ActionDeliveryRepair:
		return s.Writer.RequestDeliveryRepair(ctx, principal, int64(actor), runID)
	default:
		return s.Writer.RecoverRunWork(ctx, runID, target)
	}
}
