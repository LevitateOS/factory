package runtime

import "github.com/levitateos/factory/pkg/factory"

// Placement remains available to runtime consumers; the product contract is
// defined beside the canonical admission models used by the store.
type Placement = factory.Placement
type PlacementState = factory.PlacementState
type Coordination = factory.Coordination

const (
	PlacementInactive     = factory.PlacementInactive
	PlacementActive       = factory.PlacementActive
	PlacementRetiring     = factory.PlacementRetiring
	FountainCentral       = factory.FountainCentral
	LocalSingleController = factory.LocalSingleController
)

var ErrPlacement = factory.ErrPlacement
