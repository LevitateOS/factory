package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// The repository gate is held by the caller. Capacity and allowance size govern
// future admission; source/role withdrawal governs already admitted effects.
func runConsentTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, run factory.Run) (bool, error) {
	if allowed, err := issueMakerConsentTx(ctx, tx, a); err != nil || !allowed {
		return allowed, err
	}
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

// The issuer-authenticated scope is recorded beside its opaque provider proof.
// Admission and every worker call check it under the source's row locks; a
// historical schema/profile or a later proof never grants an old worker access.
func issueMakerConsentTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) (bool, error) {
	if a.PlanningContext == nil || a.PlanningContext.IssueMaker == nil {
		return true, nil
	}
	scope := *a.PlanningContext.IssueMaker
	var body, author string
	var revision, repository int64
	var closed bool
	var grantRaw, progressRaw []byte
	// Match source capture/retry and progress: source, role post, then intake.
	// A join's planner-selected row order cannot provide that lock ordering.
	err := tx.QueryRowContext(ctx, `SELECT body,author,revision,repository,COALESCE((data->>'deleted')::boolean,false) OR COALESCE((data->>'revoked')::boolean,false) FROM factory_feed_posts WHERE id=$1 FOR SHARE`, scope.SourcePost).Scan(&body, &author, &revision, &repository, &closed)
	if err == nil {
		err = tx.QueryRowContext(ctx, `SELECT data->'issue_maker' FROM factory_feed_posts WHERE id=$1 FOR SHARE`, scope.Post).Scan(&progressRaw)
	}
	if err == nil {
		err = tx.QueryRowContext(ctx, `SELECT data->'issue_intake_scope' FROM factory_post_intakes WHERE post=$1 AND revision=$2 AND data ? 'issue_intake_grant' AND data->'issue_intake_grant' != 'null'::jsonb FOR SHARE`, scope.SourcePost, revision).Scan(&grantRaw)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var grant factory.IssueIntakeScope
	var progress factory.IssueMakerProgress
	if scope.NotAfter <= time.Now().Unix() {
		return false, nil
	}
	if closed || revision != scope.Revision || repository != a.Repository || a.Subject.Intent == nil || author != strconv.FormatInt(a.Subject.Intent.OwnerID, 10) || json.Unmarshal(grantRaw, &grant) != nil || grant.Validate() != nil || grant.Source != scope.SourcePost || grant.Revision != revision || int64(grant.Repository) != repository || strconv.FormatInt(int64(grant.Actor), 10) != author || grant.Digest != factory.MCPInputDigest([]byte(body)) || grant.Expires <= time.Now().Unix() || json.Unmarshal(progressRaw, &progress) != nil || progress.Validate() != nil || progress.Scope != scope || progress.State == "failed" || progress.State == "completed" {
		return false, nil
	}
	if grant.Expires != scope.NotAfter {
		return false, nil
	}
	if scope.Attempt != "" {
		if scope.Attempt != a.ID {
			return false, nil
		}
		var command factory.Command
		if err := tx.QueryRowContext(ctx, `SELECT id,type,target,principal,payload,digest,finished,outcome FROM factory_commands WHERE id=$1`, scope.Attempt).Scan(&command.ID, &command.Type, &command.Target, &command.Principal, &command.Payload, &command.Digest, &command.Finished, &command.Outcome); err != nil {
			return false, err
		}
		if _, err := factory.ReadFeedPostRetryReceipt(command, scope.SourcePost, repository, author); err != nil {
			return false, nil
		}
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

// RevokeExpiredIssueMakerRuns uses the existing capability revocation as the
// durable stop intent. No worker is reauthorized when a source is continued.
func (s *Store) RevokeExpiredIssueMakerRuns(ctx context.Context, repository int64) error {
	if repository < 0 {
		return ErrAdmissionChanged
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT a.data FROM factory_assignments a JOIN factory_runs r ON r.id=a.run WHERE NOT r.settled AND ($1::bigint=0 OR a.repository=$1) AND a.data->'planning_context'->'IssueMaker' IS NOT NULL ORDER BY r.seq LIMIT $2`, repository, MaxHeldReservations+1)
	if err != nil {
		return err
	}
	var assignments []factory.Assignment
	for rows.Next() {
		var raw []byte
		var a factory.Assignment
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if json.Unmarshal(raw, &a) != nil || a.Validate() != nil {
			rows.Close()
			return ErrAdmissionChanged
		}
		assignments = append(assignments, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(assignments) > MaxHeldReservations {
		return ErrAdmissionChanged
	}
	for _, a := range assignments {
		allowed, err := issueMakerConsentTx(ctx, tx, a)
		if err != nil {
			return err
		}
		if !allowed {
			if _, err = tx.ExecContext(ctx, `UPDATE factory_mcp_capabilities SET state=$1 WHERE data->>'run_id'=$2 AND data->>'assignment_id'=$3 AND state IN ($4,$5)`, factory.MCPCapabilityRevoked, a.Run, a.ID, factory.MCPCapabilityStaged, factory.MCPCapabilityActive); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
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
