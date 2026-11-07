package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

// VerifiedApplicationOutcomeCount is the spend-free overview read of the same
// controller-owned F10 fact used by delivery admission. A worker finish or a
// historical accepted revision never supplies current outcome evidence.
func (s *Store) VerifiedApplicationOutcomeCount(ctx context.Context, revision factory.IntentRevision) (int, error) {
	if revision.State != "accepted" || revision.Repository <= 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var head string
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM factory_intent_heads WHERE id=$1 FOR SHARE`, revision.Intent).Scan(&head); err != nil {
		return 0, err
	}
	if head != revision.ID {
		return 0, nil
	}
	verified, err := verifiedAppRevisionTx(ctx, tx, revision)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if verified {
		return len(factory.ResolvedScope(revision)), nil
	}
	return 0, nil
}

// PendingAppVerifications includes finished Test Pilot reports until a trusted
// whole-app completion is recorded. Worker receipts alone never remove them.
// The exclusive sequence cursor lets the controller revisit stale reports
// without keeping later adoptable reports behind the same waiting prefix.
func (s *Store) PendingAppVerifications(ctx context.Context, after int64, limit int) ([]factory.Assignment, int64, error) {
	if limit < 1 || limit > 64 || after < 0 {
		return nil, after, errors.New("invalid verification page")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.seq,a.data FROM factory_assignments a WHERE a.stage='finished' AND a.seq>$2
		AND a.data->>'phase'='verify' AND a.data->>'outcome'='succeeded'
		AND a.data->'result'->'phase_result'->'output'->'verification'->>'outcome'='verified'
		AND NOT EXISTS(SELECT 1 FROM factory_feed_posts p WHERE p.repository=a.repository AND p.data->>'event_identity'=a.data->'subject'->'delivery'->>'delivery_id' AND p.data->'communication'->>'component_id'='F10')
		ORDER BY a.seq LIMIT $1`, limit, after)
	if err != nil {
		return nil, after, err
	}
	defer rows.Close()
	var assignments []factory.Assignment
	var next int64
	for rows.Next() {
		var raw []byte
		var a factory.Assignment
		if err = rows.Scan(&next, &raw); err != nil {
			return nil, after, err
		}
		if err = json.Unmarshal(raw, &a); err != nil {
			return nil, after, err
		}
		assignments = append(assignments, a)
	}
	return assignments, next, rows.Err()
}

func applicationVerificationInputsTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) (factory.IntentRevision, int64, error) {
	revision, tasks, err := applicationVerificationPrerequisitesTx(ctx, tx, a)
	if err != nil {
		return revision, 0, err
	}
	return revision, tasks[0].IssueIndex, nil
}

func applicationVerificationPrerequisitesTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) (factory.IntentRevision, []factory.VerificationDeliveredTask, error) {
	var revision factory.IntentRevision
	if a.Phase != project.ExecutionVerify || a.Subject.Delivery == nil || a.Subject.Delivery.Validate() != nil || a.SourceCommit != a.Subject.Delivery.SourceCommit {
		return revision, nil, errors.New("verification lacks its exact delivery subject")
	}
	subject := a.Subject.Delivery
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT r.data FROM factory_intent_heads h JOIN factory_intent_revisions r ON r.id=h.revision WHERE h.id=$1 AND h.revision=$2 FOR SHARE OF h`, subject.IntentID, subject.AcceptedRevision).Scan(&raw); err != nil {
		return revision, nil, err
	}
	if err := json.Unmarshal(raw, &revision); err != nil {
		return revision, nil, err
	}
	if revision.Repository != a.Repository || revision.State != "accepted" || revision.Validate() != nil || revision.Digest() != subject.ScopeDigest {
		return revision, nil, errors.New("accepted application scope changed")
	}
	tasks, err := deliveredIntentTaskEvidenceTx(ctx, tx, revision)
	return revision, tasks, err
}

func deliveredIntentTasksTx(ctx context.Context, tx *sql.Tx, revision factory.IntentRevision) (int64, error) {
	tasks, err := deliveredIntentTaskEvidenceTx(ctx, tx, revision)
	if err != nil {
		return 0, err
	}
	return tasks[0].IssueIndex, nil
}

func deliveredIntentTaskEvidenceTx(ctx context.Context, tx *sql.Tx, revision factory.IntentRevision) ([]factory.VerificationDeliveredTask, error) {
	var raw []byte
	tasks := revision.Tasks
	err := tx.QueryRowContext(ctx, `SELECT data FROM factory_intent_generations WHERE revision=$1 FOR SHARE`, revision.ID).Scan(&raw)
	if err == nil {
		var generation factory.IntentGeneration
		if err := json.Unmarshal(raw, &generation); err != nil {
			return nil, err
		}
		if generation.State != "completed" || generation.RevisionID != revision.ID || generation.IntentID != revision.Intent || generation.IntentDigest != revision.Digest() {
			return nil, errors.New("accepted application generation is not complete and current")
		}
		if err := factory.AssessProposedTasks(revision, generation.SubmittedTasks, revision.MaxIssues); err != nil {
			return nil, err
		}
		// Native adoption binds the recorded assessed graph, whose task keys
		// can differ from the owner's accepted planning sketch.
		tasks = generation.SubmittedTasks
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if len(tasks) < 1 || len(tasks) > 64 {
		return nil, errors.New("accepted application has no bounded delivered work")
	}
	delivered := make([]factory.VerificationDeliveredTask, 0, len(tasks))
	for _, task := range tasks {
		var binding factory.IntentTaskBinding
		if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_intent_tasks WHERE revision=$1 AND task=$2`, revision.ID, task.Key).Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &binding); err != nil {
			return nil, err
		}
		if binding.RevisionID != revision.ID || binding.TaskKey != task.Key || binding.IssueID <= 0 || binding.IssueIndex <= 0 || !project.ValidDecisionID(binding.AcceptanceID) {
			return nil, errors.New("accepted application task lacks its exact native binding")
		}
		var event factory.CompletionEvent
		var eventID, publicationID, occurrence string
		var generation int64
		if err := tx.QueryRowContext(ctx, `SELECT event,publication,occurrence,generation,data FROM factory_completions WHERE repository=$1 AND data->>'issue'=$2 AND data->>'acceptance_id'=$3 ORDER BY generation DESC LIMIT 1`, revision.Repository, fmt.Sprint(binding.IssueIndex), binding.AcceptanceID).Scan(&eventID, &publicationID, &occurrence, &generation, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, err
		}
		if event.Validate() != nil || event.ID != eventID || event.PublicationID != publicationID || event.ClosingOccurrence != occurrence || event.Generation != generation || event.Repository != revision.Repository || event.Issue != binding.IssueIndex || event.AcceptanceID != binding.AcceptanceID {
			return nil, errors.New("accepted application task differs from its canonical completion")
		}
		delivered = append(delivered, factory.VerificationDeliveredTask{TaskKey: task.Key, OutcomeIDs: append([]string(nil), task.IdeaIDs...), IssueID: binding.IssueID, IssueIndex: binding.IssueIndex, AcceptanceID: binding.AcceptanceID, Completion: event})
	}
	return delivered, nil
}

// ApplicationVerificationEvidenceInputs uses the same current accepted head and
// canonical completion join as admission, without executing checks or writing.
func (s *Store) ApplicationVerificationEvidenceInputs(ctx context.Context, a factory.Assignment) (factory.IntentRevision, factory.VerificationDeliveryPrerequisites, error) {
	var prerequisites factory.VerificationDeliveryPrerequisites
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return factory.IntentRevision{}, prerequisites, err
	}
	defer tx.Rollback()
	revision, tasks, err := applicationVerificationPrerequisitesTx(ctx, tx, a)
	if err != nil {
		return revision, prerequisites, err
	}
	prerequisites = factory.VerificationDeliveryPrerequisites{IntentID: revision.Intent, AcceptedRevision: revision.ID, ScopeDigest: revision.Digest(), SourceCommit: a.SourceCommit, Tasks: tasks}
	return revision, prerequisites, tx.Commit()
}

func (s *Store) ApplicationVerificationInputs(ctx context.Context, a factory.Assignment) (factory.IntentRevision, int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return factory.IntentRevision{}, 0, err
	}
	defer tx.Rollback()
	revision, issue, err := applicationVerificationInputsTx(ctx, tx, a)
	if err != nil {
		return revision, issue, err
	}
	return revision, issue, tx.Commit()
}

// RecordVerifiedApplication is called only after independent native observation
// of the approved check. Recheck current authority and delivered scope inside
// the same transaction as the F10 event.
func (s *Store) RecordVerifiedApplication(ctx context.Context, a factory.Assignment, manifest factory.ApprovedAppCheck, state execution.PreparationState) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	revision, tasks, err := applicationVerificationPrerequisitesTx(ctx, tx, a)
	if err != nil {
		return err
	}
	var currentRaw []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1 AND stage='finished' FOR SHARE`, a.ID).Scan(&currentRaw); err != nil {
		return err
	}
	var current factory.Assignment
	if err = json.Unmarshal(currentRaw, &current); err != nil {
		return err
	}
	if current.Result == nil || a.Result == nil || factory.ResultDigest(*current.Result) != factory.ResultDigest(*a.Result) || current.Run != a.Run {
		return ErrStaleRevision
	}
	if current.Result.PhaseResult == nil || current.Result.PhaseResult.Output == nil || current.Result.PhaseResult.Output.Verification == nil {
		return errors.New("application verification has no bound coverage report")
	}
	if err := validateVerificationJourneyRefsTx(ctx, tx, current, *current.Result.PhaseResult.Output.Verification); err != nil {
		return err
	}
	// Rejoin the persisted worker claims under this transaction's current
	// accepted head. All contributors to each outcome must be named; valid ID
	// syntax, planning-sketch keys or a subset of delivered work is insufficient.
	coverage := current.Result.PhaseResult.Output.Verification.Coverage
	resolved := factory.ResolvedScope(revision)
	if len(coverage) != len(resolved) {
		return errors.New("application coverage differs from accepted outcomes")
	}
	authorized := make(map[string]bool, len(resolved))
	for _, id := range resolved {
		authorized[id] = true
	}
	covered := map[string]bool{}
	for _, entry := range coverage {
		if !authorized[entry.OutcomeID] {
			return errors.New("application coverage differs from accepted outcomes")
		}
		expectedTasks, expectedAcceptances := map[string]bool{}, map[string]bool{}
		for _, task := range tasks {
			for _, outcome := range task.OutcomeIDs {
				if outcome == entry.OutcomeID {
					expectedTasks[task.TaskKey] = true
					expectedAcceptances[task.AcceptanceID] = true
				}
			}
		}
		if entry.IntentID != revision.Intent || covered[entry.OutcomeID] || len(expectedTasks) == 0 || len(entry.TaskKeys) != len(expectedTasks) || len(entry.DeliveredAcceptanceIDs) != len(expectedAcceptances) {
			return errors.New("application coverage differs from canonical delivered tasks")
		}
		covered[entry.OutcomeID] = true
		for _, key := range entry.TaskKeys {
			if !expectedTasks[key] {
				return errors.New("application coverage names a foreign or repeated delivered task")
			}
			delete(expectedTasks, key)
		}
		for _, id := range entry.DeliveredAcceptanceIDs {
			if !expectedAcceptances[id] {
				return errors.New("application coverage names a foreign or repeated acceptance")
			}
			delete(expectedAcceptances, id)
		}
	}
	if err = tx.QueryRowContext(ctx, `SELECT p.data FROM project_preparations p JOIN project_requirement_heads r ON r.project_id=p.project_id AND r.decision=p.requirements JOIN project_approval_heads h ON h.project_id=p.project_id AND h.decision=p.approval WHERE p.id=$1 FOR SHARE OF p,r,h`, a.Preparation).Scan(&currentRaw); err != nil {
		return err
	}
	preparation, _, err := unmarshalCandidatePreparation(currentRaw)
	if err != nil {
		return err
	}
	if state.ValidateFor(preparation.Address()) != nil || !manifest.BindsIntent(revision) || manifest.Digest() != a.Subject.Delivery.VerificationManifestDigest || factory.DeliveryPreparationDigest(preparation.Preparation) != a.Subject.Delivery.PreparationDigest || state.ID != a.Preparation || state.Project != a.ProjectID || state.Role != project.RoleReviewer || state.SourceCommit != a.SourceCommit || state.SetupDigest != preparation.Preparation.SetupDigest || !state.Ready || state.Stopped || state.Phase != project.PrepareReady || state.CheckExit == nil || *state.CheckExit != 0 {
		return errors.New("trusted application check does not match the delivery")
	}
	var approval project.ApprovalDecision
	if err = tx.QueryRowContext(ctx, `SELECT data FROM project_approval_decisions WHERE id=$1`, preparation.Preparation.Approval.ID).Scan(&currentRaw); err != nil {
		return err
	}
	if err = json.Unmarshal(currentRaw, &approval); err != nil {
		return err
	}
	if !approval.AllowsPreparation() || approval.Requirement != preparation.Preparation.Requirements.ID {
		return errors.New("application check approval is not current and verified")
	}
	outputDigest, err := checkVerifiedManifestExecutionTx(ctx, tx, current)
	if err != nil {
		return err
	}
	if outputDigest == "" {
		outputDigest = factory.MCPInputDigest([]byte(state.Output))
	}
	items := make([]factory.FeedCommunicationItem, 0, len(manifest.Outcomes))
	for _, outcome := range manifest.Outcomes {
		label := outcome
		for _, idea := range revision.Ideas {
			if idea.ID == outcome {
				label = feedText(idea.Description)
				break
			}
		}
		items = append(items, factory.FeedCommunicationItem{Label: label, State: "passed"})
	}
	if len(items) > feedPayloadCap {
		items = items[:feedPayloadCap]
	}
	if err = recordFactoryFeedEventTx(ctx, tx, a.Repository, 0, a.ID, a.Subject.Delivery.DeliveryID, "application:verified", factory.FeedCommunication{
		ComponentID: "F10", Title: "Application outcomes verified", Summary: fmt.Sprintf("All %d accepted outcomes passed the approved application check at source %s.", len(manifest.Outcomes), a.SourceCommit[:12]), Items: items,
		EvidenceRefs: []string{a.SourceCommit, manifest.Digest(), outputDigest},
	}, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RecordAppVerificationWait(ctx context.Context, a factory.Assignment, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = recordFactoryFeedEventTx(ctx, tx, a.Repository, 0, a.ID, a.ID+":"+factory.MCPInputDigest([]byte(reason)), "verification:waiting", factory.FeedCommunication{ComponentID: "F09", Title: "Application verification needs evidence", Summary: feedText(reason), Severity: "warning"}, 0); err != nil {
		return err
	}
	return tx.Commit()
}
