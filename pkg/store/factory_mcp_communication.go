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

// RecordWorkerPost records a worker message and its library effects directly
// in the post transaction. The post's normal idempotency key owns repeat calls;
// there is no second submission record or receipt to recover.
func (s *Store) RecordWorkerPost(ctx context.Context, submission factory.MCPSubmission) (FeedPostDetail, error) {
	var out FeedPostDetail
	if (!factory.MCPIsCommunication(submission.Tool) && submission.Tool != factory.MCPToolResolvePost) || !factory.ValidID(submission.SubmissionID) {
		return out, errors.New("worker post needs a communication tool and stable identity")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	capability, err := lockMCPCapabilityTx(ctx, tx, submission.CapabilityID, submission.AssignmentID)
	if err != nil {
		return out, err
	}
	if !factory.MCPToolAdmitted(capability.Profile, submission.Tool) {
		return out, errors.New("communication outlet not admitted for this worker")
	}
	if capability.Expired(time.Now()) {
		return out, ErrMCPSubmissionCapabilityExpired
	}
	if finished, err := assignmentFinishedTx(ctx, tx, capability.AssignmentID); err != nil {
		return out, err
	} else if finished {
		return out, ErrMCPSubmissionAssignmentFinished
	}
	postID := factory.MCPInputDigest([]byte(capability.AssignmentID + "\n" + submission.SubmissionID))[:32]
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_feed_posts WHERE id=$1 AND repository=$2 AND author=$3)`, postID, capability.Repository, "worker:"+capability.AssignmentID).Scan(&exists); err != nil {
		return out, err
	}
	if exists {
		if err := tx.Commit(); err != nil {
			return out, err
		}
		return s.FeedPost(ctx, postID)
	}
	switch submission.Tool {
	case factory.MCPToolResolvePost:
		if err := recordPostResolutionTx(ctx, tx, capability, submission); err != nil {
			return out, err
		}
	case factory.MCPToolRecordJourneyResult:
		if _, err := recordVerificationJourneyPostTx(ctx, tx, capability, submission); err != nil {
			return out, err
		}
	case factory.MCPToolRecordDiagnosis:
		if _, err := recordRepairDiagnosisPostTx(ctx, tx, capability, submission); err != nil {
			return out, err
		}
	case factory.MCPToolDraftRepair:
		if _, err := recordRepairDraftPostTx(ctx, tx, capability, submission); err != nil {
			return out, err
		}
	default:
		content, err := factory.DecodeMCPCommunication(submission.Tool, submission.Payload)
		if err != nil {
			return out, err
		}
		if content.ParentID != "" {
			var parentKind string
			if err = tx.QueryRowContext(ctx, `SELECT COALESCE(data->'communication'->>'kind','') FROM factory_feed_posts WHERE id=$1 AND repository=$2 AND NOT COALESCE((data->>'deleted')::boolean,false) AND NOT COALESCE((data->>'revoked')::boolean,false) FOR SHARE`, content.ParentID, capability.Repository).Scan(&parentKind); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return out, errors.New("reply target is not available in this project")
				}
				return out, err
			}
			if err := admitWorkerDiscussionReplyTx(ctx, tx, capability, content.ParentID); err != nil {
				return out, err
			}
			if submission.Tool == factory.MCPToolAnswer && parentKind != "ask" {
				return out, errors.New("answer must refer to an ask post")
			}
		}
		if err := resolveCommunicationArtifactsTx(ctx, tx, capability.Repository, &content); err != nil {
			return out, err
		}
		if _, err := recordWorkerPostTx(ctx, tx, capability, submission, content); err != nil {
			return out, err
		}
	}
	if err := tx.Commit(); err != nil {
		return out, err
	}
	return s.FeedPost(ctx, postID)
}

func recordWorkerPostTx(ctx context.Context, tx *sql.Tx, capability factory.MCPCapability, submission factory.MCPSubmission, content factory.FeedCommunication) (string, error) {
	if capability.Profile == factory.MCPProfileIssueMaker && content.ParentID == "" {
		if err := tx.QueryRowContext(ctx, `SELECT data->'planning_context'->'IssueMaker'->>'post' FROM factory_assignments WHERE id=$1`, capability.AssignmentID).Scan(&content.ParentID); err != nil {
			return "", err
		}
	}
	var issue int64
	var err error
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT issue FROM factory_assignments WHERE id=$1 AND repository=$2),0)`, capability.AssignmentID, capability.Repository).Scan(&issue); err != nil {
		return "", err
	}
	postID := factory.MCPInputDigest([]byte(capability.AssignmentID + "\n" + submission.SubmissionID))[:32]
	if err = createCommunicationLibraryTx(ctx, tx, capability, submission.Tool, &content, postID); err != nil {
		return "", err
	}
	fields := map[string]any{
		"issue": issue, "parent_id": content.ParentID, "at": time.Now().Unix(), "author_name": recordPersona(capability.Profile),
		"actor": string(capability.Profile), "assignment": capability.AssignmentID, "run": capability.RunID,
		"project": capability.Project, "communication": content,
	}
	switch submission.Tool {
	case factory.MCPToolResolvePost:
		fields["resolution"] = json.RawMessage(submission.Payload)
	case factory.MCPToolRecordJourneyResult:
		fields["journey"] = json.RawMessage(submission.Payload)
	case factory.MCPToolRecordDiagnosis:
		fields["diagnosis"] = json.RawMessage(submission.Payload)
	case factory.MCPToolDraftRepair:
		fields["repair_draft"] = string(submission.Payload)
	}
	if content.ParentID == "" && (content.Kind == "partial" || content.Kind == "reply") {
		fields["source"] = factory.PostSource{Kind: factory.PostSourceRecord, ID: capability.AssignmentID, Issue: issue, Ref: capability.AcceptedRevision}
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	// The full typed content lives in data; body is the concise textual equivalent
	// used by existing reply/JEV source readers, within their canonical body bound.
	body := content.Title + "\n\n" + content.Summary
	if content.Question != "" && !strings.Contains(body, content.Question) {
		body += "\n\n" + content.Question
	}
	if len([]rune(body)) > factory.PostBodyLimit {
		body = string([]rune(body)[:factory.PostBodyLimit])
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO factory_feed_posts(id,repository,revision,author,body,idempotency,data) VALUES($1,$2,0,$3,$4,$5,$6)`, postID, capability.Repository, "worker:"+capability.AssignmentID, body, submission.SubmissionID, string(data))
	if err != nil {
		return "", err
	}
	if content.ParentID != "" && submission.Tool != factory.MCPToolResolvePost {
		return postID, contributionReplyTx(ctx, tx, capability, content.ParentID, postID)
	}
	return postID, nil
}

func applyFeedCommunicationDetail(detail *FeedPostDetail, raw []byte) error {
	var data struct {
		Actor         string                      `json:"actor"`
		Status        string                      `json:"status"`
		Assignment    string                      `json:"assignment"`
		EventIdentity string                      `json:"event_identity"`
		Communication *factory.FeedCommunication  `json:"communication"`
		Consultation  *factory.FeedConsultation   `json:"consultation"`
		Contribution  *factory.ContributionReview `json:"contribution"`
		Source        *factory.PostSource         `json:"source"`
		Work          *mcpSubmittedWork           `json:"work"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}
	if data.Source != nil {
		detail.Source = data.Source
		if detail.Record.ParentID == "" && data.Source.Kind == factory.PostSourcePost {
			detail.Record.ParentID = data.Source.ID
		}
	}
	if data.Work != nil {
		detail.Actor, detail.Assignment, detail.Status = data.Actor, data.Assignment, data.Work.State
	}
	if data.Communication != nil {
		if data.Actor == "factory" {
			detail.EventIdentity = data.EventIdentity
		}
		detail.Actor = data.Actor
		detail.Assignment = data.Assignment
		detail.Status = data.Communication.Kind
		if (data.Actor == "factory" || data.Actor == "issue_maker") && data.Status != "" {
			detail.Status = data.Status
		}
		detail.Payload = &factory.FeedPayload{Communication: data.Communication}
	}
	if data.Consultation != nil {
		detail.Actor, detail.Assignment, detail.Status = data.Actor, data.Assignment, data.Status
		detail.Consultation = data.Consultation
	}
	if data.Contribution != nil {
		detail.Actor, detail.Assignment, detail.Status = data.Actor, data.Assignment, data.Status
		detail.Contribution = data.Contribution
	}
	return nil
}

// feedQuestionStates derives response arrival without mutating the original
// question or claiming that the answer was adopted by the worker.
func (s *Store) feedQuestionStates(ctx context.Context, details map[string]FeedPostDetail) error {
	ids := []string{}
	for id, detail := range details {
		if detail.Payload != nil && detail.Payload.Communication != nil && detail.Payload.Communication.Kind == "ask" {
			detail.Payload.Communication.QuestionState = "open"
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	// The incorporated flag rides the same derivation: an answer the
	// worker adopted moves its question past answered without
	// claiming anything about unrecorded replies.
	rows, err := s.db.QueryContext(ctx, `SELECT p.id,bool_or(c.data->'communication'->>'kind'='answer'),
 bool_or(c.data->'communication'->>'kind'='answer' AND r.data->'resolution'->>'resolution'='incorporated')
 FROM factory_feed_posts p JOIN factory_feed_posts c ON c.repository=p.repository
 AND (c.data->>'parent_id'=p.id OR (c.data->'source'->>'kind'='post' AND c.data->'source'->>'id'=p.id))
 LEFT JOIN factory_feed_posts r ON r.repository=c.repository AND r.data->'resolution'->>'post'=c.id
 WHERE p.id=ANY($1) AND NOT COALESCE((c.data->>'deleted')::boolean,false) AND NOT COALESCE((c.data->>'revoked')::boolean,false)
 GROUP BY p.id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var answered, incorporated sql.NullBool
		if err := rows.Scan(&id, &answered, &incorporated); err != nil {
			return err
		}
		detail := details[id]
		state := "replied"
		if answered.Valid && answered.Bool {
			state = "answered"
		}
		if incorporated.Valid && incorporated.Bool {
			state = "incorporated"
		}
		detail.Payload.Communication.QuestionState = state
	}
	return rows.Err()
}
