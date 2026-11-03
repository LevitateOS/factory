package control

import (
	"context"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// passOccupancy snapshots held reservations and unattributed active
// runs once per pass. Attempts refresh it from the report they build so
// one pass never over-commits the limits it just consumed.
type passOccupancy struct {
	held         []factory.Reservation
	unattributed int
	byProject    map[string]int
}

func snapshotOccupancy(ctx context.Context, db *store.Store) (passOccupancy, error) {
	held, err := db.HeldReservations(ctx, store.MaxHeldReservations)
	if err != nil {
		return passOccupancy{}, err
	}
	total, byProject, err := db.ActiveRunCounts(ctx)
	if err != nil {
		return passOccupancy{}, err
	}
	return passOccupancy{held: held, unattributed: total, byProject: byProject}, nil
}

func (o *passOccupancy) hold(r factory.Reservation) {
	o.held = append(o.held, r)
}

func (o passOccupancy) heldTotal() int { return len(o.held) }

func (o passOccupancy) heldRepo(repository int64) int {
	n := 0
	for _, r := range o.held {
		if r.Repository == repository {
			n++
		}
	}
	return n
}
