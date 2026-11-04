package store

import (
	"context"

	"github.com/levitateos/factory/pkg/collaboration"
)

// ApplyCollaborationObservation binds and maps one bounded nonsecret native
// observation atomically. No provider HTTP, credential or product authorization
// runs inside this transaction. A late conflict rolls back the complete batch.
func (s *Store) ApplyCollaborationObservation(ctx context.Context, batch collaboration.ConnectionObservationBatch) (collaboration.ConnectionObservationMapping, error) {
	var empty collaboration.ConnectionObservationMapping
	if batch.Repository < 0 || len(batch.Repositories) > 50 || len(batch.References) > 100 || len(batch.Actors) > 50_050 || batch.Repository > 0 && len(batch.Repositories) > 1 {
		return empty, collaboration.ErrIdentityConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	binding, err := bindCollaborationConnection(ctx, tx, batch.Connection)
	if err != nil {
		return empty, err
	}
	result := collaboration.ConnectionObservationMapping{Binding: binding, Repositories: []collaboration.RepositoryRef{}, Actors: map[string]collaboration.ActorRef{}, References: map[string]collaboration.RepositoryRef{}}
	if batch.Repository > 0 {
		var ref collaboration.RepositoryRef
		err = tx.QueryRowContext(ctx, `SELECT id,host,external_id,name FROM collaboration_repositories WHERE host=$1 AND id=$2 FOR SHARE`, binding.Host.ID, batch.Repository).Scan(&ref.ID, &ref.Host, &ref.ExternalID, &ref.Name)
		if err != nil {
			return empty, err
		}
		result.Repository = &ref
		if len(batch.Repositories) > 0 && batch.Repositories[0].ExternalID != ref.ExternalID {
			return empty, collaboration.ErrIdentityConflict
		}
	}
	for _, raw := range batch.Repositories {
		ref, err := observeCollaborationConnectionRepository(ctx, tx, binding.Connection, raw.ExternalID, raw.Name)
		if err != nil {
			return empty, err
		}
		result.Repositories = append(result.Repositories, ref)
		if batch.Repository > 0 {
			if ref.ID != batch.Repository {
				return empty, collaboration.ErrIdentityConflict
			}
			result.Repository = &ref
		}
	}
	for _, raw := range batch.Actors {
		if !collaboration.ValidText(raw.ExternalID, 512) || !collaboration.ValidText(raw.Login, 512) {
			return empty, collaboration.ErrIdentityConflict
		}
		var actor collaboration.ActorRef
		err = tx.QueryRowContext(ctx, observeCollaborationActorSQL, binding.Host.ID, raw.ExternalID, raw.Login).Scan(&actor.ID, &actor.Host, &actor.ExternalID, &actor.Login)
		if err != nil {
			return empty, registryWriteError(err)
		}
		result.Actors[raw.ExternalID] = actor
	}
	for _, raw := range batch.References {
		if !collaboration.ValidText(raw.ExternalID, 512) || !collaboration.ValidText(raw.Name, 512) {
			return empty, collaboration.ErrIdentityConflict
		}
		var ref collaboration.RepositoryRef
		err := tx.QueryRowContext(ctx, observeCollaborationRepositorySQL, binding.Host.ID, raw.ExternalID, raw.Name).Scan(&ref.ID, &ref.Host, &ref.ExternalID, &ref.Name)
		if err != nil {
			return empty, registryWriteError(err)
		}
		result.References[raw.ExternalID] = ref
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}
