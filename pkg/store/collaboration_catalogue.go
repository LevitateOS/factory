package store

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
)

// CollaborationRepositoryWindow lists existing observations, never remote
// discovery or access grants. The extra row lets callers advance over denied
// observations without inventing a complete provider inventory.
func (s *Store) CollaborationRepositoryWindow(ctx context.Context, after collaboration.RepositoryID, limit int) ([]collaboration.RepositoryRef, error) {
	if after < 0 || limit < 1 || limit > 100 {
		return nil, errors.New("invalid collaboration repository window")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,host,external_id,name FROM collaboration_repositories WHERE id>$1 ORDER BY id LIMIT $2`, after, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := make([]collaboration.RepositoryRef, 0, limit+1)
	for rows.Next() {
		var ref collaboration.RepositoryRef
		if err := rows.Scan(&ref.ID, &ref.Host, &ref.ExternalID, &ref.Name); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}
