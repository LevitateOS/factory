package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

func consumedCoderRetry(t *testing.T) (dispatchFixture, *Coordinator, string, factory.AttemptAllowance, string) {
	t.Helper()
	ctx := context.Background()
	db, dsn := dispatchTestDB(t)
	fx := dispatchSeed(t, db)
	head := fx.accept(t, 3, "d333333333333333333333333")
	fx.queue(t, 3, head.ID)
	first := DispatchPass(ctx, fx.deps())
	if len(first.Launched) != 1 {
		t.Fatalf("first dispatch: %+v", first)
	}
	run, err := db.FactoryRun(ctx, first.Launched[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	run.Outcome, run.Reconciled = factory.Succeeded, true
	if err := db.SaveFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	// The dispatched assignment is a phased investigation: only a strict
	// v2 phase result reports. A legacy harness shape would refuse and
	// leave the prior NeedsHuman, which retry eligibility rejects.
	prior, err := db.Assignment(ctx, first.Launched[0].AssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	plan := phaseTestPlan(t, prior.AttemptRoot, prior.SourceCommit, prior.Preparation)
	output := phaseFinish(t, factory.PhaseResult{
		Version: factory.PhaseResultVersion, Phase: project.ExecutionInvestigate, Status: "completed",
		Summary: "plan ready", Output: &factory.PhaseOutput{Plan: &plan},
	})
	finished, ok := accountRunFinish(t, ctx, db, run, output, time.Now())
	if !ok || finished.Outcome != factory.Succeeded || finished.Result == nil || !finished.Result.Reported {
		t.Fatalf("first coder was not accounted reported: %+v %v", finished, ok)
	}
	allowance, err := db.AttemptAllowance(ctx, fx.repo, 3)
	if err != nil {
		t.Fatal(err)
	}
	allowance.ActiveSeconds = int64(allowance.Limits.ActiveMinutes * 60)
	allowance.Active = false
	allowance.CheckpointUnix = time.Now().Unix()
	allowance.Revision++
	raw, _ := json.Marshal(allowance)
	fixtureDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer fixtureDB.Close()
	if _, err := fixtureDB.ExecContext(ctx, `UPDATE factory_attempt_allowances SET revision=$1,data=$2 WHERE root_assignment=$3`, allowance.Revision, string(raw), allowance.RootAssignment); err != nil {
		t.Fatal(err)
	}
	c := &Coordinator{Store: db, Host: fx.host, Broker: fx.broker, DispatchReads: fx.reads}
	command := factory.NewID()
	if result, err := c.RetryRun(ctx, command, "native:7", run.ID); err != nil || !result.Queued {
		t.Fatalf("Retry: %+v %v", result, err)
	}
	closed, err := db.AttemptAllowance(ctx, fx.repo, 3)
	if err != nil || !closed.Closed || closed.Active || closed.ActiveSeconds != allowance.ActiveSeconds || closed.Limits != allowance.Limits {
		t.Fatalf("normal Retry did not close the inactive exhausted root without refill: %+v %v", closed, err)
	}
	allowance = closed
	return fx, c, command, allowance, dsn
}

func TestRetryPreparesFreshCoderAndAdoptsLostNativeReply(t *testing.T) {
	ctx := context.Background()
	fx, c, command, original, dsn := consumedCoderRetry(t)
	var native execution.PreparationState
	fx.host.prepare = func(in project.FactoryCandidate) (execution.PreparationState, error) {
		if err := in.Validate(); err != nil {
			t.Fatal(err)
		}
		if in.Preparation.Role != project.RoleCoder || in.SourcePreparation == in.Preparation.ID || len(in.Bundle) != 0 || in.Preparation.SourceCommit != fx.tip {
			t.Fatalf("unbound fresh preparation: %+v", in)
		}
		rows, err := fx.db.CandidatePreparations(ctx, fx.repo, fx.proj, 3, command, "", 64, 0)
		if err != nil || len(rows) != 1 || rows[0].Admission.OwnerAssignment != command || rows[0].Admission.PublicationID != "" {
			t.Fatalf("native setup preceded exact custody: %+v %v", rows, err)
		}
		native = execution.PreparationState{ID: in.Preparation.ID, Project: fx.proj, Role: project.RoleCoder, SourceCommit: fx.tip, SetupDigest: in.Preparation.SetupDigest, Container: strings.Repeat("c", 64), Phase: project.PrepareRunning}
		return execution.PreparationState{}, errors.New("lost native setup reply")
	}
	fx.host.inspectPreparation = func(in project.PrepareInspect) (execution.PreparationState, error) { return native, nil }
	fx.host.contextRead = func(in project.FactoryPreparationContextRequest, role string) (project.FactoryPreparationContext, error) {
		if in.ID == "f111111111111111111111111" || !native.Ready || in.ID != native.ID {
			return project.FactoryPreparationContext{}, errors.New("old checkout HEAD is the preceding candidate")
		}
		return fixturePreparationContext(in, role), nil
	}
	beforeLaunches := len(fx.host.launches)
	c.advanceProgress(ctx)
	rows, err := fx.db.CandidatePreparations(ctx, fx.repo, fx.proj, 3, command, "", 64, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("pending native custody: %+v %v", rows, err)
	}
	deadline := rows[0].NotAfter
	if len(fx.host.launches) != beforeLaunches {
		t.Fatal("Retry launched before confirmed private preparation")
	}
	c.advanceProgress(ctx)
	rows, err = fx.db.CandidatePreparations(ctx, fx.repo, fx.proj, 3, command, "", 64, 0)
	if err != nil || len(rows) != 1 || rows[0].NotAfter != deadline || len(fx.host.preparations) != 1 {
		t.Fatalf("wake renewed deadline or duplicated clone: %+v %v", rows, err)
	}
	native.Phase, native.Ready = project.PrepareReady, true
	c.advanceProgress(ctx)
	fresh, err := fx.db.Assignment(ctx, command)
	if err != nil || fresh.Preparation != native.ID || fresh.AttemptRoot != command || len(fx.host.launches) != beforeLaunches+1 {
		t.Fatalf("fresh retry did not launch after adoption: %+v %v", fresh, err)
	}
	fixtureDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer fixtureDB.Close()
	var raw []byte
	err = fixtureDB.QueryRowContext(ctx, `SELECT data FROM factory_attempt_allowances WHERE root_assignment=$1`, original.RootAssignment).Scan(&raw)
	var old factory.AttemptAllowance
	if err == nil {
		err = json.Unmarshal(raw, &old)
	}
	if err != nil || old.RootAssignment != original.RootAssignment || old.Limits != original.Limits || old.ActiveSeconds != original.ActiveSeconds || !old.Closed || old.Active || len(old.Corrections) != len(original.Corrections) {
		t.Fatalf("fresh setup changed closed original allowance: %+v %v", old, err)
	}
	priorPrep, err := fx.db.Preparation(ctx, "f111111111111111111111111")
	if err != nil || !priorPrep.State.Ready || priorPrep.Preparation.SourceCommit != fx.tip {
		t.Fatalf("prior preparation was replaced: %+v %v", priorPrep, err)
	}
	c.advanceProgress(ctx)
	if len(fx.host.preparations) != 1 || len(fx.host.launches) != beforeLaunches+1 {
		t.Fatal("recovery duplicated preparation or provider launch")
	}
}

func TestRetryRetiresPreparedCheckoutAfterRejectedPacket(t *testing.T) {
	ctx := context.Background()
	fx, c, command, _, _ := consumedCoderRetry(t)
	stops := 0
	fx.host.stopPreparation = func(in project.PrepareStop) (execution.PreparationState, error) {
		stops++
		return execution.PreparationState{ID: in.ID, Project: in.Project, Role: project.RoleCoder, Stopped: true, Retirement: "confirmed"}, nil
	}
	fx.host.contextRead = func(in project.FactoryPreparationContextRequest, role string) (project.FactoryPreparationContext, error) {
		policy, err := fx.db.RepositoryPolicy(ctx, fx.repo)
		if err != nil {
			t.Fatal(err)
		}
		policy.Paused = true
		if err := fx.db.SaveRepositoryPolicy(ctx, policy); err != nil {
			t.Fatal(err)
		}
		return fixturePreparationContext(in, role), nil
	}
	before := len(fx.host.launches)
	c.advanceProgress(ctx)
	if stops != 1 || len(fx.host.launches) != before {
		t.Fatalf("rejected prepared Retry was not retired: stops=%d launches=%d", stops, len(fx.host.launches))
	}
	if _, err := fx.db.Assignment(ctx, command); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("rejected packet admitted an assignment: %v", err)
	}
	if allowance, err := fx.db.AttemptAllowance(ctx, fx.repo, 3); err != nil || allowance.RootAssignment == command {
		t.Fatalf("rejected packet created fresh allowance: %v", err)
	}
}

func TestRetryRetiresPendingPreparationBeforeEarlyPlanningWait(t *testing.T) {
	for _, change := range []string{"paused", "gate", "requirements", "approval", "expiry"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			fx, c, command, original, dsn := consumedCoderRetry(t)
			fx.host.prepare = func(in project.FactoryCandidate) (execution.PreparationState, error) {
				return execution.PreparationState{}, errors.New("lost native reply")
			}
			stops := 0
			fx.host.stopPreparation = func(in project.PrepareStop) (execution.PreparationState, error) {
				stops++
				return execution.PreparationState{ID: in.ID, Project: in.Project, Stopped: true, Retirement: "confirmed"}, nil
			}
			before := len(fx.host.launches)
			c.advanceProgress(ctx)
			pending, err := fx.db.PendingRetryPreparations(ctx, 64)
			if err != nil || len(pending) != 1 || pending[0].Admission.OwnerAssignment != command {
				t.Fatalf("pending setup: %+v %v", pending, err)
			}
			selected, err := fx.db.PendingExplicitRetry(ctx, fx.repo, 3, pending[0].Admission.Control.Acceptance)
			if err != nil || selected.CommandID != command {
				t.Fatalf("live setup lost its pending Retry: %+v %v", selected, err)
			}
			switch change {
			case "paused":
				policy, err := fx.db.RepositoryPolicy(ctx, fx.repo)
				if err != nil {
					t.Fatal(err)
				}
				policy.Paused = true
				if err := fx.db.SaveRepositoryPolicy(ctx, policy); err != nil {
					t.Fatal(err)
				}
			case "gate":
				if _, err := fx.db.WithdrawDispatch(ctx, fx.repo, factory.CauseControlPaused, "native:7"); err != nil {
					t.Fatal(err)
				}
			case "requirements":
				if err := fx.db.AdmitRequirementDecision(ctx, project.RequirementDecision{
					ID: "d333333333333333333333333", Predecessor: "d111111111111111111111111", Project: fx.proj, Approver: 7,
					SourceCommit: fx.tip, SetupDigest: strings.Repeat("d", 64), InputsDigest: strings.Repeat("e", 64),
				}); err != nil {
					t.Fatal(err)
				}
			case "approval":
				if err := fx.db.AdmitApprovalDecision(ctx, project.ApprovalDecision{
					ID: "d333333333333333333333333", Predecessor: "d222222222222222222222222", Project: fx.proj,
					Requirement: "d111111111111111111111111", Approver: 7, Verified: true,
					EffectsDigest: strings.Repeat("e", 64), ReadinessDigest: strings.Repeat("e", 64),
				}); err != nil {
					t.Fatal(err)
				}
			case "expiry":
				fixtureDB, err := sql.Open("pgx", dsn)
				if err != nil {
					t.Fatal(err)
				}
				defer fixtureDB.Close()
				if _, err := fixtureDB.ExecContext(ctx, `UPDATE project_preparations SET data=jsonb_set(data,'{factory_admission,not_after_unix}',to_jsonb($1::bigint)) WHERE id=$2`, time.Now().Add(-time.Second).Unix(), pending[0].Preparation.Preparation.ID); err != nil {
					t.Fatal(err)
				}
			}
			c.advanceProgress(ctx)
			if stops != 1 || len(fx.host.launches) != before || len(fx.host.preparations) != 1 {
				t.Fatalf("invalid pending setup survived or launched: stops=%d setups=%d launches=%d", stops, len(fx.host.preparations), len(fx.host.launches))
			}
			remaining, err := fx.db.PendingRetryPreparations(ctx, 64)
			if err != nil || len(remaining) != 0 {
				t.Fatalf("retirement not confirmed: %+v %v", remaining, err)
			}
			if _, err := fx.db.Assignment(ctx, command); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("invalid pending setup admitted run: %v", err)
			}
			if change != "expiry" {
				return
			}
			// A confirmed native stop permanently retires this preparation
			// identity. Keep its command/history, but do not let the oldest
			// unconsumed command starve a later explicit owner Retry.
			if selected, err = fx.db.PendingExplicitRetry(ctx, fx.repo, 3, pending[0].Admission.Control.Acceptance); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("confirmed-retired setup still selects its Retry: %+v %v", selected, err)
			}
			oldCommand, err := fx.db.FactoryCommand(ctx, command)
			if err != nil {
				t.Fatal(err)
			}
			fixtureDB, err := sql.Open("pgx", dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer fixtureDB.Close()
			prepID := pending[0].Preparation.Preparation.ID
			var tombstone []byte
			if err = fixtureDB.QueryRowContext(ctx, `SELECT data FROM project_preparations WHERE id=$1`, prepID).Scan(&tombstone); err != nil {
				t.Fatal(err)
			}
			// A stop from an uncertain or foreign owner is not evidence
			// that this command can be skipped. These disposable PG
			// mutations exercise the read boundary, then restore its exact
			// canonical native-stop record before normal recovery.
			for _, mismatch := range []struct {
				path  string
				value any
			}{
				{"factory_admission.stopped", false},
				{"factory_admission.retirement", "uncertain"},
				{"factory_admission.repository", fx.repo + 1},
				{"factory_admission.issue", int64(4)},
				{"factory_admission.project", "p" + strings.Repeat("e", 24)},
				{"factory_admission.owner_assignment", factory.NewID()},
				{"factory_admission.child_assignment", factory.NewID()},
				{"factory_admission.attempt_root", factory.NewID()},
				{"factory_admission.publication_id", factory.NewID()},
				{"factory_admission.control.acceptance", "d" + strings.Repeat("e", 24)},
				{"preparation.project", "p" + strings.Repeat("e", 24)},
				{"preparation.role", project.RoleReviewer},
			} {
				value, _ := json.Marshal(mismatch.value)
				if _, err = fixtureDB.ExecContext(ctx, `UPDATE project_preparations SET data=jsonb_set($2::jsonb,string_to_array($3,'.'),$4::jsonb) WHERE id=$1`, prepID, string(tombstone), mismatch.path, string(value)); err != nil {
					if (mismatch.path == "factory_admission.stopped" || mismatch.path == "factory_admission.retirement") && strings.Contains(err.Error(), "candidate preparation retirement cannot regress") {
						// The persistence boundary now rejects retirement regression
						// before a reader can adopt the malformed tombstone.
						continue
					}
					t.Fatal(err)
				}
				if mismatch.path == "factory_admission.stopped" || mismatch.path == "factory_admission.retirement" {
					t.Fatal("SQL downgraded confirmed candidate retirement")
				}
				if selected, err = fx.db.PendingExplicitRetry(ctx, fx.repo, 3, pending[0].Admission.Control.Acceptance); err != nil || selected.CommandID != command {
					t.Fatalf("%s retired a foreign or uncertain Retry: %+v %v", mismatch.path, selected, err)
				}
			}
			if _, err = fixtureDB.ExecContext(ctx, `UPDATE project_preparations SET data=$2 WHERE id=$1`, prepID, string(tombstone)); err != nil {
				t.Fatal(err)
			}
			prior, err := oldCommand.RetryDecision()
			if err != nil {
				t.Fatal(err)
			}
			newCommand := factory.NewID()
			if decision, retryErr := c.RetryRun(ctx, newCommand, "native:7", prior.Prior); retryErr != nil || decision.CommandID != newCommand || !decision.Queued {
				t.Fatalf("normal successor Retry refused: %+v %v", decision, retryErr)
			}
			if selected, err = fx.db.PendingExplicitRetry(ctx, fx.repo, 3, pending[0].Admission.Control.Acceptance); err != nil || selected.CommandID != newCommand {
				t.Fatalf("successor Retry remains hidden by retired setup: %+v %v", selected, err)
			}
			fx.host.prepare = nil
			c.advanceProgress(ctx)
			fresh, err := fx.db.Assignment(ctx, newCommand)
			if err != nil || fresh.AttemptRoot != newCommand || fresh.Preparation == prepID || len(fx.host.preparations) != 2 || len(fx.host.launches) != before+1 || stops != 1 {
				t.Fatalf("successor Retry did not prepare/launch once: %+v stops=%d setups=%d launches=%d err=%v", fresh, stops, len(fx.host.preparations), len(fx.host.launches), err)
			}
			if retained, readErr := fx.db.FactoryCommand(ctx, command); readErr != nil || retained != oldCommand {
				t.Fatalf("retired Retry command history changed: %+v %v", retained, readErr)
			}
			var retained []byte
			if err = fixtureDB.QueryRowContext(ctx, `SELECT data FROM project_preparations WHERE id=$1`, prepID).Scan(&retained); err != nil || string(retained) != string(tombstone) {
				t.Fatalf("native-stop history changed: %v", err)
			}
			if err = fixtureDB.QueryRowContext(ctx, `SELECT data FROM factory_attempt_allowances WHERE root_assignment=$1`, original.RootAssignment).Scan(&retained); err != nil {
				t.Fatal(err)
			}
			var old factory.AttemptAllowance
			if err = json.Unmarshal(retained, &old); err != nil || old.RootAssignment != original.RootAssignment || old.Limits != original.Limits || old.ActiveSeconds != original.ActiveSeconds || !old.Closed || old.Active {
				t.Fatalf("successor Retry refilled the original allowance: %+v %v", old, err)
			}
		})
	}
}

func TestRetryUsesCurrentSponsorshipActor(t *testing.T) {
	ctx := context.Background()
	fx, c, command, _, _ := consumedCoderRetry(t)
	if err := fx.db.UpsertUser(ctx, store.User{ID: 8, Login: "soda-new-sponsor"}); err != nil {
		t.Fatal(err)
	}
	sponsorship, err := fx.db.Sponsorship(ctx, fx.repo, "conn")
	if err != nil {
		t.Fatal(err)
	}
	sponsorship.ActorID = 8
	if err := fx.db.SaveSponsorship(ctx, sponsorship); err != nil {
		t.Fatal(err)
	}
	c.advanceProgress(ctx)
	a, err := fx.db.Assignment(ctx, command)
	if err != nil || a.ActorID != 8 {
		t.Fatalf("Retry was tied to prior actor: %+v %v", a, err)
	}
}

func TestRetryRetainsReadyPreparationAcrossUnavailableContext(t *testing.T) {
	ctx := context.Background()
	fx, c, command, _, _ := consumedCoderRetry(t)
	stops, reads := 0, 0
	fx.host.stopPreparation = func(in project.PrepareStop) (execution.PreparationState, error) {
		stops++
		return execution.PreparationState{}, errors.New("unexpected retirement")
	}
	fx.host.contextRead = func(in project.FactoryPreparationContextRequest, role string) (project.FactoryPreparationContext, error) {
		reads++
		if reads == 1 {
			return project.FactoryPreparationContext{}, errors.New("temporary native context transport failure")
		}
		return fixturePreparationContext(in, role), nil
	}
	before := len(fx.host.launches)
	c.advanceProgress(ctx)
	if stops != 0 || len(fx.host.launches) != before {
		t.Fatal("unavailable context retired or launched the preparation")
	}
	c.advanceProgress(ctx)
	a, err := fx.db.Assignment(ctx, command)
	if err != nil || a.Preparation == "f111111111111111111111111" || len(fx.host.preparations) != 1 || stops != 0 {
		t.Fatalf("ready setup was not retained: %+v %v", a, err)
	}
}

func TestRetryRetainsPendingCustodyAcrossOperationalStoreFailure(t *testing.T) {
	ctx := context.Background()
	fx, c, _, _, dsn := consumedCoderRetry(t)
	fx.host.prepare = func(in project.FactoryCandidate) (execution.PreparationState, error) {
		return execution.PreparationState{}, errors.New("lost native setup reply")
	}
	stops := 0
	fx.host.stopPreparation = func(in project.PrepareStop) (execution.PreparationState, error) {
		stops++
		return execution.PreparationState{}, errors.New("unexpected retirement")
	}
	c.advanceProgress(ctx)
	fixtureDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer fixtureDB.Close()
	if _, err := fixtureDB.ExecContext(ctx, `ALTER TABLE factory_policies RENAME TO factory_policies_unavailable`); err != nil {
		t.Fatal(err)
	}
	err = c.reconcileRetryPreparations(ctx)
	if _, restoreErr := fixtureDB.ExecContext(ctx, `ALTER TABLE factory_policies_unavailable RENAME TO factory_policies`); restoreErr != nil {
		t.Fatal(restoreErr)
	}
	if err == nil || stops != 0 {
		t.Fatalf("operational error invalidated custody: stops=%d err=%v", stops, err)
	}
	pending, err := fx.db.PendingRetryPreparations(ctx, 64)
	if err != nil || len(pending) != 1 || pending[0].Stopped {
		t.Fatalf("pending custody changed: %+v %v", pending, err)
	}
}

func TestHoldReleaseWakesExistingRetryAfterProjectStop(t *testing.T) {
	ctx := context.Background()
	fx, c, command, _, _ := consumedCoderRetry(t)
	// Exercise the serving priority path at one unchanged native revision.
	c.Priority = &PriorityJudgments{Journal: fx.db, Chooser: &priorityFixture{}, MaxCalls: 1}
	head := fx.decision[3]
	c.AcceptanceReads = &fakeAcceptanceSource{evidence: map[string]AcceptanceEvidence{
		"7/3": {Revision: 41, Issue: AcceptanceIssueView{
			Index: "3", Visible: true, TitleDigest: head.TitleDigest, TitleOccurrence: head.TitleOccurrence, LifecycleOccurrence: factory.OriginalOccurrence, ContentDigest: head.ContentDigest, ContentVer: 2,
		}, Comments: []AcceptanceComment{{ID: "11", Visible: true, Digest: head.Sources[0].Digest}}},
	}}
	c.Readiness = &fakeObserver{revision: 41, idle: true}
	before := len(fx.host.launches)
	if err := fx.db.SaveMaintenanceHold(ctx, project.MaintenanceHold{Project: fx.proj, Hold: true}); err != nil {
		t.Fatal(err)
	}
	withdrawal, err := fx.db.WithdrawDispatch(ctx, fx.repo, factory.CauseProjectStop, "native:7")
	if err != nil {
		t.Fatal(err)
	}
	c.advanceProgress(ctx)
	blocked, err := fx.db.IssueControl(ctx, fx.repo, 3)
	if err != nil || blocked.Readiness != factory.ReadinessNotAuthorized || blocked.Reason != factory.BlockerAuthorityMissing || blocked.NativeRev != 41 {
		t.Fatalf("Stop did not block readiness: %+v %v", blocked, err)
	}
	// No authority change means no native RPC, even if its observer fails.
	c.Readiness = &fakeObserver{revErr: errors.New("unchanged authority must not read native state")}
	if unchanged, err := c.refreshReadiness(ctx, blocked); err != nil || unchanged.Revision != blocked.Revision {
		t.Fatalf("unchanged authority re-read native state: %+v %v", unchanged, err)
	}
	c.Readiness = &fakeObserver{revision: 41, idle: true}
	if open, _, err := fx.db.ClearProjectStop(ctx, fx.repo, withdrawal.Revision); err != nil || !open {
		t.Fatalf("Start did not restore gate: %t %v", open, err)
	}
	if err := fx.db.SaveMaintenanceHold(ctx, project.MaintenanceHold{Project: fx.proj, Revision: 1, Hold: false}); err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 8 && len(fx.host.launches) == before; pass++ {
		c.advanceProgress(ctx)
	}
	a, err := fx.db.Assignment(ctx, command)
	if err != nil || a.AttemptRoot != command || len(fx.host.launches) != before+1 {
		control, _ := fx.db.IssueControl(ctx, fx.repo, 3)
		scan, _ := fx.db.LoadPriorityScan(ctx)
		t.Logf("control=%+v scan=%+v", control, scan)
		t.Fatalf("existing Retry stayed blocked after release: %+v %v", a, err)
	}
	if a.Acceptance != head.ID {
		t.Fatal("release replaced accepted inputs")
	}
}
