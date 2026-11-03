package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/identity"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

func TestDispatchAccountingNeverRefreshes(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	sp, err := db.Sponsorship(ctx, fx.repo, "conn")
	if err != nil {
		t.Fatal(err)
	}
	sp.AllowanceMinutes = 1
	if err := db.SaveSponsorship(ctx, sp); err != nil {
		t.Fatal(err)
	}
	headA := fx.accept(t, 3, "d333333333333333333333333")
	headB := fx.accept(t, 5, "d555555555555555555555555")
	fx.queue(t, 3, headA.ID)
	fx.queue(t, 5, headB.ID)

	first := DispatchPass(ctx, fx.deps())
	if len(first.Launched) != 1 {
		t.Fatalf("first pass = %+v %+v", first.Launched, first.Waits)
	}
	run, err := db.FactoryRun(ctx, first.Launched[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	run.Outcome, run.Summary, run.Reconciled = factory.Succeeded, "done", true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, ok := AccountSettledRun(ctx, db, run, time.Now()); !ok {
		t.Fatal("settled run unaccounted")
	}
	if total, _ := db.UsageTotal(ctx, fx.repo, "conn"); total != 1 {
		t.Fatalf("usage total = %d", total)
	}
	second := DispatchPass(ctx, fx.deps())
	if len(second.Launched) != 0 || waitReason(second, 5) != WaitAllowance {
		t.Fatalf("second pass = %+v %+v", second.Launched, second.Waits)
	}

	// An acceptance edit authorizes new work but refreshes no budget: the
	// re-queued issue still waits on the exhausted allowance.
	edited := headB
	edited.ID = "d666666666666666666666666"
	edited.Predecessor = headB.ID
	if err := db.AdmitAcceptanceDecision(ctx, edited); err != nil {
		t.Fatal(err)
	}
	control, err := db.IssueControl(ctx, fx.repo, 5)
	if err != nil {
		t.Fatal(err)
	}
	control.Acceptance = edited.ID
	control.Fingerprint = strings.Repeat("3", 64)
	if _, changed, err := db.RecordIssueAssessment(ctx, control, time.Now()); err != nil || !changed {
		t.Fatalf("re-queue = %v %v", changed, err)
	}
	third := DispatchPass(ctx, fx.deps())
	if len(third.Launched) != 0 || waitReason(third, 5) != WaitAllowance {
		t.Fatalf("third pass = %+v %+v", third.Launched, third.Waits)
	}
	if total, _ := db.UsageTotal(ctx, fx.repo, "conn"); total != 1 {
		t.Fatalf("usage total after edit = %d", total)
	}

	// A withdrawal and authorized resume likewise change no usage.
	coord := &Coordinator{Store: db}
	if _, _, err := coord.withdrawAndStopRuns(ctx, fx.repo, "test", "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := coord.ResumeRepository(ctx, fx.repo); err != nil {
		t.Fatal(err)
	}
	fourth := DispatchPass(ctx, fx.deps())
	if len(fourth.Launched) != 0 || waitReason(fourth, 5) != WaitAllowance {
		t.Fatalf("fourth pass = %+v %+v", fourth.Launched, fourth.Waits)
	}
	if total, _ := db.UsageTotal(ctx, fx.repo, "conn"); total != 1 {
		t.Fatalf("usage total after resume = %d", total)
	}
}

func TestRecordConfirmedUsagePreservesFractionalIntervalAndFirstWrite(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	start := time.Date(2026, 10, 1, 0, 0, 0, 123456789, time.FixedZone("test", 2*60*60))
	run := factory.Run{ID: factory.NewID(), Started: start}
	a := factory.Assignment{Repository: fx.repo, Connection: "conn", ActorID: 7}
	end := start.Add(time.Minute + time.Nanosecond)
	if err := recordConfirmedUsage(ctx, db, a, run, end); err != nil {
		t.Fatal(err)
	}
	if err := recordConfirmedUsage(ctx, db, a, run, end.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := db.RunUsage(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Minutes != 2 || !got.StartedAt.Equal(start) || !got.EndedAt.Equal(end) || got.StartedAt.Location() != time.UTC || got.EndedAt.Location() != time.UTC {
		t.Fatalf("usage interval lost precision or replay changed first write: %+v", got)
	}
}

func TestDispatchInterruptionReleasesOnlyUnused(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	head := fx.accept(t, 3, "d333333333333333333333333")
	fx.queue(t, 3, head.ID)
	fx.host.launch = func(in project.FactoryLaunch) (project.FactoryState, error) {
		return project.FactoryState{}, errors.New("host down")
	}

	first := DispatchPass(ctx, fx.deps())
	if len(first.Launched) != 0 || len(first.Waits) != 1 || first.Waits[0].Reason != WaitLaunchRefused {
		t.Fatalf("first pass = %+v %+v %+v", first.Launched, first.Waits, first.Errors)
	}
	assignment, err := db.LatestIssueAssignment(ctx, fx.repo, 3)
	if err != nil {
		t.Fatalf("latest assignment: %v", err)
	}
	if assignment.Attempts != 1 || assignment.Stage != factory.AssignmentAssigned {
		t.Fatalf("assignment = %+v", assignment)
	}
	run, err := db.FactoryRun(ctx, assignment.Run)
	if err != nil || !run.Reconciled || run.Outcome != factory.Failed {
		t.Fatalf("run = %+v %v", run, err)
	}
	reservation, err := db.Reservation(ctx, assignment.ID)
	if err != nil || reservation.State != factory.ReservationReleased {
		t.Fatalf("reservation = %+v %v", reservation, err)
	}
	if total, _ := db.UsageTotal(ctx, fx.repo, "conn"); total != 0 {
		t.Fatalf("usage total = %d", total)
	}

	second := DispatchPass(ctx, fx.deps())
	if len(second.Launched) != 0 || waitReason(second, 3) != WaitLaunchRefused {
		t.Fatalf("second pass = %+v %+v", second.Launched, second.Waits)
	}
	third := DispatchPass(ctx, fx.deps())
	if len(third.Launched) != 0 || waitReason(third, 3) != WaitLaunchExhausted {
		t.Fatalf("third pass = %+v %+v %+v", third.Launched, third.Waits, third.Errors)
	}
	done, err := db.Assignment(ctx, assignment.ID)
	if err != nil || done.Stage != factory.AssignmentFinished || done.Outcome != factory.Failed || done.Attempts != 3 {
		t.Fatalf("assignment = %+v %v", done, err)
	}
	if len(done.RunHistory) != 3 {
		t.Fatalf("run history = %v", done.RunHistory)
	}
	// A fourth pass finds the terminal attempt recorded, not new work.
	fourth := DispatchPass(ctx, fx.deps())
	if len(fourth.Waits) != 1 || fourth.Waits[0].Reason != WaitAttemptRecorded {
		t.Fatalf("fourth pass = %+v %+v", fourth.Launched, fourth.Waits)
	}
}

func TestExplicitRetryCreatesFreshAttempt(t *testing.T) {
	ctx := context.Background()
	db, dsn := dispatchTestDB(t)
	fixtureDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer fixtureDB.Close()
	fx := dispatchSeed(t, db)
	head := fx.accept(t, 3, "d333333333333333333333333")
	fx.queue(t, 3, head.ID)
	fx.host.launch = func(project.FactoryLaunch) (project.FactoryState, error) {
		return project.FactoryState{}, errors.New("host down")
	}
	for i := 0; i < factory.MaxDispatchAttempts; i++ {
		report := DispatchPass(ctx, fx.deps())
		if len(report.Errors) != 0 {
			t.Fatalf("failed unused attempt %d: %+v", i+1, report.Errors)
		}
	}
	old, err := db.LatestIssueAssignment(ctx, fx.repo, 3)
	if err != nil {
		t.Fatalf("settled assignment: %v", err)
	}
	if old.Stage != factory.AssignmentFinished || old.Outcome != factory.Failed || old.Attempts != factory.MaxDispatchAttempts || len(old.RunHistory) != factory.MaxDispatchAttempts {
		t.Fatalf("old attempts did not settle at their bound: %+v", old)
	}
	fourth := DispatchPass(ctx, fx.deps())
	if len(fourth.Waits) != 1 || fourth.Waits[0].Reason != WaitAttemptRecorded {
		t.Fatalf("settled retry remained dispatchable before explicit retry: %+v %+v", fourth.Launched, fourth.Waits)
	}
	oldHistory := append([]string(nil), old.RunHistory...)
	priorRun, err := db.FactoryRun(ctx, old.Run)
	if err != nil || !priorRun.Reconciled || priorRun.Outcome != factory.Failed {
		t.Fatalf("latest failed run: %+v %v", priorRun, err)
	}
	oldAllowance, err := db.AttemptAllowance(ctx, fx.repo, 3)
	if err != nil {
		t.Fatalf("previous attempt allowance: %v", err)
	}
	policy, err := db.RepositoryPolicy(ctx, fx.repo)
	if err != nil {
		t.Fatalf("repository policy: %v", err)
	}
	policy.AttemptLimits.ActiveMinutes = 45
	if err := db.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatalf("lower current attempt allowance: %v", err)
	}

	var retriedRun string
	fx.host.launch = func(in project.FactoryLaunch) (project.FactoryState, error) {
		retriedRun = in.Run.ID
		return project.FactoryState{ID: in.Run.ID, Project: in.Run.Project, Role: in.Run.Role, Phase: project.FactoryCompleted}, nil
	}
	coord := &Coordinator{Store: db}
	commandID := factory.NewID()
	decision, err := coord.RetryRun(ctx, commandID, "native:7", priorRun.ID)
	if err != nil || !decision.Queued || decision.Prior != priorRun.ID {
		t.Fatalf("explicit retry: %+v %v", decision, err)
	}
	admitted, err := db.FactoryCommand(ctx, commandID)
	if err != nil || admitted.Finished == "" || admitted.Outcome != "" {
		t.Fatalf("retry retained a response or was not admitted: %+v %v", admitted, err)
	}

	launchesBeforeRetry := len(fx.host.launches)
	report := DispatchPass(ctx, fx.deps())
	if len(report.Launched) != 1 || len(fx.host.launches) != launchesBeforeRetry+1 {
		t.Fatalf("fresh explicit retry was not launched once: launched=%+v waits=%+v errors=%+v calls=%d", report.Launched, report.Waits, report.Errors, len(fx.host.launches))
	}
	fresh, err := db.Assignment(ctx, commandID)
	if err != nil {
		t.Fatalf("fresh retry assignment: %v", err)
	}
	if fresh.ID != commandID || fresh.AttemptRoot != fresh.ID || fresh.PublicationAssignment != fresh.ID || fresh.Run == old.Run || fresh.Acceptance != head.ID || fresh.Repository != fx.repo || fresh.Issue != 3 || fresh.Attempts != 1 || len(fresh.RunHistory) != 1 || fresh.RunHistory[0] != fresh.Run {
		t.Fatalf("fresh retry assignment did not bind current accepted work: %+v", fresh)
	}
	freshRun, err := db.FactoryRun(ctx, fresh.Run)
	if err != nil || freshRun.Reconciled || freshRun.Outcome != "" {
		t.Fatalf("fresh retry run: %+v %v", freshRun, err)
	}
	reservation, err := db.Reservation(ctx, fresh.ID)
	if err != nil || reservation.State != factory.ReservationHeld || reservation.Connection != "conn" || reservation.PlannedMinutes <= 0 {
		t.Fatalf("fresh retry allowance reservation: %+v %v", reservation, err)
	}
	freshAllowance, err := db.AttemptAllowance(ctx, fx.repo, 3)
	if err != nil || freshAllowance.RootAssignment != commandID || freshAllowance.Limits.ActiveMinutes != 45 || !freshAllowance.Active || freshAllowance.Closed {
		t.Fatalf("fresh retry root allowance: %+v %v", freshAllowance, err)
	}
	if oldAllowance.RootAssignment == freshAllowance.RootAssignment || oldAllowance.Limits.ActiveMinutes != factory.DefaultAttemptLimits().ActiveMinutes {
		t.Fatalf("explicit retry did not create a fresh policy allowance: old=%+v fresh=%+v", oldAllowance, freshAllowance)
	}
	if currentOld, err := db.Assignment(ctx, old.ID); err != nil || currentOld.Stage != factory.AssignmentFinished || len(currentOld.RunHistory) != len(oldHistory) {
		t.Fatalf("old attempt history changed: %+v %v", currentOld, err)
	} else {
		for i := range oldHistory {
			if currentOld.RunHistory[i] != oldHistory[i] {
				t.Fatalf("old run history changed: %v", currentOld.RunHistory)
			}
		}
	}
	publication := factory.Publication{AssignmentID: old.ID, Repository: fx.repo, Issue: 3}
	linked, err := coord.assignmentForPublication(ctx, publication)
	if err != nil || linked.ID != old.ID {
		t.Fatalf("publication lookup did not return its exact older assignment: %+v %v", linked, err)
	}
	publication.Issue++
	if _, err = coord.assignmentForPublication(ctx, publication); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("publication lookup accepted mismatched issue: %v", err)
	}
	if total, err := db.UsageTotal(ctx, fx.repo, "conn"); err != nil || total != 0 {
		t.Fatalf("confirmed-unused attempts consumed allowance: total=%d err=%v", total, err)
	}

	fx.host.inspect = func(in project.FactoryInspect) (project.FactoryState, error) {
		if in.ID != retriedRun {
			return project.FactoryState{}, errors.New("unexpected run inspection")
		}
		return project.FactoryState{ID: in.ID, Project: in.Project, Role: project.RoleCoder, Phase: project.FactoryRunning}, nil
	}
	launchCount := len(fx.host.launches)
	replay, err := coord.RetryRun(ctx, commandID, "native:7", priorRun.ID)
	if err != nil || replay != decision || len(fx.host.launches) != launchCount {
		t.Fatalf("retry replay duplicated fresh launch: replay=%+v err=%v launches=%d/%d", replay, err, len(fx.host.launches), launchCount)
	}
	latest, err := db.LatestIssueAssignment(ctx, fx.repo, 3)
	if err != nil || latest.ID != commandID {
		t.Fatalf("retry replay replaced the consumed assignment: %+v %v", latest, err)
	}
	again := DispatchPass(ctx, fx.deps())
	if len(again.Launched) != 0 || len(fx.host.launches) != launchCount {
		t.Fatalf("replayed retry launched again: %+v calls=%d", again, len(fx.host.launches))
	}

	// Grow real command and assignment history beyond the former issue-list
	// page. Each retry uses the ordinary command, dispatch and accounting
	// paths, so targeted reads still work after substantial history growth.
	currentRun := freshRun
	currentCommand := commandID
	retainedAssignmentIDs := []string{old.ID, commandID}
	for i := 0; i < factory.MaxDispatchAttempts+8; i++ {
		settledRun := currentRun
		settledRun.Outcome, settledRun.Summary, settledRun.Reconciled = factory.Failed, "worker failed", true
		var retainedResult []byte
		if i < 2 {
			// A native process exit cannot stand in for an accepted worker report.
			settledRun.Outcome, settledRun.Summary = factory.Succeeded, "native exit zero without an accepted report"
		}
		if err := db.SaveFactoryRun(ctx, settledRun); err != nil {
			t.Fatalf("settle history run %d: %v", i, err)
		}
		if i < 2 {
			var originalSettlement sql.NullString
			if err := fixtureDB.QueryRowContext(ctx, `SELECT data->>'settled_at' FROM factory_runs WHERE id=$1`, settledRun.ID).Scan(&originalSettlement); err != nil {
				t.Fatal(err)
			}
			for pass := 0; pass < 3; pass++ {
				if recovered := RecoverDispatch(ctx, fx.deps()); len(recovered.Errors) != 0 {
					t.Fatalf("native accounting recovery: %+v", recovered.Errors)
				}
				if pass == 0 {
					first, err := db.Assignment(ctx, currentCommand)
					if err != nil {
						t.Fatal(err)
					}
					retainedResult, _ = json.Marshal(first.Result)
				}
			}
			usage, usageErr := db.RunUsage(ctx, settledRun.ID)
			reservation, reservationErr := db.Reservation(ctx, currentCommand)
			if usageErr != nil || reservationErr != nil || reservation.State != factory.ReservationConsumed || usage.Minutes < 0 {
				t.Fatal("native success without a report lost accounting", usageErr, reservationErr, reservation.State)
			}
			if err := db.SaveFactoryRun(ctx, settledRun); err != nil {
				t.Fatal(err)
			}
			var replayedSettlement sql.NullString
			if err := fixtureDB.QueryRowContext(ctx, `SELECT data->>'settled_at' FROM factory_runs WHERE id=$1`, settledRun.ID).Scan(&replayedSettlement); err != nil || replayedSettlement != originalSettlement {
				t.Fatal("run replay replaced the original settlement clock", err)
			}
			AccountSettledRun(ctx, db, settledRun, time.Now().Add(time.Hour))
			replayed, err := db.RunUsage(ctx, settledRun.ID)
			if err != nil || !reflect.DeepEqual(usage, replayed) {
				t.Fatal("accounting replay changed actual usage", err)
			}
		}
		finished, ok := AccountSettledRun(ctx, db, settledRun, time.Now())
		if i < 2 {
			finished, err = db.Assignment(ctx, currentCommand)
			if err != nil || ok {
				t.Fatal("accounting replay reported a new domain finish", err)
			}
		} else if !ok {
			t.Fatalf("account history run %d", i)
		}
		if i < 2 && (finished.Outcome != factory.NeedsHuman || finished.Reason != factory.AssignReasonNoReport || finished.Result == nil || finished.Result.Reported) {
			t.Fatalf("native success without a report was treated as completed work: %+v", finished)
		}
		if i < 2 {
			currentResult, _ := json.Marshal(finished.Result)
			if string(currentResult) != string(retainedResult) {
				t.Fatal("accounting replay replaced the retained native result")
			}
		}
		currentCommand = factory.NewID()
		retainedAssignmentIDs = append(retainedAssignmentIDs, currentCommand)
		if _, err := coord.RetryRun(ctx, currentCommand, "native:7", settledRun.ID); err != nil {
			t.Fatalf("queue history retry %d: %v", i, err)
		}
		if i < 2 {
			retainedRun, runErr := db.FactoryRun(ctx, settledRun.ID)
			retainedAssignment, assignmentErr := db.Assignment(ctx, finished.ID)
			if runErr != nil || assignmentErr != nil || retainedRun.Outcome != factory.Succeeded || retainedAssignment.Outcome != factory.NeedsHuman || retainedAssignment.Reason != factory.AssignReasonNoReport {
				t.Fatal("owner Retry rewrote native success or the fenced report", runErr, assignmentErr)
			}
		}
		retryReport := DispatchPass(ctx, fx.deps())
		if len(retryReport.Launched) != 1 {
			t.Fatalf("launch history retry %d: %+v %+v", i, retryReport.Launched, retryReport.Waits)
		}
		currentRun, err = db.FactoryRun(ctx, retryReport.Launched[0].RunID)
		if err != nil || currentRun.ID == settledRun.ID {
			t.Fatalf("history retry run %d: %+v %v", i, currentRun, err)
		}
	}
	latest, err = db.LatestIssueAssignment(ctx, fx.repo, 3)
	if err != nil || latest.ID != currentCommand {
		t.Fatalf("latest assignment after bounded history growth: %+v %v", latest, err)
	}
	if len(retainedAssignmentIDs) != 13 {
		t.Fatalf("history rows = %d, want 13", len(retainedAssignmentIDs))
	}
	for _, id := range retainedAssignmentIDs {
		assignment, err := db.Assignment(ctx, id)
		if err != nil || assignment.ID != id {
			t.Fatalf("historical assignment %s was not retained: %+v %v", id, assignment, err)
		}
	}
	publication = factory.Publication{AssignmentID: latest.ID, Repository: fx.repo, Issue: 3}
	linked, err = coord.assignmentForPublication(ctx, publication)
	if err != nil || linked.ID != latest.ID {
		t.Fatalf("publication lookup missed assignment beyond bounded page: %+v %v", linked, err)
	}
	publication.Repository++
	if _, err = coord.assignmentForPublication(ctx, publication); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("publication lookup accepted mismatched repository: %v", err)
	}
}

func TestExplicitRetryRejectsPartialAccounting(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	head := fx.accept(t, 3, "d333333333333333333333333")
	fx.queue(t, 3, head.ID)
	launched := DispatchPass(ctx, fx.deps())
	if len(launched.Launched) != 1 {
		t.Fatalf("initial attempt: %+v %+v", launched.Launched, launched.Waits)
	}
	prior, err := db.FactoryRun(ctx, launched.Launched[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := db.AssignmentByRun(ctx, prior.ID)
	if err != nil {
		t.Fatal(err)
	}
	prior.Outcome, prior.Summary, prior.Reconciled = factory.Failed, "worker failed", true
	if err = db.SaveFactoryRun(ctx, prior); err != nil {
		t.Fatal(err)
	}
	if err = db.RecordRunUsage(ctx, factory.Usage{
		RunID: prior.ID, Repository: fx.repo, Connection: "conn", StartedAt: prior.Started, EndedAt: prior.Started,
	}); err != nil {
		t.Fatal(err)
	}
	coord := &Coordinator{Store: db}
	commandID := factory.NewID()
	if _, err = coord.RetryRun(ctx, commandID, "native:7", prior.ID); err == nil {
		t.Fatal("explicit retry accepted an unsettled linked assignment")
	}
	if _, err = db.FactoryCommand(ctx, commandID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unsettled retry left a durable command: %v", err)
	}
	if err = finishFromRun(ctx, db, assignment, prior, time.Now()); err != nil {
		t.Fatal(err)
	}
	reservation, err := db.Reservation(ctx, assignment.ID)
	if err != nil || reservation.State != factory.ReservationHeld {
		t.Fatalf("partial accounting setup: %+v %v", reservation, err)
	}
	commandID = factory.NewID()
	if _, err = coord.RetryRun(ctx, commandID, "native:7", prior.ID); err == nil {
		t.Fatal("explicit retry accepted usage without the matching consumed reservation")
	}
	if _, err = db.FactoryCommand(ctx, commandID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("partial retry left a durable command: %v", err)
	}
}

func TestDispatchApprovedReceiptReleasesWhenBrokerUnknown(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	head := fx.accept(t, 3, "d333333333333333333333333")
	fx.queue(t, 3, head.ID)
	fx.host.launch = func(in project.FactoryLaunch) (project.FactoryState, error) {
		return project.FactoryState{}, errors.New("acquire refused")
	}
	fx.host.inspect = func(in project.FactoryInspect) (project.FactoryState, error) {
		return project.FactoryState{ID: in.ID, Project: in.Project, Phase: project.FactoryApproved}, nil
	}
	report := DispatchPass(ctx, fx.deps())
	if len(report.Launched) != 0 || waitReason(report, 3) != WaitLaunchRefused {
		t.Fatalf("report = %+v %+v %+v", report.Launched, report.Waits, report.Errors)
	}
	assignment, err := db.LatestIssueAssignment(ctx, fx.repo, 3)
	if err != nil {
		t.Fatalf("latest assignment: %v", err)
	}
	if reservation, _ := db.Reservation(ctx, assignment.ID); reservation.State != factory.ReservationReleased {
		t.Fatalf("reservation = %+v", reservation)
	}
	if total, _ := db.UsageTotal(ctx, fx.repo, "conn"); total != 0 {
		t.Fatalf("phantom usage = %d", total)
	}
}

func TestDispatchApprovedReceiptFencesWhenBrokerHolds(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	head := fx.accept(t, 3, "d333333333333333333333333")
	fx.queue(t, 3, head.ID)
	fx.host.launch = func(in project.FactoryLaunch) (project.FactoryState, error) {
		return project.FactoryState{}, errors.New("launch outcome unconfirmed")
	}
	fx.host.inspect = func(in project.FactoryInspect) (project.FactoryState, error) {
		return project.FactoryState{ID: in.ID, Project: in.Project, Phase: project.FactoryApproved}, nil
	}
	fx.broker.get = func(kind, id string) (identity.Execution, error) {
		return identity.Execution{Kind: kind, ExecutionID: id, State: identity.ExecutionLive, Digest: strings.Repeat("d", 64)}, nil
	}
	report := DispatchPass(ctx, fx.deps())
	if len(report.Errors) != 1 || report.Errors[0].Reason != DispatchErrFence {
		t.Fatalf("report = %+v %+v %+v", report.Launched, report.Waits, report.Errors)
	}
	assignment, err := db.LatestIssueAssignment(ctx, fx.repo, 3)
	if err != nil {
		t.Fatalf("latest assignment: %v", err)
	}
	if reservation, _ := db.Reservation(ctx, assignment.ID); reservation.State != factory.ReservationHeld {
		t.Fatalf("reservation = %+v", reservation)
	}
}

func TestDispatchFencedLaunchStaysHeld(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	head := fx.accept(t, 3, "d333333333333333333333333")
	fx.queue(t, 3, head.ID)
	fx.host.launch = func(in project.FactoryLaunch) (project.FactoryState, error) {
		return project.FactoryState{}, errors.New("launch outcome unconfirmed")
	}
	fx.broker.get = func(kind, id string) (identity.Execution, error) {
		return identity.Execution{Kind: kind, ExecutionID: id, State: identity.ExecutionLive, Digest: strings.Repeat("d", 64)}, nil
	}
	report := DispatchPass(ctx, fx.deps())
	if len(report.Launched) != 0 || len(report.Errors) != 1 || report.Errors[0].Reason != DispatchErrFence {
		t.Fatalf("report = %+v %+v %+v", report.Launched, report.Waits, report.Errors)
	}
	assignment, err := db.LatestIssueAssignment(ctx, fx.repo, 3)
	if err != nil {
		t.Fatalf("latest assignment: %v", err)
	}
	run, err := db.FactoryRun(ctx, assignment.Run)
	if err != nil || run.Reconciled {
		t.Fatalf("fenced run settled: %+v %v", run, err)
	}
	reservation, err := db.Reservation(ctx, assignment.ID)
	if err != nil || reservation.State != factory.ReservationHeld {
		t.Fatalf("reservation = %+v %v", reservation, err)
	}
}

func TestAccountSettledRunResults(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*store.Store, factory.Assignment, factory.Run) {
		t.Helper()
		db, _ := dispatchTestDB(t)
		fx := dispatchSeed(t, db)
		head := fx.accept(t, 3, "d333333333333333333333333")
		fx.queue(t, 3, head.ID)
		report := DispatchPass(ctx, fx.deps())
		if len(report.Launched) != 1 {
			t.Fatalf("launched = %+v", report.Launched)
		}
		a, err := db.Assignment(ctx, report.Launched[0].AssignmentID)
		if err != nil {
			t.Fatal(err)
		}
		run, err := db.FactoryRun(ctx, report.Launched[0].RunID)
		if err != nil {
			t.Fatal(err)
		}
		return db, a, run
	}
	settle := func(t *testing.T, db *store.Store, run factory.Run, outcome factory.Outcome) factory.Run {
		t.Helper()
		run.Outcome, run.Summary, run.Reconciled = outcome, "host says so", true
		if err := db.SaveFactoryRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		return run
	}
	setupReviewer := func(t *testing.T) (*store.Store, factory.Assignment, factory.Run, string) {
		t.Helper()
		fx, owner, _, run := reviewRunSeed(t, false)
		view, err := fx.db.FactoryRunView(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		assignment, err := fx.db.Assignment(ctx, view.Attempt)
		if err != nil {
			t.Fatal(err)
		}
		p, err := fx.db.PublicationByAssignment(ctx, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		cycleID := ""
		for _, cycle := range p.ReviewCycles {
			if cycle.ReviewerAssignment == assignment.ID && cycle.CorrectionAssignment == "" {
				cycleID = cycle.ID
			}
		}
		if cycleID == "" {
			t.Fatalf("reviewer admitted no review cycle: reviewer=%s", assignment.ID)
		}
		return fx.db, assignment, run, cycleID
	}

	t.Run("reported investigation plan", func(t *testing.T) {
		db, a, run := setup(t)
		run = settle(t, db, run, factory.Succeeded)
		// The dispatched assignment is a phased investigation: only a
		// strict v2 phase result reports, and completed planning never
		// names a publishable candidate.
		plan := phaseTestPlan(t, a.AttemptRoot, a.SourceCommit, a.Preparation)
		output := phaseFinish(t, factory.PhaseResult{
			Version: factory.PhaseResultVersion, Phase: project.ExecutionInvestigate, Status: "completed",
			Summary: "plan ready", Output: &factory.PhaseOutput{Plan: &plan},
		})
		finished, ok := accountRunFinish(t, ctx, db, run, output, time.Now())
		if !ok || finished.Outcome != factory.Succeeded || finished.Reason != factory.AssignReasonReported {
			t.Fatalf("accounted = %+v %v", finished, ok)
		}
		if finished.Result == nil || !finished.Result.Reported || finished.Result.Candidate != "" {
			t.Fatalf("result = %+v", finished.Result)
		}
		if r, _ := db.Reservation(ctx, a.ID); r.State != factory.ReservationConsumed {
			t.Fatalf("reservation = %+v", r)
		}
		if total, _ := db.UsageTotal(ctx, 7, "conn"); total < 1 {
			t.Fatalf("usage = %d", total)
		}
		if again, ok := accountRunFinish(t, ctx, db, run, output, time.Now()); ok || again.Stage != "" {
			t.Fatalf("second accounting = %+v %v", again, ok)
		}
	})
	t.Run("completed without a finish call settles incomplete", func(t *testing.T) {
		db, _, run := setup(t)
		run = settle(t, db, run, factory.Succeeded)
		finished, ok := AccountSettledRun(ctx, db, run, time.Now())
		if !ok || finished.Outcome != factory.NeedsHuman || finished.Result.Status != "blocked" ||
			!strings.Contains(finished.Result.Summary, string(factory.MCPToolFinishIssuePlan)) {
			t.Fatalf("accounted = %+v %v", finished, ok)
		}
	})
	t.Run("failed run", func(t *testing.T) {
		db, _, run := setup(t)
		run = settle(t, db, run, factory.Failed)
		finished, ok := AccountSettledRun(ctx, db, run, time.Now())
		if !ok || finished.Outcome != factory.Failed || finished.Result.Status != "failed" {
			t.Fatalf("accounted = %+v %v", finished, ok)
		}
	})
	t.Run("cancelled run", func(t *testing.T) {
		db, _, run := setup(t)
		run = settle(t, db, run, factory.Cancelled)
		finished, ok := AccountSettledRun(ctx, db, run, time.Now())
		if !ok || finished.Outcome != factory.Cancelled || finished.Result.Status != "cancelled" {
			t.Fatalf("accounted = %+v %v", finished, ok)
		}
	})
	t.Run("unassigned run ignored", func(t *testing.T) {
		db, _, _ := setup(t)
		human := factory.Run{
			ID: factory.NewID(), ProjectID: "p765432109876543210987654", Role: project.RoleCoder,
			InputSHA: strings.Repeat("c", 40), Started: time.Now(), Deadline: time.Now().Add(time.Hour),
			Image: "sha256:" + strings.Repeat("b", 64), Harness: "codex-1.2.3", Model: "m",
			Outcome: factory.Succeeded, Summary: "ok", Reconciled: true,
		}
		if _, ok := AccountSettledRun(ctx, db, human, time.Now()); ok {
			t.Fatal("unassigned run accounted")
		}
	})
	t.Run("reviewer approve", func(t *testing.T) {
		db, assignment, run, cycleID := setupReviewer(t)
		run = settle(t, db, run, factory.Succeeded)
		// The phased reviewer reports a strict v2 explicit clear: no
		// candidate, no legacy review report, head bound to its source.
		output := phaseFinish(t, factory.PhaseResult{
			Version: factory.PhaseResultVersion, Phase: project.ExecutionReview, Status: "completed",
			Summary: "No blocking findings.",
			Output: &factory.PhaseOutput{Clear: &factory.ClearReviewReport{
				Conclusion: factory.ClearReviewConclusion, CycleID: cycleID,
				Head: assignment.SourceCommit, BaseOID: strings.Repeat("f", 40),
				CoverageDigest: strings.Repeat("c", 64),
			}},
		})
		finished, ok := accountRunFinish(t, ctx, db, run, output, time.Now())
		if !ok || finished.Outcome != factory.Succeeded || finished.Result == nil || !finished.Result.Reported || finished.Result.PhaseResult == nil || finished.Result.PhaseResult.Output.Clear == nil {
			t.Fatalf("reviewer result = %+v, %v", finished, ok)
		}
		if finished.Result.Candidate != "" || finished.Result.PhaseResult.Output.Clear.Head != assignment.SourceCommit {
			t.Fatalf("reviewer candidate/report = %+v", finished.Result)
		}
		if reservation, err := db.Reservation(ctx, assignment.ID); err != nil || reservation.State != factory.ReservationConsumed {
			t.Fatalf("review reservation = %+v, %v", reservation, err)
		}
		if total, err := db.UsageTotal(ctx, assignment.Repository, assignment.Connection); err != nil || total < 1 {
			t.Fatalf("review usage = %d, %v", total, err)
		}
		if replay, err := db.Assignment(ctx, assignment.ID); err != nil || replay.Result == nil || !reflect.DeepEqual(replay.Result.PhaseResult, finished.Result.PhaseResult) {
			t.Fatalf("persisted reviewer result = %+v, %v", replay.Result, err)
		}
		if replayed, ok := accountRunFinish(t, ctx, db, run, output, time.Now()); ok || replayed.Stage != "" {
			t.Fatalf("review accounting replay = %+v, %v", replayed, ok)
		}
	})
	t.Run("reviewer request changes", func(t *testing.T) {
		db, assignment, run, cycleID := setupReviewer(t)
		run = settle(t, db, run, factory.Succeeded)
		// A blocking verdict carries the typed findings report over the
		// same strict v2 wire.
		finding := correctionFinding()
		output := phaseFinish(t, factory.PhaseResult{
			Version: factory.PhaseResultVersion, Phase: project.ExecutionReview, Status: "completed",
			Summary: "Fix the edge case.",
			Output: &factory.PhaseOutput{Review: &factory.ReviewCycleReport{
				CycleID: cycleID, NewFindings: []factory.Finding{finding},
			}},
		})
		finished, ok := accountRunFinish(t, ctx, db, run, output, time.Now())
		if !ok || finished.Outcome != factory.Succeeded || finished.Result == nil || !finished.Result.Reported || finished.Result.PhaseResult == nil || finished.Result.PhaseResult.Output.Review == nil {
			t.Fatalf("reviewer result = %+v, %v", finished, ok)
		}
		got := finished.Result.PhaseResult.Output.Review
		if len(got.NewFindings) != 1 || got.NewFindings[0].Key != finding.Key {
			t.Fatalf("reviewer candidate/report = %+v", finished.Result)
		}
		if reservation, err := db.Reservation(ctx, assignment.ID); err != nil || reservation.State != factory.ReservationConsumed {
			t.Fatalf("review reservation = %+v, %v", reservation, err)
		}
		if replayed, ok := accountRunFinish(t, ctx, db, run, output, time.Now()); ok || replayed.Stage != "" {
			t.Fatalf("review accounting replay = %+v, %v", replayed, ok)
		}
	})
}
