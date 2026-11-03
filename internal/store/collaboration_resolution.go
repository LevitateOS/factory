package store

import (
	"context"

	"github.com/levitateos/sodaos/internal/collaboration"
)

// FountainLedgerHost returns the explicitly adopted appliance namespace. An
// empty native profile or unadopted retained ledger has no such association.
func (s *Store) FountainLedgerHost(ctx context.Context) (collaboration.Host, error) {
	var host collaboration.Host
	err := s.db.QueryRowContext(ctx, `SELECT h.id,h.provider,h.api_base FROM collaboration_ledger l JOIN collaboration_hosts h ON h.id=l.fountain_host AND h.provider='fountain' WHERE l.singleton AND l.adopted`).Scan(&host.ID, &host.Provider, &host.APIBase)
	return host, err
}

// These are lookups of existing observations, never allocations, permission
// checks or credential selection. Both directions require the provider host;
// numeric equality, repository names and account logins cannot select it.
func (s *Store) CollaborationRepositoryOnHost(ctx context.Context, host collaboration.HostID, id collaboration.RepositoryID) (collaboration.RepositoryRef, error) {
	var ref collaboration.RepositoryRef
	if !collaboration.ValidID(string(host)) || id < 1 {
		return ref, collaboration.ErrIdentityConflict
	}
	err := s.db.QueryRowContext(ctx, `SELECT id,host,external_id,name FROM collaboration_repositories WHERE host=$1 AND id=$2`, host, id).Scan(&ref.ID, &ref.Host, &ref.ExternalID, &ref.Name)
	return ref, err
}

func (s *Store) CollaborationRepositoryByExternalID(ctx context.Context, host collaboration.HostID, externalID string) (collaboration.RepositoryRef, error) {
	var ref collaboration.RepositoryRef
	if !collaboration.ValidID(string(host)) || !collaboration.ValidText(externalID, 512) {
		return ref, collaboration.ErrIdentityConflict
	}
	err := s.db.QueryRowContext(ctx, `SELECT id,host,external_id,name FROM collaboration_repositories WHERE host=$1 AND external_id=$2`, host, externalID).Scan(&ref.ID, &ref.Host, &ref.ExternalID, &ref.Name)
	return ref, err
}

func (s *Store) CollaborationActorOnHost(ctx context.Context, host collaboration.HostID, id collaboration.ActorID) (collaboration.ActorRef, error) {
	var ref collaboration.ActorRef
	if !collaboration.ValidID(string(host)) || id < 1 {
		return ref, collaboration.ErrIdentityConflict
	}
	err := s.db.QueryRowContext(ctx, `SELECT id,host,external_id,login FROM collaboration_actors WHERE host=$1 AND id=$2`, host, id).Scan(&ref.ID, &ref.Host, &ref.ExternalID, &ref.Login)
	return ref, err
}

func (s *Store) CollaborationActorByExternalID(ctx context.Context, host collaboration.HostID, externalID string) (collaboration.ActorRef, error) {
	var ref collaboration.ActorRef
	if !collaboration.ValidID(string(host)) || !collaboration.ValidText(externalID, 512) {
		return ref, collaboration.ErrIdentityConflict
	}
	err := s.db.QueryRowContext(ctx, `SELECT id,host,external_id,login FROM collaboration_actors WHERE host=$1 AND external_id=$2`, host, externalID).Scan(&ref.ID, &ref.Host, &ref.ExternalID, &ref.Login)
	return ref, err
}

func (s *Store) CollaborationTeamOnHost(ctx context.Context, host collaboration.HostID, id collaboration.TeamID) (collaboration.TeamRef, error) {
	var ref collaboration.TeamRef
	if !collaboration.ValidID(string(host)) || id < 1 {
		return ref, collaboration.ErrIdentityConflict
	}
	err := s.db.QueryRowContext(ctx, `SELECT id,host,external_id,name FROM collaboration_teams WHERE host=$1 AND id=$2`, host, id).Scan(&ref.ID, &ref.Host, &ref.ExternalID, &ref.Name)
	return ref, err
}

func (s *Store) CollaborationTeamByExternalID(ctx context.Context, host collaboration.HostID, externalID string) (collaboration.TeamRef, error) {
	var ref collaboration.TeamRef
	if !collaboration.ValidID(string(host)) || !collaboration.ValidText(externalID, 512) {
		return ref, collaboration.ErrIdentityConflict
	}
	err := s.db.QueryRowContext(ctx, `SELECT id,host,external_id,name FROM collaboration_teams WHERE host=$1 AND external_id=$2`, host, externalID).Scan(&ref.ID, &ref.Host, &ref.ExternalID, &ref.Name)
	return ref, err
}

// Native Projects belong to the explicitly adopted appliance's namespace. Their
// repository_id and owner_id stay native IDs, even when a new observation gets
// a different local surrogate. Compare the native key as text: other providers'
// opaque IDs must never be coerced into native numbers or matched by name.
const nativeProjectCollaborationJoin = ` FROM projects p
 JOIN collaboration_ledger l ON l.singleton AND l.adopted
 JOIN collaboration_hosts h ON h.id=l.fountain_host AND h.provider='fountain'
 JOIN collaboration_repositories r ON r.host=h.id AND r.external_id=p.repository_id::text`

const nativeProjectCollaborationColumns = `p.id,p.name,p.repository_id,p.owner_id,p.repository,p.ip,p.ready,p.creation_profile`

// NativeProjectForCollaborationRepository resolves an existing association. A
// missing/unadopted/foreign mapping returns ErrNotFound; it does not prove remote
// absence, deny provider access, or authorize execution on the returned Project.
func (s *Store) NativeProjectForCollaborationRepository(ctx context.Context, repository collaboration.RepositoryID) (Project, error) {
	if repository < 1 {
		return Project{}, collaboration.ErrIdentityConflict
	}
	return scanProject(s.db.QueryRowContext(ctx, `SELECT `+nativeProjectCollaborationColumns+nativeProjectCollaborationJoin+` WHERE r.id=$1`, repository))
}

func (s *Store) CollaborationRepositoryForNativeProject(ctx context.Context, projectID string) (collaboration.RepositoryRef, error) {
	var ref collaboration.RepositoryRef
	if !collaboration.ValidText(projectID, 128) {
		return ref, collaboration.ErrIdentityConflict
	}
	err := s.db.QueryRowContext(ctx, `SELECT r.id,r.host,r.external_id,r.name`+nativeProjectCollaborationJoin+` WHERE p.id=$1`, projectID).Scan(&ref.ID, &ref.Host, &ref.ExternalID, &ref.Name)
	return ref, err
}
