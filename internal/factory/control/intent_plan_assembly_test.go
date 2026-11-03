package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/host"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

// nativeExpandingContextRead mimics the native reader's instruction
// expansion: every response carries the requested files plus root and
// ancestor AGENTS.md at the requested commit.
func nativeExpandingContextRead(contents map[string]string) func(project.FactoryPreparationContextRequest, string) (project.FactoryPreparationContext, error) {
	return func(in project.FactoryPreparationContextRequest, role string) (project.FactoryPreparationContext, error) {
		seen := map[string]bool{}
		var files []project.FactoryContextFile
		add := func(path string) {
			if seen[path] {
				return
			}
			seen[path] = true
			content := "native instruction context: " + path
			if explicit, ok := contents[path]; ok {
				content = explicit
			}
			files = append(files, project.FactoryContextFile{Path: path, Content: []byte(content)})
		}
		add("AGENTS.md")
		for _, path := range in.Paths {
			add(path)
			parts := strings.Split(path, "/")
			prefix := ""
			for _, part := range parts[:len(parts)-1] {
				if prefix == "" {
					prefix = part
				} else {
					prefix += "/" + part
				}
				add(prefix + "/AGENTS.md")
			}
		}
		return project.FactoryPreparationContext{
			Project: in.Project, ID: in.ID, Role: role,
			SourceCommit: in.SourceCommit, ApprovedBase: in.ApprovedBase,
			DiffBase: in.DiffBase, Candidate: in.Candidate,
			Setup: []byte("native setup instructions"), Check: []byte("native required checks"),
			Files: files,
		}, nil
	}
}

func assemblySourceInputs(t *testing.T, paths []string) map[string]string {
	t.Helper()
	selected := make(map[string]string, len(paths))
	for _, path := range paths {
		sum := sha256.Sum256([]byte("native bytes: " + path))
		selected[path] = hex.EncodeToString(sum[:])
	}
	return selected
}

func assemblyContents(paths []string) map[string]string {
	contents := make(map[string]string, len(paths))
	for _, path := range paths {
		contents[path] = "native bytes: " + path
	}
	return contents
}

func assemblyHead(repo int64) factory.IntentRevision {
	return factory.IntentRevision{
		ID: "d888888888888888888888888", Intent: "d777777777777777777777777", Owner: 7, Repository: repo,
		Objective: "Decide vacation requests with manager review", State: "draft",
		MaxIssues: 10, CreatedUnix: time.Now().UTC().Truncate(time.Second).Unix(),
	}
}

// TestAssembleIntentPlanningInputReadsExpandedContext proves server-owned
// input assembly across chunked native reads: twelve approved files plus
// one nested path need bounded reads whose shared root AGENTS.md repeats
// identically, and the assembled evidence carries exactly the approved
// paths with digest-verified bytes — never native-added instruction
// files, never a repeated-path refusal.
func TestAssembleIntentPlanningInputReadsExpandedContext(t *testing.T) {
	t.Run("captured_native_workspace", func(t *testing.T) {
		ctx := t.Context()
		contents := map[string]string{"src/main.go": "declared native context fixture"}
		sum := sha256.Sum256([]byte(contents["src/main.go"]))
		db, dsn := dispatchTestDB(t)
		fx := dispatchSeed(t, db, map[string]string{"src/main.go": hex.EncodeToString(sum[:])})
		record, err := db.Preparation(ctx, "f111111111111111111111111")
		if err != nil {
			t.Fatal(err)
		}
		owner := execution.NativeWorkspaceRef{Profile: collaboration.ProfileID(strings.Repeat("a", 32)), InstanceID: strings.Repeat("b", 32), WorktreeID: "native-context-fixture"}
		record.Preparation.ID, record.Native = "f"+strings.Repeat("a", 24), &owner
		record.State.ID, record.State.Native, record.State.Container = record.Preparation.ID, &owner, ""
		if _, _, err := db.AdmitPreparation(ctx, record); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		db, err = store.Open(dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		record, err = db.Preparation(ctx, record.Preparation.ID)
		if err != nil {
			t.Fatal(err)
		}
		proj, err := db.ProjectByRepository(ctx, fx.repo)
		if err != nil {
			t.Fatal(err)
		}
		mode, reads := "exact", 0
		var cancelRead context.CancelFunc
		fx.host.contextObservation = func(request execution.PreparationContextRequest) (execution.PreparationContext, error) {
			reads++
			if request.Native == nil || *request.Native != owner || request.Input.ID != record.Preparation.ID || request.Input.Project != record.Preparation.Project || request.Role != project.RoleCoder {
				t.Fatal("context consumer dropped captured workspace scope")
			}
			material, err := nativeExpandingContextRead(contents)(request.Input, request.Role)
			observed := execution.PreparationContext{Material: material, Native: &owner}
			foreign := owner
			switch mode {
			case "profile":
				foreign.Profile = collaboration.ProfileID(strings.Repeat("c", 32))
				observed.Native = &foreign
			case "instance":
				foreign.InstanceID = strings.Repeat("c", 32)
				observed.Native = &foreign
			case "worktree":
				foreign.WorktreeID = "another-worktree"
				observed.Native = &foreign
			case "appliance_reply":
				observed.Native = nil
			case "cancelled_reply":
				// The consumer must discard a late reply even from a reader
				// that ignored cancellation; this does not execute setup.
				cancelRead()
			}
			return observed, err
		}
		c := &Coordinator{Store: db, Host: fx.host}
		deadline := time.Now().Add(time.Minute)
		plan := &attemptPlan{projectID: proj.ID, deadline: deadline}
		plan.inputs.Issue.Body = "Change src/main.go"
		for _, next := range []string{"exact", "profile", "instance", "worktree", "appliance_reply"} {
			mode = next
			material, err := readRepositoryContext(ctx, fx.host, plan, record, project.RoleCoder, record.Preparation.SourceCommit, record.Preparation.SourceCommit, record.Preparation.SourceCommit, true)
			if mode == "exact" {
				if err != nil || material == nil {
					t.Fatal("native repository context lost owner on reopen", err)
				}
			} else if err == nil || material != nil {
				t.Fatal("repository context accepted a foreign workspace", mode, err)
			}
			merged, wait, err := c.readPlanningContext(ctx, proj, record, []string{"src/main.go"}, deadline, fx.repo)
			if mode == "exact" {
				if err != nil || wait != nil || string(merged["src/main.go"]) != contents["src/main.go"] {
					t.Fatal("native planning context lost bytes or ownership", err, wait)
				}
			} else if err == nil || merged != nil {
				t.Fatal("planning context accepted a foreign workspace", mode, err)
			}
		}
		before := reads
		foreignProject := proj
		foreignProject.ID = "p" + strings.Repeat("e", 24)
		if _, _, err := c.readPlanningContext(ctx, foreignProject, record, []string{"src/main.go"}, deadline, fx.repo); err == nil || reads != before {
			t.Fatal("context read crossed its recorded project boundary", err)
		}
		mode = "cancelled_reply"
		bounded, cancel := context.WithCancel(ctx)
		defer cancel()
		cancelRead = cancel
		if material, err := readRepositoryContext(bounded, fx.host, plan, record, project.RoleCoder, record.Preparation.SourceCommit, record.Preparation.SourceCommit, record.Preparation.SourceCommit, true); !errors.Is(err, context.Canceled) || material != nil {
			t.Fatal("repository context accepted a cancelled reply", err)
		}
		bounded, cancelPlanning := context.WithCancel(ctx)
		defer cancelPlanning()
		cancelRead = cancelPlanning
		if material, wait, err := c.readPlanningContext(bounded, proj, record, []string{"src/main.go"}, deadline, fx.repo); material != nil || wait == nil || err != nil {
			t.Fatal("planning context accepted a cancelled reply", wait, err)
		}
	})
	t.Run("native HTTP exact aggregate boundary", func(t *testing.T) {
		ctx := context.Background()
		paths := []string{"one.mjs", "two.mjs"}
		contents := map[string]string{"one.mjs": strings.Repeat("a", project.MaxFactoryContextFileBytes), "two.mjs": strings.Repeat("b", project.MaxFactoryContextFileBytes)}
		inputs := map[string]string{}
		for name, content := range contents {
			sum := sha256.Sum256([]byte(content))
			inputs[name] = hex.EncodeToString(sum[:])
		}
		db, _ := dispatchTestDB(t)
		fx := dispatchSeed(t, db, inputs)
		var reads atomic.Int32
		var oversized atomic.Bool
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/prepare-context" {
				http.NotFound(w, r)
				return
			}
			var request project.FactoryPreparationContextRequest
			if json.NewDecoder(r.Body).Decode(&request) != nil || request.Validate() != nil {
				http.Error(w, "invalid native selection", http.StatusBadRequest)
				return
			}
			reads.Add(1)
			material, _ := nativeExpandingContextRead(contents)(request, project.RoleCoder)
			material.Setup = []byte(strings.Repeat("s", project.MaxFactoryContextFileBytes))
			material.Check = []byte(strings.Repeat("c", project.MaxFactoryContextFileBytes))
			for i := range material.Files {
				if material.Files[i].Path == "AGENTS.md" {
					material.Files[i].Content = []byte(strings.Repeat("i", project.MaxFactoryContextFileBytes))
				}
			}
			if err := material.ValidateFor(request, project.RoleCoder); err != nil {
				http.Error(w, "native context aggregate refused", http.StatusUnprocessableEntity)
				return
			}
			if oversized.Load() {
				// A malformed peer cannot bypass the production client's bound.
				material.Diff = []byte("x")
			}
			_ = json.NewEncoder(w).Encode(material)
		}))
		defer peer.Close()
		endpoint, _ := url.Parse(peer.URL)
		coord := &Coordinator{Store: db, Host: &host.Client{HTTP: &http.Client{Transport: deliveryPGTransport{target: endpoint, base: http.DefaultTransport}}}}
		input, _, wait, err := coord.AssembleIntentPlanningInput(ctx, assemblyHead(fx.repo), factory.IntentPlanningPropose)
		if err != nil || wait != nil || reads.Load() != int32(len(paths)) || len(input.Inspected) != len(paths) {
			t.Fatalf("bounded native reads failed: reads=%d inspected=%d wait=%+v err=%v", reads.Load(), len(input.Inspected), wait, err)
		}
		for i, source := range input.Inspected {
			if source.Path != paths[i] || source.Evidence != contents[source.Path] {
				t.Fatal("aggregate chunking lost or shortened approved source", source.Path)
			}
		}
		oversized.Store(true)
		if _, _, wait, err := coord.AssembleIntentPlanningInput(ctx, assemblyHead(fx.repo), factory.IntentPlanningPropose); err == nil && wait == nil {
			t.Fatal("production native client accepted an aggregate above its unchanged bound")
		}
	})
	ctx := context.Background()
	paths := []string{
		"internal/factory/a.go", "internal/factory/b.go", "internal/factory/c.go",
		"internal/factory/d.go", "internal/factory/e.go", "internal/factory/f.go",
		"internal/factory/g.go", "internal/factory/h.go", "internal/factory/i.go",
		"internal/factory/j.go", "internal/factory/k.go", "internal/factory/l.go",
		"internal/store/nested/deep/plan.go",
	}
	db, _ := dispatchTestDB(t)
	contents := assemblyContents(paths)
	// A native-approved source file may legitimately exceed the prose-report
	// limit. Carry every UTF-8 byte at the native file boundary, without a
	// shortened excerpt or a synthetic replacement source.
	contents[paths[0]] = strings.Repeat("é", project.MaxFactoryContextFileBytes/2)
	inputs := assemblySourceInputs(t, paths)
	fullDigest := sha256.Sum256([]byte(contents[paths[0]]))
	inputs[paths[0]] = hex.EncodeToString(fullDigest[:])
	fx := dispatchSeed(t, db, inputs)
	fx.host.contextRead = nativeExpandingContextRead(contents)
	coord := &Coordinator{Store: db, Host: fx.host}
	head := assemblyHead(fx.repo)
	in, prep, wait, err := coord.AssembleIntentPlanningInput(ctx, head, factory.IntentPlanningPropose)
	if err != nil {
		t.Fatal(err)
	}
	if wait != nil {
		t.Fatalf("expanded selection waited: %+v", wait)
	}
	if prep.ID != "f111111111111111111111111" || prep.SourceCommit != strings.Repeat("c", 40) {
		t.Fatalf("wrong preparation resolved: %+v", prep.ID)
	}
	if len(fx.host.contextRequests) < 2 {
		t.Fatalf("multi-chunk selection read once: %d", len(fx.host.contextRequests))
	}
	for _, request := range fx.host.contextRequests {
		if len(expandContextSelection(request.Paths)) > project.MaxFactoryContextPaths {
			t.Fatalf("read exceeds its native closure bound: %v", request.Paths)
		}
	}
	if len(in.Inspected) != len(paths) {
		t.Fatalf("inspected carries %d entries, want %d", len(in.Inspected), len(paths))
	}
	for i, ref := range in.Inspected {
		if ref.Path != paths[i] || ref.Commit != strings.Repeat("c", 40) || ref.Evidence != contents[paths[i]] {
			t.Fatalf("inspected entry not exact: %+v", ref)
		}
	}
	if in.Operation != factory.IntentPlanningPropose || in.Head.ID != head.ID {
		t.Fatalf("assembled input not bound: %+v", in.Operation)
	}
	oversized := in
	oversized.Inspected = append([]factory.InspectedSource(nil), in.Inspected...)
	oversized.Inspected[0].Evidence += "x"
	if err := oversized.Validate(); err == nil {
		t.Fatal("planner input accepted source evidence beyond the native file limit")
	}
	fx.host.contextRead = nativeExpandingContextRead(map[string]string{paths[0]: contents[paths[0]] + "x"})
	if _, _, _, err := coord.AssembleIntentPlanningInput(ctx, head, factory.IntentPlanningPropose); err == nil {
		t.Fatal("assembly accepted a native file beyond its existing context limit")
	}
	fx.host.contextRead = nativeExpandingContextRead(contents)
	// The production catalog consumes the same approved native preparation,
	// without executing or trusting a check selected by Product Lead.
	a := factory.Assignment{
		Repository: fx.repo, ProjectID: fx.proj, Phase: project.ExecutionDecompose, Role: project.RoleCoder, Preparation: prep.ID, SourceCommit: prep.SourceCommit,
		Subject: factory.ExecutionSubject{Kind: factory.SubjectIntent, Intent: &factory.IntentSubject{IntentID: head.Intent, RevisionID: head.ID, OwnerID: head.Owner, ScopeDigest: head.Digest()}},
	}
	zero, failed := 0, 1
	state := execution.PreparationState{Container: strings.Repeat("c", 64), ID: prep.ID, Project: prep.Project, Role: prep.Role, SourceCommit: prep.SourceCommit, SetupDigest: prep.SetupDigest, Ready: true, Phase: project.PrepareReady, SetupExit: &zero, CheckExit: &zero}
	fx.host.inspectPreparation = func(project.PrepareInspect) (execution.PreparationState, error) { return state, nil }
	catalog, err := coord.ApprovedProjectCheckCatalog(ctx, a)
	if err != nil || catalog.Project != prep.Project || catalog.Preparation != prep.ID || catalog.PreparationDigest != factory.DeliveryPreparationDigest(prep) || catalog.CheckDigest != factory.MCPInputDigest([]byte("native required checks")) {
		t.Fatalf("production catalog omitted protected approval binding: %+v %v", catalog, err)
	}
	if _, ok := catalog.LookupCheck(factory.ApprovedAppCheckID); !ok {
		t.Fatal("approved project check missing from production catalog")
	}
	if _, ok := catalog.LookupCheck("worker.chosen.shell"); ok {
		t.Fatal("worker shell became an approved check")
	}
	validState := state
	for _, fault := range []string{"source", "setup-digest", "stopped", "not-ready", "setup-failed", "check-failed", "check-unknown"} {
		state = validState
		switch fault {
		case "source":
			state.SourceCommit = strings.Repeat("e", 40)
		case "setup-digest":
			state.SetupDigest = strings.Repeat("e", 64)
		case "stopped":
			state.Stopped = true
		case "not-ready":
			state.Ready = false
		case "setup-failed":
			state.SetupExit = &failed
		case "check-failed":
			state.CheckExit = &failed
		case "check-unknown":
			state.CheckExit = nil
		}
		if _, err := coord.ApprovedProjectCheckCatalog(ctx, a); err == nil {
			t.Fatalf("catalog accepted native %s", fault)
		}
	}
	state = validState
	fx.host.contextRead = func(request project.FactoryPreparationContextRequest, role string) (project.FactoryPreparationContext, error) {
		material := fixturePreparationContext(request, role)
		material.Check = nil
		return material, nil
	}
	if _, err := coord.ApprovedProjectCheckCatalog(ctx, a); err == nil {
		t.Fatal("catalog accepted missing protected check")
	}
	if len(fx.host.launches) != 0 || len(fx.host.preparations) != 0 {
		t.Fatal("catalog read launched native work")
	}
}

// TestChunkApprovedSelectionsRejectsDeepPathAfterFlush proves the
// per-chunk closure bound holds past a flush: a deep path following a
// valid short path waits instead of violating its own read bound.
func TestChunkApprovedSelectionsRejectsDeepPathAfterFlush(t *testing.T) {
	if _, err := chunkApprovedSelections([]string{"a.go", "d1/d2/d3/d4/d5/d6/d7/d8/d9/d10/d11/f.go"}); err == nil {
		t.Fatal("deep path after flush kept its chunk")
	}
	chunks, err := chunkApprovedSelections([]string{"a.go", "d1/d2/f.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 {
		t.Fatalf("short paths lost their separate byte budgets: %v", chunks)
	}
}

// TestAssembleIntentPlanningInputReadsApprovedLargeSource proves the
// general product fix: an approved 31,674-byte source file (past the old
// 8 KiB per-file bound) reads fully through bounded native chunks with
// its full digest verified and untruncated, while a file past the
// supported 32 KiB evidence bound refuses truthfully instead of
// truncating.
func TestAssembleIntentPlanningInputReadsApprovedLargeSource(t *testing.T) {
	ctx := context.Background()
	path := "northstar-check.mjs"
	large := strings.Repeat("n", 31674)
	sum := sha256.Sum256([]byte(large))
	inputs := map[string]string{path: hex.EncodeToString(sum[:])}
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db, inputs)
	fx.host.contextRead = nativeExpandingContextRead(map[string]string{path: large})
	coord := &Coordinator{Store: db, Host: fx.host}
	in, _, wait, err := coord.AssembleIntentPlanningInput(ctx, assemblyHead(fx.repo), factory.IntentPlanningPropose)
	if err != nil || wait != nil || len(in.Inspected) != 1 || in.Inspected[0].Evidence != large {
		t.Fatalf("approved large source did not assemble fully: inspected=%d wait=%+v err=%v", len(in.Inspected), wait, err)
	}
	// Boundary: exactly MaxFactoryContextFileBytes still assembles in full.
	atCap := strings.Repeat("m", project.MaxFactoryContextFileBytes)
	atCapSum := sha256.Sum256([]byte(atCap))
	dbCap, _ := dispatchTestDB(t)
	fxCap := dispatchSeed(t, dbCap, map[string]string{path: hex.EncodeToString(atCapSum[:])})
	fxCap.host.contextRead = nativeExpandingContextRead(map[string]string{path: atCap})
	coordCap := &Coordinator{Store: dbCap, Host: fxCap.host}
	capped, _, wait, err := coordCap.AssembleIntentPlanningInput(ctx, assemblyHead(fxCap.repo), factory.IntentPlanningPropose)
	if err != nil || wait != nil || len(capped.Inspected) != 1 || capped.Inspected[0].Evidence != atCap {
		t.Fatalf("evidence-boundary source did not assemble fully: wait=%+v err=%v", wait, err)
	}
	// Oversize: MaxFactoryContextFileBytes+1 with a matching approval
	// digest still refuses instead of truncating.
	oversized := strings.Repeat("o", project.MaxFactoryContextFileBytes+1)
	overSum := sha256.Sum256([]byte(oversized))
	dbOver, _ := dispatchTestDB(t)
	fxOver := dispatchSeed(t, dbOver, map[string]string{path: hex.EncodeToString(overSum[:])})
	fxOver.host.contextRead = nativeExpandingContextRead(map[string]string{path: oversized})
	coordOver := &Coordinator{Store: dbOver, Host: fxOver.host}
	if _, _, wait, err := coordOver.AssembleIntentPlanningInput(ctx, assemblyHead(fxOver.repo), factory.IntentPlanningPropose); err == nil || wait != nil {
		t.Fatalf("oversized approved source assembled: wait=%+v err=%v", wait, err)
	}
}

// TestAssembleIntentPlanningInputRefusesConflictingRepeat proves a
// repeated approved file with contradictory bytes refuses instead of
// assembling evidence from an inconsistent read.
func TestAssembleIntentPlanningInputRefusesConflictingRepeat(t *testing.T) {
	ctx := context.Background()
	paths := []string{"internal/factory/intent.go"}
	db, _ := dispatchTestDB(t)
	fx := dispatchSeed(t, db, assemblySourceInputs(t, paths))
	contents := assemblyContents(paths)
	fx.host.contextRead = func(in project.FactoryPreparationContextRequest, role string) (project.FactoryPreparationContext, error) {
		result, err := nativeExpandingContextRead(contents)(in, role)
		if err != nil {
			return result, err
		}
		result.Files = append(result.Files, project.FactoryContextFile{Path: paths[0], Content: []byte("contradictory bytes")})
		return result, nil
	}
	coord := &Coordinator{Store: db, Host: fx.host}
	if _, _, wait, err := coord.AssembleIntentPlanningInput(ctx, assemblyHead(fx.repo), factory.IntentPlanningPropose); err == nil || wait != nil {
		t.Fatalf("conflicting repeat assembled: wait=%+v err=%v", wait, err)
	}
}
