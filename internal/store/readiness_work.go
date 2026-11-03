package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

const (
	maxReadinessSources       = 64
	maxReadinessNodes         = 16384
	maxReadinessNodesPerWork  = 4096
	maxReadinessNodeWorkItems = 4
)

var (
	ErrReadinessCapacity = errors.New("readiness work capacity exhausted")
	ErrReadinessPending  = errors.New("readiness work is deferred or capacity is occupied")
)

type ReadinessWork struct {
	ID, Delivery                          string
	Root                                  factory.DependenceRef
	Generation                            int64
	CreationVerified                      bool
	CreatorID, NativeIssueID, CreatedUnix int64
	CreatorWrite                          bool
	TitleDigest, ContentDigest            string
}

const readinessWorkColumns = `id,delivery,repository,issue,generation,creation_verified,creator_id,creator_write,native_issue_id,created_unix,title_digest,content_digest`

type ReadinessWorkNode struct {
	Ref, Cursor factory.DependenceRef
	State       string
}

// EnqueueReadinessWork durably admits or coalesces one root. A nonempty
// delivery makes the source critical and therefore subject to intake
// acknowledgement only by CompleteReadinessWork. An explicit redelivery
// wakes that source without changing its fair turn or saved progress.
func (s *Store) EnqueueReadinessWork(ctx context.Context, id, delivery string, root factory.DependenceRef) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = enqueueReadinessWorkTx(ctx, tx, id, delivery, root); err != nil {
		return err
	}
	if delivery != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE factory_readiness_sources SET retry_at=$2 WHERE id=$1 AND retry_at>$2`, id, time.Now()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func enqueueReadinessWorkTx(ctx context.Context, tx *sql.Tx, id, delivery string, root factory.DependenceRef) error {
	if len(id) == 0 || len(id) > 160 || len(delivery) > 128 || root.Repository <= 0 || root.Issue <= 0 {
		return errors.New("invalid readiness work")
	}
	var locked int
	if err := tx.QueryRowContext(ctx, `SELECT id FROM factory_readiness_budget WHERE id=1 FOR UPDATE`).Scan(&locked); err != nil {
		return err
	}
	var existingDelivery string
	var repository, issue int64
	err := tx.QueryRowContext(ctx, `SELECT delivery,repository,issue FROM factory_readiness_sources WHERE id=$1 FOR UPDATE`, id).Scan(&existingDelivery, &repository, &issue)
	if err == nil {
		if existingDelivery != delivery || repository != root.Repository || issue != root.Issue {
			return ErrCommandConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM factory_readiness_sources`).Scan(&count); err != nil {
		return err
	}
	if count >= maxReadinessSources {
		return ErrReadinessCapacity
	}
	var generation, turn int64
	if err = tx.QueryRowContext(ctx, `SELECT generation,next_turn FROM factory_readiness_budget WHERE id=1`).Scan(&generation, &turn); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_readiness_sources(id,delivery,repository,issue,generation,turn) VALUES($1,$2,$3,$4,$5,$6)`, id, delivery, root.Repository, root.Issue, generation, turn); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE factory_readiness_budget SET next_turn=next_turn+1 WHERE id=1`)
	return err
}

// NextReadinessWork chooses the next due source and advances the round-robin
// turn. Node allocation remains deferred until BeginReadinessWork.
func (s *Store) NextReadinessWork(ctx context.Context, now time.Time) (ReadinessWork, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ReadinessWork{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var locked int
	if err = tx.QueryRowContext(ctx, `SELECT id FROM factory_readiness_budget WHERE id=1 FOR UPDATE`).Scan(&locked); err != nil {
		return ReadinessWork{}, err
	}
	work, err := readReadinessWork(ctx, tx, `SELECT `+readinessWorkColumns+` FROM factory_readiness_sources WHERE retry_at<=$1 ORDER BY turn,id LIMIT 1 FOR UPDATE`, now)
	if err != nil {
		return ReadinessWork{}, err
	}
	var turn int64
	if err = tx.QueryRowContext(ctx, `SELECT next_turn FROM factory_readiness_budget WHERE id=1`).Scan(&turn); err != nil {
		return ReadinessWork{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_readiness_sources SET turn=$2 WHERE id=$1`, work.ID, turn); err != nil {
		return ReadinessWork{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_readiness_budget SET next_turn=next_turn+1 WHERE id=1`); err != nil {
		return ReadinessWork{}, err
	}
	if err = tx.Commit(); err != nil {
		return ReadinessWork{}, err
	}
	return work, nil
}

// BeginReadinessWork allocates the root node on demand and restarts a source
// whose saved progress predates an acceptance-head mutation.
func (s *Store) BeginReadinessWork(ctx context.Context, id string, now time.Time) (ReadinessWork, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ReadinessWork{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var locked int
	if err = tx.QueryRowContext(ctx, `SELECT id FROM factory_readiness_budget WHERE id=1 FOR UPDATE`).Scan(&locked); err != nil {
		return ReadinessWork{}, err
	}
	work, err := readReadinessWork(ctx, tx, `SELECT `+readinessWorkColumns+` FROM factory_readiness_sources WHERE id=$1 AND retry_at<=$2 FOR UPDATE`, id, now)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReadinessWork{}, ErrReadinessPending
		}
		return ReadinessWork{}, err
	}
	var globalGeneration int64
	if err = tx.QueryRowContext(ctx, `SELECT generation FROM factory_readiness_budget WHERE id=1`).Scan(&globalGeneration); err != nil {
		return ReadinessWork{}, err
	}
	var nodeCount int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM factory_readiness_nodes WHERE source=$1`, id).Scan(&nodeCount); err != nil {
		return ReadinessWork{}, err
	}
	if work.Generation != globalGeneration {
		if _, err = tx.ExecContext(ctx, `DELETE FROM factory_readiness_nodes WHERE source=$1`, id); err != nil {
			return ReadinessWork{}, err
		}
		nodeCount = 0
		work.Generation = globalGeneration
		if _, err = tx.ExecContext(ctx, `UPDATE factory_readiness_sources SET generation=$2 WHERE id=$1`, id, globalGeneration); err != nil {
			return ReadinessWork{}, err
		}
	}
	if nodeCount == 0 {
		var active int
		if err = tx.QueryRowContext(ctx, `SELECT count(DISTINCT source) FROM factory_readiness_nodes`).Scan(&active); err != nil {
			return ReadinessWork{}, err
		}
		if active >= maxReadinessNodeWorkItems {
			return ReadinessWork{}, ErrReadinessPending
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO factory_readiness_nodes(source,repository,issue) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, work.Root.Repository, work.Root.Issue); err != nil {
			return ReadinessWork{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return ReadinessWork{}, err
	}
	return work, nil
}

func readReadinessWork(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, query string, args ...any,
) (ReadinessWork, error) {
	var work ReadinessWork
	err := q.QueryRowContext(ctx, query, args...).Scan(&work.ID, &work.Delivery, &work.Root.Repository, &work.Root.Issue, &work.Generation,
		&work.CreationVerified, &work.CreatorID, &work.CreatorWrite, &work.NativeIssueID, &work.CreatedUnix, &work.TitleDigest, &work.ContentDigest)
	return work, err
}

// NextReadinessWorkNode returns a scanning node before an unvisited queued
// node. The caller assesses queued nodes and resumes scanning nodes by cursor.
func (s *Store) NextReadinessWorkNode(ctx context.Context, id string) (ReadinessWorkNode, error) {
	var node ReadinessWorkNode
	err := s.db.QueryRowContext(ctx, `SELECT repository,issue,state,cursor_repository,cursor_issue FROM factory_readiness_nodes WHERE source=$1 AND state<>'done' ORDER BY CASE state WHEN 'scanning' THEN 0 ELSE 1 END,repository,issue LIMIT 1`, id).Scan(&node.Ref.Repository, &node.Ref.Issue, &node.State, &node.Cursor.Repository, &node.Cursor.Issue)
	return node, err
}

// CheckpointReadinessAssessment advances one evaluated node atomically.
func (s *Store) CheckpointReadinessAssessment(ctx context.Context, work ReadinessWork, node ReadinessWorkNode, skip bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockReadinessGeneration(ctx, tx, work); err != nil {
		return err
	}
	state := "scanning"
	if skip {
		state = "done"
	}
	result, err := tx.ExecContext(ctx, `UPDATE factory_readiness_nodes SET state=$4 WHERE source=$1 AND repository=$2 AND issue=$3 AND state='queued'`, work.ID, node.Ref.Repository, node.Ref.Issue, state)
	if err != nil {
		return err
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil {
		return rowsErr
	} else if affected != 1 {
		return ErrReadinessPending
	}
	return tx.Commit()
}

// CheckpointReadinessPage scans and stores one bounded page in one transaction.
func (s *Store) CheckpointReadinessPage(ctx context.Context, work ReadinessWork, node ReadinessWorkNode) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockReadinessGeneration(ctx, tx, work); err != nil {
		return false, err
	}
	var state string
	var storedCursor factory.DependenceRef
	if err = tx.QueryRowContext(ctx, `SELECT state,cursor_repository,cursor_issue FROM factory_readiness_nodes WHERE source=$1 AND repository=$2 AND issue=$3 FOR UPDATE`, work.ID, node.Ref.Repository, node.Ref.Issue).Scan(&state, &storedCursor.Repository, &storedCursor.Issue); err != nil {
		return false, err
	}
	if state != "scanning" || storedCursor != node.Cursor {
		return false, ErrReadinessPending
	}
	matches, cursor, hasMore, err := acceptanceDependantsPage(ctx, tx, node.Ref.Repository, node.Ref.Issue, storedCursor)
	if err != nil {
		return false, err
	}
	var own, total int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM factory_readiness_nodes WHERE source=$1`, work.ID).Scan(&own); err != nil {
		return false, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM factory_readiness_nodes`).Scan(&total); err != nil {
		return false, err
	}
	for _, match := range matches {
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_readiness_nodes WHERE source=$1 AND repository=$2 AND issue=$3)`, work.ID, match.Repository, match.Issue).Scan(&exists); err != nil {
			return false, err
		}
		if !exists {
			own++
			total++
			if own > maxReadinessNodesPerWork || total > maxReadinessNodes {
				return false, ErrReadinessCapacity
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO factory_readiness_nodes(source,repository,issue) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, work.ID, match.Repository, match.Issue); err != nil {
			return false, err
		}
	}
	state = "scanning"
	if !hasMore {
		state = "done"
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_readiness_nodes SET state=$4,cursor_repository=$5,cursor_issue=$6 WHERE source=$1 AND repository=$2 AND issue=$3`, work.ID, node.Ref.Repository, node.Ref.Issue, state, cursor.Repository, cursor.Issue); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return hasMore, nil
}

func lockReadinessGeneration(ctx context.Context, tx *sql.Tx, work ReadinessWork) error {
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM factory_readiness_budget WHERE id=1 FOR UPDATE`).Scan(&generation); err != nil {
		return err
	}
	var saved int64
	var delivery string
	var repository, issue int64
	if err := tx.QueryRowContext(ctx, `SELECT generation,delivery,repository,issue FROM factory_readiness_sources WHERE id=$1 FOR UPDATE`, work.ID).Scan(&saved, &delivery, &repository, &issue); err != nil {
		return err
	}
	if generation != work.Generation || saved != work.Generation || delivery != work.Delivery ||
		repository != work.Root.Repository || issue != work.Root.Issue {
		return ErrReadinessPending
	}
	return nil
}

// CompleteReadinessWork removes a completed source and records critical
// delivery acknowledgement in the same commit. false means nodes remain.
func (s *Store) CompleteReadinessWork(ctx context.Context, work ReadinessWork, now time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockReadinessGeneration(ctx, tx, work); err != nil {
		return false, err
	}
	var count, pending int
	if err = tx.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER (WHERE state<>'done') FROM factory_readiness_nodes WHERE source=$1`, work.ID).Scan(&count, &pending); err != nil {
		return false, err
	}
	if count == 0 || pending != 0 {
		return false, tx.Commit()
	}
	if work.Delivery != "" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO intake_deliveries(delivery,repository,issue,kind,received_at) VALUES($1,$2,$3,'event',$4) ON CONFLICT(delivery) DO NOTHING`, work.Delivery, work.Root.Repository, work.Root.Issue, now.Unix()); err != nil {
			return false, fmt.Errorf("intake delivery log failed: %w", err)
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM intake_deliveries WHERE delivery NOT IN (SELECT delivery FROM intake_deliveries ORDER BY received_at DESC,delivery DESC LIMIT $1)`, MaxIntakeDeliveries); err != nil {
			return false, fmt.Errorf("intake delivery prune failed: %w", err)
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM factory_readiness_sources WHERE id=$1`, work.ID); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// DeferReadinessWork drops partial node state, rotates the source, and applies
// capped exponential retry delay so failures cannot monopolize node capacity.
func (s *Store) DeferReadinessWork(ctx context.Context, id string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var locked int
	if err = tx.QueryRowContext(ctx, `SELECT id FROM factory_readiness_budget WHERE id=1 FOR UPDATE`).Scan(&locked); err != nil {
		return err
	}
	var attempts int
	if err = tx.QueryRowContext(ctx, `SELECT attempts FROM factory_readiness_sources WHERE id=$1 FOR UPDATE`, id).Scan(&attempts); err != nil {
		return err
	}
	attempts++
	if attempts > 6 {
		attempts = 6
	}
	delay := time.Second << (attempts - 1)
	if attempts == 6 {
		delay = time.Minute
	}
	var turn int64
	if err = tx.QueryRowContext(ctx, `SELECT next_turn FROM factory_readiness_budget WHERE id=1`).Scan(&turn); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM factory_readiness_nodes WHERE source=$1`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_readiness_sources SET attempts=$2,retry_at=$3,turn=$4 WHERE id=$1`, id, attempts, now.Add(delay), turn); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_readiness_budget SET next_turn=next_turn+1 WHERE id=1`); err != nil {
		return err
	}
	return tx.Commit()
}

// ReadinessWork returns one source header; absent IDs report ErrNotFound.
func (s *Store) ReadinessWork(ctx context.Context, id string) (ReadinessWork, error) {
	return readReadinessWork(ctx, s.db, `SELECT `+readinessWorkColumns+` FROM factory_readiness_sources WHERE id=$1`, id)
}

// enqueueReadinessRootEventTx invalidates prior progress and coalesces a
// best-effort root after a committed-domain transition, in the same tx.
func enqueueReadinessRootEventTx(ctx context.Context, tx *sql.Tx, repository, issue int64) error {
	if repository <= 0 || issue <= 0 {
		return errors.New("invalid readiness root event")
	}
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM factory_readiness_budget WHERE id=1 FOR UPDATE`).Scan(&generation); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE factory_readiness_budget SET generation=generation+1 WHERE id=1`); err != nil {
		return err
	}
	// Pending creation already owns a critical durable root. Restart its
	// graph under the new head instead of requiring a second queue slot;
	// otherwise a full creation queue could prevent every initial admission.
	var creationPending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_readiness_sources WHERE repository=$1 AND issue=$2 AND creator_id>0 AND delivery<>'')`, repository, issue).Scan(&creationPending); err != nil {
		return err
	}
	if creationPending {
		return nil
	}
	return enqueueReadinessWorkTx(ctx, tx, fmt.Sprintf("root:%d/%d", repository, issue), "", factory.DependenceRef{Repository: repository, Issue: issue})
}
