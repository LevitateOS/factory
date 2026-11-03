package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

func stallControl(repo, issue int64, readiness, reason string, blockers ...factory.Blocker) factory.IssueControl {
	return factory.IssueControl{
		Repository: repo, Issue: issue,
		Readiness: readiness, Reason: reason, Blockers: blockers,
		Fingerprint: strings.Repeat("1", 64), Authority: strings.Repeat("2", 64),
	}
}

func stallBlocker(code string, repo, issue int64) factory.Blocker {
	return factory.Blocker{
		Code: code, Resolution: factory.BlockerResolution(code),
		EndpointRepo: repo, EndpointIssue: issue,
	}
}

func stallQueued(repo, issue int64) StallInventoryIssue {
	return StallInventoryIssue{
		Repository: repo, Issue: issue,
		Control: stallControl(repo, issue, factory.ReadinessQueued, factory.ReasonEligible),
	}
}

func stallDigest(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

// T55: every named wait classifies distinctly and none alone calls an
// investigator, even beside an unrelated wait in the same repository.
func TestClassifyDistinctWaitsCallNoInvestigator(t *testing.T) {
	issues := []StallInventoryIssue{
		stallQueued(8, 1),
		stallQueued(8, 2),
		stallQueued(8, 3),
		stallQueued(8, 4),
		stallQueued(8, 5),
		stallQueued(8, 6),
		stallQueued(8, 7),
		stallQueued(8, 8),
		stallQueued(8, 9),
	}
	issues[0].ActiveRun = true
	issues[1].CIPending = true
	issues[2].DispatchWait = WaitCapacity
	issues[3].DispatchWait = WaitAllowance
	issues[4].DispatchWait = WaitPreparation
	issues[5].DispatchWait = WaitAuthority
	issues[6].Paused = true
	issues[7].ProductChoiceOpen = true
	issues[8].Fenced = true
	in := StallInventory{
		Generation: 3, Fingerprint: stallDigest("a"), Digest: stallDigest("b"),
		Complete: true, Issues: issues,
		Cycles: map[string]string{}, Producers: map[string]bool{},
	}
	classified := ClassifyStallInventory(in)
	if len(classified.Repositories) != 1 {
		t.Fatalf("repositories=%+v, want one", classified.Repositories)
	}
	repo := classified.Repositories[0]
	if repo.Kind != StallRepositoryZeroWait || repo.Investigate || repo.Event != nil {
		t.Fatalf("repo=%+v, want a zero-agent wait with no episode", repo)
	}
	want := map[int64]string{
		1: factory.StallWaitActiveWork, 2: factory.StallWaitCIChecks,
		3: factory.StallWaitCapacity, 4: factory.StallWaitQuota,
		5: factory.StallWaitPreparation, 6: factory.StallWaitPermissions,
		7: factory.StallWaitExplicitPause, 8: factory.StallWaitProductChoice,
		9: factory.StallWaitPendingCompletion,
	}
	for _, decision := range repo.Decisions {
		if decision.Kind != StallComponentWait || !decision.Named || decision.Investigate {
			t.Fatalf("decision=%+v, want a named wait without investigation", decision)
		}
		if decision.WaitKind != want[decision.Issue] {
			t.Fatalf("issue %d wait=%q, want %q", decision.Issue, decision.WaitKind, want[decision.Issue])
		}
		if decision.WaitOwner == "" || decision.WaitEvent == "" {
			t.Fatalf("issue %d names no owner or next event", decision.Issue)
		}
	}
}

// T55: partial, hidden or stale inventory gives attention, never deadlock.
func TestPartialInventoryCannotProveDeadlock(t *testing.T) {
	in := StallInventory{
		Generation: 3, Fingerprint: stallDigest("a"), Digest: stallDigest("b"),
		Complete: false,
		Issues:   []StallInventoryIssue{stallQueued(8, 1)},
		Cycles:   map[string]string{}, Producers: map[string]bool{},
	}
	if classified := ClassifyStallInventory(in); len(classified.Repositories) != 0 {
		t.Fatalf("partial inventory bound %+v, want no episode", classified.Repositories)
	}
	hidden := StallInventoryIssue{
		Repository: 8, Issue: 2,
		Control: stallControl(8, 2, factory.ReadinessNotAuthorized,
			factory.BlockerIssueInaccessible, stallBlocker(factory.BlockerIssueInaccessible, 0, 0)),
	}
	in = StallInventory{
		Generation: 3, Fingerprint: stallDigest("c"), Digest: stallDigest("d"),
		Complete: true, Issues: []StallInventoryIssue{hidden},
		Cycles: map[string]string{}, Producers: map[string]bool{},
	}
	classified := ClassifyStallInventory(in)
	if len(classified.Repositories) != 1 || classified.Repositories[0].Kind != StallRepositoryAttention {
		t.Fatalf("hidden evidence=%+v, want attention", classified.Repositories)
	}
	if classified.Repositories[0].Investigate || classified.Repositories[0].Event != nil {
		t.Fatal("hidden evidence called an investigator")
	}
}

// T55: an independent repairable component is not hidden by an
// unrelated wait in another repository.
func TestUnrelatedWaitHidesNoRepairableComponent(t *testing.T) {
	waiting := stallQueued(8, 1)
	waiting.DispatchWait = WaitCapacity
	cyclic := StallInventoryIssue{
		Repository: 9, Issue: 1,
		Control: stallControl(9, 1, factory.ReadinessBlocked,
			factory.BlockerCycle, stallBlocker(factory.BlockerCycle, 0, 0)),
	}
	in := StallInventory{
		Generation: 3, Fingerprint: stallDigest("e"), Digest: stallDigest("f"),
		Complete: true, Issues: []StallInventoryIssue{waiting, cyclic},
		Cycles: map[string]string{}, Producers: map[string]bool{},
	}
	classified := ClassifyStallInventory(in)
	if len(classified.Repositories) != 2 {
		t.Fatalf("repositories=%+v, want both classified", classified.Repositories)
	}
	if classified.Repositories[0].Kind != StallRepositoryZeroWait {
		t.Fatalf("waiting repo=%+v, want zero wait", classified.Repositories[0])
	}
	stalled := classified.Repositories[1]
	if stalled.Kind != StallRepositoryStall || !stalled.Investigate || stalled.Event == nil {
		t.Fatalf("cyclic repo=%+v, want one bound episode", stalled)
	}
	if stalled.Event.Reason != factory.StallReasonCycle {
		t.Fatalf("reason=%q, want cycle", stalled.Event.Reason)
	}
	if err := stalled.Event.Validate(); err != nil {
		t.Fatalf("bound event invalid: %v", err)
	}
}

// T56: a cycle before first completion, a missing outcome producer
// and unexplained no-ready each create one bound stall episode.
func TestStallDiscoveryBindsOneEpisodePerCause(t *testing.T) {
	cases := []struct {
		name    string
		control factory.IssueControl
		reason  string
	}{
		{"cycle", stallControl(9, 1, factory.ReadinessBlocked,
			factory.BlockerCycle, stallBlocker(factory.BlockerCycle, 0, 0)), factory.StallReasonCycle},
		{"missing producer", stallControl(9, 2, factory.ReadinessBlocked,
			factory.BlockerCodePending, stallBlocker(factory.BlockerCodePending, 9, 7)), factory.StallReasonMissingProducer},
		{"unexplained", stallControl(9, 3, factory.ReadinessBlocked,
			factory.BlockerDependencyDepth, stallBlocker(factory.BlockerDependencyDepth, 0, 0)), factory.StallReasonUnexplainedNoReady},
	}
	for _, tc := range cases {
		in := StallInventory{
			Generation: 3, Fingerprint: stallDigest("g"), Digest: stallDigest("h"),
			Complete: true,
			Issues:   []StallInventoryIssue{{Repository: 9, Issue: tc.control.Issue, Control: tc.control}},
			Cycles:   map[string]string{}, Producers: map[string]bool{},
		}
		classified := ClassifyStallInventory(in)
		if len(classified.Repositories) != 1 {
			t.Fatalf("%s: repositories=%+v", tc.name, classified.Repositories)
		}
		repo := classified.Repositories[0]
		if repo.Kind != StallRepositoryStall || repo.Event == nil {
			t.Fatalf("%s: repo=%+v, want one bound episode", tc.name, repo)
		}
		if repo.Event.Reason != tc.reason {
			t.Fatalf("%s: reason=%q, want %q", tc.name, repo.Event.Reason, tc.reason)
		}
		if err := repo.Event.Validate(); err != nil {
			t.Fatalf("%s: bound event invalid: %v", tc.name, err)
		}
	}
}

// T56: ready work resumes selection and stales an obsolete
// investigation; all outcomes delivered enters verification.
func TestReadyWorkResumesAndStalesObsoleteInvestigation(t *testing.T) {
	cyclic := StallInventoryIssue{
		Repository: 9, Issue: 1,
		Control: stallControl(9, 1, factory.ReadinessBlocked,
			factory.BlockerCycle, stallBlocker(factory.BlockerCycle, 0, 0)),
	}
	in := StallInventory{
		Generation: 4, Fingerprint: stallDigest("i"), Digest: stallDigest("j"),
		Complete: true,
		Issues:   []StallInventoryIssue{cyclic, stallQueued(9, 2)},
		Cycles:   map[string]string{}, Producers: map[string]bool{},
		PriorEvents: map[int64]map[string]string{9: {"old-event": stallDigest("h")}},
	}
	classified := ClassifyStallInventory(in)
	if len(classified.Repositories) != 1 {
		t.Fatalf("repositories=%+v", classified.Repositories)
	}
	repo := classified.Repositories[0]
	if repo.Kind != StallRepositoryReady || repo.Event != nil || repo.Investigate {
		t.Fatalf("repo=%+v, want ready selection without an episode", repo)
	}
	if len(repo.StalePrior) != 1 || repo.StalePrior[0] != "old-event" {
		t.Fatalf("stale=%+v, want the obsolete investigation retired", repo.StalePrior)
	}
	delivered := StallInventory{
		Generation: 5, Fingerprint: stallDigest("k"), Digest: stallDigest("l"),
		Complete: true,
		Issues: []StallInventoryIssue{{
			Repository: 9, Issue: 3,
			Control: stallControl(9, 3, factory.ReadinessCompleted, factory.ReasonEligible),
		}},
		Cycles: map[string]string{}, Producers: map[string]bool{},
	}
	classified = ClassifyStallInventory(delivered)
	if len(classified.Repositories) != 1 || classified.Repositories[0].Kind != StallRepositoryVerify {
		t.Fatalf("delivered=%+v, want verification instead of a stall", classified.Repositories)
	}
}

// TestReadyWorkStalesOnlyItsOwnRepository proves prior-event
// isolation: ready work in one repository never retires another
// repository's standing events, while its own obsolete
// investigation still retires.
func TestReadyWorkStalesOnlyItsOwnRepository(t *testing.T) {
	in := StallInventory{
		Generation: 4, Fingerprint: stallDigest("m"), Digest: stallDigest("n"),
		Complete: true,
		Issues:   []StallInventoryIssue{stallQueued(9, 1), stallQueued(10, 1)},
		Cycles:   map[string]string{}, Producers: map[string]bool{},
		PriorEvents: map[int64]map[string]string{
			9:  {"e9": stallDigest("o")},
			10: {"e10": stallDigest("p")},
		},
	}
	classified := ClassifyStallInventory(in)
	if len(classified.Repositories) != 2 {
		t.Fatalf("repositories=%+v, want both ready repositories", classified.Repositories)
	}
	byRepo := map[int64]StallRepositoryResult{}
	for _, result := range classified.Repositories {
		byRepo[result.Repository] = result
	}
	if got := byRepo[9]; len(got.StalePrior) != 1 || got.StalePrior[0] != "e9" {
		t.Fatalf("repo9 stale=%+v, want only its own retired event", got.StalePrior)
	}
	if got := byRepo[10]; len(got.StalePrior) != 1 || got.StalePrior[0] != "e10" {
		t.Fatalf("repo10 stale=%+v, want only its own retired event", got.StalePrior)
	}

	foreign := StallInventory{
		Generation: 4, Fingerprint: stallDigest("m"), Digest: stallDigest("n"),
		Complete: true,
		Issues:   []StallInventoryIssue{stallQueued(9, 1), stallQueued(10, 1)},
		Cycles:   map[string]string{}, Producers: map[string]bool{},
		PriorEvents: map[int64]map[string]string{
			10: {"e10": stallDigest("p")},
		},
	}
	classified = ClassifyStallInventory(foreign)
	byRepo = map[int64]StallRepositoryResult{}
	for _, result := range classified.Repositories {
		byRepo[result.Repository] = result
	}
	if got := byRepo[9]; len(got.StalePrior) != 0 {
		t.Fatalf("repo9 stale=%+v, want no foreign retirement", got.StalePrior)
	}
}

// stallSeedControl records one valid control on the real store path.
func stallSeedControl(t *testing.T, db *store.Store, repo, issue int64, readiness, reason string, blockers ...factory.Blocker) {
	t.Helper()
	control := factory.IssueControl{
		Repository: repo, Issue: issue,
		Readiness: readiness, Reason: reason, Blockers: blockers,
		Fingerprint: strings.Repeat("1", 64), Authority: strings.Repeat("2", 64),
	}
	if _, _, err := db.RecordIssueAssessment(context.Background(), control, time.Now()); err != nil {
		t.Fatalf("seed control %d/%d: %v", repo, issue, err)
	}
}

// T55 on the real serving/store path: distinct waits seed through the
// production assessment record, read back through the store inventory,
// and classify with no investigator. The disposable PG fixture
// (SODA_PG_*) skips when absent.
func TestStallWaitClassificationPG(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	coord := &Coordinator{Store: db}
	const repo = 18
	stallSeedControl(t, db, repo, 1, factory.ReadinessBlocked,
		factory.BlockerAuthorityMissing, stallBlocker(factory.BlockerAuthorityMissing, 0, 0))
	stallSeedControl(t, db, repo, 2, factory.ReadinessBlocked,
		factory.BlockerAcceptanceMissing, stallBlocker(factory.BlockerAcceptanceMissing, 0, 0))
	stallSeedControl(t, db, repo, 3, factory.ReadinessQueued, factory.ReasonEligible)
	stallSeedControl(t, db, repo, 4, factory.ReadinessQueued, factory.ReasonEligible)
	classified, err := coord.DetectRepairStalls(ctx, StallDetectDeps{
		Store: db, Generation: 7,
		DispatchWaits: []DispatchWait{
			{Repository: repo, Issue: 3, Reason: WaitCapacity, Detail: "appliance runs at its limit"},
		},
		Paused: map[string]bool{stallIssueKey(repo, 4): true},
	})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(classified.Repositories) != 1 {
		t.Fatalf("repositories=%+v, want the seeded repository", classified.Repositories)
	}
	result := classified.Repositories[0]
	// Issue 3 is queued but capacity-waited; nothing is selectable, so
	// the repository holds a zero-agent wait with no episode.
	if result.Kind != StallRepositoryZeroWait || result.Investigate || result.Event != nil {
		t.Fatalf("repo=%+v, want a zero-agent wait with no episode", result)
	}
	byIssue := map[int64]StallComponentDecision{}
	for _, decision := range result.Decisions {
		byIssue[decision.Issue] = decision
	}
	if byIssue[1].WaitKind != factory.StallWaitPermissions || !byIssue[1].Named {
		t.Fatalf("authority wait=%+v", byIssue[1])
	}
	if byIssue[2].WaitKind != factory.StallWaitProductChoice || !byIssue[2].Named {
		t.Fatalf("adoption wait=%+v", byIssue[2])
	}
	if byIssue[3].WaitKind != factory.StallWaitCapacity || !byIssue[3].Named {
		t.Fatalf("capacity wait=%+v", byIssue[3])
	}
	if byIssue[4].WaitKind != factory.StallWaitExplicitPause || !byIssue[4].Named {
		t.Fatalf("pause wait=%+v", byIssue[4])
	}
	for issue, decision := range byIssue {
		if decision.Investigate {
			t.Fatalf("issue %d wait called an investigator", issue)
		}
	}
}

// T56 on the real serving/store path: an assessment-established cycle
// binds one episode with (repository, generation, fingerprint)
// uniqueness and (repository, generation) episode identity; adding
// ready work resumes selection instead.
func TestStallDiscoveryPG(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	coord := &Coordinator{Store: db}
	const repo = 19
	stallSeedControl(t, db, repo, 1, factory.ReadinessBlocked,
		factory.BlockerCycle, stallBlocker(factory.BlockerCycle, 0, 0))
	stallSeedControl(t, db, repo, 2, factory.ReadinessBlocked,
		factory.BlockerCodePending, stallBlocker(factory.BlockerCodePending, repo, 7))
	first, err := coord.DetectRepairStalls(ctx, StallDetectDeps{Store: db, Generation: 7})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(first.Repositories) != 1 {
		t.Fatalf("repositories=%+v", first.Repositories)
	}
	result := first.Repositories[0]
	if result.Kind != StallRepositoryStall || result.Event == nil {
		t.Fatalf("repo=%+v, want one bound episode", result)
	}
	if err := result.Event.Validate(); err != nil {
		t.Fatalf("bound event invalid: %v", err)
	}
	episode, eventID := result.Event.EpisodeID, result.Event.ID
	second, err := coord.DetectRepairStalls(ctx, StallDetectDeps{Store: db, Generation: 7})
	if err != nil {
		t.Fatalf("redetect: %v", err)
	}
	again := second.Repositories[0]
	if again.Event == nil || again.Event.EpisodeID != episode || again.Event.ID != eventID {
		t.Fatalf("redetect=%+v, want the identical bound episode", again.Event)
	}
	// New ready work changes the fingerprint: the episode persists
	// while the stale event retires to fresh selection.
	stallSeedControl(t, db, repo, 3, factory.ReadinessQueued, factory.ReasonEligible)
	third, err := coord.DetectRepairStalls(ctx, StallDetectDeps{Store: db, Generation: 7})
	if err != nil {
		t.Fatalf("detect after ready: %v", err)
	}
	resumed := third.Repositories[0]
	if resumed.Kind != StallRepositoryReady || resumed.Event != nil {
		t.Fatalf("resumed=%+v, want ready selection without an episode", resumed)
	}
}

// TestExceededCycleWalkFailsClosedToIncomplete proves the bounded
// dependency walk cannot establish a cycle it did not finish: a
// two-level fan-out past the 1024-node budget marks the inventory
// incomplete, and classification binds no episode from it.
func TestExceededCycleWalkFailsClosedToIncomplete(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	coord := &Coordinator{Store: db}
	const repo = 41
	decisionID := func(tag string) string {
		sum := sha256.Sum256([]byte("stall-fanout\x00" + tag))
		return "d" + hex.EncodeToString(sum[:])[:24]
	}
	// Root fans to 50 mids; each mid fans to 50 leaves. The walk
	// visits past the node budget during the mids' expansion while
	// leaf decisions are never recorded.
	prereqs := func(first, count int, leafBase int64) []factory.AcceptedPrerequisite {
		out := make([]factory.AcceptedPrerequisite, 0, count)
		for i := 0; i < count; i++ {
			out = append(out, factory.AcceptedPrerequisite{
				Occurrence:   strconv.FormatInt(int64(first+i), 10),
				DependsOn:    strconv.FormatInt(int64(first+i), 10),
				EndpointRepo: repo, EndpointIssue: leafBase + int64(i),
				Outcome: factory.PrereqResult,
			})
		}
		return out
	}
	admit := func(issue int64, id string, prereq []factory.AcceptedPrerequisite) {
		t.Helper()
		decision := factory.Acceptance{
			ID: id, Repository: repo, IssueIndex: strconv.FormatInt(issue, 10),
			Approver: 7, NativeRev: 41,
			TitleDigest: stallDigest("title"), ContentDigest: stallDigest("body"),
			TitleOccurrence: "1", Prerequisites: prereq,
		}
		if err := db.AdmitAcceptanceDecision(ctx, decision); err != nil {
			t.Fatalf("admit decision %d: %v", issue, err)
		}
	}
	admit(1, decisionID("root"), prereqs(1, 50, 2))
	for mid := int64(0); mid < 50; mid++ {
		admit(2+mid, decisionID("mid-"+strconv.FormatInt(mid, 10)), prereqs(1000, 50, 10000+mid*50))
	}
	if _, exceeded, err := coord.findPrereqCycle(ctx, repo, 1); err != nil || !exceeded {
		t.Fatalf("fan-out walk: exceeded=%v err=%v, want the failed-closed bound", exceeded, err)
	}
	stallSeedControl(t, db, repo, 1, factory.ReadinessBlocked,
		factory.BlockerCycle, stallBlocker(factory.BlockerCycle, 0, 0))
	classified, err := coord.DetectRepairStalls(ctx, StallDetectDeps{Store: db, Generation: 3})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(classified.Repositories) != 0 {
		t.Fatalf("repositories=%+v, want no episode from an incomplete walk", classified.Repositories)
	}
}

// TestPendingPrereqNeedsItsOwnEndpointHead proves exact endpoint
// identity: a pending prerequisite whose own endpoint has no recorded
// head claims missing-producer stall even when an unrelated
// occurrence carries a head; a recorded endpoint head waits quietly.
func TestPendingPrereqNeedsItsOwnEndpointHead(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	coord := &Coordinator{Store: db}
	const repo = 42
	headless := stallControl(repo, 1, factory.ReadinessBlocked, factory.BlockerCodePending,
		stallBlocker(factory.BlockerCodePending, repo, 99))
	headless.EndpointHeads = map[string]string{"7": decisionForTest("unrelated")}
	if _, _, err := db.RecordIssueAssessment(ctx, headless, time.Now()); err != nil {
		t.Fatalf("seed headless control: %v", err)
	}
	classified, err := coord.DetectRepairStalls(ctx, StallDetectDeps{Store: db, Generation: 3})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(classified.Repositories) != 1 {
		t.Fatalf("repositories=%+v, want the seeded repository", classified.Repositories)
	}
	result := classified.Repositories[0]
	if result.Kind != StallRepositoryStall || !result.Investigate || result.Event == nil {
		t.Fatalf("repo=%+v, want missing-producer stall, not an unrelated-head wait", result)
	}
	if result.Event.Reason != factory.StallReasonMissingProducer {
		t.Fatalf("reason=%q, want the missing-producer episode", result.Event.Reason)
	}

	tracked := stallControl(repo, 2, factory.ReadinessBlocked, factory.BlockerCodePending,
		stallBlocker(factory.BlockerCodePending, repo, 98))
	if _, _, err := db.RecordIssueAssessment(ctx, tracked, time.Now()); err != nil {
		t.Fatalf("seed tracked control: %v", err)
	}
	endpoint := factory.Acceptance{
		ID: decisionForTest("endpoint"), Repository: repo, IssueIndex: "98",
		Approver: 7, NativeRev: 41,
		TitleDigest: stallDigest("title"), ContentDigest: stallDigest("body"),
		TitleOccurrence: "1",
	}
	if err := db.AdmitAcceptanceDecision(ctx, endpoint); err != nil {
		t.Fatalf("admit endpoint decision: %v", err)
	}
	again, err := coord.DetectRepairStalls(ctx, StallDetectDeps{Store: db, Generation: 3})
	if err != nil {
		t.Fatalf("redetect: %v", err)
	}
	byIssue := map[int64]StallComponentDecision{}
	for _, decision := range again.Repositories[0].Decisions {
		byIssue[decision.Issue] = decision
	}
	if got := byIssue[2]; got.Investigate || got.Kind != StallComponentWait {
		t.Fatalf("tracked endpoint=%+v, want a quiet endpoint-delivery wait", got)
	}
	if got := byIssue[1]; !got.Investigate {
		t.Fatalf("headless endpoint=%+v, want the stall to persist", got)
	}
}

// TestMixedPrereqsPreserveMissingProducer proves the producer map
// downgrades only: one issue whose pending blockers mix a missing
// endpoint with a viable one still claims missing-producer stall,
// regardless of blocker order. A later viable endpoint must not
// clear an earlier missing producer.
func TestMixedPrereqsPreserveMissingProducer(t *testing.T) {
	ctx := context.Background()
	db, _ := postgresFixture(t)
	coord := &Coordinator{Store: db}
	const repo = 43
	endpoint := factory.Acceptance{
		ID: decisionForTest("mixed-viable"), Repository: repo, IssueIndex: "96",
		Approver: 7, NativeRev: 41,
		TitleDigest: stallDigest("title"), ContentDigest: stallDigest("body"),
		TitleOccurrence: "1",
	}
	if err := db.AdmitAcceptanceDecision(ctx, endpoint); err != nil {
		t.Fatalf("admit viable endpoint decision: %v", err)
	}
	missingFirst := stallControl(repo, 1, factory.ReadinessBlocked, factory.BlockerCodePending,
		stallBlocker(factory.BlockerCodePending, repo, 97),
		stallBlocker(factory.BlockerCodePending, repo, 96))
	if _, _, err := db.RecordIssueAssessment(ctx, missingFirst, time.Now()); err != nil {
		t.Fatalf("seed missing-first control: %v", err)
	}
	missingLast := stallControl(repo, 2, factory.ReadinessBlocked, factory.BlockerCodePending,
		stallBlocker(factory.BlockerCodePending, repo, 96),
		stallBlocker(factory.BlockerCodePending, repo, 97))
	if _, _, err := db.RecordIssueAssessment(ctx, missingLast, time.Now()); err != nil {
		t.Fatalf("seed missing-last control: %v", err)
	}
	classified, err := coord.DetectRepairStalls(ctx, StallDetectDeps{Store: db, Generation: 3})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(classified.Repositories) != 1 {
		t.Fatalf("repositories=%+v, want the seeded repository", classified.Repositories)
	}
	result := classified.Repositories[0]
	if result.Kind != StallRepositoryStall || result.Event == nil {
		t.Fatalf("repo=%+v, want stall: one missing endpoint must survive a viable sibling", result)
	}
	if result.Event.Reason != factory.StallReasonMissingProducer {
		t.Fatalf("reason=%q, want the missing-producer episode", result.Event.Reason)
	}
	byIssue := map[int64]StallComponentDecision{}
	for _, decision := range result.Decisions {
		byIssue[decision.Issue] = decision
	}
	for _, issue := range []int64{1, 2} {
		if got := byIssue[issue]; !got.Investigate {
			t.Fatalf("mixed issue %d=%+v, want the stall in both orders", issue, got)
		}
	}
}

func decisionForTest(tag string) string {
	sum := sha256.Sum256([]byte("stall-decision\x00" + tag))
	return "d" + hex.EncodeToString(sum[:])[:24]
}
