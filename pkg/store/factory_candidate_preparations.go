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
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

// CandidatePreparationRegistration is the Store admission request for one
// reviewer checkout or an accepted explicit coder Retry. The owner and deadline are
// recorded beside the ordinary preparation record before the host is called.
type CandidatePreparationRegistration struct {
	Repository        int64
	Issue             int64
	ActorID           int64
	Project           string
	OwnerAssignment   string
	ChildAssignment   string
	AttemptRoot       string
	PublicationID     string
	Connection        string
	Authority         factory.AuthorityRef
	GateRevision      int64
	Control           factory.IssueControl
	RequestedNotAfter int64
	SourceRun         *project.FactoryExport
	SourceMerge       *project.FactoryMergedSource
}

// CandidatePreparationRecord joins the public preparation view with its
// Store-owned admission and native-stop receipt.
type CandidatePreparationRecord struct {
	Preparation execution.PreparationRecord
	Admission   CandidatePreparationRegistration
	NotAfter    int64
	Stopped     bool
	Retirement  string
}

// CandidatePreparation reads the exact custody owner, including retirement.
func (s *Store) CandidatePreparation(ctx context.Context, id string) (CandidatePreparationRecord, error) {
	var raw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT data FROM project_preparations WHERE id=$1`, id).Scan(&raw); err != nil {
		return CandidatePreparationRecord{}, err
	}
	p, meta, err := unmarshalCandidatePreparation(raw)
	if err != nil || meta == nil {
		return CandidatePreparationRecord{}, errors.New("candidate preparation admission is missing")
	}
	return CandidatePreparationRecord{Preparation: p, Admission: registrationFromMeta(*meta), NotAfter: meta.NotAfter, Stopped: meta.Stopped, Retirement: meta.Retirement}, nil
}

// PendingRetryPreparations includes only setup not yet transferred to a run.
// Earliest deadlines lead the bounded page so expired custody is retired first.
func (s *Store) PendingRetryPreparations(ctx context.Context, limit int) ([]CandidatePreparationRecord, error) {
	if limit <= 0 || limit > 256 {
		return nil, errors.New("invalid retry preparation limit")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.data FROM project_preparations p
		WHERE p.role=$1 AND p.data ? 'factory_admission'
		AND p.data#>>'{factory_admission,publication_id}'=''
		AND NOT (COALESCE((p.data#>>'{factory_admission,stopped}')::boolean,FALSE)
			AND COALESCE(p.data#>>'{factory_admission,retirement}'='confirmed',FALSE))
		AND NOT EXISTS(SELECT 1 FROM factory_assignments a WHERE a.id=p.data#>>'{factory_admission,owner_assignment}')
		ORDER BY (p.data#>>'{factory_admission,not_after_unix}')::bigint,p.id LIMIT $2`, project.RoleCoder, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CandidatePreparationRecord
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		p, meta, decodeErr := unmarshalCandidatePreparation(raw)
		if decodeErr != nil || meta == nil {
			return nil, errors.New("retry preparation admission is missing")
		}
		out = append(out, CandidatePreparationRecord{Preparation: p, Admission: registrationFromMeta(*meta), NotAfter: meta.NotAfter, Stopped: meta.Stopped, Retirement: meta.Retirement})
	}
	return out, rows.Err()
}

type candidatePreparationRegistration struct {
	Repository      int64                        `json:"repository"`
	Issue           int64                        `json:"issue"`
	ActorID         int64                        `json:"actor_id,string"`
	Project         string                       `json:"project"`
	OwnerAssignment string                       `json:"owner_assignment"`
	ChildAssignment string                       `json:"child_assignment"`
	AttemptRoot     string                       `json:"attempt_root"`
	PublicationID   string                       `json:"publication_id"`
	Connection      string                       `json:"connection"`
	Authority       factory.AuthorityRef         `json:"authority"`
	GateRevision    int64                        `json:"gate_revision"`
	Control         factory.IssueControl         `json:"control"`
	SourceRun       *project.FactoryExport       `json:"source_run,omitempty"`
	SourceMerge     *project.FactoryMergedSource `json:"source_merge,omitempty"`
	NotAfter        int64                        `json:"not_after_unix"`
	Stopped         bool                         `json:"stopped,omitempty"`
	Retirement      string                       `json:"retirement,omitempty"`
}

var (
	ErrCandidatePreparationPending  = errors.New("candidate preparation retirement is not confirmed")
	ErrCandidatePreparationConflict = errors.New("candidate preparation immutable input changed")
)

// AdmitCandidatePreparation is the atomic pre-host gate for a candidate
// preparation. Replays retain the first absolute native deadline and exact
// owner/authority tuple.
func admitCandidatePreparationShape(preparation execution.PreparationRecord, registration CandidatePreparationRegistration) error {
	if err := preparation.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrCandidatePreparationConflict, err)
	}
	if preparation.State.ID != "" || preparation.State.Phase != "" {
		return fmt.Errorf("%w: admission must start without observed state", ErrCandidatePreparationConflict)
	}
	if err := validateCandidatePreparationRegistration(preparation, registration); err != nil {
		return fmt.Errorf("%w: %v", ErrCandidatePreparationConflict, err)
	}
	return nil
}

func admitCandidatePreparationDeadlineTx(ctx context.Context, tx *sql.Tx, registration CandidatePreparationRegistration, grants admissionGrants, now time.Time) (int64, error) {
	deadline := now.Add(10 * time.Minute).Unix()
	if registration.PublicationID != "" {
		allowance, lockErr := lockAttemptAllowanceRootTx(ctx, tx, registration.Repository, registration.Issue, registration.AttemptRoot)
		if lockErr != nil {
			return 0, lockErr
		}
		var err error
		deadline, err = attemptDeadlineUnix(allowance, now)
		if err != nil {
			return 0, err
		}
	}
	if registration.RequestedNotAfter < deadline {
		deadline = registration.RequestedNotAfter
	}
	if deadline <= now.Unix() {
		return 0, factory.ErrAttemptTimeExhausted
	}
	return deadline, nil
}

func insertCandidatePreparationTx(ctx context.Context, tx *sql.Tx, preparation execution.PreparationRecord, registration CandidatePreparationRegistration, grants admissionGrants, deadline int64) (CandidatePreparationRecord, bool, error) {
	var empty CandidatePreparationRecord
	meta := candidatePreparationRegistration{
		Repository: registration.Repository, Issue: registration.Issue, ActorID: registration.ActorID, Project: registration.Project,
		OwnerAssignment: registration.OwnerAssignment, ChildAssignment: registration.ChildAssignment,
		AttemptRoot: registration.AttemptRoot, PublicationID: registration.PublicationID,
		Connection: registration.Connection, Authority: registration.Authority,
		GateRevision: registration.GateRevision, Control: registration.Control, NotAfter: deadline, SourceRun: registration.SourceRun, SourceMerge: registration.SourceMerge,
	}
	data, err := marshalCandidatePreparation(preparation, meta)
	if err != nil {
		return empty, false, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO project_preparations(id,project_id,role,revision,requirements,approval,data)
		VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO NOTHING`, preparation.Preparation.ID,
		preparation.Preparation.Project, preparation.Preparation.Role, preparation.Preparation.Revision,
		preparation.Preparation.Requirements.ID, preparation.Preparation.Approval.ID, string(data))
	if err != nil {
		return empty, false, fmt.Errorf("candidate preparation admission failed: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return empty, false, err
	}
	created := inserted == 1
	if created {
		if err = checkAdmissionLimitsTx(ctx, tx, registration.Repository, registration.Connection, registration.Project, grants); err != nil {
			return empty, false, err
		}
	}
	var storedData []byte
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT revision,data FROM project_preparations WHERE id=$1 FOR UPDATE`, preparation.Preparation.ID).Scan(&revision, &storedData); err != nil {
		return empty, false, err
	}
	var stored execution.PreparationRecord
	var storedMeta *candidatePreparationRegistration
	if stored, storedMeta, err = unmarshalCandidatePreparation(storedData); err != nil {
		return empty, false, err
	}
	if !samePreparationIdentity(stored, preparation) || storedMeta == nil {
		return empty, false, fmt.Errorf("%w: preparation identity already carries different admission", ErrCandidatePreparationConflict)
	}
	if !sameCandidatePreparationAdmission(*storedMeta, meta, !created) {
		return empty, false, fmt.Errorf("%w: candidate preparation admission changed", ErrCandidatePreparationConflict)
	}
	if revision != stored.Preparation.Revision {
		return empty, false, fmt.Errorf("%w: candidate preparation revision differs", ErrCandidatePreparationConflict)
	}
	if created {
		if err = recordFactoryFeedEventTx(ctx, tx, registration.Repository, registration.Issue, registration.OwnerAssignment, preparation.Preparation.ID+":admitted", "preparation:approved", factory.FeedCommunication{
			ComponentID: "F01", Title: "Worker environment admitted", Summary: "The recorded source and approved environment are ready for preparation.",
			Items: []factory.FeedCommunicationItem{{Label: preparation.Preparation.Role, Detail: preparation.Preparation.ID, State: "approved"}},
		}, 0); err != nil {
			return empty, false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return empty, false, err
	}
	return CandidatePreparationRecord{
		Preparation: stored, Admission: registrationFromMeta(*storedMeta), NotAfter: storedMeta.NotAfter,
		Stopped: storedMeta.Stopped, Retirement: storedMeta.Retirement,
	}, created, nil
}

func (s *Store) AdmitCandidatePreparation(ctx context.Context, preparation execution.PreparationRecord, registration CandidatePreparationRegistration) (CandidatePreparationRecord, bool, error) {
	var empty CandidatePreparationRecord
	if err := admitCandidatePreparationShape(preparation, registration); err != nil {
		return empty, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, false, err
	}
	defer func() { _ = tx.Rollback() }()
	grants, err := authorizeCandidatePreparationTx(ctx, tx, preparation, registration)
	if err != nil {
		return empty, false, err
	}
	if err = checkCandidatePreparationOwnerTx(ctx, tx, preparation, registration, grants.sponsorship.ActorID, grants.policy.TargetBranch); err != nil {
		return empty, false, err
	}
	now := time.Now()
	deadline, err := admitCandidatePreparationDeadlineTx(ctx, tx, registration, grants, now)
	if err != nil {
		return empty, false, err
	}
	return insertCandidatePreparationTx(ctx, tx, preparation, registration, grants, deadline)
}

// AuthorizeCandidatePreparation rechecks the recorded exact gate and owner
// immediately before the host call and returns its original deadline.
type admittedCandidatePreparation struct {
	preparation  execution.PreparationRecord
	meta         *candidatePreparationRegistration
	registration CandidatePreparationRegistration
	grants       admissionGrants
}

func (s *Store) admitCandidatePreparationTx(ctx context.Context, tx *sql.Tx, id string, preparation execution.PreparationRecord, meta *candidatePreparationRegistration) (admittedCandidatePreparation, error) {
	var admitted admittedCandidatePreparation
	registration := registrationFromMeta(*meta)
	grants, err := authorizeCandidatePreparationTx(ctx, tx, preparation, registration)
	if err != nil {
		return admitted, err
	}
	if err = checkCandidatePreparationOwnerTx(ctx, tx, preparation, registration, grants.sponsorship.ActorID, grants.policy.TargetBranch); err != nil {
		return admitted, err
	}
	var currentData []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM project_preparations WHERE id=$1 FOR UPDATE`, id).Scan(&currentData); err != nil {
		return admitted, err
	}
	currentPreparation, currentMeta, err := unmarshalCandidatePreparation(currentData)
	if err != nil || currentMeta == nil || !samePreparationIdentity(currentPreparation, preparation) ||
		!sameCandidatePreparationAdmission(*currentMeta, *meta, false) {
		return admitted, errors.New("candidate preparation admission changed")
	}
	return admittedCandidatePreparation{preparation: currentPreparation, meta: currentMeta, registration: registration, grants: grants}, nil
}

func candidatePreparationDeadlineTx(ctx context.Context, tx *sql.Tx, admitted admittedCandidatePreparation) (int64, error) {
	deadline := admitted.meta.NotAfter
	if admitted.registration.PublicationID != "" {
		allowance, lockErr := lockAttemptAllowanceRootTx(ctx, tx, admitted.registration.Repository, admitted.registration.Issue, admitted.registration.AttemptRoot)
		if lockErr != nil {
			return 0, lockErr
		}
		return attemptDeadlineUnix(allowance, time.Now())
	}
	return deadline, nil
}

func (s *Store) AuthorizeCandidatePreparation(ctx context.Context, id string) (int64, error) {
	if !project.ValidPreparationID(id) {
		return 0, errors.New("invalid candidate preparation identity")
	}
	var raw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT data FROM project_preparations WHERE id=$1`, id).Scan(&raw); err != nil {
		return 0, err
	}
	preparation, meta, err := unmarshalCandidatePreparation(raw)
	if err != nil || meta == nil {
		return 0, errors.New("candidate preparation admission is missing")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	admitted, err := s.admitCandidatePreparationTx(ctx, tx, id, preparation, meta)
	if err != nil {
		return 0, err
	}
	deadline, err := candidatePreparationDeadlineTx(ctx, tx, admitted)
	if err != nil {
		return 0, err
	}
	meta, preparation = admitted.meta, admitted.preparation
	now := time.Now().Unix()
	if meta.Stopped || meta.Retirement == "confirmed" {
		return 0, factory.ErrAttemptClosed
	}
	if now >= meta.NotAfter || meta.NotAfter > deadline {
		return 0, factory.ErrAttemptTimeExhausted
	}
	if preparation.State.Ready || preparation.State.Phase == project.PrepareFailed {
		return 0, errors.New("candidate preparation is already terminal")
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return meta.NotAfter, nil
}

// CandidatePreparations pages exact pending admissions for one repository.
// A zero issue/root scope includes every pending preparation in the project.
func (s *Store) CandidatePreparations(ctx context.Context, repository int64, projectID string, issue int64, root, after string, limit int, expiredBefore int64) ([]CandidatePreparationRecord, error) {
	if repository <= 0 || !project.ValidID(projectID) || limit <= 0 || limit > 256 || issue < 0 || root != "" && !factory.ValidID(root) || after != "" && !project.ValidPreparationID(after) {
		return nil, errors.New("invalid candidate preparation listing scope")
	}
	query := `SELECT data FROM project_preparations
		WHERE project_id=$1 AND id>$2 AND data ? 'factory_admission'
		AND (data->'factory_admission'->>'repository')::bigint=$3
		AND (COALESCE(data->'factory_admission'->>'issue','0')::bigint=$4 OR $4=0)
		AND (COALESCE(data->'factory_admission'->>'attempt_root','')=$5 OR $5='')
		AND (COALESCE(data->'factory_admission'->>'not_after_unix','0')::bigint<=$6 OR $6=0)
		AND NOT soda_preparation_quiescent(data,id,project_id,role)
		ORDER BY id LIMIT $7`
	rows, err := s.db.QueryContext(ctx, query, projectID, after, repository, issue, root, expiredBefore, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]CandidatePreparationRecord, 0)
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		preparation, meta, decodeErr := unmarshalCandidatePreparation(data)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if meta == nil {
			return nil, errors.New("candidate preparation admission disappeared")
		}
		out = append(out, CandidatePreparationRecord{
			Preparation: preparation, Admission: registrationFromMeta(*meta), NotAfter: meta.NotAfter,
			Stopped: meta.Stopped, Retirement: meta.Retirement,
		})
	}
	return out, rows.Err()
}

// RecordCandidatePreparationStop saves the native stop tombstone without
// fabricating role/setup observations for a stop-before-approve response.
func admitCandidatePreparationStopShape(id string, state execution.PreparationState) error {
	if !project.ValidPreparationID(id) || state.ID != id || !state.Stopped ||
		(state.Retirement != "confirmed" && state.Retirement != "uncertain") {
		return errors.New("invalid candidate preparation stop receipt")
	}
	return nil
}

func admitCandidatePreparationStopIdentity(state execution.PreparationState, preparation execution.PreparationRecord) error {
	if state.ValidateFor(preparation.Address()) != nil || state.Role != "" && state.Role != preparation.Preparation.Role ||
		state.SourceCommit != "" && state.SourceCommit != preparation.Preparation.SourceCommit ||
		state.SetupDigest != "" && state.SetupDigest != preparation.Preparation.SetupDigest {
		return errors.New("candidate preparation stop receipt identity differs")
	}
	return nil
}

func lockStopPreparationTx(ctx context.Context, tx *sql.Tx, id string, preparation execution.PreparationRecord, meta *candidatePreparationRegistration) (execution.PreparationRecord, *candidatePreparationRegistration, int64, error) {
	var empty execution.PreparationRecord
	var current []byte
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision,data FROM project_preparations WHERE id=$1 FOR UPDATE`, id).Scan(&revision, &current); err != nil {
		return empty, nil, 0, err
	}
	stored, currentMeta, err := unmarshalCandidatePreparation(current)
	if err != nil || currentMeta == nil || !samePreparationIdentity(stored, preparation) ||
		!sameCandidatePreparationAdmission(*currentMeta, *meta, false) {
		return empty, nil, 0, errors.New("candidate preparation admission changed during stop")
	}
	return stored, currentMeta, revision, nil
}

func persistPreparationStopTx(ctx context.Context, tx *sql.Tx, id string, stored execution.PreparationRecord, currentMeta *candidatePreparationRegistration, revision int64, state execution.PreparationState) error {
	currentMeta.Stopped, currentMeta.Retirement = state.Stopped, state.Retirement
	stored.Preparation.Revision++
	data, err := marshalCandidatePreparation(stored, *currentMeta)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE project_preparations SET revision=$1,data=$2 WHERE id=$3 AND revision=$4`, stored.Preparation.Revision, string(data), id, revision)
	if err != nil {
		return err
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
		if rowsErr != nil {
			return rowsErr
		}
		return errors.New("candidate preparation stop receipt became stale")
	}
	return nil
}

func (s *Store) RecordCandidatePreparationStop(ctx context.Context, id string, state execution.PreparationState) error {
	if err := admitCandidatePreparationStopShape(id, state); err != nil {
		return err
	}
	var raw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT data FROM project_preparations WHERE id=$1`, id).Scan(&raw); err != nil {
		return err
	}
	preparation, meta, err := unmarshalCandidatePreparation(raw)
	if err != nil || meta == nil {
		return errors.New("candidate preparation admission is missing")
	}
	if err = admitCandidatePreparationStopIdentity(state, preparation); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	allowance, lockErr := lockAttemptAllowanceRootTx(ctx, tx, meta.Repository, meta.Issue, meta.AttemptRoot)
	if lockErr != nil && !errors.Is(lockErr, sql.ErrNoRows) {
		return lockErr
	}
	stored, currentMeta, revision, err := lockStopPreparationTx(ctx, tx, id, preparation, meta)
	if err != nil {
		return err
	}
	if currentMeta.Stopped && currentMeta.Retirement == "confirmed" && state.Retirement != "confirmed" {
		return errors.New("confirmed candidate preparation retirement cannot be downgraded")
	}
	if currentMeta.Stopped && currentMeta.Retirement == state.Retirement {
		return tx.Commit()
	}
	if err = persistPreparationStopTx(ctx, tx, id, stored, currentMeta, revision, state); err != nil {
		return err
	}
	if lockErr == nil {
		if err = transitionLockedAttemptRootTx(ctx, tx, allowance, allowance.Closed, time.Now()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func validateCandidatePreparationIdentity(preparation execution.PreparationRecord, r CandidatePreparationRegistration) error {
	if r.Repository <= 0 || r.Issue <= 0 || r.ActorID <= 0 || !project.ValidID(r.Project) || r.Project != preparation.Preparation.Project ||
		!factory.ValidID(r.OwnerAssignment) || !factory.ValidID(r.ChildAssignment) || !factory.ValidID(r.AttemptRoot) ||
		(r.PublicationID != "" && !factory.ValidID(r.PublicationID)) || r.Connection == "" || r.GateRevision < 0 || r.RequestedNotAfter <= 0 {
		return errors.New("invalid candidate preparation registration")
	}
	return nil
}

func validateCandidatePreparationControl(preparation execution.PreparationRecord, r CandidatePreparationRegistration) error {
	if r.Control.Validate() != nil || r.Control.Repository != r.Repository || r.Control.Issue != r.Issue ||
		r.Control.Readiness != factory.ReadinessQueued || r.Authority.RequirementsID != preparation.Preparation.Requirements.ID ||
		r.Authority.ApprovalID != preparation.Preparation.Approval.ID || r.Authority.SponsorshipConnection != r.Connection ||
		(r.PublicationID != "" && preparation.Preparation.Role != project.RoleReviewer) ||
		(r.PublicationID == "" && (preparation.Preparation.Role != project.RoleCoder ||
			r.OwnerAssignment != r.ChildAssignment || r.AttemptRoot != r.OwnerAssignment)) {
		return errors.New("invalid candidate preparation registration")
	}
	return nil
}

func validateCandidatePreparationRegistration(preparation execution.PreparationRecord, r CandidatePreparationRegistration) error {
	if err := validateCandidatePreparationIdentity(preparation, r); err != nil {
		return err
	}
	if err := validateCandidatePreparationControl(preparation, r); err != nil {
		return err
	}
	if r.SourceRun != nil {
		if r.SourceMerge != nil {
			return errors.New("candidate preparation has conflicting sources")
		}
		input := project.FactoryCandidate{Preparation: preparation.Preparation, SourcePreparation: r.SourceRun.Preparation, SourceRun: r.SourceRun, Deadline: time.Unix(r.RequestedNotAfter, 0)}
		// Source setup can share an export preparation; request freshness is
		// checked separately against its actual protected setup by the host.
		if input.SourcePreparation == input.Preparation.ID || input.Validate() != nil {
			return errors.New("invalid preparation source export")
		}
	}
	if r.SourceMerge != nil {
		if preparation.Native == nil || r.SourceMerge.Validate() != nil || r.SourceMerge.Repository != r.Repository || r.SourceMerge.Export.Project != r.Project || r.SourceMerge.Commit != preparation.Preparation.SourceCommit || preparation.Preparation.Role != project.RoleCoder || r.PublicationID != "" {
			return errors.New("invalid merged preparation source")
		}
	}
	return nil
}

func authorizeCandidatePreparationTx(ctx context.Context, tx *sql.Tx, preparation execution.PreparationRecord, r CandidatePreparationRegistration) (admissionGrants, error) {
	var empty admissionGrants
	open, revision, _, err := ensureDispatchGateTx(ctx, tx, r.Repository)
	if err != nil {
		return empty, err
	}
	if !open {
		return empty, ErrDispatchClosed
	}
	if revision != r.GateRevision {
		return empty, ErrStaleRevision
	}
	if err = checkCandidatePreparationControlTx(ctx, tx, r.Control, r.Repository, r.Issue, r.Control.Acceptance); err != nil {
		return empty, err
	}
	// The probe authorizes the attempt the preparation serves: an
	// investigation-rooted coder root for explicit coder retries, the
	// publication review child for reviewer preparations. It carries the
	// owning guard's phase, subject and parent, never a phaseless
	// record.
	assignment := factory.Assignment{
		Authority: r.Authority, ID: r.ChildAssignment, AttemptRoot: r.AttemptRoot,
		PublicationAssignment: r.OwnerAssignment, ProjectID: r.Project,
		ActorID: r.ActorID, Role: preparation.Preparation.Role, Repository: r.Repository, Issue: r.Issue,
		NativeRev: r.Control.NativeRev, Acceptance: r.Control.Acceptance,
		Preparation: preparation.Preparation.ID, Connection: r.Connection,
		SourceCommit: preparation.Preparation.SourceCommit,
		Phase:        project.ExecutionInvestigate,
		Subject: factory.ExecutionSubject{
			Kind:  factory.SubjectIssue,
			Issue: &factory.IssueSubject{Issue: r.Issue, Acceptance: r.Control.Acceptance},
		},
	}
	if r.PublicationID != "" {
		assignment.Phase = project.ExecutionReview
		assignment.ParentAssignment = r.OwnerAssignment
	}
	if err = validateAttemptPacketTx(ctx, tx, assignment, false); err != nil {
		return empty, err
	}
	if r.PublicationID != "" {
		var publicationData []byte
		if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE assignment=$1`, r.OwnerAssignment).Scan(&publicationData); err != nil {
			return empty, err
		}
		var publication factory.Publication
		if err = json.Unmarshal(publicationData, &publication); err != nil {
			return empty, err
		}
		if publication.ID != r.PublicationID || publication.Candidate != preparation.Preparation.SourceCommit ||
			publication.ProjectID != r.Project || publication.Stage != factory.PublicationPublished || publication.WithdrawRequested {
			return empty, ErrAttemptRootMismatch
		}
	}
	grants, err := loadAdmissionGrantsTx(ctx, tx, r.Repository, r.Connection)
	if err != nil {
		return empty, err
	}
	if grants.sponsorship.ProjectID != r.Project || grants.sponsorship.ActorID != r.ActorID {
		return empty, ErrAdmissionChanged
	}
	if preparation.Native != nil {
		if grants.environment.Native == nil || grants.environment.Project != r.Project ||
			preparation.Native.Profile != grants.environment.Native.Profile {
			return empty, ErrAdmissionChanged
		}
		if err = requireNativeDecisionTx(ctx, tx, r.Project, grants.environment.Native); err != nil {
			return empty, err
		}
	} else if grants.environment.Native != nil {
		return empty, ErrAdmissionChanged
	}
	if err = checkAssignmentAuthorityTx(ctx, tx, r.Authority, assignment, grants); err != nil {
		return empty, err
	}
	if err = checkCandidatePreparationSetupTx(ctx, tx, preparation.Preparation); err != nil {
		return empty, err
	}
	return grants, nil
}

// IDs alone cannot authorize a caller-selected effective input map. Match
// both complete immutable references against the current human decisions.
func checkCandidatePreparationSetupTx(ctx context.Context, tx *sql.Tx, p project.Preparation) error {
	var requirementData, approvalData []byte
	var requirementDepth, approvalDepth int64
	if err := tx.QueryRowContext(ctx, `SELECT data,(SELECT count(*) FROM project_requirement_decisions WHERE project_id=$2) FROM project_requirement_decisions WHERE id=$1`, p.Requirements.ID, p.Project).Scan(&requirementData, &requirementDepth); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT data,(SELECT count(*) FROM project_approval_decisions WHERE project_id=$2) FROM project_approval_decisions WHERE id=$1`, p.Approval.ID, p.Project).Scan(&approvalData, &approvalDepth); err != nil {
		return err
	}
	var requirement project.RequirementDecision
	var approval project.ApprovalDecision
	if err := json.Unmarshal(requirementData, &requirement); err != nil {
		return err
	}
	if err := json.Unmarshal(approvalData, &approval); err != nil {
		return err
	}
	if requirement.Validate() != nil || approval.Validate() != nil || !requirement.Ref(requirementDepth).Equal(p.Requirements) ||
		approval.Ref(approvalDepth) != p.Approval || requirement.Project != p.Project || approval.Project != p.Project ||
		requirement.SetupDigest != p.SetupDigest || approval.Requirement != requirement.ID || !approval.AllowsPreparation() {
		return ErrCandidatePreparationConflict
	}
	return nil
}

func checkCandidatePreparationOwnerTx(ctx context.Context, tx *sql.Tx, preparation execution.PreparationRecord, r CandidatePreparationRegistration, actorID int64, target string) error {
	if r.PublicationID == "" {
		if r.SourceRun != nil || r.SourceMerge != nil {
			if err := checkMergedPreparationSourceTx(ctx, tx, r, preparation.Preparation.SourceCommit, target); err != nil {
				return err
			}
			return checkQueuedPreparationOwnerTx(ctx, tx, r)
		}
		return checkRetryPreparationOwnerTx(ctx, tx, r)
	}
	allowance, err := lockAttemptAllowanceRootTx(ctx, tx, r.Repository, r.Issue, r.AttemptRoot)
	if err != nil {
		return err
	}
	if allowance.Repository != r.Repository || allowance.Issue != r.Issue || allowance.RootAssignment != r.AttemptRoot ||
		allowance.Closed || !allowance.Active {
		return factory.ErrAttemptClosed
	}
	owner, err := currentAttemptOwnerTx(ctx, tx, r.Repository, r.Issue, r.AttemptRoot)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAttemptRootMismatch
	}
	if err != nil {
		return err
	}
	if owner != r.OwnerAssignment {
		return ErrAttemptRootMismatch
	}
	var data []byte
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1`, owner).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAttemptRootMismatch
		}
		return err
	}
	var assignment factory.Assignment
	if err = json.Unmarshal(data, &assignment); err != nil {
		return err
	}
	if assignment.ActorID != actorID || assignment.ProjectID != r.Project {
		return ErrAttemptRootMismatch
	}
	return nil
}

// A pending explicit Retry owns private setup before its dispatch packet and
// fresh allowance exist. Recheck the same normal Retry facts; setup cannot
// consume an old root, invent a run, or outlive its first native deadline.
func checkRetryPreparationOwnerTx(ctx context.Context, tx *sql.Tx, r CandidatePreparationRegistration) error {
	var typ, target, payload, finished string
	if err := tx.QueryRowContext(ctx, `SELECT type,target,payload,finished FROM factory_commands WHERE id=$1 FOR UPDATE`, r.OwnerAssignment).Scan(&typ, &target, &payload, &finished); err != nil {
		return err
	}
	var prior struct {
		Run     string `json:"prior_run"`
		Outcome string `json:"prior_outcome"`
	}
	if typ != factory.CommandRetry || finished == "" || json.Unmarshal([]byte(payload), &prior) != nil || target != "run/"+prior.Run {
		return ErrExplicitRetryIneligible
	}
	selected, err := pendingExplicitRetry(ctx, tx, r.Repository, r.Issue, r.Control.Acceptance)
	if err != nil {
		return err
	}
	if selected.CommandID != r.OwnerAssignment || selected.Prior != prior.Run {
		return ErrExplicitRetryIneligible
	}
	return validateExplicitRetryFactsTx(ctx, tx, prior.Run, r.Repository, r.Issue, r.Control.Acceptance, prior.Outcome)
}

func marshalCandidatePreparation(p execution.PreparationRecord, meta candidatePreparationRegistration) ([]byte, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	admission, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	envelope["factory_admission"] = admission
	return json.Marshal(envelope)
}

func unmarshalCandidatePreparation(data []byte) (execution.PreparationRecord, *candidatePreparationRegistration, error) {
	var preparation execution.PreparationRecord
	if err := json.Unmarshal(data, &preparation); err != nil {
		return preparation, nil, err
	}
	if err := preparation.Validate(); err != nil {
		return preparation, nil, err
	}
	var envelope struct {
		Admission *candidatePreparationRegistration `json:"factory_admission"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return preparation, nil, err
	}
	return preparation, envelope.Admission, nil
}

func registrationFromMeta(m candidatePreparationRegistration) CandidatePreparationRegistration {
	return CandidatePreparationRegistration{
		Repository: m.Repository, Issue: m.Issue, ActorID: m.ActorID, Project: m.Project, OwnerAssignment: m.OwnerAssignment,
		ChildAssignment: m.ChildAssignment, AttemptRoot: m.AttemptRoot, PublicationID: m.PublicationID,
		Connection: m.Connection, Authority: m.Authority, GateRevision: m.GateRevision,
		Control: m.Control, RequestedNotAfter: m.NotAfter, SourceRun: m.SourceRun, SourceMerge: m.SourceMerge,
	}
}

func samePreparationIdentity(left, right execution.PreparationRecord) bool {
	left.Preparation.Revision, right.Preparation.Revision = 0, 0
	return reflect.DeepEqual(left.Preparation, right.Preparation) && reflect.DeepEqual(left.Native, right.Native)
}

func sameCandidatePreparationAdmission(left, right candidatePreparationRegistration, preserveDeadline bool) bool {
	// JSON omits empty readiness collections. Compare their contents before
	// comparing the remaining captured admission, including its scalar fields.
	if !sameControlOutcome(left.Control, right.Control) {
		return false
	}
	left.Control.Blockers, right.Control.Blockers = nil, nil
	left.Control.EndpointHeads, right.Control.EndpointHeads = nil, nil
	left.Control.Satisfied, right.Control.Satisfied = nil, nil
	if preserveDeadline {
		left.NotAfter = 0
		right.NotAfter = 0
	}
	left.Stopped, right.Stopped = false, false
	left.Retirement, right.Retirement = "", ""
	left.Control.NativeRev, right.Control.NativeRev = 0, 0
	left.Control.AssessedUnix, right.Control.AssessedUnix = 0, 0
	return reflect.DeepEqual(left, right)
}

// Candidate preparation replay binds semantic readiness, not the native
// revision used to order fresh reads. NativeRev may advance while the same
// queued authorization remains valid.
func checkCandidatePreparationControlTx(ctx context.Context, tx *sql.Tx, expected factory.IssueControl, repository, issue int64, acceptance string) error {
	if err := expected.Validate(); err != nil || expected.Readiness != factory.ReadinessQueued ||
		expected.Repository != repository || expected.Issue != issue || expected.Acceptance != acceptance {
		return ErrDispatchControlStale
	}
	var data []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM issue_controls WHERE repository=$1 AND issue=$2 FOR UPDATE`, repository, issue).Scan(&data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDispatchControlStale
		}
		return err
	}
	var current factory.IssueControl
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	if err := current.Validate(); err != nil || current.Readiness != factory.ReadinessQueued ||
		current.Repository != repository || current.Issue != issue || current.Acceptance != acceptance ||
		current.NativeRev < expected.NativeRev || current.Revision != expected.Revision ||
		current.Fingerprint != expected.Fingerprint || current.Authority != expected.Authority ||
		!sameControlOutcome(current, expected) {
		return ErrDispatchControlStale
	}
	return nil
}
