package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

// BindContextBundleSelection records which input the staged run is consulting.
// A completed selection stays fixed for the run. Required work and current
// capability authority are checked before binding a new input.
func (s *Store) BindContextBundleSelection(ctx context.Context, capability factory.MCPCapability, fingerprint string) (bool, error) {
	if !factory.ValidDigest(fingerprint) {
		return false, errors.New("invalid context selection binding")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var raw []byte
	var state string
	var prior string
	if err = tx.QueryRowContext(ctx, `SELECT state,data,COALESCE(data->>'context_selection_snapshot','') FROM factory_mcp_capabilities WHERE id=$1 FOR UPDATE`, capability.ID).Scan(&state, &raw, &prior); err != nil {
		return false, err
	}
	var recorded factory.MCPCapability
	if err = json.Unmarshal(raw, &recorded); err != nil {
		return false, err
	}
	if recorded.ScopeDigest() != capability.ScopeDigest() {
		return false, ErrMCPCapabilityConflict
	}
	if state != factory.MCPCapabilityStaged || recorded.Expired(time.Now()) {
		return false, ErrMCPCapabilityState
	}
	if prior != "" {
		var decided bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_priority_judgments WHERE snapshot=$1 AND data IS NOT NULL)`, prior).Scan(&decided); err != nil || decided {
			return false, err
		}
	}
	if err = checkMCPWorkActiveTx(ctx, tx, capability); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_mcp_capabilities SET data=jsonb_set(data,'{context_selection_snapshot}',to_jsonb($2::text)) WHERE id=$1`, capability.ID, fingerprint); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
