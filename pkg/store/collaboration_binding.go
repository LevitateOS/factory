package store

import (
	"context"

	"github.com/levitateos/factory/pkg/collaboration"
)

// BindCollaborationConnection commits an observed account as one registry unit.
// The connector must first verify the remote identity and current native scope;
// this method does no network I/O and grants no authority. Reconnect preserves
// IDs and existing profile names, while fresh observed account labels may change.
// A retained unadopted ledger is never implicitly assigned to this provider.
func (s *Store) BindCollaborationConnection(ctx context.Context, observed collaboration.ConnectionObservation) (collaboration.ConnectionBinding, error) {
	var binding collaboration.ConnectionBinding
	base, err := collaboration.CanonicalAPIBase(observed.APIBase)
	if err != nil {
		return binding, err
	}
	if !collaboration.ValidID(string(observed.Profile.ID)) || !collaboration.ValidText(observed.Profile.Name, 120) ||
		!collaboration.ValidID(string(observed.Connection)) || !collaboration.ValidText(observed.Account, 512) ||
		observed.Login != "" && !collaboration.ValidText(observed.Login, 512) ||
		observed.Provider != collaboration.GitHub && observed.Provider != collaboration.Fountain {
		return binding, collaboration.ErrIdentityConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return binding, err
	}
	defer tx.Rollback()
	if err := initializeEmptyCollaborationLedger(ctx, tx); err != nil {
		return binding, err
	}
	var result collaboration.ConnectionBinding
	// The allocator gate serializes host discovery and fresh connection binding,
	// including across Store owners; the unique locator selects only an already
	// registered host or a genuinely new one, never a guessed provider number.
	err = tx.QueryRowContext(ctx, `INSERT INTO collaboration_hosts(id,provider,api_base) VALUES($1,$2,$3)
 ON CONFLICT(provider,api_base) DO UPDATE SET api_base=excluded.api_base RETURNING id,provider,api_base`,
		collaboration.NewID(), observed.Provider, base).Scan(&result.Host.ID, &result.Host.Provider, &result.Host.APIBase)
	if err != nil {
		return binding, registryWriteError(err)
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO collaboration_profiles(id,name) VALUES($1,$2)
 ON CONFLICT(id) DO UPDATE SET id=excluded.id RETURNING id,name`,
		observed.Profile.ID, observed.Profile.Name).Scan(&result.Profile.ID, &result.Profile.Name)
	if err != nil {
		return binding, registryWriteError(err)
	}
	err = tx.QueryRowContext(ctx, observeCollaborationActorSQL, result.Host.ID, observed.Account, observed.Login).
		Scan(&result.Actor.ID, &result.Actor.Host, &result.Actor.ExternalID, &result.Actor.Login)
	if err != nil {
		return binding, registryWriteError(err)
	}
	result.Connection = collaboration.ConnectionRef{ID: observed.Connection, Profile: result.Profile.ID, Host: result.Host.ID, Actor: result.Actor.ID}
	var id collaboration.ConnectionID
	err = tx.QueryRowContext(ctx, putCollaborationConnectionSQL, result.Connection.ID, result.Connection.Profile, result.Connection.Host, result.Connection.Actor).Scan(&id)
	if err != nil {
		return binding, registryWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		return binding, err
	}
	return result, nil
}

// ObserveCollaborationConnectionRepository atomically records a provider repo
// and which exact account binding observed it. The caller must supply current
// remotely observed metadata; this association is never an access grant or a
// complete inventory, and it must not survive as cached permission evidence.
func (s *Store) ObserveCollaborationConnectionRepository(ctx context.Context, connection collaboration.ConnectionRef, externalID, name string) (collaboration.RepositoryRef, error) {
	var ref collaboration.RepositoryRef
	if !collaboration.ValidID(string(connection.ID)) || !collaboration.ValidID(string(connection.Profile)) ||
		!collaboration.ValidID(string(connection.Host)) || connection.Actor < 1 ||
		!collaboration.ValidText(externalID, 512) || name != "" && !collaboration.ValidText(name, 512) {
		return ref, collaboration.ErrIdentityConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ref, err
	}
	defer tx.Rollback()
	var adopted bool
	if err := tx.QueryRowContext(ctx, `SELECT adopted FROM collaboration_ledger WHERE singleton FOR SHARE`).Scan(&adopted); err != nil {
		return ref, err
	}
	if !adopted {
		return ref, collaboration.ErrUnboundLedger
	}
	var id collaboration.ConnectionID
	err = tx.QueryRowContext(ctx, `SELECT id FROM collaboration_connections WHERE id=$1 AND profile=$2 AND host=$3 AND actor=$4 FOR SHARE`,
		connection.ID, connection.Profile, connection.Host, connection.Actor).Scan(&id)
	if err != nil {
		return ref, registryWriteError(err)
	}
	var result collaboration.RepositoryRef
	err = tx.QueryRowContext(ctx, observeCollaborationRepositorySQL, connection.Host, externalID, name).
		Scan(&result.ID, &result.Host, &result.ExternalID, &result.Name)
	if err != nil {
		return ref, registryWriteError(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO collaboration_repository_connections(repository,connection,host) VALUES($1,$2,$3)
 ON CONFLICT(repository,connection) DO NOTHING`, result.ID, connection.ID, connection.Host); err != nil {
		return ref, registryWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		return ref, err
	}
	return result, nil
}
