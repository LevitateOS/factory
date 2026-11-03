package store

import (
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/project"
)

func TestProjectMaintenanceWorkKeepsUncertainWorkVisible(t *testing.T) {
	s, ctx, _ := prepTestStore(t)
	preparation, _, err := s.AdmitPreparation(ctx, prepTestPreparation())
	if err != nil {
		t.Fatal(err)
	}
	run := inventoryRun(time.Now(), prepTestProject)
	if err = s.RecordFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	for _, state := range []execution.PreparationState{
		{},
		{Phase: project.PrepareRunning},
		{Phase: project.PrepareInterrupted},
		{Phase: project.PrepareFailed},
		{Phase: project.PrepareReady}, // Incomplete readiness is uncertain.
		{Phase: project.PrepareReady, Ready: true},
		{Phase: project.PrepareStopped, Stopped: true, Retirement: "uncertain"},
		{Phase: project.PrepareStopped, Stopped: true, Retirement: "confirmed"},
	} {
		if state.Phase != "" {
			state.ID, state.Project, state.Role = preparation.Preparation.ID, preparation.Preparation.Project, preparation.Preparation.Role
			state.SourceCommit, state.SetupDigest, state.Container = preparation.Preparation.SourceCommit, preparation.Preparation.SetupDigest, strings.Repeat("c", 64)
			preparation.State = state
			if state.Phase == project.PrepareReady && !state.Ready {
				if err = s.ObservePreparation(ctx, preparation); err == nil {
					t.Fatal("incomplete readiness was accepted")
				}
				preparation, err = s.Preparation(ctx, preparation.Preparation.ID)
				if err != nil {
					t.Fatal(err)
				}
				continue
			}
			if err = s.ObservePreparation(ctx, preparation); err != nil {
				t.Fatal(err)
			}
			preparation, err = s.Preparation(ctx, preparation.Preparation.ID)
			if err != nil {
				t.Fatal(err)
			}
		}
		want := 1
		if state.Ready || state.Stopped && state.Retirement == "confirmed" {
			want = 0
		}
		factoryWork, preparationWork, err := s.ProjectMaintenanceWork(ctx)
		if err != nil || factoryWork != 1 || preparationWork != want {
			t.Fatalf("phase %q counts = %d/%d, want 1/%d: %v", state.Phase, factoryWork, preparationWork, want, err)
		}
	}
	run.Outcome = factory.Cancelled
	run.Summary = "native work ended, reconciliation pending"
	if err = s.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if count, _, err := s.ProjectMaintenanceWork(ctx); err != nil || count != 1 {
		t.Fatalf("unsettled completion disappeared: %d %v", count, err)
	}
	run.Reconciled = true
	if err = s.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if count, preparations, err := s.ProjectMaintenanceWork(ctx); err != nil || count != 0 || preparations != 0 {
		t.Fatalf("settled work still blocks: %d/%d %v", count, preparations, err)
	}
	// Retained candidate failures keep their original check result. Their
	// separately recorded native stop receipt owns retirement, not that result.
	checkExit := 124
	// A separate candidate retains its check result and independent stop receipt.
	preparation.Preparation.ID = "f" + strings.Repeat("c", 24)
	preparation.Preparation.Revision = 0
	preparation.State = execution.PreparationState{ID: preparation.Preparation.ID, Project: prepTestProject, Role: preparation.Preparation.Role, Phase: project.PrepareFailed, CheckExit: &checkExit, Output: "retained failed check"}
	if _, _, err = s.AdmitPreparation(ctx, preparation); err != nil {
		t.Fatal(err)
	}
	meta := candidatePreparationRegistration{Repository: 7, Issue: 1, ActorID: 3, Project: prepTestProject, OwnerAssignment: factory.NewID(), ChildAssignment: factory.NewID(), AttemptRoot: factory.NewID(), PublicationID: factory.NewID(), Connection: "candidate-connection", NotAfter: time.Now().Add(time.Minute).Unix()}
	retained, err := marshalCandidatePreparation(preparation, meta)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE project_preparations SET data=$1 WHERE id=$2`, string(retained), preparation.Preparation.ID); err != nil {
		t.Fatal(err)
	}
	stop := execution.PreparationState{ID: preparation.Preparation.ID, Project: prepTestProject, Role: preparation.Preparation.Role, Phase: project.PrepareStopped, Stopped: true, Retirement: "uncertain"}
	if err = s.RecordCandidatePreparationStop(ctx, preparation.Preparation.ID, stop); err != nil {
		t.Fatal(err)
	}
	if _, count, err := s.ProjectMaintenanceWork(ctx); err != nil || count != 1 {
		t.Fatalf("uncertain candidate retirement disappeared: %d %v", count, err)
	}
	stop.Retirement = "confirmed"
	if err = s.RecordCandidatePreparationStop(ctx, preparation.Preparation.ID, stop); err != nil {
		t.Fatal(err)
	}
	if _, count, err := s.ProjectMaintenanceWork(ctx); err != nil || count != 0 {
		t.Fatalf("confirmed candidate retirement still blocks maintenance: %d %v", count, err)
	}
	kept, err := s.CandidatePreparation(ctx, preparation.Preparation.ID)
	if err != nil || !kept.Stopped || kept.Retirement != "confirmed" || kept.Preparation.State.Phase != project.PrepareFailed || kept.Preparation.State.Output != "retained failed check" || kept.Preparation.State.CheckExit == nil || *kept.Preparation.State.CheckExit != 124 {
		t.Fatalf("native retirement rewrote the retained failure: %+v %v", kept, err)
	}
	native := prepTestPreparation()
	native.Preparation.ID = "f" + strings.Repeat("a", 24)
	native.Native = &execution.NativeWorkspaceRef{Profile: collaboration.ProfileID(strings.Repeat("a", 32)), InstanceID: strings.Repeat("b", 32), WorktreeID: "native-maintenance-fixture"}
	if _, _, err := s.AdmitPreparation(ctx, native); err != nil {
		t.Fatal(err)
	}
	zero := 0
	for _, phase := range []string{project.PrepareReady, project.PrepareStopped, project.PrepareStopped} {
		retirement := ""
		if phase == project.PrepareStopped {
			retirement = "uncertain"
			if native.State.Stopped {
				retirement = "confirmed"
			}
		}
		native.State = execution.PreparationState{ID: native.Preparation.ID, Project: prepTestProject, Role: native.Preparation.Role, SourceCommit: native.Preparation.SourceCommit, SetupDigest: native.Preparation.SetupDigest, Phase: phase, Ready: phase == project.PrepareReady, Stopped: phase == project.PrepareStopped, Retirement: retirement, SetupExit: &zero, CheckExit: &zero, Native: native.Native}
		if err := s.ObservePreparation(ctx, native); err != nil {
			t.Fatal(err)
		}
		native, err = s.Preparation(ctx, native.Preparation.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := 1
		if retirement == "confirmed" {
			want = 0
		}
		if _, count, err := s.ProjectMaintenanceWork(ctx); err != nil || count != want {
			t.Fatalf("native phase=%s retirement=%s hid uncertain work: count=%d want=%d err=%v", phase, retirement, count, want, err)
		}
	}
}

func TestProjectMaintenanceWorkReportsSaturatedCount(t *testing.T) {
	s, ctx := inventoryFixture(t)
	for range 130 {
		if err := s.RecordFactoryRun(ctx, inventoryRun(time.Now(), prepTestProject)); err != nil {
			t.Fatal(err)
		}
	}
	if count, _, err := s.ProjectMaintenanceWork(ctx); err != nil || count != 129 {
		t.Fatalf("bounded count lost sentinel: %d %v", count, err)
	}
}
