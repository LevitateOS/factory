package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/host"
	"github.com/levitateos/sodaos/internal/identity"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

func dispatchDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func fixturePreparationContext(in project.FactoryPreparationContextRequest, role string) project.FactoryPreparationContext {
	files := make([]project.FactoryContextFile, 0, len(in.Paths))
	for _, path := range in.Paths {
		files = append(files, project.FactoryContextFile{Path: path, Content: []byte("synthetic selected source: " + path)})
	}
	return project.FactoryPreparationContext{
		Project: in.Project, ID: in.ID, Role: role,
		SourceCommit: in.SourceCommit, ApprovedBase: in.ApprovedBase,
		DiffBase: in.DiffBase, Candidate: in.Candidate,
		Setup: []byte("synthetic approved setup instructions"),
		Check: []byte("synthetic required checks"),
		Files: files,
		Diff:  []byte("synthetic exact candidate diff"),
	}
}

type fakeDispatchHost struct {
	launch             func(project.FactoryLaunch) (project.FactoryState, error)
	inspect            func(project.FactoryInspect) (project.FactoryState, error)
	stop               func(project.FactoryStop) (project.FactoryState, error)
	harness            func(string) (project.FactoryHarnessPin, error)
	families           []string
	launches           []project.FactoryLaunch
	contextRequests    []project.FactoryPreparationContextRequest
	contextRoles       []string
	contextRead        func(project.FactoryPreparationContextRequest, string) (project.FactoryPreparationContext, error)
	contextObservation func(execution.PreparationContextRequest) (execution.PreparationContext, error)
	prepare            func(project.FactoryCandidate) (execution.PreparationState, error)
	inspectPreparation func(project.PrepareInspect) (execution.PreparationState, error)
	stopPreparation    func(project.PrepareStop) (execution.PreparationState, error)
	preparations       []project.FactoryCandidate
}

func (f *fakeDispatchHost) FactoryLaunch(ctx context.Context, in project.FactoryLaunch) (project.FactoryState, error) {
	f.launches = append(f.launches, in)
	return f.launch(in)
}

func (f *fakeDispatchHost) FactoryInspect(ctx context.Context, in project.FactoryInspect) (project.FactoryState, error) {
	if f.inspect == nil {
		return project.FactoryState{}, host.ErrRunNotFound
	}
	return f.inspect(in)
}

func (f *fakeDispatchHost) FactoryHarness(ctx context.Context, family string) (project.FactoryHarnessPin, error) {
	f.families = append(f.families, family)
	return f.harness(family)
}

func (f *fakeDispatchHost) ReadWorkspaceContext(_ context.Context, scoped execution.PreparationContextRequest) (execution.PreparationContext, error) {
	in, role := scoped.Input, scoped.Role
	f.contextRequests = append(f.contextRequests, in)
	f.contextRoles = append(f.contextRoles, role)
	if f.contextObservation != nil {
		return f.contextObservation(scoped)
	}
	if f.contextRead != nil {
		material, err := f.contextRead(in, role)
		return execution.PreparationContext{Material: material}, err
	}
	return execution.PreparationContext{Material: fixturePreparationContext(in, role)}, nil
}

func (f *fakeDispatchHost) FactoryStop(ctx context.Context, in project.FactoryStop) (project.FactoryState, error) {
	if f.stop != nil {
		return f.stop(in)
	}
	return project.FactoryState{ID: in.ID, Project: in.Project, Phase: project.FactoryStopped, Retirement: "confirmed"}, nil
}

func (f *fakeDispatchHost) FactoryTakeover(ctx context.Context, in project.FactoryTakeover) (project.TakeoverResult, error) {
	return project.TakeoverResult{}, errors.New("unexpected host takeover")
}

func (f *fakeDispatchHost) RecoverWork(ctx context.Context, r execution.WorkRecoveryRequest) (execution.WorkRecoveryReceipt, error) {
	return host.NewFactoryObserver(f, nil).RecoverWork(ctx, r)
}

func (f *fakeDispatchHost) FactoryExport(ctx context.Context, in project.FactoryExport) (project.FactoryExportState, error) {
	return project.FactoryExportState{}, errors.New("unexpected host export")
}

func (*fakeDispatchHost) PrepareWorkspace(context.Context, execution.StandingPreparationRequest) (execution.PreparationState, error) {
	return execution.PreparationState{}, errors.New("unexpected standing preparation")
}

func (f *fakeDispatchHost) PrepareWorkspaceCandidate(_ context.Context, request execution.CandidatePreparationRequest) (execution.PreparationState, error) {
	in := request.Input
	f.preparations = append(f.preparations, in)
	if f.prepare != nil {
		return f.prepare(in)
	}
	return execution.PreparationState{ID: in.Preparation.ID, Project: in.Preparation.Project, Role: in.Preparation.Role, SourceCommit: in.Preparation.SourceCommit, SetupDigest: in.Preparation.SetupDigest, Container: strings.Repeat("c", 64), Phase: project.PrepareReady, Ready: true}, nil
}

func (f *fakeDispatchHost) InspectWorkspacePreparation(_ context.Context, address execution.PreparationAddress) (execution.PreparationState, error) {
	in := project.PrepareInspect{Project: address.Workspace, ID: address.ID}
	if f.inspectPreparation != nil {
		return f.inspectPreparation(in)
	}
	return execution.PreparationState{}, execution.ErrPreparationNotObserved
}

func (f *fakeDispatchHost) RetireWorkspacePreparation(_ context.Context, address execution.PreparationAddress) (execution.PreparationState, error) {
	in := project.PrepareStop{Project: address.Workspace, ID: address.ID}
	if f.stopPreparation != nil {
		return f.stopPreparation(in)
	}
	return execution.PreparationState{ID: in.ID, Project: in.Project, Stopped: true, Retirement: "confirmed"}, nil
}

type fakeDispatchBroker struct {
	get    func(kind, id string) (identity.Execution, error)
	close  func(kind, id string) error
	closed map[string]bool
}

func (f *fakeDispatchBroker) GetExecution(ctx context.Context, kind, id string) (identity.Execution, error) {
	if f.closed[id] {
		return identity.Execution{Kind: kind, ExecutionID: id, State: identity.ExecutionTerminal}, nil
	}
	if f.get == nil {
		return identity.Execution{}, identity.ErrNotFound
	}
	return f.get(kind, id)
}

func (f *fakeDispatchBroker) CloseExecution(ctx context.Context, kind, id string) error {
	if f.close != nil {
		return f.close(kind, id)
	}
	if f.closed == nil {
		f.closed = map[string]bool{}
	}
	f.closed[id] = true
	return nil
}

type fakeDispatchReads struct {
	inputs map[string]DispatchInputs
	err    error
	calls  int
}

func (f *fakeDispatchReads) ReadDispatchInputs(ctx context.Context, repository, issue string, commentIDs []string, targetRef string) (DispatchInputs, error) {
	f.calls++
	if f.err != nil {
		return DispatchInputs{}, f.err
	}
	in, ok := f.inputs[repository+"/"+issue]
	if !ok {
		return DispatchInputs{}, &AcceptanceRefusal{Reason: RefusalIncompleteEvidence}
	}
	return in, nil
}

type dispatchFixture struct {
	db       *store.Store
	host     *fakeDispatchHost
	broker   *fakeDispatchBroker
	reads    *fakeDispatchReads
	policy   factory.RepositoryPolicy
	proj     string
	repo     int64
	tip      string
	harness  string
	decision map[int64]factory.Acceptance
}

func dispatchTestDB(t *testing.T) (*store.Store, string) {
	t.Helper()
	db, dsn := postgresFixture(t)
	return db, dsn
}

func dispatchSeed(t *testing.T, db *store.Store, sourceInputs ...map[string]string) dispatchFixture {
	t.Helper()
	ctx := context.Background()
	fx := dispatchFixture{db: db, repo: 7, proj: "p765432109876543210987654", tip: strings.Repeat("c", 40), harness: "1.2.3", decision: map[int64]factory.Acceptance{}}
	if err := db.UpsertUser(ctx, store.User{ID: 7, Login: "soda-tester"}); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateProject(ctx, store.Project{ID: fx.proj, Name: "factory", RepositoryID: fx.repo, OwnerID: 7, Repository: "alice/factory"}); err != nil {
		t.Fatal(err)
	}
	bound, err := db.BindConfiguredFountainLedger(t.Context(), "https://fixture-fountain.example.test")
	if err != nil {
		t.Fatal(err)
	}
	repository, err := db.ObserveCollaborationRepository(ctx, bound.ID, "7", "alice/factory")
	if err != nil {
		t.Fatal(err)
	}
	fx.repo = int64(repository.ID)
	actor := func(kind string) factory.ActorBindingRef {
		return factory.ActorBindingRef{TokenID: 1, ActorID: 7, Kind: kind}
	}
	fx.policy = factory.RepositoryPolicy{
		Repository: fx.repo, GrantedBy: 7, Enabled: true, TargetBranch: "refs/heads/main",
		Roles: map[string]factory.RoleSelection{
			project.RoleCoder:    {Harness: project.FactoryHarnessMuse, HarnessVers: fx.harness, Model: "test-model"},
			project.RoleReviewer: {Harness: project.FactoryHarnessMuse, HarnessVers: fx.harness, Model: "test-model"},
		},
		Checks: []string{"ci"}, MergeMethod: factory.MergeFastForward,
		Publish: actor(factory.OpRefPublish), Create: actor(factory.OpPRCreate),
		Review: actor(factory.OpReviewSubmit), Merge: actor(factory.OpMerge),
		AttemptLimits:       factory.DefaultAttemptLimits(),
		ReassessmentMinutes: factory.DefaultReassessmentMinutes,
	}
	if err := db.SaveRepositoryPolicy(ctx, fx.policy); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCapacity(ctx, factory.Capacity{UpdatedBy: 7, MaxConcurrentRuns: 2}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveOperatorGrant(ctx, factory.OperatorGrant{Repository: fx.repo, GrantedBy: 7, Active: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSponsorship(ctx, factory.Sponsorship{
		Repository: fx.repo, GrantedBy: 7, ActorID: 7, ProjectID: fx.proj, Generation: 1, Connection: "conn", GrantID: "grant",
		Roles: []string{project.RoleCoder}, AllowanceMinutes: 120, Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveEnvironmentGrant(ctx, project.EnvironmentGrant{Repository: fx.repo, Owner: 7, Profile: &project.Profile{
		ID: project.RockyHeadless, Distribution: "rocky", Version: "9.6", Interface: "headless",
		Architecture: "amd64", Image: "sha256:" + strings.Repeat("b", 64), Revision: strings.Repeat("c", 40),
	}, Active: true}); err != nil {
		t.Fatal(err)
	}
	reqID, apprID := "d111111111111111111111111", "d222222222222222222222222"
	commit, digest := fx.tip, strings.Repeat("d", 64)
	var selected map[string]string
	if len(sourceInputs) > 0 {
		selected = sourceInputs[0]
	}
	if err := db.AdmitRequirementDecision(ctx, project.RequirementDecision{
		ID: reqID, Project: fx.proj, Approver: 7, SourceCommit: commit, SetupDigest: digest, InputsDigest: digest,
		SourceInputs: selected,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.AdmitApprovalDecision(ctx, project.ApprovalDecision{
		ID: apprID, Project: fx.proj, Requirement: reqID, Approver: 7,
		EffectsDigest: digest, ReadinessDigest: digest, Verified: true,
	}); err != nil {
		t.Fatal(err)
	}
	forrole := func(role, id string) {
		prep := execution.PreparationRecord{Preparation: project.Preparation{
			ID: id, Project: fx.proj, Role: role,
			Requirements: project.RequirementAcceptance{ID: reqID, Revision: 1, Approver: 7, SourceCommit: commit, Digest: digest, SourceInputs: selected},
			Approval:     project.AdminApproval{ID: apprID, Revision: 1, Approver: 7, EffectsDigest: digest},
			SourceCommit: commit, SetupDigest: digest, Tools: []string{"python3"},
		}}
		prep.State = execution.PreparationState{Container: strings.Repeat("c", 64), SourceCommit: prep.Preparation.SourceCommit, SetupDigest: prep.Preparation.SetupDigest, ID: id, Project: fx.proj, Role: role, Phase: project.PrepareReady, Ready: true}
		if _, _, err := db.AdmitPreparation(ctx, prep); err != nil {
			t.Fatal(err)
		}
	}
	forrole(project.RoleCoder, "f111111111111111111111111")
	forrole(project.RoleReviewer, "f222222222222222222222222")
	fx.host = &fakeDispatchHost{
		launch: func(in project.FactoryLaunch) (project.FactoryState, error) {
			return project.FactoryState{ID: in.Run.ID, Project: in.Run.Project, Role: in.Run.Role, Phase: project.FactoryCompleted}, nil
		},
		harness: func(family string) (project.FactoryHarnessPin, error) {
			return project.FactoryHarnessPin{Harness: family, Version: fx.harness, SHA256: strings.Repeat("a", 64), Image: "sha256:" + strings.Repeat("b", 64)}, nil
		},
	}
	fx.broker = &fakeDispatchBroker{}
	fx.reads = &fakeDispatchReads{inputs: map[string]DispatchInputs{}}
	return fx
}

func (fx *dispatchFixture) accept(t *testing.T, issue int64, id string, bodyOverride ...string) factory.Acceptance {
	t.Helper()
	title, body, answer := fmt.Sprintf("objective %d; inspect README.md", issue), fmt.Sprintf("body %d", issue), fmt.Sprintf("answer %d", issue)
	if len(bodyOverride) > 0 {
		body = bodyOverride[0]
	}
	decision := factory.Acceptance{
		ID: id, Repository: fx.repo, IssueIndex: fmt.Sprintf("%d", issue), Approver: 5, NativeRev: 41, TitleOccurrence: factory.OriginalOccurrence,
		TitleDigest: dispatchDigest(title), ContentDigest: dispatchDigest(body), ContentVersion: 2,
		Sources: []factory.SelectedSource{{ID: "11", ContentVersion: 0, Digest: dispatchDigest(answer)}},
	}
	if err := fx.db.AdmitAcceptanceDecision(context.Background(), decision); err != nil {
		t.Fatal(err)
	}
	fx.decision[issue] = decision
	fx.reads.inputs[fmt.Sprintf("%d/%d", fx.repo, issue)] = DispatchInputs{
		Issue: DispatchIssue{
			TitleOccurrence: factory.OriginalOccurrence, LifecycleOccurrence: factory.OriginalOccurrence,
			Title: title, Body: body, TitleDigest: decision.TitleDigest,
			ContentDigest: decision.ContentDigest, ContentVer: 2, Visible: true,
		},
		Comments: []DispatchComment{{ID: "11", Content: answer, Digest: dispatchDigest(answer), Visible: true}},
		Tip:      fx.tip, TipRef: "refs/heads/main", Revision: 41,
	}
	return decision
}

func (fx *dispatchFixture) queue(t *testing.T, issue int64, head string) {
	t.Helper()
	fx.queueAt(t, issue, head, time.Now())
}

func (fx *dispatchFixture) queueAt(t *testing.T, issue int64, head string, seen time.Time) {
	t.Helper()
	control := factory.IssueControl{
		Repository: fx.repo, Issue: issue, Acceptance: head,
		NativeRev: fx.reads.inputs[fmt.Sprintf("%d/%d", fx.repo, issue)].Revision,
		Readiness: factory.ReadinessQueued, Reason: factory.ReasonEligible,
		Fingerprint: strings.Repeat("1", 64), Authority: strings.Repeat("2", 64),
	}
	if _, _, err := fx.db.RecordIssueAssessment(context.Background(), control, seen); err != nil {
		t.Fatal(err)
	}
}

func dispatchControlForAssignment(t *testing.T, db *store.Store, a factory.Assignment) factory.IssueControl {
	t.Helper()
	control := factory.IssueControl{
		Repository: a.Repository, Issue: a.Issue, Acceptance: a.Acceptance, NativeRev: a.NativeRev,
		Readiness: factory.ReadinessQueued, Reason: factory.ReasonEligible,
		Fingerprint: strings.Repeat("1", 64), Authority: strings.Repeat("2", 64),
	}
	stored, _, err := db.RecordIssueAssessment(context.Background(), control, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func (fx *dispatchFixture) deps() DispatchDeps {
	coord := &Coordinator{Store: fx.db, Host: fx.host}
	var reads DispatchReads
	if fx.reads != nil {
		reads = fx.reads
	}
	return DispatchDeps{Store: fx.db, Host: fx.host, Broker: fx.broker, Reads: reads, Authority: coord.EffectiveAuthority, PrepareRetry: coord.prepareRetryCoder, RetireRetry: coord.retireRetryCoder}
}

func waitReason(report DispatchReport, issue int64) string {
	for _, wait := range report.Waits {
		if wait.Issue == issue {
			return wait.Reason
		}
	}
	return ""
}

type fakeAcceptanceSource struct {
	evidence map[string]AcceptanceEvidence
	reads    int
}

func (f *fakeAcceptanceSource) ReadAcceptanceEvidence(ctx context.Context, repository, issue string, commentIDs []string) (AcceptanceEvidence, error) {
	f.reads++
	evidence, ok := f.evidence[repository+"/"+issue]
	if !ok {
		return AcceptanceEvidence{}, &AcceptanceRefusal{Reason: RefusalIncompleteEvidence}
	}
	return evidence, nil
}

func (f *fakeDispatchHost) InspectRun(ctx context.Context, address execution.RunAddress) (execution.RunObservation, error) {
	return host.NewFactoryObserver(f, nil).InspectRun(ctx, address)
}

func (f *fakeDispatchHost) InspectTool(ctx context.Context, family string) (execution.ToolObservation, error) {
	return host.NewFactoryObserver(f, nil).InspectTool(ctx, family)
}

func (f *fakeDispatchHost) RetireRun(ctx context.Context, address execution.RunAddress) (execution.RunObservation, error) {
	return host.NewFactoryObserver(f, nil).RetireRun(ctx, address)
}

func (f *fakeDispatchHost) LaunchRun(ctx context.Context, launch execution.LaunchRequest) (execution.RunObservation, error) {
	return host.NewFactoryObserver(f, nil).LaunchRun(ctx, launch)
}

func (f *fakeDispatchBroker) ReadCustody(ctx context.Context, address execution.RunAddress) (execution.CustodyObservation, error) {
	return host.NewFactoryObserver(nil, f).ReadCustody(ctx, address)
}

func (f *fakeDispatchBroker) SealCustody(ctx context.Context, address execution.RunAddress) error {
	return host.NewFactoryObserver(nil, f).SealCustody(ctx, address)
}

func (f *fakeDispatchHost) ExportCandidate(ctx context.Context, request execution.CandidateRequest) (execution.CandidateBundle, error) {
	return host.NewFactoryObserver(f, nil).ExportCandidate(ctx, request)
}
