package control

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

type creationRecoveryFixture struct {
	page factory.CreationRecoveryBatch
}

func (s *creationRecoveryFixture) ReadCreations(_ context.Context, repository, after int64, limit int) (factory.CreationRecoveryBatch, error) {
	if repository != s.page.Repository || after != 0 || limit != 16 {
		return factory.CreationRecoveryBatch{}, errors.New("unexpected creation recovery scope")
	}
	return s.page, nil
}

// PostgreSQL proves that a retained-end observation with incomplete provenance
// cannot consume the recovery cursor, even though known original facts persist.
func TestCreationRecoveryRetainsIncompletePageAcrossStoreReopen(t *testing.T) {
	ctx := context.Background()
	db, dsn := postgresFixture(t)
	if err := db.SaveRepositoryPolicy(ctx, grantPolicy()); err != nil {
		t.Fatal(err)
	}
	source := &creationRecoveryFixture{page: factory.CreationRecoveryBatch{
		Repository: 42, NextPosition: 22, OldestRetainedPosition: 10, ReachedRetainedEnd: true, RetentionLimited: true,
		Events: []factory.OriginalCreationEvent{{Position: 22, Delivery: "original-task-22", Repository: 42, Issue: 3, OriginalIssueID: 101, Creator: 5, CreatorWrite: true, CreatedUnix: 1700000000, TitleDigest: readinessTitleDigest, ContentDigest: readinessContentDigest, Verified: true}},
	}}
	c := NewCoordinator(db, nil, nil)
	c.Creations = source
	ctx, _, cancel := c.readinessPass(ctx)
	defer cancel()
	if err := c.recoverCreations(ctx); !errors.Is(err, ErrCreationProvenanceIncomplete) {
		t.Fatal("incomplete provenance did not hold the scan", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if cursor, err := reopened.CreationRecoveryCursor(ctx, 42); err != nil || cursor != 0 {
		t.Fatal("incomplete page consumed original history", cursor, err)
	}
	work, err := reopened.ReadinessWork(ctx, readinessDeliverySourceID("original-task-22"))
	if err != nil || work.Root.Issue != 3 || work.NativeIssueID != 101 || work.CreatorID != 5 || !work.CreatorWrite || !work.CreationVerified || work.TitleDigest != readinessTitleDigest {
		t.Fatal("original creation facts did not survive reopen", work, err)
	}
	c = NewCoordinator(reopened, nil, nil)
	c.Creations = source
	source.page.ProvenanceComplete = true
	// A conflicting original author cannot be hidden behind a completed page.
	source.page.Events[0].Creator = 6
	if err := c.recoverCreations(ctx); !errors.Is(err, store.ErrCommandConflict) {
		t.Fatal("recovery rewrote original creation authority", err)
	}
	if cursor, err := reopened.CreationRecoveryCursor(ctx, 42); err != nil || cursor != 0 {
		t.Fatal("rejected page advanced history", cursor, err)
	}
	source.page.Events[0].Creator = 5
	if err := c.recoverCreations(ctx); err != nil {
		t.Fatal(err)
	}
	if cursor, err := reopened.CreationRecoveryCursor(ctx, 42); err != nil || cursor != 22 {
		t.Fatal("complete replay did not checkpoint", cursor, err)
	}
}

func TestFindPrereqCycle(t *testing.T) {
	c := coordinatorFixture(t, nil, nil)
	ctx := context.Background()
	chain := func(id, issue string, endpoint int64) {
		decision := readinessDecision(id, 42, issue, 12)
		decision.Prerequisites = []factory.AcceptedPrerequisite{{
			Occurrence: "21", DependsOn: "8", EndpointRepo: 42, EndpointIssue: endpoint,
			Outcome: factory.PrereqResult,
		}}
		mustAdmit(t, c, decision)
	}
	chain("d"+strings.Repeat("1", 24), "3", 4)
	chain("d"+strings.Repeat("2", 24), "4", 5)
	if cycle, exceeded, err := c.findPrereqCycle(ctx, 42, 3); err != nil || exceeded || cycle != "" {
		t.Fatal("acyclic walk reported a cycle:", cycle, exceeded, err)
	}
	self := readinessDecision("d"+strings.Repeat("3", 24), 42, "6", 12)
	self.Prerequisites = []factory.AcceptedPrerequisite{{
		Occurrence: "21", DependsOn: "8", EndpointRepo: 42, EndpointIssue: 6,
		Outcome: factory.PrereqResult,
	}}
	mustAdmit(t, c, self)
	if cycle, _, err := c.findPrereqCycle(ctx, 42, 6); err != nil || cycle != "42/6 -> 42/6" {
		t.Fatal("self cycle wrong:", cycle, err)
	}
}

func TestFindPrereqCycleExceedsBound(t *testing.T) {
	db, dsn := postgresFixture(t)
	c := NewCoordinator(db, nil, nil)
	fixtureDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fixtureDB.Close() }()
	ctx := context.Background()
	for i := int64(0); i <= MaxCycleNodes+1; i++ {
		issue := 1000 + i
		decision := readinessDecision(fmt.Sprintf("d%024x", i+1), 42, fmt.Sprint(issue), 12)
		decision.Prerequisites = []factory.AcceptedPrerequisite{{
			Occurrence: "21", DependsOn: "8", EndpointRepo: 42, EndpointIssue: issue + 1,
			Outcome: factory.PrereqResult,
		}}
		mustAdmit(t, c, decision)
		// This test shapes the graph directly; emitted root headers are not
		// under test and would otherwise hit the independent source-page cap.
		if _, err := fixtureDB.ExecContext(ctx, `DELETE FROM factory_readiness_sources WHERE id=$1`, fmt.Sprintf("root:42/%d", issue)); err != nil {
			t.Fatalf("remove emitted fixture header for issue %d: %v", issue, err)
		}
	}
	if _, exceeded, err := c.findPrereqCycle(ctx, 42, 1000); err != nil || !exceeded {
		t.Fatal("over-bound walk not exceeded:", exceeded, err)
	}
}

func TestAcceptanceHeadMutationBehindSavedCursorRestartsDelivery(t *testing.T) {
	ctx := context.Background()
	db, dsn := postgresFixture(t)
	c := NewCoordinator(db, nil, nil)
	// These in-memory boundaries model native evidence and revision checks;
	// this test makes no provider or installed-native claim.
	source := &fakeEvidenceSource{evidence: map[string]AcceptanceEvidence{}}
	c.AcceptanceReads = source
	c.Readiness = &fakeObserver{revision: 12, idle: true}
	grantFullAuthority(t, c)

	const rootIssue = int64(1000)
	root := readinessDecision("d"+strings.Repeat("f", 24), 42, fmt.Sprint(rootIssue), 12)
	mustAdmit(t, c, root)
	source.evidence[fmt.Sprintf("42/%d", rootIssue)] = readinessEvidence(12, readinessView(fmt.Sprint(rootIssue)), nil, nil)
	for issue := int64(1001); issue <= 1032; issue++ {
		dependent := readinessDecision(fmt.Sprintf("d%024x", issue), 42, fmt.Sprint(issue), 12)
		dependent.Prerequisites = []factory.AcceptedPrerequisite{{
			Occurrence: "21", DependsOn: "8", EndpointRepo: 42, EndpointIssue: rootIssue,
			Outcome: factory.PrereqCode, PrereqAcceptance: root.ID,
		}}
		mustAdmit(t, c, dependent)
		source.evidence[fmt.Sprintf("42/%d", issue)] = readinessEvidence(12, readinessView(fmt.Sprint(issue)), nil,
			[]AcceptanceEdge{{Occurrence: "21", DependsOn: "8", Visible: true}})
	}
	source.evidence[fmt.Sprintf("42/%d", rootIssue)] = readinessEvidence(12, readinessView(fmt.Sprint(rootIssue)), nil, nil)
	// Drain admission-created best-effort roots through the ordinary
	// coordinator path so only the later critical delivery is under test.
	fixtureDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fixtureDB.Close() }()
	for pass := 0; pass < 16; pass++ {
		if _, err := c.drainReadinessWork(ctx, ""); err != nil {
			t.Fatalf("drain fixture admission work: %v", err)
		}
		var pending int
		if err = fixtureDB.QueryRowContext(ctx, `SELECT count(*) FROM factory_readiness_sources`).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if pending == 0 {
			break
		}
		if pass == 15 {
			t.Fatalf("fixture admission work remains after bounded drains: %d", pending)
		}
	}

	hint := readinessHint("delivery-mutable-head", 42, rootIssue)
	if _, _, err := c.ObserveIssueEvent(ctx, hint); !errors.Is(err, ErrReadinessPassPending) {
		t.Fatalf("natural page cap did not retain intake work: %v", err)
	}
	workID := readinessDeliverySourceID(hint.Delivery)
	work, err := c.Store.ReadinessWork(ctx, workID)
	if err != nil {
		t.Fatalf("yielded delivery work was not retained: %v", err)
	}
	if work.Root != (factory.DependenceRef{Repository: 42, Issue: rootIssue}) {
		t.Fatalf("retained work changed root: %+v", work)
	}
	var nodeState string
	var cursorRepository, cursorIssue int64
	if err = fixtureDB.QueryRowContext(ctx, `SELECT state,cursor_repository,cursor_issue FROM factory_readiness_nodes WHERE source=$1 AND repository=42 AND issue=$2`, workID, rootIssue).
		Scan(&nodeState, &cursorRepository, &cursorIssue); err != nil || nodeState != "done" ||
		cursorRepository != 42 || cursorIssue != 1032 {
		t.Fatalf("first dependant page cursor not saved before yield: state=%s cursor=%d/%d err=%v", nodeState, cursorRepository, cursorIssue, err)
	}
	seen, err := c.Store.IntakeDeliverySeen(ctx, hint.Delivery)
	if err != nil || seen {
		t.Fatalf("yielded delivery was acknowledged: seen=%v err=%v", seen, err)
	}

	// This accepted head sorts behind the saved 42/1032 cursor. Admission is the
	// real producer event that advances the readiness generation.
	late := readinessDecision(fmt.Sprintf("d%024x", 1), 42, "1", 12)
	late.Prerequisites = []factory.AcceptedPrerequisite{{
		Occurrence: "21", DependsOn: "8", EndpointRepo: 42, EndpointIssue: rootIssue,
		Outcome: factory.PrereqCode, PrereqAcceptance: root.ID,
	}}
	mustAdmit(t, c, late)
	source.evidence["42/1"] = readinessEvidence(12, readinessView("1"), nil,
		[]AcceptanceEdge{{Occurrence: "21", DependsOn: "8", Visible: true}})
	if retained, readErr := c.Store.ReadinessWork(ctx, workID); readErr != nil || retained.ID != workID {
		t.Fatalf("acceptance mutation discarded retained work: %+v %v", retained, readErr)
	}
	seen, err = c.Store.IntakeDeliverySeen(ctx, hint.Delivery)
	if err != nil || seen {
		t.Fatalf("acceptance mutation acknowledged retained delivery: seen=%v err=%v", seen, err)
	}

	completed := false
	rootReads := source.calls["42/1000"]
	for attempt := 0; attempt < 8; attempt++ {
		_, _, err = c.ObserveIssueEvent(ctx, hint)
		if attempt == 0 {
			if source.calls["42/1000"] <= rootReads {
				t.Fatal("redelivery did not reread the retained root after the head mutation")
			}
			lateControl, controlErr := c.Store.IssueControl(ctx, 42, 1)
			if controlErr != nil || lateControl.Acceptance != late.ID || lateControl.Readiness != factory.ReadinessBlocked ||
				findBlocker(lateControl.Blockers, factory.BlockerCodePending) == nil {
				t.Fatalf("restart did not reassess the new behind-cursor head: %+v %v", lateControl, controlErr)
			}
			var lateNodeState string
			if queryErr := fixtureDB.QueryRowContext(ctx, `SELECT state FROM factory_readiness_nodes WHERE source=$1 AND repository=42 AND issue=1`, workID).
				Scan(&lateNodeState); queryErr != nil || (lateNodeState != "scanning" && lateNodeState != "done") {
				t.Fatalf("new head was not included in the critical delivery restart: node state=%q err=%v", lateNodeState, queryErr)
			}
			seen, seenErr := c.Store.IntakeDeliverySeen(ctx, hint.Delivery)
			if seenErr != nil || seen {
				t.Fatalf("first restart acknowledged delivery before assertions: seen=%v err=%v", seen, seenErr)
			}
		}
		if err == nil {
			completed = true
			break
		}
		if !errors.Is(err, ErrReadinessPassPending) {
			t.Fatalf("redelivery failed while restarting saved traversal: %v", err)
		}
	}
	if !completed {
		t.Fatal("bounded redeliveries did not normally complete retained work")
	}
	if _, err = c.Store.ReadinessWork(ctx, workID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("normally completed readiness work remains: %v", err)
	}
	seen, err = c.Store.IntakeDeliverySeen(ctx, hint.Delivery)
	if err != nil || !seen {
		t.Fatalf("normally completed delivery was not acknowledged: seen=%v err=%v", seen, err)
	}
}
