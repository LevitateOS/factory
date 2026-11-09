package service

import (
	"context"
	"errors"
	"maps"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

// PolicyActor names an existing operation binding. Operation kind is selected
// by its policy slot, never supplied by the command body.
type PolicyActor struct {
	ControllerGrantID string                     `json:"controller_grant_id,omitempty"`
	NativeProfile     collaboration.ProfileID    `json:"native_profile,omitempty"`
	NativeConnection  collaboration.ConnectionID `json:"native_connection,omitempty"`
	TokenID           int64                      `json:"token_id,string"`
	ActorID           int64                      `json:"actor_id,string"`
}

type PolicyChange struct {
	ExecutionActor      *PolicyActor                     `json:"execution_actor,omitempty"`
	AttemptLimits       *factory.AttemptLimits           `json:"attempt_limits"`
	Roles               map[string]factory.RoleSelection `json:"roles"`
	Judgments           *factory.RoleSelection           `json:"judgments,omitempty"`
	PublishActor        PolicyActor                      `json:"publish_actor"`
	CreateActor         PolicyActor                      `json:"create_actor"`
	ReviewActor         PolicyActor                      `json:"review_actor"`
	AutomationReviewer  *PolicyActor                     `json:"automation_reviewer,omitempty"`
	ResolveActor        *PolicyActor                     `json:"resolve_actor,omitempty"`
	MergeActor          PolicyActor                      `json:"merge_actor"`
	TargetBranch        string                           `json:"target_branch"`
	MergeMethod         string                           `json:"merge_method"`
	RequiredChecks      []string                         `json:"required_checks"`
	ExpectedRevision    int64                            `json:"expected_revision"`
	ReassessmentMinutes int                              `json:"reassessment_minutes"`
	IntentMinutes       int                              `json:"intent_minutes"`
	Enabled             bool                             `json:"enabled"`
	Paused              bool                             `json:"paused"`
}

// PolicyWriter retains the existing transactional grant/CAS and withdrawal
// owner. Applying a policy does not itself enroll accounts or dispatch work.
type PolicyWriter interface {
	ApplyPolicy(context.Context, string, factory.RepositoryPolicy) (factory.GrantResult, error)
}

// PolicyBindingAccess checks that this canonical actor binding belongs to the
// admitted provider/runtime scope. It does not enroll or replace credentials.
type PolicyBindingAccess func(context.Context, collaboration.ActorID, collaboration.RepositoryID, factory.ActorBindingRef) error

type Policies struct {
	Writer        PolicyWriter
	WriteAccess   RequestWriteAccess
	BindingAccess PolicyBindingAccess
}

var (
	ErrPolicyUnavailable        = errors.New("policy admission is unavailable")
	ErrPolicyInvalid            = errors.New("repository policy is invalid")
	ErrPolicyBindingInvalid     = errors.New("policy actor binding is invalid")
	ErrPolicyBindingUnavailable = errors.New("policy actor scope is unavailable")
	ErrPolicyResolverActor      = errors.New("resolver must use the independent reviewer account")
	ErrPolicyAccess             = errors.New("current repository policy authority is required")
)

func (s Policies) Apply(ctx context.Context, actor collaboration.ActorID, principal string, repository collaboration.RepositoryID, in PolicyChange) (factory.GrantResult, error) {
	var empty factory.GrantResult
	if actor < 1 || repository < 1 {
		return empty, ErrPolicyInvalid
	}
	if s.Writer == nil || s.WriteAccess == nil || s.BindingAccess == nil {
		return empty, ErrPolicyUnavailable
	}
	authorize := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.WriteAccess(ctx, actor, int64(repository)); err != nil {
			return errors.Join(ErrPolicyAccess, err)
		}
		return ctx.Err()
	}
	if err := authorize(); err != nil {
		return empty, err
	}
	bind := func(a PolicyActor, kind string) (factory.ActorBindingRef, error) {
		ref := factory.ActorBindingRef{ControllerGrantID: a.ControllerGrantID, NativeProfile: a.NativeProfile, NativeConnection: a.NativeConnection, TokenID: a.TokenID, ActorID: a.ActorID, Kind: kind}
		if err := ref.Validate(); err != nil {
			return factory.ActorBindingRef{}, errors.Join(ErrPolicyBindingInvalid, err)
		}
		if err := s.BindingAccess(ctx, actor, repository, ref); err != nil {
			return factory.ActorBindingRef{}, err
		}
		return ref, nil
	}
	publish, err := bind(in.PublishActor, factory.OpRefPublish)
	if err != nil {
		return empty, err
	}
	create, err := bind(in.CreateActor, factory.OpPRCreate)
	if err != nil {
		return empty, err
	}
	review, err := bind(in.ReviewActor, factory.OpReviewSubmit)
	if err != nil {
		return empty, err
	}
	merge, err := bind(in.MergeActor, factory.OpMerge)
	if err != nil {
		return empty, err
	}
	var resolve factory.ActorBindingRef
	if in.ResolveActor != nil {
		resolve, err = bind(*in.ResolveActor, factory.OpThreadResolve)
		if err != nil {
			return empty, err
		}
		if resolve.ActorID != review.ActorID {
			return empty, ErrPolicyResolverActor
		}
	}
	var execution *factory.ActorBindingRef
	if in.ExecutionActor != nil {
		ref, err := bind(*in.ExecutionActor, "execution.launch")
		if err != nil {
			return empty, err
		}
		execution = &ref
	}
	roles := maps.Clone(in.Roles)
	for role, selection := range roles {
		selection.Profile = factory.EffectiveExecutionProfile(&selection.Profile)
		roles[role] = selection
	}
	var judgments *factory.RoleSelection
	if in.Judgments != nil {
		selection := *in.Judgments
		selection.Profile = factory.EffectiveExecutionProfile(&selection.Profile)
		judgments = &selection
	}
	policy := factory.RepositoryPolicy{Execution: execution,
		Repository: int64(repository), Revision: in.ExpectedRevision, GrantedBy: int64(actor),
		Enabled: in.Enabled, Paused: in.Paused, TargetBranch: in.TargetBranch, Roles: roles, Judgments: judgments,
		Checks: in.RequiredChecks, MergeMethod: in.MergeMethod,
		Publish: publish, Create: create, Review: review, Resolve: resolve, Merge: merge,
		AttemptLimits:       factory.EffectiveAttemptLimits(in.AttemptLimits),
		ReassessmentMinutes: factory.EffectiveReassessmentMinutes(in.ReassessmentMinutes),
		// Zero remains explicitly unfunded; omission cannot invent intent credit.
		IntentMinutes: in.IntentMinutes,
	}
	if in.AutomationReviewer != nil {
		automation, err := bind(*in.AutomationReviewer, factory.OpReviewSubmit)
		if err != nil {
			return empty, err
		}
		policy.AutomationReviewer = &automation
	}
	if err := policy.ValidateForWrite(); err != nil {
		return empty, errors.Join(ErrPolicyInvalid, err)
	}
	// Identity/binding reads can outlive current administration or cancellation.
	if err := authorize(); err != nil {
		return empty, err
	}
	return s.Writer.ApplyPolicy(ctx, principal, policy)
}
