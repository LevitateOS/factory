package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// WorkAuthority describes current execution authority, separately from read
// visibility. Write-required may still permit watching the admitted repository.
type WorkAuthority struct {
	Reason         string
	SessionCurrent bool
}

type WorkControlAccess func(context.Context, collaboration.ActorID, collaboration.RepositoryID) (WorkAuthority, error)
type WorkRuntimeMembership func(context.Context, collaboration.ActorID, collaboration.RepositoryID, string) (bool, error)

type WorkControls struct {
	ExecutionUnavailable bool
	Store                *store.Store
	Visibility           WorkVisibility
	Authority            WorkControlAccess
	Membership           WorkRuntimeMembership
	Recovery             WorkRuntimeMembership
}

type WorkControlView struct {
	Capabilities WorkCapabilities `json:"capabilities"`
	Requested    *WorkRequested   `json:"requested,omitempty"`
}

var (
	ErrWorkControlUnavailable = errors.New("work control capabilities are unavailable")
	ErrWorkControlVisibility  = errors.New("work controls are not visible")
)

// Read projects possible controls, never authority to execute them. The actual
// lifecycle command rechecks its own current admission when a human acts.
func (s WorkControls) Read(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, issue int64) (WorkControlView, error) {
	var empty WorkControlView
	if actor < 1 || repository < 1 || issue < 1 {
		return empty, ErrWorkJourneyInvalid
	}
	if s.Store == nil || s.Visibility == nil || s.Authority == nil || s.Membership == nil {
		return empty, ErrWorkControlUnavailable
	}
	visible := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.Visibility(ctx, actor, repository); err != nil {
			return errors.Join(ErrWorkControlVisibility, err)
		}
		return ctx.Err()
	}
	if err := visible(); err != nil {
		return empty, err
	}
	acceptance := ""
	if recorded, err := s.Store.IssueControl(ctx, int64(repository), issue); err == nil {
		if recorded.Repository != int64(repository) || recorded.Issue != issue {
			return empty, ErrWorkJourneyScope
		}
		acceptance = recorded.Acceptance
	} else if !errors.Is(err, store.ErrNotFound) {
		return empty, err
	}
	var owner *factory.Assignment
	if recorded, err := s.Store.LatestIssueAssignment(ctx, int64(repository), issue); err == nil {
		if recorded.Repository != int64(repository) || recorded.Issue != issue {
			return empty, ErrWorkJourneyScope
		}
		owner = &recorded
	} else if !errors.Is(err, store.ErrNotFound) {
		return empty, err
	}
	view, err := s.project(ctx, actor, repository, issue, owner, acceptance)
	if err != nil {
		return empty, err
	}
	if err := visible(); err != nil {
		return empty, err
	}
	return view, nil
}

// project uses the composite issue read's already scoped recorded owner.
func (s WorkControls) project(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, issue int64, attempt *factory.Assignment, acceptance string) (WorkControlView, error) {
	var empty WorkControlView
	in := workCapabilityInput{}
	var requested *WorkRequested
	var err error
	if attempt != nil {
		if attempt.RetainedPublication == nil && attempt.Stage == factory.AssignmentFinished {
			if p, readErr := s.Store.PublicationByAssignment(ctx, attempt.ID); readErr == nil {
				in.RetainedCandidate = p.Stage == factory.PublicationFenced && !p.WithdrawRequested && p.PRNumber == 0
			} else if !errors.Is(readErr, store.ErrNotFound) {
				return empty, readErr
			}
		}
		if run, err := s.Store.FactoryRun(ctx, attempt.Run); err == nil {
			in.HasRun, in.RunSettled = true, run.Reconciled
		} else if !errors.Is(err, store.ErrNotFound) {
			return empty, err
		}
		if acceptance != "" {
			if decision, err := s.Store.PendingExplicitRetry(ctx, int64(repository), issue, acceptance); err == nil {
				in.RetryQueued = true
				requested = &WorkRequested{Retry: &workRetryRequestView{CommandID: decision.CommandID, Reason: decision.Reason, Prior: decision.Prior, Queued: decision.Queued}}
			} else if !errors.Is(err, store.ErrNotFound) && !errors.Is(err, store.ErrExplicitRetryIneligible) {
				return empty, err
			}
		}
		in.Member, err = s.Membership(ctx, actor, repository, attempt.ProjectID)
		if err != nil {
			return empty, err
		}
		if in.Member && s.Recovery != nil {
			available, err := s.Recovery(ctx, actor, repository, attempt.ProjectID)
			if err != nil {
				return empty, err
			}
			in.RecoveryUnavailable = !available
		}
	}
	open, _, _, err := s.Store.DispatchState(ctx, int64(repository))
	if err != nil {
		return empty, err
	}
	in.DispatchOpen = open
	authority, err := s.Authority(ctx, actor, repository)
	if err != nil {
		return empty, err
	}
	switch authority.Reason {
	case "", WorkWriteRequired, WorkNotVisible, WorkAuthorityUnavailable:
	default:
		return empty, ErrWorkControlUnavailable
	}
	in.AuthorityReason, in.SessionCurrent = authority.Reason, authority.SessionCurrent
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	capabilities := workCapabilitiesDTO(in)
	if attempt!=nil && attempt.ExternalPlanning!=nil { capabilities.Retry=WorkCapability{Reason:"external_planning_requires_new_adoption"} }
	mutating, _ := workCapabilityGates(in)
	if !s.ExecutionUnavailable && mutating=="" && in.DispatchOpen && in.Member {
		policy,err:=s.Store.RepositoryPolicy(ctx,int64(repository))
		if err!=nil { return empty,err }
		_,native,err:=s.Store.ExecutionWorkspace(ctx,repository)
		if err==nil && native!=nil && policy.Review.NativeProfile==native.Profile && policy.Review.NativeConnection!="" && policy.Roles[project.RoleCoder].Account==policy.Roles[project.RoleReviewer].Account && (policy.Judgments==nil || policy.Judgments.Account==policy.Roles[project.RoleCoder].Account) {
			capabilities.AdoptExternal=&WorkCapability{Available:true}
		}
	}
	if !s.ExecutionUnavailable && mutating == "" && in.DispatchOpen && in.Member && attempt != nil {
		publication, err := s.Store.PublicationByAssignment(ctx, attempt.PublicationAssignment)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return empty, err
		}
		var preparation factory.MergePreparation
		if err == nil {
			preparation, err = s.Store.UnsentMergePreparation(ctx, publication.ID)
		}
		if err == nil {
			capabilities.PrepareMerge = &WorkMergePreparationCapability{WorkCapability: WorkCapability{Available: true}, Preparation: preparation}
		} else if !errors.Is(err, store.ErrRetainedPublicationIneligible) && !errors.Is(err, store.ErrMergeConflict) && !errors.Is(err, store.ErrNotFound) && !errors.Is(err, store.ErrAdmissionChanged) && !errors.Is(err, store.ErrAttemptRunsPending) && !errors.Is(err, factory.ErrAttemptClosed) {
			return empty, err
		}
	}
	if !s.ExecutionUnavailable && mutating == "" && in.DispatchOpen && in.Member && attempt != nil {
		publication, err := s.Store.PublicationByAssignment(ctx, attempt.PublicationAssignment)
		var funding factory.RetainedCandidateFunding
		if err == nil {
			funding, err = s.Store.RetainedCandidateFundingOffer(ctx, publication.ID)
		}
		if err == nil {
			capabilities.FundRetained = &WorkFundingCapability{WorkCapability: WorkCapability{Available: true}, Funding: funding}
		} else if !errors.Is(err, store.ErrRetainedPublicationIneligible) && !errors.Is(err, store.ErrNotFound) && !errors.Is(err, store.ErrAdmissionChanged) && !errors.Is(err, store.ErrAttemptRunsPending) {
			return empty, err
		}
		if publication.ID != "" {
			retry, retryErr := s.Store.ReviewRetryOffer(ctx, publication.ID)
			if retryErr == nil {
				capabilities.RetryReview = &WorkReviewRetryCapability{WorkCapability: WorkCapability{Available:true}, Retry:retry}
			} else if !errors.Is(retryErr, store.ErrRetainedPublicationIneligible) && !errors.Is(retryErr, store.ErrNotFound) {
				return empty, retryErr
			}
		}
	}
	if s.ExecutionUnavailable {
		unavailable := WorkCapability{Reason: WorkAuthorityUnavailable}
		capabilities.PublishRetained = unavailable
		capabilities.Watch, capabilities.Stop, capabilities.Retry, capabilities.Takeover = unavailable, unavailable, unavailable, unavailable
	}
	return WorkControlView{Capabilities: capabilities, Requested: requested}, nil
}
