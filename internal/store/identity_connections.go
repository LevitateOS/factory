package store

import (
	"context"
	"errors"

	"github.com/levitateos/sodaos/internal/identity"
)

// ConnectionGeneration returns the recorded generation for one identity
// connection bound to its owner. It reports a generic failure unless the
// row exists, the owner matches, the generation is at least 1 and the
// state is ready. It never selects credential columns.
func (s *Store) ConnectionGeneration(ctx context.Context, connectionID string, ownerID int64) (int64, error) {
	var owner, generation int64
	var state string
	if err := s.db.QueryRowContext(ctx, `SELECT owner_id,generation,state FROM identity_connections WHERE id=$1`, connectionID).Scan(&owner, &generation, &state); err != nil {
		return 0, errors.New("current connection unavailable")
	}
	if owner != ownerID || generation < 1 || state != identity.Ready {
		return 0, errors.New("current connection unavailable")
	}
	return generation, nil
}
