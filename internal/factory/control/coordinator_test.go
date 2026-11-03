package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/host"
	"github.com/levitateos/sodaos/internal/identity"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

type stubHost struct {
	stop     func(project.FactoryStop) (project.FactoryState, error)
	inspect  func(project.FactoryInspect) (project.FactoryState, error)
	takeover func(project.FactoryTakeover) (project.TakeoverResult, error)
	launch   func(project.FactoryLaunch) (project.FactoryState, error)
	harness  func() (project.FactoryHarnessPin, error)
	calls    int
}

func (s *stubHost) FactoryLaunch(ctx context.Context, in project.FactoryLaunch) (project.FactoryState, error) {
	if s.launch == nil {
		return project.FactoryState{}, errors.New("unexpected host launch")
	}
	return s.launch(in)
}

func (s *stubHost) FactoryStop(ctx context.Context, in project.FactoryStop) (project.FactoryState, error) {
	s.calls++
	state, err := s.stop(in)
	if err == nil && state.ID == "" {
		state.ID = in.ID
	}
	if err == nil && state.Project == "" {
		state.Project = in.Project
	}
	return state, err
}

func (s *stubHost) FactoryInspect(ctx context.Context, in project.FactoryInspect) (project.FactoryState, error) {
	if s.inspect == nil {
		return project.FactoryState{}, errors.New("unexpected host inspect")
	}
	return s.inspect(in)
}

func (s *stubHost) FactoryTakeover(ctx context.Context, in project.FactoryTakeover) (project.TakeoverResult, error) {
	if s.takeover == nil {
		return project.TakeoverResult{}, errors.New("unexpected host takeover")
	}
	return s.takeover(in)
}

func (s *stubHost) RecoverWork(ctx context.Context, r execution.WorkRecoveryRequest) (execution.WorkRecoveryReceipt, error) {
	return host.NewFactoryObserver(s, nil).RecoverWork(ctx, r)
}

func (s *stubHost) FactoryExport(ctx context.Context, in project.FactoryExport) (project.FactoryExportState, error) {
	return project.FactoryExportState{}, errors.New("unexpected host export")
}

func (s *stubHost) FactoryHarness(ctx context.Context, _ string) (project.FactoryHarnessPin, error) {
	if s.harness == nil {
		return project.FactoryHarnessPin{}, errors.New("unexpected host harness query")
	}
	return s.harness()
}

func (*stubHost) PrepareWorkspace(context.Context, execution.StandingPreparationRequest) (execution.PreparationState, error) {
	return execution.PreparationState{}, errors.New("standing preparation unavailable in fixture")
}

func (*stubHost) PrepareWorkspaceCandidate(context.Context, execution.CandidatePreparationRequest) (execution.PreparationState, error) {
	return execution.PreparationState{}, errors.New("candidate preparation unavailable in coordinator stub")
}

func (*stubHost) InspectWorkspacePreparation(context.Context, execution.PreparationAddress) (execution.PreparationState, error) {
	return execution.PreparationState{}, errors.New("preparation inspection unavailable in coordinator stub")
}

func (*stubHost) RetireWorkspacePreparation(context.Context, execution.PreparationAddress) (execution.PreparationState, error) {
	return execution.PreparationState{}, errors.New("preparation stop unavailable in coordinator stub")
}

func (*stubHost) ReadWorkspaceContext(_ context.Context, scoped execution.PreparationContextRequest) (execution.PreparationContext, error) {
	return execution.PreparationContext{}, errors.New("prepared repository context unavailable in coordinator stub")
}

type stubBroker struct {
	closed           map[string]bool
	missingAfterSeal bool
	get              func(kind, id string) (identity.Execution, error)
	close            func(kind, id string) error
}

type nativeObservationHost struct {
	*stubHost
	observation execution.RunObservation
}

type nativeRecoveryHost struct {
	*stubHost
	recover func(execution.WorkRecoveryRequest) (execution.WorkRecoveryReceipt, error)
	calls   int
}

func (h *nativeRecoveryHost) RecoverWork(_ context.Context, r execution.WorkRecoveryRequest) (execution.WorkRecoveryReceipt, error) {
	h.calls++
	return h.recover(r)
}

// Native copy results are scripted. The coordinator, settled-run checks,
// feed receipt transaction, reopen and exact replay use real PostgreSQL.
func TestRecoverNativeWorkPG(t *testing.T) {
	for _, mode := range []string{"copy", "lost_reply", "foreign_source", "foreign_target", "path_result", "mutated_request", "pending", "appliance_member", "foreign_profile"} {
		t.Run(mode, func(t *testing.T) {
			db, dsn := postgresFixture(t)
			fx := dispatchSeed(t, db)
			h := &nativeRecoveryHost{stubHost: &stubHost{}}
			c := NewCoordinator(db, h, nil)
			run := recordRun(t, c, func(r *factory.Run) {
				r.ProjectID, r.Image, r.Harness = fx.proj, "", "muse-fixture-1"
				r.Native = &execution.NativeRecord{Tool: execution.NativeTool{Harness: project.FactoryHarnessMuse, Version: "fixture-1", SHA256: strings.Repeat("d", 64), Executable: "/home/fixture/bin/muse"}, Provenance: execution.NativeProvenance{TrustMode: "same_user_trusted_host", Profile: collaboration.ProfileID(factory.NewID()), InstanceID: factory.NewID(), WorktreeID: "owned-worktree", Custody: execution.NativeCustody{State: execution.NotAcquired}}, Retirement: execution.Retired, Sealed: true}
			})
			if mode != "pending" {
				run.Outcome, run.Reconciled = factory.Cancelled, true
				if err := db.SaveFactoryRun(t.Context(), run); err != nil {
					t.Fatal(err)
				}
			}
			target := execution.RecoveryTarget{Native: &execution.NativeCheckoutRef{Profile: run.Native.Provenance.Profile, CheckoutID: factory.NewID()}}
			if mode == "appliance_member" {
				target = execution.RecoveryTarget{Member: "alice"}
			}
			if mode == "foreign_profile" {
				target.Native.Profile = collaboration.ProfileID(factory.NewID())
			}
			h.recover = func(request execution.WorkRecoveryRequest) (execution.WorkRecoveryReceipt, error) {
				if mode == "lost_reply" && h.calls == 1 {
					return execution.WorkRecoveryReceipt{}, errors.New("lost copy reply")
				}
				if mode == "mutated_request" {
					request.Native.Target.CheckoutID = factory.NewID()
				}
				receipt := execution.WorkRecoveryReceipt{Request: request, Reused: h.calls > 1}
				copied := *request.Native
				receipt.Request.Native = &copied
				switch mode {
				case "foreign_source":
					copied.Source.WorktreeID = "another-worktree"
				case "foreign_target":
					copied.Target.CheckoutID = factory.NewID()
				case "path_result":
					receipt.Destination = "/home/fixture/unregistered-path"
				}
				return receipt, nil
			}
			record, err := c.RecoverRunWork(t.Context(), run.ID, target)
			if mode != "copy" {
				if err == nil {
					t.Fatal("unsettled/foreign recovery accepted", mode, record)
				}
				if _, err := db.Takeover(t.Context(), run.ID, target.Key()); !errors.Is(err, store.ErrNotFound) {
					t.Fatal("refused recovery stored a receipt", err)
				}
				if mode == "pending" || mode == "appliance_member" || mode == "foreign_profile" {
					if h.calls != 0 {
						t.Fatal("inadmissible recovery reached runtime")
					}
				}
				if mode != "lost_reply" {
					return
				}
				record, err = c.RecoverRunWork(t.Context(), run.ID, target)
			}
			if err != nil || record.Native == nil || record.Native.Target != *target.Native || record.Member != "" || record.Dest != "" {
				t.Fatal("native recovery lost its destination reference", record, err)
			}
			raw, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			if fields["member"] != nil || fields["destination"] != nil {
				t.Fatal("native recovery invented appliance member/path", string(raw))
			}
			calls := h.calls
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = store.Open(dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			c.Store = db
			again, err := c.RecoverRunWork(t.Context(), run.ID, target)
			if err != nil || !again.Equal(record) || h.calls != calls {
				t.Fatal("recovery replay copied again or changed retained data", again, err)
			}
			forged := record
			copied := *record.Native
			forged.Native = &copied
			forged.Native.Source.WorktreeID = "foreign-source"
			if _, err := db.RecordTakeover(t.Context(), forged); err == nil {
				t.Fatal("store accepted a foreign retained-work source")
			}
		})
	}
}

func (h *nativeObservationHost) RetireRun(context.Context, execution.RunAddress) (execution.RunObservation, error) {
	return h.observation, nil
}

func (h *nativeObservationHost) InspectRun(context.Context, execution.RunAddress) (execution.RunObservation, error) {
	return h.observation, nil
}

// The actual project-start verification reads real project/run records. Native
// runtime/custody replies are scripted; no native worker is launched here.
func TestVerifyProjectStartNativeCustodyPG(t *testing.T) {
	for _, mode := range []string{"held", "held_sealed", "unreadable", "unsealed", "never_acquired", "running", "released_exited", "released_retired", "foreign_custody", "foreign_host"} {
		t.Run(mode, func(t *testing.T) {
			db, _ := dispatchTestDB(t)
			fx := dispatchSeed(t, db)
			h := &nativeObservationHost{stubHost: &stubHost{}}
			b := &nativeObservationCustody{}
			c := NewCoordinator(db, h, b)
			run := recordRun(t, c, func(r *factory.Run) {
				r.ProjectID, r.Image, r.Harness = fx.proj, "", "muse-fixture-1"
				r.Native = &execution.NativeRecord{Tool: execution.NativeTool{Harness: project.FactoryHarnessMuse, Version: "fixture-1", SHA256: strings.Repeat("d", 64), Executable: "/home/fixture/bin/muse"}, Provenance: execution.NativeProvenance{TrustMode: "same_user_trusted_host", Profile: collaboration.ProfileID(factory.NewID()), InstanceID: factory.NewID(), WorktreeID: "owned-checkout", Custody: execution.NativeCustody{State: execution.NotAcquired}}, Retirement: execution.NotStarted}
			})
			native := run.Native.Provenance
			custody := native.Custody
			h.observation = execution.RunObservation{Run: run.ID, Workspace: run.ProjectID, Role: run.Role, Kind: collaboration.Native, Native: &native, Phase: project.FactoryStopped, Retirement: execution.Retired}
			b.observation = execution.CustodyObservation{Run: run.ID, Workspace: run.ProjectID, Kind: collaboration.Native, Native: &custody, Sealed: true}
			switch mode {
			case "held", "held_sealed":
				custody = execution.NativeCustody{State: execution.Held, AcquisitionID: "owned-acquisition", Connection: "owned-connection", Generation: 3}
				b.observation.Sealed = mode == "held_sealed"
			case "unreadable":
				b.readErr = errors.New("custody unavailable")
			case "unsealed":
				b.observation.Sealed = false
			case "running", "released_exited", "released_retired":
				native.Process = &execution.NativeProcess{InvocationID: factory.NewID(), SystemInstance: "fixture-boot", StartIdentity: "fixture-start", PID: 1001, StartedAt: run.Started, Executable: run.Native.Tool.Executable, ExecutableSHA256: run.Native.Tool.SHA256}
				if mode == "running" {
					h.observation.Phase, h.observation.Retirement, h.observation.Live = project.FactoryRunning, execution.Running, true
				} else {
					custody = execution.NativeCustody{State: execution.Released, AcquisitionID: "owned-acquisition", Connection: "owned-connection", Generation: 3}
					native.Custody = custody
					h.observation.Phase = project.FactoryCompleted
					if mode == "released_exited" {
						h.observation.Retirement = execution.Exited
					}
				}
			case "foreign_custody":
				b.observation.Run = factory.NewID()
			case "foreign_host":
				h.observation.Workspace = "p" + strings.Repeat("e", 24)
			}
			result, err := c.VerifyProjectStart(t.Context(), fx.proj)
			if err != nil {
				t.Fatal(err)
			}
			wantRevived := mode == "held" || mode == "held_sealed" || mode == "running"
			wantClean := mode == "never_acquired" || mode == "released_retired"
			if (len(result.Revived) == 1) != wantRevived || (len(result.Unverified) == 1) != (!wantRevived && !wantClean) || !result.Started {
				t.Fatal("native start verification misclassified custody/retirement", mode, result)
			}
			saved, err := db.FactoryRun(t.Context(), run.ID)
			if err != nil || saved.Reconciled || saved.Outcome != "" || saved.Image != "" || saved.IdentityBinding != nil || saved.CredentialReturned {
				t.Fatal("verification settled or invented appliance authority", saved, err)
			}
			if wantRevived && mode != "running" && saved.Native.Provenance.Custody.AcquisitionID != custody.AcquisitionID {
				t.Fatal("held native acquisition was not retained", saved.Native)
			}
		})
	}
}

func TestProgressRetainsNativeIdentityPG(t *testing.T) {
	for _, unreadable := range []bool{false, true} {
		t.Run(fmt.Sprintf("custody_unreadable_%v", unreadable), func(t *testing.T) {
			db, dsn := postgresFixture(t)
			h := &nativeObservationHost{stubHost: &stubHost{}}
			b := &nativeObservationCustody{}
			c := NewCoordinator(db, h, b)
			run := recordRun(t, c, func(r *factory.Run) {
				r.Image, r.Harness = "", "muse-fixture-1"
				r.Native = &execution.NativeRecord{Tool: execution.NativeTool{Harness: "muse", Version: "fixture-1", SHA256: strings.Repeat("d", 64), Executable: "/home/fixture/bin/agent"}, Provenance: execution.NativeProvenance{TrustMode: "same_user_trusted_host", Profile: collaboration.ProfileID(factory.NewID()), InstanceID: factory.NewID(), WorktreeID: "owned-checkout", Custody: execution.NativeCustody{State: execution.NotAcquired}}, Retirement: execution.NotStarted}
			})
			native := run.Native.Provenance
			native.Process = &execution.NativeProcess{InvocationID: factory.NewID(), SystemInstance: "fixture-boot", StartIdentity: "fixture-start-tick", PID: 1001, StartedAt: run.Started, Executable: run.Native.Tool.Executable, ExecutableSHA256: run.Native.Tool.SHA256}
			native.Custody = execution.NativeCustody{State: execution.Held, AcquisitionID: "owned-acquisition", Connection: "owned-connection", Generation: 2}
			h.observation = execution.RunObservation{Run: run.ID, Workspace: run.ProjectID, Role: run.Role, Phase: project.FactoryRunning, Retirement: execution.Running, Live: true, Kind: collaboration.Native, Native: &native}
			custody := native.Custody
			b.observation = execution.CustodyObservation{Run: run.ID, Workspace: run.ProjectID, Kind: collaboration.Native, Native: &custody}
			if unreadable {
				b.readErr = errors.New("custody unreachable")
			}
			c.advanceProgress(t.Context())
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err := store.Open(dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			c.Store = db
			saved, err := db.FactoryRun(t.Context(), run.ID)
			if err != nil || saved.Native == nil || saved.InvocationID() != native.Process.InvocationID || saved.Native.Retirement != execution.Running || saved.Reconciled || saved.Outcome != "" {
				t.Fatal("live progress lost native identity at reopen", saved, err)
			}
			expected := execution.Held
			if unreadable {
				expected = execution.CustodyUncertain
			}
			if saved.Native.Provenance.Custody.State != expected {
				t.Fatal("progress invented custody proof", saved.Native)
			}
			original := native.Process.InvocationID
			changed := *native.Process
			changed.InvocationID, changed.StartIdentity = factory.NewID(), "reused-pid-start"
			native.Process = &changed
			c.advanceProgress(t.Context())
			saved, err = db.FactoryRun(t.Context(), run.ID)
			if err != nil || saved.InvocationID() != original || saved.Reconciled || saved.Outcome != "" {
				t.Fatal("progress replaced first native incarnation", saved, err)
			}
		})
	}
}

type nativeObservationCustody struct {
	observation execution.CustodyObservation
	readErr     error
	sealErr     error
}

func (b *nativeObservationCustody) SealCustody(context.Context, execution.RunAddress) error {
	return b.sealErr
}

func (b *nativeObservationCustody) ReadCustody(context.Context, execution.RunAddress) (execution.CustodyObservation, error) {
	return b.observation, b.readErr
}

// Runs the real coordinator/store transaction and reopen path with scripted
// neutral runtime observations. It grants no native dispatch/placement authority.
func TestStopNativeCustodyRecoveryPG(t *testing.T) {
	for _, mode := range []string{"unreadable", "unsealed", "seal_error", "exited", "held", "never_acquired"} {
		t.Run(mode, func(t *testing.T) {
			db, dsn := postgresFixture(t)
			h := &nativeObservationHost{stubHost: &stubHost{}}
			b := &nativeObservationCustody{}
			c := NewCoordinator(db, h, b)
			run := recordRun(t, c, func(r *factory.Run) {
				r.Image = ""
				r.Harness = "muse-fixture-1"
				r.Native = &execution.NativeRecord{Tool: execution.NativeTool{Harness: "muse", Version: "fixture-1", SHA256: strings.Repeat("d", 64), Executable: "/home/fixture/bin/agent"}, Provenance: execution.NativeProvenance{TrustMode: "same_user_trusted_host", Profile: collaboration.ProfileID(factory.NewID()), InstanceID: factory.NewID(), WorktreeID: "owned-checkout", Custody: execution.NativeCustody{State: execution.NotAcquired}}, Retirement: execution.NotStarted}
			})
			native := run.Native.Provenance
			native.Process = &execution.NativeProcess{InvocationID: factory.NewID(), SystemInstance: "fixture-boot", StartIdentity: "fixture-start-tick", PID: 1001, StartedAt: run.Started, Executable: run.Native.Tool.Executable, ExecutableSHA256: run.Native.Tool.SHA256}
			native.Custody = execution.NativeCustody{State: execution.Released, AcquisitionID: "owned-acquisition", Connection: "owned-connection", Generation: 2}
			h.observation = execution.RunObservation{Run: run.ID, Workspace: run.ProjectID, Role: run.Role, Phase: project.FactoryStopped, Retirement: execution.Retired, Kind: collaboration.Native, Native: &native}
			custody := native.Custody
			b.observation = execution.CustodyObservation{Run: run.ID, Workspace: run.ProjectID, Sealed: true, Kind: collaboration.Native, Native: &custody}
			switch mode {
			case "unreadable":
				b.readErr = execution.ErrCustodyNotObserved
			case "unsealed":
				b.observation.Sealed = false
			case "seal_error":
				b.sealErr = errors.New("seal unavailable")
			case "exited":
				h.observation.Retirement = execution.Exited
			case "held":
				native.Custody.State, custody.State = execution.Held, execution.Held
			case "never_acquired":
				native.Process = nil
				native.Custody = execution.NativeCustody{State: execution.NotAcquired}
				custody, b.observation.Sealed = native.Custody, false
			}
			result, err := c.Stop(t.Context(), run.ID)
			if err != nil || !result.Uncertain || result.Confirmed {
				t.Fatal("incomplete native custody settled", result, err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = store.Open(dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			c.Store = db
			saved, err := db.FactoryRun(t.Context(), run.ID)
			if err != nil || saved.Reconciled || saved.Outcome != "" || saved.Native == nil || saved.InvocationID() != h.observation.InvocationID() || saved.CredentialsDelegated() != (native.Custody.AcquisitionID != "") {
				t.Fatal("fenced native facts lost at reopen", saved, err)
			}
			if mode == "unreadable" && saved.Native.Provenance.Custody.State != execution.CustodyUncertain {
				t.Fatal("host-only return became authoritative custody", saved.Native)
			}
			b.readErr, b.sealErr, b.observation.Sealed = nil, nil, true
			h.observation.Retirement, native.Custody.State, custody.State = execution.Retired, execution.Released, execution.Released
			if mode == "never_acquired" {
				native.Custody.State, custody.State = execution.NotAcquired, execution.NotAcquired
				deps := DispatchDeps{Store: db, Host: h, Broker: b}
				if unused, fenced := confirmUnused(t.Context(), deps, factory.Assignment{ProjectID: run.ProjectID}, &saved); !unused || fenced {
					t.Fatal("native sealed unacquired identity was not confirmed unused", unused, fenced)
				}
			}
			result, err = c.Stop(t.Context(), run.ID)
			if err != nil || !result.Confirmed || result.Uncertain {
				t.Fatal("confirmed native cleanup did not settle after reopen", result, err)
			}
			saved, err = db.FactoryRun(t.Context(), run.ID)
			if err != nil || !saved.Reconciled || !saved.Native.Settled() || saved.IdentityBinding != nil || saved.Image != "" || saved.CredentialReturned {
				t.Fatal("native settlement invented appliance claims", saved, err)
			}
		})
	}
}

func (s *stubBroker) GetExecution(ctx context.Context, kind, id string) (identity.Execution, error) {
	metadata, err := s.get(kind, id)
	if errors.Is(err, identity.ErrNotFound) && s.closed[id] && !s.missingAfterSeal {
		return identity.Execution{Kind: kind, ExecutionID: id, State: identity.ExecutionTerminal}, nil
	}
	return metadata, err
}

func (s *stubBroker) CloseExecution(ctx context.Context, kind, id string) error {
	if err := s.close(kind, id); err != nil {
		return err
	}
	if s.closed == nil {
		s.closed = map[string]bool{}
	}
	s.closed[id] = true
	return nil
}

func TestStopFencesIncompleteOrConflictingCustodyPG(t *testing.T) {
	for _, mode := range []string{"container_changed", "invocation_changed", "lease_changed", "credential_unreturned", "unsealed", "missing_after_seal"} {
		t.Run(mode, func(t *testing.T) {
			h := &stubHost{}
			broker := &stubBroker{missingAfterSeal: mode == "missing_after_seal", close: func(string, string) error { return nil }}
			db, dsn := postgresFixture(t)
			c := NewCoordinator(db, h, broker)
			run := recordRun(t, c, nil)
			h.stop = func(request project.FactoryStop) (project.FactoryState, error) {
				return project.FactoryState{ID: request.ID, Project: request.Project, Phase: project.FactoryStopped, Retirement: "confirmed", Container: strings.Repeat("c", 64), Invocation: strings.Repeat("a", 32), LeaseID: "lease", Generation: 3, Delivered: true, CredentialReturned: mode != "credential_unreturned"}, nil
			}
			broker.get = func(kind, id string) (identity.Execution, error) {
				if mode == "missing_after_seal" {
					return identity.Execution{}, identity.ErrNotFound
				}
				state, container := identity.ExecutionTerminal, strings.Repeat("c", 64)
				if mode == "container_changed" {
					container = strings.Repeat("d", 64)
				}
				if mode == "unsealed" {
					state = identity.ExecutionLive
				}
				invocation, lease := strings.Repeat("a", 32), "lease"
				if mode == "invocation_changed" {
					invocation = strings.Repeat("b", 32)
				}
				if mode == "lease_changed" {
					lease = "different-lease"
				}
				return identity.Execution{Kind: kind, ExecutionID: id, State: state, Digest: strings.Repeat("e", 64), LeaseID: lease, Binding: &identity.Binding{Kind: kind, ID: id, Generation: 3, Project: container, InvocationID: invocation}}, nil
			}
			result, err := c.Stop(context.Background(), run.ID)
			if err != nil || !result.Uncertain || result.Confirmed {
				t.Fatal("incomplete/conflicting observations settled a run", mode, result, err)
			}
			saved, err := c.Store.FactoryRun(context.Background(), run.ID)
			knownBinding := mode == "credential_unreturned" || mode == "unsealed"
			if err != nil || saved.Reconciled || saved.Outcome != "" || (saved.IdentityBinding != nil) != knownBinding || saved.CredentialReturned != (mode == "unsealed") {
				t.Fatal("uncertain custody changed retained run authority", saved, err)
			}
			if saved.ApplianceInvocation == nil || saved.InvocationID() != strings.Repeat("a", 32) || saved.IdentityLeaseID != "lease" || saved.IdentityGeneration != 3 {
				t.Fatal("fenced run lost its observed host identity", saved)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			c.Store = reopened
			h.stop = func(request project.FactoryStop) (project.FactoryState, error) {
				return project.FactoryState{ID: request.ID, Project: request.Project, Phase: project.FactoryStopped, Retirement: "confirmed", Container: strings.Repeat("c", 64), Invocation: strings.Repeat("b", 32), LeaseID: "lease", Generation: 3, Delivered: true, CredentialReturned: true}, nil
			}
			result, err = c.Stop(context.Background(), run.ID)
			if err != nil || !result.Uncertain || result.Confirmed {
				t.Fatal("changed invocation settled after reopen", result, err)
			}
			retained, err := reopened.FactoryRun(context.Background(), run.ID)
			if err != nil || retained.InvocationID() != saved.InvocationID() || retained.Reconciled {
				t.Fatal("reopen replaced first invocation", retained, err)
			}
		})
	}
}

func coordinatorFixture(t *testing.T, host HostFactory, broker BrokerExecution) *Coordinator {
	t.Helper()
	db, _ := postgresFixture(t)
	if host == nil {
		host = &stubHost{stop: func(project.FactoryStop) (project.FactoryState, error) {
			return project.FactoryState{}, errors.New("unexpected host call")
		}}
	}
	if broker == nil {
		broker = &stubBroker{get: func(string, string) (identity.Execution, error) {
			return identity.Execution{}, errors.New("unexpected broker call")
		}, close: func(string, string) error { return errors.New("unexpected broker call") }}
	}
	return NewCoordinator(db, host, broker)
}

func recordRun(t *testing.T, c *Coordinator, mutate func(*factory.Run)) factory.Run {
	t.Helper()
	now := time.Now()
	r := factory.Run{ID: factory.NewID(), ProjectID: "p123456789012345678901234", Role: "coder", InputSHA: strings.Repeat("a", 40), Started: now, Deadline: now.Add(time.Hour), Image: "sha256:" + strings.Repeat("b", 64), Harness: "codex-0.157.1", Model: "test"}
	if mutate != nil {
		mutate(&r)
	}
	if err := c.Store.RecordFactoryRun(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestStatusRedactsCredentialPaths(t *testing.T) {
	c := coordinatorFixture(t, nil, nil)
	r := recordRun(t, c, func(r *factory.Run) {
		r.IdentityLeaseID, r.IdentityGeneration, r.CredentialDelegated = "lease", 3, true
		r.IdentityBinding = &identity.Binding{Kind: identity.Factory, ID: r.ID, Generation: 3, Project: strings.Repeat("c", 64), CredentialRoot: "/home/coder/checkouts/p/.soda-home/runs/" + r.ID}
	})
	runs, err := c.Status(context.Background(), r.ID)
	if err != nil || len(runs) != 1 {
		t.Fatal(runs, err)
	}
	if runs[0].IdentityBinding == nil || runs[0].IdentityBinding.CredentialRoot != "" {
		t.Fatal("status exposed a credential-adjacent host path")
	}
	if _, err = c.Status(context.Background(), factory.NewID()); !errors.Is(err, factory.ErrNotFound) {
		t.Fatal("unknown run reported status")
	}
}

func TestStopUsesSettledRunState(t *testing.T) {
	host := &stubHost{}
	broker := &stubBroker{}
	c := coordinatorFixture(t, host, broker)
	r := recordRun(t, c, nil)
	binding := identity.Binding{Kind: identity.Factory, ID: r.ID, Generation: 3, Project: strings.Repeat("c", 64), CredentialRoot: "/home/coder/runs/" + r.ID}
	host.stop = func(in project.FactoryStop) (project.FactoryState, error) {
		if in.ID != r.ID || in.Project != r.ProjectID {
			return project.FactoryState{}, errors.New("stop addressed another run")
		}
		return project.FactoryState{ID: r.ID, Project: r.ProjectID, Phase: project.FactoryStopped, Retirement: "confirmed", Reason: "stopped", LeaseID: "lease", Generation: 3, CredentialReturned: true}, nil
	}
	broker.close = func(kind, id string) error {
		if kind != identity.Factory || id != r.ID {
			return errors.New("close addressed another execution")
		}
		return nil
	}
	broker.get = func(kind, id string) (identity.Execution, error) {
		return identity.Execution{Kind: identity.Factory, ExecutionID: r.ID, State: identity.ExecutionTerminal, Digest: strings.Repeat("d", 64), LeaseID: "lease", Binding: &binding}, nil
	}
	receipt, err := c.Stop(context.Background(), r.ID)
	if err != nil || !receipt.Confirmed || receipt.Uncertain || receipt.Outcome != string(factory.Cancelled) {
		t.Fatal(receipt, err)
	}
	settled, err := c.Store.FactoryRun(context.Background(), r.ID)
	if err != nil || !settled.Reconciled || settled.Outcome != factory.Cancelled || !settled.CredentialDelegated || !settled.CredentialReturned || settled.IdentityBinding == nil {
		t.Fatal(settled, err)
	}
	replay, err := c.Stop(context.Background(), r.ID)
	if err != nil || replay != receipt || host.calls != 1 {
		t.Fatal("settled run was stopped again", replay, err)
	}
}

func TestStopUsesLatestRunState(t *testing.T) {
	host := &stubHost{}
	c := coordinatorFixture(t, host, &stubBroker{
		get:   func(string, string) (identity.Execution, error) { return identity.Execution{}, identity.ErrNotFound },
		close: func(string, string) error { return nil },
	})
	r := recordRun(t, c, nil)
	host.stop = func(project.FactoryStop) (project.FactoryState, error) {
		if host.calls == 1 {
			return project.FactoryState{}, errors.New("stop not confirmed")
		}
		return project.FactoryState{ID: r.ID, Phase: project.FactoryStopped, Retirement: "confirmed", Reason: "stop-before-start"}, nil
	}
	first, err := c.Stop(context.Background(), r.ID)
	if err != nil || first.Confirmed || !first.Uncertain {
		t.Fatal(first, err)
	}
	second, err := c.Stop(context.Background(), r.ID)
	if err != nil || !second.Confirmed || second.Uncertain || host.calls != 2 {
		t.Fatal("new stop did not establish the current run state", second, err)
	}
}

func TestStopUnknownRunRecordsNothing(t *testing.T) {
	host := &stubHost{}
	c := coordinatorFixture(t, host, nil)
	if _, err := c.Stop(context.Background(), factory.NewID()); !errors.Is(err, factory.ErrNotFound) {
		t.Fatal("unknown run stopped")
	}
	if host.calls != 0 {
		t.Fatal("unknown run reached the host")
	}
}

func TestStopFencesUncertainRetirement(t *testing.T) {
	host := &stubHost{stop: func(project.FactoryStop) (project.FactoryState, error) {
		return project.FactoryState{Phase: project.FactoryStopped, Retirement: "uncertain", Reason: "stop-uncertain"}, nil
	}}
	c := coordinatorFixture(t, host, &stubBroker{
		get:   func(string, string) (identity.Execution, error) { return identity.Execution{}, identity.ErrNotFound },
		close: func(string, string) error { return nil },
	})
	r := recordRun(t, c, nil)
	receipt, err := c.Stop(context.Background(), r.ID)
	if err != nil || !receipt.Uncertain || receipt.Confirmed {
		t.Fatal(receipt, err)
	}
	current, err := c.Store.FactoryRun(context.Background(), r.ID)
	if err != nil || current.Reconciled || current.Outcome != "" {
		t.Fatal("uncertain retirement settled the run", current, err)
	}
}

func TestReconcileSettlesCompletedRun(t *testing.T) {
	host := &stubHost{}
	c := coordinatorFixture(t, host, &stubBroker{
		get:   func(string, string) (identity.Execution, error) { return identity.Execution{}, identity.ErrNotFound },
		close: func(string, string) error { return nil },
	})
	r := recordRun(t, c, nil)
	host.stop = func(project.FactoryStop) (project.FactoryState, error) {
		return project.FactoryState{ID: r.ID, Phase: project.FactoryCompleted, Retirement: "confirmed", Output: "candidate ready"}, nil
	}
	receipt, err := c.Reconcile(context.Background())
	if err != nil || len(receipt.Settled) != 1 || len(receipt.Fenced) != 0 {
		t.Fatal(receipt, err)
	}
	settled, err := c.Store.FactoryRun(context.Background(), r.ID)
	if err != nil || !settled.Reconciled || settled.Outcome != factory.Succeeded || settled.Summary != "candidate ready" {
		t.Fatal(settled, err)
	}
}

func TestReconcileFencesBrokerFailure(t *testing.T) {
	closeCalls := 0
	host := &stubHost{stop: func(project.FactoryStop) (project.FactoryState, error) {
		return project.FactoryState{Phase: project.FactoryCompleted, Retirement: "confirmed"}, nil
	}}
	c := coordinatorFixture(t, host, &stubBroker{
		get: func(string, string) (identity.Execution, error) { return identity.Execution{}, identity.ErrNotFound },
		close: func(string, string) error {
			closeCalls++
			return errors.New("broker unavailable")
		},
	})
	r := recordRun(t, c, nil)
	receipt, err := c.Reconcile(context.Background())
	if err != nil || len(receipt.Settled) != 0 || len(receipt.Fenced) != 1 || receipt.Fenced[0].ID != r.ID {
		t.Fatal(receipt, err)
	}
	current, err := c.Store.FactoryRun(context.Background(), r.ID)
	if err != nil || current.Reconciled {
		t.Fatal("broker failure settled the run", current, err)
	}
	if closeCalls != 1 {
		t.Fatalf("broker close calls = %d, want one", closeCalls)
	}
}

func TestStartTakesExclusiveOwnership(t *testing.T) {
	c := coordinatorFixture(t, nil, nil)
	lock := filepath.Join(t.TempDir(), "factory-coordinator.lock")
	ctx := context.Background()
	if err := c.Start(ctx, lock); err != nil {
		t.Fatal(err)
	}
	second := coordinatorFixture(t, nil, nil)
	second.Store = c.Store
	if err := second.Start(ctx, lock); err == nil {
		t.Fatal("second coordinator owned the database")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Start(ctx, lock); err != nil {
		t.Fatal(err)
	}
	_ = second.Close()
}

func TestCloseRetainsCoordinatorOwnershipUntilAdmittedOperatorStops(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce sync.Once
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	host := &stubHost{stop: func(in project.FactoryStop) (project.FactoryState, error) {
		enteredOnce.Do(func() { close(entered) })
		<-release
		return project.FactoryState{ID: in.ID, Phase: project.FactoryStopped, Retirement: "confirmed", Reason: "stopped"}, nil
	}}
	broker := &stubBroker{
		get:   func(string, string) (identity.Execution, error) { return identity.Execution{}, identity.ErrNotFound },
		close: func(string, string) error { return nil },
	}
	c := coordinatorFixture(t, host, broker)
	lock := filepath.Join(t.TempDir(), "factory-coordinator.lock")
	if err := c.Start(context.Background(), lock); err != nil {
		t.Fatal(err)
	}
	run := recordRun(t, c, nil)
	server := &http.Server{Handler: c.AdmitHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.OperatorHandler().ServeHTTP(w, r.WithContext(WithOperatorPrincipal(r.Context(), "os-uid:0")))
	}))}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		body := fmt.Sprintf(`{"type":"stop","target":%q}`, run.ID)
		response, requestErr := http.Post("http://"+listener.Addr().String()+OperatorPath, "application/json", strings.NewReader(body))
		if requestErr == nil {
			_ = response.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		releaseOnce.Do(func() { close(release) })
		t.Fatal("operator stop did not reach the held host callback")
	}
	c.StopAdmission()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = server.Shutdown(shutdownCtx)
	cancel()
	if err == nil {
		t.Fatal("shutdown completed while the admitted operator callback was held")
	}
	if err := server.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}

	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	second := NewCoordinator(c.Store, host, broker)
	tryCtx, cancelTry := context.WithTimeout(context.Background(), 100*time.Millisecond)
	err = second.Start(tryCtx, lock)
	cancelTry()
	if err == nil {
		t.Fatal("second coordinator acquired ownership while admitted callback remained active")
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-requestDone:
	case <-time.After(5 * time.Second):
		t.Fatal("operator request did not finish after host callback release")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("coordinator close did not drain the admitted request")
	}
	if err := <-serveDone; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}
	if err := second.Start(context.Background(), lock); err != nil {
		t.Fatalf("second coordinator could not acquire released ownership: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

// restartCoordinatorAfterCrash reopens the database and starts a fresh
// coordinator over the recorded run state.
func restartCoordinatorAfterCrash(t *testing.T, dsn string, host HostFactory, broker BrokerExecution) *Coordinator {
	t.Helper()
	reopened, err := store.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted := NewCoordinator(reopened, host, broker)
	if err := restarted.Start(context.Background(), filepath.Join(t.TempDir(), "coordinator.lock")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	return restarted
}

func settledStopBroker() *stubBroker {
	return &stubBroker{
		get:   func(string, string) (identity.Execution, error) { return identity.Execution{}, identity.ErrNotFound },
		close: func(string, string) error { return nil },
	}
}

func TestRestartedStopUsesEstablishedRunState(t *testing.T) {
	ctx := context.Background()
	db, dsn := postgresFixture(t)
	c := NewCoordinator(db, &stubHost{}, &stubBroker{})
	r := recordRun(t, c, nil)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	host := &stubHost{}
	host.stop = func(project.FactoryStop) (project.FactoryState, error) {
		return project.FactoryState{ID: r.ID, Phase: project.FactoryStopped, Retirement: "confirmed", Reason: "stop-before-start"}, nil
	}
	restarted := restartCoordinatorAfterCrash(t, dsn, host, settledStopBroker())
	run, err := restarted.Store.FactoryRun(ctx, r.ID)
	if err != nil || !run.Reconciled || run.Outcome != factory.Cancelled {
		t.Fatalf("startup did not settle the actual run: %+v %v", run, err)
	}
	calls := host.calls
	receipt, err := restarted.Stop(ctx, r.ID)
	if err != nil || !receipt.Confirmed || receipt.Uncertain {
		t.Fatalf("explicit stop after restart: %+v %v", receipt, err)
	}
	again, err := restarted.Stop(ctx, r.ID)
	if err != nil || again != receipt {
		t.Fatalf("second replay after restart: %+v %v", again, err)
	}
	if host.calls != calls {
		t.Fatal("settled run was stopped again")
	}
}

func TestRestartedStopPreservesUncertainty(t *testing.T) {
	ctx := context.Background()
	db, dsn := postgresFixture(t)
	c := NewCoordinator(db, &stubHost{}, &stubBroker{})
	r := recordRun(t, c, nil)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	host := &stubHost{}
	host.stop = func(project.FactoryStop) (project.FactoryState, error) {
		return project.FactoryState{}, errors.New("synthetic host outage")
	}
	restarted := restartCoordinatorAfterCrash(t, dsn, host, settledStopBroker())
	receipt, err := restarted.Stop(ctx, r.ID)
	if err != nil {
		t.Fatalf("explicit stop errored: %v", err)
	}
	if receipt.Confirmed || !receipt.Uncertain {
		t.Fatalf("uncertain effect reported as established: %+v", receipt)
	}
	run, err := restarted.Store.FactoryRun(ctx, r.ID)
	if err != nil || run.Reconciled {
		t.Fatalf("fenced run settled by an unconfirmed stop: %+v %v", run, err)
	}
}

func TestRestartedReconcileUsesActualRunState(t *testing.T) {
	ctx := context.Background()
	db, dsn := postgresFixture(t)
	c := NewCoordinator(db, &stubHost{}, &stubBroker{})
	r := recordRun(t, c, nil)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	host := &stubHost{}
	host.stop = func(project.FactoryStop) (project.FactoryState, error) {
		return project.FactoryState{ID: r.ID, Phase: project.FactoryCompleted, Reason: "done"}, nil
	}
	restarted := restartCoordinatorAfterCrash(t, dsn, host, settledStopBroker())
	run, err := restarted.Store.FactoryRun(ctx, r.ID)
	if err != nil || !run.Reconciled || run.Outcome != factory.Succeeded {
		t.Fatalf("startup did not settle the actual run: %+v %v", run, err)
	}
	receipt, err := restarted.Reconcile(ctx)
	if err != nil {
		t.Fatalf("explicit reconcile after restart: %v", err)
	}
	if len(receipt.Settled) != 0 || len(receipt.Fenced) != 0 {
		t.Fatalf("already settled run was processed again: %+v", receipt)
	}
}

func (f *stubHost) InspectRun(ctx context.Context, address execution.RunAddress) (execution.RunObservation, error) {
	return host.NewFactoryObserver(f, nil).InspectRun(ctx, address)
}

func (f *stubHost) InspectTool(ctx context.Context, family string) (execution.ToolObservation, error) {
	return host.NewFactoryObserver(f, nil).InspectTool(ctx, family)
}

func (f *stubHost) RetireRun(ctx context.Context, address execution.RunAddress) (execution.RunObservation, error) {
	return host.NewFactoryObserver(f, nil).RetireRun(ctx, address)
}

func (f *stubHost) LaunchRun(ctx context.Context, launch execution.LaunchRequest) (execution.RunObservation, error) {
	return host.NewFactoryObserver(f, nil).LaunchRun(ctx, launch)
}

func (f *stubBroker) ReadCustody(ctx context.Context, address execution.RunAddress) (execution.CustodyObservation, error) {
	return host.NewFactoryObserver(nil, f).ReadCustody(ctx, address)
}

func (f *stubBroker) SealCustody(ctx context.Context, address execution.RunAddress) error {
	return host.NewFactoryObserver(nil, f).SealCustody(ctx, address)
}

// Fixture wire records cross the same production translator as appliance RPCs.
func nativeRunObservation(t *testing.T, state project.FactoryState) execution.RunObservation {
	t.Helper()
	observation, err := host.RunObservationFromNative(state, execution.RunAddress{Run: state.ID, Workspace: state.Project})
	if err != nil {
		t.Fatal("invalid native run fixture", err)
	}
	return observation
}

func nativeCustodyObservation(t *testing.T, state project.FactoryState, metadata identity.Execution) execution.CustodyObservation {
	t.Helper()
	broker := &stubBroker{get: func(string, string) (identity.Execution, error) { return metadata, nil }}
	observation, err := host.NewFactoryObserver(nil, broker).ReadCustody(context.Background(), execution.RunAddress{Run: state.ID, Workspace: state.Project})
	if err != nil {
		t.Fatal("invalid native custody fixture", err)
	}
	return observation
}

func (f *stubHost) ExportCandidate(ctx context.Context, request execution.CandidateRequest) (execution.CandidateBundle, error) {
	return host.NewFactoryObserver(f, nil).ExportCandidate(ctx, request)
}
