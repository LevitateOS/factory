package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

func publicationStoreFixture(t *testing.T) *Store {
	t.Helper()
	db := grantStoreFixture(t)
	ctx := context.Background()
	projectID := "p" + strings.Repeat("d", 24)
	requirementID, approvalID := "d"+strings.Repeat("c", 24), "d"+strings.Repeat("e", 24)
	policy := grantTestPolicy()
	policy.Repository = 7
	for _, err := range []error{
		db.UpsertUser(ctx, User{ID: 7, Login: "soda-tester"}),
		db.SaveCapacity(ctx, factory.Capacity{UpdatedBy: 7, MaxConcurrentRuns: 2}),
		db.SaveRepositoryPolicy(ctx, policy),
		db.SaveOperatorGrant(ctx, factory.OperatorGrant{Repository: 7, GrantedBy: 7, Active: true}),
		db.SaveSponsorship(ctx, factory.Sponsorship{Repository: 7, GrantedBy: 7, ActorID: 7, ProjectID: projectID, Connection: "conn", GrantID: "grant", Generation: 1, Roles: []string{project.RoleCoder}, AllowanceMinutes: 60, Active: true}),
		db.SaveEnvironmentGrant(ctx, project.EnvironmentGrant{Repository: 7, Owner: 7, Active: true, Profile: &project.Profile{ID: project.RockyHeadless, Distribution: "rocky", Version: "9.6", Interface: "headless", Architecture: "amd64", Image: "sha256:" + strings.Repeat("b", 64), Revision: strings.Repeat("c", 40)}}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := db.CreateProject(ctx, Project{ID: projectID, Name: "factory", RepositoryID: 7, OwnerID: 7, Repository: "soda-tester/factory"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.BindConfiguredFountainLedger(t.Context(), "https://fixture-fountain.example.test"); err != nil {
		t.Fatal(err)
	}
	if err := db.AdmitRequirementDecision(ctx, project.RequirementDecision{ID: requirementID, Project: projectID, Approver: 7, SourceCommit: strings.Repeat("1", 40), SetupDigest: strings.Repeat("a", 64), InputsDigest: strings.Repeat("b", 64)}); err != nil {
		t.Fatal(err)
	}
	if err := db.AdmitApprovalDecision(ctx, project.ApprovalDecision{ID: approvalID, Project: projectID, Requirement: requirementID, Approver: 7, EffectsDigest: strings.Repeat("a", 64), ReadinessDigest: strings.Repeat("b", 64), Verified: true}); err != nil {
		t.Fatal(err)
	}
	seedReadyPacketPreparation(t, db, factory.Assignment{Preparation: "f" + strings.Repeat("b", 24), ProjectID: projectID, Role: project.RoleCoder, SourceCommit: strings.Repeat("1", 40), Authority: dispatchTestAuthority()})
	if err := db.AdmitAcceptanceDecision(ctx, factory.Acceptance{ID: "d" + strings.Repeat("a", 24), Repository: 7, IssueIndex: "3", Approver: 7, NativeRev: 9, TitleDigest: strings.Repeat("a", 64), TitleOccurrence: "original", ContentDigest: strings.Repeat("b", 64)}); err != nil {
		t.Fatal(err)
	}
	return db
}

func publicationTestRecord() factory.Publication {
	return factory.Publication{
		Authority:    dispatchTestAuthority(),
		Publish:      factory.PublicationOperation{Kind: factory.OpRefPublish},
		PRCreate:     factory.PublicationOperation{Kind: factory.OpPRCreate},
		ID:           factory.NewID(),
		AssignmentID: factory.NewID(),
		ProjectID:    "p" + strings.Repeat("d", 24),
		Role:         project.RoleCoder,
		Acceptance:   "d" + strings.Repeat("a", 24),
		Preparation:  "f" + strings.Repeat("b", 24),
		Run:          factory.NewID(),
		Candidate:    strings.Repeat("2", 40),
		BaseSHA:      strings.Repeat("1", 40),
		TargetBranch: "refs/heads/main",
		Stage:        factory.PublicationOpen,
		Repository:   7,
		Issue:        3,
		CreatedUnix:  1100,
	}
}

func TestRecordPublicationRoundTrip(t *testing.T) {
	db := publicationStoreFixture(t)
	ctx := context.Background()
	p := publicationTestRecord()
	if err := db.RecordPublication(ctx, p); err != nil {
		t.Fatalf("record: %v", err)
	}
	got, err := db.PublicationByAssignment(ctx, p.AssignmentID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.ID != p.ID || got.Stage != factory.PublicationOpen {
		t.Fatalf("round trip: %+v", got)
	}
	if err := db.RecordPublication(ctx, p); err == nil {
		t.Fatal("duplicate assignment publication accepted")
	}
	if _, err := db.PublicationByAssignment(ctx, factory.NewID()); err == nil {
		t.Fatal("missing publication read accepted")
	}
}

func TestUpdatePublicationComparesAndSwaps(t *testing.T) {
	db := publicationStoreFixture(t)
	ctx := context.Background()
	p := publicationTestRecord()
	seedPublicationAssignment(t, db, p)
	if err := db.RecordPublication(ctx, p); err != nil {
		t.Fatalf("record: %v", err)
	}
	p.Revision++
	p.NativeRev, p.ObservedUnix = 9, 1150
	p.Publish = factory.PublicationOperation{
		Work:        publicationStoreIntent("soda-test-publish-1"),
		OperationID: "soda-test-publish-1", Kind: factory.OpRefPublish,
		Effect: factory.OpEffectPending, Attempts: 1, UpdatedUnix: 1150,
	}
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatalf("update: %v", err)
	}
	stale := p
	stale.Revision = 1
	stale.Comparison = strings.Repeat("c", 40)
	if err := db.UpdatePublication(ctx, stale); err == nil {
		t.Fatal("stale publication update accepted")
	}
	got, err := db.PublicationByAssignment(ctx, p.AssignmentID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Revision != 1 || got.Publish.OperationID != "soda-test-publish-1" {
		t.Fatalf("stored: %+v", got)
	}
}

func finishedPublishableAssignment(t *testing.T, now time.Time, issue int64, reported bool, status string) (factory.Assignment, factory.Reservation, factory.Run, factory.RunView) {
	t.Helper()
	a, r, run, view := dispatchTestPacket(t, now)
	a.Issue = issue
	a.Subject.Issue.Issue = issue
	view.Issue = issue
	result := factory.AssignmentResult{
		AssignmentID: a.ID, RunID: a.Run, Status: status,
		Summary: "done", Candidate: strings.Repeat("2", 40),
		Findings: []string{}, Reported: reported, RecordedUnix: now.Unix(),
	}
	a.Stage, a.Outcome, a.Reason, a.Result, a.FinishedUnix = factory.AssignmentFinished, factory.Succeeded, factory.AssignReasonReported, &result, now.Unix()
	return a, r, run, view
}

func recordFinishedAssignment(t *testing.T, db *Store, now time.Time, issue int64, reported bool, status string) factory.Assignment {
	t.Helper()
	ctx := context.Background()
	a, r, run, view := finishedPublishableAssignment(t, now, issue, reported, status)
	assigned := a
	assigned.Stage, assigned.Outcome, assigned.Reason, assigned.Result, assigned.FinishedUnix = factory.AssignmentAssigned, "", "", nil, 0
	if err := recordDispatchTestPacket(t, ctx, db, assigned, r, run, view); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishAssignment(ctx, a); err != nil {
		t.Fatal(err)
	}
	settled := run
	settled.Outcome, settled.Reconciled = factory.Succeeded, true
	if err := db.SaveFactoryRun(ctx, settled); err != nil {
		t.Fatal(err)
	}
	if err := db.ConsumeReservation(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestPublishableAssignmentsSelectsReportedCompleted(t *testing.T) {
	t.Run("reported completed result is eligible", func(t *testing.T) {
		db := publicationStoreFixture(t)
		ctx := context.Background()
		want := recordFinishedAssignment(t, db, time.Now().Truncate(time.Second), 3, true, "completed")
		got, err := db.PublishableAssignments(ctx, 10)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(got) != 1 || got[0].ID != want.ID {
			t.Fatalf("publishable: %+v", got)
		}
	})
	t.Run("no report is ineligible", func(t *testing.T) {
		db := publicationStoreFixture(t)
		recordFinishedAssignment(t, db, time.Now().Truncate(time.Second), 3, false, "completed")
		got, err := db.PublishableAssignments(context.Background(), 10)
		if err != nil || len(got) != 0 {
			t.Fatalf("publishable without report: %+v %v", got, err)
		}
	})
	t.Run("blocked result is ineligible", func(t *testing.T) {
		db := publicationStoreFixture(t)
		recordFinishedAssignment(t, db, time.Now().Truncate(time.Second), 3, true, "blocked")
		got, err := db.PublishableAssignments(context.Background(), 10)
		if err != nil || len(got) != 0 {
			t.Fatalf("publishable blocked result: %+v %v", got, err)
		}
	})
	t.Run("recorded publication excludes assignment", func(t *testing.T) {
		db := publicationStoreFixture(t)
		ctx := context.Background()
		want := recordFinishedAssignment(t, db, time.Now().Truncate(time.Second), 3, true, "completed")
		p := publicationTestRecord()
		p.AssignmentID = want.ID
		p.Repository, p.Issue = want.Repository, want.Issue
		if err := db.RecordPublication(ctx, p); err != nil {
			t.Fatalf("record: %v", err)
		}
		got, err := db.PublishableAssignments(ctx, 10)
		if err != nil || len(got) != 0 {
			t.Fatalf("published assignment still publishable: %+v %v", got, err)
		}
	})
}

func TestOutstandingPublicationsListsOpenAndFenced(t *testing.T) {
	db := publicationStoreFixture(t)
	ctx := context.Background()
	open := publicationTestRecord()
	if err := db.RecordPublication(ctx, open); err != nil {
		t.Fatalf("record open: %v", err)
	}
	fenced := publicationTestRecord()
	fenced.AssignmentID = factory.NewID()
	if err := db.RecordPublication(ctx, fenced); err != nil {
		t.Fatalf("record fenced: %v", err)
	}
	fenced.Stage, fenced.Outcome, fenced.Reason, fenced.FinishedUnix = factory.PublicationFenced, factory.NeedsHuman, factory.PublishReasonFenced, 1200
	fenced.Revision++
	if err := db.UpdatePublication(ctx, fenced); err != nil {
		t.Fatalf("fence: %v", err)
	}
	done := publicationTestRecord()
	done.AssignmentID = factory.NewID()
	done.Issue = 9
	if err := db.RecordPublication(ctx, done); err != nil {
		t.Fatalf("record done: %v", err)
	}
	done.Stage, done.Outcome, done.Reason, done.FinishedUnix = factory.PublicationFailed, factory.Failed, factory.PublishReasonRefused, 1200
	done.Revision++
	if err := db.UpdatePublication(ctx, done); err != nil {
		t.Fatalf("fail: %v", err)
	}
	got, err := db.OutstandingPublications(ctx, 7, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("outstanding: %+v", got)
	}
	if got[0].AssignmentID != open.AssignmentID || got[1].AssignmentID != fenced.AssignmentID {
		t.Fatalf("order: %+v", got)
	}
}

func TestCorrectionRegistrationAndWithdrawalShareAbsentGate(t *testing.T) {
	t.Run("registration first is captured for recovery", func(t *testing.T) {
		db, ctx := publicationStoreFixture(t), context.Background()
		p := recordPublishedCorrectionFixture(t, db, ctx)
		var rows int
		if err := db.db.QueryRowContext(ctx, `SELECT count(*) FROM factory_dispatch WHERE repository=$1`, p.Repository).Scan(&rows); err != nil || rows != 0 {
			t.Fatalf("gate fixture was not absent: rows=%d err=%v", rows, err)
		}
		appendPendingCorrection(&p)
		if err := db.UpdatePublication(ctx, p); err != nil {
			t.Fatalf("register correction under default-open gate: %v", err)
		}
		recovery, err := db.OpenPublications(ctx, 10)
		if err != nil || len(recovery) != 1 || len(recovery[0].Corrections) != 1 {
			t.Fatalf("published correction omitted from recovery pass: %+v %v", recovery, err)
		}
		if _, err := db.WithdrawDispatch(ctx, p.Repository, "pause", "operator"); err != nil {
			t.Fatal(err)
		}
		pending, err := db.OutstandingPublications(ctx, p.Repository, 10)
		if err != nil || len(pending) != 1 || len(pending[0].Corrections) != 1 {
			t.Fatalf("registered correction escaped recovery: %+v %v", pending, err)
		}
	})
	t.Run("withdrawal first rejects correction", func(t *testing.T) {
		db, ctx := publicationStoreFixture(t), context.Background()
		p := recordPublishedCorrectionFixture(t, db, ctx)
		if _, err := db.WithdrawDispatch(ctx, p.Repository, "pause", "operator"); err != nil {
			t.Fatal(err)
		}
		appendPendingCorrection(&p)
		if err := db.UpdatePublication(ctx, p); !errors.Is(err, ErrDispatchClosed) {
			t.Fatalf("correction registered after withdrawal: %v", err)
		}
		stored, err := db.PublicationByAssignment(ctx, p.AssignmentID)
		if err != nil || len(stored.Corrections) != 0 {
			t.Fatalf("refused correction changed publication: %+v %v", stored, err)
		}
	})
	t.Run("concurrent absent-row race has no lost registration", func(t *testing.T) {
		db, ctx := publicationStoreFixture(t), context.Background()
		p := recordPublishedCorrectionFixture(t, db, ctx)
		appendPendingCorrection(&p)
		start := make(chan struct{})
		registered := make(chan error, 1)
		withdrawn := make(chan error, 1)
		go func() { <-start; registered <- db.UpdatePublication(ctx, p) }()
		go func() {
			<-start
			_, err := db.WithdrawDispatch(ctx, p.Repository, "pause", "operator")
			withdrawn <- err
		}()
		close(start)
		regErr, withdrawErr := <-registered, <-withdrawn
		if withdrawErr != nil {
			t.Fatal(withdrawErr)
		}
		if regErr != nil && !errors.Is(regErr, ErrDispatchClosed) {
			t.Fatalf("unexpected registration result: %v", regErr)
		}
		stored, err := db.PublicationByAssignment(ctx, p.AssignmentID)
		if err != nil {
			t.Fatal(err)
		}
		if regErr == nil {
			pending, err := db.OutstandingPublications(ctx, p.Repository, 10)
			if err != nil || len(pending) != 1 || len(pending[0].Corrections) != 1 {
				t.Fatalf("accepted correction was lost to withdrawal: %+v %v", pending, err)
			}
		} else if len(stored.Corrections) != 0 {
			t.Fatalf("refused concurrent correction persisted: %+v", stored.Corrections)
		}
	})
	t.Run("withdrawal waits for registered update at publication row", func(t *testing.T) {
		db, ctx := publicationStoreFixture(t), context.Background()
		p := recordPublishedCorrectionFixture(t, db, ctx)
		appendPendingCorrection(&p)

		// Hold the publication row so registration blocks after acquiring the
		// shared dispatch gate. The observer below waits for PostgreSQL to
		// report that exact statement waiting on a lock; this does not depend
		// on goroutine scheduling or an arbitrary sleep.
		blocker, err := db.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = blocker.Rollback() }()
		var locked []byte
		if err = blocker.QueryRowContext(ctx, `SELECT data FROM factory_publications WHERE assignment=$1 FOR UPDATE`, p.AssignmentID).Scan(&locked); err != nil {
			t.Fatal(err)
		}
		registered := make(chan error, 1)
		go func() { registered <- db.UpdatePublication(ctx, p) }()

		waitCtx, cancelWait := context.WithTimeout(ctx, 5*time.Second)
		defer cancelWait()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			var waiting int
			err = db.db.QueryRowContext(waitCtx, `SELECT count(*) FROM pg_stat_activity
				WHERE datname=current_database() AND state='active' AND wait_event_type='Lock'
				AND query LIKE '%UPDATE factory_publications SET stage=%'`).Scan(&waiting)
			if err != nil {
				_ = blocker.Rollback()
				t.Fatalf("observe blocked publication update: %v", err)
			}
			if waiting > 0 {
				break
			}
			select {
			case err = <-registered:
				_ = blocker.Rollback()
				t.Fatalf("registration returned before reaching its locked publication row: %v", err)
			case <-waitCtx.Done():
				_ = blocker.Rollback()
				t.Fatal("registration never reached its publication-row wait")
			case <-ticker.C:
			}
		}

		withdrawCtx, cancelWithdraw := context.WithTimeout(ctx, 150*time.Millisecond)
		_, withdrawErr := db.WithdrawDispatch(withdrawCtx, p.Repository, "pause", "operator")
		cancelWithdraw()
		if withdrawErr == nil {
			_ = blocker.Rollback()
			<-registered
			t.Fatal("withdrawal closed a gate already owned by registration")
		}
		if !errors.Is(withdrawErr, context.DeadlineExceeded) && !errors.Is(withdrawErr, context.Canceled) {
			_ = blocker.Rollback()
			<-registered
			t.Fatalf("withdrawal did not wait on the registration-owned gate: %v", withdrawErr)
		}
		open, revision, _, err := db.DispatchState(ctx, p.Repository)
		if err != nil || !open || revision != 0 {
			_ = blocker.Rollback()
			<-registered
			t.Fatalf("timed-out withdrawal changed the open gate: open=%v revision=%d err=%v", open, revision, err)
		}
		if err = blocker.Commit(); err != nil {
			t.Fatal(err)
		}
		if err = <-registered; err != nil {
			t.Fatalf("registered correction did not commit: %v", err)
		}
		if _, err = db.WithdrawDispatch(ctx, p.Repository, "pause", "operator"); err != nil {
			t.Fatalf("withdraw after registration commit: %v", err)
		}
		pending, err := db.OutstandingPublications(ctx, p.Repository, 10)
		if err != nil || len(pending) != 1 || len(pending[0].Corrections) != 1 {
			t.Fatalf("registered correction escaped recovery after ordered withdrawal: %+v %v", pending, err)
		}
	})
}

func recordPublishedCorrectionFixture(t *testing.T, db *Store, ctx context.Context) factory.Publication {
	t.Helper()
	p := publicationTestRecord()
	seedPublicationAssignment(t, db, p)
	if err := db.RecordPublication(ctx, p); err != nil {
		t.Fatalf("record initial publication: %v", err)
	}
	p.NativeRev, p.ObservedUnix, p.Comparison = 9, 1150, strings.Repeat("c", 40)
	publish := publishedStoreOperation(p, factory.OpRefPublish, 1, "branch-receipt")
	publish.Effect, publish.Completion = factory.OpEffectPending, factory.OpCompletionPending
	publish.Receipt = ""
	p.Publish = publish
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatalf("register initial branch intent: %v", err)
	}
	p.Publish.Effect, p.Publish.Completion, p.Publish.Receipt = factory.OpEffectCommitted, factory.OpCompletionComplete, "branch-receipt"
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatalf("record initial branch receipt: %v", err)
	}
	pr := publishedStoreOperation(p, factory.OpPRCreate, 1, `{"pr_number":9}`)
	pr.Effect, pr.Completion, pr.Receipt = factory.OpEffectPending, factory.OpCompletionPending, ""
	pr.PRNumber, pr.PRID, pr.IssueID, pr.HeadRef, pr.BaseRef, pr.HeadOID, pr.BaseOID = 0, 0, 0, "", "", "", ""
	p.PRCreate = pr
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatalf("register initial PR intent: %v", err)
	}
	p.PRCreate.Effect, p.PRCreate.Completion, p.PRCreate.Receipt = factory.OpEffectCommitted, factory.OpCompletionComplete, `{"pr_number":9}`
	p.PRNumber, p.PRID = 9, 8
	p.PRCreate.PRNumber, p.PRCreate.PRID, p.PRCreate.IssueID = 9, 8, p.Issue
	p.PRCreate.HeadRef, p.PRCreate.BaseRef = factory.PublishBranchName(p.AssignmentID), p.TargetBranch
	p.PRCreate.HeadOID, p.PRCreate.BaseOID = p.Candidate, p.Comparison
	p.Stage, p.Outcome, p.Reason, p.FinishedUnix = factory.PublicationPublished, factory.Succeeded, factory.PublishReasonLinked, 1200
	p.Revision++
	if err := db.UpdatePublication(ctx, p); err != nil {
		t.Fatalf("record linked PR: %v", err)
	}
	// Publication registration owns its own gate lock. Remove the dispatch
	// row created by the assignment fixture to exercise the logical default-
	// open case before either correction registration or withdrawal.
	if _, err := db.db.ExecContext(ctx, `DELETE FROM factory_dispatch WHERE repository=$1`, p.Repository); err != nil {
		t.Fatalf("reset dispatch gate fixture: %v", err)
	}
	return p
}

func publishedStoreOperation(p factory.Publication, kind string, ordinal int, receipt string) factory.PublicationOperation {
	id := factory.PublicationOperationID(p.ID, kind, ordinal)
	intent := publicationStoreIntent(id)
	intent.TargetBranch, intent.ComparisonRef, intent.ComparisonOID = p.TargetBranch, p.TargetBranch, p.Comparison
	intent.Candidate, intent.ExpectedOld = p.Candidate, "absent"
	if kind == factory.OpPRCreate {
		intent.ExpectedOld = p.Candidate
	}
	return factory.PublicationOperation{
		Work: intent, OperationID: id, Kind: kind, Effect: factory.OpEffectCommitted,
		Completion: factory.OpCompletionComplete, Receipt: receipt, Attempts: 1, UpdatedUnix: 1150,
	}
}

func appendPendingCorrection(p *factory.Publication) {
	id := factory.PublicationOperationID(p.ID, factory.OpRefPublish, len(p.Corrections)+2)
	intent := publicationStoreIntent(id)
	intent.TargetBranch, intent.ComparisonRef, intent.ComparisonOID = p.TargetBranch, p.TargetBranch, p.Comparison
	intent.Candidate, intent.ExpectedOld = strings.Repeat("d", 40), p.Candidate
	intent.CorrectionNumber, intent.CorrectionAuthor = p.PRNumber, p.PRCreate.Work.ActorID
	p.Corrections = append(p.Corrections, factory.PublicationOperation{Work: intent, RunID: p.Run, OperationID: id, Kind: factory.OpRefPublish, Attempts: 1, UpdatedUnix: 1250})
	p.Revision++
}

func TestPublicationCorrectionRunIDIsImmutable(t *testing.T) {
	old := factory.PublicationOperation{
		RunID: "0123456789abcdef0123456789abcdef", Kind: factory.OpRefPublish,
		OperationID: "correction-op", Attempts: 1,
	}
	next := old
	next.RunID = "1123456789abcdef0123456789abcdef"
	if publicationOperationUpdateAllowed(old, next) {
		t.Fatal("registered correction RunID changed during update")
	}
}

func publicationStoreIntent(id string) *factory.PublicationIntent {
	return &factory.PublicationIntent{
		OperationID: id, AuthRevision: "accepted-revision", Candidate: strings.Repeat("2", 40),
		TargetBranch: "refs/heads/main", ExpectedOld: "absent", ComparisonRef: "refs/heads/main", ComparisonOID: strings.Repeat("1", 40),
		PRTitle: "Factory candidate for #3", Repository: 7, ActorID: 5, NativeRev: 9, NotAfter: time.Now().Add(10 * time.Minute).Unix(),
	}
}

func seedPublicationAssignment(t *testing.T, db *Store, p factory.Publication) {
	t.Helper()
	a, reservation, run, view := dispatchTestPacket(t, time.Now())
	a.ID, a.Run, a.RunHistory = p.AssignmentID, p.Run, []string{p.Run}
	a.AttemptRoot, a.PublicationAssignment = a.ID, a.ID
	a.Repository, a.Issue, a.ProjectID, a.Acceptance, a.Authority = p.Repository, p.Issue, p.ProjectID, p.Acceptance, p.Authority
	a.Preparation, a.SourceCommit = p.Preparation, p.BaseSHA
	a.Subject.Issue.Issue, a.Subject.Issue.Acceptance = p.Issue, p.Acceptance
	reservation.AssignmentID, reservation.Repository = a.ID, a.Repository
	run.ID, run.ProjectID = a.Run, a.ProjectID
	run.InputSHA = a.SourceCommit
	view.RunID, view.Attempt, view.Repository, view.Issue = a.Run, a.ID, a.Repository, a.Issue
	if err := recordDispatchTestPacket(t, context.Background(), db, a, reservation, run, view); err != nil {
		t.Fatal(err)
	}
}
