package store

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

func grantTestTime() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) }

func grantStoreFixture(t *testing.T) *Store {
	t.Helper()
	db, _ := postgresFixture(t)
	return db
}

func grantTestPolicy() factory.RepositoryPolicy {
	return factory.RepositoryPolicy{
		Repository: 42, GrantedBy: 7, Enabled: true,
		TargetBranch: "refs/heads/main",
		Roles: map[string]factory.RoleSelection{
			project.RoleCoder:    {Account: "conn", Harness: project.FactoryHarnessMuse, HarnessVers: "0.157.1", Model: "test"},
			project.RoleReviewer: {Account: "conn", Harness: project.FactoryHarnessMuse, HarnessVers: "0.157.1", Model: "test"},
		},
		Checks:              []string{"native-ci/build"},
		MergeMethod:         factory.MergeFastForward,
		Publish:             factory.ActorBindingRef{TokenID: 11, ActorID: 12, Kind: factory.OpRefPublish},
		Create:              factory.ActorBindingRef{TokenID: 11, ActorID: 12, Kind: factory.OpPRCreate},
		Review:              factory.ActorBindingRef{TokenID: 13, ActorID: 14, Kind: factory.OpReviewSubmit},
		Merge:               factory.ActorBindingRef{TokenID: 15, ActorID: 16, Kind: factory.OpMerge},
		AttemptLimits:       factory.DefaultAttemptLimits(),
		ReassessmentMinutes: factory.DefaultReassessmentMinutes,
		IntentMinutes:       30,
	}
}

func TestPolicySaveIsCAS(t *testing.T) {
	db := grantStoreFixture(t)
	ctx := context.Background()
	supplied := grantTestPolicy()
	if err := db.SaveRepositoryPolicy(ctx, supplied); err != nil {
		t.Fatal(err)
	}
	stored, err := db.RepositoryPolicy(ctx, 42)
	if err != nil || stored.Revision != 1 || !stored.Enabled {
		t.Fatalf("policy: %+v %v", stored, err)
	}
	if stored.Roles[project.RoleCoder].Account != supplied.Roles[project.RoleCoder].Account {
		t.Fatal("selected paying account did not survive policy storage")
	}
	unselected := stored
	unselected.Roles = map[string]factory.RoleSelection{}
	for role, selection := range stored.Roles {
		selection.Account = ""
		unselected.Roles[role] = selection
	}
	if err := unselected.Validate(); err != nil {
		t.Fatal("historical policy must remain readable", err)
	}
	if err := db.SaveRepositoryPolicy(ctx, unselected); err == nil {
		t.Fatal("new policy save omitted paying account choices")
	}
	unselected.Enabled = false
	if err := db.SaveRepositoryPolicy(ctx, unselected); err != nil {
		t.Fatal("disabling historical policy required new spending choices", err)
	}
	input, err := db.FactoryAuthorityInput(ctx, 42)
	if err != nil || input.Sponsorship != nil || !slices.Contains(factory.EvaluateAuthority(input).Missing, factory.MissingSpendingAccount) {
		t.Fatal("historical missing account did not remain unavailable", input.Sponsorship, err)
	}
	stored, err = db.RepositoryPolicy(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Roles[project.RoleCoder].Profile.Effort != factory.DefaultEffort || supplied.Roles[project.RoleCoder].Profile.Effort != "" {
		t.Fatal("policy did not capture defaults independently of the caller's map")
	}
	stale := grantTestPolicy()
	stale.Paused = true
	if err = db.SaveRepositoryPolicy(ctx, stale); !errors.Is(err, ErrStaleRevision) {
		t.Fatal("stale policy revision saved", err)
	}
	stored.Paused = true
	if err = db.SaveRepositoryPolicy(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if _, err = db.RepositoryPolicy(ctx, 43); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing policy reported", err)
	}
	for _, method := range []string{"merge", "squash", "rebase"} {
		stored, err = db.RepositoryPolicy(ctx, 42)
		if err != nil {
			t.Fatal(err)
		}
		stored.MergeMethod = method
		if err = db.SaveRepositoryPolicy(ctx, stored); err != nil {
			t.Fatalf("save %s policy: %v", method, err)
		}
		got, readErr := db.RepositoryPolicy(ctx, 42)
		if readErr != nil || got.MergeMethod != method {
			t.Fatalf("lost selected method %s: %+v %v", method, got, readErr)
		}
	}
}

func TestCapacityAndOperatorGrant(t *testing.T) {
	db := grantStoreFixture(t)
	ctx := context.Background()
	if _, err := db.Capacity(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing capacity reported", err)
	}
	if err := db.SaveCapacity(ctx, factory.Capacity{UpdatedBy: 1, MaxConcurrentRuns: 2}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveCapacity(ctx, factory.Capacity{UpdatedBy: 1, MaxConcurrentRuns: 2}); !errors.Is(err, ErrStaleRevision) {
		t.Fatal("stale capacity saved", err)
	}
	grant := factory.OperatorGrant{Repository: 42, GrantedBy: 1, Active: true}
	if err := db.SaveOperatorGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	grant.Active = false
	if err := db.SaveOperatorGrant(ctx, grant); !errors.Is(err, ErrStaleRevision) {
		t.Fatal("stale operator grant saved", err)
	}
}

func TestSponsorships(t *testing.T) {
	db := grantStoreFixture(t)
	ctx := context.Background()
	sp := factory.Sponsorship{
		Repository: 42, GrantedBy: 9, ActorID: 8, ProjectID: "p" + strings.Repeat("a", 24), Connection: "conn-1", GrantID: "grant-1",
		Generation: 3, Roles: []string{project.RoleCoder}, AllowanceMinutes: 60, Active: true,
	}
	if err := db.SaveSponsorship(ctx, sp); err != nil {
		t.Fatal(err)
	}
	stored, err := db.Sponsorship(ctx, 42, "conn-1")
	if err != nil || stored.Revision != 1 {
		t.Fatalf("sponsorship: %+v %v", stored, err)
	}
	list, err := db.Sponsorships(ctx, 42)
	if err != nil || len(list) != 1 {
		t.Fatalf("sponsorships: %+v %v", list, err)
	}
	sp.Active = false
	if err = db.SaveSponsorship(ctx, sp); !errors.Is(err, ErrStaleRevision) {
		t.Fatal("stale sponsorship saved", err)
	}
	// The first active grant is eligible for both roles but is not selected.
	other := stored
	other.Revision, other.Connection = 0, "aaa-unselected"
	other.Roles = []string{project.RoleCoder, project.RoleReviewer}
	if err := db.SaveSponsorship(ctx, other); err != nil {
		t.Fatal(err)
	}
	reviewer := stored
	reviewer.Revision, reviewer.Connection = 0, "conn-2"
	reviewer.Roles = []string{project.RoleReviewer}
	if err := db.SaveSponsorship(ctx, reviewer); err != nil {
		t.Fatal(err)
	}
	policy := grantTestPolicy()
	coderChoice, reviewerChoice := policy.Roles[project.RoleCoder], policy.Roles[project.RoleReviewer]
	coderChoice.Account, reviewerChoice.Account = "conn-1", "conn-2"
	policy.Roles[project.RoleCoder], policy.Roles[project.RoleReviewer] = coderChoice, reviewerChoice
	if err := db.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	for role, expected := range map[string]string{project.RoleCoder: "conn-1", project.RoleReviewer: "conn-2"} {
		input, err := db.FactoryRoleAuthorityInput(ctx, 42, role)
		if err != nil || input.Sponsorship == nil || input.Sponsorship.Connection != expected {
			t.Fatalf("wrong %s paying account: %+v %v", role, input.Sponsorship, err)
		}
		if got := factory.PermittedCompletionRoles(policy.Roles, input.Sponsorships); len(got) != 2 {
			t.Fatalf("role-specific funding missing from completion: %v", got)
		}
	}
	reviewer.Revision, reviewer.Active = 1, false
	if err := db.SaveSponsorship(ctx, reviewer); err != nil {
		t.Fatal(err)
	}
	input, err := db.FactoryRoleAuthorityInput(ctx, 42, project.RoleReviewer)
	if err != nil || input.Sponsorship == nil || input.Sponsorship.Active || input.Sponsorship.Connection != reviewer.Connection {
		t.Fatalf("withdrawn selection switched accounts: %+v %v", input.Sponsorship, err)
	}
}

func TestDispatchWithdrawalOrdering(t *testing.T) {
	db := dispatchStoreFixture(t)
	ctx := context.Background()
	open, revision, _, err := db.DispatchState(ctx, 7)
	if err != nil || !open || revision != 0 {
		t.Fatal("fresh dispatch is not open", open, revision, err)
	}
	a, r, run, view := dispatchTestPacket(t, time.Now().UTC())
	expected := dispatchTestControlFor(t, db, a)
	withdrawal, err := db.WithdrawDispatch(ctx, 7, "policy_paused", "native:7")
	if err != nil || withdrawal.Revision != 1 {
		t.Fatalf("withdrawal: %+v %v", withdrawal, err)
	}
	if err = db.RecordDispatchPacket(ctx, expected, a, r, run, view); !errors.Is(err, ErrDispatchClosed) {
		t.Fatal("late dispatch escaped withdrawal", err)
	}
	assertDispatchPacketAbsent(t, db, a)
	repeated, err := db.WithdrawDispatch(ctx, 7, "policy_paused", "native:7")
	if err != nil || repeated.Revision != 1 {
		t.Fatalf("repeated withdrawal: %+v %v", repeated, err)
	}
	if _, _, err = db.ReopenDispatch(ctx, 7, 0); !errors.Is(err, ErrStaleRevision) {
		t.Fatal("stale reopen accepted", err)
	}
	if opened, revision, reopenErr := db.ReopenDispatch(ctx, 7, 1); reopenErr != nil || !opened || revision != 2 {
		t.Fatal(opened, revision, reopenErr)
	}
	if err = db.RecordDispatchPacket(ctx, expected, a, r, run, view); err != nil {
		t.Fatal("dispatch refused after reopen", err)
	}
}

func TestDispatchCausesComposeAndClearIndependently(t *testing.T) {
	db := grantStoreFixture(t)
	ctx := context.Background()
	first, err := db.WithdrawDispatch(ctx, 42, factory.CauseProjectStop, "native:7")
	if err != nil || first.Revision != 1 || len(first.ActiveCauses) != 1 {
		t.Fatalf("first cause: %+v %v", first, err)
	}
	second, err := db.WithdrawDispatch(ctx, 42, factory.CauseControlPaused, "native:8")
	if err != nil || second.Revision != first.Revision || second.Cause != first.Cause || second.ClosedBy != first.ClosedBy || len(second.ActiveCauses) != 2 {
		t.Fatalf("second cause changed first-closure receipt: %+v %v", second, err)
	}
	open, revision, state, err := db.DispatchState(ctx, 42)
	if err != nil || open || revision != 2 || len(state.ActiveCauses) != 2 {
		t.Fatalf("composed state: %v %d %+v %v", open, revision, state, err)
	}
	duplicate, err := db.WithdrawDispatch(ctx, 42, factory.CauseControlPaused, "different-principal")
	if err != nil || len(duplicate.ActiveCauses) != 2 {
		t.Fatalf("duplicate cause changed state: %+v %v", duplicate, err)
	}
	open, revision, err = db.ClearProjectStop(ctx, 42, revision)
	if err != nil || open || revision != 3 {
		t.Fatalf("clearing Project stop cleared independent pause: %v %d %v", open, revision, err)
	}
	open, revision, err = db.ReopenDispatch(ctx, 42, revision)
	if err != nil || !open || revision != 4 {
		t.Fatalf("resume did not clear final cause: %v %d %v", open, revision, err)
	}
	open, revision, state, err = db.DispatchState(ctx, 42)
	if err != nil || !open || revision != 4 || len(state.ActiveCauses) != 0 || state.Cause != factory.CauseProjectStop {
		t.Fatalf("open state lost first-closure receipt or retained causes: %v %d %+v %v", open, revision, state, err)
	}
}

func TestProjectStopCauseComposesAfterExistingPause(t *testing.T) {
	db := grantStoreFixture(t)
	ctx := context.Background()
	first, err := db.WithdrawDispatch(ctx, 42, factory.CauseControlPaused, "native:7")
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.WithdrawDispatch(ctx, 42, factory.CauseProjectStop, "native:8")
	if err != nil || second.Cause != first.Cause || second.ClosedBy != first.ClosedBy || len(second.ActiveCauses) != 2 {
		t.Fatalf("Project stop overwrote the first pause receipt: %+v %v", second, err)
	}
	open, revision, err := db.ClearProjectStop(ctx, 42, 2)
	if err != nil || open || revision != 3 {
		t.Fatalf("Start cleared the independent pause: %v %d %v", open, revision, err)
	}
	open, revision, _, err = db.DispatchState(ctx, 42)
	if err != nil || open || revision != 3 {
		t.Fatalf("pause did not remain active: %v %d %v", open, revision, err)
	}
	open, revision, err = db.ReopenDispatch(ctx, 42, revision)
	if err != nil || !open || revision != 4 {
		t.Fatalf("authorized Resume did not clear the final pause: %v %d %v", open, revision, err)
	}
}

func TestPolicyWithdrawalCommitsWithCancellationIntents(t *testing.T) {
	db := grantStoreFixture(t)
	ctx := context.Background()
	publication := publicationTestRecord()
	publication.Repository = 42
	if err := db.RecordPublication(ctx, publication); err != nil {
		t.Fatal("record publication", err)
	}
	merge := mergeTestRecord()
	merge.Repository = 42
	if err := db.RecordMerge(ctx, merge); err != nil {
		t.Fatal("record merge", err)
	}

	policy := grantTestPolicy()
	policy.Paused = true
	result, err := db.ApplyFactoryGrant(ctx, "native:7", policy)
	if err != nil || result.Revision != 1 {
		t.Fatalf("apply policy: %+v %v", result, err)
	}
	if !result.Withdrawn || result.Revision != 1 {
		t.Fatalf("result omitted policy withdrawal: %+v", result)
	}
	if !result.PublicationsPending {
		t.Fatalf("result omitted publication intent: %+v", result)
	}
	if !result.MergesPending {
		t.Fatalf("result omitted merge intent: %+v", result)
	}
	withdrawnPublication, err := db.PublicationByAssignment(ctx, publication.AssignmentID)
	if err != nil || !withdrawnPublication.WithdrawRequested {
		t.Fatalf("publication intent not committed: %+v %v", withdrawnPublication, err)
	}
	withdrawnMerge, err := db.MergeByPublication(ctx, merge.PublicationID)
	if err != nil || !withdrawnMerge.WithdrawRequested {
		t.Fatalf("merge intent not committed: %+v %v", withdrawnMerge, err)
	}
	if _, err := db.ApplyFactoryGrant(ctx, "native:7", policy); !errors.Is(err, ErrStaleRevision) {
		t.Fatal("stale policy save was accepted", err)
	}
	var commands int
	if err := db.db.QueryRowContext(ctx, `SELECT count(*) FROM factory_commands`).Scan(&commands); err != nil || commands != 0 {
		t.Fatal("policy wrote command receipts", commands, err)
	}
}

func TestPolicyWithdrawalFailureRollsBackGrant(t *testing.T) {
	db := grantStoreFixture(t)
	ctx := context.Background()
	if err := db.SaveRepositoryPolicy(ctx, grantTestPolicy()); err != nil {
		t.Fatal("save initial policy", err)
	}
	_, err := db.db.ExecContext(ctx, `CREATE FUNCTION refuse_dispatch_closure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NOT NEW.open THEN RAISE EXCEPTION 'injected dispatch closure failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER refuse_dispatch_closure BEFORE UPDATE ON factory_dispatch FOR EACH ROW EXECUTE FUNCTION refuse_dispatch_closure()`)
	if err != nil {
		t.Fatal("install closure failure trigger", err)
	}
	paused := grantTestPolicy()
	paused.Revision, paused.Paused = 1, true
	_, err = db.ApplyFactoryGrant(ctx, "native:7", paused)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Message != "injected dispatch closure failure" {
		t.Fatalf("policy did not reach closure failure: %v", err)
	}
	storedPolicy, err := db.RepositoryPolicy(ctx, 42)
	if err != nil {
		t.Fatal("read policy after failed withdrawal", err)
	}
	if storedPolicy.Revision != 1 || storedPolicy.Paused || !storedPolicy.Enabled {
		t.Errorf("policy committed despite failed closure: %+v", storedPolicy)
	}
	open, _, _, err := db.DispatchState(ctx, 42)
	if err != nil {
		t.Fatal("read dispatch after failed withdrawal", err)
	}
	if !open {
		t.Error("dispatch remained closed after rollback")
	}
}

func TestPolicyWithdrawalIgnoresCompletedHistory(t *testing.T) {
	db := grantStoreFixture(t)
	ctx := context.Background()
	// Retained history must not limit withdrawal. Only the two current
	// assignments should appear as outstanding work on the pause post.
	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for i := 0; i < 1027; i++ {
		a, _, _, _ := dispatchTestPacket(t, grantTestTime())
		a.Repository, a.Issue = 42, int64(i+1)
		a.Subject.Issue.Issue = a.Issue
		if i < 1025 {
			a.Stage, a.Outcome, a.Reason = factory.AssignmentFinished, factory.Failed, factory.AssignReasonRunFailed
			a.FinishedUnix = grantTestTime().Unix()
			result := factory.ResultSynthesized(a.ID, a.Run, "failed", "completed", a.FinishedUnix)
			a.Result = &result
		}
		data, marshalErr := json.Marshal(a)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,$5,$6,$7)`, a.ID, a.Repository, a.Issue, a.Run, a.Stage, a.Revision, string(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < storePublicationLimit; i++ {
		publication := publicationTestRecord()
		publication.Repository = 42
		if err := db.RecordPublication(ctx, publication); err != nil {
			t.Fatalf("record publication %d: %v", i, err)
		}
	}
	for i := 0; i < storeMergeLimit; i++ {
		merge := mergeTestRecord()
		merge.Repository = 42
		if err := db.RecordMerge(ctx, merge); err != nil {
			t.Fatalf("record merge %d: %v", i, err)
		}
	}

	policy := grantTestPolicy()
	policy.Paused = true
	result, err := db.ApplyFactoryGrant(ctx, "native:7", policy)
	if err != nil || result.Revision != 1 {
		t.Fatalf("apply policy: %+v %v", result, err)
	}

	if !result.PublicationsPending || !result.MergesPending {
		t.Fatalf("settings result omitted outstanding effects: %+v", result)
	}
	cards := factoryEventCards(t, db, 42)["F08"]
	if len(cards) != 1 || len(cards[0].Payload.Communication.Items) != 2 || cards[0].Payload.Communication.Items[1].Detail != "2 assigned work items" {
		t.Fatalf("pause post did not report current assignments: %+v", cards)
	}
	policyAfter, err := db.RepositoryPolicy(ctx, 42)
	if err != nil || !policyAfter.Paused || policyAfter.Revision != 1 {
		t.Fatalf("policy decision did not commit: %+v %v", policyAfter, err)
	}
	open, _, _, err := db.DispatchState(ctx, 42)
	if err != nil || open {
		t.Fatalf("dispatch gate did not close: open=%t err=%v", open, err)
	}
	var publicationCount, mergeCount int
	if err = db.db.QueryRowContext(ctx, `SELECT count(*) FROM factory_publications WHERE repository=42 AND COALESCE((data->>'withdraw_requested')::boolean,FALSE)`).Scan(&publicationCount); err != nil {
		t.Fatal("count latched publication intents", err)
	}
	if err = db.db.QueryRowContext(ctx, `SELECT count(*) FROM factory_merges WHERE repository=42 AND COALESCE((data->>'withdraw_requested')::boolean,FALSE)`).Scan(&mergeCount); err != nil {
		t.Fatal("count latched merge intents", err)
	}
	if publicationCount != storePublicationLimit || mergeCount != storeMergeLimit {
		t.Fatalf("atomic cancellation intent counts: publications=%d merges=%d", publicationCount, mergeCount)
	}
}
