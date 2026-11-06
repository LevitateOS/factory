package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

type grantCommandSQL interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// FactoryAuthorityInput returns the current grant and preparation inputs used
// by both visible authority reads and atomic grant changes.
func (s *Store) FactoryAuthorityInput(ctx context.Context, repository int64) (factory.AuthorityInput, error) {
	return factoryAuthorityInput(ctx, s.db, repository)
}

// loadAuthorityGrantRows loads the policy, operator and appliance
// capacity grants behind one authority evaluation, tolerating absent
// rows.
func loadAuthorityGrantRows(ctx context.Context, q grantCommandSQL, repository int64, in *factory.AuthorityInput) error {
	var policy factory.RepositoryPolicy
	if err := loadGrantQuery(ctx, q, "factory_policies", "repository", repository, &policy); err == nil {
		in.Policy = &policy
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	var operator factory.OperatorGrant
	if err := loadGrantQuery(ctx, q, "factory_operator_grants", "repository", repository, &operator); err == nil {
		in.Operator = &operator
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	var capacity factory.Capacity
	if err := loadGrantQuery(ctx, q, "factory_capacity", "id", 1, &capacity); err == nil {
		in.Appliance = &capacity
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// pickAuthoritySponsorship selects the active sponsorship behind one
// authority evaluation, falling back to the first recorded sponsorship.
func pickAuthoritySponsorship(ctx context.Context, q grantCommandSQL, repository int64, in *factory.AuthorityInput) error {
	sponsorships, err := sponsorshipsQuery(ctx, q, repository)
	if err != nil {
		return err
	}
	for i := range sponsorships {
		if sponsorships[i].Active {
			in.Sponsorship = &sponsorships[i]
			break
		}
	}
	if in.Sponsorship == nil && len(sponsorships) > 0 {
		in.Sponsorship = &sponsorships[0]
	}
	return nil
}

// loadAuthorityProjectReadiness resolves project existence, the
// maintenance hold and coder/reviewer preparation readiness behind
// one authority evaluation.
func loadAuthorityProjectReadiness(ctx context.Context, q grantCommandSQL, repository int64, in *factory.AuthorityInput) error {
	var environment project.EnvironmentGrant
	if err := loadGrantQuery(ctx, q, "project_environment_grants", "repository", repository, &environment); err == nil {
		in.Environment = &environment
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if environment.Native != nil {
		var present bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_native_workspaces WHERE id=$1 AND repository=$2 AND runtime=$3 AND profile=$4)`, environment.Project, repository, environment.Native.Runtime, environment.Native.Profile).Scan(&present); err != nil {
			return err
		}
		in.ProjectExists = present
		if present {
			encoded, err := json.Marshal(environment.Native)
			if err != nil {
				return err
			}
			var approved bool
			if err = q.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM project_requirement_heads rh JOIN project_requirement_decisions r ON r.id=rh.decision
 JOIN project_approval_heads ah ON ah.project_id=rh.project_id JOIN project_approval_decisions a ON a.id=ah.decision
 JOIN factory_placements p ON p.repository=$2
 WHERE rh.project_id=$1 AND r.data->'native'=$3::jsonb AND a.data->'native'=$3::jsonb
 AND a.data->>'requirement'=r.id AND a.data->>'verified'='true'
 AND p.state='active' AND p.runtime=$4 AND p.generation=$5)`, environment.Project, repository, string(encoded), environment.Native.Runtime, environment.Native.Generation).Scan(&approved); err != nil {
				return err
			}
			if !approved {
				return nil
			}
			return scanReadyPreparations(ctx, q, environment.Project, in, environment.Native)
		}
		return nil
	}
	projectRecord, err := scanProject(q.QueryRowContext(ctx, `SELECT `+nativeProjectCollaborationColumns+nativeProjectCollaborationJoin+` WHERE r.id=$1`, repository))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err == nil {
		in.ProjectExists = true
		maintenanceHeld := false
		var holdData []byte
		err = q.QueryRowContext(ctx, `SELECT data FROM project_maintenance WHERE project_id=$1`, projectRecord.ID).Scan(&holdData)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err == nil {
			var hold project.MaintenanceHold
			if err = json.Unmarshal(holdData, &hold); err != nil {
				return err
			}
			if hold.Hold {
				maintenanceHeld = true
			}
		}
		if !maintenanceHeld {
			if err = scanReadyPreparations(ctx, q, projectRecord.ID, in, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// scanReadyPreparations marks coder/reviewer preparation readiness
// from recorded preparations.
func scanReadyPreparations(ctx context.Context, q grantCommandSQL, projectID string, in *factory.AuthorityInput, native *project.NativeEnvironment) error {
	rows, err := q.QueryContext(ctx, `SELECT data FROM project_preparations WHERE project_id=$1 ORDER BY id LIMIT 129`, projectID)
	if err != nil {
		return err
	}
	ready := map[string]bool{}
	for rows.Next() {
		var data []byte
		var preparation execution.PreparationRecord
		if err = rows.Scan(&data); err != nil {
			_ = rows.Close()
			return err
		}
		if err = json.Unmarshal(data, &preparation); err != nil {
			_ = rows.Close()
			return err
		}
		if err = preparation.Validate(); err != nil {
			_ = rows.Close()
			return err
		}
		if preparation.State.Ready && !preparation.State.Stopped &&
			(native == nil && preparation.Native == nil || native != nil && preparation.Native != nil && preparation.Native.Profile == native.Profile) {
			ready[preparation.Preparation.Role] = true
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	in.PreparationReady = ready[project.RoleCoder] && ready[project.RoleReviewer]
	return nil
}

func factoryAuthorityInput(ctx context.Context, q grantCommandSQL, repository int64) (factory.AuthorityInput, error) {
	var in factory.AuthorityInput
	if err := loadAuthorityGrantRows(ctx, q, repository, &in); err != nil {
		return in, err
	}
	if err := pickAuthoritySponsorship(ctx, q, repository, &in); err != nil {
		return in, err
	}
	if err := loadAuthorityProjectReadiness(ctx, q, repository, &in); err != nil {
		return in, err
	}
	open, _, _, err := dispatchStateQuery(ctx, q, repository)
	if err != nil {
		return in, err
	}
	in.DispatchOpen = open
	return in, nil
}

// grantChangeScope binds a change to its repository, expected revision and
// withdrawal cause.
type grantChangeScope struct {
	repository int64
	revision   int64
	withdraw   bool
	cause      string
}

// resolveGrantChangeScope identifies the gate and withdrawal terms for a change.
func resolveGrantChangeScope(change any) (grantChangeScope, error) {
	var resolved grantChangeScope
	switch g := change.(type) {
	case factory.RepositoryPolicy:
		resolved.repository, resolved.revision = g.Repository, g.Revision
		resolved.withdraw, resolved.cause = !g.Enabled || g.Paused, "policy_disabled"
		if g.Paused {
			resolved.cause = "policy_paused"
		}
	case factory.Capacity:
		resolved.revision = g.Revision
	case factory.OperatorGrant:
		resolved.repository, resolved.revision = g.Repository, g.Revision
		resolved.withdraw, resolved.cause = !g.Active, "operator_grant_withdrawn"
	case factory.Sponsorship:
		resolved.repository, resolved.revision = g.Repository, g.Revision
		resolved.cause = "sponsorship_withdrawn"
	case project.EnvironmentGrant:
		resolved.repository, resolved.revision = g.Repository, g.Revision
		resolved.withdraw, resolved.cause = !g.Active, "environment_grant_withdrawn"
	default:
		return resolved, errors.New("unsupported factory grant")
	}
	return resolved, nil
}

// lockGrantScopeTx locks the repository gate or the appliance
// capacity row for a grant change, matching dispatch lock
// order.
func lockGrantScopeTx(ctx context.Context, tx *sql.Tx, repository int64) error {
	if repository > 0 {
		_, _, _, err := ensureDispatchGateTx(ctx, tx, repository)
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_capacity(id,revision,data) VALUES(1,0,'{}'::jsonb) ON CONFLICT(id) DO NOTHING`); err != nil {
		return err
	}
	var locked int64
	return tx.QueryRowContext(ctx, `SELECT revision FROM factory_capacity WHERE id=1 FOR UPDATE`).Scan(&locked)
}

// refreshSponsorshipWithdrawTx clears a sponsorship withdrawal when
// another active connection remains in the repository.
func refreshSponsorshipWithdrawTx(ctx context.Context, tx *sql.Tx, change any, withdraw bool) (bool, error) {
	sp, ok := change.(factory.Sponsorship)
	if !ok || sp.Active {
		return withdraw, nil
	}
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_sponsorships
		WHERE repository=$1 AND connection<>$2 AND COALESCE((data->>'active')::boolean,FALSE))`, sp.Repository, sp.Connection).Scan(&active); err != nil {
		return withdraw, err
	}
	return !active, nil
}

// settleGrantWithdrawalTx records dispatch closure and publication/merge
// cancellation intents in the grant transaction. A save that withdraws nothing
// (a changed number, a re-enabled grant) leaves open work alone.
func settleGrantWithdrawalTx(ctx context.Context, tx *sql.Tx, repository int64, withdraw bool, cause, principal string, result *factory.GrantResult) error {
	if repository <= 0 || !withdraw {
		return nil
	}
	_, err := withdrawDispatchTx(ctx, tx, repository, cause, principal)
	if err != nil {
		return err
	}
	if result.PublicationsPending, err = requestPublicationWithdrawalsTx(ctx, tx, repository); err != nil {
		return err
	}
	if result.MergesPending, err = requestMergeWithdrawalsTx(ctx, tx, repository); err != nil {
		return err
	}
	return nil
}

// ApplyFactoryGrant saves a settings change under its expected revision. Any
// dispatch closure and native cancellation intents commit in this transaction.
func (s *Store) ApplyFactoryGrant(ctx context.Context, principal string, change any) (factory.GrantResult, error) {
	var result factory.GrantResult
	scope, err := resolveGrantChangeScope(change)
	if err != nil {
		return result, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = lockGrantScopeTx(ctx, tx, scope.repository); err != nil {
		return result, err
	}
	if g, ok := change.(project.EnvironmentGrant); ok && g.Native != nil {
		g, err = bindNativeEnvironmentTx(ctx, tx, g)
		if err != nil {
			return result, err
		}
		change = g
	}
	if err = saveFactoryGrantChangeTx(ctx, tx, change); err != nil {
		return result, err
	}
	withdraw, err := refreshSponsorshipWithdrawTx(ctx, tx, change, scope.withdraw)
	if err != nil {
		return result, err
	}
	result = factory.GrantResult{Revision: scope.revision + 1, Withdrawn: withdraw}
	if err = settleGrantWithdrawalTx(ctx, tx, scope.repository, withdraw, scope.cause, principal, &result); err != nil {
		return result, err
	}
	input, err := factoryAuthorityInput(ctx, tx, scope.repository)
	if err != nil {
		return result, err
	}
	result.Effective = factory.EvaluateAuthority(input)
	return result, tx.Commit()
}

func saveFactoryGrantChangeTx(ctx context.Context, tx *sql.Tx, change any) error {
	switch g := change.(type) {
	case factory.RepositoryPolicy:
		if err := g.Validate(); err != nil {
			return err
		}
		next := g
		next.Revision++
		return saveRevisionedGrantQuery(ctx, tx, "factory_policies", "repository", g.Repository, g.Revision, next, "repository policy")
	case factory.Capacity:
		if err := g.Validate(); err != nil {
			return err
		}
		next := g
		next.Revision++
		return saveRevisionedGrantQuery(ctx, tx, "factory_capacity", "id", 1, g.Revision, next, "appliance capacity")
	case factory.OperatorGrant:
		if err := g.Validate(); err != nil {
			return err
		}
		next := g
		next.Revision++
		return saveRevisionedGrantQuery(ctx, tx, "factory_operator_grants", "repository", g.Repository, g.Revision, next, "operator grant")
	case factory.Sponsorship:
		if err := g.Validate(); err != nil {
			return err
		}
		next := g
		next.Revision++
		return saveSponsorshipQuery(ctx, tx, g, next)
	case project.EnvironmentGrant:
		if err := g.Validate(); err != nil {
			return err
		}
		next := g
		next.Revision++
		return saveRevisionedGrantQuery(ctx, tx, "project_environment_grants", "repository", g.Repository, g.Revision, next, "environment grant")
	default:
		return errors.New("unsupported factory grant")
	}
}

func saveRevisionedGrantQuery(ctx context.Context, q grantCommandSQL, table, key string, id any, revision int64, next any, what string) error {
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	result, err := q.ExecContext(ctx, `INSERT INTO `+table+`(`+key+`,revision,data) VALUES($1,$2,$3)
		ON CONFLICT(`+key+`) DO UPDATE SET revision=$4,data=$5 WHERE `+table+`.revision=$6`,
		id, revision+1, string(data), revision+1, string(data), revision)
	return checkGrantRowsAffected(result, err, what+" save failed")
}

func saveSponsorshipQuery(ctx context.Context, q grantCommandSQL, current, next factory.Sponsorship) error {
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	result, err := q.ExecContext(ctx, `INSERT INTO factory_sponsorships(repository,connection,revision,data) VALUES($1,$2,$3,$4)
		ON CONFLICT(repository,connection) DO UPDATE SET revision=$5,data=$6 WHERE factory_sponsorships.revision=$7`,
		current.Repository, current.Connection, next.Revision, string(data), next.Revision, string(data), current.Revision)
	return checkGrantRowsAffected(result, err, "sponsorship save failed")
}

func checkGrantRowsAffected(result sql.Result, err error, what string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStaleRevision
	}
	return nil
}

func recordFactoryCommandTx(ctx context.Context, tx *sql.Tx, cmd factory.Command, now time.Time) (factory.Command, bool, error) {
	if err := cmd.Validate(); err != nil {
		return factory.Command{}, false, err
	}
	if cmd.Outcome != "" {
		return factory.Command{}, false, errors.New("new command must not carry an outcome")
	}
	created := now.UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO NOTHING`, cmd.ID, cmd.Type, cmd.Target, cmd.Principal, cmd.Digest, cmd.Payload, created)
	if err != nil {
		return factory.Command{}, false, fmt.Errorf("factory command record failed: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return factory.Command{}, false, err
	}
	var stored factory.Command
	var storedCreated, finished string
	err = tx.QueryRowContext(ctx, `SELECT id,type,target,principal,digest,payload,outcome,created,finished FROM factory_commands WHERE id=$1 FOR UPDATE`, cmd.ID).Scan(&stored.ID, &stored.Type, &stored.Target, &stored.Principal, &stored.Digest, &stored.Payload, &stored.Outcome, &storedCreated, &finished)
	if err != nil {
		return factory.Command{}, false, err
	}
	stored.Created, stored.Finished = storedCreated, finished
	if stored.Digest != cmd.Digest {
		return factory.Command{}, false, ErrCommandConflict
	}
	return stored, n == 1, nil
}

func finishFactoryCommandTx(ctx context.Context, tx *sql.Tx, id string, now time.Time) error {
	finished := now.UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `UPDATE factory_commands SET finished=$1 WHERE id=$2 AND finished=''`, finished, id)
	return checkGrantRowsAffected(result, err, "factory command finish failed")
}
