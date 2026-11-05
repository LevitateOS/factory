package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
)

// ObserveNativeRecovery locks and advances only native facts against the exact
// retained Go intent. It neither admits work nor settles outcome/accounting.
// Replays and concurrent observations use the same immutable advance checks.
func (s *Store) ObserveNativeRecovery(ctx context.Context, profile collaboration.ProfileID, intent string, receipt execution.NativeReceiptObservation, custody *execution.NativeCustodyObservation, desktopUID int) error {
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
	if json.Unmarshal(raw, &run) != nil || run.Validate() != nil || run.ID != bound.Run || run.Reconciled ||
		run.Native == nil || run.Native.Intent != intent || run.Native.Provenance.Profile != profile {
		return ErrAdmissionChanged
	}
	native, err := execution.ObserveNativeReceipt(*run.Native, receipt, custody, desktopUID)
	if err != nil {
		return err
	}
	started := native.Provenance.Process.StartedAt
	if started.Before(run.Started) || started.After(run.Deadline) {
		return execution.ErrObservation
	}
	run.Native = &native
	if run.Validate() != nil {
		return ErrAdmissionChanged
	}
	encoded, err := json.Marshal(native)
	if err != nil {
		return err
	}
	// Leave every other field and the active/settled projections untouched.
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
