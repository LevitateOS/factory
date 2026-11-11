package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// B16 review/correct packet tests: the dispatch transaction admits
// phase=review/correct publication children through phase-specific guards
// (never through the implement guard), binding the publication owner, the
// current candidate, the reviewer cycle lineage and the original attempt
// root. Refusals land no rows: a refused phase mismatch, wrong
// parent/subject/tuple/reviewer, stale preparation or authority,
// exhausted allowance or D13 fence launches nothing.

const (
	reviewCorrectCandidate = "2222222222222222222222222222222222222222"
	reviewCorrectBase      = "cccccccccccccccccccccccccccccccccccccccc"
)

func reviewCorrectSponsorship(t *testing.T, db *Store) {
	t.Helper()
	ctx := context.Background()
	sponsorship, err := db.Sponsorship(ctx, 7, "conn")
	if err != nil {
		t.Fatal(err)
	}
	sponsorship.Roles = append(sponsorship.Roles, project.RoleReviewer)
	if err := db.SaveSponsorship(ctx, sponsorship); err != nil {
		t.Fatal(err)
	}
}

func reviewCorrectAuthority(t *testing.T, ctx context.Context, db *Store, projectID string, repository int64, connection string) factory.AuthorityRef {
	t.Helper()
	policy, err := db.RepositoryPolicy(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	operator, err := db.OperatorGrant(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	capacity, err := db.Capacity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sponsorship, err := db.Sponsorship(ctx, repository, connection)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := db.EnvironmentGrant(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	requirement, err := db.RequirementHead(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := db.ApprovalHead(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	return factory.AuthorityRef{
		Policy: policy.Revision, Operator: operator.Revision,
		Capacity: capacity.Revision, Sponsorship: sponsorship.Revision,
		SponsorshipConnection: connection,
		Environment:           environment.Revision, RequirementsID: requirement, ApprovalID: approval,
	}
}

func reviewCorrectOwner(t *testing.T, ctx context.Context, db *Store, now time.Time) factory.Assignment {
	t.Helper()
	reviewCorrectSponsorship(t, db)
	root, reservation, run, view := phaseTestInvestigateRoot(t, now)
	// Grant saves bump revisions, so the seed captures live authority
	// exactly like production dispatch does.
	root.Authority = reviewCorrectAuthority(t, ctx, db, root.ProjectID, root.Repository, root.Connection)
	if err := recordDispatchTestPacket(t, ctx, db, root, reservation, run, view); err != nil {
		t.Fatalf("investigate root refused: %v", err)
	}
	return finishInvestigateRoot(t, ctx, db, root, run, now)
}

func reviewCorrectIntent(t *testing.T, opID string, owner factory.Assignment, now time.Time) *factory.PublicationIntent {
	t.Helper()
	intent := factory.PublicationIntent{
		OperationID: opID, AuthRevision: "fixture-revision",
		TargetBranch: "refs/heads/main", Candidate: reviewCorrectCandidate, ExpectedOld: "absent",
		ComparisonRef: "refs/heads/main", ComparisonOID: reviewCorrectBase,
		PRTitle: factory.PRTitleFor(owner.Issue), PRBody: "fixture body",
		Repository: owner.Repository, ActorID: owner.ActorID,
		NativeRev: owner.NativeRev, NotAfter: now.Add(time.Hour).Unix(),
	}
	if err := intent.Validate(); err != nil {
		t.Fatalf("publication intent invalid: %v", err)
	}
	return &intent
}

func reviewCorrectPublication(t *testing.T, owner factory.Assignment, now time.Time) factory.Publication {
	t.Helper()
	p := factory.Publication{
		ID: factory.NewID(), AssignmentID: owner.ID, ProjectID: owner.ProjectID, Role: project.RoleCoder,
		Repository: owner.Repository, Issue: owner.Issue, Acceptance: owner.Acceptance,
		Preparation: owner.Preparation, Run: owner.Run, Candidate: reviewCorrectCandidate, BaseSHA: reviewCorrectBase,
		TargetBranch: "refs/heads/main", Stage: factory.PublicationPublished,
		Outcome: factory.Succeeded, Reason: factory.PublishReasonLinked,
		CreatedUnix: now.Unix(), FinishedUnix: now.Unix(),
		PRNumber: 9, PRID: 8,
		Publish: factory.PublicationOperation{
			Work:        reviewCorrectIntent(t, "publish-"+owner.ID[:8], owner, now),
			OperationID: "publish-" + owner.ID[:8], Kind: factory.OpRefPublish,
			Effect: factory.OpEffectCommitted, Completion: factory.OpCompletionComplete, Attempts: 1,
			UpdatedUnix: now.Unix(),
		},
		PRCreate: factory.PublicationOperation{
			Work:        reviewCorrectIntent(t, "prcreate-"+owner.ID[:8], owner, now),
			OperationID: "prcreate-" + owner.ID[:8], Kind: factory.OpPRCreate,
			Effect: factory.OpEffectCommitted, Completion: factory.OpCompletionComplete, Attempts: 1,
			PRNumber: 9, PRID: 8, IssueID: 10, UpdatedUnix: now.Unix(),
			HeadRef: "refs/heads/candidate", BaseRef: "refs/heads/main",
			HeadOID: reviewCorrectCandidate, BaseOID: reviewCorrectBase,
		},
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("publication fixture invalid: %v", err)
	}
	return p
}

func reviewCorrectCycle(t *testing.T, p factory.Publication, owner factory.Assignment, round int, reviewer, correction string) factory.ReviewCycle {
	t.Helper()
	cycle := factory.ReviewCycle{
		PublicationID: p.ID, AttemptRoot: owner.AttemptRoot,
		ID:                 factory.ReviewCycleID(p.ID, owner.AttemptRoot, round, "review"),
		Phase:              "review",
		TriggerDecision:    "initial-review",
		TriggerReason:      "first review of published head",
		Head:               reviewCorrectCandidate,
		BaseOID:            reviewCorrectBase,
		FindingDigest:      strings.Repeat("e", 64),
		ReviewerAssignment: reviewer, CorrectionAssignment: correction, Round: round,
	}
	if err := cycle.Validate(); err != nil {
		t.Fatalf("cycle fixture invalid: %v", err)
	}
	return cycle
}

func reviewCorrectChild(t *testing.T, ctx context.Context, db *Store, now time.Time, owner factory.Assignment, phase, role, parent, preparation string) (factory.Assignment, factory.Reservation, factory.Run, factory.RunView) {
	t.Helper()
	selection, ok := grantTestPolicy().Roles[role]
	if !ok {
		t.Fatalf("child fixture role %q has no policy selection", role)
	}
	prompt := []byte("publication child prompt for " + phase)
	sum := sha256.Sum256(prompt)
	runID := factory.NewID()
	a := factory.Assignment{
		Authority: reviewCorrectAuthority(t, ctx, db, owner.ProjectID, owner.Repository, owner.Connection),
		ID:        factory.NewID(), AttemptRoot: owner.AttemptRoot, ParentAssignment: parent,
		PublicationAssignment: owner.ID,
		Subject: factory.ExecutionSubject{
			Kind:  factory.SubjectIssue,
			Issue: &factory.IssueSubject{Issue: owner.Issue, Acceptance: owner.Acceptance},
		},
		Phase:     phase,
		ProjectID: owner.ProjectID, Role: role,
		Repository: owner.Repository, Issue: owner.Issue, NativeRev: owner.NativeRev,
		Acceptance: owner.Acceptance, Preparation: preparation,
		Harness: selection.Harness + "-" + selection.HarnessVers, HarnessVers: selection.HarnessVers, Model: selection.Model,
		Connection: owner.Connection, SourceCommit: reviewCorrectCandidate,
		Prompt: prompt, PromptSHA: hex.EncodeToString(sum[:]),
		Run: runID, RunHistory: []string{runID}, Stage: factory.AssignmentAssigned,
		Attempts: 1, CreatedUnix: now.Unix(), ActorID: owner.ActorID,
	}
	// A foreign phase is intentionally unadmitted record shape: the packet
	// guard below must refuse it, so record validation stays unchecked
	// for that case only.
	if phase != project.ExecutionReassess {
		if err := a.Validate(); err != nil {
			t.Fatalf("publication child invalid: %v", err)
		}
	}
	seedReadyPacketPreparation(t, db, a)
	r := factory.Reservation{AssignmentID: a.ID, Repository: a.Repository, Connection: a.Connection, State: factory.ReservationHeld, PlannedMinutes: 30}
	run := factory.Run{
		ID: runID, ProjectID: a.ProjectID, Role: role, InputSHA: reviewCorrectCandidate,
		Started: now, Deadline: now.Add(30 * time.Minute),
		Image: "sha256:" + strings.Repeat("b", 64), Harness: a.Harness, Model: a.Model,
	}
	view := factory.RunView{RunID: runID, Repository: a.Repository, Issue: a.Issue, Attempt: a.ID}
	return a, r, run, view
}

func finishReviewCorrectChild(t *testing.T, ctx context.Context, db *Store, child factory.Assignment, run factory.Run, now time.Time, cycleID string, clear bool) factory.Assignment {
	t.Helper()
	run.Outcome, run.Summary, run.Reconciled = factory.Succeeded, "reviewed", true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatalf("review run unsettled: %v", err)
	}
	if err := db.ConsumeReservation(ctx, child.ID); err != nil {
		t.Fatalf("review reservation unconsumed: %v", err)
	}
	stored, err := db.Assignment(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	output := &factory.PhaseOutput{}
	if clear {
		output.Clear = &factory.ClearReviewReport{
			Conclusion: factory.ClearReviewConclusion, CycleID: cycleID,
			Head: reviewCorrectCandidate, BaseOID: reviewCorrectBase,
			CoverageDigest: strings.Repeat("f", 64),
		}
	} else {
		output.Review = &factory.ReviewCycleReport{
			CycleID: cycleID,
			NewFindings: []factory.Finding{{
				Key: string("000000000000000000000000000000a0"), OriginRun: child.Run,
				Severity: factory.FindingSeverityMajor, Defect: "missing bound",
				Evidence: "line 42 lacks a check", RequiredOutcome: "add the check",
				FixKind: factory.FindingFixCode, Actionable: true,
			}},
		}
	}
	phased := factory.PhaseResult{
		Version: 2, Phase: project.ExecutionReview, Status: "completed",
		Summary: "reviewed", Output: output,
	}
	result := factory.AssignmentResult{
		AssignmentID: child.ID, RunID: run.ID, Phase: project.ExecutionReview,
		PhaseResult: &phased, Status: "completed", Summary: "reviewed",
		Findings: []string{}, Reported: true, RecordedUnix: now.Unix(),
	}
	stored.Stage, stored.Outcome, stored.Reason = factory.AssignmentFinished, factory.Succeeded, factory.AssignReasonReported
	stored.FinishedUnix = now.Unix()
	stored.Result = &result
	if err := db.FinishAssignment(ctx, stored); err != nil {
		t.Fatalf("review finish refused: %v", err)
	}
	finished, err := db.Assignment(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	return finished
}

func finishCorrectionChild(t *testing.T, ctx context.Context, db *Store, child factory.Assignment, run factory.Run, now time.Time) factory.Assignment {
	t.Helper()
	run.Outcome, run.Summary, run.Reconciled = factory.Succeeded, "corrected", true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatalf("correction run unsettled: %v", err)
	}
	if err := db.ConsumeReservation(ctx, child.ID); err != nil {
		t.Fatalf("correction reservation unconsumed: %v", err)
	}
	stored, err := db.Assignment(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	key := "000000000000000000000000000000a0"
	setDigest, err := factory.FindingSetDigest([]string{key})
	if err != nil {
		t.Fatal(err)
	}
	phased := factory.PhaseResult{
		Version: 2, Phase: project.ExecutionCorrect, Status: "completed",
		Summary: "corrected",
		Output: &factory.PhaseOutput{Correction: &factory.CorrectionReport{
			SetDigest: setDigest, NewCandidate: reviewCorrectCandidate,
			Findings: []factory.Finding{{
				Key: key, OriginRun: child.ParentAssignment,
				Severity: factory.FindingSeverityMajor, Defect: "missing bound",
				Evidence: "line 42 lacks a check", RequiredOutcome: "add the check",
				FixKind: factory.FindingFixCode, Actionable: true,
			}},
			Dispositions: []factory.CorrectionDisposition{{Key: key, Disposition: factory.DispositionFixed}},
		}},
	}
	result := factory.AssignmentResult{
		AssignmentID: child.ID, RunID: run.ID, Phase: project.ExecutionCorrect,
		PhaseResult: &phased, Status: "completed", Summary: "corrected",
		Candidate: reviewCorrectCandidate,
		Findings:  []string{}, Reported: true, RecordedUnix: now.Unix(),
	}
	stored.Stage, stored.Outcome, stored.Reason = factory.AssignmentFinished, factory.Succeeded, factory.AssignReasonReported
	stored.FinishedUnix = now.Unix()
	stored.Result = &result
	if err := db.FinishAssignment(ctx, stored); err != nil {
		t.Fatalf("correction finish refused: %v", err)
	}
	finished, err := db.Assignment(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	return finished
}

func settleReviewCorrectRun(t *testing.T, ctx context.Context, db *Store, runID string) {
	t.Helper()
	stored, err := db.FactoryRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	stored.Outcome, stored.Reconciled, stored.Summary = factory.Failed, true, "superseded"
	if err := db.SaveFactoryRun(ctx, stored); err != nil {
		t.Fatal(err)
	}
}

func appendCommittedReviewOp(t *testing.T, ctx context.Context, db *Store, p factory.Publication, runID, event string, actor int64, now time.Time) factory.Publication {
	t.Helper()
	agentVerdict := ""
	if actor == p.PRCreate.Work.ActorID {
		agentVerdict, event = event, "COMMENT"
	}
	p.ReviewOperations = append(p.ReviewOperations, factory.ReviewOperation{
		RunID: runID,
		Work: factory.ReviewWork{
			OperationID: "review-" + runID, AuthRevision: factory.ReviewAuthRevision(p.AssignmentID, runID),
			Repository: p.Repository, ActorID: actor,
			PRNumber: 9, PRID: 8, IssueID: 10, PRAuthorID: 7,
			HeadRef: "refs/heads/candidate", BaseRef: "refs/heads/main",
			HeadOID: reviewCorrectCandidate, BaseOID: reviewCorrectBase,
			NativeRev: 9, NotAfter: now.Add(10 * time.Minute).Unix(),
			Event: event, AgentVerdict: agentVerdict, Body: "independent review",
		},
		Outcome: factory.OperationOutcome{
			OperationID: "review-" + runID, InstallationID: "installation-1",
			Kind: factory.OpReviewSubmit, ActorID: actor, RepositoryID: p.Repository,
			Effect: factory.OpEffectCommitted, Completion: factory.OpCompletionComplete,
			Receipt: []byte(`{"review_id":31}`),
		},
	})
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatalf("review operation unrecorded: %v", err)
	}
	stored, err := db.PublicationByAssignment(ctx, p.AssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func recordReviewCorrectPublication(t *testing.T, ctx context.Context, db *Store, owner factory.Assignment, p factory.Publication) factory.Publication {
	t.Helper()
	authority := reviewCorrectAuthority(t, ctx, db, owner.ProjectID, owner.Repository, owner.Connection)
	open := p
	open.Authority = authority
	open.Stage, open.Outcome, open.Reason, open.FinishedUnix = factory.PublicationOpen, "", "", 0
	open.PRNumber, open.PRID = 0, 0
	open.Publish = factory.PublicationOperation{Kind: factory.OpRefPublish}
	open.PRCreate = factory.PublicationOperation{Kind: factory.OpPRCreate}
	if err := open.Validate(); err != nil {
		t.Fatalf("open publication fixture invalid: %v", err)
	}
	if err := db.RecordPublication(ctx, open); err != nil {
		t.Fatalf("publication unrecorded: %v", err)
	}
	recorded, err := db.PublicationByAssignment(ctx, p.AssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	p.Authority = authority
	p.Revision = recorded.Revision + 1
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatalf("publication unpublished: %v", err)
	}
	stored, err := db.PublicationByAssignment(ctx, p.AssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func TestReviewChildAdmitsThroughReviewGuard(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	db := dispatchStoreFixture(t)
	owner := reviewCorrectOwner(t, ctx, db, now)
	stored := recordReviewCorrectPublication(t, ctx, db, owner, reviewCorrectPublication(t, owner, now))
	payingAccount, err := db.Sponsorship(ctx, owner.Repository, owner.Connection)
	if err != nil {
		t.Fatal(err)
	}
	payingAccount.Connection, payingAccount.Revision = "review-paying-account", 0
	payingAccount.Roles = []string{project.RoleReviewer}
	if err := db.SaveSponsorship(ctx, payingAccount); err != nil {
		t.Fatal(err)
	}
	policy, err := db.RepositoryPolicy(ctx, owner.Repository)
	if err != nil {
		t.Fatal(err)
	}
	selection := policy.Roles[project.RoleReviewer]
	selection.Account = payingAccount.Connection
	policy.Roles[project.RoleReviewer] = selection
	if err := db.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	approval, err := db.ApprovalDecision(ctx, owner.Authority.ApprovalID)
	if err != nil {
		t.Fatal(err)
	}
	approval.Predecessor, approval.ID = approval.ID, "d"+strings.Repeat("9", 24)
	if err := db.AdmitApprovalDecision(ctx, approval); err != nil {
		t.Fatal(err)
	}
	reviewerPrep := "f" + strings.Repeat("d", 24)
	child, reservation, run, view := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionReview, project.RoleReviewer, owner.ID, reviewerPrep)
	child.Connection, reservation.Connection = payingAccount.Connection, payingAccount.Connection
	child.Authority = reviewCorrectAuthority(t, ctx, db, child.ProjectID, child.Repository, child.Connection)
	cycle := reviewCorrectCycle(t, stored, owner, 1, child.ID, "")
	stored.ReviewCycles = append(stored.ReviewCycles, cycle)
	stored.Revision++
	if err := db.UpdatePublication(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if err := recordDispatchTestPacket(t, ctx, db, child, reservation, run, view); err != nil {
		t.Fatalf("review child refused: %v", err)
	}
	allowance, err := db.AttemptAllowance(ctx, owner.Repository, owner.Issue)
	if err != nil || allowance.RootAssignment != owner.ID || allowance.Closed || len(allowance.Corrections) != 0 {
		t.Fatalf("review child changed allowance: %+v %v", allowance, err)
	}
	t.Run("preparation_lock_order", func(t *testing.T) {
		bounded, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		stop, err := db.db.BeginTx(bounded, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer stop.Rollback()
		var stopPID int
		if err := stop.QueryRowContext(bounded, `SELECT pg_backend_pid()`).Scan(&stopPID); err != nil {
			t.Fatal(err)
		}
		if _, err := lockAttemptAllowanceRootTx(bounded, stop, owner.Repository, owner.Issue, owner.AttemptRoot); err != nil {
			t.Fatal(err)
		}
		readerPID, result := make(chan int, 1), make(chan error, 1)
		go func() {
			tx, err := db.db.BeginTx(bounded, nil)
			if err != nil {
				readerPID <- 0
				result <- err
				return
			}
			defer tx.Rollback()
			var pid int
			err = tx.QueryRowContext(bounded, `SELECT pg_backend_pid()`).Scan(&pid)
			readerPID <- pid
			if err == nil {
				err = checkApplianceRunPreparationTx(bounded, tx, child, true)
			}
			result <- err
		}()
		pid := <-readerPID
		if pid == 0 {
			t.Fatal("admission connection unavailable", <-result)
		}
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			var blocked bool
			if err := db.db.QueryRowContext(bounded, `SELECT $1::int=ANY(pg_blocking_pids($2::int))`, stopPID, pid).Scan(&blocked); err != nil {
				t.Fatal(err)
			}
			if blocked {
				break
			}
			select {
			case err := <-result:
				t.Fatal("admission bypassed the held attempt root", err)
			case <-bounded.Done():
				t.Fatal(bounded.Err())
			case <-ticker.C:
			}
		}
		// An observer/stop already holding the root can take the preparation
		// immediately; the waiting admission must not hold it in reverse order.
		var identity string
		if err := stop.QueryRowContext(bounded, `SELECT id FROM project_preparations WHERE id=$1 FOR UPDATE NOWAIT`, child.Preparation).Scan(&identity); err != nil {
			t.Fatal("admission reversed preparation/attempt lock order", err)
		}
		if err := stop.Rollback(); err != nil {
			t.Fatal(err)
		}
		if err := <-result; err != nil {
			t.Fatal("admission did not recover after stop released its root", err)
		}
	})
	finishReviewCorrectChild(t, ctx, db, child, run, now, cycle.ID, true)
	stored = cyclePublicationRefresh(t, ctx, db, owner.ID)
	op := factory.ReviewOperation{RunID: run.ID, Work: factory.ReviewWork{
		OperationID: "review-" + run.ID, AuthRevision: factory.ReviewAuthRevision(owner.ID, run.ID),
		Repository: stored.Repository, ActorID: policy.Review.ActorID,
		PRNumber: stored.PRNumber, PRID: stored.PRID, IssueID: stored.PRCreate.IssueID, PRAuthorID: stored.PRCreate.Work.ActorID,
		HeadRef: stored.PRCreate.HeadRef, BaseRef: stored.PRCreate.BaseRef, HeadOID: stored.Candidate, BaseOID: stored.PRCreate.BaseOID,
		NativeRev: 9, NotAfter: time.Now().Add(5 * time.Minute).Unix(), Event: "COMMENT", AgentVerdict: "APPROVED", Body: "clear independent inspection",
	}}
	stored.ReviewOperations = append(stored.ReviewOperations, op)
	stored.Revision++
	payingAccount, err = db.Sponsorship(ctx, child.Repository, child.Connection)
	if err != nil {
		t.Fatal(err)
	}
	payingAccount.Active = false
	if err := db.SaveSponsorship(ctx, payingAccount); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdatePublication(ctx, stored); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("withdrawn reviewer source admitted review: %v", err)
	}
	payingAccount, err = db.Sponsorship(ctx, child.Repository, child.Connection)
	if err != nil {
		t.Fatal(err)
	}
	payingAccount.Active = true
	if err := db.SaveSponsorship(ctx, payingAccount); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdatePublication(ctx, stored); err != nil {
		t.Fatalf("reviewer's captured fresh setup/source did not admit review: %v", err)
	}
	after := cyclePublicationRefresh(t, ctx, db, owner.ID)
	if after.Authority != stored.Authority || after.Authority.ApprovalID == child.Authority.ApprovalID || len(after.ReviewOperations) != 1 || !reflect.DeepEqual(after.ReviewOperations[0].Work, op.Work) {
		t.Fatal("review changed the original publication authority or work")
	}
	allowanceBefore, err := db.AttemptAllowance(ctx, owner.Repository, owner.Issue)
	if err != nil {
		t.Fatal(err)
	}
	m := mergeTestRecord()
	m.PublicationID, m.AssignmentID, m.Authority = after.ID, owner.ID, after.Authority
	m.ProjectID, m.Repository, m.Issue, m.Acceptance = after.ProjectID, after.Repository, after.Issue, after.Acceptance
	m.HeadRef, m.BaseRef, m.HeadOID, m.BaseOID = after.PRCreate.HeadRef, after.PRCreate.BaseRef, after.Candidate, after.PRCreate.BaseOID
	m.PRNumber, m.PRID, m.IssueID, m.PRAuthorID, m.ReviewerID = after.PRNumber, after.PRID, after.PRCreate.IssueID, after.PRCreate.Work.ActorID, policy.Review.ActorID
	if err := db.RecordMerge(ctx, m); err != nil {
		t.Fatal(err)
	}
	offer, err := db.UnsentMergePreparation(ctx, after.ID)
	if err != nil || offer.ApprovalID != child.Authority.ApprovalID || offer.RequirementsID != child.Authority.RequirementsID {
		t.Fatalf("ordinary merge offer did not capture current setup: %+v %v", offer, err)
	}
	approval.Predecessor, approval.ID = approval.ID, "d"+strings.Repeat("8", 24)
	if err := db.AdmitApprovalDecision(ctx, approval); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PrepareUnsentMerge(ctx, factory.NewID(), "owner", offer, now); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("changed setup admitted stale merge offer: %v", err)
	}
	offer, err = db.UnsentMergePreparation(ctx, after.ID)
	if err != nil {
		t.Fatal(err)
	}
	command := factory.NewID()
	receipt, err := db.PrepareUnsentMerge(ctx, command, "owner", offer, now)
	if err != nil {
		t.Fatalf("ordinary merge preparation: %v", err)
	}
	replay, err := db.PrepareUnsentMerge(ctx, command, "owner", offer, now.Add(time.Hour))
	if err != nil || replay != receipt {
		t.Fatalf("ordinary merge command replay: %+v %v", replay, err)
	}
	prepared, err := db.MergeByPublication(ctx, after.ID)
	if err != nil || prepared.ID != command || prepared.Authority.ApprovalID != approval.ID || prepared.Operation.Attempts != 0 || prepared.Operation.OperationID != "" {
		t.Fatalf("ordinary merge preparation renewed old work: %+v %v", prepared, err)
	}
	allowanceAfter, err := db.AttemptAllowance(ctx, owner.Repository, owner.Issue)
	if err != nil || !reflect.DeepEqual(allowanceBefore, allowanceAfter) {
		t.Fatal("merge preparation changed inference accounting", err)
	}
	// Quit can withdraw an original merge before it acquires authorization or
	// a native operation. The explicit merge-only command retains that terminal
	// row and the completed reviewer, without funding another execution.
	withdrawn := prepared
	withdrawn.WithdrawRequested = true
	withdrawn.Settle(factory.MergeWithdrawn, factory.MergeReasonWithdrawn, time.Now().Unix())
	withdrawn.Revision++
	if err := db.UpdateMerge(ctx, withdrawn); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UnsentMergePreparation(ctx, after.ID); !errors.Is(err, ErrMergeConflict) {
		t.Fatal("open original allowance admitted merge-only recovery", err)
	}
	if _, err := db.CloseAttempt(ctx, owner.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, a, g, err := mergePreparationScopeTx(ctx, tx, after.ID)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*factory.Merge){
		"foreign acceptance": func(m *factory.Merge) { m.Acceptance = "d" + strings.Repeat("7", 24) },
		"foreign project":    func(m *factory.Merge) { m.ProjectID = "p" + strings.Repeat("7", 24) },
		"foreign head ref":   func(m *factory.Merge) { m.HeadRef = "refs/heads/foreign" },
		"foreign PR author":  func(m *factory.Merge) { m.PRAuthorID++ },
		"foreign PR issue":   func(m *factory.Merge) { m.IssueID++ },
		"allocated operation": func(m *factory.Merge) {
			m.Operation.OperationID = factory.MergeOperationIDForGeneration(m.PublicationID, 1)
		},
	} {
		changed := withdrawn
		mutate(&changed)
		if err := admitUnsentMergePreparationTx(ctx, tx, p, a, g, changed); !errors.Is(err, ErrMergeConflict) {
			t.Fatalf("%s admitted withdrawn recovery: %v", name, err)
		}
	}
	_ = tx.Rollback()
	if _, err := db.db.ExecContext(ctx, `UPDATE factory_runs SET settled=false,active=true WHERE id=$1`, child.Run); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UnsentMergePreparation(ctx, after.ID); !errors.Is(err, ErrAttemptRunsPending) {
		t.Fatal("unsettled reviewer admitted merge recovery", err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE factory_runs SET settled=true,active=false WHERE id=$1`, child.Run); err != nil {
		t.Fatal(err)
	}
	var prior, inferenceBefore, inferenceAfter string
	if err := db.db.QueryRowContext(ctx, `SELECT data::text FROM factory_merges WHERE publication=$1`, after.ID).Scan(&prior); err != nil {
		t.Fatal(err)
	}
	inference := func(out *string) error {
		return db.db.QueryRowContext(ctx, `SELECT jsonb_build_object('allowance',(SELECT data FROM factory_attempt_allowances WHERE root_assignment=$1),'assignments',(SELECT jsonb_agg(to_jsonb(a) ORDER BY seq) FROM factory_assignments a),'runs',(SELECT jsonb_agg(to_jsonb(r) ORDER BY seq) FROM factory_runs r),'publication',(SELECT data FROM factory_publications WHERE assignment=$1))::text`, owner.ID).Scan(out)
	}
	if err := inference(&inferenceBefore); err != nil {
		t.Fatal(err)
	}
	offer, err = db.UnsentMergePreparation(ctx, after.ID)
	if err != nil || !offer.RetryNoSend {
		t.Fatal("closed original withdrawal did not offer merge-only recovery", err)
	}
	mergeOnlyCommand := factory.NewID()
	mergeOnly, err := db.PrepareUnsentMerge(ctx, mergeOnlyCommand, "owner", offer, time.Now())
	if err != nil || mergeOnly.NotAfter <= time.Now().Unix() || mergeOnly.NotAfter > time.Now().Unix()+300 {
		t.Fatalf("withdrawn merge-only command: %+v %v", mergeOnly, err)
	}
	replayed, err := db.PrepareUnsentMerge(ctx, mergeOnlyCommand, "owner", offer, time.Now().Add(time.Hour))
	if err != nil || replayed != mergeOnly {
		t.Fatal("withdrawn merge replay renewed its finite grant", err)
	}
	archive, err := db.FactoryCommand(ctx, mergeOnlyCommand)
	var capture struct {
		Prior string `json:"prior"`
	}
	if err != nil || json.Unmarshal([]byte(archive.Outcome), &capture) != nil || capture.Prior != prior {
		t.Fatal("withdrawn original was not archived exactly", err)
	}
	fresh, err := db.MergeByPublication(ctx, after.ID)
	if err != nil || fresh.ID != mergeOnlyCommand || fresh.RetryCommand != mergeOnlyCommand || fresh.Authorization.Bound() || !pristineMergeOperation(fresh.Operation) || len(fresh.History) != 0 || fresh.WithdrawRequested {
		t.Fatalf("withdrawn recovery fabricated authority or a native generation: %+v %v", fresh, err)
	}
	if deadline, err := db.MergeSendDeadline(ctx, fresh, time.Now().Add(time.Hour).Unix()); err != nil || deadline != mergeOnly.NotAfter {
		t.Fatal("withdrawn command did not bound the merge-only deadline", deadline, err)
	}
	if _, err := db.CapAttemptDeadline(ctx, owner.ID, time.Now().Add(time.Minute).Unix()); !errors.Is(err, factory.ErrAttemptClosed) {
		t.Fatal("merge-only recovery reopened model admission", err)
	}
	if _, err := db.UnsentMergePreparation(ctx, after.ID); !errors.Is(err, ErrMergeConflict) {
		t.Fatal("fresh finite command offered automatic replacement", err)
	}
	if err := inference(&inferenceAfter); err != nil || inferenceBefore != inferenceAfter {
		t.Fatal("merge-only recovery changed retained inference or publication", err)
	}
}

func TestCorrectChildAdmitsThroughCorrectGuard(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	db := dispatchStoreFixture(t)
	owner := reviewCorrectOwner(t, ctx, db, now)
	stored := recordReviewCorrectPublication(t, ctx, db, owner, reviewCorrectPublication(t, owner, now))
	reviewerPrep := "f" + strings.Repeat("d", 24)
	reviewer, reviewerReservation, reviewerRun, reviewerView := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionReview, project.RoleReviewer, owner.ID, reviewerPrep)
	corrector, correctionReservation, correctionRun, correctionView := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionCorrect, project.RoleCoder, reviewer.ID, owner.Preparation)
	cycle := reviewCorrectCycleWithCorrection(t, stored, owner, 1, reviewer, corrector)
	stored.ReviewCycles = append(stored.ReviewCycles, cycle)
	stored.Revision++
	if err := db.UpdatePublication(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if err := recordDispatchTestPacket(t, ctx, db, reviewer, reviewerReservation, reviewerRun, reviewerView); err != nil {
		t.Fatalf("review child refused: %v", err)
	}
	finished := finishReviewCorrectChild(t, ctx, db, reviewer, reviewerRun, now, cycle.ID, false)
	if finished.ID != reviewer.ID {
		t.Fatalf("reviewer identity changed: %s", finished.ID)
	}
	stored, err := db.PublicationByAssignment(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored = appendCommittedReviewOp(t, ctx, db, stored, finished.Run, "REQUEST_CHANGES", stored.PRCreate.Work.ActorID, now)
	op := stored.ReviewOperations[len(stored.ReviewOperations)-1]
	if op.Work.Event != "COMMENT" || op.Work.Disposition() != "REQUEST_CHANGES" || op.Work.AgentVerdict == "" {
		t.Fatal("shared-account inspection lost its separate agent verdict")
	}
	invalid := op.Work
	invalid.Event = "REQUEST_CHANGES"
	if invalid.Validate() == nil {
		t.Fatal("author account submitted a provider request-changes review")
	}
	invalid = op.Work
	invalid.AgentVerdict = "APPROVED"
	if factory.ReviewWorkEqual(invalid, op.Work) {
		t.Fatal("agent verdict can change under the same review identity")
	}
	if err := recordDispatchTestPacket(t, ctx, db, corrector, correctionReservation, correctionRun, correctionView); err != nil {
		t.Fatalf("correct child refused: %v", err)
	}
	allowance, err := db.AttemptAllowance(ctx, owner.Repository, owner.Issue)
	if err != nil || allowance.RootAssignment != owner.ID || allowance.Closed {
		t.Fatalf("correction changed roots: %+v %v", allowance, err)
	}
	found := false
	for _, id := range allowance.Corrections {
		if id == corrector.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("correction cycle not charged by assignment identity: %+v", allowance.Corrections)
	}
}

func reviewCorrectCycleWithCorrection(t *testing.T, stored factory.Publication, owner factory.Assignment, round int, reviewer, corrector factory.Assignment) factory.ReviewCycle {
	t.Helper()
	return reviewCorrectCycle(t, stored, owner, round, reviewer.ID, corrector.ID)
}

// admitReviewCorrectRound runs one full review/correction round through the
// real packet path: reviewer admission, typed finish, committed submission
// operation, then correction admission against the round cycle that names
// both children. Cycles are append-only like production records them: the
// correction identity is bound at creation, never mutated afterwards.
func admitReviewCorrectRound(t *testing.T, ctx context.Context, db *Store, owner factory.Assignment, stored factory.Publication, now time.Time, round int) (factory.Assignment, factory.Assignment, factory.Publication) {
	t.Helper()
	reviewerPrep := "f" + strings.Repeat("d", 24)
	reviewer, reviewerReservation, reviewerRun, reviewerView := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionReview, project.RoleReviewer, owner.ID, reviewerPrep)
	corrector, correctionReservation, correctionRun, correctionView := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionCorrect, project.RoleCoder, reviewer.ID, owner.Preparation)
	cycle := reviewCorrectCycleWithCorrection(t, stored, owner, round, reviewer, corrector)
	stored.ReviewCycles = append(stored.ReviewCycles, cycle)
	stored.Revision++
	if err := db.UpdatePublication(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if err := recordDispatchTestPacket(t, ctx, db, reviewer, reviewerReservation, reviewerRun, reviewerView); err != nil {
		t.Fatalf("round %d review refused: %v", round, err)
	}
	finished := finishReviewCorrectChild(t, ctx, db, reviewer, reviewerRun, now, cycle.ID, false)
	recorded, err := db.PublicationByAssignment(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	appendCommittedReviewOp(t, ctx, db, recorded, finished.Run, "REQUEST_CHANGES", 14, now)
	if err := recordDispatchTestPacket(t, ctx, db, corrector, correctionReservation, correctionRun, correctionView); err != nil {
		t.Fatalf("round %d correction refused: %v", round, err)
	}
	// Settle the corrector the way production settlement does so later
	// rounds keep the one-session-per-repository invariant: the run goes
	// inactive and the reservation is consumed.
	settled := finishCorrectionChild(t, ctx, db, corrector, correctionRun, now)
	admitted, err := db.PublicationByAssignment(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	return finished, settled, admitted
}

func TestReviewCorrectPhaseMismatchRefusals(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	t.Run("review phase needs the reviewer role", func(t *testing.T) {
		db := dispatchStoreFixture(t)
		owner := reviewCorrectOwner(t, ctx, db, now)
		stored := recordReviewCorrectPublication(t, ctx, db, owner, reviewCorrectPublication(t, owner, now))
		child, reservation, run, view := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionReview, project.RoleReviewer, owner.ID, "f"+strings.Repeat("d", 24))
		// A coder cannot ride the review phase: record validation refuses
		// before any phase guard runs, so the implement guard never sees
		// this shape either.
		child.Role = project.RoleCoder
		cycle := reviewCorrectCycle(t, stored, owner, 1, child.ID, "")
		stored.ReviewCycles = append(stored.ReviewCycles, cycle)
		stored.Revision++
		if err := db.UpdatePublication(ctx, stored); err != nil {
			t.Fatal(err)
		}
		err := recordDispatchTestPacket(t, ctx, db, child, reservation, run, view)
		if err == nil || strings.Contains(err.Error(), "implementation child") {
			t.Fatalf("coder review routed to the implement guard: %v", err)
		}
		assertDispatchPacketAbsent(t, db, child)
	})
	t.Run("correct phase needs the coder role", func(t *testing.T) {
		db := dispatchStoreFixture(t)
		owner := reviewCorrectOwner(t, ctx, db, now)
		stored := recordReviewCorrectPublication(t, ctx, db, owner, reviewCorrectPublication(t, owner, now))
		child, reservation, run, view := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionCorrect, project.RoleCoder, owner.ID, owner.Preparation)
		child.Role = project.RoleReviewer
		cycle := reviewCorrectCycle(t, stored, owner, 1, owner.ID, child.ID)
		stored.ReviewCycles = append(stored.ReviewCycles, cycle)
		stored.Revision++
		if err := db.UpdatePublication(ctx, stored); err != nil {
			t.Fatal(err)
		}
		err := recordDispatchTestPacket(t, ctx, db, child, reservation, run, view)
		if err == nil || strings.Contains(err.Error(), "implementation child") {
			t.Fatalf("reviewer correction routed to the implement guard: %v", err)
		}
		assertDispatchPacketAbsent(t, db, child)
	})
	t.Run("investigation cannot ride a publication slot", func(t *testing.T) {
		db := dispatchStoreFixture(t)
		owner := reviewCorrectOwner(t, ctx, db, now)
		_ = recordReviewCorrectPublication(t, ctx, db, owner, reviewCorrectPublication(t, owner, now))
		child, reservation, run, view := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionInvestigate, project.RoleCoder, owner.ID, owner.Preparation)
		err := recordDispatchTestPacket(t, ctx, db, child, reservation, run, view)
		if err == nil || !strings.Contains(err.Error(), "unadmitted execution phase") {
			t.Fatalf("foreign phase routed past its guard: %v", err)
		}
		assertDispatchPacketAbsent(t, db, child)
	})
}

func TestReviewCorrectLineageRefusals(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	t.Run("reviewer needs its publication owner", func(t *testing.T) {
		db := dispatchStoreFixture(t)
		owner := reviewCorrectOwner(t, ctx, db, now)
		stored := recordReviewCorrectPublication(t, ctx, db, owner, reviewCorrectPublication(t, owner, now))
		first, firstReservation, firstRun, firstView := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionReview, project.RoleReviewer, owner.ID, "f"+strings.Repeat("d", 24))
		firstCycle := reviewCorrectCycle(t, stored, owner, 1, first.ID, "")
		stored.ReviewCycles = append(stored.ReviewCycles, firstCycle)
		stored.Revision++
		if err := db.UpdatePublication(ctx, stored); err != nil {
			t.Fatal(err)
		}
		if err := recordDispatchTestPacket(t, ctx, db, first, firstReservation, firstRun, firstView); err != nil {
			t.Fatalf("first review refused: %v", err)
		}
		finishedFirst := finishReviewCorrectChild(t, ctx, db, first, firstRun, now, firstCycle.ID, false)
		second, secondReservation, secondRun, secondView := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionReview, project.RoleReviewer, finishedFirst.ID, "f"+strings.Repeat("e", 24))
		secondCycle := reviewCorrectCycle(t, stored, owner, 2, second.ID, "")
		fresh, err := db.PublicationByAssignment(ctx, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		fresh.ReviewCycles = append(fresh.ReviewCycles, secondCycle)
		fresh.Revision++
		if err := db.UpdatePublication(ctx, fresh); err != nil {
			t.Fatal(err)
		}
		err = recordDispatchTestPacket(t, ctx, db, second, secondReservation, secondRun, secondView)
		if err == nil || !errors.Is(err, ErrAttemptRootMismatch) {
			t.Fatalf("reviewer under a non-owner parent admitted: %v", err)
		}
		assertDispatchPacketAbsent(t, db, second)
	})
	t.Run("correction needs its recorded cycle", func(t *testing.T) {
		db := dispatchStoreFixture(t)
		owner := reviewCorrectOwner(t, ctx, db, now)
		stored := recordReviewCorrectPublication(t, ctx, db, owner, reviewCorrectPublication(t, owner, now))
		reviewerPrep := "f" + strings.Repeat("d", 24)
		reviewer, reviewerReservation, reviewerRun, reviewerView := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionReview, project.RoleReviewer, owner.ID, reviewerPrep)
		cycle := reviewCorrectCycle(t, stored, owner, 1, reviewer.ID, "")
		stored.ReviewCycles = append(stored.ReviewCycles, cycle)
		stored.Revision++
		if err := db.UpdatePublication(ctx, stored); err != nil {
			t.Fatal(err)
		}
		if err := recordDispatchTestPacket(t, ctx, db, reviewer, reviewerReservation, reviewerRun, reviewerView); err != nil {
			t.Fatalf("review refused: %v", err)
		}
		finished := finishReviewCorrectChild(t, ctx, db, reviewer, reviewerRun, now, cycle.ID, false)
		published, err := db.PublicationByAssignment(ctx, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		published = appendCommittedReviewOp(t, ctx, db, published, finished.Run, "REQUEST_CHANGES", 14, now)
		corrector, correctionReservation, correctionRun, correctionView := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionCorrect, project.RoleCoder, finished.ID, owner.Preparation)
		// The recorded round names another correction identity: this
		// child answers no recorded round. Cycles stay immutable, so the
		// round is recorded with its foreign binding up front.
		published.ReviewCycles = append(published.ReviewCycles, reviewCorrectCycle(t, published, owner, 2, finished.ID, factory.NewID()))
		published.Revision++
		if err := db.UpdatePublication(ctx, published); err != nil {
			t.Fatal(err)
		}
		err = recordDispatchTestPacket(t, ctx, db, corrector, correctionReservation, correctionRun, correctionView)
		if err == nil || !errors.Is(err, ErrAttemptRootMismatch) {
			t.Fatalf("correction without its cycle admitted: %v", err)
		}
		assertDispatchPacketAbsent(t, db, corrector)
	})
	t.Run("correction answers only its current reviewer", func(t *testing.T) {
		db := dispatchStoreFixture(t)
		owner := reviewCorrectOwner(t, ctx, db, now)
		stored := recordReviewCorrectPublication(t, ctx, db, owner, reviewCorrectPublication(t, owner, now))
		stale, _, admitted := admitReviewCorrectRound(t, ctx, db, owner, stored, now, 1)
		_, _, admitted = admitReviewCorrectRound(t, ctx, db, owner, admitted, now, 2)
		// A correction parented at the superseded first-round reviewer
		// refuses even though that reviewer finished on this head: the
		// newest committed verdict belongs to round two.
		corrector, correctionReservation, correctionRun, correctionView := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionCorrect, project.RoleCoder, stale.ID, owner.Preparation)
		admitted.ReviewCycles = append(admitted.ReviewCycles, reviewCorrectCycle(t, admitted, owner, 3, stale.ID, corrector.ID))
		admitted.Revision++
		if err := db.UpdatePublication(ctx, admitted); err != nil {
			t.Fatal(err)
		}
		err := recordDispatchTestPacket(t, ctx, db, corrector, correctionReservation, correctionRun, correctionView)
		if err == nil || !errors.Is(err, ErrAttemptRootMismatch) {
			t.Fatalf("correction under a superseded reviewer admitted: %v", err)
		}
		assertDispatchPacketAbsent(t, db, corrector)
	})
	t.Run("correction needs the current candidate", func(t *testing.T) {
		db := dispatchStoreFixture(t)
		owner := reviewCorrectOwner(t, ctx, db, now)
		stored := recordReviewCorrectPublication(t, ctx, db, owner, reviewCorrectPublication(t, owner, now))
		finished, _, admitted := admitReviewCorrectRound(t, ctx, db, owner, stored, now, 1)
		corrector, correctionReservation, correctionRun, correctionView := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionCorrect, project.RoleCoder, finished.ID, owner.Preparation)
		corrector.SourceCommit = strings.Repeat("3", 40)
		correctionRun.InputSHA = corrector.SourceCommit
		admitted.ReviewCycles = append(admitted.ReviewCycles, reviewCorrectCycle(t, admitted, owner, 2, finished.ID, corrector.ID))
		admitted.Revision++
		if err := db.UpdatePublication(ctx, admitted); err != nil {
			t.Fatal(err)
		}
		if err := recordDispatchTestPacket(t, ctx, db, corrector, correctionReservation, correctionRun, correctionView); err == nil {
			t.Fatal("correction off the current candidate admitted")
		}
		assertDispatchPacketAbsent(t, db, corrector)
	})
	t.Run("subject binds its issue", func(t *testing.T) {
		db := dispatchStoreFixture(t)
		owner := reviewCorrectOwner(t, ctx, db, now)
		stored := recordReviewCorrectPublication(t, ctx, db, owner, reviewCorrectPublication(t, owner, now))
		child, reservation, run, view := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionReview, project.RoleReviewer, owner.ID, "f"+strings.Repeat("d", 24))
		child.Subject.Issue.Issue = owner.Issue + 100
		cycle := reviewCorrectCycle(t, stored, owner, 1, child.ID, "")
		stored.ReviewCycles = append(stored.ReviewCycles, cycle)
		stored.Revision++
		if err := db.UpdatePublication(ctx, stored); err != nil {
			t.Fatal(err)
		}
		if err := recordDispatchTestPacket(t, ctx, db, child, reservation, run, view); err == nil {
			t.Fatal("review child off its subject admitted")
		} else {
			assertDispatchPacketAbsent(t, db, child)
		}
	})
	t.Run("replaced acceptance refuses", func(t *testing.T) {
		db := dispatchStoreFixture(t)
		owner := reviewCorrectOwner(t, ctx, db, now)
		stored := recordReviewCorrectPublication(t, ctx, db, owner, reviewCorrectPublication(t, owner, now))
		next := acceptanceFixture("d"+strings.Repeat("b", 24), owner.Acceptance)
		next.Repository, next.IssueIndex, next.NativeRev = owner.Repository, "3", owner.NativeRev+1
		if err := db.AdmitAcceptanceDecision(ctx, next); err != nil {
			t.Fatal(err)
		}
		child, reservation, run, view := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionReview, project.RoleReviewer, owner.ID, "f"+strings.Repeat("d", 24))
		cycle := reviewCorrectCycle(t, stored, owner, 1, child.ID, "")
		stored.ReviewCycles = append(stored.ReviewCycles, cycle)
		stored.Revision++
		if err := db.UpdatePublication(ctx, stored); err != nil {
			t.Fatal(err)
		}
		err := recordDispatchTestPacket(t, ctx, db, child, reservation, run, view)
		if err == nil || !errors.Is(err, ErrDispatchControlStale) {
			t.Fatalf("review child on a replaced acceptance admitted: %v", err)
		}
		assertDispatchPacketAbsent(t, db, child)
	})
	t.Run("stale preparation refuses", func(t *testing.T) {
		t.Run("correction keeps its owner preparation", func(t *testing.T) {
			db := dispatchStoreFixture(t)
			owner := reviewCorrectOwner(t, ctx, db, now)
			stored := recordReviewCorrectPublication(t, ctx, db, owner, reviewCorrectPublication(t, owner, now))
			finished, _, admitted := admitReviewCorrectRound(t, ctx, db, owner, stored, now, 1)
			stray, strayReservation, strayRun, strayView := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionCorrect, project.RoleCoder, finished.ID, "f"+strings.Repeat("e", 24))
			admitted.ReviewCycles = append(admitted.ReviewCycles, reviewCorrectCycle(t, admitted, owner, 2, finished.ID, stray.ID))
			admitted.Revision++
			if err := db.UpdatePublication(ctx, admitted); err != nil {
				t.Fatal(err)
			}
			if err := recordDispatchTestPacket(t, ctx, db, stray, strayReservation, strayRun, strayView); err == nil {
				t.Fatal("correction off its owner preparation admitted")
			} else {
				assertDispatchPacketAbsent(t, db, stray)
			}
		})
		t.Run("reviewer shares its owner preparation", func(t *testing.T) {
			db := dispatchStoreFixture(t)
			owner := reviewCorrectOwner(t, ctx, db, now)
			stored := recordReviewCorrectPublication(t, ctx, db, owner, reviewCorrectPublication(t, owner, now))
			child, reservation, run, view := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionReview, project.RoleReviewer, owner.ID, owner.Preparation)
			cycle := reviewCorrectCycle(t, stored, owner, 1, child.ID, "")
			fresh, err := db.PublicationByAssignment(ctx, owner.ID)
			if err != nil {
				t.Fatal(err)
			}
			fresh.ReviewCycles = append(fresh.ReviewCycles, cycle)
			fresh.Revision++
			if err := db.UpdatePublication(ctx, fresh); err != nil {
				t.Fatal(err)
			}
			if err := recordDispatchTestPacket(t, ctx, db, child, reservation, run, view); err == nil {
				t.Fatal("reviewer sharing its owner preparation admitted")
			} else {
				assertDispatchPacketAbsent(t, db, child)
			}
		})
	})
	t.Run("stale authority refuses", func(t *testing.T) {
		db := dispatchStoreFixture(t)
		owner := reviewCorrectOwner(t, ctx, db, now)
		stored := recordReviewCorrectPublication(t, ctx, db, owner, reviewCorrectPublication(t, owner, now))
		stale := reviewCorrectAuthority(t, ctx, db, owner.ProjectID, owner.Repository, owner.Connection)
		policy, err := db.RepositoryPolicy(ctx, owner.Repository)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.SaveRepositoryPolicy(ctx, policy); err != nil {
			t.Fatal(err)
		}
		child, reservation, run, view := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionReview, project.RoleReviewer, owner.ID, "f"+strings.Repeat("d", 24))
		child.Authority = stale
		cycle := reviewCorrectCycle(t, stored, owner, 1, child.ID, "")
		stored.ReviewCycles = append(stored.ReviewCycles, cycle)
		stored.Revision++
		if err := db.UpdatePublication(ctx, stored); err != nil {
			t.Fatal(err)
		}
		// The assignment still pins the superseded policy revision.
		if err := recordDispatchTestPacket(t, ctx, db, child, reservation, run, view); err == nil || !errors.Is(err, ErrAdmissionChanged) {
			t.Fatalf("review child under stale authority admitted: %v", err)
		}
		assertDispatchPacketAbsent(t, db, child)
	})
}

func TestReviewCorrectAllowanceAndFence(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	t.Run("correction cycles exhaust without launch", func(t *testing.T) {
		db := dispatchStoreFixture(t)
		owner := reviewCorrectOwner(t, ctx, db, now)
		stored := recordReviewCorrectPublication(t, ctx, db, owner, reviewCorrectPublication(t, owner, now))
		for round := 1; round <= 3; round++ {
			_, _, stored = admitReviewCorrectRound(t, ctx, db, owner, stored, now, round)
		}
		allowance, err := db.AttemptAllowance(ctx, owner.Repository, owner.Issue)
		if err != nil || len(allowance.Corrections) != 3 {
			t.Fatalf("three corrections uncharged: %+v %v", allowance, err)
		}
		reviewerPrep := "f" + strings.Repeat("d", 24)
		reviewer, reviewerReservation, reviewerRun, reviewerView := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionReview, project.RoleReviewer, owner.ID, reviewerPrep)
		corrector, correctionReservation, correctionRun, correctionView := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionCorrect, project.RoleCoder, reviewer.ID, owner.Preparation)
		cycle := reviewCorrectCycleWithCorrection(t, stored, owner, 4, reviewer, corrector)
		stored.ReviewCycles = append(stored.ReviewCycles, cycle)
		stored.Revision++
		if err := db.UpdatePublication(ctx, stored); err != nil {
			t.Fatal(err)
		}
		if err := recordDispatchTestPacket(t, ctx, db, reviewer, reviewerReservation, reviewerRun, reviewerView); err != nil {
			t.Fatalf("fourth review refused: %v", err)
		}
		finished := finishReviewCorrectChild(t, ctx, db, reviewer, reviewerRun, now, cycle.ID, false)
		round, err := db.PublicationByAssignment(ctx, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		appendCommittedReviewOp(t, ctx, db, round, finished.Run, "REQUEST_CHANGES", 14, now)
		err = recordDispatchTestPacket(t, ctx, db, corrector, correctionReservation, correctionRun, correctionView)
		if err == nil || !errors.Is(err, factory.ErrCorrectionExhausted) {
			t.Fatalf("fourth correction admitted past exhaustion: %v", err)
		}
		assertDispatchPacketAbsent(t, db, corrector)
	})
	t.Run("completion fence refuses fresh and retry packets", func(t *testing.T) {
		db := dispatchStoreFixture(t)
		owner := reviewCorrectOwner(t, ctx, db, now)
		stored := recordReviewCorrectPublication(t, ctx, db, owner, reviewCorrectPublication(t, owner, now))
		reviewerPrep := "f" + strings.Repeat("d", 24)
		reviewer, reviewerReservation, reviewerRun, reviewerView := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionReview, project.RoleReviewer, owner.ID, reviewerPrep)
		cycle := reviewCorrectCycle(t, stored, owner, 1, reviewer.ID, "")
		stored.ReviewCycles = append(stored.ReviewCycles, cycle)
		stored.Revision++
		if err := db.UpdatePublication(ctx, stored); err != nil {
			t.Fatal(err)
		}
		if err := recordDispatchTestPacket(t, ctx, db, reviewer, reviewerReservation, reviewerRun, reviewerView); err != nil {
			t.Fatalf("review refused before the fence: %v", err)
		}
		if _, err := db.db.ExecContext(ctx, `INSERT INTO factory_completions(repository,generation,event,publication,occurrence,data) VALUES($1,$2,$3,$4,$5,$6)`,
			owner.Repository, 1, factory.NewID(), factory.NewID(), "fixture-fence", `{}`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.db.ExecContext(ctx, `INSERT INTO factory_reassessments(cycle,repository,generation,event,state,data) VALUES($1,$2,$3,$4,$5,$6)`,
			factory.NewID(), owner.Repository, 1, factory.NewID(), factory.ReassessmentCollecting, `{}`); err != nil {
			t.Fatal(err)
		}
		fresh, freshReservation, freshRun, freshView := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionReview, project.RoleReviewer, owner.ID, "f"+strings.Repeat("e", 24))
		if err := recordDispatchTestPacket(t, ctx, db, fresh, freshReservation, freshRun, freshView); !errors.Is(err, ErrCompletionFenced) {
			t.Fatalf("fenced review admitted: %v", err)
		} else {
			assertDispatchPacketAbsent(t, db, fresh)
		}
		retryRun := factory.Run{
			ID: factory.NewID(), ProjectID: reviewer.ProjectID, Role: reviewer.Role, InputSHA: reviewer.SourceCommit,
			Started: now, Deadline: now.Add(30 * time.Minute),
			Image: reviewerRun.Image, Harness: reviewer.Harness, Model: reviewer.Model,
		}
		retryView := factory.RunView{RunID: retryRun.ID, Repository: reviewer.Repository, Issue: reviewer.Issue, Attempt: reviewer.ID}
		storedReviewer, err := db.Assignment(ctx, reviewer.ID)
		if err != nil {
			t.Fatal(err)
		}
		control := dispatchTestControlFor(t, db, storedReviewer)
		if _, err := db.RecordRetryPacket(ctx, storedReviewer, control, storedReviewer.Authority, retryRun, retryView, 30); !errors.Is(err, ErrCompletionFenced) {
			t.Fatalf("fenced retry admitted: %v", err)
		}
		var count int
		if err := db.db.QueryRowContext(ctx, `SELECT count(*) FROM factory_runs WHERE id=$1`, retryRun.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatal("fenced retry recorded its run")
		}
	})
	t.Run("retry and redelivery charge once", func(t *testing.T) {
		db := dispatchStoreFixture(t)
		owner := reviewCorrectOwner(t, ctx, db, now)
		stored := recordReviewCorrectPublication(t, ctx, db, owner, reviewCorrectPublication(t, owner, now))
		reviewerPrep := "f" + strings.Repeat("d", 24)
		reviewer, reviewerReservation, reviewerRun, reviewerView := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionReview, project.RoleReviewer, owner.ID, reviewerPrep)
		corrector, correctionReservation, correctionRun, correctionView := reviewCorrectChild(t, ctx, db, now, owner, project.ExecutionCorrect, project.RoleCoder, reviewer.ID, owner.Preparation)
		cycle := reviewCorrectCycleWithCorrection(t, stored, owner, 1, reviewer, corrector)
		stored.ReviewCycles = append(stored.ReviewCycles, cycle)
		stored.Revision++
		if err := db.UpdatePublication(ctx, stored); err != nil {
			t.Fatal(err)
		}
		if err := recordDispatchTestPacket(t, ctx, db, reviewer, reviewerReservation, reviewerRun, reviewerView); err != nil {
			t.Fatalf("review refused: %v", err)
		}
		// Production charges the round by assignment identity before the
		// host call; the packet that follows retains the root.
		if spent, exhausted, err := db.ChargeReviewDispatch(ctx, owner.Repository, owner.Issue, owner.ID, reviewer.ID, reviewCorrectCandidate, false); err != nil || exhausted || !spent {
			t.Fatalf("review charge failed: spent=%v exhausted=%v err=%v", spent, exhausted, err)
		}
		// Restarting the assigned reviewer records a fresh run through
		// the review guard without spending another round. Recovery
		// settles the superseded run first so it stops counting against
		// the one-session invariant.
		settleReviewCorrectRun(t, ctx, db, reviewerRun.ID)
		storedReviewer, err := db.Assignment(ctx, reviewer.ID)
		if err != nil {
			t.Fatal(err)
		}
		restartRun := factory.Run{
			ID: factory.NewID(), ProjectID: storedReviewer.ProjectID, Role: storedReviewer.Role, InputSHA: storedReviewer.SourceCommit,
			Started: now, Deadline: now.Add(30 * time.Minute),
			Image: "sha256:" + strings.Repeat("b", 64), Harness: storedReviewer.Harness, Model: storedReviewer.Model,
		}
		restartView := factory.RunView{RunID: restartRun.ID, Repository: storedReviewer.Repository, Issue: storedReviewer.Issue, Attempt: storedReviewer.ID}
		if _, err := db.RecordRetryPacket(ctx, storedReviewer, dispatchTestControlFor(t, db, storedReviewer), storedReviewer.Authority, restartRun, restartView, 30); err != nil {
			t.Fatalf("reviewer restart refused: %v", err)
		}
		finished := finishReviewCorrectChild(t, ctx, db, reviewer, restartRun, now, cycle.ID, false)
		round, err := db.PublicationByAssignment(ctx, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		appendCommittedReviewOp(t, ctx, db, round, finished.Run, "REQUEST_CHANGES", 14, now)
		if err := recordDispatchTestPacket(t, ctx, db, corrector, correctionReservation, correctionRun, correctionView); err != nil {
			t.Fatalf("correction refused: %v", err)
		}
		before, err := db.AttemptAllowance(ctx, owner.Repository, owner.Issue)
		if err != nil || len(before.Reviews) != 1 || len(before.Corrections) != 1 {
			t.Fatalf("round uncharged: %+v %v", before, err)
		}
		// Redelivery of the same review round replays without recharge.
		spent, exhausted, err := db.ChargeReviewDispatch(ctx, owner.Repository, owner.Issue, owner.ID, before.Reviews[0], reviewCorrectCandidate, false)
		if err != nil || exhausted || spent {
			t.Fatalf("review redelivery recharged: spent=%v exhausted=%v err=%v", spent, exhausted, err)
		}
		// Restarting the assigned corrector records a fresh run without
		// spending another correction cycle.
		settleReviewCorrectRun(t, ctx, db, correctionRun.ID)
		storedCorrector, err := db.Assignment(ctx, corrector.ID)
		if err != nil {
			t.Fatal(err)
		}
		restartCorrection := factory.Run{
			ID: factory.NewID(), ProjectID: storedCorrector.ProjectID, Role: storedCorrector.Role, InputSHA: storedCorrector.SourceCommit,
			Started: now, Deadline: now.Add(30 * time.Minute),
			Image: "sha256:" + strings.Repeat("b", 64), Harness: storedCorrector.Harness, Model: storedCorrector.Model,
		}
		restartCorrectionView := factory.RunView{RunID: restartCorrection.ID, Repository: storedCorrector.Repository, Issue: storedCorrector.Issue, Attempt: storedCorrector.ID}
		if _, err := db.RecordRetryPacket(ctx, storedCorrector, dispatchTestControlFor(t, db, storedCorrector), storedCorrector.Authority, restartCorrection, restartCorrectionView, 30); err != nil {
			t.Fatalf("corrector restart refused: %v", err)
		}
		after, err := db.AttemptAllowance(ctx, owner.Repository, owner.Issue)
		if err != nil || len(after.Reviews) != 1 || len(after.Corrections) != 1 {
			t.Fatalf("restart recharged: %+v %v", after, err)
		}
	})
}
