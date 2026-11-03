package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// journeyReassignmentLimit bounds one per-issue reassessment listing; the
// caller reads one extra record past its render cap as the truncation
// signal, so a bounded read never reports a cut chain as whole.
const journeyReassignmentLimit = 256

// journeyPhaseGroupLimit bounds one selected-root latest-per-phase read:
// one newest row per distinct recorded phase value plus one extra group
// as the truncation signal. Admitted execution phases are a small frozen
// vocabulary, so a cut here only ever covers pathological junk phase
// values; the caller still names the cut instead of passing a partial
// group set as the whole lineage.
const journeyPhaseGroupLimit = 32

// PhaseAttempt carries one phase group's newest recorded assignment
// behind the selected owner AttemptRoot, with its stable seq order.
// The read layer classifies the recorded phase value (canonical or
// unknown); the store only scopes and orders.
type PhaseAttempt struct {
	Assignment factory.Assignment
	Seq        int64
}

// LatestPhaseAttempts returns the newest recorded assignment per
// distinct recorded phase value for one repository issue under the
// selected owner AttemptRoot and acceptance, newest first per group by
// stable seq order. One current state per phase means an old capped
// prefix can never stand in for a newer failed or current row, and a
// sibling AttemptRoot sharing the acceptance cannot leak in. The
// truncated flag reports whether more phase groups name the scope than
// the limit carries, so the caller marks its read incomplete instead
// of rendering a cut group set as the whole lineage. Rows that decode
// or scope-check refuse as storage errors: a corrupt recorded row is
// an explicit failure, never a skipped candidate.
func (s *Store) LatestPhaseAttempts(ctx context.Context, repository, issue int64, acceptance, attemptRoot string) ([]PhaseAttempt, bool, error) {
	if repository <= 0 || issue <= 0 || acceptance == "" || attemptRoot == "" {
		return nil, false, errors.New("invalid latest phase scope")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT ON ((data->>'phase')) seq, data FROM factory_assignments
		WHERE repository=$1 AND issue=$2 AND data->>'acceptance'=$3 AND data->>'attempt_root'=$4
		ORDER BY (data->>'phase'), seq DESC LIMIT $5`,
		repository, issue, acceptance, attemptRoot, journeyPhaseGroupLimit+1)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	groups := make([]PhaseAttempt, 0, journeyPhaseGroupLimit+1)
	for rows.Next() {
		var group PhaseAttempt
		var raw []byte
		if err = rows.Scan(&group.Seq, &raw); err != nil {
			return nil, false, err
		}
		if err = json.Unmarshal(raw, &group.Assignment); err != nil {
			return nil, false, err
		}
		a := group.Assignment
		if a.Repository != repository || a.Issue != issue || a.Acceptance != acceptance || a.AttemptRoot != attemptRoot {
			return nil, false, errors.New("latest phase scope mismatch")
		}
		groups = append(groups, group)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	if len(groups) > journeyPhaseGroupLimit {
		return groups[:journeyPhaseGroupLimit], true, nil
	}
	return groups, false, nil
}

// JourneyReassignmentCycles returns up to limit reassessment cycles for one
// issue, including released and superseded decision history. Unresolved
// cycles take precedence over recent history at the bound. The result has
// stable generation/terminal-state/identity order, with current cycles last
// within each generation; explicit cycle lineage remains authoritative. The
// truncated flag reports whether more cycles name the issue than the
// limit carries, so the caller marks its read incomplete instead of
// rendering a cut set as the whole lineage. Rows that decode or validate
// refuse as storage errors: a corrupt recorded row is an explicit
// failure, never a skipped candidate.
func (s *Store) JourneyReassignmentCycles(ctx context.Context, repository, issue int64, limit int) ([]ReassessmentRecord, bool, error) {
	if repository <= 0 || issue <= 0 || limit <= 0 || limit > journeyReassignmentLimit {
		return nil, false, errors.New("invalid journey reassessment listing")
	}
	// The issue lives inside the recorded JSON (factory_reassessments
	// carries no issue column): the digit guard keeps unparseable rows
	// out of the cast without failing the scoped read, and decoded
	// records re-assert their scope below.
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM factory_reassessments
		WHERE repository=$1
		AND data->>'issue' ~ '^[0-9]+$' AND (data->>'issue')::bigint=$2
		ORDER BY (state NOT IN ('released','superseded')) DESC, generation DESC, cycle DESC LIMIT $3`, repository, issue, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	records := make([]ReassessmentRecord, 0, limit+1)
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, false, err
		}
		var record ReassessmentRecord
		if err = json.Unmarshal(raw, &record); err != nil {
			return nil, false, err
		}
		if err = record.Validate(); err != nil {
			return nil, false, err
		}
		if record.Issue != issue {
			return nil, false, errors.New("journey reassessment scope mismatch")
		}
		records = append(records, record)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	truncated := len(records) > limit
	if truncated {
		records = records[:limit]
	}
	sort.Slice(records, func(i, j int) bool {
		a, b := records[i].Cycle, records[j].Cycle
		if a.Generation != b.Generation {
			return a.Generation < b.Generation
		}
		aClosed := a.State == factory.ReassessmentReleased || a.State == factory.ReassessmentSuperseded
		bClosed := b.State == factory.ReassessmentReleased || b.State == factory.ReassessmentSuperseded
		if aClosed != bClosed {
			return aClosed
		}
		return a.CycleID < b.CycleID
	})
	return records, truncated, nil
}

// journeyAdoptionLimit bounds one per-issue adoption listing; the
// caller reads one extra record past its render cap as the truncation
// signal, mirroring the reassessment listing.
const journeyAdoptionLimit = 256

// JourneyAdoption is one recorded intent adoption behind an issue: the
// intent and revision whose generated task binding adopted the native
// issue, with the binding itself. A binding exists only after native
// readback confirmed the issue, so its presence is the adoption proof;
// its absence means no intent ever adopted this issue.
type JourneyAdoption struct {
	Intent   string
	Revision string
	Binding  factory.IntentTaskBinding
}

// JourneyAdoptions returns up to limit recorded adoptions naming one
// repository issue, oldest binding first. The join is canonical:
// factory_intent_tasks bindings by issue index, joined to their intent
// revision, whose recorded repository must match the journey scope.
// The truncated flag mirrors the other journey readers. Rows that
// decode, validate or scope-check refuse as storage errors.
func (s *Store) JourneyAdoptions(ctx context.Context, repository, issue int64, limit int) ([]JourneyAdoption, bool, error) {
	if repository <= 0 || issue <= 0 || limit <= 0 || limit > journeyAdoptionLimit {
		return nil, false, errors.New("invalid journey adoption listing")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.data, t.data FROM factory_intent_tasks t
		JOIN factory_intent_revisions r ON r.id=t.revision
		WHERE t.data->>'issue_index' ~ '^[0-9]+$' AND (t.data->>'issue_index')::bigint=$2
		AND r.data->>'repository' ~ '^-?[0-9]+$' AND (r.data->>'repository')::bigint=$1
		ORDER BY t.revision, t.task LIMIT $3`, repository, issue, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	adoptions := make([]JourneyAdoption, 0, limit+1)
	for rows.Next() {
		var revisionRaw, bindingRaw []byte
		if err = rows.Scan(&revisionRaw, &bindingRaw); err != nil {
			return nil, false, err
		}
		var revision factory.IntentRevision
		if err = json.Unmarshal(revisionRaw, &revision); err != nil {
			return nil, false, err
		}
		if err = revision.Validate(); err != nil {
			return nil, false, err
		}
		var binding factory.IntentTaskBinding
		if err = json.Unmarshal(bindingRaw, &binding); err != nil {
			return nil, false, err
		}
		if revision.Repository != repository || binding.RevisionID != revision.ID ||
			binding.IssueIndex != issue || binding.IssueID <= 0 {
			return nil, false, errors.New("journey adoption scope mismatch")
		}
		adoptions = append(adoptions, JourneyAdoption{
			Intent: revision.Intent, Revision: revision.ID, Binding: binding,
		})
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	if len(adoptions) > limit {
		return adoptions[:limit], true, nil
	}
	return adoptions, false, nil
}

// JourneyDeliveredCompletion returns the latest canonical completion
// event behind one adopted native binding: the exact delivered-task
// evidence for the adopted task. Completions are immutable history, so
// the newest generation is the delivery proof; older generations stay
// out. No recorded completion is ErrNotFound, never an empty event.
func (s *Store) JourneyDeliveredCompletion(ctx context.Context, repository, issue int64, acceptance string) (factory.CompletionEvent, error) {
	var event factory.CompletionEvent
	if repository <= 0 || issue <= 0 || !project.ValidDecisionID(acceptance) {
		return event, errors.New("invalid delivered completion scope")
	}
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_completions
		WHERE repository=$1 AND data->>'issue'=$2 AND data->>'acceptance_id'=$3
		ORDER BY generation DESC LIMIT 1`, repository, strconv.FormatInt(issue, 10), acceptance).Scan(&raw)
	if err != nil {
		return event, err
	}
	if err = json.Unmarshal(raw, &event); err != nil {
		return event, err
	}
	if err = event.Validate(); err != nil {
		return event, err
	}
	if event.Repository != repository || event.Issue != issue || event.AcceptanceID != acceptance {
		return event, errors.New("delivered completion scope mismatch")
	}
	return event, nil
}

// JourneyVerifiedApp names the verified application revision behind one
// adopted intent revision: the latest finished verify assignment whose
// delivery subject binds this intent revision, with the source commit
// and verification manifest digest from its recorded F10 verified
// evidence. Only the trusted application check renders here; worker
// journey observations and receipts alone never do. No recorded
// verification is ErrNotFound, never an unverified claim.
type JourneyVerifiedApp struct {
	Intent       string
	Revision     string
	SourceCommit string
	Manifest     string
}

// JourneyVerifiedApp returns the verified application evidence for one
// adopted intent revision. The read is spend-free and write-free: it
// joins the recorded verify assignment to its recorded F10 feed event
// under the same canonical identity, outcome and scope the delivery
// admission owns (finished verify, succeeded outcome, trusted
// verification outcome verified, exact intent, revision and scope
// digest, with the F10 event bound to both the delivery and the
// assignment). The observed manifest must equal the admitted
// VerificationManifestDigest, matching the delivery admission and the
// journey observation contract. A worker finish, another revision, a
// foreign scope, a foreign manifest, a non-verified result or an F10
// event naming the wrong assignment never qualifies. Rows that decode,
// validate or scope-check refuse as storage errors; a missing
// assignment or event is ErrNotFound.
func (s *Store) JourneyVerifiedApp(ctx context.Context, repository int64, intent, revision string) (JourneyVerifiedApp, error) {
	var out JourneyVerifiedApp
	if repository <= 0 || !project.ValidDecisionID(intent) || !project.ValidDecisionID(revision) {
		return out, errors.New("invalid verified application scope")
	}
	var revisionRaw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_intent_revisions WHERE id=$1`, revision).Scan(&revisionRaw); err != nil {
		return out, err
	}
	var adopted factory.IntentRevision
	if err := json.Unmarshal(revisionRaw, &adopted); err != nil {
		return out, err
	}
	if adopted.ID != revision || adopted.Intent != intent || adopted.Repository != repository {
		return out, errors.New("verified application scope mismatch")
	}
	scope := adopted.Digest()
	var raw, feed []byte
	err := s.db.QueryRowContext(ctx, `SELECT a.data, p.data FROM factory_assignments a
		JOIN factory_feed_posts p ON p.repository=a.repository
		AND p.data->>'event_identity'=a.data->'subject'->'delivery'->>'delivery_id'
		AND p.data->>'assignment'=a.id AND p.data->'communication'->>'component_id'='F10'
		WHERE a.repository=$1 AND a.stage='finished' AND a.data->>'phase'='verify'
		AND a.data->>'outcome'='succeeded'
		AND a.data->'result'->'phase_result'->'output'->'verification'->>'outcome'='verified'
		AND a.data->'subject'->'delivery'->>'intent_id'=$2
		AND a.data->'subject'->'delivery'->>'accepted_revision'=$3
		AND a.data->'subject'->'delivery'->>'scope_digest'=$4
		ORDER BY a.seq DESC LIMIT 1`, repository, intent, revision, scope).Scan(&raw, &feed)
	if err != nil {
		return out, err
	}
	var assignment factory.Assignment
	if err = json.Unmarshal(raw, &assignment); err != nil {
		return out, err
	}
	subject := assignment.Subject.Delivery
	if subject == nil || subject.Validate() != nil ||
		subject.IntentID != intent || subject.AcceptedRevision != revision ||
		subject.ScopeDigest != scope || assignment.Repository != repository {
		return out, errors.New("verified application scope mismatch")
	}
	var event struct {
		Communication struct {
			EvidenceRefs []string `json:"evidence_refs"`
		} `json:"communication"`
	}
	if err = json.Unmarshal(feed, &event); err != nil {
		return out, err
	}
	refs := event.Communication.EvidenceRefs
	if len(refs) < 2 || refs[0] != subject.SourceCommit ||
		!factory.ValidCommit(refs[0]) || !factory.ValidDigest(refs[1]) ||
		refs[1] != subject.VerificationManifestDigest {
		return out, errors.New("verified application evidence mismatch")
	}
	return JourneyVerifiedApp{
		Intent: intent, Revision: revision,
		SourceCommit: refs[0], Manifest: refs[1],
	}, nil
}

// journeyDeliveryLimit bounds one per-publication delivery receipt
// listing; the extra record past the cap is the truncation signal.
const journeyDeliveryLimit = 64

// DeliveryJudgmentsForPublication returns up to limit recorded funded
// delivery receipts whose fingerprinted input names one publication
// and attempt root. Delivery acceptances carry the funded call
// identity rather than the evidence fingerprint, so the receipt join
// is over the recorded input scope (publication, attempt root); the
// caller binds the exact round and tuple. Every returned receipt
// revalidates, which recomputes its evidence fingerprint. The
// truncated flag mirrors the other journey readers.
func (s *Store) DeliveryJudgmentsForPublication(ctx context.Context, publicationID, attemptRoot string, limit int) ([]factory.DeliveryReceipt, bool, error) {
	if !factory.ValidID(publicationID) || !factory.ValidID(attemptRoot) || limit <= 0 || limit > journeyDeliveryLimit {
		return nil, false, errors.New("invalid delivery judgment listing")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM factory_priority_judgments
		WHERE data IS NOT NULL AND data->>'kind'=$3
		AND data->'input'->>'publication_id'=$1 AND data->'input'->>'attempt_root'=$2
		ORDER BY snapshot LIMIT $4`, publicationID, attemptRoot, factory.DeliveryReceiptKind, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	receipts := make([]factory.DeliveryReceipt, 0, limit+1)
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, false, err
		}
		var receipt factory.DeliveryReceipt
		if err = json.Unmarshal(raw, &receipt); err != nil {
			return nil, false, err
		}
		if receipt.Input.PublicationID != publicationID || receipt.Input.AttemptRoot != attemptRoot {
			return nil, false, errors.New("delivery judgment scope mismatch")
		}
		receipts = append(receipts, receipt)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	if len(receipts) > limit {
		return receipts[:limit], true, nil
	}
	return receipts, false, nil
}

// LatestIssueAttempt returns the newest recorded assignment for one
// repository issue, whatever its ownership. The newest row may be a
// child correction or review attempt: callers keep its AttemptRoot as
// the lineage root instead of implicitly re-rooting the journey on the
// child.
func (s *Store) LatestIssueAttempt(ctx context.Context, repository, issue int64) (factory.Assignment, error) {
	var a factory.Assignment
	if repository <= 0 || issue <= 0 {
		return a, errors.New("invalid latest attempt scope")
	}
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_assignments
		WHERE repository=$1 AND issue=$2 ORDER BY seq DESC LIMIT 1`, repository, issue).Scan(&data)
	if err == nil {
		err = json.Unmarshal(data, &a)
	}
	return a, err
}
