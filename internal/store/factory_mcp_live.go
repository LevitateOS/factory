package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

// MCPRunWasObserved retains execution history after capability retirement.
// Staging, revocation and confirmed-unused settlement do not set this fact.
func (s *Store) MCPRunWasObserved(ctx context.Context, run, assignment, project string) (bool, error) {
	var observed bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_mcp_capabilities
WHERE data->>'run_id'=$1 AND data->>'assignment_id'=$2 AND data->>'project'=$3
AND data->>'live_observed'='true')`, run, assignment, project).Scan(&observed)
	return observed, err
}

// ActivateObservedMCPCapability is the production activation joint. The caller
// has just observed the matching live native run; current stored stop intent is
// rechecked under the same transaction that activates the capability.
func (s *Store) ActivateObservedMCPCapability(ctx context.Context, observed factory.MCPCapability) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw []byte
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT state,data FROM factory_mcp_capabilities WHERE id=$1 FOR UPDATE`, observed.ID).Scan(&state, &raw); err != nil {
		return err
	}
	var current factory.MCPCapability
	if err = json.Unmarshal(raw, &current); err != nil {
		return err
	}
	if current.ScopeDigest() != observed.ScopeDigest() {
		return ErrMCPCapabilityConflict
	}
	if state != factory.MCPCapabilityStaged && state != factory.MCPCapabilityActive {
		return ErrMCPCapabilityState
	}
	if current.Expired(time.Now()) {
		return ErrMCPSubmissionCapabilityExpired
	}
	if err = checkMCPWorkActiveTx(ctx, tx, current); err != nil {
		return err
	}
	if state == factory.MCPCapabilityStaged {
		if _, err = tx.ExecContext(ctx, `UPDATE factory_mcp_capabilities SET state=$2,data=jsonb_set(data,'{live_observed}','true'::jsonb) WHERE id=$1`, current.ID, factory.MCPCapabilityActive); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CheckMCPWorkActive validates current stored work on every authenticated read.
// Mutations repeat this guard inside their insert transaction when the
// capability has been activated through the production observation joint.
func (s *Store) CheckMCPWorkActive(ctx context.Context, capability factory.MCPCapability) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = checkMCPWorkActiveTx(ctx, tx, capability); err != nil {
		return err
	}
	return tx.Commit()
}

func checkMCPWorkActiveTx(ctx context.Context, tx *sql.Tx, capability factory.MCPCapability) error {
	// Use the same repository gate as dispatch withdrawal, then the current
	// assignment and preparation rows. A stop committed first is visible here;
	// a concurrent stop waits until this operation has recorded its result.
	var open bool
	if err := tx.QueryRowContext(ctx, `SELECT open FROM factory_dispatch WHERE repository=$1 FOR SHARE`, capability.Repository).Scan(&open); err != nil {
		return err
	}
	if !open {
		return errors.New("factory dispatch is stopped")
	}
	var raw []byte
	var stage string
	if err := tx.QueryRowContext(ctx, `SELECT stage,data FROM factory_assignments WHERE id=$1 FOR SHARE`, capability.AssignmentID).Scan(&stage, &raw); err != nil {
		return err
	}
	var assignment factory.Assignment
	if err := json.Unmarshal(raw, &assignment); err != nil {
		return err
	}
	if stage != factory.AssignmentAssigned || assignment.Run != capability.RunID || assignment.ProjectID != capability.Project || assignment.Repository != capability.Repository || factory.MCPProfileOfAssignment(assignment) != capability.Profile {
		return errors.New("factory assignment is no longer the admitted work")
	}
	if err := tx.QueryRowContext(ctx, `SELECT data FROM project_preparations WHERE id=$1 FOR SHARE`, assignment.Preparation).Scan(&raw); err != nil {
		return err
	}
	preparation, meta, err := unmarshalCandidatePreparation(raw)
	if err != nil {
		return err
	}
	if preparation.Preparation.SetupDigest != capability.PreparationDigest || preparation.State.Stopped || (meta != nil && (meta.Stopped || meta.Retirement == "confirmed")) {
		return errors.New("factory preparation is stopped or changed")
	}
	return nil
}
