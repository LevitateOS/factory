package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

// The library adds its own tables without rewriting the existing appliance data.
// Its current projection and immutable revisions commit with the worker post.
func initializeLibrarySchema(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS factory_library_items(id TEXT PRIMARY KEY,repository BIGINT NOT NULL,revision BIGINT NOT NULL,kind TEXT NOT NULL,removed BOOLEAN NOT NULL DEFAULT FALSE,data JSONB NOT NULL);
CREATE INDEX IF NOT EXISTS factory_library_repository ON factory_library_items(repository,id);
CREATE INDEX IF NOT EXISTS factory_library_search ON factory_library_items USING GIN(to_tsvector('simple',coalesce(data->>'title','') || ' ' || coalesce(data->>'body','') || ' ' || coalesce(data->>'scope','')));
CREATE TABLE IF NOT EXISTS factory_library_revisions(item TEXT NOT NULL REFERENCES factory_library_items(id),revision BIGINT NOT NULL,repository BIGINT NOT NULL,data JSONB NOT NULL,PRIMARY KEY(item,revision));`)
	return err
}

type libraryQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readLibraryItem(ctx context.Context, q libraryQuerier, repository int64, id string, lock bool) (factory.LibraryItem, error) {
	var item factory.LibraryItem
	var raw []byte
	query := `SELECT data FROM factory_library_items WHERE repository=$1 AND id=$2`
	if lock {
		query += " FOR UPDATE"
	}
	if err := q.QueryRowContext(ctx, query, repository, id).Scan(&raw); err != nil {
		return item, err
	}
	err := json.Unmarshal(raw, &item)
	return item, err
}

func (s *Store) LibrarySearch(ctx context.Context, repository int64, query, kind, cursor string, includeRemoved, currentOnly bool) (factory.LibraryPage, error) {
	page := factory.LibraryPage{Items: []factory.LibraryItem{}, Complete: true}
	if err := (factory.LibraryQuery{Query: query, Kind: kind, Cursor: cursor, History: includeRemoved, CurrentOnly: currentOnly}).Validate(repository); err != nil {
		return page, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM factory_library_items WHERE repository=$1 AND ($2 OR NOT removed) AND ($3='' OR kind=$3) AND id>$4 AND ($5='' OR to_tsvector('simple',coalesce(data->>'title','') || ' ' || coalesce(data->>'body','') || ' ' || coalesce(data->>'scope','')) @@ plainto_tsquery('simple',$5)) AND (NOT $6 OR (NOT removed AND COALESCE(data->>'state','') NOT IN ('superseded','retracted'))) ORDER BY id LIMIT 51`, repository, includeRemoved, kind, cursor, query, currentOnly)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	bytes := 0
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return page, err
		}
		if len(page.Items) == 50 || (bytes+len(raw) > factory.MCPMaxPageBytes && len(page.Items) > 0) {
			page.Complete = false
			page.NextCursor = page.Items[len(page.Items)-1].ID
			break
		}
		var item factory.LibraryItem
		if err = json.Unmarshal(raw, &item); err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
		bytes += len(raw)
	}
	return page, rows.Err()
}

func (s *Store) LibraryRead(ctx context.Context, repository int64, id string, includeRemoved bool) (factory.LibraryRead, error) {
	out := factory.LibraryRead{History: []factory.LibraryRevision{}}
	item, err := readLibraryItem(ctx, s.db, repository, id, false)
	if err != nil {
		return out, err
	}
	if item.Removed && !includeRemoved {
		return out, ErrNotFound
	}
	out.Item = item
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM factory_library_revisions WHERE repository=$1 AND item=$2 ORDER BY revision DESC LIMIT 20`, repository, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	bytes := 0
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return out, err
		}
		if bytes+len(raw) > factory.MCPMaxPageBytes {
			break
		}
		var revision factory.LibraryRevision
		if err = json.Unmarshal(raw, &revision); err != nil {
			return out, err
		}
		out.History = append(out.History, revision)
		bytes += len(raw)
	}
	return out, rows.Err()
}

func writeLibraryRevision(ctx context.Context, tx *sql.Tx, item factory.LibraryItem, reason, actor string) error {
	item.UpdatedAt = time.Now().Unix()
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO factory_library_items(id,repository,revision,kind,removed,data) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,removed=excluded.removed,data=excluded.data`, item.ID, item.Repository, item.Revision, item.Kind, item.Removed, string(raw))
	if err != nil {
		return err
	}
	revision, err := json.Marshal(factory.LibraryRevision{Item: item, Reason: reason, Actor: actor})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO factory_library_revisions(item,revision,repository,data) VALUES($1,$2,$3,$4)`, item.ID, item.Revision, item.Repository, string(revision))
	return err
}

func createCommunicationLibraryTx(ctx context.Context, tx *sql.Tx, cap factory.MCPCapability, tool factory.MCPTool, content *factory.FeedCommunication, postID string) error {
	if tool != factory.MCPToolLearned && tool != factory.MCPToolRecordDecision {
		return nil
	}
	if content.RevisesItem != "" {
		return amendCommunicationLibraryTx(ctx, tx, cap, content, postID)
	}
	item := factory.LibraryItem{ID: factory.MCPInputDigest([]byte("library\n" + postID))[:32], Repository: cap.Repository, Revision: 1, Kind: "knowledge", Title: content.Title, Body: content.Summary, Scope: content.Scope, EvidenceRefs: content.EvidenceRefs, OwnerRole: string(cap.Profile), Assignment: cap.AssignmentID, SourcePost: postID, State: "observed"}
	if tool == factory.MCPToolRecordDecision {
		item.Kind = "decision"
		item.State = "current"
		item.Decision = content.Decision
		// The actual decision owner defaults to the logging worker;
		// naming another owner references a choice made by an
		// authorized person/controller. The revision actor below
		// always stays the logging worker.
		if owner := strings.TrimSpace(content.DecisionOwner); owner != "" {
			item.OwnerRole = owner
		}
		item.Rationale = content.Rationale
		item.Alternatives = content.Alternatives
		if item.Scope == "" {
			item.Scope = cap.Subject
		}
	}
	if err := writeLibraryRevision(ctx, tx, item, "Recorded by "+string(cap.Profile), "worker:"+cap.AssignmentID); err != nil {
		return err
	}
	content.LibraryItemID = item.ID
	content.LibraryRevision = 1
	return nil
}

// amendCommunicationLibraryTx revises a recorded knowledge entry under
// compare-and-swap: the expected revision must match the current row,
// so concurrent amendments serialize instead of silently overwriting.
// An amendment replaces the entry body and restores the observed
// state; a challenge keeps the body and marks it contested. Either
// records a full revision with its actor and source post, preserving
// one identity across work, feed and retrieval. Removed entries and
// decisions (R32.5) refuse here.
func amendCommunicationLibraryTx(ctx context.Context, tx *sql.Tx, cap factory.MCPCapability, content *factory.FeedCommunication, postID string) error {
	item, err := readLibraryItem(ctx, tx, cap.Repository, content.RevisesItem, true)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("amended entry is not available in this project")
		}
		return err
	}
	if item.Removed {
		return errors.New("removed entry cannot be revised")
	}
	if item.Kind != "knowledge" {
		return errors.New("decisions amend through their own owner")
	}
	if item.Revision != content.ExpectedRevision {
		return errors.New("knowledge revision changed under this amendment")
	}
	verb := "Amended"
	// A challenge keeps the recorded body and only marks it contested;
	// the counter-statement lives on the feed post and in the revision
	// reason. An amendment replaces the body and restores observed.
	if content.Challenge {
		item.State = "contested"
		verb = "Challenged"
	} else {
		item.Title, item.Body, item.Scope, item.EvidenceRefs = content.Title, content.Summary, content.Scope, content.EvidenceRefs
		item.State = "observed"
	}
	item.Revision++
	item.SourcePost = postID
	if err := writeLibraryRevision(ctx, tx, item, verb+" by "+string(cap.Profile)+": "+content.Title, "worker:"+cap.AssignmentID); err != nil {
		return err
	}
	content.LibraryItemID = item.ID
	content.LibraryRevision = item.Revision
	return nil
}
