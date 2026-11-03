package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

// storeMergeLimit bounds one outstanding-merge listing.
const storeMergeLimit = 1024

// storeMergeableLimit bounds one mergeable-publication listing.
const storeMergeableLimit = 256

// ErrMergeConflict rejects replacement of a recorded merge intent.
var ErrMergeConflict = errors.New("merge immutable input changed")

// RecordMerge stores one publication's fresh open merge. One publication
// merges at most once: a second record for the publication refuses
// instead of merging twice.
func (s *Store) RecordMerge(ctx context.Context, m factory.Merge) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if m.Stage != factory.MergeOpen || m.Outcome != "" || m.Reason != "" || m.FinishedUnix != 0 || m.Revision != 0 || m.Operation.Attempts != 0 || m.WithdrawRequested {
		return errors.New("merge packet carries fresh open work")
	}
	// Authorization binds only through registration on a recorded row,
	// never on the fresh packet: a recorded row proves the candidate
	// existed before its admission.
	if m.Authorization.Bound() {
		return errors.New("merge packet carries no authorization")
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO factory_merges(publication,repository,issue,pr,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		m.PublicationID, m.Repository, m.Issue, m.PRNumber, m.Stage, m.Revision, string(data)); err != nil {
		return fmt.Errorf("merge record failed: %w", err)
	}
	return nil
}

// MergeByPublication returns one publication's merge.
func (s *Store) MergeByPublication(ctx context.Context, publicationID string) (factory.Merge, error) {
	var m factory.Merge
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_merges WHERE publication=$1`, publicationID).Scan(&data)
	if err == nil {
		err = json.Unmarshal(data, &m)
	}
	return m, err
}

// UpdateMerge stores one merge's next revision exactly once. The caller
// increments Revision first; only the recorded predecessor advances,
// and anything else reports stale instead of overwriting a concurrent
// receipt.
func loadPreviousMergeTx(ctx context.Context, tx *sql.Tx, publicationID string) (factory.Merge, error) {
	var previousData []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_merges WHERE publication=$1 FOR UPDATE`, publicationID).Scan(&previousData); err != nil {
		return factory.Merge{}, err
	}
	var previous factory.Merge
	if err := json.Unmarshal(previousData, &previous); err != nil {
		return factory.Merge{}, err
	}
	return previous, nil
}

func (s *Store) UpdateMerge(ctx context.Context, m factory.Merge) error {
	if err := m.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	previous, err := loadPreviousMergeTx(ctx, tx, m.PublicationID)
	if err != nil {
		return err
	}
	if previous.Revision != m.Revision-1 {
		return ErrStaleRevision
	}
	if err := mergeUpdateAllowed(previous, m); err != nil {
		return err
	}
	// A newly bound union authorization re-verifies its journaled D07
	// acceptance inside this same transaction: the journaled current
	// applicable acceptance behind the exact generation, the current
	// union authority and confirmed accepted resolutions. Recovery of
	// recorded effects never enters this path: only a binding that this
	// write introduces rechecks, so committed effects still reconcile
	// and adopt after a later invalidation.
	if mergeBindsAuthorization(previous, m) && m.Authorization.Generation.UnionBound() {
		if err := recheckMergeUnionBindingTx(ctx, tx, m); err != nil {
			return err
		}
	}
	registering := previous.Operation.Work == nil && m.Operation.Work != nil
	if registering && (previous.WithdrawRequested || m.WithdrawRequested) {
		return ErrDispatchClosed
	}
	if registering {
		if err = ensureAttemptDeadlineForRegistrationTx(ctx, tx, m.AssignmentID, m.Operation.Work.NotAfter); err != nil {
			return err
		}
	}
	result, err := applyMergeUpdateTx(ctx, tx, m, registering)
	if err != nil {
		return err
	}
	return s.settleMergeUpdateTx(ctx, tx, previous, m, registering, result)
}

func applyMergeUpdateTx(ctx context.Context, tx *sql.Tx, m factory.Merge, registering bool) (sql.Result, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	// Gate inspection and registration share one statement: a withdrawal
	// either sees this operation or closes the gate before it can register.
	return tx.ExecContext(ctx, `UPDATE factory_merges SET stage=$1,revision=$2,data=$3
		WHERE publication=$4 AND revision=$5
		AND (NOT $6 OR (
		 NOT EXISTS(SELECT 1 FROM factory_dispatch WHERE repository=$7 AND NOT open)
		 AND EXISTS(SELECT 1 FROM issue_acceptance_heads WHERE repository=$8 AND issue=$9 AND decision=$10)
		 AND NOT EXISTS(SELECT 1 FROM issue_acceptance_withdrawals WHERE repository=$11 AND issue=$12 AND decision=$13)
		 AND EXISTS(SELECT 1 FROM factory_policies WHERE repository=$7
		   AND COALESCE((data->>'enabled')::boolean,FALSE) AND NOT COALESCE((data->>'paused')::boolean,FALSE))
		 AND EXISTS(SELECT 1 FROM factory_operator_grants WHERE repository=$7 AND COALESCE((data->>'active')::boolean,FALSE))
		 AND EXISTS(SELECT 1 FROM project_environment_grants WHERE repository=$7 AND COALESCE((data->>'active')::boolean,FALSE))
		 AND EXISTS(SELECT 1 FROM factory_sponsorships s JOIN factory_assignments a
		   ON a.id=$14 AND s.connection=a.data->>'connection'
		   WHERE s.repository=$7 AND COALESCE((s.data->>'active')::boolean,FALSE))
		 AND EXISTS(SELECT 1 FROM project_requirement_heads WHERE project_id=$15 AND decision=$16)
		 AND EXISTS(SELECT 1 FROM project_approval_heads WHERE project_id=$15 AND decision=$17)
		))`,
		m.Stage, m.Revision, string(data), m.PublicationID, m.Revision-1, registering,
		m.Repository, m.Repository, m.Issue, m.Acceptance, m.Repository, m.Issue, m.Acceptance,
		m.AssignmentID, m.ProjectID, m.Authority.RequirementsID, m.Authority.ApprovalID)
}

func (s *Store) settleMergeUpdateTx(ctx context.Context, tx *sql.Tx, previous, m factory.Merge, registering bool, result sql.Result) error {
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		if registering {
			if open, _, _, readErr := s.DispatchState(ctx, m.Repository); readErr != nil {
				return readErr
			} else if !open {
				return ErrDispatchClosed
			}
		}
		return ErrStaleRevision
	}
	closeRoot := m.Stage == factory.MergeMerged || m.Stage == factory.MergeFenced ||
		m.Stage == factory.MergeFailed && m.Reason != factory.MergeReasonSuperseded && m.Reason != factory.MergeReasonWithdrawn
	if m.Stage != factory.MergeOpen {
		if err = transitionAttemptAssignmentTx(ctx, tx, previous.AssignmentID, closeRoot, time.Now()); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if m.Stage == factory.MergeMerged && previous.Stage != factory.MergeMerged {
		if err = enqueueReadinessRootEventTx(ctx, tx, m.Repository, m.Issue); err != nil {
			return err
		}
		// D13 completion fence (FW31): atomically close the issue
		// allowance (above, through the assignment transition), retain
		// the readiness enqueue (above), insert the unique completion
		// event and increment the repository generation. Rollback rolls
		// back all four. Recovery re-observation replays here and
		// records once through the unique key.
		if _, err = recordCompletionTx(ctx, tx, m); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// mergeBindsAuthorization reports whether the write introduces a new
// authorization binding: a first bind on an unbound row or a
// replacement generation. Carried bindings never recheck.
func mergeBindsAuthorization(old, next factory.Merge) bool {
	if !next.Authorization.Bound() {
		return false
	}
	return !old.Authorization.Bound() ||
		next.Authorization.Generation.Generation != old.Authorization.Generation.Generation
}

// recheckMergeUnionBindingTx re-verifies one newly bound union
// authorization against the publication journal inside the update
// transaction: the acceptance must be journaled, current for the exact
// candidate and base, kind-matching with confirmed accepted resolutions
// under the row authority.
func recheckMergeUnionBindingTx(ctx context.Context, tx *sql.Tx, m factory.Merge) error {
	var data []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE assignment=$1`, m.AssignmentID).Scan(&data); err != nil {
		return err
	}
	var publication factory.Publication
	if err := json.Unmarshal(data, &publication); err != nil {
		return err
	}
	var acceptance factory.DeliveryAcceptance
	found := false
	for _, candidate := range publication.DeliveryAcceptances {
		if candidate.ID == m.Authorization.Generation.AcceptanceID {
			acceptance, found = candidate, true
			break
		}
	}
	if !found {
		return ErrMergeConflict
	}
	if err := factory.CheckMergeUnionBinding(factory.MergeUnionBindingInput{
		Publication: publication, Acceptance: acceptance, Generation: m.Authorization.Generation,
		Decision: m.Authorization.Decision, ResolutionsConfirmed: m.Authorization.ResolutionsConfirmed,
		CurrentAuthority: m.Authority,
	}); err != nil {
		return err
	}
	return nil
}

func mergeHistoryPrefixStable(old, next factory.Merge) error {
	// History is append-only: an ordinary same-generation update carries
	// every prior snapshot exactly. Any equal-length rewrite — receipt,
	// authorization, effect or tuple — refuses with zero writes.
	for i := range old.History {
		if !reflect.DeepEqual(old.History[i], next.History[i]) {
			return ErrMergeConflict
		}
	}
	return nil
}

func mergeLedgerStable(old, next factory.Merge) bool {
	return old.ID == next.ID && old.PublicationID == next.PublicationID && old.AssignmentID == next.AssignmentID &&
		old.ProjectID == next.ProjectID && old.Role == next.Role && old.Acceptance == next.Acceptance &&
		old.Repository == next.Repository && old.Issue == next.Issue && old.PRNumber == next.PRNumber &&
		old.PRID == next.PRID && old.IssueID == next.IssueID && old.PRAuthorID == next.PRAuthorID &&
		old.ReviewerID == next.ReviewerID && old.HeadRef == next.HeadRef && old.BaseRef == next.BaseRef &&
		old.BaseOID == next.BaseOID
}

func mergeAuthorityStable(old, next factory.Merge) bool {
	return old.CreatedUnix == next.CreatedUnix && old.Authority == next.Authority &&
		!(old.WithdrawRequested && !next.WithdrawRequested)
}

func mergeIdentityStable(old, next factory.Merge) error {
	if !mergeLedgerStable(old, next) || !mergeAuthorityStable(old, next) {
		return ErrMergeConflict
	}
	// A pristine row (no drive ever recorded) may follow the newest
	// published head; every other HeadOID change stays a conflict. A
	// rebased row clears its authorization: the binding never moves to
	// another head, and the new head waits for re-authorization.
	if old.HeadOID != next.HeadOID && !mergeRebaseAllowed(old, next) {
		return ErrMergeConflict
	}
	if old.HeadOID != next.HeadOID && (old.Authorization.Bound() && next.Authorization.Bound()) {
		return ErrMergeConflict
	}
	// A bound authorization is immutable on its head. The single
	// exception is the rebase clear: a pristine row following the newest
	// published head drops its stale binding and waits for
	// re-authorization. Stripping a binding without moving the head, or
	// swapping one binding for another, stays a conflict: uncertain or
	// committed predecessors are never replaced on this row.
	if old.Authorization.Bound() && !reflect.DeepEqual(next.Authorization, old.Authorization) &&
		(old.HeadOID == next.HeadOID || next.Authorization.Bound()) {
		return ErrMergeConflict
	}
	return nil
}

func mergeUpdateAllowed(old, next factory.Merge) error {
	// History is never rewritten: authorization binds generation one only.
	if len(next.History) != len(old.History) {
		return ErrMergeConflict
	}
	if err := mergeHistoryPrefixStable(old, next); err != nil {
		return err
	}
	if err := mergeIdentityStable(old, next); err != nil {
		return err
	}
	if !old.Authorization.Bound() && next.Authorization.Bound() {
		if old.Operation.Work != nil || old.Operation.Attempts != 0 {
			return ErrMergeConflict
		}
		if err := next.Authorization.Validate(); err != nil {
			return ErrMergeConflict
		}
	}
	if old.Stage != factory.MergeOpen && old.Stage != factory.MergeFenced {
		return ErrMergeConflict
	}
	if !mergeOperationUpdateAllowed(old.Operation, next.Operation) {
		return ErrMergeConflict
	}
	return nil
}

func pristineMergeOperation(o factory.MergeOperation) bool {
	return o.Work == nil && o.OperationID == "" && o.InstallationID == "" &&
		o.ActorID == 0 && o.RepositoryID == 0 && o.Effect == "" &&
		o.Cancellation == "" && o.Completion == "" && o.Reason == "" &&
		o.Receipt == "" && o.HeadRef == "" && o.BaseRef == "" &&
		o.HeadOID == "" && o.BaseOID == "" && o.MergedCommit == "" &&
		o.Attempts == 0 && o.PRNumber == 0 && o.PRID == 0 &&
		o.IssueID == 0 && o.UpdatedUnix == 0
}

func mergeRebaseAllowed(old, next factory.Merge) bool {
	return old.Operation.Kind == next.Operation.Kind && pristineMergeOperation(old.Operation) && pristineMergeOperation(next.Operation) && next.HeadOID != ""
}

func mergeOperationUpdateAllowed(old, next factory.MergeOperation) bool {
	if old.Kind != next.Kind {
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

// OutstandingMerges lists every open or fenced merge for one
// repository, oldest first. Recovery reconciles each one against native
// state; withdrawal cancels each one's outstanding operation.
func (s *Store) OutstandingMerges(ctx context.Context, repository int64, limit int) ([]factory.Merge, error) {
	if repository <= 0 {
		return nil, errors.New("invalid merge repository")
	}
	if limit <= 0 || limit > storeMergeLimit {
		return nil, errors.New("invalid merge listing limit")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM factory_merges
		WHERE repository=$1 AND stage IN ('open','fenced') ORDER BY seq LIMIT $2`, repository, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []factory.Merge
	for rows.Next() {
		var data []byte
		var m factory.Merge
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// requestMergeWithdrawalsTx durably latches every open or fenced merge under
// the caller's repository gate lock. An oversized set aborts the whole
// settings transaction rather than silently truncating cancellation intent.
func requestMergeWithdrawalsTx(ctx context.Context, tx *sql.Tx, repository int64) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT publication,revision,COALESCE((data->>'withdraw_requested')::boolean,FALSE) FROM factory_merges
		WHERE repository=$1 AND stage IN ('open','fenced') ORDER BY seq LIMIT $2 FOR UPDATE`, repository, storeMergeLimit+1)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	type mergeIntent struct {
		publication string
		revision    int64
		requested   bool
	}
	var merges []mergeIntent
	for rows.Next() {
		var item mergeIntent
		if err = rows.Scan(&item.publication, &item.revision, &item.requested); err != nil {
			return false, err
		}
		merges = append(merges, item)
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	if err = rows.Close(); err != nil {
		return false, err
	}
	if len(merges) > storeMergeLimit {
		return false, errors.New("too many outstanding merges to withdraw")
	}
	for _, m := range merges {
		if m.requested {
			continue
		}
		revision := m.revision + 1
		updated, updateErr := tx.ExecContext(ctx, `UPDATE factory_merges SET revision=$1,
			data=jsonb_set(jsonb_set(data,'{withdraw_requested}','true'::jsonb,true),'{revision}',to_jsonb($1::integer),true)
			WHERE publication=$2 AND revision=$3`, revision, m.publication, m.revision)
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
	return len(merges) > 0, nil
}

// OpenMerges lists every open or fenced merge across repositories,
// oldest first, for pass recovery. Deferred work waits for a later
// pass; nothing here submits without reconciling first.
func (s *Store) OpenMerges(ctx context.Context, limit int) ([]factory.Merge, error) {
	if limit <= 0 || limit > storeMergeLimit {
		return nil, errors.New("invalid merge listing limit")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM factory_merges
		WHERE stage IN ('open','fenced') ORDER BY seq LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []factory.Merge
	for rows.Next() {
		var data []byte
		var m factory.Merge
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MergeablePublications lists published publications that carry no
// merge yet, oldest first. Only natively completed exact PRs merge;
// anything else never reaches this listing.
func (s *Store) MergeablePublications(ctx context.Context, limit int) ([]factory.Publication, error) {
	if limit <= 0 || limit > storeMergeableLimit {
		return nil, errors.New("invalid mergeable listing limit")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.data FROM factory_publications p
		LEFT JOIN factory_merges m ON m.publication=p.data->>'id'
		WHERE p.stage='published' AND m.publication IS NULL
		ORDER BY p.seq LIMIT $1`, limit)
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

// MergeForIssue returns the latest merge for one repository issue, any
// stage. Absence reports ErrNotFound: no merge exists yet for it.
func (s *Store) MergeForIssue(ctx context.Context, repository, issue int64) (factory.Merge, error) {
	var m factory.Merge
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_merges WHERE repository=$1 AND issue=$2 ORDER BY seq DESC LIMIT 1`, repository, issue).Scan(&data)
	if err == nil {
		err = json.Unmarshal(data, &m)
	}
	return m, err
}

// IssueMergeCompletion returns the latest completed merge for one
// repository issue: the attributable factory completion behind code
// prerequisite satisfaction. Absence reports ErrNotFound.
func (s *Store) IssueMergeCompletion(ctx context.Context, repository, issue int64) (factory.Merge, error) {
	var m factory.Merge
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT data FROM factory_merges WHERE repository=$1 AND issue=$2 AND stage='merged' ORDER BY seq DESC LIMIT 1`, repository, issue).Scan(&data)
	if err == nil {
		err = json.Unmarshal(data, &m)
	}
	return m, err
}
