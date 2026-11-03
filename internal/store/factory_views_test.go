package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

func TestFactoryRunViewRecordReplays(t *testing.T) {
	s, now := factoryFixture(t)
	ctx := context.Background()
	run := factoryRun(now)
	if err := s.RecordFactoryRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	want := factory.RunView{RunID: run.ID, Repository: 7, Issue: 42, Attempt: "attempt-1"}
	stored, created, err := s.RecordFactoryRunView(ctx, want)
	if err != nil || !created || stored != want {
		t.Fatalf("record: %+v created=%v err=%v", stored, created, err)
	}
	replayed, created, err := s.RecordFactoryRunView(ctx, want)
	if err != nil || created || replayed != want {
		t.Fatalf("replay: %+v created=%v err=%v", replayed, created, err)
	}
	got, err := s.FactoryRunView(ctx, run.ID)
	if err != nil || got != want {
		t.Fatalf("read: %+v err=%v", got, err)
	}
	moved := want
	moved.Issue = 43
	if _, _, err = s.RecordFactoryRunView(ctx, moved); !errors.Is(err, ErrRunViewConflict) {
		t.Fatalf("rebound view: %v", err)
	}
	if _, err = s.FactoryRunView(ctx, factory.NewID()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown view: %v", err)
	}
}

func TestFactoryRunViewRequiresItsRun(t *testing.T) {
	s, _ := factoryFixture(t)
	ctx := context.Background()
	view := factory.RunView{RunID: factory.NewID(), Repository: 7, Issue: 42}
	if _, _, err := s.RecordFactoryRunView(ctx, view); err == nil {
		t.Fatal("view recorded without its run")
	}
}

func TestFactoryRunViewsListNewestFirst(t *testing.T) {
	s, now := factoryFixture(t)
	ctx := context.Background()
	var want []factory.RunView
	for i := 0; i < 3; i++ {
		run := factoryRun(now.Add(time.Duration(i) * time.Second))
		if err := s.RecordFactoryRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		view := factory.RunView{RunID: run.ID, Repository: 7, Issue: int64(40 + i)}
		if _, _, err := s.RecordFactoryRunView(ctx, view); err != nil {
			t.Fatal(err)
		}
		want = append([]factory.RunView{view}, want...)
	}
	got, err := s.FactoryRunViews(ctx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != want[0] || got[2] != want[2] {
		t.Fatalf("views: %+v", got)
	}
	if _, err = s.FactoryRunViews(ctx, 0); err == nil {
		t.Fatal("unbounded view list accepted")
	}
}
