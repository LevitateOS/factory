package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
)

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

// NativeRecoveryCandidates includes fact-recorded rows whose root-retirement
// acknowledgement may have been lost after root removal. It excludes unobserved
// intent-only history rather than fabricating a process or cleanup observation.
func (s *Store) NativeRecoveryCandidates(ctx context.Context, profile collaboration.ProfileID, after int64) ([]NativeRecoveryCandidate, string, error) {
	if !collaboration.ValidID(string(profile)) || after < 0 {
		return nil, "", ErrAdmissionChanged
	}
	rows, err := s.db.QueryContext(ctx, `SELECT seq,id,data FROM factory_runs
	 WHERE seq>$1 AND data#>>'{native,provenance,profile}'=$2
	 AND coalesce(data#>>'{native,intent}','')!='' AND data#>>'{native,sealed}'='true'
	 AND data#>>'{native,retirement}' IN ('exited','uncertain')
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
		if json.Unmarshal(raw, &run) != nil || run.Validate() != nil || run.ID != id || run.Native == nil || run.Native.Provenance.Process == nil || run.Native.Provenance.Profile != profile {
			return nil, "", ErrAdmissionChanged
		}
		items = append(items, NativeRecoveryCandidate{BrokerRun: run.Native.Provenance.BrokerRun, SourceSHA: run.Native.BundleSHA256})
		last = seq
	}
	return items, next, rows.Err()
}
