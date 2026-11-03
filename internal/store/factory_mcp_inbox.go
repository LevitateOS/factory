package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/levitateos/sodaos/internal/factory"
)

// mcpVisibleDiscussion derives the worker's conversation from its actual
// assignment posts and replies to that assignment. Repository scope alone is
// insufficient: unrelated project discussions stay outside this read.
const mcpVisibleDiscussion = `WITH RECURSIVE visible AS (
 SELECT id,seq,data FROM factory_feed_posts
 WHERE repository=$1 AND NOT COALESCE((data->>'deleted')::boolean,false) AND NOT COALESCE((data->>'revoked')::boolean,false)
 AND (data->>'assignment'=$2 OR (data->'source'->>'kind'='record' AND data->'source'->>'id'=$2)
 OR data->'contribution'->>'assignment'=$2
 OR EXISTS(SELECT 1 FROM factory_feed_posts invitation WHERE invitation.repository=$1
 AND invitation.data->'contribution'->>'source'=factory_feed_posts.id
 AND (invitation.data->'contribution'->>'revision')::bigint=factory_feed_posts.revision
 AND invitation.data->'contribution'->>'assignment'=$2 AND invitation.data->'contribution'->>'state' IN ('invited','responded')))
 UNION
 SELECT c.id,c.seq,c.data FROM factory_feed_posts c JOIN visible p
 ON c.data->>'parent_id'=p.id OR (c.data->'source'->>'kind'='post' AND c.data->'source'->>'id'=p.id)
 WHERE c.repository=$1 AND NOT COALESCE((c.data->>'deleted')::boolean,false) AND NOT COALESCE((c.data->>'revoked')::boolean,false)
)`

func (s *Store) MCPInbox(ctx context.Context, capability factory.MCPCapability, cursor string) (factory.MCPFeedPage, error) {
	return s.mcpDiscussionPage(ctx, capability, "", cursor)
}

func (s *Store) MCPDiscussion(ctx context.Context, capability factory.MCPCapability, postID, cursor string) (factory.MCPFeedPage, error) {
	if !factory.ValidID(postID) {
		return factory.MCPFeedPage{}, errors.New("discussion needs a post_id")
	}
	var visible bool
	if err := s.db.QueryRowContext(ctx, mcpVisibleDiscussion+` SELECT EXISTS(SELECT 1 FROM visible WHERE id=$3)`, capability.Repository, capability.AssignmentID, postID).Scan(&visible); err != nil {
		return factory.MCPFeedPage{}, err
	}
	if visible {
		return s.mcpDiscussionPage(ctx, capability, postID, cursor)
	}
	// Record leg: the assignment record itself is never a comment row, so
	// the comments-only CTE cannot contain it. An invited worker may still
	// read the original result/context through the live record invitation:
	// resolve the invitation for this exact worker/source revision and
	// serve its discussion, which carries the bounded authorized source
	// snapshot/body/context and the invitation reply target.
	var invitation string
	err := s.db.QueryRowContext(ctx, `SELECT i.id FROM factory_feed_posts i
 JOIN factory_assignments a ON a.id=$3 AND a.repository=$1
 WHERE i.repository=$1 AND a.repository=$1
 AND i.data->'contribution'->>'assignment'=$2
 AND i.data->'contribution'->>'source'=$3 AND i.data->'contribution'->>'state' IN ('invited','responded')
 AND (i.data->'contribution'->>'revision')::bigint=a.revision AND a.stage='finished'
 AND i.data->'source'->>'kind'='record' LIMIT 1`, capability.Repository, capability.AssignmentID, postID).Scan(&invitation)
	if err == nil && factory.ValidID(invitation) {
		return s.mcpDiscussionPage(ctx, capability, invitation, cursor)
	}
	return factory.MCPFeedPage{}, ErrNotFound
}

func (s *Store) mcpDiscussionPage(ctx context.Context, capability factory.MCPCapability, postID, cursor string) (factory.MCPFeedPage, error) {
	out := factory.MCPFeedPage{Items: []factory.MCPFeedMessage{}}
	before, err := factory.ParseFeedCursor(cursor)
	if err != nil {
		return out, err
	}
	query := mcpVisibleDiscussion
	if postID == "" {
		query += ` SELECT id,seq FROM visible WHERE ($3::bigint=0 OR seq<$3) ORDER BY seq DESC LIMIT 51`
	} else {
		query += `, thread AS (
   SELECT id,seq,data FROM visible WHERE id=$4
   UNION SELECT c.id,c.seq,c.data FROM visible c JOIN thread p
   ON c.data->>'parent_id'=p.id OR (c.data->'source'->>'kind'='post' AND c.data->'source'->>'id'=p.id)
  ) SELECT id,seq FROM thread WHERE ($3::bigint=0 OR seq<$3) ORDER BY seq DESC LIMIT 51`
	}
	args := []any{capability.Repository, capability.AssignmentID, before}
	if postID != "" {
		args = append(args, postID)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return out, err
	}
	type member struct {
		id  string
		seq int64
	}
	members := []member{}
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.id, &m.seq); err != nil {
			rows.Close()
			return out, err
		}
		members = append(members, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.id)
	}
	details, err := s.FeedPostDetails(ctx, ids)
	if err != nil {
		return out, err
	}
	totalBytes := 128
	for _, m := range members {
		detail, ok := details[m.id]
		if !ok {
			continue
		}
		message := factory.MCPFeedMessage{ID: m.id, ParentID: detail.Record.ParentID, Actor: detail.Record.Author, Author: detail.AuthorName, At: detail.At, Status: detail.Status, Summary: TruncateFeedSummary(detail.Body)}
		if detail.Actor != "" {
			message.Actor = detail.Actor
		}
		if postID != "" {
			message.Body = detail.Body
			message.Consultation = detail.Consultation
			if detail.Payload != nil {
				message.Communication = detail.Payload.Communication
			}
			message.Source = detail.Source
			if detail.Contribution != nil {
				// Worker projection carries the exact bounded authorized
				// assessment without JEV call internals: source, revision,
				// body/context, roles and state stay; per-question calls do
				// not travel to the worker.
				trimmed := *detail.Contribution
				trimmed.Calls = nil
				message.Contribution = &trimmed
			}
		}
		message, encoded, err := boundedMCPFeedMessage(message)
		if err != nil {
			return out, err
		}
		if len(out.Items) == 50 || totalBytes+len(encoded) > factory.MCPMaxPageBytes {
			out.Incomplete = true
			break
		}
		out.Items = append(out.Items, message)
		totalBytes += len(encoded) + 1
		out.NextCursor = factory.FormatFeedCursor(m.seq)
	}
	if !out.Incomplete {
		out.NextCursor = ""
	}
	return out, nil
}

// MCP pages remain bounded even when a real consultation's admitted request
// and answer exceed one page. Preserve exact stored inputs for the full UI;
// this worker projection explicitly names omitted fields and always advances.
// Contribution roles are the first to shed: the invited worker keeps its
// exact source snapshot/body/context and reply target while the page stays
// bounded.
func boundedMCPFeedMessage(message factory.MCPFeedMessage) (factory.MCPFeedMessage, []byte, error) {
	raw, err := json.Marshal(message)
	if err != nil || len(raw) <= factory.MCPMaxPageBytes/2 {
		return message, raw, err
	}
	if message.Consultation != nil {
		copy := *message.Consultation
		copy.State, copy.Instructions, copy.Criteria, copy.Questions, copy.Answer = nil, nil, nil, nil, nil
		message.Consultation = &copy
		message.Omitted = append(message.Omitted, "consultation_inputs_and_answer: full recorded transcript retained in the project conversation")
	}
	if message.Communication != nil {
		message.Communication = nil
		message.Omitted = append(message.Omitted, "structured_communication: full recorded content retained in the project conversation")
	}
	if message.Contribution != nil {
		trimmed := *message.Contribution
		trimmed.Roles = nil
		message.Contribution = &trimmed
		message.Omitted = append(message.Omitted, "contribution_roles: eligible role list retained in the project conversation")
	}
	raw, err = json.Marshal(message)
	if err == nil && len(raw) > factory.MCPMaxPageBytes/2 {
		message.Body = ""
		message.Omitted = append(message.Omitted, "body: use the recorded summary and request specific missing context")
		raw, err = json.Marshal(message)
	}
	if err == nil && len(raw) > factory.MCPMaxPageBytes/2 && message.Contribution != nil {
		message.Contribution = nil
		message.Omitted = append(message.Omitted, "contribution: use the recorded summary and request specific missing context")
		raw, err = json.Marshal(message)
	}
	if err == nil && len(raw) > factory.MCPMaxPageBytes/2 && message.Source != nil {
		message.Source = nil
		message.Omitted = append(message.Omitted, "source: use the recorded summary and request specific missing context")
		raw, err = json.Marshal(message)
	}
	return message, raw, err
}
