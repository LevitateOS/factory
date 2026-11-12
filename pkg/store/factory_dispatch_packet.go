package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

// ErrAssignmentActive reports a dispatch packet for an issue that already
// carries an unfinished assignment. One issue runs at most one assignment
// at a time; a concurrent dispatcher won this issue.
var ErrAssignmentActive = errors.New("issue already carries an unfinished assignment")

// Admission refusals from the atomic dispatch gate. The packet records
// first and the limits are rechecked inside the same transaction, so a
// refusal means a concurrent admission consumed the room this attempt
// planned against; the dispatcher waits instead of launching.
var (
	ErrDispatchControlStale = errors.New("queued issue control changed during dispatch")
	ErrCapacityFull         = errors.New("appliance runs at its limit")
	ErrRepositoryFull       = errors.New("repository runs at its limit")
	ErrAllowanceExhausted   = errors.New("sponsorship allowance is exhausted")
	ErrAdmissionChanged     = errors.New("admission grants changed during dispatch")
)

// RecordDispatchPacket checks the gate and stores one dispatch atomically:
// the exact current queued control, the assigned assignment, its held reservation, the
// recorded run and the display binding. Either the whole packet lands or
// nothing does, so a crash never leaves a reservation without its
// assignment or a run without its dispatch. A second unfinished
// assignment for the issue refuses with ErrAssignmentActive instead of
// dispatching twice.
// The control row is locked after the dispatch gate and before packet
// writes; readiness assessment takes the same row lock, so changed or
// missing outcome evidence refuses the whole transaction.
// Admission also locks the acceptance head shared with acceptance withdrawal;
// a replaced or withdrawn head refuses even before readiness is reassessed.
//
// The packet is also the atomic admission gate: the limit rows are
// locked, the packet records, and then appliance, repository,
// sponsorship and spend-cap limits are rechecked inside the same
// transaction. Concurrent passes serialize here, so only room that
// actually exists is admitted; the losers wait instead of launching.
func (s *Store) RecordDispatchPacket(ctx context.Context, expected factory.IssueControl, a factory.Assignment, r factory.Reservation, run factory.Run, view factory.RunView) error {
	return s.recordDispatchPacket(ctx, expected, a, r, run, view, nil)
}

// RecordNativeDispatchPacket is the native producer's explicit admission entry.
// Retained native observations cannot enter the appliance packet by accident.
func (s *Store) RecordNativeDispatchPacket(ctx context.Context, expected factory.IssueControl, a factory.Assignment, r factory.Reservation, run factory.Run, view factory.RunView, native execution.NativeLaunch) error {
	return s.recordDispatchPacket(ctx, expected, a, r, run, view, &native)
}

func (s *Store) recordDispatchPacket(ctx context.Context, expected factory.IssueControl, a factory.Assignment, r factory.Reservation, run factory.Run, view factory.RunView, native *execution.NativeLaunch) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = checkDispatchOpenTx(ctx, tx, a.Repository); err != nil {
		return err
	}
	if err = recordDispatchPacketTx(ctx, tx, expected, a, r, run, view, false, native); err != nil {
		return err
	}
	return tx.Commit()
}

func sameControlOutcome(left, right factory.IssueControl) bool {
	return left.Reason == right.Reason && slices.Equal(left.Blockers, right.Blockers) &&
		maps.Equal(left.EndpointHeads, right.EndpointHeads) && maps.Equal(left.Satisfied, right.Satisfied)
}

func checkDispatchPacketInputs(expected factory.IssueControl, a factory.Assignment, r factory.Reservation, run factory.Run, view factory.RunView) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if err := r.Validate(); err != nil {
		return err
	}
	if err := run.Validate(); err != nil {
		return err
	}
	if a.PlanningContext != nil && a.PlanningContext.IssueMaker != nil && run.Deadline.After(time.Unix(a.PlanningContext.IssueMaker.NotAfter, 0)) {
		return ErrAdmissionChanged
	}
	if err := view.Validate(); err != nil {
		return err
	}
	if expected.NativeRev != a.NativeRev {
		return ErrDispatchControlStale
	}
	return nil
}

func checkDispatchPacketBindings(a factory.Assignment, r factory.Reservation, run factory.Run, view factory.RunView) error {
	if a.Stage != factory.AssignmentAssigned || a.Outcome != "" || a.Result != nil {
		return errors.New("dispatch packet carries a fresh assignment")
	}
	if r.AssignmentID != a.ID || r.Repository != a.Repository || r.Connection != a.Connection || r.State != factory.ReservationHeld {
		return errors.New("dispatch packet reservation does not match its assignment")
	}
	return checkDispatchPacketRunView(run, view, a)
}

func checkDispatchPacketRunView(run factory.Run, view factory.RunView, a factory.Assignment) error {
	if run.ID != a.Run || run.ProjectID != a.ProjectID || run.Role != a.Role || run.InputSHA != a.SourceCommit || run.Outcome != "" || run.Reconciled {
		return errors.New("dispatch packet run does not match its assignment")
	}
	if view.RunID != run.ID || view.Repository != a.Repository || view.Issue != a.Issue || view.Attempt != a.ID {
		return errors.New("dispatch packet view does not match its assignment")
	}
	return nil
}

func writeDispatchPacketRowsTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, r factory.Reservation, run factory.Run, view factory.RunView) error {
	adata, err := json.Marshal(a)
	if err != nil {
		return err
	}
	rdata, err := json.Marshal(r)
	if err != nil {
		return err
	}
	rundata, err := json.Marshal(run)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		a.ID, a.Repository, a.Issue, a.Run, a.Stage, a.Revision, string(adata)); err != nil {
		return dispatchPacketError(err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_reservations(assignment,repository,connection,state,data) VALUES($1,$2,$3,$4,$5)`,
		r.AssignmentID, r.Repository, r.Connection, r.State, string(rdata)); err != nil {
		return fmt.Errorf("dispatch packet failed: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_runs(id,active,settled,data) VALUES($1,TRUE,FALSE,$2)`, run.ID, string(rundata)); err != nil {
		return fmt.Errorf("dispatch packet failed: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_run_views(run,repository,issue,attempt) VALUES($1,$2,$3,$4)`,
		view.RunID, view.Repository, view.Issue, view.Attempt); err != nil {
		return fmt.Errorf("dispatch packet failed: %w", err)
	}
	if a.PlanningContext != nil && a.PlanningContext.IssueMaker != nil {
		scope := a.PlanningContext.IssueMaker
		result, err := tx.ExecContext(ctx, `UPDATE factory_feed_posts p SET data=p.data || jsonb_build_object('assignment',$2::text,'run',$3::text)
		 FROM factory_feed_posts c WHERE p.id=$1 AND p.repository=$4 AND c.id=$5 AND c.revision=$6 AND NOT COALESCE((c.data->>'deleted')::boolean,false) AND NOT COALESCE((c.data->>'revoked')::boolean,false)`, scope.Post, a.ID, run.ID, a.Repository, scope.SourcePost, scope.Revision)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count != 1 {
			return ErrStaleRevision
		}
		return nil
	}
	_, author := RecordAuthor(a.Phase, a.Role)
	if author == "" {
		author = a.Role
	}
	return recordFactoryFeedEventTx(ctx, tx, a.Repository, a.Issue, a.ID, a.ID, "dispatch:assigned", factory.FeedCommunication{
		ComponentID: "F02", Title: author + " assigned", Summary: "The worker assignment and its run are recorded. Startup is next.",
		Items: []factory.FeedCommunicationItem{{Label: author, Detail: a.Phase, State: "assigned"}},
	}, a.CreatedUnix)
}

func fundDispatchPacketAttemptTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, grants admissionGrants, run factory.Run, now time.Time, freshAttempt bool) error {
	if a.ExternalPlanning!=nil {
		return insertAttemptAllowanceTx(ctx,tx,a.Repository,a.Issue,a.ID,a.ExternalPlanning.Request.Limits,run.Deadline,now)
	}
	if freshAttempt {
		return startFreshAttemptAllowanceTx(ctx, tx, a.Repository, a.Issue, a.AttemptRoot, grants.policy.AttemptLimits, run.Deadline, now)
	}
	// Only the correction phase spends a correction cycle; advanced
	// implementations retain the root unconsumed.
	if a.PublicationAssignment != a.ID && a.Role == project.RoleCoder &&
		a.Phase == project.ExecutionCorrect {
		if err := consumeCorrectionAttemptTx(ctx, tx, a, now); err != nil {
			return err
		}
	}
	if a.Issue == 0 {
		// Issue-zero roots take no issue allowance. Time-only
		// funding follows the recorded subject: reassessment roots
		// fund against their verified completion, intent roots
		// against their persisted revision. The packet rows above
		// are already recorded, so each constructor binds the exact
		// recorded root, and replays conflict on the recorded row
		// rather than refilling it.
		switch a.Subject.Kind {
		case factory.SubjectCompletion:
			return insertReassessmentAttemptAllowanceTx(ctx, tx, a, grants.policy.ReassessmentMinutes, run.Deadline, now)
		case factory.SubjectIntent:
			return insertIntentAttemptAllowanceTx(ctx, tx, a, grants.policy.IntentMinutes, run.Deadline, now)
		case factory.SubjectDelivery:
			return insertDeliveryAttemptAllowanceTx(ctx, tx, a, grants.policy.AttemptLimits.ActiveMinutes, run.Deadline, now)
		case factory.SubjectStall:
			return insertStallAttemptAllowanceTx(ctx, tx, a, run.Deadline, now)
		case factory.SubjectDeliveryRepair:
			return insertDeliveryRepairAttemptAllowanceTx(ctx, tx, a, run.Deadline, now)
		default:
			return fmt.Errorf("%w: issue-zero root carries no fundable subject", ErrAttemptRootMismatch)
		}
	}
	return admitAttemptAllowanceTx(ctx, tx, a.Repository, a.Issue, a.AttemptRoot, &grants.policy.AttemptLimits, run.Deadline, now)
}

func recordDispatchPacketTx(ctx context.Context, tx *sql.Tx, expected factory.IssueControl, a factory.Assignment, r factory.Reservation, run factory.Run, view factory.RunView, freshAttempt bool, native *execution.NativeLaunch) error {
	if err := checkDispatchPacketInputs(expected, a, r, run, view); err != nil {
		return err
	}
	// D13 completion fence (FW31): the gate row above is locked, so this
	// read observes every committed completion. A pending event refuses
	// fresh attempts, explicit retries and advanced children alike; an
	// earlier priority check alone is insufficient. Reassessment roots
	// are exempt: the guard below binds the exact pending event and open
	// cycle they answer, and fencing them would deadlock the cycle they
	// are admitted to work. Independent delivery verification also needs
	// the integrated work while that cycle remains held. Its owning guard
	// below binds the current accepted intent, delivered tasks, source and
	// ready reviewer preparation; it grants no new issue implementation.
	deliveryVerification := a.Subject.Kind == factory.SubjectDelivery && a.Subject.Delivery != nil &&
		a.Phase == project.ExecutionVerify && a.Role == project.RoleReviewer && a.Issue == 0
	deliveryRepair := a.Subject.Kind == factory.SubjectDeliveryRepair && a.Subject.DeliveryRepair != nil && a.Phase == project.ExecutionRepair && a.Role == project.RoleCoder && a.Issue == 0
	if a.Phase != project.ExecutionReassess && !deliveryVerification && !deliveryRepair {
		if err := checkCompletionFenceTx(ctx, tx, a.Repository); err != nil {
			return err
		}
	}
	if a.Issue == 0 {
		// Issue-zero roots carry no issue control row: the guard binds
		// the verified completion and staged cycle as the outcome
		// evidence instead. Any other issue-zero subject stays refused
		// at allowance admission below.
	} else if err := checkQueuedControlTx(ctx, tx, expected, a.Repository, a.Issue, a.Acceptance); err != nil {
		return err
	}
	if err := checkDispatchPacketBindings(a, r, run, view); err != nil {
		return err
	}
	if err := validateAttemptPacketTx(ctx, tx, a, freshAttempt); err != nil {
		return err
	}
	if err := checkVerificationManifestPacketTx(ctx, tx, a, run); err != nil {
		return err
	}
	grants, err := loadAdmissionGrantsTx(ctx, tx, a.Repository, a.Connection)
	if err != nil {
		return err
	}
	if err = checkAssignmentAuthorityTx(ctx, tx, a.Authority, a, grants); err != nil {
		return err
	}
	if err = checkImplementationPlanReviewTx(ctx, tx, a); err != nil {
		return err
	}
	if native != nil {
		run, err = admitNativeDispatchTx(ctx, tx, a, run, grants, *native)
		if err != nil {
			return err
		}
	} else {
		if run.Native != nil || a.NativeTool != nil || grants.environment.Native != nil {
			return ErrAdmissionChanged
		}
		if err = checkApplianceRunPreparationTx(ctx, tx, a, a.AttemptRoot != a.ID); err != nil {
			return err
		}
		tool, err := factory.AdoptedToolIdentity(a, run.Image)
		if err != nil {
			return err
		}
		run.Admission = &factory.RunAdmission{Authority: a.Authority, Policy: grants.policy, Profile: grants.environment.Profile, Tool: &tool}
	}
	if run.Admission.Policy.Execution != nil {
		captured, err := factory.ControllerAdmissionDigest(a, run)
		if err != nil {
			return err
		}
		run.Admission.ControllerManifestDigest = captured
	}
	if err = checkCoderPreparationCustodyTx(ctx, tx, a); err != nil {
		return err
	}
	if err = run.Validate(); err != nil {
		return err
	}
	if err=recordExternalPlanningCommandTx(ctx,tx,a,time.Now()); err!=nil { return err }
	if err = writeDispatchPacketRowsTx(ctx, tx, a, r, run, view); err != nil {
		return err
	}
	if err = fundDispatchPacketAttemptTx(ctx, tx, a, grants, run, time.Now(), freshAttempt); err != nil {
		return err
	}
	return checkAdmissionLimitsTx(ctx, tx, a.Repository, a.Connection, a.ProjectID, grants)
}

// validateAttemptPacketTx checks whether this packet owns a publication or
// is a reviewer/correction child of the currently published candidate.
// Owner replans retain their prior allowance root while starting a new
// publication; children retain both parent publication and allowance roots.
func validateOwnerAttemptRootTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, freshAttempt bool) error {
	if freshAttempt {
		if a.AttemptRoot != a.ID || a.Role != project.RoleCoder {
			return errors.New("fresh allowance requires a self-owned coder assignment")
		}
	}
	if a.Role != project.RoleCoder {
		return errors.New("publication owner assignment must be a coder")
	}
	// Only investigation opens an attempt root: intent roots
	// arrive through FW08 and reassessment roots through FW30.
	if a.Phase != project.ExecutionInvestigate {
		return errors.New("only investigation opens a phased attempt root")
	}
	if a.AttemptRoot != a.ID {
		return childAllowanceRootExistsTx(ctx, tx, a)
	}
	return nil
}

func validateAttemptPacketTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, freshAttempt bool) error {
	owner := a.PublicationAssignment == a.ID
	if owner {
		return validateOwnerAttemptRootTx(ctx, tx, a, freshAttempt)
	}
	if freshAttempt {
		return errors.New("fresh allowance requires a self-owned coder assignment")
	}
	// Children enter through their phase's owning guard. An advanced
	// implementation binds its finished investigation parent without a
	// publication or PR; a reviewer binds its publication owner, the
	// current candidate and its review cycle; a corrector binds the
	// current reviewer, its correction cycle and the retained owner slot.
	switch a.Phase {
	case project.ExecutionDecompose:
		return validateDecomposeRootTx(ctx, tx, a)
	case project.ExecutionImplement:
		return validateImplementChildTx(ctx, tx, a)
	case project.ExecutionReassess:
		return validateReassessRootTx(ctx, tx, a)
	case project.ExecutionVerify:
		return validateDeliveryRootTx(ctx, tx, a)
	case project.ExecutionRepair:
		if a.Subject.Kind == factory.SubjectDeliveryRepair {
			return validateDeliveryRepairRootTx(ctx, tx, a)
		}
		return validateStallRootTx(ctx, tx, a)
	case project.ExecutionReview:
		return validateReviewChildTx(ctx, tx, a)
	case project.ExecutionCorrect:
		return validateCorrectChildTx(ctx, tx, a)
	default:
		return errors.New("child assignment carries an unadmitted execution phase")
	}
}

// validateDecomposeRootTx admits one intent decompose fresh root against
// its persisted intent revision. The root carries no parent, publication
// slot or issue authority; it plans the recorded draft but implements
// nothing. The revision must already be recorded as the current head for
// this intent and owner in this repository — intent persists before any
// host call — so an unrecorded, superseded or foreign revision refuses.
// The subject's scope digest must equal the persisted revision's digest:
// caller-supplied snapshot variation cannot redefine the funded
// operation. This read is unlocked; the duplicate-refusal revalidates
// the head under its lock before funding.
func validateDecomposeRootTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) error {
	if allowed, err := issueMakerConsentTx(ctx, tx, a); err != nil {
		return err
	} else if !allowed {
		return ErrStaleRevision
	}
	if a.Phase != project.ExecutionDecompose {
		return errors.New("intent root needs its decompose phase")
	}
	if a.Role != project.RoleCoder || a.ParentAssignment != "" || a.AttemptRoot != a.ID || a.PublicationAssignment != "" {
		return errors.New("intent planning is a fresh attempt root")
	}
	if !factory.ValidPhaseRole(a.Phase, a.Role) || !factory.SubjectKindAllowed(a.Subject.Kind, a.Phase) {
		return errors.New("intent root does not match its execution phase")
	}
	if a.Subject.Kind != factory.SubjectIntent || a.Subject.Intent == nil {
		return errors.New("intent root does not match its intent subject")
	}
	if a.Issue != 0 || a.Acceptance != "" {
		return errors.New("intent assignment carries issue authority")
	}
	var head string
	var owner int64
	var revisionData []byte
	if err := tx.QueryRowContext(ctx, `SELECT h.revision, h.owner_id, r.data FROM factory_intent_heads h JOIN factory_intent_revisions r ON r.id=h.revision WHERE h.id=$1`, a.Subject.Intent.IntentID).Scan(&head, &owner, &revisionData); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: intent revision is not recorded", ErrAttemptRootMismatch)
		}
		return err
	}
	if owner != a.Subject.Intent.OwnerID || head != a.Subject.Intent.RevisionID {
		return fmt.Errorf("%w: intent root answers a superseded or foreign revision", ErrAttemptRootMismatch)
	}
	var revision factory.IntentRevision
	if err := json.Unmarshal(revisionData, &revision); err != nil {
		return err
	}
	if err := revision.Validate(); err != nil {
		return err
	}
	if revision.Repository != a.Repository {
		return fmt.Errorf("%w: intent root answers another repository revision", ErrAttemptRootMismatch)
	}
	if a.Subject.Intent.ScopeDigest != revision.Digest() {
		return fmt.Errorf("%w: intent subject does not match its recorded revision", ErrAttemptRootMismatch)
	}
	return nil
}

// validateImplementChildTx admits one advanced implementation child against
// its finished investigation parent. The parent must be a reported
// completed investigation carrying an executable plan; the child must keep
// the parent's subject, root, publication slot, preparation and source and
// carry the deterministic advancement identity. No publication, PR or
// allowance change is involved: the admission reuses the retained root.
func validateImplementChildTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) error {
	if a.Phase != project.ExecutionImplement {
		return errors.New("phased review and correction enter through their owning boundaries")
	}
	if a.Role != project.RoleCoder || a.ParentAssignment == "" {
		return errors.New("implementation child needs its coder role and parent")
	}
	if !factory.ValidPhaseRole(a.Phase, a.Role) || !factory.SubjectKindAllowed(a.Subject.Kind, a.Phase) {
		return errors.New("implementation child does not match its execution phase")
	}
	parent, err := loadValidatedAssignmentTx(ctx, tx, a.ParentAssignment)
	if err != nil {
		return err
	}
	if err := checkInvestigationParentComplete(parent, a); err != nil {
		return err
	}
	if err := checkImplementationLineage(parent, a); err != nil {
		return err
	}
	childID, err := factory.AdvancementID(parent.ID, factory.ResultDigest(*parent.Result), project.ExecutionImplement)
	if err != nil {
		return fmt.Errorf("%w: implementation child is not its parent's advancement", ErrAttemptRootMismatch)
	}
	if childID != a.ID {
		if err := validateImplementationRetryChildTx(ctx, tx, a); err != nil {
			return err
		}
	}
	if err := currentHeadMatchesTx(ctx, tx, a.Repository, a.Issue, a.Acceptance); err != nil {
		return err
	}
	return childAllowanceRootExistsTx(ctx, tx, a)
}

func checkInvestigationParentComplete(parent, a factory.Assignment) error {
	if parent.ID != a.ParentAssignment || parent.Phase != project.ExecutionInvestigate ||
		!parent.SucceededReported() ||
		parent.Result == nil || parent.Result.PhaseResult == nil {
		return fmt.Errorf("%w: implementation parent is not a completed investigation", ErrAttemptRootMismatch)
	}
	phased := parent.Result.PhaseResult
	if phased.Phase != project.ExecutionInvestigate || phased.Status != "completed" ||
		phased.Output == nil || phased.Output.Plan == nil ||
		phased.Output.Plan.Validate() != nil || phased.Output.Plan.Executable() != nil {
		return fmt.Errorf("%w: implementation parent lacks an executable plan", ErrAttemptRootMismatch)
	}
	return nil
}

func checkImplementationLineage(parent, a factory.Assignment) error {
	if parent.Repository != a.Repository || parent.Issue != a.Issue || parent.Acceptance != a.Acceptance ||
		parent.ProjectID != a.ProjectID || parent.AttemptRoot != a.AttemptRoot ||
		parent.PublicationAssignment != a.PublicationAssignment || parent.Preparation != a.Preparation ||
		parent.SourceCommit != a.SourceCommit ||
		parent.Subject.Kind != a.Subject.Kind ||
		parent.Subject.SubjectKey(parent.Repository) != a.Subject.SubjectKey(a.Repository) {
		return fmt.Errorf("%w: implementation child does not match its investigation parent", ErrAttemptRootMismatch)
	}
	return nil
}

func loadValidatedAssignmentTx(ctx context.Context, tx *sql.Tx, id string) (factory.Assignment, error) {
	var data []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1 FOR UPDATE`, id).Scan(&data); err != nil {
		return factory.Assignment{}, err
	}
	var stored factory.Assignment
	if err := json.Unmarshal(data, &stored); err != nil {
		return factory.Assignment{}, err
	}
	if err := stored.Validate(); err != nil {
		return factory.Assignment{}, err
	}
	return stored, nil
}

func checkReviewChildOwner(parent, a factory.Assignment) error {
	if parent.ID != a.ParentAssignment || parent.PublicationAssignment != parent.ID ||
		parent.Role != project.RoleCoder || !parent.PublicationOwnerReady() ||
		parent.Repository != a.Repository || parent.Issue != a.Issue ||
		parent.ProjectID != a.ProjectID || parent.Acceptance != a.Acceptance ||
		parent.AttemptRoot != a.AttemptRoot || a.PublicationAssignment != parent.ID {
		return fmt.Errorf("%w: review child does not match its publication owner", ErrAttemptRootMismatch)
	}
	return nil
}

func checkReviewChildPublication(publication factory.Publication, parent factory.Assignment, a factory.Assignment) error {
	if publication.AssignmentID != parent.ID || publication.Repository != a.Repository ||
		publication.Issue != a.Issue || publication.Acceptance != a.Acceptance ||
		publication.Stage != factory.PublicationPublished || publication.WithdrawRequested ||
		publication.PRNumber <= 0 || publication.PRID <= 0 || publication.Candidate != a.SourceCommit {
		return fmt.Errorf("%w: review child does not match the current published candidate", ErrAttemptRootMismatch)
	}
	return nil
}

func reviewCycleBound(publication factory.Publication, a factory.Assignment) bool {
	for _, cycle := range publication.ReviewCycles {
		if cycle.ReviewerAssignment == a.ID && cycle.PublicationID == publication.ID &&
			cycle.AttemptRoot == a.AttemptRoot && cycle.Head == a.SourceCommit &&
			cycle.BaseOID == publication.PullTarget().BaseOID {
			return true
		}
	}
	return false
}

func correctionCycleBound(publication factory.Publication, a factory.Assignment) bool {
	for _, cycle := range publication.ReviewCycles {
		if cycle.CorrectionAssignment == a.ID && cycle.PublicationID == publication.ID &&
			cycle.AttemptRoot == a.AttemptRoot && cycle.Head == a.SourceCommit &&
			cycle.BaseOID == publication.PullTarget().BaseOID {
			return true
		}
	}
	return false
}

// validateReviewChildTx admits one phased reviewer child against its
// publication owner and review cycle. The child keeps the owner's attempt
// root, publication slot, acceptance and candidate, runs under the
// reviewer account on a distinct preparation, and answers exactly the
// cycle that names it. No allowance is consumed beyond the review round
// charged before the host call; the admission only retains the root.
func validateReviewChildTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) error {
	if a.Role != project.RoleReviewer || a.ParentAssignment == "" {
		return errors.New("review child needs its reviewer role and publication parent")
	}
	if !factory.ValidPhaseRole(a.Phase, a.Role) || !factory.SubjectKindAllowed(a.Subject.Kind, a.Phase) {
		return errors.New("review child does not match its execution phase")
	}
	if a.Subject.Kind != factory.SubjectIssue || a.Subject.Issue == nil ||
		a.Subject.Issue.Issue != a.Issue || a.Subject.Issue.Acceptance != a.Acceptance {
		return errors.New("review child does not match its issue subject")
	}
	parent, err := loadValidatedAssignmentTx(ctx, tx, a.ParentAssignment)
	if err != nil {
		return err
	}
	if err := checkReviewChildOwner(parent, a); err != nil {
		return err
	}
	publication, err := lockedChildPublicationTx(ctx, tx, parent.ID)
	if err != nil {
		return err
	}
	if err := checkReviewChildPublication(publication, parent, a); err != nil {
		return err
	}
	if err := currentHeadMatchesTx(ctx, tx, a.Repository, a.Issue, a.Acceptance); err != nil {
		return err
	}
	if !reviewCycleBound(publication, a) {
		return fmt.Errorf("%w: review child answers an unrecorded cycle", ErrAttemptRootMismatch)
	}
	if a.Preparation == parent.Preparation {
		return errors.New("reviewer child requires a distinct preparation")
	}
	return childAllowanceRootExistsTx(ctx, tx, a)
}

// validateReassessRootTx admits one completion reassessment fresh root
// against its verified completion event and staged reassessment cycle.
// The root carries no parent, publication slot or issue authority; it
// reviews the sealed inventory but implements nothing. Both the event
// and the cycle must already be recorded in this repository — a
// reassessment answers a verified completion, never an unrecorded one.
func validateReassessRootTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) error {
	if a.Phase != project.ExecutionReassess {
		return errors.New("reassessment root needs its reassess phase")
	}
	if a.Role != project.RoleCoder || a.ParentAssignment != "" || a.AttemptRoot != a.ID || a.PublicationAssignment != "" {
		return errors.New("reassessment cycle is a fresh attempt root")
	}
	if !factory.ValidPhaseRole(a.Phase, a.Role) || !factory.SubjectKindAllowed(a.Subject.Kind, a.Phase) {
		return errors.New("reassessment root does not match its execution phase")
	}
	if a.Subject.Kind != factory.SubjectCompletion || a.Subject.Completion == nil {
		return errors.New("reassessment root does not match its completion subject")
	}
	if a.Issue != 0 || a.Acceptance != "" {
		return errors.New("completion assignment carries issue authority")
	}
	event, err := loadReassessmentEventTx(ctx, tx, a)
	if err != nil {
		return err
	}
	// The exact cycle row is locked for the packet duration, following
	// the reassessment CAS lock ordering (state,data FOR UPDATE by
	// cycle): concurrent admissions serialize on the row instead of
	// racing the seal. The locked record must carry the reassess
	// decision in its deciding state and bind this root's exact
	// sealed inventory and source commit: an unsealed or collecting
	// cycle, a mismatched digest or source, or any other
	// decision/state refuses with no packet or charge rows. Released
	// and superseded cycles no longer take work.
	record, err := loadReassessmentCycleTx(ctx, tx, a, event)
	if err != nil {
		return err
	}
	if err := checkReassessmentCycleSealed(record, a); err != nil {
		return err
	}
	// The sole-run ledger and transition commit with the packet and its
	// allowance. A refused packet leaves the cycle deciding; concurrent
	// admissions cannot record a second run, even after the first settles.
	ledger, err := record.Cycle.Ledger.AdmitCoderRun(a.Run, record.Cycle.Decision)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAttemptRootMismatch, err)
	}
	record.Cycle.Ledger = ledger
	if err := record.Cycle.AdvanceTo(factory.ReassessmentReassessing); err != nil {
		return err
	}
	record.Cycle.State = factory.ReassessmentReassessing
	record.WaitReason = ""
	if err := factory.AdmitReassessmentCoder(record.Cycle, a, record.Cycle.Decision); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE factory_reassessments SET state=$1,data=$2 WHERE cycle=$3`,
		record.Cycle.State, string(data), record.Cycle.CycleID)
	return err
}

func loadReassessmentEventTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) (factory.CompletionEvent, error) {
	var eventData []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_completions WHERE repository=$1 AND event=$2`, a.Repository, a.Subject.Completion.CompletionID).Scan(&eventData); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return factory.CompletionEvent{}, fmt.Errorf("%w: reassessment answers an unrecorded completion", ErrAttemptRootMismatch)
		}
		return factory.CompletionEvent{}, err
	}
	var event factory.CompletionEvent
	if err := json.Unmarshal(eventData, &event); err != nil {
		return factory.CompletionEvent{}, err
	}
	if err := event.Validate(); err != nil {
		return factory.CompletionEvent{}, err
	}
	if event.ID != a.Subject.Completion.CompletionID || event.Repository != a.Repository {
		return factory.CompletionEvent{}, fmt.Errorf("%w: reassessment answers another repository completion", ErrAttemptRootMismatch)
	}
	return event, nil
}

func loadReassessmentCycleTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, event factory.CompletionEvent) (ReassessmentRecord, error) {
	var cycleState string
	var cycleData []byte
	if err := tx.QueryRowContext(ctx, `SELECT state,data FROM factory_reassessments WHERE repository=$1 AND event=$2 AND cycle=$3 FOR UPDATE`, a.Repository, event.ID, a.Subject.Completion.CycleID).Scan(&cycleState, &cycleData); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReassessmentRecord{}, fmt.Errorf("%w: reassessment answers an unstaged cycle", ErrAttemptRootMismatch)
		}
		return ReassessmentRecord{}, err
	}
	var record ReassessmentRecord
	if err := json.Unmarshal(cycleData, &record); err != nil {
		return ReassessmentRecord{}, err
	}
	if err := record.Validate(); err != nil {
		return ReassessmentRecord{}, err
	}
	if len(record.Cycle.Ledger.CoderRuns) != 0 {
		if err := refuseDuplicateReassessmentAttemptTx(ctx, tx, a.Repository, a.ID, *a.Subject.Completion); err != nil {
			return ReassessmentRecord{}, err
		}
	}
	if cycleState != factory.ReassessmentDeciding || record.Cycle.State != factory.ReassessmentDeciding ||
		record.Cycle.Decision != factory.ReassessmentOptionReassess || len(record.Cycle.Ledger.CoderRuns) != 0 {
		return ReassessmentRecord{}, fmt.Errorf("%w: reassessment answers a cycle without its reassess decision", ErrAttemptRootMismatch)
	}
	if record.Cycle.EventID != event.ID || record.Cycle.CycleID != a.Subject.Completion.CycleID || record.Cycle.Repository != a.Repository {
		return ReassessmentRecord{}, fmt.Errorf("%w: reassessment answers another cycle", ErrAttemptRootMismatch)
	}
	return record, nil
}

func checkReassessmentCycleSealed(record ReassessmentRecord, a factory.Assignment) error {
	if !record.Local.Sealed() || record.InventoryDigest == "" {
		return fmt.Errorf("%w: reassessment answers an unsealed cycle", ErrAttemptRootMismatch)
	}
	if record.InventoryDigest != a.Subject.Completion.InventoryDigest {
		return fmt.Errorf("%w: reassessment answers another sealed inventory", ErrAttemptRootMismatch)
	}
	if record.Local.SourceCommit != a.SourceCommit {
		return fmt.Errorf("%w: reassessment runs a head its cycle was not sealed against", ErrAttemptRootMismatch)
	}
	return nil
}

// validateCorrectChildTx admits one phased corrector child against the
// current reviewer, its correction cycle and the retained owner slot. The
// parent must have authored the newest committed verdict on the exact
// current head and base: a stale reviewer from a superseded round refuses,
// as does a correction without a submitted verdict. The child keeps the
// owner's coder preparation (distinct from the reviewer session), root,
// publication slot and candidate; the correction cycle it consumes is
// charged by the packet's allowance leg.
func checkCorrectionReviewerIdentity(parent, a factory.Assignment) error {
	if res, ok := parent.Report(); parent.ID != a.ParentAssignment || parent.Role != project.RoleReviewer ||
		parent.Phase != project.ExecutionReview || !ok ||
		res.Status != "completed" {
		return fmt.Errorf("%w: correction parent is not its current reviewer", ErrAttemptRootMismatch)
	}
	return nil
}

func checkCorrectionReviewerScope(parent, a factory.Assignment) error {
	if parent.Repository != a.Repository || parent.Issue != a.Issue ||
		parent.ProjectID != a.ProjectID || parent.Acceptance != a.Acceptance ||
		parent.ActorID != a.ActorID ||
		parent.SourceCommit != a.SourceCommit {
		return fmt.Errorf("%w: correction parent is not its current reviewer", ErrAttemptRootMismatch)
	}
	return nil
}

func checkCorrectionVerdict(parent, a factory.Assignment) error {
	verdict := parent.Result.PhaseResult
	if verdict == nil || verdict.Phase != project.ExecutionReview || verdict.Output == nil ||
		(verdict.Output.Review == nil) == (verdict.Output.Clear == nil) {
		return fmt.Errorf("%w: correction parent carries no single typed verdict", ErrAttemptRootMismatch)
	}
	if verdict.Output.Clear != nil && verdict.Output.Clear.Head != a.SourceCommit {
		return fmt.Errorf("%w: correction parent finished on another candidate", ErrAttemptRootMismatch)
	}
	return nil
}

func checkCorrectionReviewer(parent, a factory.Assignment) error {
	if err := checkCorrectionReviewerIdentity(parent, a); err != nil {
		return err
	}
	if err := checkCorrectionReviewerScope(parent, a); err != nil {
		return err
	}
	return checkCorrectionVerdict(parent, a)
}

func checkCorrectionOwner(owner, a factory.Assignment) error {
	if owner.ID != a.PublicationAssignment || owner.PublicationAssignment != owner.ID ||
		owner.Role != project.RoleCoder || !owner.PublicationOwnerReady() ||
		owner.Repository != a.Repository || owner.Issue != a.Issue ||
		owner.ProjectID != a.ProjectID || owner.Acceptance != a.Acceptance ||
		owner.AttemptRoot != a.AttemptRoot {
		return fmt.Errorf("%w: correction child does not match its publication owner", ErrAttemptRootMismatch)
	}
	return nil
}

func checkCorrectionPublication(publication factory.Publication, owner, a factory.Assignment) error {
	if publication.AssignmentID != owner.ID || publication.Repository != a.Repository ||
		publication.Issue != a.Issue || publication.Acceptance != a.Acceptance ||
		publication.Stage != factory.PublicationPublished || publication.WithdrawRequested ||
		publication.PRNumber <= 0 || publication.PRID <= 0 || publication.Candidate != a.SourceCommit {
		return fmt.Errorf("%w: correction child does not match the current published candidate", ErrAttemptRootMismatch)
	}
	return nil
}

func admitCorrectionLineageTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) (parent, owner factory.Assignment, publication factory.Publication, err error) {
	if parent, err = loadValidatedAssignmentTx(ctx, tx, a.ParentAssignment); err != nil {
		return factory.Assignment{}, factory.Assignment{}, factory.Publication{}, err
	}
	if err = checkCorrectionReviewer(parent, a); err != nil {
		return factory.Assignment{}, factory.Assignment{}, factory.Publication{}, err
	}
	if owner, err = loadValidatedAssignmentTx(ctx, tx, a.PublicationAssignment); err != nil {
		return factory.Assignment{}, factory.Assignment{}, factory.Publication{}, err
	}
	if err = checkCorrectionOwner(owner, a); err != nil {
		return factory.Assignment{}, factory.Assignment{}, factory.Publication{}, err
	}
	if publication, err = lockedChildPublicationTx(ctx, tx, owner.ID); err != nil {
		return factory.Assignment{}, factory.Assignment{}, factory.Publication{}, err
	}
	if err = checkCorrectionPublication(publication, owner, a); err != nil {
		return factory.Assignment{}, factory.Assignment{}, factory.Publication{}, err
	}
	if err = checkCorrectionReviewCurrencyTx(ctx, tx, publication, parent); err != nil {
		return factory.Assignment{}, factory.Assignment{}, factory.Publication{}, err
	}
	return parent, owner, publication, nil
}

// Only the finished explicit funding chain can supply an older reviewer.
// Its report and submitted operation remain owned by their original root.
func checkCorrectionReviewCurrencyTx(ctx context.Context, tx *sql.Tx, publication factory.Publication, parent factory.Assignment) error {
	source, op, err := currentPublicationReviewTx(ctx, tx, publication)
	if err != nil {
		return err
	}
	if op.RunID != parent.Run || parent.AttemptRoot != source.AssignmentID || parent.PublicationAssignment != source.AssignmentID {
		return fmt.Errorf("%w: correction answers a superseded review", ErrAttemptRootMismatch)
	}
	return nil
}

func checkCorrectionCurrency(publication factory.Publication, parent, owner, a factory.Assignment) error {
	if !correctionCycleBound(publication, a) {
		return fmt.Errorf("%w: correction child answers an unrecorded cycle", ErrAttemptRootMismatch)
	}
	if !a.PublicationCoderPreparation(owner) {
		return errors.New("correction must retain the publication coder preparation")
	}
	if a.Preparation == parent.Preparation {
		return errors.New("corrector session requires a preparation distinct from its reviewer")
	}
	return nil
}

func validateCorrectChildTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) error {
	if a.Role != project.RoleCoder || a.ParentAssignment == "" {
		return errors.New("correction child needs its coder role and current reviewer")
	}
	if !factory.ValidPhaseRole(a.Phase, a.Role) || !factory.SubjectKindAllowed(a.Subject.Kind, a.Phase) {
		return errors.New("correction child does not match its execution phase")
	}
	if a.Subject.Kind != factory.SubjectIssue || a.Subject.Issue == nil ||
		a.Subject.Issue.Issue != a.Issue || a.Subject.Issue.Acceptance != a.Acceptance {
		return errors.New("correction child does not match its issue subject")
	}
	parent, owner, publication, err := admitCorrectionLineageTx(ctx, tx, a)
	if err != nil {
		return err
	}
	if err := currentHeadMatchesTx(ctx, tx, a.Repository, a.Issue, a.Acceptance); err != nil {
		return err
	}
	if err := checkCorrectionCurrency(publication, parent, owner, a); err != nil {
		return err
	}
	return childAllowanceRootExistsTx(ctx, tx, a)
}

// lockedChildPublicationTx loads and validates the publication owned by
// assignmentID, holding its row for the admitting transaction.
func lockedChildPublicationTx(ctx context.Context, tx *sql.Tx, assignmentID string) (factory.Publication, error) {
	var publicationData []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE assignment=$1 FOR UPDATE`, assignmentID).Scan(&publicationData); err != nil {
		return factory.Publication{}, err
	}
	var publication factory.Publication
	if err := json.Unmarshal(publicationData, &publication); err != nil {
		return factory.Publication{}, err
	}
	if err := publication.Validate(); err != nil {
		return factory.Publication{}, err
	}
	return publication, nil
}

// currentHeadMatchesTx refuses admissions on a replaced acceptance head.
func currentHeadMatchesTx(ctx context.Context, tx *sql.Tx, repository, issue int64, acceptance string) error {
	var head string
	if err := tx.QueryRowContext(ctx, `SELECT decision FROM issue_acceptance_heads WHERE repository=$1 AND issue=$2 FOR UPDATE`, repository, issue).Scan(&head); err != nil {
		return err
	}
	if head != acceptance {
		return ErrAdmissionChanged
	}
	return nil
}

// childAllowanceRootExistsTx requires the retained attempt root behind one
// publication child admission.
func childAllowanceRootExistsTx(ctx context.Context, tx *sql.Tx, a factory.Assignment) error {
	var rootExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM factory_attempt_allowances WHERE repository=$1 AND issue=$2 AND root_assignment=$3)`, a.Repository, a.Issue, a.AttemptRoot).Scan(&rootExists); err != nil {
		return err
	}
	if !rootExists {
		return ErrAttemptRootMismatch
	}
	return nil
}

func checkExpectedControlQueued(expected factory.IssueControl, repository, issue int64, acceptance string) error {
	if err := expected.Validate(); err != nil || expected.Readiness != factory.ReadinessQueued || expected.Repository != repository || expected.Issue != issue || expected.Acceptance != acceptance {
		return ErrDispatchControlStale
	}
	return nil
}

func loadLockedControlTx(ctx context.Context, tx *sql.Tx, repository, issue int64) (factory.IssueControl, error) {
	var controlData []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM issue_controls WHERE repository=$1 AND issue=$2 FOR UPDATE`, repository, issue).Scan(&controlData); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return factory.IssueControl{}, ErrDispatchControlStale
		}
		return factory.IssueControl{}, err
	}
	var current factory.IssueControl
	if err := json.Unmarshal(controlData, &current); err != nil {
		return factory.IssueControl{}, err
	}
	return current, nil
}

func checkCurrentControlQueued(current, expected factory.IssueControl, repository, issue int64, acceptance string) error {
	if err := current.Validate(); err != nil || current.Readiness != factory.ReadinessQueued || current.Repository != repository || current.Issue != issue || current.Acceptance != acceptance || current.NativeRev != expected.NativeRev || current.Revision != expected.Revision || current.Fingerprint != expected.Fingerprint || current.Authority != expected.Authority || !sameControlOutcome(current, expected) {
		return ErrDispatchControlStale
	}
	return nil
}

func checkControlHeadCurrentTx(ctx context.Context, tx *sql.Tx, repository, issue int64, acceptance string) error {
	var head string
	if err := tx.QueryRowContext(ctx, `SELECT decision FROM issue_acceptance_heads WHERE repository=$1 AND issue=$2 FOR UPDATE`, repository, issue).Scan(&head); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDispatchControlStale
		}
		return err
	}
	if head != acceptance {
		return ErrDispatchControlStale
	}
	var withdrawn bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM issue_acceptance_withdrawals WHERE repository=$1 AND issue=$2 AND decision=$3)`, repository, issue, acceptance).Scan(&withdrawn); err != nil {
		return err
	}
	if withdrawn {
		return ErrDispatchControlStale
	}
	return nil
}

func checkQueuedControlTx(ctx context.Context, tx *sql.Tx, expected factory.IssueControl, repository, issue int64, acceptance string) error {
	if err := checkExpectedControlQueued(expected, repository, issue, acceptance); err != nil {
		return err
	}
	current, err := loadLockedControlTx(ctx, tx, repository, issue)
	if err != nil {
		return err
	}
	if err := checkCurrentControlQueued(current, expected, repository, issue, acceptance); err != nil {
		return err
	}
	return checkControlHeadCurrentTx(ctx, tx, repository, issue, acceptance)
}

type admissionGrants struct {
	capacity    factory.Capacity
	policy      factory.RepositoryPolicy
	operator    factory.OperatorGrant
	sponsorship factory.Sponsorship
	environment project.EnvironmentGrant
}

func loadGrantDocumentTx(ctx context.Context, t *sql.Tx, dest any, query string, args ...any) error {
	var data []byte
	if err := t.QueryRowContext(ctx, query, args...).Scan(&data); err != nil {
		return admissionChanged(err)
	}
	return json.Unmarshal(data, dest)
}

func selectAdmissionSponsorshipTx(ctx context.Context, t *sql.Tx, repository int64, connection string) (factory.Sponsorship, error) {
	rows, err := t.QueryContext(ctx, `SELECT connection,data FROM factory_sponsorships WHERE repository=$1 ORDER BY connection LIMIT 33 FOR UPDATE`, repository)
	if err != nil {
		return factory.Sponsorship{}, err
	}
	var selected factory.Sponsorship
	rowCount := 0
	found := false
	for rows.Next() {
		rowCount++
		var candidate factory.Sponsorship
		var candidateConnection string
		var data []byte
		if err = rows.Scan(&candidateConnection, &data); err != nil {
			_ = rows.Close()
			return factory.Sponsorship{}, err
		}
		if err = json.Unmarshal(data, &candidate); err != nil {
			_ = rows.Close()
			return factory.Sponsorship{}, err
		}
		if candidate.Connection != candidateConnection {
			_ = rows.Close()
			return factory.Sponsorship{}, ErrAdmissionChanged
		}
		if candidate.Active && candidate.Connection == connection {
			selected, found = candidate, true
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return factory.Sponsorship{}, err
	}
	if err = rows.Close(); err != nil {
		return factory.Sponsorship{}, err
	}
	if rowCount >= 33 || !found || selected.Connection != connection {
		return factory.Sponsorship{}, ErrAdmissionChanged
	}
	return selected, nil
}

func loadAdmissionGrantsTx(ctx context.Context, t *sql.Tx, repository int64, connection string) (admissionGrants, error) {
	var grants admissionGrants
	if err := loadGrantDocumentTx(ctx, t, &grants.capacity, `SELECT data FROM factory_capacity WHERE id=1 FOR UPDATE`); err != nil {
		return admissionGrants{}, err
	}
	if err := loadGrantDocumentTx(ctx, t, &grants.policy, `SELECT data FROM factory_policies WHERE repository=$1 FOR UPDATE`, repository); err != nil {
		return admissionGrants{}, err
	}
	if err := loadGrantDocumentTx(ctx, t, &grants.operator, `SELECT data FROM factory_operator_grants WHERE repository=$1 FOR UPDATE`, repository); err != nil {
		return admissionGrants{}, err
	}
	sponsorship, err := selectAdmissionSponsorshipTx(ctx, t, repository, connection)
	if err != nil {
		return admissionGrants{}, err
	}
	grants.sponsorship = sponsorship
	if err := loadGrantDocumentTx(ctx, t, &grants.environment, `SELECT data FROM project_environment_grants WHERE repository=$1 FOR UPDATE`, repository); err != nil {
		return admissionGrants{}, err
	}
	if grants.environment.Validate() != nil {
		return admissionGrants{}, ErrAdmissionChanged
	}
	return grants, nil
}

// Appliance packets cannot consume a native preparation's readiness. Lock an
// existing attempt root before its preparation, matching observation/stop order.
func checkApplianceRunPreparationTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, requireRoot bool) error {
	// The initial root may be created when this packet commits. An explicit
	// retry's setup can already have admitted that same root, so lock it when
	// present even for a self-owned packet. Children/retries require it.
	if _, err := lockAttemptAllowanceRootTx(ctx, tx, a.Repository, a.Issue, a.AttemptRoot); err != nil && (requireRoot || !errors.Is(err, sql.ErrNoRows)) {
		return err
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT data FROM project_preparations WHERE id=$1 FOR SHARE`, a.Preparation).Scan(&raw); err != nil {
		return admissionChanged(err)
	}
	record, meta, err := unmarshalCandidatePreparation(raw)
	if err != nil {
		return err
	}
	p := record.Preparation
	// Phase-specific packet guards bind the candidate/source. A corrector can
	// retain its owner's approved preparation while starting from a later head.
	if record.Native != nil || p.Project != a.ProjectID || p.Role != a.Role ||
		p.Requirements.ID != a.Authority.RequirementsID || p.Approval.ID != a.Authority.ApprovalID || !record.State.Ready || record.State.Stopped ||
		meta != nil && (meta.Stopped || meta.Retirement == "confirmed") {
		return ErrAdmissionChanged
	}
	return nil
}

// checkAssignmentAuthorityTx binds fresh authorization to the grant rows locked
// for this admission. A retry retains its original assignment history while
// checking the current role grant and the same approved preparation decisions.
func checkAssignmentAuthorityTx(ctx context.Context, t *sql.Tx, expected factory.AuthorityRef, a factory.Assignment, grants admissionGrants) error {
	if err := checkRetainedContinuationTx(ctx, t, a, grants); err != nil {
		return err
	}
	return checkAssignmentStandingAuthorityTx(ctx, t, expected, a, grants)
}

func checkAssignmentStandingAuthorityTx(ctx context.Context, t *sql.Tx, expected factory.AuthorityRef, a factory.Assignment, grants admissionGrants) error {
	if grants.policy.Roles[a.Role].Account != a.Connection {
		return ErrAdmissionChanged
	}
	if (grants.environment.Native == nil) != (grants.sponsorship.Native == nil) {
		return ErrAdmissionChanged
	}
	if account := grants.sponsorship.Native; account != nil && (account.Validate() != nil || account.Profile != grants.environment.Native.Profile) {
		return ErrAdmissionChanged
	}
	var requirement, approval string
	if err := t.QueryRowContext(ctx, `SELECT decision FROM project_requirement_heads WHERE project_id=$1 FOR UPDATE`, a.ProjectID).Scan(&requirement); err != nil {
		return admissionChanged(err)
	}
	if err := t.QueryRowContext(ctx, `SELECT decision FROM project_approval_heads WHERE project_id=$1 FOR UPDATE`, a.ProjectID).Scan(&approval); err != nil {
		return admissionChanged(err)
	}

	permitted := false
	for _, role := range grants.sponsorship.Roles {
		permitted = permitted || role == a.Role
	}
	if !grants.policy.Enabled || grants.policy.Paused || !grants.operator.Active ||
		!grants.sponsorship.Active || !permitted || !grants.environment.Active || grants.environment.Validate() != nil {
		return ErrAdmissionChanged
	}
	if grants.sponsorship.ActorID != a.ActorID || grants.sponsorship.ProjectID != a.ProjectID {
		return ErrAdmissionChanged
	}
	current := factory.AuthorityRef{
		Policy: grants.policy.Revision, Operator: grants.operator.Revision,
		Capacity: grants.capacity.Revision, Sponsorship: grants.sponsorship.Revision,
		SponsorshipConnection: grants.sponsorship.Connection,
		Environment:           grants.environment.Revision, RequirementsID: requirement, ApprovalID: approval,
	}
	if current != expected || requirement != a.Authority.RequirementsID || approval != a.Authority.ApprovalID {
		return ErrAdmissionChanged
	}
	return nil
}

// checkAdmissionLimitsTx enforces appliance, one-session-per-repository,
// sponsorship and spend-cap limits inside the admission transaction. The limit rows are
// locked first so concurrent admissions serialize here; the packet's own
// held reservation is already recorded, so every comparison accounts for
// it and refuses exactly when the pre-packet state plus this admission
// would exceed a limit. Count reads are capped just past each limit,
// which decides exact admission without scanning settled history.
func checkAdmissionLimitsTx(ctx context.Context, t *sql.Tx, repository int64, connection, projectID string, grants admissionGrants) error {
	heldTotal, err := cappedCountTx(ctx, t, `SELECT 1 FROM factory_reservations WHERE state='held'`, grants.capacity.MaxConcurrentRuns+1)
	if err != nil {
		return err
	}
	unattributed, err := cappedCountTx(ctx, t, `SELECT 1 FROM factory_runs WHERE active AND id NOT IN (SELECT run FROM factory_assignments WHERE run!='')`, grants.capacity.MaxConcurrentRuns+1)
	if err != nil {
		return err
	}
	if heldTotal+unattributed > grants.capacity.MaxConcurrentRuns {
		return ErrCapacityFull
	}
	// One executing factory session per repository is a product invariant,
	// even when the policy or operator has configured a larger concurrency
	// value. Count held reservations and every active attributed run: a run
	// whose reservation was consumed or released still owns its process slot.
	// A preparation transferred to its exact admitted child shares that child's
	// slot while its reservation/run is active. It still needs separate retirement.
	repoSessions, err := cappedCountTx(ctx, t, `
		SELECT 1 FROM factory_reservations
			WHERE state='held' AND repository=$1
		UNION ALL
		SELECT 1 FROM factory_runs r
			JOIN factory_assignments a ON a.run=r.id
			WHERE r.active AND a.repository=$1
			  AND NOT EXISTS (SELECT 1 FROM factory_reservations res
				WHERE res.assignment=a.id AND res.state='held')
		UNION ALL
		SELECT 1 FROM factory_runs r
			WHERE r.active
			  AND r.id NOT IN (SELECT run FROM factory_assignments WHERE run!='')
			  AND r.data->>'project_id'=$2
		UNION ALL
		SELECT 1 FROM project_preparations p
			WHERE p.project_id=$2 AND p.data ? 'factory_admission'
			  AND (p.data->'factory_admission'->>'repository')::bigint=$1
			  AND NOT soda_preparation_quiescent(p.data,p.id,p.project_id,p.role)
			  AND NOT EXISTS (
				SELECT 1 FROM factory_assignments a JOIN factory_runs r ON r.id=a.run
				WHERE (a.id=p.data#>>'{factory_admission,child_assignment}'
				  OR (a.data->>'parent_assignment'=p.data#>>'{factory_admission,child_assignment}'
				    AND a.data->>'phase'='implement')) AND a.repository=$1
				  AND a.data->>'project_id'=p.project_id AND a.data->>'role'=p.role
				  AND a.data->>'preparation'=p.id
				  AND a.data->>'attempt_root'=p.data#>>'{factory_admission,attempt_root}'
				  AND (r.active OR EXISTS (SELECT 1 FROM factory_reservations res
				    WHERE res.assignment=a.id AND res.state='held')))`, 2, repository, projectID)
	if err != nil {
		return err
	}
	if repoSessions > 1 {
		return ErrRepositoryFull
	}
	// Capacity counts issue sessions, not historical accounting roots. An
	// explicit retained-candidate continuation can share its issue's slot while
	// the closed original keeps uncertain write accounting. Actual reservations,
	// runs and preparations above still forbid overlapping native execution.
	activeAttempts, err := cappedCountTx(ctx, t, `SELECT DISTINCT repository,
	 CASE WHEN issue>0 THEN 'issue:'||issue::text ELSE 'root:'||root_assignment END
	 FROM factory_attempt_allowances WHERE data->>'active'='true'`, grants.capacity.MaxConcurrentRuns+1)
	if err != nil {
		return err
	}
	if activeAttempts > grants.capacity.MaxConcurrentRuns {
		return ErrCapacityFull
	}
	repoAttempts, err := cappedCountTx(ctx, t, `SELECT DISTINCT
	 CASE WHEN issue>0 THEN 'issue:'||issue::text ELSE 'root:'||root_assignment END
	 FROM factory_attempt_allowances WHERE repository=$1 AND data->>'active'='true'`, 2, repository)
	if err != nil {
		return err
	}
	if repoAttempts > 1 {
		return ErrRepositoryFull
	}
	var used int
	err = t.QueryRowContext(ctx, `SELECT COALESCE(SUM(minutes),0) FROM factory_usage WHERE repository=$1 AND connection=$2`,
		repository, connection).Scan(&used)
	if err != nil {
		return err
	}
	// The spend cap is soft: any minutes left admit one full run.
	if grants.sponsorship.AllowanceMinutes-used < 1 {
		return ErrAllowanceExhausted
	}
	return nil
}

func admissionChanged(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) {
		return ErrAdmissionChanged
	}
	return err
}

// cappedCountTx counts a query's rows up to and including limit: it
// returns the exact count below the limit and the limit itself above
// it. Admission compares against limits far below any table size, so
// the capped count decides exact admission with bounded work.
func cappedCountTx(ctx context.Context, t *sql.Tx, query string, limit int, args ...any) (int, error) {
	var n int
	params := append(append([]any{}, args...), limit)
	limitParameter := "$" + strconv.Itoa(len(params))
	err := t.QueryRowContext(ctx, `SELECT count(*) FROM (`+query+` LIMIT `+limitParameter+`) t`, params...).Scan(&n)
	return n, err
}

func dispatchPacketError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrAssignmentActive
	}
	return fmt.Errorf("dispatch packet failed: %w", err)
}
