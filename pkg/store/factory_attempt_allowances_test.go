package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

func TestCandidatePreparationOwnerPreservesReadFailure(t *testing.T) {
	ctx := context.Background()
	db := dispatchStoreFixture(t)
	a, reservation, run, view := dispatchTestPacket(t, time.Now())
	if err := recordDispatchTestPacket(t, ctx, db, a, reservation, run, view); err != nil {
		t.Fatal(err)
	}
	registration := CandidatePreparationRegistration{
		Repository: a.Repository, Issue: a.Issue, AttemptRoot: a.AttemptRoot,
		PublicationID: factory.NewID(), OwnerAssignment: a.ID, Project: a.ProjectID,
	}
	check := func() error {
		t.Helper()
		tx, err := db.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		return checkCandidatePreparationOwnerTx(ctx, tx, execution.PreparationRecord{}, registration, a.ActorID, "main")
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE factory_assignments SET data=data || '{"actor_id":"not-an-integer"}'::jsonb WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := check(); err == nil || errors.Is(err, ErrAttemptRootMismatch) {
		t.Fatalf("unreadable owner became a permanent identity refusal: %v", err)
	}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `UPDATE factory_assignments SET data=$2 WHERE id=$1`, a.ID, string(data)); err != nil {
		t.Fatal(err)
	}
	// Restart may leave the current open owner with an idle accounting clock.
	if _, err = db.db.ExecContext(ctx, `UPDATE factory_attempt_allowances SET data=jsonb_set(data,'{active}','false') WHERE root_assignment=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if err = check(); err != nil {
		t.Fatalf("recovered exact owner remains unavailable: %v", err)
	}
	registration.OwnerAssignment = factory.NewID()
	if err = check(); !errors.Is(err, ErrAttemptRootMismatch) {
		t.Fatalf("proved owner mismatch lost permanent refusal: %v", err)
	}
}

func TestAttemptAllowanceSurvivesRetryAndPolicyChange(t *testing.T) {
	ctx := context.Background()
	db := dispatchStoreFixture(t)
	now := time.Now().UTC()
	a, reservation, run, view := dispatchTestPacket(t, now)
	if err := recordDispatchTestPacket(t, ctx, db, a, reservation, run, view); err != nil {
		t.Fatal(err)
	}

	allowance, err := db.AttemptAllowance(ctx, a.Repository, a.Issue)
	if err != nil || allowance.RootAssignment != a.ID || allowance.Limits != factory.DefaultAttemptLimits() || !allowance.Active {
		t.Fatalf("initial allowance = %+v, %v", allowance, err)
	}
	originalRun, err := db.FactoryRun(ctx, run.ID)
	if err != nil || originalRun.Admission == nil || originalRun.Admission.Policy.AttemptLimits != factory.DefaultAttemptLimits() {
		t.Fatalf("initial run admission = %+v, %v", originalRun.Admission, err)
	}

	policy, err := db.RepositoryPolicy(ctx, a.Repository)
	if err != nil {
		t.Fatal(err)
	}
	policy.AttemptLimits.ActiveMinutes += 60
	if err = db.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}

	control, err := db.IssueControl(ctx, a.Repository, a.Issue)
	if err != nil {
		t.Fatal(err)
	}
	originalRun, err = db.FactoryRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	originalRun.Outcome, originalRun.Reconciled, originalRun.Summary = factory.Failed, true, "superseded"
	if err = db.SaveFactoryRun(ctx, originalRun); err != nil {
		t.Fatal(err)
	}
	retry, retryView := retryTestRun(t, a, run)
	currentAuthority := a.Authority
	currentAuthority.Policy++ // SaveRepositoryPolicy above advanced the live grant revision.
	if _, err = db.RecordRetryPacket(ctx, a, control, currentAuthority, retry, retryView, 30); err != nil {
		t.Fatal(err)
	}
	storedOriginal, err := db.FactoryRun(ctx, run.ID)
	if err != nil || storedOriginal.Admission == nil || storedOriginal.Admission.Policy.AttemptLimits != factory.DefaultAttemptLimits() {
		t.Fatalf("policy change rewrote original run admission = %+v, %v", storedOriginal.Admission, err)
	}
	storedRetry, err := db.FactoryRun(ctx, retry.ID)
	if err != nil || storedRetry.Admission == nil || storedRetry.Admission.Policy.AttemptLimits != policy.AttemptLimits {
		t.Fatalf("retry did not capture current admission policy = %+v, %v", storedRetry.Admission, err)
	}
	allowance, err = db.AttemptAllowance(ctx, a.Repository, a.Issue)
	if err != nil || allowance.RootAssignment != a.ID || allowance.Limits != factory.DefaultAttemptLimits() {
		t.Fatalf("retry replenished the allowance: %+v, %v", allowance, err)
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = admitAttemptAllowanceTx(ctx, tx, a.Repository, a.Issue, a.ID, nil, time.Now().Add(time.Hour), time.Now()); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	allowance, err = db.AttemptAllowance(ctx, a.Repository, a.Issue)
	if err != nil || allowance.RootAssignment != a.ID || allowance.Limits != factory.DefaultAttemptLimits() {
		t.Fatalf("new assignment identity replenished the allowance: %+v, %v", allowance, err)
	}
}

// phasedCorrectionReviewer records a finished reviewer with a typed
// findings verdict and a committed review operation on the published
// candidate, returning the reviewer. Correction children parented to
// the reviewer admit through the phased correction guard.
func phasedCorrectionReviewer(t *testing.T, ctx context.Context, db *Store, now time.Time, root factory.Assignment, p *factory.Publication) factory.Assignment {
	t.Helper()
	selection, ok := grantTestPolicy().Roles[project.RoleReviewer]
	if !ok {
		t.Fatal("reviewer policy selection missing")
	}
	reviewerRunID := factory.NewID()
	reviewer := root
	reviewer.ID, reviewer.Run, reviewer.RunHistory = factory.NewID(), reviewerRunID, []string{reviewerRunID}
	reviewer.AttemptRoot, reviewer.PublicationAssignment, reviewer.ParentAssignment = root.AttemptRoot, root.ID, root.ID
	reviewer.Role, reviewer.Phase = project.RoleReviewer, project.ExecutionReview
	reviewer.Preparation = "f" + strings.Repeat("d", 24)
	reviewer.SourceCommit = p.Candidate
	reviewer.Stage, reviewer.Revision, reviewer.Attempts = factory.AssignmentAssigned, 0, 1
	reviewer.Outcome, reviewer.Reason, reviewer.Result, reviewer.FinishedUnix = "", "", nil, 0
	reviewer.Harness, reviewer.HarnessVers, reviewer.Model = selection.Harness+"-"+selection.HarnessVers, selection.HarnessVers, selection.Model
	reviewer.Authority = dispatchCurrentAuthority(t, db, reviewer)
	seedReadyPacketPreparation(t, db, reviewer)
	if err := reviewer.Validate(); err != nil {
		t.Fatalf("reviewer fixture invalid: %v", err)
	}
	cycle := factory.ReviewCycle{
		PublicationID: p.ID, AttemptRoot: root.AttemptRoot,
		ID:    factory.ReviewCycleID(p.ID, root.AttemptRoot, 1, "review"),
		Phase: "review", TriggerDecision: "initial-review",
		TriggerReason: "first review of published head",
		Head:          p.Candidate, BaseOID: p.PRCreate.BaseOID,
		FindingDigest:      strings.Repeat("e", 64),
		ReviewerAssignment: reviewer.ID, Round: 1,
	}
	if err := cycle.Validate(); err != nil {
		t.Fatalf("reviewer cycle invalid: %v", err)
	}
	p.ReviewCycles = append(p.ReviewCycles, cycle)
	p.Revision++
	if err := db.UpdatePublication(ctx, *p); err != nil {
		t.Fatal(err)
	}
	*p = cyclePublicationRefresh(t, ctx, db, p.AssignmentID)
	reservation := factory.Reservation{AssignmentID: reviewer.ID, Repository: reviewer.Repository, Connection: reviewer.Connection, State: factory.ReservationHeld, PlannedMinutes: 30}
	run := factory.Run{
		ID: reviewerRunID, ProjectID: reviewer.ProjectID, Role: project.RoleReviewer, InputSHA: p.Candidate,
		Started: now, Deadline: now.Add(30 * time.Minute),
		Image: "sha256:" + strings.Repeat("b", 64), Harness: reviewer.Harness, Model: reviewer.Model,
	}
	view := factory.RunView{RunID: run.ID, Repository: reviewer.Repository, Issue: reviewer.Issue, Attempt: reviewer.ID}
	if err := db.RecordDispatchPacket(ctx, dispatchTestControlFor(t, db, reviewer), reviewer, reservation, run, view); err != nil {
		t.Fatalf("reviewer packet refused: %v", err)
	}
	run.Outcome, run.Summary, run.Reconciled = factory.Succeeded, "reviewed", true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := db.ConsumeReservation(ctx, reviewer.ID); err != nil {
		t.Fatal(err)
	}
	output := &factory.PhaseOutput{Review: &factory.ReviewCycleReport{
		CycleID: cycle.ID,
		NewFindings: []factory.Finding{{
			Key: string("000000000000000000000000000000a0"), OriginRun: reviewer.Run,
			Severity: factory.FindingSeverityMajor, Defect: "missing bound",
			Evidence: "line 42 lacks a check", RequiredOutcome: "add the check",
			FixKind: factory.FindingFixCode, Actionable: true,
		}},
	}}
	phased := factory.PhaseResult{
		Version: 2, Phase: project.ExecutionReview, Status: "completed",
		Summary: "reviewed", Output: output,
	}
	result := factory.AssignmentResult{
		AssignmentID: reviewer.ID, RunID: run.ID, Phase: project.ExecutionReview,
		PhaseResult: &phased, Status: "completed", Summary: "reviewed",
		Findings: []string{}, Reported: true, RecordedUnix: now.Unix(),
	}
	stored, err := db.Assignment(ctx, reviewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored.Stage, stored.Outcome, stored.Reason = factory.AssignmentFinished, factory.Succeeded, factory.AssignReasonReported
	stored.FinishedUnix = now.Unix()
	stored.Result = &result
	if err := db.FinishAssignment(ctx, stored); err != nil {
		t.Fatalf("reviewer finish refused: %v", err)
	}
	// The reviewer packet advanced the stored publication; reload before
	// appending the operation so the revision is current.
	*p = cyclePublicationRefresh(t, ctx, db, p.AssignmentID)
	p.ReviewOperations = append(p.ReviewOperations, factory.ReviewOperation{
		RunID: reviewer.Run,
		Work: factory.ReviewWork{
			OperationID: "review-" + reviewer.Run, AuthRevision: factory.ReviewAuthRevision(p.AssignmentID, reviewer.Run),
			Repository: p.Repository, ActorID: 14,
			PRNumber: p.PRNumber, PRID: p.PRID, IssueID: p.PRCreate.IssueID, PRAuthorID: p.PRCreate.Work.ActorID,
			HeadRef: p.PRCreate.HeadRef, BaseRef: p.PRCreate.BaseRef,
			HeadOID: p.Candidate, BaseOID: p.PRCreate.BaseOID,
			NativeRev: p.NativeRev, NotAfter: now.Add(10 * time.Minute).Unix(),
			Event: "REQUEST_CHANGES", Body: "independent review",
		},
		Outcome: factory.OperationOutcome{
			OperationID: "review-" + reviewer.Run, InstallationID: "installation-1",
			Kind: factory.OpReviewSubmit, ActorID: 14, RepositoryID: p.Repository,
			Effect: factory.OpEffectCommitted, Completion: factory.OpCompletionComplete,
			Receipt: []byte(`{"review_id":31}`),
		},
	})
	p.Revision++
	if err := db.UpdatePublication(ctx, *p); err != nil {
		t.Fatalf("review operation unrecorded: %v", err)
	}
	*p = cyclePublicationRefresh(t, ctx, db, p.AssignmentID)
	finished, err := db.Assignment(ctx, reviewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	return finished
}

// cyclePublicationRefresh reloads the publication the tests mutate
// in place, so later UpdatePublication calls carry its live revision.
func cyclePublicationRefresh(t *testing.T, ctx context.Context, db *Store, assignmentID string) factory.Publication {
	t.Helper()
	stored, err := db.PublicationByAssignment(ctx, assignmentID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

// grantCorrectionFixtureRoles adds the reviewer role to the fixture
// sponsorship in place, keeping its revision: the world being simulated
// granted both roles from the start, so every canned test authority
// still binds.
func grantCorrectionFixtureRoles(t *testing.T, ctx context.Context, db *Store) {
	t.Helper()
	sponsorship, err := db.Sponsorship(ctx, 7, "conn")
	if err != nil {
		t.Fatal(err)
	}
	sponsorship.Roles = append(sponsorship.Roles, project.RoleReviewer)
	data, err := json.Marshal(sponsorship)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE factory_sponsorships SET data=$1 WHERE repository=$2 AND connection=$3 AND revision=$4`,
		string(data), sponsorship.Repository, sponsorship.Connection, sponsorship.Revision); err != nil {
		t.Fatal(err)
	}
}

func TestCorrectionAdmissionConsumesRootAllowanceOnce(t *testing.T) {
	ctx := context.Background()
	db := publicationStoreFixture(t)
	grantCorrectionFixtureRoles(t, ctx, db)
	now := time.Now().UTC()
	root := recordFinishedAssignment(t, db, now, 3, true, "completed")
	p := publicationTestRecord()
	p.AssignmentID, p.Run, p.Preparation, p.BaseSHA = root.ID, root.Run, root.Preparation, root.SourceCommit
	if err := db.RecordPublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.NativeRev, p.ObservedUnix, p.Comparison = 9, 1150, strings.Repeat("c", 40)
	p.Publish = publishedStoreOperation(p, factory.OpRefPublish, 1, "branch-receipt")
	p.Publish.Effect, p.Publish.Completion, p.Publish.Receipt = factory.OpEffectPending, factory.OpCompletionPending, ""
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Publish.Effect, p.Publish.Completion, p.Publish.Receipt = factory.OpEffectCommitted, factory.OpCompletionComplete, "branch-receipt"
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.PRCreate = publishedStoreOperation(p, factory.OpPRCreate, 1, `{"pr_number":9}`)
	p.PRCreate.Effect, p.PRCreate.Completion, p.PRCreate.Receipt = factory.OpEffectPending, factory.OpCompletionPending, ""
	p.PRCreate.PRNumber, p.PRCreate.PRID, p.PRCreate.IssueID = 0, 0, 0
	p.PRCreate.HeadRef, p.PRCreate.BaseRef, p.PRCreate.HeadOID, p.PRCreate.BaseOID = "", "", "", ""
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.PRCreate.Effect, p.PRCreate.Completion, p.PRCreate.Receipt = factory.OpEffectCommitted, factory.OpCompletionComplete, `{"pr_number":9}`
	p.PRNumber, p.PRID = 9, 8
	p.PRCreate.PRNumber, p.PRCreate.PRID, p.PRCreate.IssueID = 9, 8, p.Issue
	p.PRCreate.HeadRef, p.PRCreate.BaseRef = factory.PublishBranchName(p.AssignmentID), p.TargetBranch
	p.PRCreate.HeadOID, p.PRCreate.BaseOID = p.Candidate, p.Comparison
	p.Stage, p.Outcome, p.Reason, p.FinishedUnix = factory.PublicationPublished, factory.Succeeded, factory.PublishReasonLinked, 1200
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatal(err)
	}

	reviewer := phasedCorrectionReviewer(t, ctx, db, now, root, &p)
	correctorRound := 2
	newChild := func(source string) (factory.Assignment, factory.Reservation, factory.Run, factory.RunView, factory.IssueControl) {
		child := root
		child.ID, child.AttemptRoot, child.PublicationAssignment = factory.NewID(), root.AttemptRoot, root.ID
		child.ParentAssignment = reviewer.ID
		child.Phase = project.ExecutionCorrect
		child.Run, child.RunHistory, child.Stage, child.Revision = factory.NewID(), nil, factory.AssignmentAssigned, 0
		child.Attempts, child.Outcome, child.Reason, child.Result, child.FinishedUnix = 1, "", "", nil, 0
		child.SourceCommit = source
		child.RunHistory = []string{child.Run}
		child.Authority = dispatchCurrentAuthority(t, db, child)
		if err := child.Validate(); err != nil {
			t.Fatalf("corrector fixture invalid: %v", err)
		}
		cycle := factory.ReviewCycle{
			PublicationID: p.ID, AttemptRoot: root.AttemptRoot,
			ID:    factory.ReviewCycleID(p.ID, root.AttemptRoot, correctorRound, "review"),
			Phase: "review", TriggerDecision: "initial-review",
			TriggerReason: "correction of published head",
			Head:          source, BaseOID: p.PRCreate.BaseOID,
			FindingDigest:      strings.Repeat("e", 64),
			ReviewerAssignment: reviewer.ID, CorrectionAssignment: child.ID, Round: correctorRound,
		}
		correctorRound++
		if err := cycle.Validate(); err != nil {
			t.Fatalf("corrector cycle invalid: %v", err)
		}
		p.ReviewCycles = append(p.ReviewCycles, cycle)
		p.Revision++
		if err := db.UpdatePublication(ctx, p); err != nil {
			t.Fatal(err)
		}
		p = cyclePublicationRefresh(t, ctx, db, p.AssignmentID)
		run := factory.Run{
			ID: child.Run, ProjectID: child.ProjectID, Role: child.Role, InputSHA: source,
			Started: time.Now().UTC(), Deadline: time.Now().UTC().Add(30 * time.Minute), Image: "sha256:" + strings.Repeat("b", 64), Harness: child.Harness, Model: child.Model,
		}
		reservation := factory.Reservation{AssignmentID: child.ID, Repository: child.Repository, Connection: child.Connection, State: factory.ReservationHeld, PlannedMinutes: 30}
		view := factory.RunView{RunID: run.ID, Repository: child.Repository, Issue: child.Issue, Attempt: child.ID}
		return child, reservation, run, view, dispatchTestControlFor(t, db, child)
	}
	allowance, err := db.AttemptAllowance(ctx, root.Repository, root.Issue)
	if err != nil {
		t.Fatal(err)
	}
	if len(allowance.Corrections) != 0 {
		t.Fatalf("fresh root carries charges: %+v", allowance)
	}
	bad, badReservation, badRun, badView, badControl := newChild(strings.Repeat("4", 40))
	if err = db.RecordDispatchPacket(ctx, badControl, bad, badReservation, badRun, badView); err == nil {
		t.Fatal("noncurrent published candidate packet accepted")
	}
	if _, err = db.Assignment(ctx, bad.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("candidate mismatch left assignment: %v", err)
	}

	child, childReservation, childRun, childView, control := newChild(p.Candidate)
	if err = db.RecordDispatchPacket(ctx, control, child, childReservation, childRun, childView); err != nil {
		t.Fatalf("published correction packet refused: %v", err)
	}
	allowance, err = db.AttemptAllowance(ctx, root.Repository, root.Issue)
	if err != nil || allowance.RootAssignment != root.ID || len(allowance.Corrections) != 1 || allowance.Corrections[0] != child.ID {
		t.Fatalf("correction packet charge = %+v, %v", allowance, err)
	}

	childRun.Outcome, childRun.Reconciled = factory.Failed, true
	if err = db.SaveFactoryRun(ctx, childRun); err != nil {
		t.Fatal(err)
	}
	if err = db.ReleaseReservation(ctx, child.ID); err != nil {
		t.Fatal(err)
	}
	retryRun, retryView := retryTestRun(t, child, childRun)
	if _, err = db.RecordRetryPacket(ctx, child, control, child.Authority, retryRun, retryView, 30); err != nil {
		t.Fatalf("correction retry refused: %v", err)
	}
	allowance, err = db.AttemptAllowance(ctx, root.Repository, root.Issue)
	if err != nil || len(allowance.Corrections) != 1 || allowance.Corrections[0] != child.ID {
		t.Fatalf("correction retry charged another cycle: %+v, %v", allowance, err)
	}

	wrongRoot, wrongReservation, wrongRun, wrongView, wrongControl := newChild(p.Candidate)
	wrongRoot.AttemptRoot = factory.NewID()
	if err = db.RecordDispatchPacket(ctx, wrongControl, wrongRoot, wrongReservation, wrongRun, wrongView); err == nil {
		t.Fatal("child with mismatched attempt root accepted")
	}
	if _, err = db.Assignment(ctx, wrongRoot.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("root mismatch left assignment: %v", err)
	}
	allowance, err = db.AttemptAllowance(ctx, root.Repository, root.Issue)
	if err != nil || len(allowance.Corrections) != 1 || allowance.Corrections[0] != child.ID {
		t.Fatalf("rejected child changed correction history: %+v, %v", allowance, err)
	}
}

func TestCorrectionExhaustionRefusesDispatchAfterBudgetFilled(t *testing.T) {
	ctx := context.Background()
	db := publicationStoreFixture(t)
	grantCorrectionFixtureRoles(t, ctx, db)
	now := time.Now().UTC()
	root := recordFinishedAssignment(t, db, now, 3, true, "completed")
	p := publicationTestRecord()
	p.AssignmentID, p.Run, p.Preparation, p.BaseSHA = root.ID, root.Run, root.Preparation, root.SourceCommit
	if err := db.RecordPublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.NativeRev, p.ObservedUnix, p.Comparison = 9, 1150, strings.Repeat("c", 40)
	p.Publish = publishedStoreOperation(p, factory.OpRefPublish, 1, "branch-receipt")
	p.Publish.Effect, p.Publish.Completion, p.Publish.Receipt = factory.OpEffectPending, factory.OpCompletionPending, ""
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Publish.Effect, p.Publish.Completion, p.Publish.Receipt = factory.OpEffectCommitted, factory.OpCompletionComplete, "branch-receipt"
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.PRCreate = publishedStoreOperation(p, factory.OpPRCreate, 1, `{"pr_number":9}`)
	p.PRCreate.Effect, p.PRCreate.Completion, p.PRCreate.Receipt = factory.OpEffectPending, factory.OpCompletionPending, ""
	p.PRCreate.PRNumber, p.PRCreate.PRID, p.PRCreate.IssueID = 0, 0, 0
	p.PRCreate.HeadRef, p.PRCreate.BaseRef, p.PRCreate.HeadOID, p.PRCreate.BaseOID = "", "", "", ""
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.PRCreate.Effect, p.PRCreate.Completion, p.PRCreate.Receipt = factory.OpEffectCommitted, factory.OpCompletionComplete, `{"pr_number":9}`
	p.PRNumber, p.PRID = 9, 8
	p.PRCreate.PRNumber, p.PRCreate.PRID, p.PRCreate.IssueID = 9, 8, p.Issue
	p.PRCreate.HeadRef, p.PRCreate.BaseRef = factory.PublishBranchName(p.AssignmentID), p.TargetBranch
	p.PRCreate.HeadOID, p.PRCreate.BaseOID = p.Candidate, p.Comparison
	p.Stage, p.Outcome, p.Reason, p.FinishedUnix = factory.PublicationPublished, factory.Succeeded, factory.PublishReasonLinked, 1200
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	reviewer := phasedCorrectionReviewer(t, ctx, db, now, root, &p)
	child := root
	child.ID, child.AttemptRoot, child.PublicationAssignment = factory.NewID(), root.AttemptRoot, root.ID
	child.ParentAssignment = reviewer.ID
	child.Phase = project.ExecutionCorrect
	child.Run, child.RunHistory, child.Stage, child.Revision = factory.NewID(), nil, factory.AssignmentAssigned, 0
	child.Attempts, child.Outcome, child.Reason, child.Result, child.FinishedUnix = 1, "", "", nil, 0
	child.SourceCommit = p.Candidate
	child.RunHistory = []string{child.Run}
	child.Authority = dispatchCurrentAuthority(t, db, child)
	if err := child.Validate(); err != nil {
		t.Fatalf("corrector fixture invalid: %v", err)
	}
	cycle := factory.ReviewCycle{
		PublicationID: p.ID, AttemptRoot: root.AttemptRoot,
		ID:    factory.ReviewCycleID(p.ID, root.AttemptRoot, 2, "review"),
		Phase: "review", TriggerDecision: "initial-review",
		TriggerReason: "correction of published head",
		Head:          p.Candidate, BaseOID: p.PRCreate.BaseOID,
		FindingDigest:      strings.Repeat("e", 64),
		ReviewerAssignment: reviewer.ID, CorrectionAssignment: child.ID, Round: 2,
	}
	if err := cycle.Validate(); err != nil {
		t.Fatalf("corrector cycle invalid: %v", err)
	}
	p.ReviewCycles = append(p.ReviewCycles, cycle)
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	run := factory.Run{
		ID: child.Run, ProjectID: child.ProjectID, Role: child.Role, InputSHA: p.Candidate,
		Started: time.Now().UTC(), Deadline: time.Now().UTC().Add(30 * time.Minute), Image: "sha256:" + strings.Repeat("b", 64), Harness: child.Harness, Model: child.Model,
	}
	reservation := factory.Reservation{AssignmentID: child.ID, Repository: child.Repository, Connection: child.Connection, State: factory.ReservationHeld, PlannedMinutes: 30}
	view := factory.RunView{RunID: run.ID, Repository: child.Repository, Issue: child.Issue, Attempt: child.ID}
	control := dispatchTestControlFor(t, db, child)
	allowance, err := db.AttemptAllowance(ctx, root.Repository, root.Issue)
	if err != nil {
		t.Fatal(err)
	}
	// Fill the correction budget by appending (prefix preserved, never
	// erased); the correction dispatch then refuses as exhausted.
	for len(allowance.Corrections) < allowance.Limits.CorrectionCycles {
		allowance.Corrections = append(allowance.Corrections, factory.NewID())
	}
	allowance.Revision++
	fillTx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveAttemptAllowanceTx(ctx, fillTx, allowance); err != nil {
		_ = fillTx.Rollback()
		t.Fatal(err)
	}
	if err = fillTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = db.RecordDispatchPacket(ctx, control, child, reservation, run, view); !errors.Is(err, factory.ErrCorrectionExhausted) {
		t.Fatalf("exhausted correction packet = %v", err)
	}
	if _, err = db.Assignment(ctx, child.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("exhausted correction left assignment: %v", err)
	}
}

func TestFreshRetryAllowancePreservesConsumedPriorRoot(t *testing.T) {
	ctx := context.Background()
	db := dispatchStoreFixture(t)
	now := time.Now().UTC()
	a, reservation, run, view := dispatchTestPacket(t, now)
	if err := recordDispatchTestPacket(t, ctx, db, a, reservation, run, view); err != nil {
		t.Fatal(err)
	}
	prior, err := db.AttemptAllowance(ctx, a.Repository, a.Issue)
	if err != nil {
		t.Fatal(err)
	}
	prior.ActiveSeconds = int64(prior.Limits.ActiveMinutes * 60)
	prior.Corrections = []string{factory.NewID(), factory.NewID(), factory.NewID()}
	// Preserve the actually stored checkpoint: overwriting it with the
	// earlier pre-dispatch instant rewinds the recorded clock, and the
	// no-refill guard correctly refuses that save as backward.
	prior.Active = true
	prior.Revision++

	root := a
	root.ID, root.Run = factory.NewID(), factory.NewID()
	root.AttemptRoot, root.PublicationAssignment = root.ID, root.ID
	root.Stage, root.Outcome, root.Reason = factory.AssignmentFinished, factory.Failed, factory.AssignReasonRunFailed
	root.Attempts, root.RunHistory, root.Revision = 1, []string{root.Run}, 0
	root.FinishedUnix = now.Unix()
	result := factory.ResultSynthesized(root.ID, root.Run, "failed", "settled", now.Unix())
	root.Result = &result
	if err := root.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		root.ID, root.Repository, root.Issue, root.Run, root.Stage, root.Revision, string(data)); err != nil {
		t.Fatal(err)
	}
	if err = saveAttemptAllowanceTx(ctx, tx, prior); err != nil {
		t.Fatal(err)
	}
	limits := factory.AttemptLimits{ActiveMinutes: 45, CorrectionCycles: 1, ReviewRounds: 6, MaxChildren: 3}
	// Derive a coherent retry time: the explicit retry arrives after the
	// preserved checkpoint, never at the earlier pre-dispatch instant.
	retryNow := time.Now().UTC()
	if retryNow.Unix() < prior.CheckpointUnix {
		retryNow = time.Unix(prior.CheckpointUnix, 0).UTC()
	}
	if err = startFreshAttemptAllowanceTx(ctx, tx, a.Repository, a.Issue, root.ID, limits, retryNow.Add(30*time.Minute), retryNow); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}

	fresh, err := db.AttemptAllowance(ctx, a.Repository, a.Issue)
	if err != nil || fresh.RootAssignment != root.ID || fresh.Limits != limits || fresh.ActiveSeconds != 0 || len(fresh.Corrections) != 0 || !fresh.Active {
		t.Fatalf("fresh root allowance = %+v, %v", fresh, err)
	}
	var oldData []byte
	if err = db.db.QueryRowContext(ctx, `SELECT data FROM factory_attempt_allowances WHERE root_assignment=$1`, a.ID).Scan(&oldData); err != nil {
		t.Fatal(err)
	}
	var previous factory.AttemptAllowance
	if err = json.Unmarshal(oldData, &previous); err != nil {
		t.Fatal(err)
	}
	if err = validateAttemptAllowanceRow(previous, a.Repository, a.Issue, a.ID, previous.Revision); err != nil {
		t.Fatal(err)
	}
	if previous.Limits != factory.DefaultAttemptLimits() || len(previous.Corrections) != 3 || previous.ActiveSeconds != int64(previous.Limits.ActiveMinutes*60) || previous.Active || !previous.Closed {
		t.Fatalf("prior root consumption changed: %+v", previous)
	}
	// The no-refill guard still refuses genuinely backward input:
	// rewinding the preserved checkpoint by one second must fail
	// instead of silently rewriting history.
	rewound := previous
	rewound.CheckpointUnix--
	rewound.Revision++
	probeTx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = probeTx.Rollback() }()
	if err = saveAttemptAllowanceTx(ctx, probeTx, rewound); err == nil {
		t.Fatal("backward checkpoint rewrite admitted")
	} else if !strings.Contains(err.Error(), "clock moved backward") {
		t.Fatalf("backward checkpoint refusal %q does not name the clock", err)
	}
}

func TestInitialAttemptDeadlineMustFitRecordedAllowance(t *testing.T) {
	for name, expired := range map[string]bool{"over budget": false, "already expired": true} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			db := dispatchStoreFixture(t)
			now := time.Now().UTC()
			a, reservation, run, view := dispatchTestPacket(t, now)
			if expired {
				run.Started = now.Add(-time.Minute)
				run.Deadline = now.Add(-time.Second)
			} else {
				run.Deadline = now.Add(3 * time.Hour)
			}
			if err := recordDispatchTestPacket(t, ctx, db, a, reservation, run, view); !errors.Is(err, factory.ErrAttemptTimeExhausted) {
				t.Fatalf("inadmissible initial deadline error = %v", err)
			}
			for _, check := range []struct {
				table, key string
				id         string
			}{
				{"factory_assignments", "id", a.ID},
				{"factory_runs", "id", run.ID},
				{"factory_reservations", "assignment", a.ID},
			} {
				var exists bool
				if err := db.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+check.table+` WHERE `+check.key+`=$1)`, check.id).Scan(&exists); err != nil {
					t.Fatal(err)
				}
				if exists {
					t.Fatalf("refused packet left %s row", check.table)
				}
			}
			if _, err := db.AttemptAllowance(ctx, a.Repository, a.Issue); !errors.Is(err, ErrNotFound) {
				t.Fatalf("refused packet left allowance: %v", err)
			}
		})
	}
}

func TestPauseFreezesOnlyAfterSettledViewsAndRechecksGateRevision(t *testing.T) {
	ctx := context.Background()
	db, dsn := postgresFixture(t)
	seedDispatchLimits(t, db)
	now := time.Now().UTC()
	a, reservation, run, view := dispatchTestPacket(t, now)
	if err := recordDispatchTestPacket(t, ctx, db, a, reservation, run, view); err != nil {
		t.Fatal(err)
	}
	reviewer := factory.NewID()
	if consumed, exhausted, err := db.ChargeReviewDispatch(ctx, a.Repository, a.Issue, a.AttemptRoot, reviewer, strings.Repeat("2", 40), false); err != nil || !consumed || exhausted {
		t.Fatalf("first review charge: consumed=%t exhausted=%t err=%v", consumed, exhausted, err)
	}
	withdrawal, err := db.WithdrawDispatch(ctx, a.Repository, factory.CauseControlPaused, "soda-tester")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.FreezeAttemptAllowances(ctx, a.Repository, withdrawal.Revision, time.Now()); !errors.Is(err, ErrAttemptRunsPending) {
		t.Fatalf("pause freeze with unsettled run = %v", err)
	}
	settled, err := db.FactoryRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	settled.Outcome, settled.Reconciled = factory.Failed, true
	if err = db.SaveFactoryRun(ctx, settled); err != nil {
		t.Fatal(err)
	}
	// A retained native candidate must not disappear from stop/capacity/clock
	// accounting merely because its setup reports Ready and zero exit codes.
	// The envelope is a declared fixture, not native policy enrollment.
	native, err := db.Preparation(ctx, a.Preparation)
	if err != nil {
		t.Fatal(err)
	}
	native.Preparation.ID = "f" + strings.Repeat("a", 24)
	native.Native = &execution.NativeWorkspaceRef{Profile: collaboration.ProfileID(strings.Repeat("a", 32)), InstanceID: strings.Repeat("b", 32), WorktreeID: "native-allowance-fixture"}
	native.State.ID, native.State.Native, native.State.Container = native.Preparation.ID, native.Native, ""
	zero := 0
	native.State.SetupExit, native.State.CheckExit = &zero, &zero
	if _, _, err := db.AdmitPreparation(ctx, native); err != nil {
		t.Fatal(err)
	}
	meta := candidatePreparationRegistration{Repository: a.Repository, Issue: a.Issue, ActorID: a.ActorID, Project: a.ProjectID, OwnerAssignment: a.ID, ChildAssignment: a.ID, AttemptRoot: a.AttemptRoot, Connection: a.Connection, Authority: a.Authority, Control: dispatchTestControlFor(t, db, a), NotAfter: time.Now().Add(time.Minute).Unix()}
	raw, err := marshalCandidatePreparation(native, meta)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE project_preparations SET data=$1 WHERE id=$2`, string(raw), native.Preparation.ID); err != nil {
		t.Fatal(err)
	}
	retainedEvidence := func() string {
		t.Helper()
		var evidence string
		if err := db.db.QueryRowContext(ctx, `SELECT jsonb_build_object(
			'assignment',(SELECT to_jsonb(a) FROM factory_assignments a WHERE id=$1),
			'run',(SELECT to_jsonb(r) FROM factory_runs r WHERE id=$2),
			'publications',(SELECT jsonb_agg(to_jsonb(p) ORDER BY assignment) FROM factory_publications p WHERE assignment=$1))::text`, a.ID, run.ID).Scan(&evidence); err != nil {
			t.Fatal(err)
		}
		return evidence
	}
	beforeEvidence := retainedEvidence()
	beforeStop, err := db.AttemptAllowance(ctx, a.Repository, a.Issue)
	if err != nil || beforeStop.Closed || len(beforeStop.Reviews) != 1 || beforeStop.Reviews[0] != reviewer {
		t.Fatalf("allocated open review allowance: %+v %v", beforeStop, err)
	}
	// Closed roots release transferred checkouts only after their actual run
	// rows settle. Keep this pause fixture's original allowance unchanged.
	var originalAllowance string
	if err = db.db.QueryRowContext(ctx, `SELECT data::text FROM factory_attempt_allowances WHERE root_assignment=$1`, a.AttemptRoot).Scan(&originalAllowance); err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `UPDATE factory_attempt_allowances SET data=jsonb_set(data,'{closed}','true'::jsonb) WHERE root_assignment=$1`, a.AttemptRoot); err != nil {
		t.Fatal(err)
	}
	closedRecords, err := db.PendingCandidatePreparations(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	foundClosed := false
	for _, p := range closedRecords {
		if p.Preparation.Preparation.ID == native.Preparation.ID && p.RetireClosed {
			foundClosed = true
		}
	}
	if !foundClosed {
		t.Fatal("closed settled root did not retire its transferred checkout")
	}
	if _, err = db.db.ExecContext(ctx, `UPDATE factory_runs SET active=true WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	liveRecords, err := db.PendingCandidatePreparations(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range liveRecords {
		if p.Preparation.Preparation.ID == native.Preparation.ID {
			t.Fatal("closed root attempted retirement while its run remained active")
		}
	}
	if _, err = db.db.ExecContext(ctx, `UPDATE factory_runs SET active=false WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `UPDATE factory_attempt_allowances SET data=$1 WHERE root_assignment=$2`, originalAllowance, a.AttemptRoot); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	unobserved := native
	unobserved.State = execution.PreparationState{}
	if _, _, err := db.AdmitCandidatePreparation(ctx, unobserved, registrationFromMeta(meta)); !errors.Is(err, ErrDispatchClosed) {
		t.Fatal("native candidate bypassed the paused dispatch gate", err)
	}
	if _, err := db.AuthorizeCandidatePreparation(ctx, native.Preparation.ID); !errors.Is(err, ErrDispatchClosed) {
		t.Fatal("retained native candidate bypassed the paused dispatch gate", err)
	}
	retained, err := db.CandidatePreparation(ctx, native.Preparation.ID)
	if err != nil || retained.NotAfter != meta.NotAfter || retained.Preparation.Preparation.Revision != native.Preparation.Revision {
		t.Fatal("refused native admission rewrote its deadline or observed revision", err)
	}
	for _, retirement := range []string{"", "uncertain", "confirmed"} {
		if retirement != "" {
			stop := execution.PreparationState{ID: native.Preparation.ID, Project: a.ProjectID, Native: native.Native, Phase: project.PrepareStopped, Stopped: true, Retirement: retirement}
			if err := db.RecordCandidatePreparationStop(ctx, native.Preparation.ID, stop); err != nil {
				t.Fatal(err)
			}
		}
		want := 1
		if retirement == "confirmed" {
			want = 0
		}
		pending, err := db.CandidatePreparations(ctx, a.Repository, a.ProjectID, a.Issue, a.AttemptRoot, "", 16, 0)
		if err != nil || len(pending) != want {
			t.Fatalf("native retirement %q lost pending checkout: %d want %d err=%v", retirement, len(pending), want, err)
		}
		if _, count, err := db.ProjectMaintenanceWork(ctx); err != nil || count != want {
			t.Fatalf("native retirement %q lost maintenance work: %d want %d err=%v", retirement, count, want, err)
		}
		if retirement != "confirmed" {
			if err := db.FreezeAttemptAllowances(ctx, a.Repository, withdrawal.Revision, time.Now()); !errors.Is(err, ErrAttemptRunsPending) {
				t.Fatal("ready/uncertain native preparation froze the attempt clock", err)
			}
			allowance, err := db.AttemptAllowance(ctx, a.Repository, a.Issue)
			if err != nil || !allowance.Active {
				t.Fatal("native preparation released active attempt time", err)
			}
		}
		afterStop, err := db.AttemptAllowance(ctx, a.Repository, a.Issue)
		comparisonTime := time.Now()
		if err != nil || afterStop.Closed || afterStop.RootAssignment != beforeStop.RootAssignment || afterStop.Limits != beforeStop.Limits ||
			!reflect.DeepEqual(afterStop.Reviews, beforeStop.Reviews) || !reflect.DeepEqual(afterStop.ReviewedHeads, beforeStop.ReviewedHeads) ||
			!reflect.DeepEqual(afterStop.UnchangedHeadExtra, beforeStop.UnchangedHeadExtra) || !reflect.DeepEqual(afterStop.Corrections, beforeStop.Corrections) ||
			afterStop.ActiveSeconds < beforeStop.ActiveSeconds || afterStop.RemainingSeconds(comparisonTime) > beforeStop.RemainingSeconds(comparisonTime) {
			t.Fatalf("empty-publication retirement changed the open review grant: before=%+v after=%+v err=%v", beforeStop, afterStop, err)
		}
		if got := retainedEvidence(); got != beforeEvidence {
			t.Fatal("preparation retirement rewrote retained assignment, run or publication evidence")
		}
	}
	if err = db.FreezeAttemptAllowances(ctx, a.Repository, withdrawal.Revision, time.Now()); err != nil {
		t.Fatal(err)
	}
	allowance, err := db.AttemptAllowance(ctx, a.Repository, a.Issue)
	if err != nil || allowance.Active || allowance.Closed {
		t.Fatalf("pause did not freeze allowance: %+v, %v", allowance, err)
	}
	if _, _, err = db.ReopenDispatch(ctx, a.Repository, withdrawal.Revision); err != nil {
		t.Fatal(err)
	}
	if consumed, exhausted, err := db.ChargeReviewDispatch(ctx, a.Repository, a.Issue, a.AttemptRoot, reviewer, strings.Repeat("2", 40), false); err != nil || consumed || exhausted {
		t.Fatalf("first review replay after retirement: consumed=%t exhausted=%t err=%v", consumed, exhausted, err)
	}
	if err = db.FreezeAttemptAllowances(ctx, a.Repository, withdrawal.Revision, time.Now()); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("freeze accepted stale closed-gate revision: %v", err)
	}
}

func TestCapAttemptDeadlineUsesPerRunTimeAndPreservesTimeOnlyGrants(t *testing.T) {
	ctx := context.Background()
	db := dispatchStoreFixture(t)
	now := time.Now().UTC()
	a, reservation, run, view := dispatchTestPacket(t, now)
	if err := recordDispatchTestPacket(t, ctx, db, a, reservation, run, view); err != nil {
		t.Fatal(err)
	}
	allowance, err := db.AttemptAllowance(ctx, a.Repository, a.Issue)
	if err != nil {
		t.Fatal(err)
	}
	allowance.ActiveSeconds = int64(allowance.Limits.ActiveMinutes*60) + 30
	allowance.Active = false // A settled earlier run leaves the issue attempt open.
	// Admission may cross a second boundary after now was sampled. Keep
	// its recorded checkpoint instead of rewinding the fixture's clock.
	allowance.Revision++
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveAttemptAllowanceTx(ctx, tx, allowance); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	requested := time.Now().Add(time.Duration(allowance.Limits.ActiveMinutes*2) * time.Minute).Unix()
	before := time.Now().Unix()
	got, err := db.CapAttemptDeadline(ctx, a.ID, requested)
	seconds := int64(allowance.Limits.ActiveMinutes * 60)
	if err != nil || got < before+seconds || got > time.Now().Unix()+seconds || got >= requested {
		t.Fatalf("capped deadline = %d, want full per-run limit %d before request %d: %v", got, seconds, requested, err)
	}
	short := time.Now().Add(20 * time.Second).Unix()
	if capped, err := db.CapAttemptDeadline(ctx, a.ID, short); err != nil || capped != short {
		t.Fatalf("requested shorter deadline changed: %d %v", capped, err)
	}
	timeOnly := allowance
	timeOnly.Issue = 0
	timeOnly.Intent = &factory.IntentAttemptScope{IntentID: "d151515151515151515151515", OwnerID: 1}
	timeOnly.Limits = factory.AttemptLimits{ActiveMinutes: allowance.Limits.ActiveMinutes}
	timeOnly.ActiveSeconds = seconds - 30
	timeOnly.CheckpointUnix = time.Now().Unix()
	if _, err := attemptDeadlineUnix(timeOnly, time.Now()); !errors.Is(err, factory.ErrAttemptTimeExhausted) {
		t.Fatalf("inactive time-only grant renewed: %v", err)
	}
	timeOnly.Active = true
	if deadline, err := attemptDeadlineUnix(timeOnly, time.Now()); err != nil || deadline != timeOnly.CheckpointUnix+30 {
		t.Fatalf("time-only aggregate grant changed: %d %v", deadline, err)
	}
	timeOnly.ActiveSeconds = seconds
	if _, err := attemptDeadlineUnix(timeOnly, time.Now()); !errors.Is(err, factory.ErrAttemptTimeExhausted) {
		t.Fatalf("exhausted time-only grant renewed: %v", err)
	}

	allowance.CheckpointUnix = time.Now().Add(time.Minute).Unix()
	allowance.Revision++
	tx, err = db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveAttemptAllowanceTx(ctx, tx, allowance); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = db.CapAttemptDeadline(ctx, a.ID, requested); !errors.Is(err, factory.ErrAllowanceClock) {
		t.Fatalf("future-checkpoint deadline cap error = %v", err)
	}
}

func TestPublicationRegistrationCapsDeadlineAndRecoversRecordedReceipt(t *testing.T) {
	ctx := context.Background()
	db := publicationStoreFixture(t)
	p := recordPublishedCorrectionFixture(t, db, ctx)
	allowance, err := db.AttemptAllowance(ctx, p.Repository, p.Issue)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	allowance.ActiveSeconds = int64(allowance.Limits.ActiveMinutes*60) - 30
	allowance.CheckpointUnix = now.Unix()
	allowance.Revision++
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveAttemptAllowanceTx(ctx, tx, allowance); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	requested := now.Add(time.Duration(allowance.Limits.ActiveMinutes*2) * time.Minute).Unix()
	before := time.Now().Unix()
	capped, err := db.CapAttemptDeadline(ctx, p.AssignmentID, requested)
	if err != nil || capped >= requested || capped < before+int64(allowance.Limits.ActiveMinutes*60) || capped > time.Now().Unix()+int64(allowance.Limits.ActiveMinutes*60) {
		t.Fatalf("publication deadline cap = %d, request=%d allowance=%+v err=%v", capped, requested, allowance, err)
	}
	appendPendingCorrection(&p)
	correctionIndex := len(p.Corrections) - 1
	p.Corrections[correctionIndex].Work.NotAfter = requested
	if err = db.UpdatePublication(ctx, p); !errors.Is(err, factory.ErrAttemptTimeExhausted) {
		t.Fatalf("overlong publication intent registration = %v", err)
	}
	p.Corrections[correctionIndex].Work.NotAfter = capped
	if err = db.UpdatePublication(ctx, p); err != nil {
		t.Fatalf("capped publication intent registration: %v", err)
	}

	allowance.Closed, allowance.Active = true, false
	allowance.Revision++
	tx, err = db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveAttemptAllowanceTx(ctx, tx, allowance); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	p.Corrections[correctionIndex].Effect = factory.OpEffectCommitted
	p.Corrections[correctionIndex].Completion = factory.OpCompletionComplete
	p.Corrections[correctionIndex].Receipt = "recorded-native-receipt"
	p.Candidate = p.Corrections[correctionIndex].Work.Candidate
	p.Run = p.Corrections[correctionIndex].RunID
	p.Revision++
	if err = db.UpdatePublication(ctx, p); err != nil {
		t.Fatalf("existing receipt recovery after close: %v", err)
	}
	published, err := db.PublicationByAssignment(ctx, p.AssignmentID)
	if err != nil || len(published.Corrections) != 1 || published.Corrections[0].Receipt != "recorded-native-receipt" {
		t.Fatalf("recorded receipt not recovered: %+v %v", published.Corrections, err)
	}
	appendPendingCorrection(&p)
	newCorrection := len(p.Corrections) - 1
	p.Corrections[newCorrection].RunID = factory.NewID()
	p.Corrections[newCorrection].Work.Candidate = strings.Repeat("e", 40)
	if err = db.UpdatePublication(ctx, p); !errors.Is(err, factory.ErrAttemptClosed) {
		t.Fatalf("new publication intent registered after close: %v", err)
	}
	unchanged, err := db.PublicationByAssignment(ctx, p.AssignmentID)
	if err != nil || len(unchanged.Corrections) != 1 {
		t.Fatalf("refused intent changed publication: %+v %v", unchanged.Corrections, err)
	}
}

func TestPublicationRegistrationPreservesIdleRootAndRefusesMissingRoot(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode string
	}{
		{name: "idle", mode: "idle"},
		{name: "missing", mode: "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db := publicationStoreFixture(t)
			p := recordPublishedCorrectionFixture(t, db, ctx)
			allowance, err := db.AttemptAllowance(ctx, p.Repository, p.Issue)
			if err != nil {
				t.Fatal(err)
			}
			switch tc.mode {
			case "idle":
				allowance.Active = false
			}
			if tc.mode == "missing" {
				if _, err = db.db.ExecContext(ctx, `DELETE FROM factory_attempt_allowances WHERE root_assignment=$1`, allowance.RootAssignment); err != nil {
					t.Fatal(err)
				}
			} else {
				allowance.Revision++
				tx, beginErr := db.db.BeginTx(ctx, nil)
				if beginErr != nil {
					t.Fatal(beginErr)
				}
				if err = saveAttemptAllowanceTx(ctx, tx, allowance); err != nil {
					_ = tx.Rollback()
					t.Fatal(err)
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			appendPendingCorrection(&p)
			p.Corrections[len(p.Corrections)-1].Work.NotAfter = time.Now().Add(10 * time.Minute).Unix()
			err = db.UpdatePublication(ctx, p)
			if tc.mode == "idle" && err != nil || tc.mode == "missing" && !errors.Is(err, factory.ErrAttemptTimeExhausted) {
				t.Fatalf("registration with %s root = %v", tc.mode, err)
			}
			stored, err := db.PublicationByAssignment(ctx, p.AssignmentID)
			wantCorrections := 0
			if tc.mode == "idle" {
				wantCorrections = 1
			}
			if err != nil || len(stored.Corrections) != wantCorrections {
				t.Fatalf("registered corrections with %s root: %+v %v", tc.mode, stored.Corrections, err)
			}
			if tc.mode == "idle" {
				active, err := db.ActivePublishedPublications(ctx, 100)
				found := false
				for _, item := range active {
					found = found || item.ID == p.ID
				}
				if err != nil || !found {
					t.Fatalf("idle open publication omitted: %v", err)
				}
				if closed, err := db.CloseExpiredAttempt(ctx, p.AssignmentID); err != nil || closed {
					t.Fatalf("inactive positive allowance closed: %t %v", closed, err)
				}
				after, err := db.AttemptAllowance(ctx, p.Repository, p.Issue)
				if err != nil || after.Revision != allowance.Revision || after.Active || after.Closed || after.ActiveSeconds != allowance.ActiveSeconds || after.CheckpointUnix != allowance.CheckpointUnix || after.Limits != allowance.Limits {
					t.Fatal("inactive positive allowance changed during expiry check")
				}
			}
		})
	}
}

func TestMergeRegistrationRefusesClosedRoot(t *testing.T) {
	ctx := context.Background()
	db := publicationStoreFixture(t)
	m := mergeTestRecord()
	seedMergeAssignment(t, db, m)
	if err := db.RecordMerge(ctx, m); err != nil {
		t.Fatal(err)
	}
	allowance, err := db.AttemptAllowance(ctx, m.Repository, m.Issue)
	if err != nil {
		t.Fatal(err)
	}
	allowance.ActiveSeconds = int64(allowance.Limits.ActiveMinutes * 60)
	allowance.CheckpointUnix = time.Now().Unix()
	allowance.Closed, allowance.Active = true, false
	allowance.Revision++
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveAttemptAllowanceTx(ctx, tx, allowance); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	m.Revision++
	m.Operation = factory.MergeOperation{Kind: factory.OpMerge, Attempts: 1, OperationID: "soda-x-merge-1", UpdatedUnix: time.Now().Unix(), Work: mergeTestIntent("soda-x-merge-1")}
	m.Operation.Work.NotAfter = time.Now().Add(10 * time.Minute).Unix()
	if err = db.UpdateMerge(ctx, m); !errors.Is(err, factory.ErrAttemptClosed) {
		t.Fatalf("closed-root merge registration = %v", err)
	}
	stored, err := db.MergeByPublication(ctx, m.PublicationID)
	if err != nil || stored.Operation.Work != nil {
		t.Fatalf("refused merge registration changed record: %+v %v", stored.Operation, err)
	}
}
