package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

// Consultation comments use the existing authorized conversation store. The
// comment owns the displayed question and actual answer.
// Neither reading a comment nor refreshing its thread dispatches a call.
type SystemOneConsultation struct {
	factory.FeedConsultation
	Repository int64
	Role       factory.MCPProfile
}

const (
	ConsultationStatusPending     = "pending"
	ConsultationStatusReturned    = "returned"
	ConsultationStatusFailed      = "failed"
	ConsultationStatusUnavailable = "unavailable"
	ConsultationStatusUncertain   = "uncertain"
)

func redactConsultationValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			name := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
			if name == "authorization" || name == "api_key" || name == "apikey" || name == "password" || name == "secret" || name == "credentials" || name == "token" || strings.HasSuffix(name, "_token") || strings.HasSuffix(name, "_secret") || strings.HasSuffix(name, "_password") {
				out[key] = "[redacted protected input]"
			} else {
				out[key] = redactConsultationValue(item)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = redactConsultationValue(item)
		}
		return out
	default:
		return value
	}
}

func boundedConsultationValue(value any, limit int) any {
	if value == nil {
		return nil
	}
	value = redactConsultationValue(value)
	raw, err := json.Marshal(value)
	if err != nil {
		return "[input unavailable]"
	}
	if len(raw) <= limit {
		return value
	}
	runes := []rune(string(raw))
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes) + "… [recorded input truncated]"
}

// RecordConsultationRequest records the exact permitted input before dispatch.
// The canonical assignment record is the consulting worker's stable work post.
// A repeated snapshot retains its original request, lifecycle and timestamp.
func (s *Store) RecordConsultationRequest(ctx context.Context, c SystemOneConsultation) error {
	if c.Snapshot == "" || c.Repository < 1 || !factory.ValidID(c.Assignment) || factory.MCPPhaseOf(c.Role) == "" || c.Tool == "" || c.Kind == "" {
		return errors.New("consultation needs its call, worker, scope and tool")
	}
	if c.GroupID == "" {
		c.GroupID = c.Snapshot
	}
	if c.CreatedUnix <= 0 {
		c.CreatedUnix = time.Now().Unix()
	}
	c.UpdatedUnix, c.Status = c.CreatedUnix, ConsultationStatusPending
	c.State = boundedConsultationValue(c.State, factory.MCPMaxSubmissionPayload)
	c.Instructions = boundedConsultationValue(c.Instructions, factory.MCPMaxSubmissionPayload)
	c.Criteria = boundedConsultationValue(c.Criteria, factory.MCPMaxSubmissionPayload)
	for id, question := range c.Questions {
		c.Questions[id] = boundedConsultationValue(question, factory.MCPMaxSubmissionPayload)
	}
	var issue int64
	if err := s.db.QueryRowContext(ctx, `SELECT issue FROM factory_assignments WHERE id=$1 AND repository=$2`, c.Assignment, c.Repository).Scan(&issue); err != nil {
		return err
	}
	data, err := json.Marshal(map[string]any{
		"actor": string(c.Role), "author_name": recordPersona(c.Role), "assignment": c.Assignment, "parent_id": c.Assignment,
		"issue": issue, "at": c.CreatedUnix, "status": "consultation:pending",
		"source":       factory.PostSource{Kind: factory.PostSourceRecord, ID: c.Assignment, Issue: issue, Ref: c.SourceRevision},
		"consultation": c.FeedConsultation,
	})
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO factory_feed_posts(id,repository,revision,author,body,idempotency,data)
	 VALUES($1,$2,0,$3,$4,$5,$6) ON CONFLICT(id) DO NOTHING`, factory.FeedConsultationPostID(c.Snapshot), c.Repository,
		"worker:"+c.Assignment, "Consulted JEV · "+c.Kind, factory.MCPInputDigest([]byte("consultation:"+c.Snapshot)), string(data))
	return err
}

// RecordConsultationResult advances only the recorded outcome. A returned
// answer cannot be overwritten by a later timeout/replay. Missing rows fail.
func (s *Store) RecordConsultationResult(ctx context.Context, snapshot, model, status string, answer map[string]any) error {
	switch status {
	case ConsultationStatusReturned, ConsultationStatusFailed, ConsultationStatusUnavailable, ConsultationStatusUncertain:
	default:
		return errors.New("unknown consultation outcome")
	}
	if status == ConsultationStatusReturned && len(answer) == 0 {
		return errors.New("returned consultation needs its actual answer")
	}
	patch := map[string]any{"status": status, "updated_unix": time.Now().Unix()}
	if answer != nil {
		patch["answer"], patch["model"] = answer, model
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE factory_feed_posts
	 SET data=jsonb_set(data,'{consultation}',data->'consultation' || $2::jsonb) || jsonb_build_object('status',$3::text)
	 WHERE id=$1 AND data ? 'consultation' AND (data->'consultation'->>'status'<>'returned' OR $4='returned')`,
		factory.FeedConsultationPostID(snapshot), string(raw), "consultation:"+status, status)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		var exists bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_feed_posts WHERE id=$1 AND data ? 'consultation')`, factory.FeedConsultationPostID(snapshot)).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrNotFound
		}
	}
	return nil
}
