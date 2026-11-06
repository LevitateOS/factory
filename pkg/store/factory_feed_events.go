package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

const RepositoryOpeningQuestion = "What would you like this repository to do?"

// RecordRepositoryOpening consumes the signed native creation event. The
// repository identity, rather than the webhook delivery ID, makes redelivery
// harmless. A Factory question has no human intake and starts no worker.
func (s *Store) RecordRepositoryOpening(ctx context.Context, repository, at int64) error {
	if repository <= 0 {
		return errors.New("invalid repository creation")
	}
	if at <= 0 {
		at = time.Now().Unix()
	}
	key := fmt.Sprintf("factory:%d:repository-created", repository)
	digest := sha256.Sum256([]byte(key))
	id := hex.EncodeToString(digest[:16])
	data, err := json.Marshal(map[string]any{
		"actor": "factory", "author_name": "Factory", "at": at,
		"status": "repository:waiting", "event_identity": "repository:created",
		"communication": factory.FeedCommunication{
			Kind: "ask", Title: RepositoryOpeningQuestion, Summary: RepositoryOpeningQuestion,
		},
	})
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO factory_feed_posts(id,repository,revision,author,body,idempotency,data)
		VALUES($1,$2,0,'factory',$3,$4,$5) ON CONFLICT(repository,author,idempotency) DO NOTHING`,
		id, repository, RepositoryOpeningQuestion, hex.EncodeToString(digest[:]), string(data))
	return err
}

// factoryFeedEvents are the controller-written feed event kinds. The kind is
// stored as communication.component_id: verification and contribution queries
// filter on it, and the browser presents the event from it.
var factoryFeedEvents = []string{
	"F01", // preparation and admission checks
	"F02", // dispatch and role transition
	"F03", // why this work is next
	"F04", // JEV assessment
	"F05", // native issue or pull request created
	"F06", // merge and issue closure
	"F07", // dependency released
	"F08", // pause, stop and takeover
	"F09", // reconciliation problem or unavailable effect
	"F10", // application outcomes verified
	"F11", // design decision recorded
	"F12", // coder-time allowance exhausted
}

// NoteAllowanceExhausted records one durable allowance-exhausted notice for
// an issue waiting on coder-time sponsorship. Identity is per issue, so
// repeated dispatch passes replay it without duplicating the card; a later
// launch's assignment event marks resumption. Unlike the transient dispatch
// observation (owner/admin-only until restart), the Feed card is visible to
// every authorized reader until the wait clears.
func (s *Store) NoteAllowanceExhausted(ctx context.Context, repository, issue int64, at int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	err = recordFactoryFeedEventTx(ctx, tx, repository, issue, "", fmt.Sprintf("%d:allowance-exhausted", issue), "allowance:exhausted", factory.FeedCommunication{
		ComponentID: "F12",
		Title:       fmt.Sprintf("Issue #%d is waiting on allowance", issue),
		Summary:     "The coder-time sponsorship allowance is used up. Raise Allowance minutes in Settings → Factory authority → Provider allowance and sponsorship to continue.",
	}, at)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// recordFactoryFeedEventTx is private to the canonical store writers. A worker
// cannot call it or choose a Factory event through its MCP report tools.
// Events share the write's transaction and replay identity, so a card only
// exists when its observed effect committed, and a retry cannot duplicate it.
func recordFactoryFeedEventTx(ctx context.Context, tx *sql.Tx, repository, issue int64, assignment, identity, status string, content factory.FeedCommunication, at int64) error {
	if !slices.Contains(factoryFeedEvents, content.ComponentID) || repository <= 0 || identity == "" {
		return errors.New("invalid canonical Factory feed event")
	}
	content.Kind = "system"
	if assignment != "" && content.ParentID == "" {
		var parent string
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT data->'planning_context'->'IssueMaker'->>'post' FROM factory_assignments WHERE id=$1),'')`, assignment).Scan(&parent); err != nil {
			return err
		}
		if parent != "" {
			content.ParentID = parent
		}
	}
	if at <= 0 {
		at = time.Now().Unix()
	}
	key := fmt.Sprintf("factory:%d:%s:%s", repository, content.ComponentID, identity)
	digest := sha256.Sum256([]byte(key))
	id := hex.EncodeToString(digest[:16])
	key = hex.EncodeToString(digest[:])
	data, err := json.Marshal(map[string]any{
		"actor": "factory", "author_name": "Factory", "issue": issue,
		"assignment": assignment, "at": at, "status": status, "communication": content, "parent_id": content.ParentID, "event_identity": identity,
	})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO factory_feed_posts(id,repository,revision,author,body,idempotency,data)
		VALUES($1,$2,0,'factory',$3,$4,$5) ON CONFLICT(repository,author,idempotency) DO NOTHING`,
		id, repository, content.Title+"\n\n"+content.Summary, key, string(data))
	return err
}

// A run may predate dispatch records (for example a supervised operator run).
// Only a recorded project/repository binding is sufficient for a scoped card.
func factoryRunFeedScopeTx(ctx context.Context, tx *sql.Tx, run, project string) (repository, issue int64, assignment string, err error) {
	err = tx.QueryRowContext(ctx, `SELECT repository,issue,id FROM factory_assignments WHERE run=$1`, run).Scan(&repository, &issue, &assignment)
	if errors.Is(err, sql.ErrNoRows) {
		var native bool
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(data->'native'!='null'::jsonb,FALSE) FROM factory_runs WHERE id=$1`, run).Scan(&native); err != nil {
			return
		}
		if native {
			err = tx.QueryRowContext(ctx, `SELECT repository FROM factory_native_workspaces WHERE id=$1`, project).Scan(&repository)
		} else {
			err = tx.QueryRowContext(ctx, `SELECT r.id`+nativeProjectCollaborationJoin+` WHERE p.id=$1`, project).Scan(&repository)
		}
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
	}
	return repository, issue, assignment, err
}
