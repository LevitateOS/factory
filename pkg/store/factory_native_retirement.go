package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
)

// RecordNativeContextNoStart consumes the startup owner's explicit no-start
// acknowledgement. ContextPending is cleared before any host launch, so it is
// positive unsent evidence; a missing process receipt alone never qualifies.
func (s *Store) RecordNativeContextNoStart(ctx context.Context, profile collaboration.ProfileID, intent string) error {
	bound, err := execution.ParseNativeIntent(intent)
	if err != nil || bound.Native.Workspace.Profile != profile {
		return ErrAdmissionChanged
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_runs WHERE id=$1 FOR UPDATE`, bound.Run).Scan(&raw); err != nil {
		return err
	}
	var run factory.Run
	if json.Unmarshal(raw, &run) != nil || run.Validate() != nil || run.ID != bound.Run || !run.ContextPending || run.HasExecutionIdentity() ||
		run.Native == nil || run.Native.Intent != intent || run.Native.Provenance.Profile != profile || run.Admission == nil || run.Admission.Native == nil ||
		!bound.Native.MatchesRecord(*run.Native) || *run.Admission.Native != bound.Native || run.Native.Provenance.Process != nil || run.Native.Provenance.Isolation != nil || run.Native.Provenance.Connector != nil ||
		run.Native.Provenance.Custody != (execution.NativeCustody{State: execution.NotAcquired}) {
		return ErrAdmissionChanged
	}
	if run.Reconciled {
		if run.Outcome != factory.Cancelled || !run.Native.Settled() {
			return ErrAdmissionChanged
		}
		var minutes int
		if err = tx.QueryRowContext(ctx, `SELECT minutes FROM factory_usage WHERE run=$1`, run.ID).Scan(&minutes); err != nil || minutes != 0 {
			return ErrAdmissionChanged
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		return s.retireSettledNativeCapability(ctx, run)
	}
	if run.Outcome != "" || run.Native.Retirement != execution.NotStarted || run.Native.Sealed {
		return ErrAdmissionChanged
	}
	var a factory.Assignment
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE run=$1 FOR UPDATE`, run.ID).Scan(&raw); err != nil {
		return err
	}
	if json.Unmarshal(raw, &a) != nil || a.Validate() != nil || a.Stage != factory.AssignmentAssigned || a.Run != run.ID || a.ProjectID != run.ProjectID {
		return ErrAdmissionChanged
	}
	var state string
	var observed bool
	if err = tx.QueryRowContext(ctx, `SELECT state,data,COALESCE((data->>'live_observed')::boolean,false) FROM factory_mcp_capabilities WHERE id=$1 FOR UPDATE`, run.ID).Scan(&state, &raw, &observed); err != nil {
		return err
	}
	var cap factory.MCPCapability
	if json.Unmarshal(raw, &cap) != nil || cap.Validate() != nil || state != factory.MCPCapabilityStaged || cap.State != factory.MCPCapabilityStaged || observed ||
		cap.RunID != run.ID || cap.AssignmentID != a.ID || cap.Project != run.ProjectID {
		return ErrAdmissionChanged
	}
	var reservation factory.Reservation
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_reservations WHERE assignment=$1 FOR UPDATE`, a.ID).Scan(&raw); err != nil {
		return err
	}
	if json.Unmarshal(raw, &reservation) != nil || reservation.Validate() != nil || reservation.State != factory.ReservationHeld || reservation.Connection != a.Connection || reservation.Repository != a.Repository {
		return ErrAdmissionChanged
	}
	var charged bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_usage WHERE run=$1)`, run.ID).Scan(&charged); err != nil || charged {
		return ErrAdmissionChanged
	}
	native := *run.Native
	native.Retirement, native.Sealed = execution.Retired, true
	if !native.Advances(*run.Native) {
		return execution.ErrRunChanged
	}
	run.Native, run.Outcome, run.Reconciled, run.Summary = &native, factory.Cancelled, true, "context preparation ended before native launch"
	if run.Validate() != nil {
		return ErrAdmissionChanged
	}
	encoded, err := json.Marshal(run)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_runs SET active=false,settled=true,data=$2::jsonb || jsonb_build_object('settled_at',clock_timestamp()) WHERE id=$1`, run.ID, string(encoded)); err != nil {
		return err
	}
	usage := factory.Usage{RunID: run.ID, Repository: a.Repository, Connection: a.Connection, StartedAt: run.Started, EndedAt: run.Started}
	encoded, _ = json.Marshal(usage)
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_usage(run,repository,connection,minutes,data,started_at,ended_at) VALUES($1,$2,$3,0,$4,$5,$5)`, run.ID, a.Repository, a.Connection, string(encoded), run.Started); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_reservations SET state='consumed',data=jsonb_set(jsonb_set(data,'{state}','"consumed"'::jsonb),'{revision}',to_jsonb((data->>'revision')::bigint+1)) WHERE assignment=$1`, a.ID); err != nil {
		return err
	}
	if err = settleClosedAttemptForRunTx(ctx, tx, run.ID, time.Now()); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return s.retireSettledNativeCapability(ctx, run)
}

// RecordNativeRootRetirement consumes the trusted native owner's separate
// broker acknowledgement after process/acquisition facts are retained. This
// advances resources only, never product outcome, reservation or accounting.
func (s *Store) RecordNativeRootRetirement(ctx context.Context, profile collaboration.ProfileID, intent, invocation, acquisition string) error {
	bound, err := execution.ParseNativeIntent(intent)
	if err != nil || bound.Native.Workspace.Profile != profile {
		return ErrAdmissionChanged
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_runs WHERE id=$1 FOR UPDATE`, bound.Run).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var run factory.Run
	if json.Unmarshal(raw, &run) != nil || run.Validate() != nil || run.ID != bound.Run || run.Native == nil ||
		run.Native.Intent != intent || run.Native.Provenance.Profile != profile {
		return ErrAdmissionChanged
	}
	native := *run.Native
	process := native.Provenance.Process
	custody := native.Provenance.Custody
	custodyDone := custody.State == execution.Released && acquisition != ""
	if native.Provenance.TrustMode == execution.NativeCLIProvider {
		custodyDone = custody.State == execution.NotAcquired && acquisition == ""
	}
	if process == nil || process.InvocationID != invocation || invocation == "" || custody.AcquisitionID != acquisition || !custodyDone || !native.Sealed {
		return execution.ErrObservation
	}
	native.Retirement = execution.Retired
	if !native.Advances(*run.Native) {
		return execution.ErrRunChanged
	}
	run.Native = &native
	if run.Validate() != nil {
		return ErrAdmissionChanged
	}
	encoded, err := json.Marshal(native)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE factory_runs SET data=jsonb_set(data,'{native}',$1::jsonb) WHERE id=$2`, string(encoded), run.ID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrNotFound
	}
	return tx.Commit()
}

type NativeRecoveryCandidate struct {
	BrokerRun string `json:"broker_run"`
	SourceSHA string `json:"source_sha256"`
}

// NativeRecoveryCandidates includes observed processes even when Go missed the
// final native receipt before root removal. Native recovery must still verify
// retained terminal/custody facts and acknowledge exact root retirement. An
// unobserved intent alone does not establish that a worker existed.
func (s *Store) NativeRecoveryCandidates(ctx context.Context, profile collaboration.ProfileID, after int64) ([]NativeRecoveryCandidate, string, error) {
	if !collaboration.ValidID(string(profile)) || after < 0 {
		return nil, "", ErrAdmissionChanged
	}
	rows, err := s.db.QueryContext(ctx, `SELECT seq,id,data FROM factory_runs
	 WHERE seq>$1 AND data#>>'{native,provenance,profile}'=$2
	 AND coalesce(data#>>'{native,intent}','')!=''
	 AND (((data#>>'{native,sealed}'='true' OR jsonb_typeof(data#>'{native,provenance,process}')='object')
	 AND data#>>'{native,retirement}' IN ('running','exited','uncertain'))
	 OR (data#>>'{native,provenance,trust_mode}'='cli_managed_provider' AND
	 (data#>>'{native,retirement}'!='retired' OR data->>'reconciled'!='true')))
	 ORDER BY seq LIMIT 33`, after, string(profile))
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rows.Close() }()
	items := []NativeRecoveryCandidate{}
	next := ""
	var last int64
	for rows.Next() {
		var seq int64
		var id string
		var raw []byte
		if err = rows.Scan(&seq, &id, &raw); err != nil {
			return nil, "", err
		}
		if len(items) == 32 {
			next = strconv.FormatInt(last, 10)
			break
		}
		var run factory.Run
		if json.Unmarshal(raw, &run) != nil || run.Validate() != nil || run.ID != id || run.Native == nil || (run.Native.Provenance.Process == nil && run.Native.Provenance.TrustMode != execution.NativeCLIProvider) || run.Native.Provenance.Profile != profile {
			return nil, "", ErrAdmissionChanged
		}
		items = append(items, NativeRecoveryCandidate{BrokerRun: run.Native.Provenance.BrokerRun, SourceSHA: run.Native.BundleSHA256})
		last = seq
	}
	return items, next, rows.Err()
}
