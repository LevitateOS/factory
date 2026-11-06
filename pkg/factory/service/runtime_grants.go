package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// RuntimeGrantWriter retains the existing transactional revisions, dispatch
// withdrawal and publication/merge cancellation owner. Saving permission does
// not provision an environment or start work.
type RuntimeGrantWriter interface {
	ApplyCapacity(context.Context, string, factory.Capacity) (factory.GrantResult, error)
	ApplyOperatorGrant(context.Context, string, factory.OperatorGrant) (factory.GrantResult, error)
	ApplyEnvironmentGrant(context.Context, string, project.EnvironmentGrant) (factory.GrantResult, error)
}

type CapacityChange struct {
	ExpectedRevision  int64 `json:"expected_revision"`
	MaxConcurrentRuns int   `json:"max_concurrent_runs"`
}

type OperatorGrantChange struct {
	ExpectedRevision int64 `json:"expected_revision"`
	Active           bool  `json:"active"`
}

type EnvironmentGrantChange struct {
	Profile          *project.Profile `json:"profile"`
	Project          string           `json:"project,omitempty"`
	ExpectedRevision int64            `json:"expected_revision"`
	Active           bool             `json:"active"`
	Native           bool             `json:"native,omitempty"`
}

// RuntimeGrants separates admitted canonical attribution from the host's
// authenticated principal. Neither is selected by a command body. OperatorAccess
// checks current host administration; EnvironmentAccess checks current repository
// environment ownership. An operator grant additionally requires Visibility.
type RuntimeGrants struct {
	Writer            RuntimeGrantWriter
	OperatorAccess    func(context.Context, collaboration.ActorID) error
	Visibility        WorkVisibility
	EnvironmentAccess WorkVisibility
	NativeEnvironment func(context.Context, collaboration.ActorID, collaboration.RepositoryID, bool) (*project.NativeEnvironment, error)
}

var (
	ErrRuntimeGrantUnavailable   = errors.New("runtime grant admission is unavailable")
	ErrRuntimeGrantAccess        = errors.New("current runtime grant authority is required")
	ErrRuntimeCapacityInvalid    = errors.New("runtime capacity is invalid")
	ErrRuntimeOperatorInvalid    = errors.New("runtime operator grant is invalid")
	ErrRuntimeEnvironmentInvalid = errors.New("runtime environment grant is invalid")
)

func (s RuntimeGrants) ApplyCapacity(ctx context.Context, actor collaboration.ActorID, principal string, in CapacityChange) (factory.GrantResult, error) {
	var empty factory.GrantResult
	capacity := factory.Capacity{Revision: in.ExpectedRevision, UpdatedBy: int64(actor), MaxConcurrentRuns: in.MaxConcurrentRuns}
	if principal == "" || capacity.Validate() != nil {
		return empty, ErrRuntimeCapacityInvalid
	}
	if s.Writer == nil || s.OperatorAccess == nil {
		return empty, ErrRuntimeGrantUnavailable
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if err := s.OperatorAccess(ctx, actor); err != nil {
		return empty, errors.Join(ErrRuntimeGrantAccess, err)
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	return s.Writer.ApplyCapacity(ctx, principal, capacity)
}

func (s RuntimeGrants) ApplyOperatorGrant(ctx context.Context, actor collaboration.ActorID, principal string, repository collaboration.RepositoryID, in OperatorGrantChange) (factory.GrantResult, error) {
	var empty factory.GrantResult
	grant := factory.OperatorGrant{Repository: int64(repository), Revision: in.ExpectedRevision, GrantedBy: int64(actor), Active: in.Active}
	if principal == "" || grant.Validate() != nil {
		return empty, ErrRuntimeOperatorInvalid
	}
	if s.Writer == nil || s.OperatorAccess == nil || s.Visibility == nil {
		return empty, ErrRuntimeGrantUnavailable
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if err := s.Visibility(ctx, actor, repository); err != nil {
		return empty, errors.Join(ErrRuntimeGrantAccess, err)
	}
	// Visibility can require a provider round trip. Check the host's current
	// operator permission after it, immediately before the transactional writer.
	if err := s.OperatorAccess(ctx, actor); err != nil {
		return empty, errors.Join(ErrRuntimeGrantAccess, err)
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	return s.Writer.ApplyOperatorGrant(ctx, principal, grant)
}

func (s RuntimeGrants) ApplyEnvironmentGrant(ctx context.Context, actor collaboration.ActorID, principal string, repository collaboration.RepositoryID, in EnvironmentGrantChange) (factory.GrantResult, error) {
	var empty factory.GrantResult
	grant := project.EnvironmentGrant{Repository: int64(repository), Revision: in.ExpectedRevision, Owner: int64(actor), Profile: in.Profile, Project: in.Project, Active: in.Active}
	if principal == "" || !in.Native && grant.Validate() != nil || in.Native && (in.Profile != nil || in.Project != "" || in.ExpectedRevision < 0) {
		return empty, ErrRuntimeEnvironmentInvalid
	}
	if s.Writer == nil || s.EnvironmentAccess == nil {
		return empty, ErrRuntimeGrantUnavailable
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if err := s.EnvironmentAccess(ctx, actor, repository); err != nil {
		return empty, errors.Join(ErrRuntimeGrantAccess, err)
	}
	if in.Native {
		if s.NativeEnvironment == nil {
			return empty, ErrRuntimeGrantUnavailable
		}
		var err error
		grant.Native, err = s.NativeEnvironment(ctx, actor, repository, in.Active)
		if err != nil {
			return empty, err
		}
		if grant.Native == nil || grant.Validate() != nil {
			return empty, ErrRuntimeEnvironmentInvalid
		}
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	return s.Writer.ApplyEnvironmentGrant(ctx, principal, grant)
}
