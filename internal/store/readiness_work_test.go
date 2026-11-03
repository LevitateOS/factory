package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

func TestReadinessWorkCheckpointAndAtomicDeliveryFinish(t *testing.T) {
	s, ctx := readinessTestStore(t)
	now := time.Unix(1700000000, 0)
	root := factory.DependenceRef{Repository: 7, Issue: 3}
	if err := s.EnqueueReadinessWork(ctx, "delivery:delivery-1", "delivery-1", root); err != nil {
		t.Fatal(err)
	}
	if err := s.EnqueueReadinessWork(ctx, "delivery:delivery-1", "delivery-1", root); err != nil {
		t.Fatal("identical enqueue must be idempotent:", err)
	}
	if err := s.EnqueueReadinessWork(ctx, "delivery:delivery-1", "delivery-2", root); !errors.Is(err, ErrCommandConflict) {
		t.Fatal("source identity rewrite must conflict:", err)
	}
	work, err := s.NextReadinessWork(ctx, now)
	if err != nil || work.ID != "delivery:delivery-1" {
		t.Fatal("next source:", work, err)
	}
	work, err = s.BeginReadinessWork(ctx, work.ID, now)
	if err != nil {
		t.Fatal("begin source:", work, err)
	}
	node, err := s.NextReadinessWorkNode(ctx, work.ID)
	if err != nil || node.Ref != root || node.State != "queued" {
		t.Fatal("root node:", node, err)
	}
	if err := s.CheckpointReadinessAssessment(ctx, work, node, false); err != nil {
		t.Fatal("checkpoint root assessment:", err)
	}
	node, err = s.NextReadinessWorkNode(ctx, work.ID)
	if err != nil || node.State != "scanning" {
		t.Fatal("scanning root:", node, err)
	}
	more, err := s.CheckpointReadinessPage(ctx, work, node)
	if err != nil || more {
		t.Fatal("empty dependant scan:", more, err)
	}
	work, err = s.ReadinessWork(ctx, work.ID)
	if err != nil {
		t.Fatal("read source before completion:", err)
	}
	complete, err := s.CompleteReadinessWork(ctx, work, now)
	if err != nil || !complete {
		t.Fatal("complete source:", complete, err)
	}
	seen, err := s.IntakeDeliverySeen(ctx, "delivery-1")
	if err != nil || !seen {
		t.Fatal("completion must atomically acknowledge delivery:", seen, err)
	}
	if _, err = s.ReadinessWork(ctx, work.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("completed source retained:", err)
	}
}

func TestReadinessWorkCapacityGenerationAndDefer(t *testing.T) {
	s, ctx := readinessTestStore(t)
	now := time.Unix(1700000100, 0)
	for i := 0; i < maxReadinessNodeWorkItems; i++ {
		id := fmt.Sprintf("best-effort:%d", i)
		if err := s.EnqueueReadinessWork(ctx, id, "", factory.DependenceRef{Repository: 7, Issue: int64(i + 1)}); err != nil {
			t.Fatal("enqueue:", err)
		}
		if _, err := s.BeginReadinessWork(ctx, id, now); err != nil {
			t.Fatal("allocate bounded root:", err)
		}
	}
	if err := s.EnqueueReadinessWork(ctx, "best-effort:overflow", "", factory.DependenceRef{Repository: 7, Issue: 99}); err != nil {
		t.Fatal("header admission is separately bounded:", err)
	}
	if _, err := s.BeginReadinessWork(ctx, "best-effort:overflow", now); !errors.Is(err, ErrReadinessPending) {
		t.Fatal("fifth node-bearing source must wait:", err)
	}
	if err := s.DeferReadinessWork(ctx, "best-effort:0", now); err != nil {
		t.Fatal("defer source:", err)
	}
	if _, err := s.BeginReadinessWork(ctx, "best-effort:0", now); !errors.Is(err, ErrReadinessPending) {
		t.Fatal("deferred source must respect retry time:", err)
	}
	var resetNodes int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM factory_readiness_nodes WHERE source=$1`, "best-effort:0").Scan(&resetNodes); err != nil || resetNodes != 0 {
		t.Fatal("defer must release all node capacity:", resetNodes, err)
	}
	if _, err := s.BeginReadinessWork(ctx, "best-effort:overflow", now); err != nil {
		t.Fatal("released node capacity:", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE factory_readiness_budget SET generation=generation+1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	work, err := s.BeginReadinessWork(ctx, "best-effort:1", now)
	if err != nil {
		t.Fatal("generation restart:", err)
	}
	var nodes int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM factory_readiness_nodes WHERE source=$1`, work.ID).Scan(&nodes); err != nil || nodes != 1 {
		t.Fatal("generation restart must retain only root:", nodes, err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM factory_readiness_sources`); err != nil {
		t.Fatal(err)
	}
}

func TestReadinessWorkSourceAdmissionBound(t *testing.T) {
	s, ctx := readinessTestStore(t)
	for i := 0; i < maxReadinessSources; i++ {
		if err := s.EnqueueReadinessWork(ctx, fmt.Sprintf("source:%02d", i), "", factory.DependenceRef{Repository: 7, Issue: int64(i + 1)}); err != nil {
			t.Fatal("admit within source cap:", i, err)
		}
	}
	if err := s.EnqueueReadinessWork(ctx, "source:overflow", "", factory.DependenceRef{Repository: 7, Issue: 1000}); !errors.Is(err, ErrReadinessCapacity) {
		t.Fatal("source cap must refuse before allocation:", err)
	}
}

func TestReadinessRedeliveryWakesOnlyMatchingSource(t *testing.T) {
	s, ctx := readinessTestStore(t)
	root := factory.DependenceRef{Repository: 7, Issue: 3}
	const critical = "delivery:wake"
	const bestEffort = "root:wake"
	for _, item := range []struct{ id, delivery string }{{critical, "wake"}, {bestEffort, ""}} {
		if err := s.EnqueueReadinessWork(ctx, item.id, item.delivery, root); err != nil {
			t.Fatal(err)
		}
		if err := s.DeferReadinessWork(ctx, item.id, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	var beforeTurn, beforeAttempts int64
	if err := s.db.QueryRowContext(ctx, `SELECT turn,attempts FROM factory_readiness_sources WHERE id=$1`, critical).Scan(&beforeTurn, &beforeAttempts); err != nil {
		t.Fatal(err)
	}
	if err := s.EnqueueReadinessWork(ctx, critical, "other-delivery", root); !errors.Is(err, ErrCommandConflict) {
		t.Fatal("different delivery must not wake the source:", err)
	}
	if _, err := s.BeginReadinessWork(ctx, critical, time.Now()); !errors.Is(err, ErrReadinessPending) {
		t.Fatal("conflicting delivery changed retry admission:", err)
	}
	if err := s.EnqueueReadinessWork(ctx, critical, "wake", root); err != nil {
		t.Fatal(err)
	}
	var afterTurn, afterAttempts int64
	if err := s.db.QueryRowContext(ctx, `SELECT turn,attempts FROM factory_readiness_sources WHERE id=$1`, critical).Scan(&afterTurn, &afterAttempts); err != nil || beforeTurn != afterTurn || beforeAttempts != afterAttempts {
		t.Fatal("redelivery reset fairness or failure history:", beforeTurn, afterTurn, beforeAttempts, afterAttempts, err)
	}
	work, err := s.BeginReadinessWork(ctx, critical, time.Now())
	if err != nil {
		t.Fatal("matching delivery did not wake:", err)
	}
	node, err := s.NextReadinessWorkNode(ctx, critical)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CheckpointReadinessAssessment(ctx, work, node, false); err != nil {
		t.Fatal(err)
	}
	if err = s.EnqueueReadinessWork(ctx, critical, "wake", root); err != nil {
		t.Fatal(err)
	}
	node, err = s.NextReadinessWorkNode(ctx, critical)
	if err != nil || node.State != "scanning" {
		t.Fatal("redelivery discarded a saved assessment:", node, err)
	}
	if err = s.EnqueueReadinessWork(ctx, bestEffort, "", root); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginReadinessWork(ctx, bestEffort, time.Now()); !errors.Is(err, ErrReadinessPending) {
		t.Fatal("critical wake changed best-effort backoff:", err)
	}
}

func TestReadinessWorkFairRotationAndStaleCheckpoint(t *testing.T) {
	s, ctx := readinessTestStore(t)
	now := time.Unix(1700000200, 0)
	for _, id := range []string{"fair:a", "fair:b", "fair:c"} {
		if err := s.EnqueueReadinessWork(ctx, id, "", factory.DependenceRef{Repository: 7, Issue: int64(len(id))}); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"fair:a", "fair:b", "fair:c", "fair:a"} {
		work, err := s.NextReadinessWork(ctx, now)
		if err != nil || work.ID != want {
			t.Fatalf("round-robin source: got=%q want=%q err=%v", work.ID, want, err)
		}
	}
	work, err := s.BeginReadinessWork(ctx, "fair:a", now)
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.NextReadinessWorkNode(ctx, work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE factory_readiness_budget SET generation=generation+1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckpointReadinessAssessment(ctx, work, node, false); !errors.Is(err, ErrReadinessPending) {
		t.Fatal("stale generation checkpoint must be rejected:", err)
	}
}

func TestReadinessWorkPageCursorDedupAndCapacityRollback(t *testing.T) {
	s, ctx := readinessTestStore(t)
	now := time.Unix(1700000300, 0)
	root := factory.DependenceRef{Repository: 7, Issue: 9}
	decision := factory.Acceptance{
		ID: "d" + fmt.Sprintf("%024d", 1), Repository: 7, IssueIndex: "3", Approver: 5,
		NativeRev: 9, TitleDigest: strings.Repeat("d", 64), TitleOccurrence: factory.OriginalOccurrence, ContentDigest: strings.Repeat("d", 64),
		Prerequisites: []factory.AcceptedPrerequisite{{Occurrence: "21", DependsOn: "8", EndpointRepo: 7, EndpointIssue: 9, Outcome: factory.PrereqResult}},
	}
	if err := s.AdmitAcceptanceDecision(ctx, decision); err != nil {
		t.Fatal(err)
	}
	if err := s.EnqueueReadinessWork(ctx, "page:normal", "", root); err != nil {
		t.Fatal(err)
	}
	work, err := s.BeginReadinessWork(ctx, "page:normal", now)
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.NextReadinessWorkNode(ctx, work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CheckpointReadinessAssessment(ctx, work, node, false); err != nil {
		t.Fatal(err)
	}
	node, err = s.NextReadinessWorkNode(ctx, work.ID)
	if err != nil {
		t.Fatal(err)
	}
	more, err := s.CheckpointReadinessPage(ctx, work, node)
	if err != nil || more {
		t.Fatal("bounded page:", more, err)
	}
	if _, err = s.CheckpointReadinessPage(ctx, work, node); !errors.Is(err, ErrReadinessPending) {
		t.Fatal("replayed stale cursor must not advance twice:", err)
	}
	var storedCursor int64
	if err = s.db.QueryRowContext(ctx, `SELECT cursor_issue FROM factory_readiness_nodes WHERE source=$1 AND repository=7 AND issue=9`, work.ID).Scan(&storedCursor); err != nil || storedCursor != 3 {
		t.Fatal("final page cursor should advance over scanned heads:", storedCursor, err)
	}
	var matches int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM factory_readiness_nodes WHERE source=$1 AND repository=7 AND issue=3`, work.ID).Scan(&matches); err != nil || matches != 1 {
		t.Fatal("match was not enqueued exactly once:", matches, err)
	}

	if err := s.EnqueueReadinessWork(ctx, "page:full", "", root); err != nil {
		t.Fatal(err)
	}
	full, err := s.BeginReadinessWork(ctx, "page:full", now)
	if err != nil {
		t.Fatal(err)
	}
	fullNode, err := s.NextReadinessWorkNode(ctx, full.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CheckpointReadinessAssessment(ctx, full, fullNode, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO factory_readiness_nodes(source,repository,issue) SELECT $1,8,n FROM generate_series(1,$2) n`, full.ID, maxReadinessNodesPerWork-1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CheckpointReadinessPage(ctx, full, fullNode); !errors.Is(err, ErrReadinessCapacity) {
		t.Fatal("full source must reject a page before checkpoint:", err)
	}
	var count, cursor int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*),max(cursor_issue) FILTER (WHERE repository=7 AND issue=9) FROM factory_readiness_nodes WHERE source=$1`, full.ID).Scan(&count, &cursor); err != nil || count != maxReadinessNodesPerWork || cursor != 0 {
		t.Fatal("capacity rejection must roll back nodes and cursor:", count, cursor, err)
	}
}

func TestReadinessWorkFinishPruneFailureRollsBackAckAndCleanup(t *testing.T) {
	s, ctx := readinessTestStore(t)
	now := time.Unix(1700000400, 0)
	if err := s.EnqueueReadinessWork(ctx, "delivery:prune-fail", "delivery-prune-fail", factory.DependenceRef{Repository: 7, Issue: 3}); err != nil {
		t.Fatal(err)
	}
	work, err := s.BeginReadinessWork(ctx, "delivery:prune-fail", now)
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.NextReadinessWorkNode(ctx, work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CheckpointReadinessAssessment(ctx, work, node, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO intake_deliveries(delivery,repository,issue,kind,received_at) SELECT 'old-'||n,7,3,'event',n FROM generate_series(1,$1) n`, MaxIntakeDeliveries+1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `CREATE FUNCTION fail_readiness_prune() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced prune failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `CREATE TRIGGER fail_readiness_prune BEFORE DELETE ON intake_deliveries FOR EACH ROW EXECUTE FUNCTION fail_readiness_prune()`); err != nil {
		t.Fatal(err)
	}
	if complete, err := s.CompleteReadinessWork(ctx, work, now); err == nil || complete {
		t.Fatal("forced prune failure must abort completion:", complete, err)
	}
	seen, err := s.IntakeDeliverySeen(ctx, "delivery-prune-fail")
	if err != nil || seen {
		t.Fatal("failed completion must leave delivery unseen:", seen, err)
	}
	if _, err = s.ReadinessWork(ctx, work.ID); err != nil {
		t.Fatal("failed completion must retain source:", err)
	}
	var count int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM factory_readiness_nodes WHERE source=$1`, work.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed completion must retain node progress:", count, err)
	}
}

func readinessGeneration(t *testing.T, s *Store) int64 {
	t.Helper()
	var generation int64
	if err := s.db.QueryRowContext(context.Background(), `SELECT generation FROM factory_readiness_budget WHERE id=1`).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	return generation
}

func fillReadinessSourceHeaders(t *testing.T, s *Store, prefix string, target int) {
	t.Helper()
	var existing int
	if err := s.db.QueryRowContext(context.Background(), `SELECT count(*) FROM factory_readiness_sources`).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	for i := 0; existing < target; i++ {
		id := fmt.Sprintf("%s:%d", prefix, i)
		if err := s.EnqueueReadinessWork(context.Background(), id, "", factory.DependenceRef{Repository: 9, Issue: int64(i + 1)}); err != nil {
			t.Fatalf("fill readiness headers (%d/%d): %v", existing, target, err)
		}
		existing++
	}
}

func TestAcceptanceHeadEventsAreAtomicAndIdempotent(t *testing.T) {
	s, ctx := readinessTestStore(t)
	first := acceptanceFixture("d0123456789abcdef01234567", "")
	if err := s.AdmitAcceptanceDecision(ctx, first); err != nil {
		t.Fatal(err)
	}
	generation := readinessGeneration(t, s)
	work, err := s.ReadinessWork(ctx, "root:7/3")
	if err != nil || work.Generation != generation || generation != 2 {
		t.Fatal("head advance did not atomically create current root:", work, generation, err)
	}
	if err = s.AdmitAcceptanceDecision(ctx, first); err != nil || readinessGeneration(t, s) != generation {
		t.Fatal("identical head replay changed generation:", err, readinessGeneration(t, s))
	}
	beforeMutation, err := s.BeginReadinessWork(ctx, "root:7/3", time.Now())
	if err != nil {
		t.Fatal("begin root before head change:", err)
	}
	rootNode, err := s.NextReadinessWorkNode(ctx, beforeMutation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CheckpointReadinessAssessment(ctx, beforeMutation, rootNode, false); err != nil {
		t.Fatal(err)
	}
	rootNode, err = s.NextReadinessWorkNode(ctx, beforeMutation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CheckpointReadinessPage(ctx, beforeMutation, rootNode); err != nil {
		t.Fatal("save pre-mutation page cursor:", err)
	}
	second := acceptanceFixture("d123456789abcdef012345678", first.ID)
	second.NativeRev++
	if err = s.AdmitAcceptanceDecision(ctx, second); err != nil {
		t.Fatal(err)
	}
	generation++
	if got := readinessGeneration(t, s); got != generation {
		t.Fatal("new head did not advance generation once:", got)
	}
	if _, err = s.CheckpointReadinessPage(ctx, beforeMutation, rootNode); !errors.Is(err, ErrReadinessPending) {
		t.Fatal("head mutation must fence saved page cursor:", err)
	}
	if _, err = s.BeginReadinessWork(ctx, "root:7/3", time.Now()); err != nil {
		t.Fatal("head mutation should reset active work to root:", err)
	}
	reset, err := s.NextReadinessWorkNode(ctx, "root:7/3")
	if err != nil || reset.State != "queued" || reset.Cursor != (factory.DependenceRef{}) {
		t.Fatal("saved progress was not reset to root:", reset, err)
	}
	if err = s.WithdrawAcceptanceDecision(ctx, 7, 3, second.ID, 5); err != nil {
		t.Fatal(err)
	}
	generation++
	if got := readinessGeneration(t, s); got != generation {
		t.Fatal("withdrawal did not advance generation:", got)
	}
	if err = s.WithdrawAcceptanceDecision(ctx, 7, 3, second.ID, 5); err != nil || readinessGeneration(t, s) != generation {
		t.Fatal("identical withdrawal replay changed generation:", err, readinessGeneration(t, s))
	}
}

func TestAcceptanceRootCapacityRollsBackAdmissionAndWithdrawal(t *testing.T) {
	t.Run("new-head-admission", func(t *testing.T) {
		s, ctx := readinessTestStore(t)
		fillReadinessSourceHeaders(t, s, "capacity:admit", maxReadinessSources)
		before := readinessGeneration(t, s)
		first := acceptanceFixture("d0123456789abcdef01234567", "")
		first.IssueIndex = "4"
		if err := s.AdmitAcceptanceDecision(ctx, first); !errors.Is(err, ErrReadinessCapacity) {
			t.Fatal("full source table must reject new head advance:", err)
		}
		if _, err := s.AcceptanceHead(ctx, 7, 4); !errors.Is(err, ErrNotFound) || readinessGeneration(t, s) != before {
			t.Fatal("refused admission partially committed:", err, readinessGeneration(t, s))
		}
		if _, err := s.AcceptanceDecision(ctx, first.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("refused decision remains recorded:", err)
		}
	})
	t.Run("existing-root-coalesces-at-capacity", func(t *testing.T) {
		s, ctx := readinessTestStore(t)
		first := acceptanceFixture("d0123456789abcdef01234567", "")
		if err := s.AdmitAcceptanceDecision(ctx, first); err != nil {
			t.Fatal(err)
		}
		fillReadinessSourceHeaders(t, s, "capacity:coalesce", maxReadinessSources)
		before := readinessGeneration(t, s)
		second := acceptanceFixture("d123456789abcdef012345678", first.ID)
		second.NativeRev++
		if err := s.AdmitAcceptanceDecision(ctx, second); err != nil {
			t.Fatal("existing root must coalesce at source capacity:", err)
		}
		if got := readinessGeneration(t, s); got != before+1 {
			t.Fatal("head mutation should advance generation once:", got, before)
		}
	})
	t.Run("withdrawal", func(t *testing.T) {
		s, ctx := readinessTestStore(t)
		first := acceptanceFixture("d0123456789abcdef01234567", "")
		if err := s.AdmitAcceptanceDecision(ctx, first); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM factory_readiness_sources WHERE id='root:7/3'`); err != nil {
			t.Fatal(err)
		}
		fillReadinessSourceHeaders(t, s, "capacity:withdraw", maxReadinessSources)
		before := readinessGeneration(t, s)
		if err := s.WithdrawAcceptanceDecision(ctx, 7, 3, first.ID, 5); !errors.Is(err, ErrReadinessCapacity) {
			t.Fatal("full source table must reject withdrawal:", err)
		}
		withdrawn, _, err := s.AcceptanceWithdrawn(ctx, 7, 3, first.ID)
		if err != nil || withdrawn || readinessGeneration(t, s) != before {
			t.Fatal("refused withdrawal partially committed:", withdrawn, readinessGeneration(t, s), err)
		}
	})
}
