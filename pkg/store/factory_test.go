package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/identity"
	"github.com/levitateos/factory/pkg/project"
)

// Native process and checkpoint facts are fixture data. PostgreSQL performs the
// actual binding checks, receipt transitions and store reopen; no executor runs.
func TestNativeCheckpointRetentionPG(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := t.Context()
	if err := s.InitializeEmptyCollaborationLedger(ctx); err != nil {
		t.Fatal(err)
	}
	host, err := s.PutCollaborationHost(ctx, collaboration.Host{ID: collaboration.HostID(factory.NewID()), Provider: collaboration.GitHub, APIBase: "https://api.github.com"})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := s.ObserveCollaborationRepository(ctx, host.ID, "placement-fixture", "fixture/placement")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := s.ObserveCollaborationActor(ctx, host.ID, "placement-admin", "placement-admin")
	if err != nil {
		t.Fatal(err)
	}
	controllers := []collaboration.RuntimeRef{}
	for range 2 {
		id := collaboration.ProfileID(factory.NewID())
		if err := s.PutCollaborationProfile(ctx, collaboration.Profile{ID: id, Name: "Placement fixture"}); err != nil {
			t.Fatal(err)
		}
		controllers = append(controllers, collaboration.RuntimeRef{ID: collaboration.RuntimeID(id), Profile: id, Kind: collaboration.Native, Name: "Placement fixture"})
	}
	type selection struct {
		placement factory.Placement
		err       error
	}
	selected := make(chan selection, 2)
	for _, controller := range controllers {
		go func() {
			p, err := s.SelectPlacement(ctx, host.ID, repository.ID, actor.ID, controller, "", factory.PlacementActive)
			selected <- selection{p, err}
		}()
	}
	var placed factory.Placement
	winners := 0
	for range 2 {
		result := <-selected
		if result.err == nil {
			placed = result.placement
			winners++
		} else if !errors.Is(result.err, factory.ErrPlacement) {
			t.Fatal(result.err)
		}
	}
	if winners != 1 {
		t.Fatal("concurrent controllers both selected", winners)
	}
	controller := controllers[0]
	if controller.ID == placed.Runtime {
		controller = controllers[1]
	}
	if _, err := s.SelectPlacement(ctx, host.ID, repository.ID, actor.ID, controller, placed.Revision, factory.PlacementActive); !errors.Is(err, factory.ErrPlacement) {
		t.Fatal("active placement changed controller", err)
	}
	priorController := controllers[0]
	if priorController.ID != placed.Runtime {
		priorController = controllers[1]
	}
	placed, err = s.SelectPlacement(ctx, host.ID, repository.ID, actor.ID, priorController, placed.Revision, factory.PlacementRetiring)
	if err != nil {
		t.Fatal(err)
	}
	if placed.RequireDispatch(host.ID, repository.ID, placed.Runtime, placed.Generation) == nil {
		t.Fatal("retiring placement admitted dispatch")
	}
	placed, err = s.SelectPlacement(ctx, host.ID, repository.ID, actor.ID, priorController, placed.Revision, factory.PlacementInactive)
	if err != nil {
		t.Fatal(err)
	}
	staleRevision := placed.Revision
	placed, err = s.SelectPlacement(ctx, host.ID, repository.ID, actor.ID, controller, placed.Revision, factory.PlacementActive)
	if err != nil || placed.Generation != 2 {
		t.Fatal("new controller generation", err)
	}
	if _, err := s.SelectPlacement(ctx, host.ID, repository.ID, actor.ID, controller, staleRevision, factory.PlacementRetiring); !errors.Is(err, factory.ErrPlacement) {
		t.Fatal("stale placement revision admitted", err)
	}
	r := factoryRun(time.Now().UTC())
	r.Image, r.Harness, r.Role = "", "muse-fixture-1", project.RoleCoder
	r.Native = &execution.NativeRecord{Tool: execution.NativeTool{Harness: project.FactoryHarnessMuse, Version: "fixture-1", SHA256: strings.Repeat("d", 64), Executable: "/home/fixture/bin/muse"}, BundleSHA256: strings.Repeat("e", 64), PromptSHA256: strings.Repeat("f", 64), Provenance: execution.NativeProvenance{TrustMode: "isolated_system_worker", Profile: collaboration.ProfileID(factory.NewID()), InstanceID: factory.NewID(), WorktreeID: "owned-checkout", BrokerRun: factory.NewID(), Isolation: &execution.NativeIsolation{DesktopUID: 1000, WorkerUID: 64596}, Custody: execution.NativeCustody{State: execution.NotAcquired}}, Retirement: execution.Running}
	r.Native.Provenance.Process = &execution.NativeProcess{InvocationID: factory.NewID(), SystemInstance: "12345678-1234-1234-1234-123456789abc", StartIdentity: "123456", PID: 1001, StartedAt: r.Started, Executable: r.Native.Tool.Executable, ExecutableSHA256: r.Native.Tool.SHA256}
	plan := phaseTestPlan(t, factory.NewID(), r.InputSHA, "f"+strings.Repeat("1", 24))
	planBytes, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	r.ExecutionManifest, err = project.MarshalExecutionManifest(project.ExecutionManifest{Plan: planBytes, PlanDigest: project.PlanDigest(planBytes), Invocation: r.ID, Phase: project.ExecutionImplement, Run: r.ID, Deadline: r.Deadline, Effort: project.TaskEffortLow, MaxChildren: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordFactoryRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO factory_run_views(run,repository,issue,attempt) VALUES($1,$2,1,$3)`, r.ID, repository.ID, factory.NewID()); err != nil {
		t.Fatal(err)
	}
	placed, err = s.SelectPlacement(ctx, host.ID, repository.ID, actor.ID, controller, placed.Revision, factory.PlacementRetiring)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SelectPlacement(ctx, host.ID, repository.ID, actor.ID, controller, placed.Revision, factory.PlacementInactive); !errors.Is(err, factory.ErrPlacement) {
		t.Fatal("unsettled native work released its controller", err)
	}
	anchor, err := execution.NativeCheckpoint(r.Native.Provenance)
	if err != nil {
		t.Fatal(err)
	}
	request := execution.CheckpointRequest{Project: r.ProjectID, ID: r.ID, Invocation: r.InvocationID(), ManifestDigest: project.ExecutionManifestDigest(r.ExecutionManifest), ExpectedHead: r.InputSHA, Native: anchor}
	result := execution.CheckpointReceipt{Request: request, FinalCandidate: true, Status: "pending"}
	for _, field := range []string{"profile", "instance", "worktree", "broker", "desktop_uid", "worker_uid", "boot", "start", "pid", "digest", "container", "kind"} {
		bad := result
		native := *anchor
		bad.Request.Native = &native
		forgedRun := r
		forgedNative := *r.Native
		forgedRun.Native = &forgedNative
		switch field {
		case "profile":
			native.Profile = collaboration.ProfileID(factory.NewID())
		case "instance":
			native.InstanceID = factory.NewID()
		case "worktree":
			native.WorktreeID = "another-checkout"
			forgedRun.Native.Provenance.WorktreeID = native.WorktreeID
		case "broker":
			native.BrokerRun = factory.NewID()
		case "desktop_uid":
			native.DesktopUID++
		case "worker_uid":
			native.WorkerUID++
		case "boot":
			native.SystemInstance = "another-boot"
		case "start":
			native.StartIdentity = "recycled-start"
		case "pid":
			native.PID++
		case "digest":
			native.ExecutableSHA256 = strings.Repeat("e", 64)
		case "container":
			bad.Request.Container = strings.Repeat("c", 64)
		case "kind":
			bad.Request.Native = nil
			bad.Request.Container = strings.Repeat("c", 64)
		}
		if err := s.SaveFactoryFinalCheckpoint(ctx, forgedRun, bad); err == nil {
			t.Fatal("foreign checkpoint anchor persisted", field)
		}
	}
	if err := s.SaveFactoryFinalCheckpoint(ctx, r, result); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	retainedPlacement, err := s.Placement(ctx, repository.ID)
	if err != nil || retainedPlacement != placed {
		t.Fatal("placement did not survive reopen", err)
	}
	saved, err := s.FactoryRun(ctx, r.ID)
	if err != nil || saved.FinalCheckpoint == nil || !saved.FinalCheckpoint.Request.Equal(request) || saved.Validate() != nil {
		t.Fatal("native pending receipt did not survive reopen", saved.FinalCheckpoint, err)
	}
	raw, err := json.Marshal(saved.FinalCheckpoint.Request)
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatal(err)
	}
	if _, exists := shape["container"]; exists || shape["native"] == nil {
		t.Fatal("native checkpoint fabricated an appliance binding", string(raw))
	}
	result.Status, result.Candidate, result.Tree, result.ReceiptDigest = "committed", request.ExpectedHead, strings.Repeat("b", 40), strings.Repeat("e", 64)
	if err := s.SaveFactoryFinalCheckpoint(ctx, saved, result); err != nil {
		t.Fatal(err)
	}
	changed := result
	changed.Tree = strings.Repeat("c", 40)
	if err := s.SaveFactoryFinalCheckpoint(ctx, saved, changed); err == nil {
		t.Fatal("terminal checkpoint bytes changed")
	}
	if err := s.SaveFactoryFinalCheckpoint(ctx, saved, result); err != nil {
		t.Fatal("exact receipt replay refused", err)
	}
}

// Real PostgreSQL retention/reopen and raw transactional writer checks. Process
// evidence is supplied fixture metadata; this does not qualify an executor.
func TestFactoryNativeRunEvidencePG(t *testing.T) {
	t.Run("captured_launch_binding", func(t *testing.T) {
		db, dsn := postgresFixture(t)
		seedDispatchLimits(t, db)
		ctx := t.Context()
		a, reservation, run, view := dispatchTestPacket(t, time.Now().UTC())
		a.AdoptedHarness = project.FactoryHarnessPin{Harness: project.FactoryHarnessMuse, Version: a.HarnessVers, SHA256: strings.Repeat("d", 64), Image: run.Image}
		if err := recordDispatchTestPacket(t, ctx, db, a, reservation, run, view); err != nil {
			t.Fatal(err)
		}
		run, err := db.FactoryRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		selection := run.Admission.Policy.Roles[a.Role]
		profile := factory.EffectiveExecutionProfile(&selection.Profile)
		input := project.FactoryLaunch{Run: project.FactoryRun{ID: run.ID, Project: a.ProjectID, Role: a.Role, Preparation: a.Preparation, Harness: run.Admission.Tool.Harness, HarnessVers: run.Admission.Tool.Version, Model: a.Model, Assignment: a.PromptSHA, SourceCommit: a.SourceCommit, Connection: a.Connection, Actor: a.ActorID, Deadline: run.Deadline, ExecutionPhase: a.Phase, Effort: profile.Effort, Contributor: profile.Contributor}, Prompt: a.Prompt, HarnessSHA256: run.Admission.Tool.SHA256}
		request, err := run.LaunchRequest(a, input)
		if err != nil || request.Appliance == nil || request.Native != nil || request.Appliance.Profile != *run.Admission.Profile || request.Appliance.Tool.Image != run.Image {
			t.Fatal("appliance launch did not retain its admitted profile/tool", err)
		}
		historical := run
		historicalAdmission := *run.Admission
		historicalAdmission.Policy.Roles = maps.Clone(run.Admission.Policy.Roles)
		historicalSelection := historicalAdmission.Policy.Roles[a.Role]
		historicalSelection.Profile = factory.ExecutionProfile{}
		historicalAdmission.Policy.Roles[a.Role] = historicalSelection
		historical.Admission = &historicalAdmission
		historicalInput := input
		historicalInput.Run.Effort, historicalInput.Run.Contributor = "", false
		if _, err := historical.LaunchRequest(a, historicalInput); err != nil {
			t.Fatal("historical CLI-default profile was replaced", err)
		}
		if _, err := historical.LaunchRequest(a, input); err == nil {
			t.Fatal("historical launch adopted current product defaults")
		}
		unproved := run
		oldAdmission := *run.Admission
		oldAdmission.Tool = &project.FactoryHarnessPin{}
		unproved.Admission = &oldAdmission
		if unproved.Validate() != nil {
			t.Fatal("historical admission metadata became unreadable")
		}
		if _, err := unproved.LaunchRequest(a, input); err == nil {
			t.Fatal("historical launch adopted an unproved current tool")
		}
		for _, field := range []string{"prompt", "actor", "connection", "preparation", "model", "deadline", "effort"} {
			changed := input
			switch field {
			case "prompt":
				changed.Prompt = []byte("different but self-consistent prompt")
				changed.Run.Assignment = project.FactoryPromptDigest(changed.Prompt)
			case "actor":
				changed.Run.Actor++
			case "connection":
				changed.Run.Connection = "another-connection"
			case "preparation":
				changed.Run.Preparation = "f" + strings.Repeat("a", 24)
			case "model":
				changed.Run.Model = "another-model"
			case "deadline":
				changed.Run.Deadline = changed.Run.Deadline.Add(time.Second)
			case "effort":
				changed.Run.Effort = ""
			}
			if _, err := run.LaunchRequest(a, changed); err == nil {
				t.Fatal("launch replaced captured assignment inputs", field)
			}
		}
		// Native admission below is a declared persistence fixture. Production
		// dispatch still refuses native placement until its owning gate exists.
		native := run
		native.ID, native.Image = factory.NewID(), ""
		native.Harness = "muse-fixture-1"
		native.Native = &execution.NativeRecord{Tool: execution.NativeTool{Harness: "muse", Version: "fixture-1", SHA256: strings.Repeat("d", 64), Executable: "/home/fixture/bin/muse"}, BundleSHA256: strings.Repeat("e", 64), PromptSHA256: a.PromptSHA, Provenance: execution.NativeProvenance{TrustMode: "isolated_system_worker", Profile: collaboration.ProfileID(strings.Repeat("a", 32)), InstanceID: strings.Repeat("b", 32), WorktreeID: "owned-launch-fixture", BrokerRun: strings.Repeat("c", 32), Custody: execution.NativeCustody{State: execution.NotAcquired}}, Retirement: execution.NotStarted}
		admission := *run.Admission
		admission.Profile, admission.Tool = nil, nil
		admission.Native = &execution.NativeLaunch{Workspace: execution.NativeWorkspaceRef{Profile: native.Native.Provenance.Profile, InstanceID: native.Native.Provenance.InstanceID, WorktreeID: native.Native.Provenance.WorktreeID}, Tool: native.Native.Tool, BundleSHA256: native.Native.BundleSHA256, PromptSHA256: native.Native.PromptSHA256, Runtime: collaboration.RuntimeID(strings.Repeat("e", 32)), PlacementGeneration: 7, TrustMode: "isolated_system_worker", BrokerRun: native.Native.Provenance.BrokerRun}
		native.Admission = &admission
		nativeAssignment := a
		nativeAssignment.ID, nativeAssignment.Run, nativeAssignment.RunHistory = factory.NewID(), native.ID, []string{native.ID}
		nativeAssignment.AttemptRoot, nativeAssignment.PublicationAssignment = nativeAssignment.ID, nativeAssignment.ID
		nativeAssignment.Harness, nativeAssignment.HarnessVers, nativeAssignment.AdoptedHarness = native.Harness, native.Native.Tool.Version, project.FactoryHarnessPin{}
		nativeInput := input
		nativeInput.Run.ID, nativeInput.Run.Harness, nativeInput.Run.HarnessVers, nativeInput.HarnessSHA256 = native.ID, native.Native.Tool.Harness, native.Native.Tool.Version, native.Native.Tool.SHA256
		if err := db.RecordFactoryRun(ctx, native); err == nil {
			t.Fatal("supervisor write admitted a native placement")
		}
		raw, err := json.Marshal(native)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.db.ExecContext(ctx, `INSERT INTO factory_runs(id,active,settled,data) VALUES($1,true,false,$2)`, native.ID, string(raw)); err != nil {
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
		native, err = db.FactoryRun(ctx, native.ID)
		if err != nil {
			t.Fatal(err)
		}
		request, err = native.LaunchRequest(nativeAssignment, nativeInput)
		if err != nil || request.Kind != collaboration.Native || request.Appliance != nil || request.Native == nil || request.Native.Runtime != admission.Native.Runtime || request.Native.PlacementGeneration != 7 || request.Native.BundleSHA256 != native.Native.BundleSHA256 || request.Native.PromptSHA256 != native.Native.PromptSHA256 {
			t.Fatal("native launch lost captured placement at reopen", err)
		}
		changedBundle := native
		bundleRecord := *native.Native
		bundleRecord.BundleSHA256 = strings.Repeat("a", 64)
		changedBundle.Native = &bundleRecord
		if _, err := changedBundle.LaunchRequest(nativeAssignment, nativeInput); err == nil {
			t.Fatal("native launch accepted a bundle digest different from admission")
		}
		changedPrompt := native
		promptRecord := *native.Native
		promptRecord.PromptSHA256 = strings.Repeat("b", 64)
		changedPrompt.Native = &promptRecord
		promptAdmission := *native.Admission
		promptLaunch := *native.Admission.Native
		promptLaunch.PromptSHA256 = promptRecord.PromptSHA256
		promptAdmission.Native = &promptLaunch
		changedPrompt.Admission = &promptAdmission
		if _, err := changedPrompt.LaunchRequest(nativeAssignment, nativeInput); err == nil {
			t.Fatal("native launch accepted a prompt digest different from its assignment")
		}
		legacy := native
		legacyRecord := *native.Native
		legacyRecord.Provenance.TrustMode, legacyRecord.Provenance.BrokerRun = "same_user_trusted_host", ""
		legacyRecord.BundleSHA256, legacyRecord.PromptSHA256 = "", ""
		legacy.Native = &legacyRecord
		legacyAdmission := *native.Admission
		legacyLaunch := *native.Admission.Native
		legacyLaunch.TrustMode, legacyLaunch.BrokerRun = "", ""
		legacyLaunch.BundleSHA256, legacyLaunch.PromptSHA256 = "", ""
		legacyAdmission.Native = &legacyLaunch
		legacy.Admission = &legacyAdmission
		if legacy.Validate() != nil {
			t.Fatal("historical same-user native evidence became unreadable")
		}
		if _, err := legacy.LaunchRequest(nativeAssignment, nativeInput); err == nil {
			t.Fatal("historical same-user native evidence acquired new launch authority")
		}
		legacyRequest := request
		legacyRequest.Native = &legacyLaunch
		if legacyRequest.Validate() == nil {
			t.Fatal("historical same-user launch metadata became an active instruction")
		}
		encoded, err := json.Marshal(request)
		if err != nil || bytes.Contains(encoded, []byte(`"image"`)) || bytes.Contains(encoded, []byte(`"uid"`)) || bytes.Contains(encoded, []byte(`"container"`)) {
			t.Fatal("native launch fabricated appliance fields", err)
		}
		observation := execution.RunObservation{Run: native.ID, Workspace: native.ProjectID, Role: native.Role, Kind: collaboration.Native, Phase: project.FactoryApproved, Retirement: execution.NotStarted, Native: &native.Native.Provenance}
		if !request.MatchesObservation(observation) {
			t.Fatal("matching native launch observation lost its scope")
		}
		foreign := *observation.Native
		foreign.WorktreeID = "another-worktree"
		observation.Native = &foreign
		if request.MatchesObservation(observation) {
			t.Fatal("launch accepted another native worktree's observation")
		}
		unplaced := native
		missing := *native.Admission
		missing.Native = nil
		unplaced.Admission = &missing
		if unplaced.Validate() != nil {
			t.Fatal("unplaced metadata became unreadable")
		}
		if _, err := unplaced.LaunchRequest(nativeAssignment, nativeInput); err == nil {
			t.Fatal("unplaced native evidence acquired launch authority")
		}
		sealed := native
		sealedRecord := *native.Native
		sealedRecord.Sealed = true
		sealed.Native = &sealedRecord
		if _, err := sealed.LaunchRequest(nativeAssignment, nativeInput); err == nil {
			t.Fatal("sealed native run acquired launch authority")
		}
		missingRuntime := request
		missingBinding := *request.Native
		missingBinding.Runtime = ""
		missingRuntime.Native = &missingBinding
		if missingRuntime.Validate() == nil {
			t.Fatal("native launch invented a runtime for unplaced work")
		}
		if _, err := db.db.ExecContext(ctx, `UPDATE factory_runs SET data=jsonb_set(data,'{admission,native,placement_generation}','"8"'::jsonb) WHERE id=$1`, native.ID); err == nil {
			t.Fatal("SQL replaced captured native placement generation")
		}
	})
	s, dsn := postgresFixture(t)
	ctx := t.Context()
	now := time.Now().UTC()
	r := factoryRun(now)
	r.Image = ""
	r.Harness = "muse-fixture-1"
	r.Native = &execution.NativeRecord{
		Tool:       execution.NativeTool{Harness: "muse", Version: "fixture-1", SHA256: strings.Repeat("d", 64), Executable: "/home/fixture/bin/agent"},
		Provenance: execution.NativeProvenance{TrustMode: "same_user_trusted_host", Profile: collaboration.ProfileID(strings.Repeat("a", 32)), InstanceID: strings.Repeat("b", 32), WorktreeID: "owned-fixture", Custody: execution.NativeCustody{State: execution.NotAcquired}},
		Retirement: execution.NotStarted,
	}
	if err := s.RecordFactoryRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"image", "credential_delegated", "credential_returned", "identity_binding", "identity_lease_id", "identity_generation", "appliance_invocation"} {
		if _, exists := shape[field]; exists {
			t.Fatal("native record fabricated appliance provenance", field)
		}
	}
	r.Native.Provenance.Process = &execution.NativeProcess{InvocationID: strings.Repeat("c", 32), SystemInstance: "12345678-1234-1234-1234-123456789abc", StartIdentity: "123456", PID: 1001, StartedAt: now, Executable: r.Native.Tool.Executable, ExecutableSHA256: r.Native.Tool.SHA256}
	r.Native.Provenance.Custody = execution.NativeCustody{State: execution.Held, AcquisitionID: "owned-acquisition", Connection: "owned-connection", Generation: 1}
	r.Native.Retirement = execution.Running
	if err := s.SaveFactoryRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err = s.FactoryRun(ctx, r.ID)
	if err != nil || r.InvocationID() != strings.Repeat("c", 32) || !r.CredentialsDelegated() || r.CredentialsReturned() {
		t.Fatal("native evidence lost at reopen", r, err)
	}
	changed := r
	n := *r.Native
	changed.Native = &n
	p := *n.Provenance.Process
	changed.Native.Provenance.Process = &p
	changed.Native.Provenance.Process.StartIdentity = "reused-pid-start"
	if err := s.SaveFactoryRun(ctx, changed); err == nil {
		t.Fatal("recycled process replaced retained invocation")
	}
	for _, expression := range []string{
		`data #- '{native}'`,
		`jsonb_set(data,'{native,tool,sha256}',to_jsonb(repeat('e',64)))`,
		`jsonb_set(data,'{native,provenance,process,start_identity}','"changed"')`,
		`jsonb_set(data,'{native,provenance,custody,acquisition_id}','"different-acquisition"')`,
		`data || '{"credential_returned":true}'`,
	} {
		if _, err := s.db.ExecContext(ctx, `UPDATE factory_runs SET data=`+expression+` WHERE id=$1`, r.ID); err == nil {
			t.Fatal("raw writer replaced native evidence", expression)
		}
	}
	exit := 0
	r.Native.Provenance.Process.ExitCode = &exit
	r.Native.Retirement = execution.Exited
	if err := s.SaveFactoryRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Outcome, r.Reconciled = factory.Succeeded, true
	if err := s.SaveFactoryRun(ctx, r); err == nil {
		t.Fatal("exit alone settled native custody")
	}
	r.Native.Retirement, r.Native.Sealed = execution.Retired, true
	if err := s.SaveFactoryRun(ctx, r); err == nil {
		t.Fatal("seal and retirement settled held custody")
	}
	r.Native.Provenance.Custody.State = execution.Released
	if err := s.SaveFactoryRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	for _, expression := range []string{
		`jsonb_set(data,'{native,sealed}','false')`,
		`jsonb_set(data,'{native,retirement}','"running"')`,
		`jsonb_set(data,'{native,provenance,custody,state}','"held"')`,
		`jsonb_set(data,'{native,provenance,process,exit_code}','1')`,
	} {
		if _, err := s.db.ExecContext(ctx, `UPDATE factory_runs SET data=`+expression+` WHERE id=$1`, r.ID); err == nil {
			t.Fatal("raw writer erased settled native evidence", expression)
		}
	}
	saved, err := s.FactoryRun(ctx, r.ID)
	if err != nil || !saved.Reconciled || !saved.Native.Settled() || !saved.CredentialsReturned() {
		t.Fatal("settled evidence did not survive refused writes", saved, err)
	}
}

func factoryFixture(t *testing.T) (*Store, time.Time) {
	t.Helper()
	s, _ := postgresFixture(t)
	return s, time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
}

func factoryRun(now time.Time) factory.Run {
	return factory.Run{ID: factory.NewID(), ProjectID: "p123456789012345678901234", Role: "coder", InputSHA: strings.Repeat("a", 40), Started: now, Deadline: now.Add(time.Hour), Image: "sha256:" + strings.Repeat("d", 64), Harness: "codex-0.157.1", Model: "test-model"}
}

func TestFactoryRunIdentityOnlyAdvances(t *testing.T) {
	s, now := factoryFixture(t)
	ctx := context.Background()
	r := factoryRun(now)
	if err := s.RecordFactoryRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordFactoryRun(ctx, r); err == nil {
		t.Fatal("consumed run identity reused")
	}
	r.IdentityLeaseID, r.IdentityGeneration, r.CredentialDelegated = "lease", 3, true
	id := r.ID
	r.IdentityBinding = &identity.Binding{Kind: identity.Factory, ID: id, Generation: 3}
	if err := s.SaveFactoryRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.IdentityGeneration = 4
	if err := s.SaveFactoryRun(ctx, r); err == nil {
		t.Fatal("reserved credential generation changed")
	}
	r.IdentityGeneration = 3
	r.Outcome, r.Summary, r.CredentialReturned, r.Reconciled = factory.Cancelled, "stopped by operator", true, true
	if err := s.SaveFactoryRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Outcome = factory.Failed
	if err := s.SaveFactoryRun(ctx, r); err == nil {
		t.Fatal("terminal outcome changed")
	}
	r.Outcome = factory.Cancelled
	r.Reconciled = false
	if err := s.SaveFactoryRun(ctx, r); err == nil {
		t.Fatal("settled run reopened")
	}
}

func TestFactoryAppliancePartialEvidencePG(t *testing.T) {
	s, now := factoryFixture(t)
	ctx := t.Context()
	r := factoryRun(now)
	r.ApplianceInvocation = &execution.ApplianceInvocation{Project: r.ProjectID, Invocation: strings.Repeat("a", 32)}
	if err := s.RecordFactoryRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	partial := r
	p := *r.ApplianceInvocation
	partial.ApplianceInvocation = &p
	r.ApplianceInvocation.Container, r.ApplianceInvocation.Unit = strings.Repeat("c", 64), "owned-run.service"
	r.ApplianceInvocation.UID, r.ApplianceInvocation.GID = 1001, 1002
	r.ApplianceInvocation.Generation, r.IdentityGeneration, r.IdentityLeaseID = 3, 3, "observed-lease"
	if err := s.SaveFactoryRun(ctx, r); err != nil {
		t.Fatal("unknown appliance fields could not resolve", err)
	}
	if err := s.SaveFactoryRun(ctx, partial); err == nil {
		t.Fatal("stale partial observation erased known fields")
	}
	for _, field := range []string{"invocation", "container", "unit", "uid", "generation"} {
		value := `"changed"`
		if field == "uid" {
			value = `1003`
		}
		if field == "generation" {
			value = `"4"`
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE factory_runs SET data=jsonb_set(data,$1::text[],$2::jsonb) WHERE id=$3`, "{appliance_invocation,"+field+"}", value, r.ID); err == nil {
			t.Fatal("raw writer changed captured appliance field", field)
		}
	}
	saved, err := s.FactoryRun(ctx, r.ID)
	if err != nil || saved.InvocationID() != r.InvocationID() || saved.ApplianceInvocation == nil || *saved.ApplianceInvocation != *r.ApplianceInvocation {
		t.Fatal("captured boundary did not survive refused writes", saved, err)
	}
}

func TestFactoryRunsListNewestFirst(t *testing.T) {
	s, now := factoryFixture(t)
	ctx := context.Background()
	var ids []string
	for range 3 {
		r := factoryRun(now)
		ids = append(ids, r.ID)
		if err := s.RecordFactoryRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.FactoryRuns(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != ids[2] || got[2].ID != ids[0] {
		t.Fatal("recorded runs out of order")
	}
	got, err = s.FactoryRuns(ctx, 2)
	if err != nil || len(got) != 2 {
		t.Fatal("run list bound ignored")
	}
}
