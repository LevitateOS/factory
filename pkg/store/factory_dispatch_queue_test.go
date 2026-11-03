package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

func TestReservationTransitions(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	db := dispatchStoreFixture(t)
	a, r, run, view := dispatchTestPacket(t, now)
	if err := recordDispatchTestPacket(t, ctx, db, a, r, run, view); err != nil {
		t.Fatal(err)
	}
	if err := db.ConsumeReservation(ctx, a.ID); err != nil {
		t.Fatalf("consume refused: %v", err)
	}
	if err := db.ConsumeReservation(ctx, a.ID); err == nil {
		t.Fatal("double consume accepted")
	}
	if err := db.ReleaseReservation(ctx, a.ID); err == nil {
		t.Fatal("release after consume accepted")
	}
	held, err := db.HeldReservations(ctx, 10)
	if err != nil || len(held) != 0 {
		t.Fatalf("consumed reservation still held: %+v %v", held, err)
	}
	// Release/rehold is an independent reservation transition. Use a fresh
	// fixture rather than admitting another active attempt in this repository.
	db = dispatchStoreFixture(t)
	b, r2, run2, view2 := dispatchTestPacket(t, now)
	if err := recordDispatchTestPacket(t, ctx, db, b, r2, run2, view2); err != nil {
		t.Fatal(err)
	}
	if err := db.ReleaseReservation(ctx, b.ID); err != nil {
		t.Fatalf("release refused: %v", err)
	}
	rehold := factory.Reservation{AssignmentID: b.ID, Repository: 7, Connection: "conn", State: factory.ReservationHeld, PlannedMinutes: 12, Revision: 2}
	if err := db.ReholdReservation(ctx, rehold); err != nil {
		t.Fatalf("rehold refused: %v", err)
	}
	if err := db.ReholdReservation(ctx, rehold); err == nil {
		t.Fatal("rehold while held accepted")
	}
	got, err := db.Reservation(ctx, b.ID)
	if err != nil || got.State != factory.ReservationHeld || got.PlannedMinutes != 12 {
		t.Fatalf("reheld reservation wrong: %+v %v", got, err)
	}
}

func TestRunUsageFirstWriteWins(t *testing.T) {
	ctx := context.Background()
	db := dispatchStoreFixture(t)
	u := factory.Usage{
		RunID: factory.NewID(), Repository: 7, Connection: "conn", Minutes: 4,
		StartedAt: time.Unix(1699999760, 0).UTC(), EndedAt: time.Unix(1700000000, 0).UTC(),
	}
	if err := db.RecordRunUsage(ctx, u); err != nil {
		t.Fatal(err)
	}
	dup := u
	dup.Minutes = 9
	dup.EndedAt = dup.StartedAt.Add(9 * time.Minute)
	if err := db.RecordRunUsage(ctx, dup); err != nil {
		t.Fatalf("usage replay refused: %v", err)
	}
	other := factory.Usage{
		RunID: factory.NewID(), Repository: 7, Connection: "conn", Minutes: 3,
		StartedAt: time.Unix(1699999821, 0).UTC(), EndedAt: time.Unix(1700000001, 0).UTC(),
	}
	if err := db.RecordRunUsage(ctx, other); err != nil {
		t.Fatal(err)
	}
	got, err := db.RunUsage(ctx, u.RunID)
	if err != nil || got.Minutes != 4 || !got.StartedAt.Equal(u.StartedAt) || !got.EndedAt.Equal(u.EndedAt) {
		t.Fatalf("run usage = %+v %v", got, err)
	}
	total, err := db.UsageTotal(ctx, 7, "conn")
	if err != nil || total != 7 {
		t.Fatalf("usage total = %d, %v", total, err)
	}
	empty, err := db.UsageTotal(ctx, 7, "other")
	if err != nil || empty != 0 {
		t.Fatalf("missing usage total = %d, %v", empty, err)
	}
}

func queuedTestControl(repository, issue int64, readiness, reason string, firstSeen int64) factory.IssueControl {
	return factory.IssueControl{
		Repository: repository, Issue: issue,
		Readiness: readiness, Reason: reason,
		Acceptance:  "d" + strings.Repeat("a", 24),
		Fingerprint: strings.Repeat("1", 64), Authority: strings.Repeat("2", 64),
		FirstSeenUnix: firstSeen, AssessedUnix: firstSeen,
		Blockers: func() []factory.Blocker {
			if readiness == factory.ReadinessQueued {
				return nil
			}
			return []factory.Blocker{{Code: factory.BlockerAcceptanceMissing, Resolution: factory.BlockerResolution(factory.BlockerAcceptanceMissing)}}
		}(),
	}
}

func TestQueuedControlsOldestFirst(t *testing.T) {
	ctx := context.Background()
	db := dispatchStoreFixture(t)
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	young := queuedTestControl(7, 10, factory.ReadinessQueued, factory.ReasonEligible, now.Add(-time.Hour).Unix())
	old := queuedTestControl(8, 2, factory.ReadinessQueued, factory.ReasonEligible, now.Add(-2*time.Hour).Unix())
	tied := queuedTestControl(7, 3, factory.ReadinessQueued, factory.ReasonEligible, now.Add(-2*time.Hour).Unix())
	blocked := queuedTestControl(7, 4, factory.ReadinessBlocked, factory.BlockerAcceptanceMissing, now.Add(-3*time.Hour).Unix())
	for _, c := range []factory.IssueControl{young, old, tied, blocked} {
		if _, _, err := db.RecordIssueAssessment(ctx, c, now); err != nil {
			t.Fatal(err)
		}
	}
	// FirstSeenUnix is assigned by the store clock; set distinct stamps
	// directly so the test proves oldest-first visiting with a
	// deterministic repository/issue tie-break.
	for _, stamp := range []struct {
		repository, issue, seen int64
	}{
		{7, 10, now.Add(-time.Hour).Unix()},
		{8, 2, now.Add(-2 * time.Hour).Unix()},
		{7, 3, now.Add(-2 * time.Hour).Unix()},
	} {
		if _, err := db.db.ExecContext(ctx, `UPDATE issue_controls
			SET data=jsonb_set(data,'{first_seen_unix}',to_jsonb($1::bigint)) WHERE repository=$2 AND issue=$3`,
			stamp.seen, stamp.repository, stamp.issue); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.QueuedControls(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]int64{{7, 3}, {8, 2}, {7, 10}}
	if len(got) != len(want) {
		t.Fatalf("queued count = %d", len(got))
	}
	for i, w := range want {
		if got[i].Repository != w[0] || got[i].Issue != w[1] {
			t.Fatalf("queued[%d] = %d/%d, want %d/%d", i, got[i].Repository, got[i].Issue, w[0], w[1])
		}
	}
}

func TestActiveRunCountsSkipAttributed(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	db := dispatchStoreFixture(t)
	a, r, run, view := dispatchTestPacket(t, now)
	if err := recordDispatchTestPacket(t, ctx, db, a, r, run, view); err != nil {
		t.Fatal(err)
	}
	human := factory.Run{
		ID: factory.NewID(), ProjectID: a.ProjectID, Role: project.RoleCoder, InputSHA: strings.Repeat("c", 40),
		Started: now, Deadline: now.Add(time.Hour),
		Image: "sha256:" + strings.Repeat("b", 64), Harness: "codex-1.2.3", Model: "m",
	}
	if err := db.RecordFactoryRun(ctx, human); err != nil {
		t.Fatal(err)
	}
	total, byProject, err := db.ActiveRunCounts(ctx)
	if err != nil || total != 1 || byProject[a.ProjectID] != 1 {
		t.Fatalf("counts = %d %+v, %v", total, byProject, err)
	}
}
