package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

var ErrContextResolutionIneligible = errors.New("current context resolution is no longer admissible")

// SaveContextCurrentResolution saves the owner's choice on its capability.
// Scope, deadlines, paid judgment and attempt allowances retain their owners.
func (s *Store) SaveContextCurrentResolution(ctx context.Context, expected factory.MCPCapability, request factory.ContextCurrentRequest, principal string, now time.Time) (factory.ContextCurrentResolution, error) {
	var result factory.ContextCurrentResolution
	if principal == "" || len(principal) > 128 || request.ScopeDigest != expected.ScopeDigest() || request.Fingerprint != expected.ContextSelectionSnapshot || !factory.ValidDigest(request.RequiredDigest) {
		return result, ErrContextResolutionIneligible
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var cap factory.MCPCapability
	var raw []byte
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT state,data FROM factory_mcp_capabilities WHERE id=$1 FOR UPDATE`, expected.ID).Scan(&state, &raw); err != nil {
		return result, err
	}
	if json.Unmarshal(raw, &cap) != nil {
		return result, ErrContextResolutionIneligible
	}
	cap.State = state
	if cap.State != factory.MCPCapabilityStaged || cap.Expired(now) || cap.ScopeDigest() != expected.ScopeDigest() || cap.ContextSelectionSnapshot != request.Fingerprint || cap.ContextCurrentResolution != nil {
		return result, ErrContextResolutionIneligible
	}
	var run factory.Run
	if err = contextJSONRow(ctx, tx, &run, `SELECT data FROM factory_runs WHERE id=$1 FOR UPDATE`, cap.RunID); err != nil {
		return result, err
	}
	if !run.ContextPending || run.Reconciled || run.Outcome != "" || run.HasExecutionIdentity() || !now.Before(run.Deadline) {
		return result, ErrContextResolutionIneligible
	}
	var a factory.Assignment
	if err = contextJSONRow(ctx, tx, &a, `SELECT data FROM factory_assignments WHERE id=$1 FOR UPDATE`, cap.AssignmentID); err != nil {
		return result, err
	}
	if a.Stage != factory.AssignmentAssigned || a.Run != cap.RunID || a.ProjectID != cap.Project || a.Repository != cap.Repository {
		return result, ErrContextResolutionIneligible
	}
	if err = checkMCPWorkActiveTx(ctx, tx, cap); err != nil {
		return result, ErrContextResolutionIneligible
	}
	grants, err := loadAdmissionGrantsTx(ctx, tx, a.Repository, a.Connection)
	if err != nil {
		return result, err
	}
	if err = checkAssignmentAuthorityTx(ctx, tx, a.Authority, a, grants); err != nil {
		return result, ErrContextResolutionIneligible
	}
	current, err := factoryCurrentContextTx(ctx, tx, cap, now)
	if err != nil || current.Evidence.Availability != "complete" || current.Allowance == nil || current.Allowance.Closed || current.Allowance.ActiveSecondsRemaining <= 0 {
		return result, ErrContextResolutionIneligible
	}
	digest, err := factory.CurrentRequiredSemanticDigest(current.Role)
	if err != nil || digest != request.RequiredDigest {
		return result, ErrContextResolutionIneligible
	}
	result = factory.ContextCurrentResolution{Principal: principal, RunID: cap.RunID, AssignmentID: cap.AssignmentID, ScopeDigest: cap.ScopeDigest(), Fingerprint: request.Fingerprint, RequiredDigest: digest, Bundle: factory.ContextBundleCurrent, RecordedUnix: now.Unix()}
	out, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_mcp_capabilities SET data=jsonb_set(data,'{context_current_resolution}',$2::jsonb) WHERE id=$1`, cap.ID, string(out)); err != nil {
		return result, err
	}
	return result, tx.Commit()
}
