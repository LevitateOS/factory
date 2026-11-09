package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

func initializeFactoryPlacementSchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRowContext(ctx, `SELECT version FROM schema_version FOR UPDATE`).Scan(&version); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS factory_placements(
 repository BIGINT PRIMARY KEY, host TEXT NOT NULL, runtime TEXT NOT NULL REFERENCES collaboration_runtimes(id),
 generation BIGINT NOT NULL CHECK(generation>0), state TEXT NOT NULL CHECK(state IN ('inactive','active','retiring')),
 selected_by BIGINT NOT NULL, revision TEXT NOT NULL, data JSONB NOT NULL,
 FOREIGN KEY(repository,host) REFERENCES collaboration_repositories(id,host),
 FOREIGN KEY(selected_by,host) REFERENCES collaboration_actors(id,host))`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Placement(ctx context.Context, repository collaboration.RepositoryID) (factory.Placement, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_placements WHERE repository=$1`, repository).Scan(&raw)
	var p factory.Placement
	if err == nil {
		err = json.Unmarshal(raw, &p)
		if err == nil {
			err = p.Validate()
		}
	}
	return p, err
}

// ObserveFountainPlacement retains a fresh central observation for this native
// profile. It never selects a host runtime or grants execution/publication.
// The native connector verifies the exact enrollment owner before calling it;
// dispatch still needs an original host claim and current operation grant.
func (s *Store) ObserveFountainPlacement(ctx context.Context, p factory.Placement, profile collaboration.ProfileID) error {
	if p.Validate() != nil || p.Coordination != factory.FountainCentral || string(p.Runtime) != string(profile) {
		return factory.ErrPlacement
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, _, _, err = ensureDispatchGateTx(ctx, tx, int64(p.Repository)); err != nil {
		return err
	}
	var provider collaboration.Provider
	if err = tx.QueryRowContext(ctx, `SELECT h.provider FROM collaboration_repositories r JOIN collaboration_hosts h ON h.id=r.host WHERE r.id=$1 AND r.host=$2`, p.Repository, p.Host).Scan(&provider); err != nil {
		return err
	}
	if provider != collaboration.Fountain {
		return factory.ErrPlacement
	}
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT data FROM factory_placements WHERE repository=$1 FOR UPDATE`, p.Repository).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		var prior factory.Placement
		if json.Unmarshal(raw, &prior) != nil || prior.Validate() != nil || prior.Coordination != factory.FountainCentral || p.Generation < prior.Generation || p.Runtime != prior.Runtime {
			return factory.ErrPlacement
		}
		if p.Generation == prior.Generation {
			rank := func(state factory.PlacementState) int {
				switch state {
				case factory.PlacementActive:
					return 0
				case factory.PlacementRetiring:
					return 1
				default:
					return 2
				}
			}
			if rank(p.State) < rank(prior.State) || p.State == prior.State && p.Revision != prior.Revision {
				return factory.ErrPlacement
			}
		} else if err = placementQuiescentTx(ctx, tx, p.Repository); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO collaboration_runtimes(id,profile,kind,name) VALUES($1,$2,'native','This computer') ON CONFLICT(id) DO NOTHING`, p.Runtime, profile); err != nil {
		return err
	}
	var matches bool
	if err = tx.QueryRowContext(ctx, `SELECT profile=$2 AND kind='native' FROM collaboration_runtimes WHERE id=$1`, p.Runtime, profile).Scan(&matches); err != nil || !matches {
		return factory.ErrPlacement
	}
	if err = saveFactoryPlacementTx(ctx, tx, p); err != nil {
		return err
	}
	return tx.Commit()
}

// SelectPlacement serializes with dispatch, grant changes and publication. The
// caller supplies current administration; a stored selection grants no spending
// or credentials and is not a cross-installation GitHub lease.
func (s *Store) SelectPlacement(ctx context.Context, host collaboration.HostID, repository collaboration.RepositoryID, actor collaboration.ActorID, runtime collaboration.RuntimeRef, expected collaboration.Revision, state factory.PlacementState) (factory.Placement, error) {
	var empty factory.Placement
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if _, _, _, err = ensureDispatchGateTx(ctx, tx, int64(repository)); err != nil {
		return empty, err
	}
	// Native desktop placement is local and GitHub-only. Fountain central CAS
	// stays with its owning gateway rather than accepting a local substitute.
	var provider collaboration.Provider
	if err = tx.QueryRowContext(ctx, `SELECT h.provider FROM collaboration_repositories r JOIN collaboration_hosts h ON h.id=r.host WHERE r.id=$1 AND r.host=$2`, repository, host).Scan(&provider); err != nil {
		return empty, err
	}
	if provider != collaboration.GitHub || runtime.Kind != collaboration.Native || !collaboration.ValidID(string(runtime.ID)) || !collaboration.ValidID(string(runtime.Profile)) || !collaboration.ValidText(runtime.Name, 120) {
		return empty, factory.ErrPlacement
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO collaboration_runtimes(id,profile,kind,name) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO NOTHING`, runtime.ID, runtime.Profile, runtime.Kind, runtime.Name); err != nil {
		return empty, err
	}
	var matches bool
	if err = tx.QueryRowContext(ctx, `SELECT profile=$2 AND kind=$3 FROM collaboration_runtimes WHERE id=$1`, runtime.ID, runtime.Profile, runtime.Kind).Scan(&matches); err != nil {
		return empty, err
	}
	if !matches {
		return empty, factory.ErrPlacement
	}
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT data FROM factory_placements WHERE repository=$1 FOR UPDATE`, repository).Scan(&raw)
	var prior factory.Placement
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return empty, err
	}
	if err == nil {
		if json.Unmarshal(raw, &prior) != nil || prior.Validate() != nil || prior.Revision != expected {
			return empty, factory.ErrPlacement
		}
	} else if expected != "" {
		return empty, factory.ErrPlacement
	}
	p := factory.Placement{Host: host, Repository: repository, Runtime: runtime.ID, Generation: 1, State: state, Coordination: factory.LocalSingleController, SelectedBy: actor, Revision: collaboration.Revision(collaboration.NewID()), UpdatedAt: time.Now().UTC()}
	if prior.Generation == 0 {
		if state != factory.PlacementActive {
			return empty, factory.ErrPlacement
		}
		if err = placementQuiescentTx(ctx, tx, repository); err != nil {
			return empty, err
		}
	} else {
		p.Generation = prior.Generation
		switch {
		case prior.State == factory.PlacementActive && state == factory.PlacementRetiring && prior.Runtime == p.Runtime:
		case prior.State == factory.PlacementRetiring && state == factory.PlacementInactive && prior.Runtime == p.Runtime:
			if err = placementQuiescentTx(ctx, tx, repository); err != nil {
				return empty, err
			}
		case prior.State == factory.PlacementInactive && state == factory.PlacementActive && prior.Generation < math.MaxInt64:
			if err = placementQuiescentTx(ctx, tx, repository); err != nil {
				return empty, err
			}
			p.Generation++
		default:
			return empty, factory.ErrPlacement
		}
	}
	if p.Validate() != nil {
		return empty, factory.ErrPlacement
	}
	if err = saveFactoryPlacementTx(ctx, tx, p); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return p, nil
}

func saveFactoryPlacementTx(ctx context.Context, tx *sql.Tx, p factory.Placement) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO factory_placements(repository,host,runtime,generation,state,selected_by,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7,$8)
 ON CONFLICT(repository) DO UPDATE SET runtime=excluded.runtime,generation=excluded.generation,state=excluded.state,selected_by=excluded.selected_by,revision=excluded.revision,data=excluded.data`, p.Repository, p.Host, p.Runtime, p.Generation, p.State, p.SelectedBy, p.Revision, string(raw))
	return err
}

func placementQuiescentTx(ctx context.Context, tx *sql.Tx, repository collaboration.RepositoryID) error {
	var pending bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_runs r JOIN factory_run_views v ON v.run=r.id WHERE v.repository=$1 AND
 (r.active OR NOT r.settled OR (NULLIF(r.data->'native','null'::jsonb) IS NOT NULL AND
 (r.data#>>'{native,retirement}' IS DISTINCT FROM 'retired' OR r.data#>>'{native,sealed}' IS DISTINCT FROM 'true' OR COALESCE(r.data#>>'{native,provenance,custody,state}','') NOT IN ('released','not_acquired')))))
 OR EXISTS(SELECT 1 FROM factory_publications WHERE repository=$1 AND
 (stage IN ('open','fenced') OR jsonb_path_exists(data,'$.**.completion ? (@ == "pending" || @ == "needs_intervention")')))
 OR EXISTS(SELECT 1 FROM project_preparations p WHERE
 (p.data#>>'{factory_admission,repository}'=$1::text OR p.project_id IN (SELECT data->>'project' FROM project_environment_grants WHERE repository=$1))
 AND NULLIF(p.data->'native','null'::jsonb) IS NOT NULL
 AND NOT soda_preparation_quiescent(p.data,p.id,p.project_id,p.role))`, repository).Scan(&pending)
	if err != nil {
		return err
	}
	if pending {
		return factory.ErrPlacement
	}
	return nil
}

// The caller holds the repository dispatch gate for the whole admission.
func requirePlacementTx(ctx context.Context, tx *sql.Tx, host collaboration.HostID, repository collaboration.RepositoryID, runtime collaboration.RuntimeID, generation int64, profile collaboration.ProfileID) error {
	var raw []byte
	var runtimeProfile collaboration.ProfileID
	err := tx.QueryRowContext(ctx, `SELECT p.data,r.profile FROM factory_placements p JOIN collaboration_runtimes r ON r.id=p.runtime WHERE p.repository=$1 FOR SHARE OF p`, repository).Scan(&raw, &runtimeProfile)
	if err != nil {
		return admissionChanged(err)
	}
	var p factory.Placement
	if json.Unmarshal(raw, &p) != nil || runtimeProfile != profile || p.RequireDispatch(host, repository, runtime, generation) != nil {
		return factory.ErrPlacement
	}
	return nil
}
