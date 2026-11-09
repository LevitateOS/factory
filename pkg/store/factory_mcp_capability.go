package store

// D14 capability persistence (FW38).
//
// factory_mcp_capabilities records one row per Factory run authority.
// Exact restart delivery reproduces the same capability, never a new
// deadline or allowance: StageMCPCapability replays identically, and
// activation compares the observed run binding inside the
// transaction. Rows retain the capability ID and scope digest, never
// secret bytes. Key rotation revokes outstanding capabilities through
// RevokeMCPCapability; it cannot transparently reauthorize an old
// run. Schema registration rides the coordinator-serialized proposal
// (see evidence/fw38/registration-proposal.md); this file owns the
// DDL constants and the transaction methods.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
)

// FactoryMCPCapabilitiesDDL creates the capability table. data
// carries the factory.MCPCapability; authority_digest binds the
// scope without storing secret bytes.
const FactoryMCPCapabilitiesDDL = `CREATE TABLE factory_mcp_capabilities(
id TEXT PRIMARY KEY CHECK(id ~ '^[a-f0-9]{32}$'),
assignment TEXT NOT NULL CHECK(assignment ~ '^[a-f0-9]{32}$'),
run TEXT NOT NULL CHECK(char_length(run)>0 AND char_length(run)<=256),
generation BIGINT NOT NULL CHECK(generation>=1),
profile TEXT NOT NULL, subject TEXT NOT NULL,
repository BIGINT NOT NULL CHECK(repository>0), project TEXT NOT NULL,
preparation_digest TEXT NOT NULL, authority_digest TEXT NOT NULL,
deadline BIGINT NOT NULL, state TEXT NOT NULL,
data JSONB NOT NULL)`

// FactoryMCPCapabilityDeleteTrigger preserves capability history:
// revocation and retirement move state, rows are never deleted.
const FactoryMCPCapabilityDeleteTrigger = `CREATE TRIGGER factory_mcp_capability_no_delete BEFORE DELETE ON factory_mcp_capabilities FOR EACH ROW EXECUTE FUNCTION soda_reject_immutable()`

var (
	// ErrMCPCapabilityConflict reports a staged capability whose
	// immutable identity or scope differs from the recorded row.
	ErrMCPCapabilityConflict = errors.New("factory capability conflicts with the recorded scope")
	// ErrMCPCapabilityState reports a transition the recorded state
	// refuses: only staged activates, only active revokes/retires.
	ErrMCPCapabilityState = errors.New("factory capability state refuses the transition")
)

// StageMCPCapability records a staged capability. Exact restart
// delivery replays identically and returns the recorded row; a
// changed scope under the same ID conflicts instead of replacing
// authority.
func (s *Store) StageMCPCapability(ctx context.Context, capability factory.MCPCapability) error {
	if err := capability.Validate(); err != nil {
		return err
	}
	if capability.State != factory.MCPCapabilityStaged {
		return errors.New("staged capability carries a non-staged state")
	}
	raw, err := json.Marshal(capability)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var current []byte
	err = tx.QueryRowContext(ctx, `SELECT data FROM factory_mcp_capabilities WHERE id=$1 FOR UPDATE`, capability.ID).Scan(&current)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO factory_mcp_capabilities(id,assignment,run,generation,profile,subject,repository,project,preparation_digest,authority_digest,deadline,state,data) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			capability.ID, capability.AssignmentID, capability.RunID, capability.Generation,
			string(capability.Profile), capability.Subject, capability.Repository, capability.Project,
			capability.PreparationDigest, capability.Authority, capability.DeadlineUnix,
			capability.State, string(raw)); err != nil {
			return err
		}
		return tx.Commit()
	}
	var recorded factory.MCPCapability
	if err = json.Unmarshal(current, &recorded); err != nil {
		return err
	}
	if recorded.ScopeDigest() != capability.ScopeDigest() {
		return ErrMCPCapabilityConflict
	}
	return tx.Commit()
}

// ActivateMCPCapability moves a staged capability to active under an
// observed-binding guard: the recorded run, assignment and authority
// must match the host observation, the deadline must still hold and
// stop intent must be absent. Only staged rows activate, so a
// revoked or retired capability never returns.
func (s *Store) ActivateMCPCapability(ctx context.Context, id string, generation int64, runID, assignmentID, authority string, nowUnix int64, stopped bool) error {
	if !factory.ValidID(id) || !factory.ValidID(assignmentID) {
		return errors.New("invalid capability activation identity")
	}
	if stopped {
		return errors.New("stop intent refuses capability activation")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var raw []byte
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT state,data FROM factory_mcp_capabilities WHERE id=$1 FOR UPDATE`, id).Scan(&state, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if state != factory.MCPCapabilityStaged {
		return ErrMCPCapabilityState
	}
	var recorded factory.MCPCapability
	if err = json.Unmarshal(raw, &recorded); err != nil {
		return err
	}
	if err = recorded.Validate(); err != nil {
		return err
	}
	if recorded.Generation != generation || recorded.RunID != runID ||
		recorded.AssignmentID != assignmentID || recorded.Authority != authority {
		return ErrMCPCapabilityConflict
	}
	if recorded.DeadlineUnix <= nowUnix {
		return ErrMCPCapabilityState
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_mcp_capabilities SET state=$2 WHERE id=$1`, id, factory.MCPCapabilityActive); err != nil {
		return err
	}
	return tx.Commit()
}

// RevokeMCPCapability moves an active or staged capability to
// revoked: expiry, withdrawal and key rotation land here. Retired
// rows stay retired; revocation never reactivates.
func (s *Store) RevokeMCPCapability(ctx context.Context, id string) error {
	return casMCPCapabilityState(ctx, s.db, id, []string{factory.MCPCapabilityStaged, factory.MCPCapabilityActive}, factory.MCPCapabilityRevoked)
}

// RetireMCPCapability moves an active capability to retired after
// terminal settlement and confirmed native retirement. Revoked rows
// stay revoked; retirement never reactivates.
func (s *Store) RetireMCPCapability(ctx context.Context, id string) error {
	return casMCPCapabilityState(ctx, s.db, id, []string{factory.MCPCapabilityActive}, factory.MCPCapabilityRetired)
}

// Run settlement is durable before this downgrade. Keeping it outside the run
// transaction preserves the gate/capability/run lock order used by live calls.
func (s *Store) retireSettledNativeCapability(ctx context.Context, run factory.Run) error {
	if run.Native == nil || !run.Native.Settled() || !run.Reconciled || run.Outcome == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE factory_mcp_capabilities
 SET state=CASE state WHEN 'active' THEN 'retired' ELSE 'revoked' END
 WHERE id=$1 AND data->>'run_id'=$1 AND data->>'project'=$2 AND state IN ('active','staged')`, run.ID, run.ProjectID)
	return err
}

// RecoverNativeCapabilityRetirement consumes existing sealed receipts only.
// Unknown or unsettled native records never become confirmed retirement here.
func (s *Store) RecoverNativeCapabilityRetirement(ctx context.Context) error {
	after := ""
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT r.id,r.data FROM factory_runs r JOIN factory_mcp_capabilities c ON c.id=r.id
 WHERE r.id>$1 AND NOT r.active AND r.settled AND r.data#>>'{native,sealed}'='true'
 AND c.state IN ('active','staged') ORDER BY r.id LIMIT 128`, after)
		if err != nil {
			return err
		}
		var runs []factory.Run
		visited := 0
		for rows.Next() {
			visited++
			var raw []byte
			if err = rows.Scan(&after, &raw); err != nil {
				rows.Close()
				return err
			}
			var run factory.Run
			if json.Unmarshal(raw, &run) == nil && run.Validate() == nil {
				runs = append(runs, run)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, run := range runs {
			if err := s.retireSettledNativeCapability(ctx, run); err != nil {
				return err
			}
		}
		if visited < 128 {
			return nil
		}
	}
}

func casMCPCapabilityState(ctx context.Context, db *sql.DB, id string, from []string, to string) error {
	if !factory.ValidID(id) {
		return errors.New("invalid capability identity")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM factory_mcp_capabilities WHERE id=$1 FOR UPDATE`, id).Scan(&state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	admitted := false
	for _, want := range from {
		if state == want {
			admitted = true
		}
	}
	if !admitted {
		return ErrMCPCapabilityState
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_mcp_capabilities SET state=$2 WHERE id=$1`, id, to); err != nil {
		return err
	}
	return tx.Commit()
}

// GetMCPCapability reads the recorded capability for admission. The
// row carries scope, never secret bytes.
func (s *Store) GetMCPCapability(ctx context.Context, id string) (factory.MCPCapability, error) {
	var capability factory.MCPCapability
	var raw []byte
	var state string
	if err := s.db.QueryRowContext(ctx, `SELECT state,data FROM factory_mcp_capabilities WHERE id=$1`, id).Scan(&state, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return factory.MCPCapability{}, ErrNotFound
		}
		return factory.MCPCapability{}, err
	}
	if err := json.Unmarshal(raw, &capability); err != nil {
		return factory.MCPCapability{}, err
	}
	capability.State = state
	if err := capability.Validate(); err != nil {
		return factory.MCPCapability{}, err
	}
	return capability, nil
}
