package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/levitateos/factory/pkg/factory"
)

// SaveIntentRevision serializes revisions on a durable head and preserves all
// review history. Identity replay is exact; changed content cannot overwrite it.
func (s *Store) SaveIntentRevision(ctx context.Context, r factory.IntentRevision) error {
	if err := r.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := saveIntentRevisionTx(ctx, tx, r, raw); err != nil {
		return err
	}
	return tx.Commit()
}

// saveIntentRevisionTx runs the head-serialized CAS revision save inside an
// open transaction. Atomic review saves share it so a revision and its exact
// presentation commit or roll back together.
func lockIntentHeadTx(ctx context.Context, tx *sql.Tx, r factory.IntentRevision) (string, error) {
	if r.Department != "" {
		var deptOwner int64
		if err := tx.QueryRowContext(ctx, `SELECT owner_id FROM factory_departments WHERE id=$1`, r.Department).Scan(&deptOwner); err != nil {
			return "", err
		}
		if deptOwner != r.Owner {
			return "", errors.New("department is private to another owner")
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_intent_heads(id,owner_id,revision) VALUES($1,$2,'') ON CONFLICT(id) DO NOTHING`, r.Intent, r.Owner); err != nil {
		return "", err
	}
	var head string
	var owner int64
	if err := tx.QueryRowContext(ctx, `SELECT revision,owner_id FROM factory_intent_heads WHERE id=$1 FOR UPDATE`, r.Intent).Scan(&head, &owner); err != nil {
		return "", err
	}
	if owner != r.Owner {
		return "", ErrCommandConflict
	}
	return head, nil
}

func replayIntentRevisionTx(ctx context.Context, tx *sql.Tx, r factory.IntentRevision, raw []byte) error {
	var old []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_intent_revisions WHERE id=$1`, r.ID).Scan(&old); err != nil {
		return err
	}
	same, e := sameJSONDocument(ctx, tx, old, raw)
	if e != nil {
		return e
	}
	if !same {
		return ErrCommandConflict
	}
	return nil
}

func admitIntentPredecessorTx(ctx context.Context, tx *sql.Tx, r factory.IntentRevision, head string) error {
	var headRaw []byte
	var headRev factory.IntentRevision
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_intent_revisions WHERE id=$1`, head).Scan(&headRaw); err != nil {
		return err
	}
	if err := json.Unmarshal(headRaw, &headRev); err != nil {
		return err
	}
	if headRev.State == factory.IntentAccepted && r.Repository != headRev.Repository {
		return errors.New("repository change needs a separate build request")
	}
	// The first commitment pins the repository for the whole intent:
	// any later draft, rejected, declined, or abandoned head keeps it.
	// Revision rows are immutable and the head lock serializes saves,
	// so one committed row read here binds this save exactly.
	var committed string
	err := tx.QueryRowContext(ctx, `SELECT data->>'repository' FROM factory_intent_revisions WHERE intent=$1 AND data->>'state'='accepted' LIMIT 1`, r.Intent).Scan(&committed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && committed != strconv.FormatInt(r.Repository, 10) {
		return errors.New("repository change needs a separate build request")
	}
	return nil
}

func saveIntentRevisionTx(ctx context.Context, tx *sql.Tx, r factory.IntentRevision, raw []byte) error {
	head, err := lockIntentHeadTx(ctx, tx, r)
	if err != nil {
		return err
	}
	if head == r.ID {
		return replayIntentRevisionTx(ctx, tx, r, raw)
	}
	if head != r.Predecessor {
		return ErrStaleRevision
	}
	if head != "" {
		if err = admitIntentPredecessorTx(ctx, tx, r, head); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_intent_revisions(id,intent,data) VALUES($1,$2,$3)`, r.ID, r.Intent, string(raw)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_intent_heads SET revision=$2 WHERE id=$1`, r.Intent, r.ID); err != nil {
		return err
	}
	return recordIntentRevisionFeedTx(ctx, tx, r)
}

func (s *Store) IntentRevision(ctx context.Context, intent string, owner int64) (factory.IntentRevision, error) {
	var r factory.IntentRevision
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT r.data FROM factory_intent_heads h JOIN factory_intent_revisions r ON r.id=h.revision WHERE h.id=$1 AND h.owner_id=$2`, intent, owner).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &r)
	}
	return r, err
}

// SavedIntentRevision returns one immutable revision under its current private
// request owner. It does not require that revision to remain the head or fit in
// a bounded history page. This is receipt lookup, never replay or head mutation.
func (s *Store) SavedIntentRevision(ctx context.Context, intent, revision string, owner int64) (factory.IntentRevision, error) {
	var result factory.IntentRevision
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT r.data FROM factory_intent_heads h
 JOIN factory_intent_revisions r ON r.intent=h.id
 WHERE h.id=$1 AND h.owner_id=$2 AND r.id=$3`, intent, owner, revision).Scan(&raw)
	if err != nil {
		return result, err
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return factory.IntentRevision{}, err
	}
	if result.ID != revision || result.Intent != intent || result.Owner != owner {
		return factory.IntentRevision{}, ErrNotFound
	}
	return result, nil
}

// IntentHistory returns immutable revisions for one owner-private request,
// newest first, bounded to 50 entries. It follows the head backwards through
// recorded predecessors; a repository change after commitment creates a
// separate intent identity and never appears here.
func (s *Store) IntentHistory(ctx context.Context, intent string, owner int64, limit int) ([]factory.IntentRevision, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit < 1 || limit > 50 {
		return nil, errors.New("invalid history limit")
	}
	head, err := s.IntentRevision(ctx, intent, owner)
	if err != nil {
		return nil, err
	}
	result := []factory.IntentRevision{}
	cursor := head.ID
	seen := map[string]bool{}
	for cursor != "" && len(result) < limit {
		if seen[cursor] || len(seen) > 256 {
			return nil, errors.New("invalid intent revision history")
		}
		seen[cursor] = true
		var raw []byte
		var r factory.IntentRevision
		if err = s.db.QueryRowContext(ctx, `SELECT data FROM factory_intent_revisions WHERE id=$1 AND intent=$2`, cursor, intent).Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		if r.Owner != owner {
			return nil, ErrNotFound
		}
		result = append(result, r)
		cursor = r.Predecessor
	}
	return result, nil
}

// RecordPriorityJudgment stores a completed decision directly.
func (s *Store) RecordPriorityJudgment(ctx context.Context, judgment factory.PriorityReceipt) error {
	return s.recordJudgment(ctx, judgment.Snapshot, judgment)
}

// recordJudgment preserves the first completed decision for its input key.
func (s *Store) recordJudgment(ctx context.Context, snapshot string, judgment any) error {
	raw, err := json.Marshal(judgment)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO factory_priority_judgments(snapshot,max_calls,data) VALUES($1,0,$2) ON CONFLICT(snapshot) DO NOTHING`, snapshot, string(raw))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return ErrCommandConflict
	}
	return err
}

// CheckIntentJudgment checks that a new consultation uses the current owned
// intent revision. Completed decisions are read by their input key separately.
func (s *Store) CheckIntentJudgment(ctx context.Context, revision factory.IntentRevision) error {
	var head string
	if err := s.db.QueryRowContext(ctx, `SELECT revision FROM factory_intent_heads WHERE id=$1 AND owner_id=$2`, revision.Intent, revision.Owner).Scan(&head); err != nil {
		return err
	}
	if head != revision.ID {
		return ErrStaleRevision
	}
	return nil
}

func (s *Store) PriorityJudgment(ctx context.Context, snapshot string) (factory.PriorityReceipt, error) {
	var r factory.PriorityReceipt
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_priority_judgments WHERE snapshot=$1 AND data IS NOT NULL`, snapshot).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &r)
	}
	return r, err
}
