package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/levitateos/sodaos/internal/factory"
)

func TestActivitySettingsIndependentAtomicRevision(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	defaults, revision, err := s.ActivitySettings(ctx, 7)
	if err != nil || revision != 0 || defaults != factory.DefaultActivitySettings() {
		t.Fatal(defaults, revision, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.SetActivitySettings(ctx, 7, 1, 0, factory.ActivitySettings{PRCreated: true})
		}()
	}
	wg.Wait()
	close(results)
	ok, stale := 0, 0
	for err := range results {
		if err == nil {
			ok++
		} else if errors.Is(err, ErrStaleRevision) {
			stale++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || stale != 1 {
		t.Fatal(ok, stale)
	}
	settings, revision, err := s.ActivitySettings(ctx, 7)
	if err != nil || revision != 1 || settings.IssueCreated || !settings.PRCreated || settings.VerifiedMerge {
		t.Fatal(settings, revision, err)
	}
	var count int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM factory_policies`).Scan(&count); err != nil || count != 0 {
		t.Fatal("activity altered execution policy", count, err)
	}
}

func TestBoundActivityFeedOrdersAndCuts(t *testing.T) {
	items := []factory.ActivityItem{}
	for i := 65; i >= 1; i-- {
		items = append(items, factory.ActivityItem{ID: fmt.Sprintf("item-%02d", i), CreatedUnix: int64(i)})
	}
	items = append(items, factory.ActivityItem{ID: "tie-b", CreatedUnix: 65}, factory.ActivityItem{ID: "tie-a", CreatedUnix: 65})
	bounded, truncated := boundActivityFeed(items)
	if !truncated || len(bounded) != factory.ActivityItemBound {
		t.Fatalf("bounded: %d truncated=%v", len(bounded), truncated)
	}
	if bounded[0].ID != "item-65" || bounded[1].ID != "tie-a" || bounded[2].ID != "tie-b" {
		t.Fatalf("order: %+v %+v %+v", bounded[0], bounded[1], bounded[2])
	}
	if bounded[len(bounded)-1].CreatedUnix != 8 {
		t.Fatalf("tail: %+v", bounded[len(bounded)-1])
	}
	short, truncated := boundActivityFeed([]factory.ActivityItem{{ID: "only", CreatedUnix: 1}})
	if truncated || len(short) != 1 {
		t.Fatalf("short: %+v truncated=%v", short, truncated)
	}
}

func TestBoundActivitySection(t *testing.T) {
	items := make([]factory.ActivityItem, 0, 62)
	for i := 0; i < 61; i++ {
		items = append(items, factory.ActivityItem{ID: fmt.Sprintf("item-%02d", i)})
	}
	bounded, truncated := boundActivitySection(items, 0, 61, false)
	if !truncated || len(bounded) != factory.ActivityItemBound {
		t.Fatalf("bounded: %d truncated=%v", len(bounded), truncated)
	}
	if _, truncated := boundActivitySection(nil, 0, 61, false); !truncated {
		t.Fatal("full page without valid items hides truncation")
	}
	if _, truncated := boundActivitySection(items[:60], 0, 60, false); truncated {
		t.Fatal("exact page reports truncation")
	}
	if _, truncated := boundActivitySection(nil, 0, 0, true); !truncated {
		t.Fatal("prior truncation lost")
	}
}

func TestRepositoryActivityCarriesObservation(t *testing.T) {
	body, err := json.Marshal(factory.RepositoryActivity{Repository: "7", ObservedUnix: 42})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["observed_unix"] != float64(42) {
		t.Fatalf("observation: %v", decoded)
	}
	if _, ok := decoded["truncated"]; ok {
		t.Fatalf("empty truncation rendered: %v", decoded)
	}
}

func TestActivityReportsOnlyCommittedPublishedAndConfirmedClosure(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	save := func(table, id string, v any) {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		query := `INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data) VALUES($1,7,3,'run','published',1,$2)`
		if table == "merge" {
			query = `INSERT INTO factory_merges(publication,repository,issue,pr,stage,revision,data) VALUES($1,7,3,4,'merged',1,$2)`
		}
		if _, err = s.db.ExecContext(ctx, query, id, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	p := factory.Publication{Repository: 7, Issue: 3, Stage: factory.PublicationPublished, Outcome: factory.Succeeded, Reason: factory.PublishReasonLinked, PRID: 104, PRNumber: 4, FinishedUnix: 10, Publish: factory.PublicationOperation{Effect: factory.OpEffectCommitted, Completion: factory.OpCompletionComplete}, PRCreate: factory.PublicationOperation{Effect: factory.OpEffectCommitted, Completion: factory.OpCompletionComplete, PRID: 104, PRNumber: 4}}
	save("pr", "published", p)
	p.PRCreate.Completion = factory.OpCompletionPending
	p.PRID = 105
	p.PRCreate.PRID = 105
	save("pr", "pending", p)
	m := factory.Merge{ID: "merge-complete", Repository: 7, Issue: 3, IssueID: 103, PRID: 104, PRNumber: 4, Stage: factory.MergeMerged, Outcome: factory.Succeeded, Reason: factory.MergeReasonMerged, HeadOID: "exact-head", MergedCommit: "exact-head", MergedUnix: 20, ClosedUnix: 21, FinishedUnix: 22, OriginalClosingOccurrence: "34", OriginalClosedUnix: 21, Operation: factory.MergeOperation{Effect: factory.OpEffectCommitted, Completion: factory.OpCompletionComplete, MergedCommit: "exact-head", PRID: 104, PRNumber: 4, IssueID: 103}}
	save("merge", "complete", m)
	m.ID = "merge-unconfirmed"
	m.OriginalClosedUnix = 0
	save("merge", "unconfirmed", m)
	m.ID = "merge-wrong-head"
	m.OriginalClosedUnix = 21
	m.MergedCommit = "another-head"
	save("merge", "wrong-head", m)
	out, err := s.RepositoryActivity(ctx, 7)
	if err != nil || len(out.Items) != 2 {
		t.Fatal(out, err)
	}
	if out.ObservedUnix <= 0 || out.Truncated {
		t.Fatal("untruncated feed lacks observation", out.ObservedUnix, out.Truncated)
	}
	if out.Items[0].Kind != "verified_merge" || out.Items[0].URL != "" || out.Items[1].Kind != "pr_created" || out.Items[1].URL != "" {
		t.Fatal(out.Items)
	}
	if err = s.SetActivitySettings(ctx, 7, 1, 0, factory.ActivitySettings{}); err != nil {
		t.Fatal(err)
	}
	out, err = s.RepositoryActivity(ctx, 7)
	if err != nil || len(out.Items) != 0 {
		t.Fatal(out, err)
	}
}

func TestActivityCreatedIssuesDeduplicateAndKeepNewestSixty(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO factory_intent_heads(id,owner_id,revision) VALUES('intent',1,'r1')`); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 65; i++ {
		revision := fmt.Sprintf("r%d", i)
		if _, err := s.db.ExecContext(ctx, `INSERT INTO factory_intent_revisions(id,intent,data) VALUES($1,'intent','{"repository":"7"}')`, revision); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO factory_intent_generations(revision,data) VALUES($1,'{"state":"completed"}')`, revision); err != nil {
			t.Fatal(err)
		}
		b := factory.IntentTaskBinding{RevisionID: revision, TaskKey: "task", IssueID: int64(1000 - i), IssueIndex: int64(i), AcceptanceID: "accepted", CreatedUnix: int64(i)}
		raw, _ := json.Marshal(b)
		if _, err := s.db.ExecContext(ctx, `INSERT INTO factory_intent_tasks(revision,task,data) VALUES($1,'task',$2)`, revision, string(raw)); err != nil {
			t.Fatal(err)
		}
		if i == 65 {
			if _, err := s.db.ExecContext(ctx, `INSERT INTO factory_intent_tasks(revision,task,data) VALUES($1,'reused',$2)`, revision, string(raw)); err != nil {
				t.Fatal(err)
			}
		}
	}
	out, err := s.RepositoryActivity(ctx, 7)
	if err != nil || len(out.Items) != 60 {
		t.Fatal(len(out.Items), err)
	}
	if out.ObservedUnix <= 0 || !out.Truncated {
		t.Fatal("capped feed hides its observation", out.ObservedUnix, out.Truncated)
	}
	if out.Items[0].Issue != "65" || out.Items[59].Issue != "6" {
		t.Fatal(out.Items[0], out.Items[59])
	}
	seen := map[string]bool{}
	for _, item := range out.Items {
		if seen[item.ID] {
			t.Fatal("duplicate reused native issue", item)
		}
		seen[item.ID] = true
	}
}
