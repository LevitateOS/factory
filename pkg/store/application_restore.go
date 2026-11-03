package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/levitateos/factory/pkg/application"
)

const ApplicationRestoresDDL = `CREATE TABLE application_restores(id TEXT PRIMARY KEY,application_id TEXT NOT NULL REFERENCES applications(id),base_revision BIGINT NOT NULL,target_data TEXT NOT NULL UNIQUE,state TEXT NOT NULL CHECK(state IN ('pending','complete','aborted')),data JSONB NOT NULL)`

func scanApplicationRestore(row *sql.Row) (application.RestoreOperation, error) {
	var operation application.RestoreOperation
	var raw []byte
	var state string
	if err := row.Scan(&raw, &state); err != nil {
		return operation, err
	}
	if err := json.Unmarshal(raw, &operation); err != nil {
		return operation, err
	}
	operation.State = state
	return operation, operation.Validate()
}

func (s *Store) ApplicationRestore(ctx context.Context, app, id string) (application.RestoreOperation, error) {
	return scanApplicationRestore(s.db.QueryRowContext(ctx, `SELECT data,state FROM application_restores WHERE application_id=$1 AND id=$2`, app, id))
}

func (s *Store) PendingApplicationRestore(ctx context.Context, app string) (*application.RestoreOperation, error) {
	op, err := scanApplicationRestore(s.db.QueryRowContext(ctx, `SELECT data,state FROM application_restores WHERE application_id=$1 AND state='pending'`, app))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &op, nil
}

func (s *Store) BeginApplicationRestore(ctx context.Context, administrator int64, operation application.RestoreOperation) error {
	if err := operation.Validate(); err != nil {
		return err
	}
	if operation.State != "pending" {
		return errors.New("restore must begin pending")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := scanApplication(tx.QueryRowContext(ctx, applicationRecordSQL+` FOR UPDATE OF a`, operation.Previous.ID, administrator))
	if err != nil {
		return err
	}
	if current.Application != operation.Previous || current.Observation == nil || current.Observation.Running || current.Observation.Container != operation.ExpectedContainer {
		return errors.New("stop and inspect the exact application before restoring")
	}
	var used bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM application_revisions WHERE spec->>'data'=$1)`, operation.Next.Data).Scan(&used); err != nil {
		return err
	}
	if used {
		return errors.New("restore data identity has already been admitted")
	}
	raw, err := json.Marshal(operation)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO application_restores(id,application_id,base_revision,target_data,state,data) VALUES($1,$2,$3,$4,'pending',$5)`, operation.ID, operation.Previous.ID, operation.Previous.Revision, operation.Next.Data, string(raw))
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE applications SET observation=NULL WHERE id=$1`, operation.Previous.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// SettleApplicationRestore changes the data binding only after matching native
// readback. The prior immutable revision and data remain available for recovery.
func (s *Store) SettleApplicationRestore(ctx context.Context, administrator int64, app, id string, observed application.Observation, abort bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := scanApplication(tx.QueryRowContext(ctx, applicationRecordSQL+` FOR UPDATE OF a`, app, administrator))
	if err != nil {
		return err
	}
	op, err := scanApplicationRestore(tx.QueryRowContext(ctx, `SELECT data,state FROM application_restores WHERE application_id=$1 AND id=$2 FOR UPDATE`, app, id))
	if err != nil {
		return err
	}
	state, target := "complete", op.Next
	if abort {
		state, target = "aborted", op.Previous
	}
	if op.State == state && current.Application == target && application.SameObservation(current.Observation, &observed) {
		return nil
	}
	if op.State != "pending" || current.Application != op.Previous || observed.Running || observed.Ready {
		return errors.New("restore intent or application changed")
	}
	if err := observed.Validate(target); err != nil {
		return err
	}
	if (abort && observed.Container != op.ExpectedContainer) || (!abort && observed.Container == op.ExpectedContainer) || observed.Container == "" {
		return errors.New("restore did not confirm the requested native generation")
	}
	if !abort {
		if err := insertApplicationRevision(ctx, tx, target); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE applications SET revision=$1,data_id=$2 WHERE id=$3`, target.Revision, target.Data, app); err != nil {
			return err
		}
	}
	value, err := applicationObservationValue(&observed, target)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE applications SET observation=$1 WHERE id=$2`, value, app); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE application_restores SET state=$1 WHERE id=$2 AND state='pending'`, state, id); err != nil {
		return err
	}
	return tx.Commit()
}
