package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// storePublicationLimit bounds one outstanding-publication listing.
const storePublicationLimit = 1024

// storePublishableLimit bounds one publishable-assignment listing.
const storePublishableLimit = 256

// ErrPublicationConflict rejects replacement of a recorded publication intent.
var ErrPublicationConflict = errors.New("publication immutable input changed")

// RecordPublication stores one assignment's fresh open publication. One
// assignment publishes at most once: a second record for the assignment
// refuses instead of publishing twice.
func (s *Store) RecordPublication(ctx context.Context, p factory.Publication) error {
	// External origin requires its separate explicit command admission. This
	// ordinary Builder-completion entrypoint can never manufacture it.
	if p.External!=nil { return ErrPublicationConflict }
	if err := p.Validate(); err != nil {
		return err
	}
	if p.Stage != factory.PublicationOpen || p.Outcome != "" || p.Reason != "" || p.FinishedUnix != 0 || p.Revision != 0 || p.Publish.Attempts != 0 || p.PRCreate.Attempts != 0 || p.WithdrawRequested {
		return errors.New("publication packet carries fresh open work")
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		p.AssignmentID, p.Repository, p.Issue, p.Run, p.Stage, p.Revision, string(data)); err != nil {
		return fmt.Errorf("publication record failed: %w", err)
	}
	return nil
}

// PublicationByAssignment returns one assignment's publication.
func (s *Store) PublicationByAssignment(ctx context.Context, assignmentID string) (factory.Publication, error) {
	var p factory.Publication
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE assignment=$1`, assignmentID).Scan(&data)
	if err == nil {
		err = json.Unmarshal(data, &p)
	}
	return p, err
}

func (s *Store) PublicationByID(ctx context.Context, id string) (factory.Publication, error) {
	var p factory.Publication
	if !factory.ValidID(id) {
		return p, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM factory_publications WHERE data->>'id'=$1 LIMIT 2`, id)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return p, err
		}
		if err = json.Unmarshal(raw, &p); err != nil {
			return p, err
		}
		count++
	}
	if err = rows.Err(); err != nil {
		return p, err
	}
	if count != 1 {
		return factory.Publication{}, ErrNotFound
	}
	return p, p.Validate()
}

// Resolve only the operation frozen in the canonical publication. A provider
// command or matching branch name cannot invent workflow ownership.
func (s *Store) PublicationOperation(ctx context.Context, id string) (factory.Publication, factory.PublicationOperation, error) {
	var empty factory.Publication
	var op factory.PublicationOperation
	if !factory.ValidPublicationOperationID(id) {
		return empty, op, ErrNotFound
	}
	filter, _ := json.Marshal(map[string]any{"corrections": []any{map[string]string{"operation_id": id}}})
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM factory_publications WHERE data->'publish'->>'operation_id'=$1 OR data->'pr_create'->>'operation_id'=$1 OR data @> $2::jsonb LIMIT 2`, id, string(filter))
	if err != nil {
		return empty, op, err
	}
	defer rows.Close()
	var result factory.Publication
	count := 0
	for rows.Next() {
		var raw []byte
		var p factory.Publication
		if err = rows.Scan(&raw); err != nil {
			return empty, op, err
		}
		if err = json.Unmarshal(raw, &p); err != nil {
			return empty, op, err
		}
		if err = p.Validate(); err != nil {
			return empty, op, err
		}
		for _, candidate := range append([]factory.PublicationOperation{p.Publish, p.PRCreate}, p.Corrections...) {
			if candidate.OperationID == id {
				result, op = p, candidate
				count++
			}
		}
	}
	if err = rows.Err(); err != nil {
		return empty, op, err
	}
	if count != 1 {
		return empty, op, ErrNotFound
	}
	return result, op, nil
}

// PublicationReviewOperation resolves only registered immutable review work.
// Native lookup and cancellation use this record even after authority withdrawal.
func (s *Store) PublicationReviewOperation(ctx context.Context, operation string) (factory.Publication, factory.ReviewOperation, error) {
	var empty factory.Publication
	if !factory.ValidPublicationOperationID(operation) {
		return empty, factory.ReviewOperation{}, ErrNotFound
	}
	filter, err := json.Marshal(map[string]any{"review_operations": []any{map[string]any{"work": map[string]string{"operation_id": operation}}}})
	if err != nil {
		return empty, factory.ReviewOperation{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM factory_publications WHERE data @> $1::jsonb LIMIT 2`, string(filter))
	if err != nil {
		return empty, factory.ReviewOperation{}, err
	}
	defer rows.Close()
	var result factory.Publication
	var found factory.ReviewOperation
	count := 0
	for rows.Next() {
		var raw []byte
		var p factory.Publication
		if err = rows.Scan(&raw); err != nil {
			return empty, found, err
		}
		if err = json.Unmarshal(raw, &p); err != nil {
			return empty, found, err
		}
		for _, op := range p.ReviewOperations {
			if op.Work.OperationID == operation {
				if err = op.ValidateForPublication(p); err != nil {
					return empty, found, err
				}
				result, found = p, op
				count++
			}
		}
	}
	if err = rows.Err(); err != nil {
		return empty, found, err
	}
	if count != 1 {
		return empty, factory.ReviewOperation{}, ErrNotFound
	}
	return result, found, nil
}

// UpdatePublication stores one publication's next revision exactly once.
// The caller increments Revision first; only the recorded predecessor
// advances, and anything else reports stale instead of overwriting a
// concurrent receipt.
func loadPreviousPublicationTx(ctx context.Context, tx *sql.Tx, p factory.Publication) (factory.Publication, error) {
	var previous factory.Publication
	var previousData []byte
	err := tx.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE assignment=$1`, p.AssignmentID).Scan(&previousData)
	if err == nil {
		err = json.Unmarshal(previousData, &previous)
	}
	if err != nil {
		return previous, err
	}
	if previous.Revision != p.Revision-1 {
		return previous, ErrStaleRevision
	}
	if err := publicationUpdateAllowed(previous, p); err != nil {
		return previous, err
	}
	return previous, nil
}

func publicationRegistration(previous, p factory.Publication) (bool, []int64, error) {
	registering := previous.Publish.Work == nil && p.Publish.Work != nil ||
		previous.PRCreate.Work == nil && p.PRCreate.Work != nil ||
		len(p.Corrections) > len(previous.Corrections) ||
		len(p.ResolutionOperations) > len(previous.ResolutionOperations) ||
		len(p.ReviewOperations) > len(previous.ReviewOperations)
	var newDeadlines []int64
	if previous.Publish.Work == nil && p.Publish.Work != nil {
		newDeadlines = append(newDeadlines, p.Publish.Work.NotAfter)
	}
	if previous.PRCreate.Work == nil && p.PRCreate.Work != nil {
		newDeadlines = append(newDeadlines, p.PRCreate.Work.NotAfter)
	}
	for i := len(previous.Corrections); i < len(p.Corrections); i++ {
		if p.Corrections[i].Work == nil {
			return false, nil, factory.ErrAttemptTimeExhausted
		}
		newDeadlines = append(newDeadlines, p.Corrections[i].Work.NotAfter)
	}
	for i := len(previous.ReviewOperations); i < len(p.ReviewOperations); i++ {
		newDeadlines = append(newDeadlines, p.ReviewOperations[i].Work.NotAfter)
	}
	for i := len(previous.ResolutionOperations); i < len(p.ResolutionOperations); i++ {
		newDeadlines = append(newDeadlines, p.ResolutionOperations[i].Work.NotAfter)
	}
	if registering && (previous.WithdrawRequested || p.WithdrawRequested) {
		return false, nil, ErrDispatchClosed
	}
	return registering, newDeadlines, nil
}

func admitPublicationRegistrationTx(ctx context.Context, tx *sql.Tx, previous, p factory.Publication, registering bool, newDeadlines []int64) error {
	if !registering {
		return nil
	}
	// Materialize the logical default-open gate so registration and
	// WithdrawDispatch serialize on the same row even before its first
	// withdrawal. A missing row otherwise has no lockable identity.
	if _, err := tx.ExecContext(ctx, `INSERT INTO factory_dispatch(repository,revision,open,data)
		VALUES($1,0,TRUE,'{}'::jsonb) ON CONFLICT(repository) DO NOTHING`, p.Repository); err != nil {
		return err
	}
	var open bool
	if err := tx.QueryRowContext(ctx, `SELECT open FROM factory_dispatch WHERE repository=$1 FOR UPDATE`, p.Repository).Scan(&open); err != nil {
		return err
	}
	if !open {
		return ErrDispatchClosed
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1`, p.AssignmentID).Scan(&raw); err != nil {
		return err
	}
	var owner factory.Assignment
	if err := json.Unmarshal(raw, &owner); err != nil {
		return err
	}
	initialSend := previous.Publish.Work == nil && p.Publish.Work != nil || previous.PRCreate.Work == nil && p.PRCreate.Work != nil
	if owner.RetainedPublication != nil && initialSend {
		if owner.Validate() != nil || owner.RetainedPublication.AdoptedFrom != "" || len(p.Corrections) != 0 || len(p.ReviewOperations) != 0 || len(p.ResolutionOperations) != 0 ||
			p.Run != owner.RetainedPublication.Run || p.Candidate != owner.RetainedPublication.Candidate {
			return ErrRetainedPublicationIneligible
		}
		for _, deadline := range newDeadlines {
			if deadline <= time.Now().Unix() || deadline > owner.RetainedPublication.NotAfter {
				return factory.ErrAttemptTimeExhausted
			}
		}
		return nil
	}
	if len(p.ReviewOperations) == len(previous.ReviewOperations)+1 {
		op := p.ReviewOperations[len(previous.ReviewOperations)]
		if op.RetryOf != "" {
			return admitReviewRetryRegistrationTx(ctx, tx, previous, p, op)
		}
	}
	return ensureAttemptDeadlineForRegistrationTx(ctx, tx, p.AssignmentID, newDeadlines...)
}

func (s *Store) UpdatePublication(ctx context.Context, p factory.Publication) error {
	if err := p.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = updatePublicationTx(ctx, tx, p); err != nil {
		return err
	}
	return tx.Commit()
}

func updatePublicationTx(ctx context.Context, tx *sql.Tx, p factory.Publication) error {
	if err := p.Validate(); err != nil {
		return err
	}
	previous, err := loadPreviousPublicationTx(ctx, tx, p)
	if err != nil {
		return err
	}
	registering, newDeadlines, err := publicationRegistration(previous, p)
	if err != nil {
		return err
	}
	if err = admitPublicationRegistrationTx(ctx, tx, previous, p, registering, newDeadlines); err != nil {
		return err
	}
	assignment, authority := p.AssignmentID, p.Authority
	if len(p.ReviewOperations) > len(previous.ReviewOperations) {
		// A later independent reviewer captures its own source and approved
		// setup. Restart may retire the Builder's standing setup; it must not
		// strand that review or silently reauthorize the old publication.
		if len(p.ReviewOperations) != len(previous.ReviewOperations)+1 || len(newDeadlines) != 1 {
			return ErrPublicationConflict
		}
		op := p.ReviewOperations[len(previous.ReviewOperations)]
		var child factory.Assignment
		if err = loadGrantDocumentTx(ctx, tx, &child, `SELECT a.data FROM factory_assignments a JOIN factory_runs r ON r.id=a.run
			WHERE a.run=$1 AND r.data->>'reconciled'='true' AND r.data->>'outcome'='succeeded'`, op.RunID); err != nil {
			return err
		}
		var root string
		if err = tx.QueryRowContext(ctx, `SELECT data->>'attempt_root' FROM factory_assignments WHERE id=$1`, p.AssignmentID).Scan(&root); err != nil {
			return err
		}
		if child.Validate() != nil || child.Role != project.RoleReviewer || child.Phase != project.ExecutionReview ||
			child.Stage != factory.AssignmentFinished || child.Outcome != factory.Succeeded ||
			child.Result == nil || !child.Result.Reported || child.Result.Status != "completed" ||
			child.AttemptRoot != root || child.PublicationAssignment != p.AssignmentID || child.ProjectID != p.ProjectID ||
			child.Repository != p.Repository || child.Issue != p.Issue || child.Acceptance != p.Acceptance || child.SourceCommit != op.Work.HeadOID {
			return ErrPublicationConflict
		}
		assignment, authority = child.ID, child.Authority
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	// Gate inspection and registration share one statement: a withdrawal
	// either sees this operation or closes the gate before it can register.
	// Grants are checked by their active flags, not their revisions, so a
	// saved number never strands registered work.
	result, err := tx.ExecContext(ctx, `UPDATE factory_publications SET stage=$1,revision=$2,data=$3
		WHERE assignment=$4 AND revision=$5
		AND (NOT $6 OR (
		 NOT EXISTS(SELECT 1 FROM factory_dispatch WHERE repository=$7 AND NOT open)
		 AND EXISTS(SELECT 1 FROM issue_acceptance_heads WHERE repository=$8 AND issue=$9 AND decision=$10)
		 AND NOT EXISTS(SELECT 1 FROM issue_acceptance_withdrawals WHERE repository=$11 AND issue=$12 AND decision=$13)
		 AND EXISTS(SELECT 1 FROM factory_policies WHERE repository=$7
		   AND COALESCE((data->>'enabled')::boolean,FALSE) AND NOT COALESCE((data->>'paused')::boolean,FALSE))
		 AND EXISTS(SELECT 1 FROM factory_operator_grants WHERE repository=$7 AND COALESCE((data->>'active')::boolean,FALSE))
		 AND EXISTS(SELECT 1 FROM project_environment_grants WHERE repository=$7 AND COALESCE((data->>'active')::boolean,FALSE))
		 AND EXISTS(SELECT 1 FROM factory_sponsorships s JOIN factory_assignments a
		   ON a.id=$14 AND s.connection=COALESCE(NULLIF(a.data->>'connection',''),a.data#>>'{retained_publication,connection}')
		   WHERE s.repository=$7 AND COALESCE((s.data->>'active')::boolean,FALSE))
		 AND EXISTS(SELECT 1 FROM project_requirement_heads WHERE project_id=$15 AND decision=$16)
		 AND EXISTS(SELECT 1 FROM project_approval_heads WHERE project_id=$15 AND decision=$17)
		))`,
		p.Stage, p.Revision, string(data), p.AssignmentID, p.Revision-1, registering,
		p.Repository, p.Repository, p.Issue, p.Acceptance, p.Repository, p.Issue, p.Acceptance,
		assignment, p.ProjectID, authority.RequirementsID, authority.ApprovalID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStaleRevision
	}
	if p.Stage == factory.PublicationFailed || p.Stage == factory.PublicationFenced || p.Stage == factory.PublicationWithdrawn {
		closeRoot := p.Reason != factory.PublishReasonSuperseded && p.Reason != factory.PublishReasonWithdrawn
		if err = transitionAttemptAssignmentTx(ctx, tx, p.AssignmentID, closeRoot, time.Now()); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if previous.PRNumber == 0 && p.PRNumber > 0 {
		if err = recordFactoryFeedEventTx(ctx, tx, p.Repository, p.Issue, p.AssignmentID, p.ID+":pr", "native_pr:created", factory.FeedCommunication{
			ComponentID: "F05", Title: fmt.Sprintf("Pull request #%d created", p.PRNumber), Summary: "The native repository confirmed the pull request for this candidate.",
			Items: []factory.FeedCommunicationItem{{Label: fmt.Sprintf("PR #%d", p.PRNumber), State: "created"}},
		}, p.ObservedUnix); err != nil {
			return err
		}
	}
	if previous.Stage != p.Stage && (p.Stage == factory.PublicationFailed || p.Stage == factory.PublicationFenced || p.Stage == factory.PublicationWithdrawn) {
		if err = recordFactoryFeedEventTx(ctx, tx, p.Repository, p.Issue, p.AssignmentID, fmt.Sprintf("%s:%d", p.ID, p.Revision), "publication:"+p.Stage, factory.FeedCommunication{
			ComponentID: "F09", Title: "Publication " + p.Stage, Summary: p.Reason, Severity: "warning",
			Items: []factory.FeedCommunicationItem{{Label: "Native publication", Detail: p.Reason, State: p.Stage}},
		}, p.FinishedUnix); err != nil {
			return err
		}
	}
	return nil
}

// publicationTupleTransitionAllowed pins the current-tuple lifecycle for
// publicationUpdateAllowed. BaseSHA above stays the immutable original
// admitted source base and is never compared to Tuple.Base here. A nil
// tuple stays nil for nondependent updates; initialization requires
// Revision 0 with a valid shape bound to the next candidate; a present
// tuple never drops to nil; an unchanged value (even a new pointer) stays
// allowed; any other change requires exactly old Revision+1 with a valid
// shape bound to the next candidate. Fresh native observation and tuple
// CAS initialization belong to serving callers; this guard alone is not
// native-authority proof.
func publicationTupleTransitionAllowed(old, next factory.Publication) error {
	oldTuple, nextTuple := old.Tuple, next.Tuple
	switch {
	case oldTuple == nil && nextTuple == nil:
		return nil
	case oldTuple == nil:
		if nextTuple.Revision != 0 {
			return ErrPublicationConflict
		}
	case nextTuple == nil:
		return ErrPublicationConflict
	case *oldTuple == *nextTuple:
		return nil
	case nextTuple.Revision != oldTuple.Revision+1:
		return ErrPublicationConflict
	}
	if err := nextTuple.Validate(); err != nil {
		return ErrPublicationConflict
	}
	if nextTuple.Head != next.Candidate {
		return ErrPublicationConflict
	}
	return nil
}

func publicationIdentityStable(old, next factory.Publication) bool {
	return old.ID == next.ID && old.AssignmentID == next.AssignmentID && old.ProjectID == next.ProjectID &&
		old.Role == next.Role && old.Acceptance == next.Acceptance && old.Preparation == next.Preparation &&
		old.BaseSHA == next.BaseSHA &&
		old.TargetBranch == next.TargetBranch && old.Repository == next.Repository && old.Issue == next.Issue &&
		old.CreatedUnix == next.CreatedUnix && old.Authority == next.Authority && (!old.WithdrawRequested || next.WithdrawRequested)
}

func publicationRunAdvanced(old, next factory.Publication) bool {
	// The run and head advance only along a recorded correction chain;
	// Publication.Validate pins the exact chain and head.
	if (old.Run != next.Run || old.Candidate != next.Candidate) && len(next.Corrections) == 0 {
		return false
	}
	if old.Run == next.Run {
		return true
	}
	committedRun := ""
	for _, op := range next.Corrections {
		if op.Effect == factory.OpEffectCommitted {
			committedRun = op.RunID
		}
	}
	return next.Run == committedRun
}

func publicationCorrectionsStable(old, next factory.Publication) bool {
	if len(next.Corrections) < len(old.Corrections) {
		return false
	}
	for i := range old.Corrections {
		if !publicationOperationUpdateAllowed(old.Corrections[i], next.Corrections[i]) {
			return false
		}
	}
	return true
}

func publicationReviewsStable(old, next factory.Publication) bool {
	if len(next.ResolutionOperations) < len(old.ResolutionOperations) {
		return false
	}
	for i, prior := range old.ResolutionOperations {
		updated := next.ResolutionOperations[i]
		if prior.AcceptanceID != updated.AcceptanceID || prior.FindingKey != updated.FindingKey || prior.ResolvedDigest != updated.ResolvedDigest || prior.Work != updated.Work || prior.Confirmed && !updated.Confirmed ||
			!reviewOperationUpdateAllowed(factory.ReviewOperation{Outcome: prior.Outcome}, factory.ReviewOperation{Outcome: updated.Outcome}) {
			return false
		}
	}
	if len(next.ReviewOperations) < len(old.ReviewOperations) {
		return false
	}
	for i := range old.ReviewOperations {
		if !reviewOperationUpdateAllowed(old.ReviewOperations[i], next.ReviewOperations[i]) {
			return false
		}
	}
	return true
}

func publicationJournalsStable(old, next factory.Publication) bool {
	// Review cycles and correction journals are immutable once recorded:
	// entries never change, and new entries only append. Publication
	// validation pins the cycle/disposition linkage for the appended tail.
	if len(next.ReviewCycles) < len(old.ReviewCycles) {
		return false
	}
	for i := range old.ReviewCycles {
		if next.ReviewCycles[i] != old.ReviewCycles[i] {
			return false
		}
	}
	if len(next.CorrectionJournals) < len(old.CorrectionJournals) {
		return false
	}
	for i := range old.CorrectionJournals {
		if !correctionJournalUpdateAllowed(old.CorrectionJournals[i], next.CorrectionJournals[i]) {
			return false
		}
	}
	return true
}

func publicationAcceptancesStable(old, next factory.Publication) bool {
	// Delivery acceptances are immutable once recorded: entries never
	// change, and new acceptances only append. FW35 journals clean_review
	// acceptance here; FW37 appends jev_acceptance through the same rule.
	if len(next.DeliveryAcceptances) < len(old.DeliveryAcceptances) {
		return false
	}
	for i := range old.DeliveryAcceptances {
		if !deliveryAcceptanceEqual(old.DeliveryAcceptances[i], next.DeliveryAcceptances[i]) {
			return false
		}
	}
	return true
}

func publicationStageAllowed(old, next factory.Publication) bool {
	switch {
	case old.Stage == factory.PublicationOpen || old.Stage == factory.PublicationFenced:
		return true
	case old.Stage == factory.PublicationPublished &&
		(next.Stage == factory.PublicationPublished || next.Stage == factory.PublicationFenced || old.External!=nil && next.Stage==factory.PublicationWithdrawn):
		return true
	default:
		return false
	}
}

func publicationUpdateAllowed(old, next factory.Publication) error {
	if !reflect.DeepEqual(old.External,next.External) { return ErrPublicationConflict }
	if !publicationIdentityStable(old, next) || !publicationRunAdvanced(old, next) {
		return ErrPublicationConflict
	}
	if err := publicationTupleTransitionAllowed(old, next); err != nil {
		return err
	}
	if !publicationCorrectionsStable(old, next) || !publicationReviewsStable(old, next) ||
		!publicationJournalsStable(old, next) || !publicationAcceptancesStable(old, next) ||
		!publicationStageAllowed(old, next) {
		return ErrPublicationConflict
	}
	if !publicationOperationUpdateAllowed(old.Publish, next.Publish) || !publicationOperationUpdateAllowed(old.PRCreate, next.PRCreate) {
		return ErrPublicationConflict
	}
	return nil
}

func publicationOperationUpdateAllowed(old, next factory.PublicationOperation) bool {
	if old.Kind != next.Kind || old.RunID != next.RunID {
		return false
	}
	if old.Work != nil && (next.Work == nil || *old.Work != *next.Work || old.OperationID != next.OperationID || old.Attempts != next.Attempts) {
		return false
	}
	if old.Receipt != "" && old.Receipt != next.Receipt {
		return false
	}
	if (old.Effect == factory.OpEffectCommitted || old.Effect == factory.OpEffectNotCommitted) && old.Effect != next.Effect {
		return false
	}
	return true
}

// correctionJournalUpdateAllowed reports whether one recorded correction
// journal entry survived unchanged. Journal entries are fully immutable:
// a new correction run appends a new entry rather than editing one.
// Findings compare by value: their optional native anchor is a pointer,
// so struct identity comparison would refuse legitimate read-modify-write
// cycles over identical decoded records.
func correctionJournalUpdateAllowed(old, next factory.CorrectionJournal) bool {
	if old.CycleID != next.CycleID || old.RunID != next.RunID {
		return false
	}
	if old.Report.SetDigest != next.Report.SetDigest || len(old.Report.Findings) != len(next.Report.Findings) || len(old.Report.Dispositions) != len(next.Report.Dispositions) {
		return false
	}
	for i := range old.Report.Findings {
		if !correctionFindingEqual(old.Report.Findings[i], next.Report.Findings[i]) {
			return false
		}
	}
	for i := range old.Report.Dispositions {
		if old.Report.Dispositions[i] != next.Report.Dispositions[i] {
			return false
		}
	}
	return true
}

func correctionFindingEqual(a, b factory.Finding) bool {
	if a.Key != b.Key || a.OriginRun != b.OriginRun || a.OriginReview != b.OriginReview ||
		a.Severity != b.Severity || a.Actionable != b.Actionable || a.Defect != b.Defect ||
		a.Evidence != b.Evidence || a.RequiredOutcome != b.RequiredOutcome || a.FixKind != b.FixKind {
		return false
	}
	if (a.Location == nil) != (b.Location == nil) {
		return false
	}
	if a.Location != nil && *a.Location != *b.Location {
		return false
	}
	return true
}

// deliveryAcceptanceEqual reports whether one recorded delivery
// acceptance survived unchanged. Every field compares by value; resolution
// intents compare element-wise, so a rewritten intent refuses like any
// other journal mutation.
func deliveryAcceptanceEqual(old, next factory.DeliveryAcceptance) bool {
	if old.Kind != next.Kind || old.ID != next.ID || old.PublicationID != next.PublicationID ||
		old.AttemptRoot != next.AttemptRoot || old.ScopeDigest != next.ScopeDigest ||
		old.FindingDigest != next.FindingDigest || old.CoverageDigest != next.CoverageDigest ||
		old.CheckDigest != next.CheckDigest || old.PolicyRevision != next.PolicyRevision ||
		old.DecisionID != next.DecisionID || old.OriginReceipt != next.OriginReceipt ||
		old.Round != next.Round || old.Tuple != next.Tuple {
		return false
	}
	if len(old.ResolutionIntents) != len(next.ResolutionIntents) {
		return false
	}
	for i := range old.ResolutionIntents {
		if old.ResolutionIntents[i] != next.ResolutionIntents[i] {
			return false
		}
	}
	return true
}

func reviewOperationIdentityStable(old, next factory.ReviewOperation) bool {
	if old.RunID != next.RunID || old.RetryOf != next.RetryOf || old.RetryCommand != next.RetryCommand || !factory.ReviewWorkEqual(old.Work, next.Work) {
		return false
	}
	prior, updated := old.Outcome, next.Outcome
	if prior.OperationID != "" && prior.OperationID != updated.OperationID ||
		prior.InstallationID != "" && prior.InstallationID != updated.InstallationID ||
		prior.Kind != "" && prior.Kind != updated.Kind || prior.ActorID != 0 && prior.ActorID != updated.ActorID ||
		prior.RepositoryID != 0 && prior.RepositoryID != updated.RepositoryID {
		return false
	}
	return len(prior.Receipt) == 0 || string(prior.Receipt) == string(updated.Receipt)
}

func reviewOperationOutcomeMonotonic(old, next factory.ReviewOperation) bool {
	prior, updated := old.Outcome, next.Outcome
	if (prior.Effect == factory.OpEffectCommitted || prior.Effect == factory.OpEffectNotCommitted) && prior.Effect != updated.Effect {
		return false
	}
	if prior.Cancellation == factory.OpCancelCancelled && updated.Cancellation != prior.Cancellation {
		return false
	}
	return prior.Completion != factory.OpCompletionComplete || updated.Completion == prior.Completion
}

func reviewOperationUpdateAllowed(old, next factory.ReviewOperation) bool {
	return reviewOperationIdentityStable(old, next) && reviewOperationOutcomeMonotonic(old, next)
}

// OutstandingPublications lists every open or fenced publication and each
// published record with an unresolved correction for one repository, oldest
// first. Recovery reconciles each one against native state; withdrawal
// cancels each one's outstanding operations.
func (s *Store) OutstandingPublications(ctx context.Context, repository int64, limit int) ([]factory.Publication, error) {
	if repository <= 0 {
		return nil, errors.New("invalid publication repository")
	}
	if limit <= 0 || limit > storePublicationLimit {
		return nil, errors.New("invalid publication listing limit")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM factory_publications
		WHERE repository=$1 AND (
			stage IN ('open','fenced') OR
			(stage='published' AND (data ? 'external' OR EXISTS (
				SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(data->'corrections')='array' THEN data->'corrections' ELSE '[]'::jsonb END) AS correction
				WHERE COALESCE(correction->>'effect','') IN ('','pending','indeterminate')
				   OR COALESCE(correction->>'cancellation','') IN ('pending','indeterminate')
				   OR (correction->>'effect'='committed' AND COALESCE(correction->>'completion','') <> 'complete')
			) OR EXISTS (
				SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(data->'review_operations')='array' THEN data->'review_operations' ELSE '[]'::jsonb END) AS review_op
				WHERE COALESCE(review_op->'outcome'->>'Effect','') IN ('','pending','indeterminate')
				   OR COALESCE(review_op->'outcome'->>'Cancellation','') IN ('pending','indeterminate')
				   OR (review_op->'outcome'->>'Effect'='committed' AND COALESCE(review_op->'outcome'->>'Completion','') <> 'complete')
			) OR EXISTS (
				SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(data->'resolution_operations')='array' THEN data->'resolution_operations' ELSE '[]'::jsonb END) AS resolve_op
				WHERE COALESCE(resolve_op->'outcome'->>'Effect','') IN ('','pending','indeterminate')
				   OR COALESCE(resolve_op->'outcome'->>'Cancellation','') IN ('pending','indeterminate')
				   OR (resolve_op->'outcome'->>'Effect'='committed' AND (COALESCE(resolve_op->'outcome'->>'Completion','') <> 'complete' OR COALESCE(resolve_op->>'confirmed','false') <> 'true'))
			)))
		) ORDER BY seq LIMIT $2`, repository, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []factory.Publication
	for rows.Next() {
		var data []byte
		var p factory.Publication
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// requestPublicationWithdrawalsTx durably latches every outstanding
// publication under the caller's repository gate lock. It refuses an
// oversized set rather than leaving later records uncancelled.
func requestPublicationWithdrawalsTx(ctx context.Context, tx *sql.Tx, repository int64) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT assignment,revision,COALESCE((data->>'withdraw_requested')::boolean,FALSE) FROM factory_publications
		WHERE repository=$1 AND (
			stage IN ('open','fenced') OR
			(stage='published' AND (data ? 'external' OR EXISTS (
				SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(data->'corrections')='array' THEN data->'corrections' ELSE '[]'::jsonb END) AS correction
				WHERE COALESCE(correction->>'effect','') IN ('','pending','indeterminate')
				   OR COALESCE(correction->>'cancellation','') IN ('pending','indeterminate')
				   OR (correction->>'effect'='committed' AND COALESCE(correction->>'completion','') <> 'complete')
			) OR EXISTS (
				SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(data->'review_operations')='array' THEN data->'review_operations' ELSE '[]'::jsonb END) AS review_op
				WHERE COALESCE(review_op->'outcome'->>'Effect','') IN ('','pending','indeterminate')
				   OR COALESCE(review_op->'outcome'->>'Cancellation','') IN ('pending','indeterminate')
				   OR (review_op->'outcome'->>'Effect'='committed' AND COALESCE(review_op->'outcome'->>'Completion','') <> 'complete')
			) OR EXISTS (
				SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(data->'resolution_operations')='array' THEN data->'resolution_operations' ELSE '[]'::jsonb END) AS resolve_op
				WHERE COALESCE(resolve_op->'outcome'->>'Effect','') IN ('','pending','indeterminate')
				   OR COALESCE(resolve_op->'outcome'->>'Cancellation','') IN ('pending','indeterminate')
				   OR (resolve_op->'outcome'->>'Effect'='committed' AND (COALESCE(resolve_op->'outcome'->>'Completion','') <> 'complete' OR COALESCE(resolve_op->>'confirmed','false') <> 'true'))
			)))
		) ORDER BY seq LIMIT $2 FOR UPDATE`, repository, storePublicationLimit+1)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	type publicationIntent struct {
		assignment string
		revision   int64
		requested  bool
	}
	var publications []publicationIntent
	for rows.Next() {
		var item publicationIntent
		if err = rows.Scan(&item.assignment, &item.revision, &item.requested); err != nil {
			return false, err
		}
		publications = append(publications, item)
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	if err = rows.Close(); err != nil {
		return false, err
	}
	if len(publications) > storePublicationLimit {
		return false, errors.New("too many outstanding publications to withdraw")
	}
	for _, p := range publications {
		if p.requested {
			continue
		}
		revision := p.revision + 1
		updated, updateErr := tx.ExecContext(ctx, `UPDATE factory_publications SET revision=$1,
			data=jsonb_set(jsonb_set(data,'{withdraw_requested}','true'::jsonb,true),'{revision}',to_jsonb($1::integer),true)
			WHERE assignment=$2 AND revision=$3`, revision, p.assignment, p.revision)
		if updateErr != nil {
			return false, updateErr
		}
		n, affectedErr := updated.RowsAffected()
		if affectedErr != nil {
			return false, affectedErr
		}
		if n != 1 {
			return false, ErrStaleRevision
		}
	}
	return len(publications) > 0, nil
}

// OpenPublications lists every open or fenced publication across
// repositories, oldest first, for pass recovery. Deferred work waits for
// a later pass; nothing here submits without reconciling first.
const pendingPublicationReviews = `EXISTS (
			SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(data->'review_operations')='array' THEN data->'review_operations' ELSE '[]'::jsonb END) AS review_op
			WHERE COALESCE(review_op->'outcome'->>'Effect','') IN ('','pending','indeterminate')
			   OR COALESCE(review_op->'outcome'->>'Cancellation','') IN ('pending','indeterminate')
			   OR (review_op->'outcome'->>'Effect'='committed' AND COALESCE(review_op->'outcome'->>'Completion','') <> 'complete')
		)`

// PendingReviewPublications excludes settled review history from the bounded pass.
func (s *Store) PendingReviewPublications(ctx context.Context, limit int) ([]factory.Publication, error) {
	if limit <= 0 || limit > storePublicationLimit {
		return nil, errors.New("invalid review publication listing limit")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM factory_publications WHERE stage IN ('published','fenced') AND `+pendingPublicationReviews+` ORDER BY seq LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []factory.Publication
	for rows.Next() {
		var raw []byte
		var p factory.Publication
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) OpenPublications(ctx context.Context, limit int) ([]factory.Publication, error) {
	if limit <= 0 || limit > storePublicationLimit {
		return nil, errors.New("invalid publication listing limit")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM factory_publications
		WHERE stage IN ('open','fenced') OR (stage='published' AND (COALESCE((data->>'withdraw_requested')::boolean,FALSE) OR EXISTS (
			SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(data->'corrections')='array' THEN data->'corrections' ELSE '[]'::jsonb END) AS correction
			WHERE COALESCE(correction->>'effect','') IN ('','pending','indeterminate')
			   OR COALESCE(correction->>'cancellation','') IN ('pending','indeterminate')
			   OR (correction->>'effect'='committed' AND COALESCE(correction->>'completion','') <> 'complete')
		) OR `+pendingPublicationReviews+` OR EXISTS (
			SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(data->'resolution_operations')='array' THEN data->'resolution_operations' ELSE '[]'::jsonb END) AS resolve_op
			WHERE COALESCE(resolve_op->'outcome'->>'Effect','') IN ('','pending','indeterminate')
			   OR COALESCE(resolve_op->'outcome'->>'Cancellation','') IN ('pending','indeterminate')
			   OR (resolve_op->'outcome'->>'Effect'='committed' AND (COALESCE(resolve_op->'outcome'->>'Completion','') <> 'complete' OR COALESCE(resolve_op->>'confirmed','false') <> 'true'))
		))) ORDER BY seq LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []factory.Publication
	for rows.Next() {
		var data []byte
		var p factory.Publication
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ActivePublishedPublications returns published candidates whose exact
// assignment root still owns an open issue allowance and has not merged.
// An idle accounting clock does not withdraw its bounded later phases.
// The active-attempt cap keeps this bounded without walking settled history.
func (s *Store) ActivePublishedPublications(ctx context.Context, limit int) ([]factory.Publication, error) {
	return s.activePublishedPublications(ctx, limit, false)
}

// CheckablePublications includes finite merge-only continuations. They can
// refresh CI observations while their inference root remains closed; child
// dispatch deliberately keeps the narrower active-allowance enumeration.
func (s *Store) CheckablePublications(ctx context.Context, limit int) ([]factory.Publication, error) {
	return s.activePublishedPublications(ctx, limit, true)
}

func (s *Store) activePublishedPublications(ctx context.Context, limit int, mergeOnly bool) ([]factory.Publication, error) {
	if limit <= 0 || limit > storePublicationLimit {
		return nil, errors.New("invalid active publication listing limit")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.data FROM factory_publications p
		JOIN factory_assignments a ON a.id=p.assignment
		JOIN factory_attempt_allowances l ON l.repository=p.repository AND l.issue=p.issue
			AND l.root_assignment=a.data->>'attempt_root'
		WHERE p.stage='published'
		  AND ((l.issue>0 AND COALESCE(l.data->>'closed','false') <> 'true')
		    OR ($2 AND EXISTS(SELECT 1 FROM factory_merges retry JOIN factory_commands c ON c.id=retry.data->>'retry_command'
		      WHERE retry.publication=p.data->>'id' AND retry.stage='open' AND c.type='prepare-unsent-merge' AND c.finished<>''
		      AND (retry.data->>'retry_not_after')::bigint>$3)))
		  AND NOT EXISTS (SELECT 1 FROM factory_merges m
			WHERE m.publication=p.data->>'id' AND m.stage='merged')
		ORDER BY p.seq LIMIT $1`, limit, mergeOnly, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []factory.Publication
	for rows.Next() {
		var data []byte
		var p factory.Publication
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PublishableAssignments lists finished assignments whose harness
// reported a completed exact candidate and that carry no publication
// yet, oldest first. Only succeeded work with an attributable report
// publishes; synthesized results never do.
func (s *Store) PublishableAssignments(ctx context.Context, limit int) ([]factory.Assignment, error) {
	if limit <= 0 || limit > storePublishableLimit {
		return nil, errors.New("invalid publishable listing limit")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.data FROM factory_assignments a
		LEFT JOIN factory_publications p ON p.assignment=a.id
		WHERE a.stage='finished' AND p.assignment IS NULL
		AND a.data->>'role'='soda-coder'
		AND a.data->>'publication_assignment'=a.id
		AND a.data->>'outcome'='succeeded'
		AND (a.data#>>'{result,reported}')::boolean
		AND a.data#>>'{result,status}'='completed'
		ORDER BY a.seq LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []factory.Assignment
	for rows.Next() {
		var data []byte
		var a factory.Assignment
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// PendingPublicationChildren lists finished child results awaiting their first
// native registration. Registered operations retain their existing recovery
// path. Only the current published head is eligible for a new effect.
// The assignment cursor bounds each pass without letting a waiting child
// permanently displace later work; a missing cursor starts at the oldest row.
func (s *Store) PendingPublicationChildren(ctx context.Context, afterAssignment string, limit int) ([]factory.Assignment, error) {
	if limit <= 0 || limit > storePublishableLimit || (afterAssignment != "" && !factory.ValidID(afterAssignment)) {
		return nil, errors.New("invalid pending publication child listing")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.data FROM factory_assignments a
		JOIN factory_publications p ON p.assignment=a.data->>'publication_assignment'
		  AND p.repository=a.repository AND p.issue=a.issue
		WHERE a.stage='finished' AND a.data->>'publication_assignment'<>a.id
		AND a.data->>'role' IN ('soda-coder','soda-reviewer')
		AND a.data->>'outcome'='succeeded'
		AND a.data#>>'{result,reported}'='true'
		AND a.data#>>'{result,status}'='completed'
		AND p.stage='published' AND COALESCE(p.data->>'withdraw_requested','false')='false'
		AND a.data->>'source_commit'=p.data->>'candidate'
		AND NOT EXISTS (
			SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(p.data->'review_operations')='array' THEN p.data->'review_operations' ELSE '[]'::jsonb END) op
			WHERE op->>'run_id'=a.run
		)
		AND NOT EXISTS (
			SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(p.data->'corrections')='array' THEN p.data->'corrections' ELSE '[]'::jsonb END) op
			WHERE op->>'run_id'=a.run
		)
		AND a.seq>COALESCE((SELECT seq FROM factory_assignments WHERE id=$1),0)
		ORDER BY a.seq LIMIT $2`, afterAssignment, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []factory.Assignment
	for rows.Next() {
		var data []byte
		var a factory.Assignment
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
