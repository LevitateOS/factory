package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/levitateos/sodaos/internal/factory"
)

// AdmitAcceptanceDecision records one immutable issue acceptance and
// advances its repository/issue head. The expected predecessor must equal
// the current head, or be empty when no decision was ever recorded.
// Repeating the identical decision replays without advancing twice; new
// content under a recorded ID conflicts instead of overwriting history.
func replayAcceptanceHeadTx(ctx context.Context, tx *sql.Tx, id string, data []byte) error {
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM issue_acceptance_decisions WHERE id=$1`, id).Scan(&raw); err != nil {
		return err
	}
	same, err := sameJSONDocument(ctx, tx, raw, data)
	if err != nil {
		return err
	}
	if !same {
		return ErrCommandConflict
	}
	return tx.Commit()
}

func (s *Store) AdmitAcceptanceDecision(ctx context.Context, d factory.Acceptance) error {
	if err := d.Validate(); err != nil {
		return err
	}
	issue, err := strconv.ParseInt(d.IssueIndex, 10, 64)
	if err != nil || issue <= 0 {
		return errors.New("invalid acceptance issue index")
	}
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var head sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT decision FROM issue_acceptance_heads WHERE repository=$1 AND issue=$2 FOR UPDATE`, d.Repository, issue).Scan(&head)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	current := ""
	if head.Valid {
		current = head.String
	}
	if current == d.ID {
		return replayAcceptanceHeadTx(ctx, tx, d.ID, data)
	}
	if current != d.Predecessor {
		return ErrStaleRevision
	}
	var taken int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM issue_acceptance_decisions WHERE id=$1`, d.ID).Scan(&taken); err != nil {
		return err
	}
	if taken != 0 {
		return ErrCommandConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO issue_acceptance_decisions(id,repository,issue,predecessor,data) VALUES($1,$2,$3,$4,$5)`, d.ID, d.Repository, issue, d.Predecessor, string(data)); err != nil {
		return fmt.Errorf("acceptance admission failed: %w", err)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO issue_acceptance_heads(repository,issue,decision) VALUES($1,$2,$3)
		ON CONFLICT(repository,issue) DO UPDATE SET decision=$4 WHERE issue_acceptance_heads.decision=$5`, d.Repository, issue, d.ID, d.ID, d.Predecessor)
	if err != nil {
		return fmt.Errorf("acceptance head advance failed: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStaleRevision
	}
	if err = enqueueReadinessRootEventTx(ctx, tx, d.Repository, issue); err != nil {
		return err
	}
	return tx.Commit()
}

// AcceptanceDecision returns one recorded issue acceptance.
func (s *Store) AcceptanceDecision(ctx context.Context, id string) (factory.Acceptance, error) {
	var d factory.Acceptance
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM issue_acceptance_decisions WHERE id=$1`, id).Scan(&data)
	if err == nil {
		err = json.Unmarshal(data, &d)
	}
	return d, err
}

// AcceptanceHead returns the current acceptance decision for one native issue.
func (s *Store) AcceptanceHead(ctx context.Context, repository, issue int64) (string, error) {
	var head string
	err := s.db.QueryRowContext(ctx, `SELECT decision FROM issue_acceptance_heads WHERE repository=$1 AND issue=$2`, repository, issue).Scan(&head)
	return head, err
}

// AcceptanceDepth counts the recorded acceptance chain for one native issue.
func (s *Store) AcceptanceDepth(ctx context.Context, repository, issue int64) (int64, error) {
	var depth int64
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM issue_acceptance_decisions WHERE repository=$1 AND issue=$2`, repository, issue).Scan(&depth)
	return depth, err
}

// WithdrawAcceptanceDecision latches one recorded head decision as
// withdrawn by a current maintainer. Repeating the identical withdrawal
// replays; withdrawing a superseded decision is stale. A later acceptance
// advances the head past the latch with a fresh decision.
func (s *Store) WithdrawAcceptanceDecision(ctx context.Context, repository, issue int64, decision string, withdrawer int64) error {
	if repository <= 0 || issue <= 0 || withdrawer <= 0 || decision == "" {
		return errors.New("invalid acceptance withdrawal")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var head string
	if err = tx.QueryRowContext(ctx, `SELECT decision FROM issue_acceptance_heads WHERE repository=$1 AND issue=$2 FOR UPDATE`, repository, issue).Scan(&head); err != nil {
		return err
	}
	if head != decision {
		return ErrStaleRevision
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO issue_acceptance_withdrawals(repository,issue,decision,withdrawer) VALUES($1,$2,$3,$4) ON CONFLICT(repository,issue,decision) DO NOTHING`, repository, issue, decision, withdrawer)
	if err != nil {
		return fmt.Errorf("acceptance withdrawal failed: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 1 {
		if err = enqueueReadinessRootEventTx(ctx, tx, repository, issue); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AcceptanceWithdrawn reports whether the named decision was withdrawn and,
// when so, the withdrawing maintainer.
func (s *Store) AcceptanceWithdrawn(ctx context.Context, repository, issue int64, decision string) (bool, int64, error) {
	var withdrawer int64
	err := s.db.QueryRowContext(ctx, `SELECT withdrawer FROM issue_acceptance_withdrawals WHERE repository=$1 AND issue=$2 AND decision=$3`, repository, issue, decision).Scan(&withdrawer)
	if errors.Is(err, ErrNotFound) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	return true, withdrawer, nil
}
