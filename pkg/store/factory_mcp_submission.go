package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
)

var (
	ErrMCPSubmissionFrozen             = errors.New("assignment already submitted its result; read factory.status")
	ErrMCPSubmissionCapabilityInactive = errors.New("factory capability is not active for this assignment")
	ErrMCPSubmissionCapabilityExpired  = errors.New("factory capability is expired")
	ErrMCPSubmissionAssignmentFinished = errors.New("assignment already finished")
)

// mcpSubmittedWork is the work attached to a post. State and Checkpoint are the
// controller's processing state, rather than a second record of the action.
type mcpSubmittedWork struct {
	Capability string                       `json:"capability"`
	Tool       factory.MCPTool              `json:"tool"`
	Payload    string                       `json:"payload"`
	State      string                       `json:"state"`
	Reason     string                       `json:"reason,omitempty"`
	Checkpoint *execution.CheckpointReceipt `json:"checkpoint,omitempty"`
}

func decodeMCPWorkPost(assignment, key, id string, raw []byte) (factory.MCPSubmission, factory.MCPWorkStatus, error) {
	var data struct {
		Work mcpSubmittedWork `json:"work"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return factory.MCPSubmission{}, factory.MCPWorkStatus{}, err
	}
	payload := []byte(data.Work.Payload)
	submission := factory.MCPSubmission{AssignmentID: assignment, SubmissionID: key, CapabilityID: data.Work.Capability, Tool: data.Work.Tool, Payload: payload}
	status := factory.MCPWorkStatus{SubmissionID: key, Tool: data.Work.Tool, State: data.Work.State, Reason: data.Work.Reason, FeedPostID: id, Checkpoint: data.Work.Checkpoint}
	return submission, status, nil
}

const mcpWorkPostLookup = `SELECT id,idempotency,data FROM factory_feed_posts WHERE data->>'assignment'=$1 AND idempotency=$2 AND data ? 'work'`

// RecordMCPSubmission records the work on its post. The capability row
// serializes requests from the active run; no separate guard row is needed.
func (s *Store) RecordMCPSubmission(ctx context.Context, submission factory.MCPSubmission) (factory.MCPWorkStatus, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return factory.MCPWorkStatus{}, err
	}
	defer tx.Rollback()
	capability, err := lockMCPCapabilityTx(ctx, tx, submission.CapabilityID, submission.AssignmentID)
	if err != nil {
		return factory.MCPWorkStatus{}, err
	}
	var id, key string
	var raw []byte
	err = tx.QueryRowContext(ctx, mcpWorkPostLookup, submission.AssignmentID, submission.SubmissionID).Scan(&id, &key, &raw)
	if err == nil {
		_, result, err := decodeMCPWorkPost(submission.AssignmentID, key, id, raw)
		if err != nil {
			return result, err
		}
		return result, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return factory.MCPWorkStatus{}, err
	}
	if terminal, frozen, err := terminalMCPWorkStatus(submission.AssignmentID, tx.QueryRowContext(ctx, terminalMCPWorkQuery, submission.AssignmentID)); err != nil {
		return terminal, err
	} else if frozen {
		return terminal, ErrMCPSubmissionFrozen
	}
	if finished, err := assignmentFinishedTx(ctx, tx, submission.AssignmentID); err != nil {
		return factory.MCPWorkStatus{}, err
	} else if finished {
		return factory.MCPWorkStatus{}, ErrMCPSubmissionAssignmentFinished
	}
	if capability.Expired(time.Now()) {
		return factory.MCPWorkStatus{}, ErrMCPSubmissionCapabilityExpired
	}
	if err := recordMCPVerificationJourneyReferencesTx(ctx, tx, capability, submission); err != nil {
		return factory.MCPWorkStatus{}, err
	}
	work := mcpSubmittedWork{Capability: capability.ID, Tool: submission.Tool, Payload: string(submission.Payload), State: factory.MCPWorkRecorded}
	id = factory.MCPInputDigest([]byte(capability.AssignmentID + "\n" + submission.SubmissionID))[:32]
	var issue int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT issue FROM factory_assignments WHERE id=$1 AND repository=$2),0)`, capability.AssignmentID, capability.Repository).Scan(&issue); err != nil {
		return factory.MCPWorkStatus{}, err
	}
	data := map[string]any{"issue": issue, "assignment": capability.AssignmentID, "run": capability.RunID, "project": capability.Project, "actor": string(capability.Profile), "author_name": recordPersona(capability.Profile), "parent_id": capability.AssignmentID, "at": time.Now().Unix(), "work": work}
	raw, err = json.Marshal(data)
	if err != nil {
		return factory.MCPWorkStatus{}, err
	}
	body := recordPersona(capability.Profile) + " submitted work for Factory to process."
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_feed_posts(id,repository,revision,author,body,idempotency,data) VALUES($1,$2,0,$3,$4,$5,$6)`, id, capability.Repository, "worker:"+capability.AssignmentID, body, submission.SubmissionID, string(raw)); err != nil {
		return factory.MCPWorkStatus{}, err
	}
	_, result, err := decodeMCPWorkPost(submission.AssignmentID, submission.SubmissionID, id, raw)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

// lockMCPCapabilityTx holds the run's authority through its write, sharing the
// same row lock as revocation and retirement.
func lockMCPCapabilityTx(ctx context.Context, tx *sql.Tx, capabilityID, assignmentID string) (factory.MCPCapability, error) {
	var capability factory.MCPCapability
	var state string
	var raw []byte
	// Match grant withdrawal's gate-before-capability order. Legacy unbound
	// fixture capabilities have no dispatch gate and never supply live proof.
	var repository int64
	if err := tx.QueryRowContext(ctx, `SELECT (data->>'repository')::bigint FROM factory_mcp_capabilities WHERE id=$1`, capabilityID).Scan(&repository); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return capability, ErrMCPSubmissionCapabilityInactive
		}
		return capability, err
	}
	var open bool
	if err := tx.QueryRowContext(ctx, `SELECT open FROM factory_dispatch WHERE repository=$1 FOR SHARE`, repository).Scan(&open); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return capability, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT state,data FROM factory_mcp_capabilities WHERE id=$1 FOR UPDATE`, capabilityID).Scan(&state, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return capability, ErrMCPSubmissionCapabilityInactive
		}
		return capability, err
	}
	var data struct {
		factory.MCPCapability
		Live bool `json:"live_observed"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return capability, err
	}
	capability = data.MCPCapability
	capability.State = state
	if capability.Repository != repository {
		return capability, ErrMCPCapabilityConflict
	}
	if data.Live {
		if err := checkMCPWorkActiveTx(ctx, tx, capability); err != nil {
			return capability, err
		}
	}
	if capability.State != factory.MCPCapabilityActive || capability.AssignmentID != assignmentID {
		return capability, ErrMCPSubmissionCapabilityInactive
	}
	return capability, nil
}

func assignmentFinishedTx(ctx context.Context, tx *sql.Tx, assignmentID string) (bool, error) {
	var stage string
	err := tx.QueryRowContext(ctx, `SELECT stage FROM factory_assignments WHERE id=$1`, assignmentID).Scan(&stage)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return stage != "" && stage != string(factory.AssignmentAssigned), nil
}

func (s *Store) MCPWorkStatus(ctx context.Context, assignmentID, submissionID string) (factory.MCPWorkStatus, error) {
	var id, key string
	var raw []byte
	if err := s.db.QueryRowContext(ctx, mcpWorkPostLookup, assignmentID, submissionID).Scan(&id, &key, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return factory.MCPWorkStatus{}, ErrNotFound
		}
		return factory.MCPWorkStatus{}, err
	}
	_, result, err := decodeMCPWorkPost(assignmentID, key, id, raw)
	return result, err
}

func (s *Store) TerminalMCPProposal(ctx context.Context, assignmentID string) (factory.MCPWorkStatus, bool, error) {
	return terminalMCPWorkStatus(assignmentID, s.db.QueryRowContext(ctx, terminalMCPWorkQuery, assignmentID))
}

const terminalMCPWorkQuery = `SELECT id,idempotency,data FROM factory_feed_posts WHERE data->>'assignment'=$1 AND starts_with(data->'work'->>'tool','factory.finish_') ORDER BY seq LIMIT 1`

func terminalMCPWorkStatus(assignmentID string, row *sql.Row) (factory.MCPWorkStatus, bool, error) {
	var id, key string
	var raw []byte
	err := row.Scan(&id, &key, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return factory.MCPWorkStatus{}, false, nil
	}
	if err != nil {
		return factory.MCPWorkStatus{}, false, err
	}
	_, status, err := decodeMCPWorkPost(assignmentID, key, id, raw)
	return status, err == nil, err
}

func (s *Store) TerminalMCPSubmission(ctx context.Context, assignmentID string) (factory.MCPSubmission, bool, error) {
	var id, key string
	var raw []byte
	err := s.db.QueryRowContext(ctx, terminalMCPWorkQuery, assignmentID).Scan(&id, &key, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return factory.MCPSubmission{}, false, nil
	}
	if err != nil {
		return factory.MCPSubmission{}, false, err
	}
	submission, _, err := decodeMCPWorkPost(assignmentID, key, id, raw)
	return submission, err == nil, err
}

func (s *Store) MCPSubmissionCount(ctx context.Context, assignmentID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM factory_feed_posts WHERE data->>'assignment'=$1 AND data ? 'work'`, assignmentID).Scan(&count)
	return count, err
}

// AdvanceMCPWork updates processing state on the original post. It never
// writes another status envelope or copies the submitted payload.
func (s *Store) AdvanceMCPWork(ctx context.Context, assignmentID, submissionID, expectedState string, result factory.MCPWorkStatus) error {
	raw, err := json.Marshal(result.Checkpoint)
	if err != nil {
		return err
	}
	changed, err := s.db.ExecContext(ctx, `UPDATE factory_feed_posts SET data=jsonb_set(data,'{work}',data->'work'||jsonb_build_object('state',$4::text,'reason',$5::text,'checkpoint',$6::jsonb)) WHERE data->>'assignment'=$1 AND idempotency=$2 AND data->'work'->>'state'=$3`, assignmentID, submissionID, expectedState, result.State, result.Reason, string(raw))
	if err != nil {
		return err
	}
	n, err := changed.RowsAffected()
	if err == nil && n != 1 {
		return ErrStaleRevision
	}
	return err
}
