package service

import (
	"context"
	"errors"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

type repositoryQueueView struct {
	Queued int `json:"queued"`
	Active int `json:"active"`
}

type RepositoryPreparation struct {
	Project      string `json:"project"`
	Requirements string `json:"requirements,omitempty"`
	Approval     string `json:"approval,omitempty"`
	Ready        bool   `json:"ready"`
}

type RepositoryStatusView struct {
	PolicyOptions       *RepositoryPolicyOptions     `json:"policy_options,omitempty"`
	NativeBindings      []factory.ActorBindingRef    `json:"native_bindings"`
	Policy              *factory.RepositoryPolicy    `json:"policy"`
	OperatorGrant       *factory.OperatorGrant       `json:"operator_grant"`
	Appliance           *factory.Capacity            `json:"capacity"`
	Sponsorships        []factory.Sponsorship        `json:"sponsorships"`
	EnvironmentGrant    *project.EnvironmentGrant    `json:"environment_grant"`
	Preparation         *RepositoryPreparation       `json:"preparation"`
	Queue               repositoryQueueView          `json:"queue"`
	Effective           factory.EffectiveAuthority   `json:"effective"`
	DispatchObservation *control.DispatchObservation `json:"dispatch_observation,omitempty"`
	Repository          string                       `json:"repository"`
}

// RepositoryPolicyOptions are current provider settings, not mutation authority.
type RepositoryPolicyOptions struct {
	TargetBranch string   `json:"target_branch"`
	MergeMethods []string `json:"merge_methods"`
}

type RepositoryStatus struct {
	PolicyOptions func(context.Context, collaboration.ActorID, collaboration.RepositoryID) (*RepositoryPolicyOptions, error)
	Store         *store.Store
	Visibility    WorkVisibility
	Bindings      func(context.Context, collaboration.ActorID, collaboration.RepositoryID) ([]factory.ActorBindingRef, error)
	Preparation   func(context.Context, collaboration.ActorID, collaboration.RepositoryID) (*RepositoryPreparation, error)
	Authority     func(context.Context, collaboration.ActorID, collaboration.RepositoryID) (factory.EffectiveAuthority, error)
	// Diagnostic is optional and must check current administration itself.
	// Unknown administration omits private diagnostics, never enables them.
	Diagnostic func(context.Context, collaboration.ActorID, collaboration.RepositoryID) (*control.DispatchObservation, error)
}

var (
	ErrRepositoryStatusUnavailable = errors.New("repository status is unavailable")
	ErrRepositoryStatusVisibility  = errors.New("repository status is not visible")
	ErrRepositoryStatusBindings    = errors.New("repository binding identity is unavailable")
	ErrRepositoryStatusPreparation = errors.New("repository preparation is unavailable")
)

// Read assembles current configuration and recorded dispatch facts. Runtime
// readiness/bindings are supplied explicitly; absent ports cannot imply ready.
func (s RepositoryStatus) Read(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) (*RepositoryStatusView, error) {
	if actor < 1 || repository < 1 {
		return nil, ErrWorkReadQuery
	}
	if s.Store == nil || s.Visibility == nil || s.Bindings == nil || s.Preparation == nil || s.Authority == nil {
		return nil, ErrRepositoryStatusUnavailable
	}
	visible := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.Visibility(ctx, actor, repository); err != nil {
			return errors.Join(ErrRepositoryStatusVisibility, err)
		}
		return ctx.Err()
	}
	if err := visible(); err != nil {
		return nil, err
	}
	view := &RepositoryStatusView{Repository: strconv.FormatInt(int64(repository), 10), NativeBindings: []factory.ActorBindingRef{}, Sponsorships: []factory.Sponsorship{}}
	if s.PolicyOptions != nil {
		// Missing provider options keep initial setup unavailable; they do not
		// prevent browsing or editing an already recorded policy.
		if options, err := s.PolicyOptions(ctx, actor, repository); err == nil {
			view.PolicyOptions = options
		}
	}
	bindings, err := s.Bindings(ctx, actor, repository)
	if err != nil {
		return nil, errors.Join(ErrRepositoryStatusBindings, err)
	}
	for _, binding := range bindings {
		if err := binding.Validate(); err != nil {
			return nil, errors.Join(ErrRepositoryStatusBindings, err)
		}
		view.NativeBindings = append(view.NativeBindings, binding)
	}
	if policy, err := s.Store.RepositoryPolicy(ctx, int64(repository)); err == nil {
		if policy.Repository != int64(repository) {
			return nil, ErrWorkJourneyScope
		}
		view.Policy = &policy
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if grant, err := s.Store.OperatorGrant(ctx, int64(repository)); err == nil {
		if grant.Repository != int64(repository) {
			return nil, ErrWorkJourneyScope
		}
		view.OperatorGrant = &grant
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if capacity, err := s.Store.Capacity(ctx); err == nil {
		view.Appliance = &capacity
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	sponsorships, err := s.Store.Sponsorships(ctx, int64(repository))
	if err != nil {
		return nil, err
	}
	for _, sponsorship := range sponsorships {
		if sponsorship.Repository != int64(repository) {
			return nil, ErrWorkJourneyScope
		}
		if sponsorship.GrantedBy != int64(actor) {
			sponsorship.AllowanceMinutes = 0
		}
		view.Sponsorships = append(view.Sponsorships, sponsorship)
	}
	if grant, err := s.Store.EnvironmentGrant(ctx, int64(repository)); err == nil {
		if grant.Repository != int64(repository) {
			return nil, ErrWorkJourneyScope
		}
		view.EnvironmentGrant = &grant
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	view.Preparation, err = s.Preparation(ctx, actor, repository)
	if err != nil {
		return nil, errors.Join(ErrRepositoryStatusPreparation, err)
	}
	if view.Preparation != nil && view.Preparation.Project == "" {
		return nil, ErrRepositoryStatusPreparation
	}
	view.Effective, err = s.Authority(ctx, actor, repository)
	if err != nil {
		return nil, err
	}
	view.Queue.Queued, view.Queue.Active, err = s.Store.RepositoryDispatchCounts(ctx, int64(repository))
	if err != nil {
		return nil, err
	}
	if s.Diagnostic != nil {
		if observation, err := s.Diagnostic(ctx, actor, repository); err == nil {
			view.DispatchObservation = observation
		}
	}
	if err := visible(); err != nil {
		return nil, err
	}
	return view, nil
}
