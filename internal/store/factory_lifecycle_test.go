package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

func takeoverStore(t *testing.T) *Store {
	t.Helper()
	s, _ := postgresFixture(t)
	if err := s.UpsertUser(context.Background(), User{ID: 1, Login: "alice", Name: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateProject(context.Background(), Project{ID: "p765432109876543210987654", Name: "factory", RepositoryID: 7, OwnerID: 1, Repository: "alice/factory"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindConfiguredFountainLedger(t.Context(), "https://fixture-fountain.example.test"); err != nil {
		t.Fatal(err)
	}
	r := factoryRun(time.Now().UTC())
	r.ID, r.ProjectID = strings.Repeat("a", 32), "p765432109876543210987654"
	if err := s.RecordFactoryRun(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	r.Outcome, r.Reconciled = factory.Cancelled, true
	if err := s.SaveFactoryRun(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	return s
}

func takeoverFixture() factory.TakeoverRecord {
	run := strings.Repeat("a", 32)
	return factory.TakeoverRecord{Run: run, Member: "alice", Project: "p765432109876543210987654", Dest: "/home/alice/factory-takeover/" + run, Copied: "2026-10-01T00:00:00Z"}
}

func TestTakeoverRecordReplays(t *testing.T) {
	s := takeoverStore(t)
	ctx := context.Background()
	want := takeoverFixture()
	stored, err := s.RecordTakeover(ctx, want)
	if err != nil || stored != want {
		t.Fatalf("record: %+v %v", stored, err)
	}
	replayed, err := s.RecordTakeover(ctx, want)
	if err != nil || replayed != want {
		t.Fatalf("replay: %+v %v", replayed, err)
	}
	got, err := s.Takeover(ctx, want.Run, want.Member)
	if err != nil || got != want {
		t.Fatalf("read: %+v %v", got, err)
	}
	other, err := s.RecordTakeover(ctx, factory.TakeoverRecord{Run: want.Run, Member: "bob", Project: want.Project, Dest: "/home/bob/factory-takeover/" + want.Run, Copied: want.Copied})
	if err != nil || other.Member != "bob" {
		t.Fatalf("second member: %+v %v", other, err)
	}
	moved := want
	moved.Copied = "2026-10-01T00:00:01Z"
	if _, err = s.RecordTakeover(ctx, moved); !errors.Is(err, ErrTakeoverConflict) {
		t.Fatalf("changed takeover: %v", err)
	}
	var posts, commands int
	if err = s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM factory_feed_posts WHERE data->'takeover'->>'run'=$1),(SELECT count(*) FROM factory_commands)`, want.Run).Scan(&posts, &commands); err != nil || posts != 2 || commands != 0 {
		t.Fatalf("takeover storage: posts=%d commands=%d err=%v", posts, commands, err)
	}
}

func TestTakeoverUnknownIsNotFound(t *testing.T) {
	s := takeoverStore(t)
	if _, err := s.Takeover(context.Background(), strings.Repeat("c", 32), "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown takeover: %v", err)
	}
}

func TestTakeoverRecordValidates(t *testing.T) {
	s := takeoverStore(t)
	bad := takeoverFixture()
	bad.Dest = "/tmp/takeover"
	if _, err := s.RecordTakeover(context.Background(), bad); err == nil {
		t.Fatal("takeover outside the member home stored")
	}
}
