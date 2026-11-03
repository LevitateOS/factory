package control

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

func TestMergedSourcePreparationAdoptsLostReplyBeforeProviderAdmission(t *testing.T) {
	mergedSourcePreparationScenario(t, false)
}

func TestExpiredReadySourcePreparationRefusesPacketAndRetiresCustody(t *testing.T) {
	mergedSourcePreparationScenario(t, true)
}

func TestMergedSourcePreparationUsesCommittedCorrectionRun(t *testing.T) {
	mergedSourcePreparationScenario(t, false, true)
}

// driveMergedCompletionContinue releases the scenario's merged completion
// through the D13 continue branch, opening the fence for the fresh issue.
// It judges substance and continues exactly like the completion supplement;
// release-branch decisions stay with their owning tests.
func driveMergedCompletionContinue(t *testing.T, fx *mergeFixture) {
	t.Helper()
	ctx := context.Background()
	db := fx.publish.db
	fx.publish.coord.Planning = &PlanningJudgments{Chooser: &continueChooser{}}
	deps := CompletionDriveDeps{Enumerator: twoPageEnumerator(t), CaptureLocal: db.CaptureCompletionLocal}
	for i := 0; i < 6; i++ {
		if report := fx.publish.coord.DriveCompletions(ctx, deps); len(report.Released) != 0 {
			return
		}
	}
	t.Fatal("merged completion did not release through the continue branch")
}

func commitSourceFixtureCorrection(t *testing.T, fx *mergeFixture, p factory.Publication) factory.Publication {
	t.Helper()
	ctx := context.Background()
	owner, err := fx.publish.db.Assignment(ctx, p.AssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	_, run := correctionChildFixture(t, fx.publish, owner, &p, true)
	tip := strings.Repeat("d", 40)
	accountChildResult(t, fx.publish, run, correctionOutput(tip))
	if report := fx.publish.coord.PublishCorrection(ctx, run.ID); len(report.Errors) != 0 || len(report.Corrected) != 1 {
		t.Fatalf("correction was not committed: %+v", report)
	}
	corrected, err := fx.publish.db.PublicationByAssignment(ctx, owner.ID)
	if err != nil || corrected.Run != run.ID || corrected.Candidate != tip {
		t.Fatal("publication did not retain its committed correction run", corrected, err)
	}
	checks, err := fx.publish.db.CheckAssessment(ctx, p.Repository, p.PRNumber)
	if err != nil {
		t.Fatal(err)
	}
	checks.HeadOID, checks.Revision = tip, 0
	if _, err := fx.publish.db.RecordCheckAssessment(ctx, checks); err != nil {
		t.Fatal(err)
	}
	return corrected
}

func mergedSourcePreparationScenario(t *testing.T, expireBeforePacket bool, correction ...bool) {
	t.Helper()
	ctx := context.Background()
	fx, p := mergeSeed(t, 3, map[string]string{"config/dependencies.lock": dispatchDigest("selected configuration")})
	if len(correction) > 0 && correction[0] {
		p = commitSourceFixtureCorrection(t, fx, p)
	}
	fx.wire(happyMerger())
	fx.pass(t)
	// The merge judgment gate holds the opened row for its authorization:
	// bind the ready first-generation fixture auth, then drive the merge
	// to completion. Later generations stay out of this scenario.
	if open := fx.merge(t, p); open.Stage == factory.MergeOpen {
		authorizeMergeFixture(t, ctx, fx.publish.db, fx.publish.coord, p)
		fx.pass(t)
	}
	if merge := fx.merge(t, p); merge.Stage != factory.MergeMerged {
		t.Fatal(merge)
	}
	// The merged completion engages the D13 fence until its reassessment
	// decision releases: drive the continue branch so the fresh issue
	// below admits. Releases follow reassessment decisions elsewhere;
	// this scenario only continues past its own merge.
	driveMergedCompletionContinue(t, fx)
	seed := fx.publish.seed
	fixtureDB, err := sql.Open("pgx", fx.publish.dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer fixtureDB.Close()
	head := seed.accept(t, 5, "d555555555555555555555555")
	seed.queue(t, 5, head.ID)
	input := seed.reads.inputs["7/5"]
	input.Tip = p.Candidate
	seed.reads.inputs["7/5"] = input
	var native execution.PreparationState
	seed.host.prepare = func(in project.FactoryCandidate) (execution.PreparationState, error) {
		if in.SourceRun == nil || in.SourceRun.ID != p.Run || in.SourceRun.Candidate != p.Candidate || in.SourcePreparation != "f111111111111111111111111" || len(in.Bundle) != 0 {
			t.Fatalf("unbound source refresh: %+v", in)
		}
		if in.Preparation.Requirements.SourceInputs["config/dependencies.lock"] != dispatchDigest("selected configuration") {
			t.Fatal("approved input selection was lost")
		}
		native = execution.PreparationState{ID: in.Preparation.ID, Project: seed.proj, Role: project.RoleCoder, SourceCommit: p.Candidate, SetupDigest: in.Preparation.SetupDigest, Container: strings.Repeat("c", 64), Phase: project.PrepareRunning}
		return execution.PreparationState{}, errors.New("lost native preparation reply")
	}
	seed.host.inspectPreparation = func(project.PrepareInspect) (execution.PreparationState, error) { return native, nil }
	seed.host.contextRead = func(in project.FactoryPreparationContextRequest, role string) (project.FactoryPreparationContext, error) {
		if !native.Ready || in.ID != native.ID || in.SourceCommit != p.Candidate {
			return project.FactoryPreparationContext{}, errors.New("fresh source is not confirmed")
		}
		if expireBeforePacket {
			if _, err := fixtureDB.ExecContext(ctx, `UPDATE project_preparations SET data=jsonb_set(data,'{factory_admission,not_after_unix}',to_jsonb($2::bigint)) WHERE id=$1`, in.ID, time.Now().Unix()-1); err != nil {
				t.Fatal(err)
			}
		}
		return fixturePreparationContext(in, role), nil
	}
	c := &Coordinator{Store: seed.db, Host: seed.host, Broker: seed.broker, DispatchReads: seed.reads}
	first := c.Dispatch(ctx)
	if len(first.Launched) != 0 || native.ID == "" {
		t.Fatalf("setup did not precede launch: %+v", first)
	}
	pending, err := seed.db.CandidatePreparation(ctx, native.ID)
	if err != nil {
		t.Fatal(err)
	}
	fixed := pending.NotAfter
	second := c.Dispatch(ctx)
	again, err := seed.db.CandidatePreparation(ctx, native.ID)
	if err != nil || again.NotAfter != fixed || len(second.Launched) != 0 || len(seed.host.preparations) != 1 {
		t.Fatal("lost reply duplicated source preparation or renewed deadline", second, err)
	}
	native.Phase, native.Ready = project.PrepareReady, true
	third := c.Dispatch(ctx)
	if expireBeforePacket {
		if len(third.Launched) != 0 || len(seed.host.launches) != 0 {
			t.Fatal("expired Ready custody launched a provider", third)
		}
		retired, err := seed.db.CandidatePreparation(ctx, native.ID)
		if err != nil || !retired.Stopped || retired.Retirement != "confirmed" {
			t.Fatal("expired private checkout was not retired", retired, err)
		}
		var assignments, runs, reservations int
		if err := fixtureDB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM factory_assignments WHERE repository=7 AND issue=5),(SELECT count(*) FROM factory_run_views WHERE repository=7 AND issue=5),(SELECT count(*) FROM factory_reservations r JOIN factory_assignments a ON a.id=r.assignment WHERE a.repository=7 AND a.issue=5)`).Scan(&assignments, &runs, &reservations); err != nil || assignments != 0 || runs != 0 || reservations != 0 {
			t.Fatal("expired transfer retained admission rows", assignments, runs, reservations, err)
		}
		return
	}
	if len(third.Launched) != 1 {
		t.Fatalf("confirmed source was not admitted: %+v", third)
	}
	assignment, err := seed.db.Assignment(ctx, third.Launched[0].AssignmentID)
	if err != nil || assignment.ID != pending.Admission.OwnerAssignment || assignment.Preparation != native.ID || assignment.SourceCommit != p.Candidate {
		t.Fatal("fresh custody was not transferred to exact assignment", assignment, err)
	}
	old, err := seed.db.Preparation(ctx, "f111111111111111111111111")
	if err != nil || old.Preparation.SourceCommit != seed.tip {
		t.Fatal("standing preparation was overwritten", old, err)
	}
	altered := again.Preparation
	altered.State = execution.PreparationState{}
	altered.Preparation.Requirements.SourceInputs["config/dependencies.lock"] = strings.Repeat("e", 64)
	if _, _, err := seed.db.AdmitCandidatePreparation(ctx, altered, again.Admission); err == nil {
		t.Fatal("same requirement ID admitted a changed input selector")
	}
	if _, err := seed.db.MergedPreparationSource(ctx, seed.repo, seed.proj, "main", strings.Repeat("e", 40)); err == nil {
		t.Fatal("unattributable target tip supplied a source export")
	}
}

func TestServingSourceAdvanceWithoutCompleteSelectionRetainsHumanApproval(t *testing.T) {
	ctx := context.Background()
	db, _ := dispatchTestDB(t)
	seed := dispatchSeed(t, db)
	head := seed.accept(t, 5, "d555555555555555555555555")
	seed.queue(t, 5, head.ID)
	input := seed.reads.inputs["7/5"]
	input.Tip = strings.Repeat("e", 40)
	seed.reads.inputs["7/5"] = input
	visited := make(chan struct{}, 1)
	harness := seed.host.harness
	seed.host.harness = func(family string) (project.FactoryHarnessPin, error) {
		select {
		case visited <- struct{}{}:
		default:
		}
		return harness(family)
	}
	c := NewCoordinator(db, seed.host, seed.broker)
	c.DispatchReads = seed.reads
	if err := c.Start(ctx, filepath.Join(t.TempDir(), "coordinator.lock")); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	select {
	case <-visited:
	case <-time.After(15 * time.Second):
		t.Fatal("serving owner never revisited the queued dependent")
	}
	// Wait for this observed owner pass to finish; no test drives a stage.
	c.progressMu.Lock()
	c.progressMu.Unlock()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if len(seed.host.preparations) != 0 || len(seed.host.launches) != 0 {
		t.Fatal("unknown effective selection reached setup or provider")
	}
	if _, err := db.LatestIssueAssignment(ctx, seed.repo, 5); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("refused source advance fabricated an assignment", err)
	}
	if got, err := db.AcceptanceHead(ctx, seed.repo, 5); err != nil || got != head.ID {
		t.Fatal("source refresh replaced human objective", got, err)
	}
}
