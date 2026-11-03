// Package runtime owns Factory runtime placement records. Rust owns processes
// and native supervision; this package does not launch or control OS processes.
package runtime

import (
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
)

type (
	PlacementState string
	Coordination   string
)

const (
	PlacementInactive     PlacementState = "inactive"
	PlacementActive       PlacementState = "active"
	PlacementRetiring     PlacementState = "retiring"
	FountainCentral       Coordination   = "fountain_central"
	LocalSingleController Coordination   = "local_single_controller"
)

var ErrPlacement = errors.New("factory placement unavailable or stale")

// Placement is the retained selection, not a lease synthesized from a login.
// Runtime changes require retirement of old work before activating a strictly
// newer generation. Fountain's gateway owns central CAS; GitHub's first release
// has one configured controller. A local lock cannot prove global exclusion.
type Placement struct {
	Host         collaboration.HostID       `json:"host"`
	Repository   collaboration.RepositoryID `json:"repository,string"`
	Runtime      collaboration.RuntimeID    `json:"runtime"`
	Generation   int64                      `json:"generation,string"`
	State        PlacementState             `json:"state"`
	Coordination Coordination               `json:"coordination"`
	SelectedBy   collaboration.ActorID      `json:"selected_by,string"`
	Revision     collaboration.Revision     `json:"revision"`
	UpdatedAt    time.Time                  `json:"updated_at"`
}

func (p Placement) Validate() error {
	if !collaboration.ValidID(string(p.Host)) || p.Repository <= 0 ||
		!collaboration.ValidID(string(p.Runtime)) || p.Generation <= 0 || p.SelectedBy <= 0 ||
		!collaboration.ValidText(string(p.Revision), 256) || p.UpdatedAt.IsZero() ||
		(p.State != PlacementInactive && p.State != PlacementActive && p.State != PlacementRetiring) ||
		(p.Coordination != FountainCentral && p.Coordination != LocalSingleController) {
		return ErrPlacement
	}
	return nil
}

// RequireDispatch checks an authoritative placement observation against the
// exact retained dispatch binding. The caller must hold its owning store/gateway
// exclusion gate through admission; this method is not a lock or lease.
func (p Placement) RequireDispatch(host collaboration.HostID, repository collaboration.RepositoryID, runtime collaboration.RuntimeID, generation int64) error {
	if p.Validate() != nil || p.State != PlacementActive || p.Host != host || p.Repository != repository ||
		p.Runtime != runtime || p.Generation != generation {
		return ErrPlacement
	}
	return nil
}
