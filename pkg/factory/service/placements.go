package service

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

type PlacementChange struct {
	ExpectedRevision collaboration.Revision `json:"expected_revision"`
	State            factory.PlacementState `json:"state"`
}

// Runtime is the host's configured controller, never a browser-chosen identity.
type Placements struct {
	Store   *store.Store
	Host    collaboration.HostID
	Runtime collaboration.RuntimeRef
	// Host-specific controller management: local owner on desktop, repository
	// administration where the host operates a shared appliance.
	AdminAccess WorkVisibility
	// LocalSettings permits local capacity/workspace consent independently of
	// host-owned controller selection. Actual environment admission is separate.
	LocalSettings WorkVisibility
}

var ErrPlacementUnavailable = errors.New("runtime placement admission is unavailable")
var ErrPlacementAdminRequired = errors.New("current repository administration is required")

func (s Placements) Apply(ctx context.Context, actor collaboration.ActorID, repository collaboration.RepositoryID, in PlacementChange) (factory.Placement, error) {
	var empty factory.Placement
	if s.Store == nil || s.AdminAccess == nil {
		return empty, ErrPlacementUnavailable
	}
	if actor <= 0 || repository <= 0 || (in.State != factory.PlacementActive && in.State != factory.PlacementRetiring && in.State != factory.PlacementInactive) {
		return empty, factory.ErrPlacement
	}
	if err := s.AdminAccess(ctx, actor, repository); err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	return s.Store.SelectPlacement(ctx, s.Host, repository, actor, s.Runtime, in.ExpectedRevision, in.State)
}
