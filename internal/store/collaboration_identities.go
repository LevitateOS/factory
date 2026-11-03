package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/levitateos/sodaos/internal/collaboration"
)

// Registry methods persist observations and bindings only. They grant no remote
// permission or spending, and never accept/store provider credentials.
func (s *Store) PutCollaborationHost(ctx context.Context, host collaboration.Host) (collaboration.Host, error) {
	base, err := collaboration.CanonicalAPIBase(host.APIBase)
	if err != nil {
		return host, err
	}
	if !collaboration.ValidID(string(host.ID)) || (host.Provider != collaboration.Fountain && host.Provider != collaboration.GitHub) {
		return host, errors.New("invalid collaboration host")
	}
	err = s.db.QueryRowContext(ctx, `INSERT INTO collaboration_hosts(id,provider,api_base) VALUES($1,$2,$3)
 ON CONFLICT(id) DO UPDATE SET api_base=excluded.api_base WHERE collaboration_hosts.provider=excluded.provider
 RETURNING id,provider,api_base`, host.ID, host.Provider, base).Scan(&host.ID, &host.Provider, &host.APIBase)
	return host, registryWriteError(err)
}

func (s *Store) CollaborationHost(ctx context.Context, id collaboration.HostID) (collaboration.Host, error) {
	var host collaboration.Host
	err := s.db.QueryRowContext(ctx, `SELECT id,provider,api_base FROM collaboration_hosts WHERE id=$1`, id).Scan(&host.ID, &host.Provider, &host.APIBase)
	return host, err
}

// Resolve an already configured instance during account connection. This is a
// lookup of its locator, not permission to merge or recreate its stable HostID.
func (s *Store) CollaborationHostByAPIBase(ctx context.Context, provider collaboration.Provider, base string) (collaboration.Host, error) {
	var host collaboration.Host
	base, err := collaboration.CanonicalAPIBase(base)
	if err != nil {
		return host, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT id,provider,api_base FROM collaboration_hosts WHERE provider=$1 AND api_base=$2`, provider, base).Scan(&host.ID, &host.Provider, &host.APIBase)
	return host, err
}

func (s *Store) PutCollaborationProfile(ctx context.Context, profile collaboration.Profile) error {
	if !collaboration.ValidID(string(profile.ID)) || !collaboration.ValidText(profile.Name, 120) {
		return errors.New("invalid collaboration profile")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO collaboration_profiles(id,name) VALUES($1,$2) ON CONFLICT(id) DO UPDATE SET name=excluded.name`, profile.ID, profile.Name)
	return registryWriteError(err)
}

func (s *Store) CollaborationProfile(ctx context.Context, id collaboration.ProfileID) (collaboration.Profile, error) {
	var profile collaboration.Profile
	err := s.db.QueryRowContext(ctx, `SELECT id,name FROM collaboration_profiles WHERE id=$1`, id).Scan(&profile.ID, &profile.Name)
	return profile, err
}

// InitializeEmptyCollaborationLedger is an explicit composition step, not a
// side effect of opening storage. Retained provider-numbered rows require the
// separate Fountain adoption path; they must never share an unseeded allocator.
func (s *Store) InitializeEmptyCollaborationLedger(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var adopted bool
	if err := tx.QueryRowContext(ctx, `SELECT adopted FROM collaboration_ledger WHERE singleton FOR UPDATE`).Scan(&adopted); err != nil {
		return err
	}
	if adopted {
		return tx.Commit()
	}
	empty, err := emptyCollaborationLedger(ctx, tx)
	if err != nil {
		return err
	}
	if !empty {
		return collaboration.ErrUnboundLedger
	}
	if _, err := tx.ExecContext(ctx, `UPDATE collaboration_ledger SET adopted=true WHERE singleton`); err != nil {
		return err
	}
	return tx.Commit()
}

// Empty display metadata ensures the observed identity with an external-ID
// placeholder, preserving any existing label even across concurrent observers.
func (s *Store) ObserveCollaborationRepository(ctx context.Context, host collaboration.HostID, externalID, name string) (collaboration.RepositoryRef, error) {
	var ref collaboration.RepositoryRef
	if !collaboration.ValidID(string(host)) || !collaboration.ValidText(externalID, 512) || name != "" && !collaboration.ValidText(name, 512) {
		return ref, errors.New("invalid collaboration repository observation")
	}
	err := s.observeCollaborationIdentity(ctx, `INSERT INTO collaboration_repositories(host,external_id,name) VALUES($1,$2,COALESCE(NULLIF($3,''),$2))
 ON CONFLICT(host,external_id) DO UPDATE SET name=CASE WHEN $3='' THEN collaboration_repositories.name ELSE excluded.name END RETURNING id,host,external_id,name`, []any{host, externalID, name}, &ref.ID, &ref.Host, &ref.ExternalID, &ref.Name)
	return ref, registryWriteError(err)
}

func (s *Store) CollaborationRepository(ctx context.Context, id collaboration.RepositoryID) (collaboration.RepositoryRef, error) {
	var ref collaboration.RepositoryRef
	err := s.db.QueryRowContext(ctx, `SELECT id,host,external_id,name FROM collaboration_repositories WHERE id=$1`, id).Scan(&ref.ID, &ref.Host, &ref.ExternalID, &ref.Name)
	return ref, err
}

// An empty login records identity-only evidence without replacing a known login.
func (s *Store) ObserveCollaborationActor(ctx context.Context, host collaboration.HostID, externalID, login string) (collaboration.ActorRef, error) {
	var ref collaboration.ActorRef
	if !collaboration.ValidID(string(host)) || !collaboration.ValidText(externalID, 512) || login != "" && !collaboration.ValidText(login, 512) {
		return ref, errors.New("invalid collaboration actor observation")
	}
	err := s.observeCollaborationIdentity(ctx, `INSERT INTO collaboration_actors(host,external_id,login) VALUES($1,$2,COALESCE(NULLIF($3,''),$2))
 ON CONFLICT(host,external_id) DO UPDATE SET login=CASE WHEN $3='' THEN collaboration_actors.login ELSE excluded.login END RETURNING id,host,external_id,login`, []any{host, externalID, login}, &ref.ID, &ref.Host, &ref.ExternalID, &ref.Login)
	return ref, registryWriteError(err)
}

func (s *Store) CollaborationActor(ctx context.Context, id collaboration.ActorID) (collaboration.ActorRef, error) {
	var ref collaboration.ActorRef
	err := s.db.QueryRowContext(ctx, `SELECT id,host,external_id,login FROM collaboration_actors WHERE id=$1`, id).Scan(&ref.ID, &ref.Host, &ref.ExternalID, &ref.Login)
	return ref, err
}

// ObserveCollaborationTeam records an observed provider team, not membership or
// current access. An empty display name preserves known metadata. The shared
// allocation gate runs before evaluating the independent team's ID sequence.
func (s *Store) ObserveCollaborationTeam(ctx context.Context, host collaboration.HostID, externalID, name string) (collaboration.TeamRef, error) {
	var ref collaboration.TeamRef
	if !collaboration.ValidID(string(host)) || !collaboration.ValidText(externalID, 512) || name != "" && !collaboration.ValidText(name, 512) {
		return ref, errors.New("invalid collaboration team observation")
	}
	err := s.observeCollaborationIdentity(ctx, `INSERT INTO collaboration_teams(host,external_id,name) VALUES($1,$2,COALESCE(NULLIF($3,''),$2))
 ON CONFLICT(host,external_id) DO UPDATE SET name=CASE WHEN $3='' THEN collaboration_teams.name ELSE excluded.name END RETURNING id,host,external_id,name`, []any{host, externalID, name}, &ref.ID, &ref.Host, &ref.ExternalID, &ref.Name)
	return ref, registryWriteError(err)
}

func (s *Store) CollaborationTeam(ctx context.Context, id collaboration.TeamID) (collaboration.TeamRef, error) {
	var ref collaboration.TeamRef
	err := s.db.QueryRowContext(ctx, `SELECT id,host,external_id,name FROM collaboration_teams WHERE id=$1`, id).Scan(&ref.ID, &ref.Host, &ref.ExternalID, &ref.Name)
	return ref, err
}

func (s *Store) PutCollaborationConnection(ctx context.Context, ref collaboration.ConnectionRef) error {
	if !collaboration.ValidID(string(ref.ID)) || !collaboration.ValidID(string(ref.Profile)) || !collaboration.ValidID(string(ref.Host)) || ref.Actor < 1 {
		return errors.New("invalid collaboration connection")
	}
	var id string
	err := s.db.QueryRowContext(ctx, `INSERT INTO collaboration_connections(id,profile,host,actor) VALUES($1,$2,$3,$4)
 ON CONFLICT(id) DO UPDATE SET id=excluded.id WHERE collaboration_connections.profile=excluded.profile
 AND collaboration_connections.host=excluded.host AND collaboration_connections.actor=excluded.actor RETURNING id`, ref.ID, ref.Profile, ref.Host, ref.Actor).Scan(&id)
	return registryWriteError(err)
}

func (s *Store) CollaborationConnection(ctx context.Context, id collaboration.ConnectionID) (collaboration.ConnectionRef, error) {
	var ref collaboration.ConnectionRef
	err := s.db.QueryRowContext(ctx, `SELECT id,profile,host,actor FROM collaboration_connections WHERE id=$1`, id).Scan(&ref.ID, &ref.Profile, &ref.Host, &ref.Actor)
	return ref, err
}

// AssociateCollaborationRepository records which connection observed a repo.
// The relation is not an access grant; current provider permissions are separate.
func (s *Store) AssociateCollaborationRepository(ctx context.Context, repository collaboration.RepositoryID, connection collaboration.ConnectionID) error {
	result, err := s.db.ExecContext(ctx, `INSERT INTO collaboration_repository_connections(repository,connection,host)
 SELECT r.id,c.id,r.host FROM collaboration_repositories r JOIN collaboration_connections c ON c.host=r.host
 WHERE r.id=$1 AND c.id=$2 ON CONFLICT(repository,connection) DO UPDATE SET host=excluded.host`, repository, connection)
	if err != nil {
		return registryWriteError(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return collaboration.ErrIdentityConflict
	}
	return nil
}

func (s *Store) PutCollaborationRuntime(ctx context.Context, ref collaboration.RuntimeRef) error {
	if !collaboration.ValidID(string(ref.ID)) || !collaboration.ValidID(string(ref.Profile)) || !collaboration.ValidText(ref.Name, 120) || (ref.Kind != collaboration.Native && ref.Kind != collaboration.Appliance) {
		return errors.New("invalid collaboration runtime")
	}
	var id string
	err := s.db.QueryRowContext(ctx, `INSERT INTO collaboration_runtimes(id,profile,kind,name) VALUES($1,$2,$3,$4)
 ON CONFLICT(id) DO UPDATE SET name=excluded.name WHERE collaboration_runtimes.profile=excluded.profile
 AND collaboration_runtimes.kind=excluded.kind RETURNING id`, ref.ID, ref.Profile, ref.Kind, ref.Name).Scan(&id)
	return registryWriteError(err)
}

func (s *Store) CollaborationRuntime(ctx context.Context, id collaboration.RuntimeID) (collaboration.RuntimeRef, error) {
	var ref collaboration.RuntimeRef
	err := s.db.QueryRowContext(ctx, `SELECT id,profile,kind,name FROM collaboration_runtimes WHERE id=$1`, id).Scan(&ref.ID, &ref.Profile, &ref.Kind, &ref.Name)
	return ref, err
}

func registryWriteError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return collaboration.ErrIdentityConflict
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		if pg.ConstraintName == "collaboration_ledger_unbound" {
			return collaboration.ErrUnboundLedger
		}
		if pg.Code == "23505" || pg.Code == "23503" || pg.Code == "P0001" {
			return collaboration.ErrIdentityConflict
		}
	}
	return err
}
