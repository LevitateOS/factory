package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/levitateos/sodaos/internal/factory"
)

func TestSubscribedFeedWindowsAcrossRepositories(t *testing.T) {
	ctx := context.Background()
	s, dsn := postgresFixture(t)
	raw := journeySQL(t, dsn)
	acceptance := "d" + strings.Repeat("a", 24)
	// Insertion order differs from repository grouping: the feed must
	// still read newest-first across projects by the global order key.
	insertJourneyAssignment(t, raw, journeyAssignment(strings.Repeat("1", 32), strings.Repeat("1", 32), acceptance, 7, 3, true), "assigned", 1)
	insertJourneyAssignment(t, raw, journeyAssignment(strings.Repeat("2", 32), strings.Repeat("2", 32), acceptance, 9, 5, true), "assigned", 1)
	insertJourneyAssignment(t, raw, journeyAssignment(strings.Repeat("3", 32), strings.Repeat("3", 32), acceptance, 7, 4, true), "assigned", 1)

	page, next, truncated, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7, 9}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 3 || truncated || next == "" {
		t.Fatalf("full window = %d entries truncated=%v next=%q", len(page), truncated, next)
	}
	if page[0].ID != strings.Repeat("3", 32) || page[1].ID != strings.Repeat("2", 32) || page[2].ID != strings.Repeat("1", 32) {
		t.Fatalf("unordered window: %+v", page)
	}

	first, resume, truncated, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7, 9}, Limit: 2})
	if err != nil || len(first) != 2 || !truncated {
		t.Fatalf("cut window = %d entries truncated=%v err=%v", len(first), truncated, err)
	}
	rest, _, truncated, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7, 9}, Limit: 2, Cursor: resume})
	if err != nil || len(rest) != 1 || truncated || rest[0].ID != strings.Repeat("1", 32) {
		t.Fatalf("resumed window = %+v truncated=%v err=%v", rest, truncated, err)
	}

	scoped, _, truncated, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{9}, Limit: 10})
	if err != nil || len(scoped) != 1 || truncated || scoped[0].Repository != 9 {
		t.Fatalf("scoped window = %+v truncated=%v err=%v", scoped, truncated, err)
	}

	posts, next, truncated, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7, 9}, Kinds: []string{factory.FeedKindPost}, Limit: 10})
	if err != nil || len(posts) != 0 || truncated || next != "" {
		t.Fatalf("posts-only window = %+v next=%q truncated=%v err=%v", posts, next, truncated, err)
	}

	if _, _, _, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7}, Limit: 10, Cursor: "v9:1"}); err == nil {
		t.Fatal("versioned cursor admitted")
	}
	if _, _, _, err := s.SubscribedFeed(ctx, factory.FeedQuery{Limit: 10}); err == nil {
		t.Fatal("unscoped query admitted")
	}
}

func TestSubscribedFeedContinuationUnderInsert(t *testing.T) {
	ctx := context.Background()
	s, dsn := postgresFixture(t)
	raw := journeySQL(t, dsn)
	acceptance := "d" + strings.Repeat("a", 24)
	insertJourneyAssignment(t, raw, journeyAssignment(strings.Repeat("1", 32), strings.Repeat("1", 32), acceptance, 7, 3, true), "assigned", 1)
	insertJourneyAssignment(t, raw, journeyAssignment(strings.Repeat("2", 32), strings.Repeat("2", 32), acceptance, 9, 5, true), "assigned", 1)

	first, resume, truncated, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7, 9}, Limit: 1})
	if err != nil || len(first) != 1 || !truncated {
		t.Fatalf("first window = %d entries truncated=%v err=%v", len(first), truncated, err)
	}
	// Concurrent newer inserts take higher order keys and never disturb
	// the in-flight tail: resuming still reads the older rows in order.
	insertJourneyAssignment(t, raw, journeyAssignment(strings.Repeat("3", 32), strings.Repeat("3", 32), acceptance, 7, 4, true), "assigned", 1)
	insertJourneyAssignment(t, raw, journeyAssignment(strings.Repeat("4", 32), strings.Repeat("4", 32), acceptance, 9, 6, true), "assigned", 1)

	rest, _, truncated, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7, 9}, Limit: 2, Cursor: resume})
	if err != nil || len(rest) != 1 || truncated {
		t.Fatalf("resumed window = %d entries truncated=%v err=%v", len(rest), truncated, err)
	}
	if rest[0].ID != strings.Repeat("1", 32) {
		t.Fatalf("resumed tail: %+v", rest)
	}
}

func TestSubscribedFeedSubscriptionScopeAndUnsubscribe(t *testing.T) {
	ctx := context.Background()
	s, dsn := postgresFixture(t)
	raw := journeySQL(t, dsn)
	acceptance := "d" + strings.Repeat("a", 24)
	insertJourneyAssignment(t, raw, journeyAssignment(strings.Repeat("1", 32), strings.Repeat("1", 32), acceptance, 7, 3, true), "assigned", 1)
	insertJourneyAssignment(t, raw, journeyAssignment(strings.Repeat("2", 32), strings.Repeat("2", 32), acceptance, 9, 5, true), "assigned", 1)

	sub := factory.FeedSubscription{Subscriber: "reader-1", Repositories: []int64{7}}
	sub.Revision = 1
	if err := s.SetFeedSubscription(ctx, "reader-1", 0, sub); err != nil {
		t.Fatal(err)
	}
	stored, _, err := s.FeedSubscription(ctx, "reader-1")
	if err != nil {
		t.Fatal(err)
	}
	narrowed := factory.ApplyFeedSubscription(factory.FeedQuery{Repositories: []int64{7, 9}, Limit: 10}, stored)
	page, _, truncated, err := s.SubscribedFeed(ctx, narrowed)
	if err != nil || len(page) != 1 || truncated || page[0].Repository != 7 {
		t.Fatalf("subscribed window = %+v truncated=%v err=%v", page, truncated, err)
	}
	// Unsubscribe: the narrowed scope empties and refuses instead of
	// widening back to the query scope.
	away := factory.FeedSubscription{Subscriber: "reader-1", Repositories: []int64{11}, Revision: 2}
	if err := s.SetFeedSubscription(ctx, "reader-1", 1, away); err != nil {
		t.Fatal(err)
	}
	emptied := factory.ApplyFeedSubscription(factory.FeedQuery{Repositories: []int64{7, 9}, Limit: 10}, away)
	if _, _, _, err := s.SubscribedFeed(ctx, emptied); err == nil {
		t.Fatal("unsubscribed scope read served")
	}
}

func TestSubscribedFeedAdoptedDraftPredecessorStaysCurrent(t *testing.T) {
	ctx := context.Background()
	s, dsn := postgresFixture(t)
	raw := journeySQL(t, dsn)

	intent := "d" + strings.Repeat("2", 24)
	draft := "d" + strings.Repeat("1", 24)
	accepted := "d" + strings.Repeat("3", 24)
	abandoned := "d" + strings.Repeat("4", 24)
	idea := "d" + strings.Repeat("5", 24)
	task := "d" + strings.Repeat("6", 24)
	acceptance := "d" + strings.Repeat("a", 24)

	revision := func(id, predecessor, state string) factory.IntentRevision {
		return factory.IntentRevision{
			ID: id, Intent: intent, Predecessor: predecessor, Owner: 1, Repository: 7,
			Objective: "Build a request form", State: state, MaxIssues: 4, CreatedUnix: 1,
			Ideas: []factory.ProductIdea{{ID: idea, Description: "Request form", Consequence: "Requests are recorded", Decision: "accept", Source: "requested", Provenance: "human", Actor: 1}},
			Tasks: []factory.IntentTask{{Key: task, Objective: "Build request form", Outcomes: []string{"Submit a request"}, IdeaIDs: []string{idea}}},
		}
	}
	// A real draft→accepted succession: the completed draft predecessor is
	// adopted while the live accepted head directly cites it.
	if err := s.SaveIntentRevision(ctx, revision(draft, "", "draft")); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIntentRevision(ctx, revision(accepted, draft, "accepted")); err != nil {
		t.Fatal(err)
	}
	// An abandoned fork off the same draft with no accepted citer: the raw
	// insert leaves the head on the accepted successor, exactly as a
	// superseded lineage reads live.
	fork, err := json.Marshal(revision(abandoned, draft, "draft"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO factory_intent_revisions(id,intent,data) VALUES($1,$2,$3)`,
		abandoned, intent, string(fork)); err != nil {
		t.Fatal(err)
	}

	decomposeRecord := func(id, bound string) factory.Assignment {
		t.Helper()
		a := journeyAssignment(id, id, acceptance, 7, 3, true)
		a.Phase = "decompose"
		a.Result = &factory.AssignmentResult{
			PhaseResult: &factory.PhaseResult{
				Phase: "decompose", Status: "completed", Summary: "decomposition recorded",
				Version: factory.PhaseResultVersion,
				Output: &factory.PhaseOutput{
					Decomposition: &factory.SemanticPlannerOutput{
						Intent: intent, Revision: bound, Operation: factory.IntentPlanningPropose,
					},
				},
			},
		}
		return a
	}
	adoptedID, successorID, forkID := strings.Repeat("7", 32), strings.Repeat("8", 32), strings.Repeat("9", 32)
	insertJourneyAssignment(t, raw, decomposeRecord(adoptedID, draft), factory.AssignmentFinished, 1)
	insertJourneyAssignment(t, raw, decomposeRecord(successorID, accepted), factory.AssignmentFinished, 1)
	insertJourneyAssignment(t, raw, decomposeRecord(forkID, abandoned), factory.AssignmentFinished, 1)

	records := func(page []factory.FeedEntry) map[string]int64 {
		out := map[string]int64{}
		for _, e := range page {
			if e.Kind == factory.FeedKindRecord && e.Activity == "" {
				out[e.ID] = e.Seq
			}
		}
		return out
	}

	all, _, _, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	allRecords := records(all)
	for _, id := range []string{adoptedID, successorID, forkID} {
		if _, ok := allRecords[id]; !ok {
			t.Fatalf("history view drops %s: %+v", id, all)
		}
	}

	current, _, _, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7}, Limit: 10, Current: true})
	if err != nil {
		t.Fatal(err)
	}
	currentRecords := records(current)
	if _, ok := currentRecords[adoptedID]; !ok {
		t.Fatalf("current view drops adopted draft predecessor %s: %+v", adoptedID, current)
	}
	if _, ok := currentRecords[successorID]; !ok {
		t.Fatalf("current view drops head-bound successor %s: %+v", successorID, current)
	}
	if _, ok := currentRecords[forkID]; ok {
		t.Fatalf("current view keeps uncited fork %s: %+v", forkID, current)
	}
	// Current-only is a filter, never a reorder: shared keys match history.
	for id, seq := range currentRecords {
		if allRecords[id] != seq {
			t.Fatalf("record %s moved %d != %d", id, seq, allRecords[id])
		}
	}
}

func TestFeedSubscriptionRestartPersistence(t *testing.T) {
	ctx := context.Background()
	_, dsn := postgresFixture(t)
	first, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	sub := factory.FeedSubscription{Subscriber: "reader-9", Repositories: []int64{7, 9}, Kinds: []string{factory.FeedKindRecord}}
	sub.Revision = 1
	if err := first.SetFeedSubscription(ctx, "reader-9", 0, sub); err != nil {
		t.Fatal(err)
	}
	// A reopened handle reads the same preference back: subscriptions
	// persist across restarts.
	second, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	stored, revision, err := second.FeedSubscription(ctx, "reader-9")
	if err != nil || revision != 1 || len(stored.Repositories) != 2 {
		t.Fatalf("restarted subscription = %+v %d %v", stored, revision, err)
	}
}

func TestSubscribedFeedMilestonesRecordTheirOwnOrder(t *testing.T) {
	ctx := context.Background()
	s, dsn := postgresFixture(t)
	raw := journeySQL(t, dsn)
	acceptance := "d" + strings.Repeat("a", 24)

	older := journeyAssignment(strings.Repeat("1", 32), strings.Repeat("1", 32), acceptance, 7, 3, true)
	insertJourneyAssignment(t, raw, older, factory.AssignmentFinished, 1)
	impl := journeyAssignment(strings.Repeat("2", 32), strings.Repeat("2", 32), acceptance, 7, 3, true)
	impl.CreatedUnix = 1700000100
	insertJourneyAssignment(t, raw, impl, factory.AssignmentFinished, 1)
	newer := journeyAssignment(strings.Repeat("3", 32), strings.Repeat("3", 32), acceptance, 7, 4, true)
	insertJourneyAssignment(t, raw, newer, factory.AssignmentFinished, 1)

	publication := strings.Repeat("9", 32)
	mergedCommit := strings.Repeat("e", 40)
	if _, err := raw.Exec(`INSERT INTO factory_publications(assignment,repository,issue,run,stage,revision,data)
		VALUES($1,7,3,'run','published',1,$2)`, impl.ID, `{"id":"`+publication+`"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO factory_merges(publication,repository,issue,pr,stage,revision,data)
		VALUES($1,7,3,42,'merged',1,$2)`, publication,
		`{"merged_commit":"`+mergedCommit+`","merged_unix":1700002000,"finished_unix":1700002000}`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO factory_completions(repository,generation,event,publication,occurrence,data)
		VALUES(7,1,$1,$2,'occ-1',$3)`, strings.Repeat("e", 32), publication,
		`{"issue":"3","merged_commit":"`+mergedCommit+`"}`); err != nil {
		t.Fatal(err)
	}

	insertJudgment := func(snapshot string, receipt any) {
		t.Helper()
		data, err := json.Marshal(receipt)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(`INSERT INTO factory_priority_judgments(snapshot,max_calls,data) VALUES($1,1,$2)`,
			snapshot, string(data)); err != nil {
			t.Fatal(err)
		}
	}
	deliverySnap := strings.Repeat("ab", 32)
	insertJudgment(deliverySnap, factory.DeliveryReceipt{
		Kind: factory.DeliveryReceiptKind, Fingerprint: deliverySnap, CreatedUnix: 1700001500,
		Input: factory.DeliveryFingerprintInput{PublicationID: publication},
		Decision: factory.DeliveryDecision{Effective: factory.DeliveryEffectiveReady, Answer: factory.DeliveryAnswer{
			Selected: factory.DeliveryReadyToMerge, Probability: 0.9, Confidence: 0.8, Model: "jev",
			Probabilities: map[string]float64{factory.DeliveryReadyToMerge: 0.9, factory.DeliveryReviewAgain: 0.1},
		}},
	})
	prioritySnap := strings.Repeat("cd", 32)
	insertJudgment(prioritySnap, factory.PriorityReceipt{
		Snapshot: prioritySnap, Selected: "cand-a", Outcome: "selected", CreatedUnix: 1700000050,
		Candidates: []factory.PriorityCandidate{
			{ID: "cand-a", Objective: "ship it", Control: factory.IssueControl{Repository: 7, Issue: 3}},
			{ID: "cand-b", Objective: "later", Control: factory.IssueControl{Repository: 7, Issue: 4}},
		},
		Calls: []factory.PriorityCall{{Answer: factory.PriorityAnswer{
			Selected: "cand-a", Confidence: 0.7, Model: "jev",
			Probabilities: map[string]float64{"cand-a": 0.6, "cand-b": 0.4},
		}}},
	})
	// A selection is visible even before dispatch; non-issue judgments
	// do not acquire a repository scope from their option labels.
	insertJudgment(strings.Repeat("ef", 32), factory.PriorityReceipt{
		Snapshot: strings.Repeat("ef", 32), Selected: "cand-x", CreatedUnix: 1700000050,
		Candidates: []factory.PriorityCandidate{{ID: "cand-x", Control: factory.IssueControl{Repository: 7, Issue: 99}}},
	})
	insertJudgment(strings.Repeat("fe", 32), factory.PriorityReceipt{
		Snapshot: strings.Repeat("fe", 32), Selected: "new_issue", CreatedUnix: 1700000050,
		Candidates: []factory.PriorityCandidate{{ID: "new_issue"}},
	})

	page, _, truncated, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(page) != 8 {
		t.Fatalf("milestone window = %d entries truncated=%v: %+v", len(page), truncated, page)
	}
	wantOrder := []struct{ id, activity string }{
		{strings.Repeat("ef", 32), factory.FeedActivityJudgment},
		{prioritySnap, factory.FeedActivityJudgment},
		{deliverySnap, factory.FeedActivityJudgment},
		{strings.Repeat("e", 32), factory.FeedActivityCompleted},
		{publication, factory.FeedActivityMerged},
		{newer.ID, ""},
		{impl.ID, ""},
		{older.ID, ""},
	}
	for i, want := range wantOrder {
		if page[i].ID != want.id || page[i].Activity != want.activity {
			t.Fatalf("entry %d = %s/%q, want %s/%q (window %+v)", i, page[i].ID, page[i].Activity, want.id, want.activity, page)
		}
		if want.activity != "" && i != 0 && page[i].Assignment != impl.ID {
			t.Fatalf("milestone %d assignment = %q", i, page[i].Assignment)
		}
	}
	for i := 1; i < len(page); i++ {
		if page[i-1].Seq <= page[i].Seq {
			t.Fatalf("non-chronological keys: %+v", page)
		}
	}
	if page[0].Assignment != "" {
		t.Fatal("undispatched selection has a fabricated assignment")
	}

	// Page through late milestones without losing or duplicating entries.
	var paged []factory.FeedEntry
	cursor := ""
	for {
		part, next, more, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7}, Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		if len(part) > 2 {
			t.Fatal("page exceeded requested bound")
		}
		paged = append(paged, part...)
		if !more {
			break
		}
		cursor = next
	}
	if len(paged) != len(page) {
		t.Fatalf("paging lost entries: %d != %d", len(paged), len(page))
	}
	for i := range page {
		if paged[i] != page[i] {
			t.Fatalf("page %d changed identity/order", i)
		}
	}

	// Recording another assignment then confirming the older publication
	// must put its merge on top. Rewriting the same milestone is a replay.
	if _, err := raw.Exec("UPDATE factory_merges SET data=data WHERE publication=$1", publication); err != nil {
		t.Fatal(err)
	}
	refresh, _, _, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for i := range page {
		if refresh[i] != page[i] {
			t.Fatal("milestone replay changed order")
		}
	}

	details, err := s.FeedMilestoneDetails(ctx, page)
	if err != nil {
		t.Fatal(err)
	}
	merged := details[publication]
	if merged.Actor != "factory" || merged.Author != "Factory" || merged.At != 1700002000 ||
		merged.Ref != mergedCommit || merged.PR != 42 || merged.Payload == nil ||
		merged.Payload.Merge == nil || merged.Payload.Merge.MergedCommit != mergedCommit {
		t.Fatalf("merge detail: %+v", merged)
	}
	completed := details[strings.Repeat("e", 32)]
	if completed.Actor != "factory" || completed.Status != "issue:completed" || completed.Issue != 3 ||
		completed.Payload == nil || completed.Payload.Completion == nil ||
		completed.Payload.Completion.Publication != publication {
		t.Fatalf("completion detail: %+v", completed)
	}
	delivery := details[deliverySnap]
	if delivery.Actor != "jev" || delivery.Author != "JEV" || delivery.Issue != 3 ||
		delivery.Payload == nil || delivery.Payload.Judgment == nil ||
		delivery.Payload.Judgment.Kind != "delivery" ||
		delivery.Payload.Judgment.Effective != factory.DeliveryEffectiveReady {
		t.Fatalf("delivery judgment detail: %+v", delivery)
	}
	priority := details[prioritySnap]
	if priority.Actor != "jev" || priority.Issue != 3 || priority.Payload == nil ||
		priority.Payload.Judgment == nil || priority.Payload.Judgment.Kind != "priority" ||
		priority.Payload.Judgment.Selected != "cand-a" || priority.Payload.Judgment.CandidatesTotal != 2 ||
		priority.Payload.Judgment.Candidates[0].Objective != "ship it" {
		t.Fatalf("priority judgment detail: %+v", priority)
	}
}
