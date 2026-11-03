package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
)

func verificationJourneyScopeTx(ctx context.Context, tx *sql.Tx, cap factory.MCPCapability) (factory.Assignment, factory.IntentRevision, error) {
	var a factory.Assignment
	var r factory.IntentRevision
	if cap.Profile != factory.MCPProfileTestPilot {
		return a, r, errors.New("verification scope requires Test Pilot authority")
	}
	var recorded factory.MCPCapability
	var state string
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT state,data FROM factory_mcp_capabilities WHERE id=$1 FOR SHARE`, cap.ID).Scan(&state, &raw); err != nil {
		return a, r, err
	}
	if json.Unmarshal(raw, &recorded) != nil || state != factory.MCPCapabilityActive || recorded.ScopeDigest() != cap.ScopeDigest() || recorded.Expired(time.Now()) {
		return a, r, errors.New("verification scope capability is no longer active")
	}
	if err := contextJSONRow(ctx, tx, &a, `SELECT data FROM factory_assignments WHERE id=$1 FOR SHARE`, cap.AssignmentID); err != nil {
		return a, r, err
	}
	if a.ID != cap.AssignmentID || a.Subject.Delivery == nil || a.Phase != project.ExecutionVerify || a.Role != project.RoleReviewer || a.Issue != 0 || a.Run != cap.RunID || a.ProjectID != cap.Project || a.Repository != cap.Repository || a.Subject.SubjectKey(a.Repository) != cap.Subject || a.SourceCommit != cap.SourceDigest || strconv.FormatInt(a.NativeRev, 10) != cap.AcceptedRevision {
		return a, r, errors.New("verification scope differs from the admitted worker")
	}
	r, _, err := applicationVerificationPrerequisitesTx(ctx, tx, a)
	return a, r, err
}

// The complete observation lives on its attributed post. This appends a report
// only; F10 and terminal results have other owners.
func recordVerificationJourneyPostTx(ctx context.Context, tx *sql.Tx, cap factory.MCPCapability, sub factory.MCPSubmission) (string, error) {
	o, err := factory.DecodeVerificationJourney(sub.Payload)
	if err != nil {
		return "", err
	}
	a, r, err := verificationJourneyScopeTx(ctx, tx, cap)
	if err != nil {
		return "", err
	}
	if err := o.ValidateFor(cap, a, factory.ResolvedScope(r)); err != nil {
		return "", err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM factory_feed_posts WHERE data->>'assignment'=$1 AND data->'communication'->>'kind'='journey'`, a.ID).Scan(&count); err != nil {
		return "", err
	}
	if count >= factory.MaxVerificationJourneys {
		return "", errors.New("journey observation limit reached; finish remains available")
	}
	content := factory.FeedCommunication{Kind: "journey", Title: o.Name, Summary: "Recorded journey observation: " + o.Result + ". Application verification is unchanged.", Scope: o.DeliveryID, Before: o.Expected, After: o.Observed, EvidenceRefs: append([]string(nil), o.EvidenceRefs...), Remaining: append([]string(nil), o.Limitations...)}
	for i, step := range o.Steps {
		content.Items = append(content.Items, factory.FeedCommunicationItem{Label: fmt.Sprintf("%d. %s", i+1, step), State: "reported"})
	}
	postID := factory.MCPInputDigest([]byte(a.ID + "\n" + sub.SubmissionID))[:32]
	content.EvidenceRefs = append(content.EvidenceRefs, o.SourceCommit, "journey:"+postID)
	for _, id := range o.ArtifactIDs {
		var owned bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_feed_artifacts WHERE id=$1 AND repository=$2 AND assignment=$3 AND complete)`, id, cap.Repository, a.ID).Scan(&owned); err != nil {
			return "", err
		}
		if !owned {
			return "", errors.New("journey artifact is not a complete artifact of this assignment")
		}
		content.Artifacts = append(content.Artifacts, factory.FeedArtifact{ID: id})
	}
	if err := resolveCommunicationArtifactsTx(ctx, tx, cap.Repository, &content); err != nil {
		return "", err
	}
	return recordWorkerPostTx(ctx, tx, cap, sub, content)
}

// Validate new references only after immutable submission/terminal replay has
// returned. A moved head cannot invalidate recovery of an admitted finish.
func recordMCPVerificationJourneyReferencesTx(ctx context.Context, tx *sql.Tx, cap factory.MCPCapability, sub factory.MCPSubmission) error {
	if sub.Tool != factory.MCPToolFinishAppVerify {
		return nil
	}
	var references struct {
		EvidenceRefs []string `json:"evidence_refs"`
	}
	if err := json.Unmarshal(sub.Payload, &references); err != nil {
		return err
	}
	ids, err := factory.VerificationJourneyReferences(references.EvidenceRefs)
	if err != nil || len(ids) == 0 {
		return err
	}
	var report factory.AppVerificationReport
	if err := json.Unmarshal(sub.Payload, &report); err != nil {
		return err
	}
	if err := report.Validate(); err != nil {
		return err
	}
	a, _, err := verificationJourneyScopeTx(ctx, tx, cap)
	if err != nil {
		return err
	}
	return validateVerificationJourneyRefsTx(ctx, tx, a, report)
}

// Final adoption rechecks the same immutable observations after native
// retirement. The original capability may now be retired; its exact scope
// remains evidence, never renewed authority or a new observation admission.
func validateVerificationJourneyRefsTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, report factory.AppVerificationReport) error {
	ids, err := factory.VerificationJourneyReferences(report.EvidenceRefs)
	if err != nil || len(ids) == 0 {
		return err
	}
	if a.Subject.Delivery == nil || report.DeliveryID != a.Subject.Delivery.DeliveryID {
		return errors.New("journey references lack their exact delivery report")
	}
	var cap factory.MCPCapability
	if err := contextJSONRow(ctx, tx, &cap, `SELECT data FROM factory_mcp_capabilities WHERE assignment=$1 AND run=$2 FOR SHARE`, a.ID, a.Run); err != nil {
		return err
	}
	if cap.Validate() != nil || cap.ScopeDigest() != report.InputDigest {
		return errors.New("journey references differ from the report input scope")
	}
	var revision factory.IntentRevision
	if err := contextJSONRow(ctx, tx, &revision, `SELECT data FROM factory_intent_revisions WHERE id=$1 FOR SHARE`, a.Subject.Delivery.AcceptedRevision); err != nil {
		return err
	}
	if revision.Validate() != nil || revision.Intent != a.Subject.Delivery.IntentID || revision.Repository != a.Repository || revision.Digest() != a.Subject.Delivery.ScopeDigest {
		return errors.New("journey references differ from their accepted outcome scope")
	}
	for _, id := range ids {
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT data->'journey' FROM factory_feed_posts WHERE id=$1 AND repository=$2 AND data->>'assignment'=$3 AND data->>'run'=$4 AND data->'communication'->>'kind'='journey' FOR SHARE`, id, a.Repository, a.ID, a.Run).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("journey reference is not a recorded observation of this assignment")
			}
			return err
		}
		var o factory.VerificationJourneyResult
		if json.Unmarshal(raw, &o) != nil || o.ValidateFor(cap, a, factory.ResolvedScope(revision)) != nil {
			return errors.New("journey reference differs from its canonical observation")
		}
		if report.Outcome == factory.AppVerificationVerified && o.Result != "passed" {
			return errors.New("verified report cites a failed or incomplete journey")
		}
	}
	return nil
}

// VerificationScope reads retained observations in canonical feed sequence.
// Keyset pages do not run models, append records, or change workflow state.
func (s *Store) VerificationScope(ctx context.Context, cap factory.MCPCapability, after int64) (factory.VerificationScopeView, error) {
	var out factory.VerificationScopeView
	if after < 0 {
		return out, errors.New("invalid journey observation cursor")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	a, r, err := verificationJourneyScopeTx(ctx, tx, cap)
	if err != nil {
		return out, err
	}
	out = factory.VerificationScopeView{Assignment: a.ID, Run: a.Run, InputDigest: cap.ScopeDigest(), Delivery: *a.Subject.Delivery, OutcomeIDs: append([]string(nil), factory.ResolvedScope(r)...), Records: []factory.VerificationJourneyRecord{}, Limit: factory.VerificationJourneyPageSize, Maximum: factory.MaxVerificationJourneys}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM factory_feed_posts WHERE repository=$1 AND data->>'assignment'=$2 AND data->>'run'=$3 AND data->'communication'->>'kind'='journey'`, a.Repository, a.ID, a.Run).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,seq,data->'journey' FROM factory_feed_posts WHERE repository=$1 AND data->>'assignment'=$2 AND data->>'run'=$3 AND data->'communication'->>'kind'='journey' AND seq>$4 ORDER BY seq LIMIT $5`, a.Repository, a.ID, a.Run, after, factory.VerificationJourneyPageSize+1)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var record factory.VerificationJourneyRecord
		var raw []byte
		if err := rows.Scan(&record.ID, &record.Sequence, &raw); err != nil {
			rows.Close()
			return out, err
		}
		if len(out.Records) == out.Limit {
			out.NextAfter = out.Records[len(out.Records)-1].Sequence
			break
		}
		if json.Unmarshal(raw, &record.Observation) != nil || record.Observation.ValidateFor(cap, a, factory.ResolvedScope(r)) != nil {
			rows.Close()
			return out, errors.New("recorded journey differs from its admitted scope")
		}
		out.Records = append(out.Records, record)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.Complete = out.NextAfter == 0
	if err := tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}
