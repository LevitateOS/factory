package service

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory/access"
)

var ErrRepositoryAuthorityUnavailable = errors.New("current repository authority is unavailable")

// RepositoryAuthorization contains current host-admitted evidence and display
// metadata. It is internal adapter output, never an assertion from browser JSON.
type RepositoryAuthorization struct {
	Actor access.AdmittedActor
	Name  string
}

// RepositoryAuthority binds one authenticated principal to current repository
// permission reads. Current must authenticate/revalidate provider or grant truth
// on every call, including revocation and canonical identity lookup. A stored
// observation or an unexpired evidence envelope alone cannot implement Current.
type RepositoryAuthority struct {
	Host       collaboration.HostID
	Actor      collaboration.ActorID
	Source     access.AdmissionSource
	Connection *collaboration.ConnectionID
	Runtime    *collaboration.RuntimeID
	Current    func(context.Context, collaboration.RepositoryID, access.Permission) (RepositoryAuthorization, error)
	Now        func() time.Time
}

// Check admits the exact actor, host, repository, permission and principal kind.
// It returns no partial metadata on errors. Calling it neither creates a grant
// nor admits execution, publication, provider custody or spending.
func (a RepositoryAuthority) Check(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, permission access.Permission) (RepositoryAuthorization, error) {
	if err := ctx.Err(); err != nil {
		return RepositoryAuthorization{}, err
	}
	if !collaboration.ValidID(string(a.Host)) || a.Actor < 1 || actor != a.Actor || repository < 1 ||
		(permission != access.Read && permission != access.Collaborate && permission != access.Configure) {
		return RepositoryAuthorization{}, access.ErrDenied
	}
	if a.Current == nil {
		return RepositoryAuthorization{}, ErrRepositoryAuthorityUnavailable
	}
	current, err := a.Current(ctx, repository, permission)
	if err != nil {
		return RepositoryAuthorization{}, err
	}
	if err := ctx.Err(); err != nil {
		return RepositoryAuthorization{}, err
	}
	evidence := current.Actor.Evidence()
	if evidence.Actor != actor || evidence.Source != a.Source ||
		(evidence.Connection == nil) != (a.Connection == nil) ||
		(evidence.Runtime == nil) != (a.Runtime == nil) ||
		a.Connection != nil && *evidence.Connection != *a.Connection ||
		a.Runtime != nil && *evidence.Runtime != *a.Runtime {
		return RepositoryAuthorization{}, access.ErrDenied
	}
	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}
	if err := current.Actor.Require(a.Host, repository, permission, now); err != nil {
		return RepositoryAuthorization{}, err
	}
	return current, nil
}

// Visibility implements the current-read port shared by catalogue and feed.
// Only a trusted adapter's explicit ErrRepositoryHidden represents invisibility;
// missing ports, malformed evidence and unknown access remain errors.
func (a RepositoryAuthority) Visibility(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID) (string, error) {
	current, err := a.Check(ctx, actor, repository, access.Read)
	if err != nil {
		return "", err
	}
	if !collaboration.ValidText(current.Name, 512) {
		return "", ErrRepositoryAuthorityUnavailable
	}
	return current.Name, nil
}

// Configure implements the current-administration port for request/policy
// mutations. Services still recheck it at their existing mutation boundaries.
func (a RepositoryAuthority) Configure(ctx context.Context, actor collaboration.ActorID, repository int64) error {
	_, err := a.Check(ctx, actor, collaboration.RepositoryID(repository), access.Configure)
	return err
}
