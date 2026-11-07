package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// The repository gate is held by the caller. Capacity and allowance size govern
// future admission; source/role withdrawal governs already admitted effects.
func runConsentTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, run factory.Run) (bool, error) {
	var policy factory.RepositoryPolicy
	var operator factory.OperatorGrant
	var environment project.EnvironmentGrant
	var source factory.Sponsorship
	for _, load := range []func() error{
		func() error { return loadGrantQuery(ctx, tx, "factory_policies", "repository", a.Repository, &policy) },
		func() error {
			return loadGrantQuery(ctx, tx, "factory_operator_grants", "repository", a.Repository, &operator)
		},
		func() error {
			return loadGrantQuery(ctx, tx, "project_environment_grants", "repository", a.Repository, &environment)
		},
	} {
		if err := load(); err != nil {
			return false, err
		}
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_sponsorships WHERE repository=$1 AND connection=$2`, a.Repository, a.Connection).Scan(&raw); err != nil {
		return false, err
	}
	if json.Unmarshal(raw, &source) != nil || source.Validate() != nil || policy.Validate() != nil || operator.Validate() != nil || environment.Validate() != nil {
		return false, ErrAdmissionChanged
	}
	role := false
	for _, candidate := range source.Roles {
		role = role || candidate == a.Role
	}
	if !policy.Enabled || policy.Paused || !operator.Active || !environment.Active || !source.Active || !role ||
		policy.Roles[a.Role].Account != a.Connection || source.ActorID != a.ActorID || source.ProjectID != a.ProjectID || environment.Project != a.ProjectID {
		return false, nil
	}
	if run.Native != nil {
		if run.Admission == nil || run.Admission.Native == nil {
			return false, ErrAdmissionChanged
		}
		native := run.Admission.Native
		if source.Native == nil || environment.Native == nil || source.Native.Profile != native.Workspace.Profile || environment.Native.Profile != native.Workspace.Profile ||
			environment.Native.Runtime != native.Runtime || environment.Native.Generation != native.PlacementGeneration ||
			!native.MatchesAuthentication(source.Native.Authentication, source.Native.CredentialSHA256) {
			return false, nil
		}
	} else if source.Native != nil || environment.Native != nil {
		return false, nil
	}
	return true, nil
}

// Revocation is the existing durable stop intent. Re-enabling a source never
// reactivates an old capability. This runs in the grant transaction, before any
// native cancellation, so later calls cannot spend through another source.
func revokeUnconsentedRunsTx(ctx context.Context, tx *sql.Tx, repository int64) error {
	if repository <= 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.data,r.data FROM factory_assignments a
JOIN factory_run_views v ON v.attempt=a.id JOIN factory_runs r ON r.id=v.run
WHERE a.repository=$1 AND a.run=r.id AND NOT r.settled ORDER BY r.seq LIMIT $2`, repository, MaxHeldReservations+1)
	if err != nil {
		return err
	}
	type recorded struct {
		assignment factory.Assignment
		run        factory.Run
	}
	var runs []recorded
	for rows.Next() {
		var a, r []byte
		if err = rows.Scan(&a, &r); err != nil {
			rows.Close()
			return err
		}
		var item recorded
		if json.Unmarshal(a, &item.assignment) != nil || json.Unmarshal(r, &item.run) != nil || item.assignment.Validate() != nil || item.run.Validate() != nil {
			rows.Close()
			return ErrAdmissionChanged
		}
		runs = append(runs, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(runs) > MaxHeldReservations {
		return errors.New("run consent inventory exceeds admitted capacity")
	}
	var open bool
	if err = tx.QueryRowContext(ctx, `SELECT open FROM factory_dispatch WHERE repository=$1`, repository).Scan(&open); err != nil {
		return err
	}
	for _, item := range runs {
		consent, err := runConsentTx(ctx, tx, item.assignment, item.run)
		if err != nil {
			return err
		}
		if !open || !consent {
			if _, err = tx.ExecContext(ctx, `UPDATE factory_mcp_capabilities SET state=$1
WHERE data->>'run_id'=$2 AND data->>'assignment_id'=$3 AND state IN ($4,$5)`, factory.MCPCapabilityRevoked, item.run.ID, item.assignment.ID, factory.MCPCapabilityStaged, factory.MCPCapabilityActive); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) RevokedFactoryRuns(ctx context.Context, repository int64, limit int) ([]factory.Run, error) {
	if repository < 0 || limit < 1 || limit > MaxHeldReservations {
		return nil, ErrAdmissionChanged
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.data FROM factory_runs r WHERE NOT r.settled
AND EXISTS(SELECT 1 FROM factory_mcp_capabilities c WHERE c.data->>'run_id'=r.id AND c.state=$1
AND ($2::bigint=0 OR (c.data->>'repository')::bigint=$2)) ORDER BY r.seq LIMIT $3`, factory.MCPCapabilityRevoked, repository, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []factory.Run
	for rows.Next() {
		var raw []byte
		var run factory.Run
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &run) != nil || run.Validate() != nil {
			return nil, ErrAdmissionChanged
		}
		out = append(out, run)
	}
	return out, rows.Err()
}
