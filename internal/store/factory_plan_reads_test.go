package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/levitateos/sodaos/internal/factory"
)

func TestPriorityReceiptsForIssueValidation(t *testing.T) {
	empty := &Store{}
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"scope": func() error {
			_, _, err := empty.PriorityReceiptsForIssue(ctx, 0, 42, PriorityComparisonCursor{}, 5)
			return err
		},
		"bound": func() error {
			_, _, err := empty.PriorityReceiptsForIssue(ctx, 7, 42, PriorityComparisonCursor{}, 0)
			return err
		},
		"cursor": func() error {
			_, _, err := empty.PriorityReceiptsForIssue(ctx, 7, 42, PriorityComparisonCursor{BeforeUnix: 100}, 5)
			return err
		},
	} {
		if err := call(); err == nil {
			t.Fatalf("accepted invalid %s", name)
		}
	}
}

func TestPriorityReceiptsForIssuePages(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	save := func(snapshot string, created int64, candidates ...factory.PriorityCandidate) {
		t.Helper()
		raw, err := json.Marshal(factory.PriorityReceipt{Snapshot: snapshot, Candidates: candidates, Outcome: "selected", CreatedUnix: created})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.ExecContext(ctx, `INSERT INTO factory_priority_judgments(snapshot,max_calls,data) VALUES($1,1,$2)`, snapshot, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	named := func(repo, issue int64) factory.PriorityCandidate {
		return factory.PriorityCandidate{ID: "named", Control: factory.IssueControl{Repository: repo, Issue: issue}}
	}
	first, second := strings.Repeat("a", 64), strings.Repeat("b", 64)
	save(first, 100, named(7, 42))
	save(second, 200, named(7, 42), named(8, 9))
	save(strings.Repeat("c", 64), 300, named(8, 9))
	page, hasMore, err := s.PriorityReceiptsForIssue(ctx, 7, 42, PriorityComparisonCursor{}, 1)
	if err != nil || !hasMore || len(page) != 1 || page[0].Snapshot != second {
		t.Fatal(page, hasMore, err)
	}
	page, hasMore, err = s.PriorityReceiptsForIssue(ctx, 7, 42, PriorityComparisonCursor{BeforeUnix: 200, BeforeSnapshot: second}, 5)
	if err != nil || hasMore || len(page) != 1 || page[0].Snapshot != first {
		t.Fatal(page, hasMore, err)
	}
	page, hasMore, err = s.PriorityReceiptsForIssue(ctx, 7, 43, PriorityComparisonCursor{}, 5)
	if err != nil || hasMore || len(page) != 0 {
		t.Fatal(page, hasMore, err)
	}
}
