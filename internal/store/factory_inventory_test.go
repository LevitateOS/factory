package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

func inventoryFixture(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, _ := postgresFixture(t)
	return s, context.Background()
}

func inventoryRun(now time.Time, project string) factory.Run {
	return factory.Run{
		ID: factory.NewID(), ProjectID: project, Role: "coder",
		InputSHA: strings.Repeat("a", 40), Started: now, Deadline: now.Add(time.Hour),
		Image: "sha256:" + strings.Repeat("d", 64), Harness: "codex-0.157.1", Model: "test-model",
	}
}

func recordInventoryRun(t *testing.T, s *Store, ctx context.Context, r factory.Run, settled bool) factory.Run {
	t.Helper()
	if err := s.RecordFactoryRun(ctx, r); err != nil {
		t.Fatal(err)
	}
	if settled {
		r.Outcome, r.Summary, r.Reconciled = factory.Cancelled, "settled history", true
		if err := s.SaveFactoryRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestFactoryUnsettledRunsSkipsSettledHistory(t *testing.T) {
	s, ctx := inventoryFixture(t)
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	live := recordInventoryRun(t, s, ctx, inventoryRun(now, "p123456789012345678901234"), false)
	for i := 0; i < 1000; i++ {
		recordInventoryRun(t, s, ctx, inventoryRun(now, "p999999999999999999999999"), true)
	}
	got, err := s.FactoryUnsettledRuns(ctx, 1000)
	if err != nil || len(got) != 1 || got[0].ID != live.ID {
		t.Fatalf("unsettled listing missed live work: %d runs %v", len(got), err)
	}
	if _, err = s.FactoryUnsettledRuns(ctx, 0); err == nil {
		t.Fatal("unbounded unsettled listing accepted")
	}
	if _, err = s.FactoryUnsettledRuns(ctx, 1001); err == nil {
		t.Fatal("oversized unsettled listing accepted")
	}
}

func TestProjectRunListsScopeToProject(t *testing.T) {
	s, ctx := inventoryFixture(t)
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	home, away := "p123456789012345678901234", "p999999999999999999999999"
	live := recordInventoryRun(t, s, ctx, inventoryRun(now, home), false)
	recordInventoryRun(t, s, ctx, inventoryRun(now, home), true)
	recordInventoryRun(t, s, ctx, inventoryRun(now, away), false)
	history, err := s.ProjectFactoryRuns(ctx, home, 1000)
	if err != nil || len(history) != 2 {
		t.Fatalf("project history wrong: %d runs %v", len(history), err)
	}
	if history[0].Reconciled == history[1].Reconciled {
		t.Fatal("project history lost newest-first order")
	}
	outstanding, err := s.ProjectUnsettledRuns(ctx, home, 1000)
	if err != nil || len(outstanding) != 1 || outstanding[0].ID != live.ID {
		t.Fatalf("project outstanding wrong: %d runs %v", len(outstanding), err)
	}
	if _, err = s.ProjectUnsettledRuns(ctx, "", 10); err == nil {
		t.Fatal("unscoped outstanding listing accepted")
	}
}

func TestSpaceProjectsAfterPages(t *testing.T) {
	s, ctx := inventoryFixture(t)
	if err := s.UpsertUser(ctx, User{ID: 1, Login: "alice", Name: "alice"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"pa00000000000000000000001", "pa00000000000000000000002", "pa00000000000000000000003"} {
		if err := s.CreateProject(ctx, Project{ID: id, Name: "n", RepositoryID: int64(len(id)) + int64(id[len(id)-1]), OwnerID: 1}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.SpaceProjectsAfter(ctx, "", 2)
	if err != nil || len(first) != 2 || first[0].ID != "pa00000000000000000000001" {
		t.Fatalf("first association page wrong: %+v %v", first, err)
	}
	rest, err := s.SpaceProjectsAfter(ctx, first[1].ID, 129)
	if err != nil || len(rest) != 1 || rest[0].ID != "pa00000000000000000000003" {
		t.Fatalf("continued association page wrong: %+v %v", rest, err)
	}
	empty, err := s.SpaceProjectsAfter(ctx, rest[0].ID, 129)
	if err != nil || len(empty) != 0 {
		t.Fatalf("exhausted association page wrong: %+v %v", empty, err)
	}
	if _, err = s.SpaceProjectsAfter(ctx, "", 0); err == nil {
		t.Fatal("unbounded association page accepted")
	}
	if _, err = s.SpaceProjectsAfter(ctx, "", 130); err == nil {
		t.Fatal("oversized association page accepted")
	}
}

func TestDispatchControlsAfterContinues(t *testing.T) {
	s, ctx := inventoryFixture(t)
	now := time.Unix(500, 0)
	for _, issue := range []int64{3, 4, 5} {
		candidate := readinessCandidate()
		candidate.Issue = issue
		candidate.Fingerprint = strings.Repeat(string(rune('a'+issue)), 64)
		if issue == 4 {
			candidate.Acceptance = "d" + strings.Repeat("a", 24)
			candidate.Readiness, candidate.Reason = factory.ReadinessNotAuthorized, factory.BlockerAuthorityMissing
			candidate.Blockers = []factory.Blocker{{Code: factory.BlockerAuthorityMissing, Resolution: factory.BlockerResolution(factory.BlockerAuthorityMissing)}}
		}
		if _, _, err := s.RecordIssueAssessment(ctx, candidate, now.Add(time.Duration(issue)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	for _, code := range []string{factory.BlockerAuthorityMissing, factory.BlockerAcceptanceInvalid} {
		candidate := readinessCandidate()
		candidate.Issue = 6
		candidate.Readiness, candidate.Reason = factory.ReadinessNotAuthorized, code
		candidate.Blockers = []factory.Blocker{{Code: code, Resolution: factory.BlockerResolution(code)}}
		if code == factory.BlockerAcceptanceInvalid {
			candidate.Issue = 7
			candidate.Acceptance = "d" + strings.Repeat("a", 24)
		}
		if _, _, err := s.RecordIssueAssessment(ctx, candidate, now.Add(time.Duration(candidate.Issue)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.DispatchControlsAfter(ctx, 2, 0, 0, 0, false)
	if err != nil || len(first) != 2 || first[0].Issue != 3 || first[1].Issue != 4 {
		t.Fatalf("first dispatch page wrong: %+v %v", first, err)
	}
	last := first[1]
	rest, err := s.DispatchControlsAfter(ctx, 2, last.FirstSeenUnix, last.Repository, last.Issue, true)
	if err != nil || len(rest) != 1 || rest[0].Issue != 5 {
		t.Fatalf("continued dispatch page wrong: %+v %v", rest, err)
	}
	if _, err = s.DispatchControlsAfter(ctx, 0, 0, 0, 0, false); err == nil {
		t.Fatal("unbounded dispatch listing accepted")
	}
}
