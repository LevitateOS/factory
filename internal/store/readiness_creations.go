package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

// EnqueueReadinessCreation retains original creation authority until admission
// and assessment complete. Redelivery may enrich an incomplete hint, but cannot
// replace authenticated original facts.
func (s *Store) EnqueueReadinessCreation(ctx context.Context, work ReadinessWork) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = enqueueReadinessCreationTx(ctx, tx, work); err != nil {
		return err
	}
	return tx.Commit()
}

func enqueueReadinessCreationTx(ctx context.Context, tx *sql.Tx, work ReadinessWork) error {
	if err := admitReadinessCreation(work); err != nil {
		return err
	}
	if err := enqueueReadinessWorkTx(ctx, tx, work.ID, work.Delivery, work.Root); err != nil {
		return err
	}
	saved, err := readReadinessWork(ctx, tx, `SELECT `+readinessWorkColumns+` FROM factory_readiness_sources WHERE id=$1 FOR UPDATE`, work.ID)
	if err != nil {
		return err
	}
	if err := checkSavedCreationConflict(saved, work); err != nil {
		return err
	}
	if !saved.CreationVerified {
		if err := fillUnverifiedCreationTx(ctx, tx, saved, &work); err != nil {
			return err
		}
		if work.CreationVerified {
			if err := recordFactoryFeedEventTx(ctx, tx, work.Root.Repository, work.Root.Issue, "", fmt.Sprintf("issue:%d", work.Root.Issue), "native_issue:created", factory.FeedCommunication{
				ComponentID: "F05", Title: fmt.Sprintf("Issue #%d created", work.Root.Issue), Summary: "The native repository confirmed this issue. Readiness assessment follows.",
				Items: []factory.FeedCommunicationItem{{Label: fmt.Sprintf("Issue #%d", work.Root.Issue), State: "created"}},
			}, work.CreatedUnix); err != nil {
				return err
			}
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE factory_readiness_sources SET retry_at=$2 WHERE id=$1 AND retry_at>$2`, work.ID, time.Now())
	return err
}

// admitReadinessCreation refuses malformed readiness creations.
func admitReadinessCreation(work ReadinessWork) error {
	if work.CreatorID <= 0 || work.Delivery == "" || work.NativeIssueID < 0 || work.CreatedUnix < 0 ||
		!creationDigest(work.TitleDigest) || !creationDigest(work.ContentDigest) ||
		(work.CreationVerified && (work.NativeIssueID == 0 || work.CreatedUnix == 0 || work.TitleDigest == "" || work.ContentDigest == "")) {
		return errors.New("invalid readiness creation")
	}
	return nil
}

// checkSavedCreationConflict refuses creations that contradict the
// recorded creator, native issue, timestamps, digests or write.
func checkSavedCreationConflict(saved, work ReadinessWork) error {
	if saved.CreatorID != 0 && (saved.CreatorID != work.CreatorID ||
		(saved.NativeIssueID != 0 && work.NativeIssueID != 0 && saved.NativeIssueID != work.NativeIssueID) ||
		(saved.CreatedUnix != 0 && work.CreatedUnix != 0 && saved.CreatedUnix != work.CreatedUnix) ||
		(saved.TitleDigest != "" && work.TitleDigest != "" && saved.TitleDigest != work.TitleDigest) ||
		(saved.ContentDigest != "" && work.ContentDigest != "" && saved.ContentDigest != work.ContentDigest) ||
		(saved.CreationVerified && work.CreationVerified && saved.CreatorWrite != work.CreatorWrite)) {
		return ErrCommandConflict
	}
	return nil
}

// fillUnverifiedCreationTx carries recorded creation facts forward
// into one unverified creation work item.
func fillUnverifiedCreationTx(ctx context.Context, tx *sql.Tx, saved ReadinessWork, work *ReadinessWork) error {
	if work.NativeIssueID == 0 {
		work.NativeIssueID = saved.NativeIssueID
	}
	if work.CreatedUnix == 0 {
		work.CreatedUnix = saved.CreatedUnix
	}
	if work.TitleDigest == "" {
		work.TitleDigest = saved.TitleDigest
	}
	if work.ContentDigest == "" {
		work.ContentDigest = saved.ContentDigest
	}
	_, err := tx.ExecContext(ctx, `UPDATE factory_readiness_sources SET creation_verified=$2,creator_id=$3,creator_write=$4,native_issue_id=$5,created_unix=$6,title_digest=$7,content_digest=$8 WHERE id=$1`,
		work.ID, work.CreationVerified, work.CreatorID, work.CreatorWrite, work.NativeIssueID, work.CreatedUnix, work.TitleDigest, work.ContentDigest)
	return err
}

func creationDigest(value string) bool {
	if value == "" {
		return true
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func (s *Store) CreationRecoveryCursor(ctx context.Context, repository int64) (int64, error) {
	if repository <= 0 {
		return 0, errors.New("invalid creation repository")
	}
	var cursor int64
	err := s.db.QueryRowContext(ctx, `SELECT task_id FROM factory_creation_cursors WHERE repository=$1`, repository).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return cursor, err
}

// CreationRecoveryRepositories pages configured repositories without changing
// their policy revisions or treating the global work capacity as a product limit.
func (s *Store) CreationRecoveryRepositories(ctx context.Context, after int64, limit int) ([]int64, error) {
	if after < 0 || limit < 1 || limit > 64 {
		return nil, errors.New("invalid creation repository page")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT repository FROM factory_policies WHERE repository>$1 ORDER BY repository LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var repositories []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		repositories = append(repositories, id)
	}
	return repositories, rows.Err()
}

// EnqueueReadinessCreationPage advances native history only after every original
// observation is durable. Capacity, identity conflicts and stale cursors roll
// back the whole page, so a later serving pass can retry the same native tasks.
func (s *Store) EnqueueReadinessCreationPage(ctx context.Context, repository, expected, next int64, works []ReadinessWork) error {
	if repository <= 0 || expected < 0 || next < expected || len(works) > 16 {
		return errors.New("invalid creation page")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var locked int
	if err = tx.QueryRowContext(ctx, `SELECT id FROM factory_readiness_budget WHERE id=1 FOR UPDATE`).Scan(&locked); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_creation_cursors(repository,task_id) VALUES($1,0) ON CONFLICT DO NOTHING`, repository); err != nil {
		return err
	}
	var saved int64
	if err = tx.QueryRowContext(ctx, `SELECT task_id FROM factory_creation_cursors WHERE repository=$1 FOR UPDATE`, repository).Scan(&saved); err != nil {
		return err
	}
	if saved != expected {
		return ErrStaleRevision
	}
	for _, work := range works {
		if work.Root.Repository != repository {
			return ErrCommandConflict
		}
		var priorRepo, priorIssue int64
		err = tx.QueryRowContext(ctx, `SELECT repository,issue FROM intake_deliveries WHERE delivery=$1`, work.Delivery).Scan(&priorRepo, &priorIssue)
		if err == nil {
			if priorRepo != repository || priorIssue != work.Root.Issue {
				return ErrCommandConflict
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err = enqueueReadinessCreationTx(ctx, tx, work); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE factory_creation_cursors SET task_id=$2 WHERE repository=$1`, repository, next); err != nil {
		return err
	}
	return tx.Commit()
}
