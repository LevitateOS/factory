package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/levitateos/factory/pkg/factory"
)

func TestIssueMakerOpaqueGrantAndMutationJournal(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := t.Context()
	post := saveTestPost(t, s, 7, "42", "Original request", "opaque-intake")
	if err := s.RecordPostIntake(ctx, post, 0); err != nil {
		t.Fatal(err)
	}
	scope := factory.IssueIntakeScope{Source: post, Actor: 42, Repository: 7, Digest: factory.MCPInputDigest([]byte("Original request")), Expires: 2000000000}
	// Seed the exact pre-cutover JSON shape, without interpreting provider proof
	// in the store or minting a credential. Existing rows require no migration.
	proof := json.RawMessage(fmt.Sprintf(`{"token":"fixture-old-proof","claims":{"installation":"fixture","actor":"42","repository":"7","source":%q,"revision":0,"digest":%q,"expires":2000000000}}`, post, scope.Digest))
	if _, err := s.db.ExecContext(ctx, `UPDATE factory_post_intakes SET data=data || jsonb_build_object('issue_intake_grant',$2::jsonb) WHERE post=$1`, post, string(proof)); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.IssueIntakeGrant(ctx, post, 0)
	if err != nil {
		t.Fatal(err)
	}
	var before, after any
	if json.Unmarshal(proof, &before) != nil || json.Unmarshal(loaded, &after) != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("retained provider proof changed")
	}
	renewed := json.RawMessage(strings.ReplaceAll(string(proof), "fixture-old-proof", "fixture-refreshed-proof"))
	if err = s.SaveIssueIntakeGrant(ctx, post, 0, scope, renewed); err != nil {
		t.Fatal(err)
	}
	wrong := scope
	wrong.Actor = 43
	if err = s.SaveIssueIntakeGrant(ctx, post, 0, wrong, proof); !errors.Is(err, ErrStaleRevision) {
		t.Fatal("another actor replaced proof", err)
	}
	wrong = scope
	wrong.Repository = 8
	if err = s.SaveIssueIntakeGrant(ctx, post, 0, wrong, proof); !errors.Is(err, ErrStaleRevision) {
		t.Fatal("another repository replaced proof", err)
	}
	wrong = scope
	wrong.Digest = factory.MCPInputDigest([]byte("Another source body"))
	if err = s.SaveIssueIntakeGrant(ctx, post, 0, wrong, proof); !errors.Is(err, ErrStaleRevision) {
		t.Fatal("another source body replaced proof", err)
	}
	wrong = scope
	wrong.Revision = 1
	if err = s.SaveIssueIntakeGrant(ctx, post, 0, wrong, proof); err == nil {
		t.Fatal("another revision replaced proof")
	}
	mutation := factory.IssueMakerMutation{Action: "create", Title: "First task", Body: "Exact original body"}
	hash := factory.MCPInputDigest([]byte("stable mutation identity"))
	first, err := s.PrepareIssueMakerMutation(ctx, post, "first", hash, "task", mutation)
	if err != nil || first.Slot != 1 || first.Step != 0 {
		t.Fatal(first, err)
	}
	if generated, pending, err := s.AutomaticIssueOrigin(ctx, 7, 1); err != nil || generated || !pending {
		t.Fatal("unresolved Issue Maker create must hold initial intake", generated, pending, err)
	}
	changed := mutation
	changed.Title = "Recomputed title"
	changed.Body = "Recomputed body"
	retry, err := s.PrepareIssueMakerMutation(ctx, post, "first", hash, "task", changed)
	if err != nil || retry != first {
		t.Fatal("retry rewrote the captured request", retry, err)
	}
	if _, err = s.PrepareIssueMakerMutation(ctx, post, "first", factory.MCPInputDigest([]byte("another identity")), "task", mutation); !errors.Is(err, ErrStaleRevision) {
		t.Fatal("changed identity replaced mutation", err)
	}
	refine := factory.IssueMakerMutation{Action: "refine", Title: "Refined", Body: "Exact refinement", ExpectedTitle: first.Title, ExpectedBodyDigest: factory.MCPInputDigest([]byte(first.Body))}
	second, err := s.PrepareIssueMakerMutation(ctx, post, "refine", factory.MCPInputDigest([]byte("refine identity")), "task", refine)
	if err != nil || second.Slot != first.Slot || second.Step != 1 {
		t.Fatal(second, err)
	}
	var raw []byte
	if err = s.db.QueryRowContext(ctx, `SELECT data->'issue_operations' FROM factory_feed_posts WHERE id=$1`, post).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "fixture-") || strings.Contains(string(raw), `"grant"`) || strings.Contains(string(raw), `"token"`) {
		t.Fatal("bearer proof entered semantic mutation journal")
	}
	// A second Store connection proves retained decoding and idempotency after
	// reopen, independently of in-memory request or provider objects.
	reopened, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	loaded, err = reopened.IssueIntakeGrant(ctx, post, 0)
	if err != nil || !strings.Contains(string(loaded), "fixture-refreshed-proof") {
		t.Fatal("refreshed proof not retained", err)
	}
	retry, err = reopened.PrepareIssueMakerMutation(ctx, post, "refine", factory.MCPInputDigest([]byte("refine identity")), "task", refine)
	if err != nil || retry != second {
		t.Fatal("refinement preconditions changed on reopen", retry, err)
	}
	// Competing callers share one row lock and allocate one slot/step.
	var wg sync.WaitGroup
	results := make(chan factory.IssueMakerMutation, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			m, e := reopened.PrepareIssueMakerMutation(ctx, post, "concurrent", factory.MCPInputDigest([]byte("concurrent identity")), "next", mutation)
			results <- m
			failures <- e
		})
	}
	wg.Wait()
	close(results)
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	for m := range results {
		if m.Slot != 2 || m.Step != 2 {
			t.Fatal("concurrent retry reallocated identity", m)
		}
	}
	for step := 3; step < 32; step++ {
		operation := fmt.Sprintf("step-%d", step)
		m, e := reopened.PrepareIssueMakerMutation(ctx, post, operation, factory.MCPInputDigest([]byte(operation)), "task", refine)
		if e != nil || m.Step != step || m.Slot != 1 {
			t.Fatal("refinement step allocation", m, e)
		}
	}
	if _, err = reopened.PrepareIssueMakerMutation(ctx, post, "overflow", hash, "task", refine); err == nil {
		t.Fatal("33rd operation accepted")
	}
	retry, err = reopened.PrepareIssueMakerMutation(ctx, post, "first", hash, "task", mutation)
	if err != nil || retry != first {
		t.Fatal("full journal rejected retained replay", err)
	}
	other := saveTestPost(t, reopened, 7, "42", "Slot bounds", "slot-bounds")
	for slot := 1; slot < 16; slot++ {
		key := fmt.Sprintf("task-%d", slot)
		m, e := reopened.PrepareIssueMakerMutation(ctx, other, key, factory.MCPInputDigest([]byte(key)), key, mutation)
		if e != nil || m.Slot != slot {
			t.Fatal("issue slot allocation", m, e)
		}
	}
	if _, err = reopened.PrepareIssueMakerMutation(ctx, other, "overflow", hash, "task-16", mutation); err == nil {
		t.Fatal("17th issue slot accepted")
	}
	legacy := saveTestPost(t, reopened, 7, "42", "Retained request", "retained-mutation")
	// This is the previous SDK mutation's persisted wire shape. A newly
	// recomputed precondition must not replace its captured refinement guard.
	legacyJSON := fmt.Sprintf(`{"legacy":{"hash":%q,"mutation":{"action":"refine","slot":1,"step":0,"title":"Captured title","body":"Captured body","expected_title":"Historical title","expected_body_digest":%q}}}`, hash, factory.MCPInputDigest([]byte("Historical body")))
	if _, err = reopened.db.ExecContext(ctx, `UPDATE factory_feed_posts SET data=data || jsonb_build_object('issue_operations',$2::jsonb,'issue_slots','{"request":0,"legacy-task":1}'::jsonb) WHERE id=$1`, legacy, legacyJSON); err != nil {
		t.Fatal(err)
	}
	retained, err := reopened.PrepareIssueMakerMutation(ctx, legacy, "legacy", hash, "legacy-task", refine)
	if err != nil || retained.Title != "Captured title" || retained.Body != "Captured body" || retained.ExpectedTitle != "Historical title" || retained.ExpectedBodyDigest != factory.MCPInputDigest([]byte("Historical body")) || retained.Slot != 1 || retained.Step != 0 {
		t.Fatal("retained SDK request changed", retained, err)
	}
}

func TestFeedRepliesIncludeOtherAuthorsAndPageWithoutCrossingSources(t *testing.T) {
	ctx := context.Background()
	s, _ := postgresFixture(t)
	parent := saveTestPost(t, s, 7, "42", "parent", "parent")
	for i := 0; i < 23; i++ {
		_, _, err := s.SaveFeedPost(ctx, factory.PostRequest{Repository: 7, Body: "reply", ParentID: parent, IdempotencyKey: fmt.Sprint(i)}, "43", "Another person", 1729000001)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := s.SaveFeedPost(ctx, factory.PostRequest{Repository: 8, Body: "other project", ParentID: parent, IdempotencyKey: "foreign"}, "44", "Other", 1729000002)
	if err == nil {
		t.Fatal("cross-repository parent admitted")
	}
	first, next, err := s.FeedReplyIDs(ctx, 7, "post", parent, 0)
	if err != nil || len(first) != 20 || next == 0 {
		t.Fatalf("first page: %d %d %v", len(first), next, err)
	}
	second, next, err := s.FeedReplyIDs(ctx, 7, "post", parent, next)
	if err != nil || len(second) != 3 || next != 0 {
		t.Fatalf("second page: %d %d %v", len(second), next, err)
	}
	grandchild, _, err := s.SaveFeedPost(ctx, factory.PostRequest{Repository: 7, Body: "nested reply", ParentID: first[0], IdempotencyKey: "nested"}, "43", "Another person", 1729000003)
	if err != nil {
		t.Fatal(err)
	}
	children, _, err := s.FeedReplyIDs(ctx, 7, "post", first[0], 0)
	if err != nil || len(children) != 1 || children[0] != grandchild {
		t.Fatalf("direct children: %v %v", children, err)
	}
	rootChildren, _, err := s.FeedReplyIDs(ctx, 7, "post", parent, 0)
	if err != nil || len(rootChildren) != 20 {
		t.Fatalf("root page: %v %v", rootChildren, err)
	}
	for _, child := range rootChildren {
		if child == grandchild {
			t.Fatal("grandchild flattened under root")
		}
	}
	seen := map[string]bool{}
	for _, id := range append(first, second...) {
		if seen[id] {
			t.Fatal("reply duplicated across pages")
		}
		seen[id] = true
	}
	if _, err := s.DeleteFeedPost(ctx, second[0], "43", 0); err != nil {
		t.Fatal(err)
	}
	remaining, _, err := s.FeedReplyIDs(ctx, 7, "post", parent, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range remaining {
		if id == second[0] {
			t.Fatal("deleted reply exposed")
		}
	}
}

func saveTestPost(t *testing.T, s *Store, repository int64, author, body, key string) string {
	t.Helper()
	id, revision, err := s.SaveFeedPost(context.Background(), factory.PostRequest{
		Repository: repository, Body: body, IdempotencyKey: key,
	}, author, author+"-name", 1729000000)
	if err != nil {
		t.Fatal(err)
	}
	if revision != 0 {
		t.Fatalf("fresh comment revision = %d", revision)
	}
	return id
}

func TestFeedbackQueueDoesNotReplayHistoricalIssueMappings(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	id := saveTestPost(t, s, 7, "42", "existing work", "old-feedback")
	if err := s.RecordPostIntake(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	pipe, err := s.PostIntake(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	next := pipe
	next.Judgment = factory.PostJudgmentApplied
	next.Route = factory.PostRouteExistingWork
	next.Issue = 55
	if err = s.AdvancePostIntake(ctx, pipe, next); err != nil {
		t.Fatal(err)
	}
	queued, err := s.PendingPostIntakes(ctx, 4)
	if err != nil || len(queued) != 1 {
		t.Fatalf("newly assessed feedback absent: %+v %v", queued, err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE factory_post_intakes SET data=data-'feedback_revision' WHERE post=$1`, id); err != nil {
		t.Fatal(err)
	}
	queued, err = s.PendingPostIntakes(ctx, 4)
	if err != nil || len(queued) != 0 {
		t.Fatalf("historical mapping replayed: %+v %v", queued, err)
	}
}

func TestFeedPostSaveReplaysIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	s, _ := postgresFixture(t)
	first := saveTestPost(t, s, 7, "42", "please fix the login redirect", "key-1")
	again, revision, err := s.SaveFeedPost(ctx, factory.PostRequest{
		Repository: 7, Body: "a different body", IdempotencyKey: "key-1",
	}, "42", "ada", 1729000060)
	if err != nil {
		t.Fatal(err)
	}
	if again != first || revision != 0 {
		t.Fatalf("replay = %q rev %d, want %q rev 0", again, revision, first)
	}
	detail, err := s.FeedPost(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Body != "please fix the login redirect" || detail.AuthorName != "42-name" || detail.At != 1729000000 {
		t.Fatalf("replay rewrote the original: %+v", detail)
	}
	// A different key from the same author is a new comment.
	other := saveTestPost(t, s, 7, "42", "another thought", "key-2")
	if other == first {
		t.Fatal("distinct key collided")
	}
}

func TestFeedPostEditDeleteRevoke(t *testing.T) {
	ctx := context.Background()
	s, _ := postgresFixture(t)
	id := saveTestPost(t, s, 7, "42", "original", "key-1")
	if err := s.RecordPostIntake(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	revision, err := s.EditFeedPost(ctx, id, "42", "edited text", 0, 1729000100)
	if err != nil || revision != 1 {
		t.Fatalf("edit = %d %v", revision, err)
	}
	detail, err := s.FeedPost(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Body != "edited text" || !detail.Record.Edited || detail.Record.Revision != 1 {
		t.Fatalf("edited detail: %+v", detail)
	}
	pipe, err := s.PostIntake(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if pipe.Revision != 1 {
		t.Fatalf("pipeline not rebound: %+v", pipe)
	}
	// Stale revision refuses.
	if _, err := s.EditFeedPost(ctx, id, "42", "stale", 0, 1729000200); err == nil {
		t.Fatal("stale edit admitted")
	}
	// Foreign author refuses.
	if _, err := s.EditFeedPost(ctx, id, "43", "hijack", 1, 1729000200); err == nil {
		t.Fatal("foreign edit admitted")
	}
	// Delete tombstones and blocks the unsettled effect.
	revision, err = s.DeleteFeedPost(ctx, id, "42", 1)
	if err != nil || revision != 2 {
		t.Fatalf("delete = %d %v", revision, err)
	}
	pipe, err = s.PostIntake(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if pipe.Effect != factory.PostEffectBlocked {
		t.Fatalf("deleted effect = %q", pipe.Effect)
	}
	// Deleted sources refuse edits but still resolve.
	if _, err := s.EditFeedPost(ctx, id, "42", "resurrect", 2, 1729000300); err == nil {
		t.Fatal("edit after delete admitted")
	}
	if _, err := s.FeedPost(ctx, id); err != nil {
		t.Fatal(err)
	}
	// Revoke blocks the unsettled effect on a second comment.
	second := saveTestPost(t, s, 7, "42", "second", "key-2")
	if err := s.RecordPostIntake(ctx, second, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkFeedPostRevoked(ctx, second); err != nil {
		t.Fatal(err)
	}
	pipe, err = s.PostIntake(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if pipe.Effect != factory.PostEffectBlocked {
		t.Fatalf("revoked effect = %q", pipe.Effect)
	}
}

func TestPostPipelineAdvanceIsCAS(t *testing.T) {
	ctx := context.Background()
	s, _ := postgresFixture(t)
	id := saveTestPost(t, s, 7, "42", "route me", "key-1")
	if err := s.RecordPostIntake(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	// Replaying the intake recovers the row.
	if err := s.RecordPostIntake(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	pipe, err := s.PostIntake(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	next := pipe
	next.Judgment = factory.PostJudgmentApplied
	next.Route = factory.PostRouteDiscussion
	if err := s.AdvancePostIntake(ctx, pipe, next); err != nil {
		t.Fatal(err)
	}
	// The pre-advance snapshot is now stale and refuses.
	stale := next
	stale.Route = factory.PostRouteClarify
	if err := s.AdvancePostIntake(ctx, pipe, stale); err == nil {
		t.Fatal("stale advance admitted")
	}
	// Attempts exhaust at the bound.
	for i := 0; i < factory.PostAttemptLimit; i++ {
		if err := s.NotePostAttempt(ctx, id); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if err := s.NotePostAttempt(ctx, id); err == nil {
		t.Fatal("over-limit attempt admitted")
	}
}

func TestPostDraftBindsSourceRevision(t *testing.T) {
	ctx := context.Background()
	s, _ := postgresFixture(t)
	id := saveTestPost(t, s, 7, "42", "needs work", "key-1")
	if err := s.RecordPostIntake(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	draft := factory.IssueDraft{SourcePost: id, Title: "Fix it", Body: "Change the code.", Objective: "Works.", Acceptance: "Tests pass.", Metadata: `{"work":"soda"}`}
	if err := s.RecordPostDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PostDraft(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	// After an edit the retained draft no longer binds.
	if _, err := s.EditFeedPost(ctx, id, "42", "needs work, updated", 0, 1729000100); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PostDraft(ctx, id, 1); err == nil {
		t.Fatal("stale-revision draft served")
	}
	// Malformed drafts refuse before any native effect.
	broken := draft
	broken.Acceptance = ""
	if err := s.RecordPostDraft(ctx, broken); err == nil {
		t.Fatal("malformed draft retained")
	}
}

func TestSubscribedFeedMergesPostsNewestFirst(t *testing.T) {
	ctx := context.Background()
	s, dsn := postgresFixture(t)
	raw := journeySQL(t, dsn)
	acceptance := "d" + strings.Repeat("a", 24)
	insertJourneyAssignment(t, raw, journeyAssignment(strings.Repeat("1", 32), strings.Repeat("1", 32), acceptance, 7, 3, true), "assigned", 1)
	first := saveTestPost(t, s, 7, "42", "first comment", "key-1")
	insertJourneyAssignment(t, raw, journeyAssignment(strings.Repeat("2", 32), strings.Repeat("2", 32), acceptance, 7, 4, true), "assigned", 1)
	second := saveTestPost(t, s, 9, "43", "second comment", "key-9")

	page, _, truncated, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7, 9}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(page) != 4 {
		t.Fatalf("combined window = %+v truncated=%v", page, truncated)
	}
	// Insertion order was record, post, record, post: newest-first must
	// interleave them exactly in reverse.
	if page[0].ID != second || page[0].Kind != factory.FeedKindPost ||
		page[1].ID != strings.Repeat("2", 32) || page[1].Kind != factory.FeedKindRecord ||
		page[2].ID != first || page[2].Kind != factory.FeedKindPost ||
		page[3].ID != strings.Repeat("1", 32) || page[3].Kind != factory.FeedKindRecord {
		t.Fatalf("unordered combined window: %+v", page)
	}
	// Kind filters still scope.
	posts, _, _, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7, 9}, Kinds: []string{factory.FeedKindPost}, Limit: 10})
	if err != nil || len(posts) != 2 {
		t.Fatalf("posts-only window = %+v %v", posts, err)
	}
	records, _, _, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7, 9}, Kinds: []string{factory.FeedKindRecord}, Limit: 10})
	if err != nil || len(records) != 2 {
		t.Fatalf("records-only window = %+v %v", records, err)
	}
	// Cursor paging crosses the kind boundary.
	head, resume, truncated, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7, 9}, Limit: 3})
	if err != nil || len(head) != 3 || !truncated {
		t.Fatalf("cut window = %+v truncated=%v %v", head, truncated, err)
	}
	tail, _, truncated, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7, 9}, Limit: 3, Cursor: resume})
	if err != nil || len(tail) != 1 || truncated || tail[0].ID != strings.Repeat("1", 32) {
		t.Fatalf("resumed window = %+v truncated=%v %v", tail, truncated, err)
	}
	// Deleted and revoked sources leave the feed but still resolve.
	if _, err := s.DeleteFeedPost(ctx, first, "42", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkFeedPostRevoked(ctx, second); err != nil {
		t.Fatal(err)
	}
	remaining, _, _, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7, 9}, Kinds: []string{factory.FeedKindPost}, Limit: 10})
	if err != nil || len(remaining) != 0 {
		t.Fatalf("closed posts surface: %+v %v", remaining, err)
	}
}

// insertTestAssignmentRow seeds one minimal finished assignment row
// behind a feed record / work-candidate read.
func insertTestAssignmentRow(t *testing.T, s *Store, ctx context.Context, repository, issue int64) string {
	t.Helper()
	id := factory.NewID()
	data, err := json.Marshal(factory.Assignment{
		ID: id, Repository: repository, Issue: issue,
		Run: factory.NewID(), Stage: factory.AssignmentFinished,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO factory_assignments(id,repository,issue,run,stage,revision,data) VALUES($1,$2,$3,$4,'finished',0,$5)`,
		id, repository, issue, factory.NewID(), string(data)); err != nil {
		t.Fatalf("seed assignment: %v", err)
	}
	return id
}

// TestPostSourceResolutionAndRoundTrip proves the authorized
// source snapshot: records and posts resolve inside their repository,
// cross-repository sources refuse as not found, and the persisted
// snapshot reads back on the saved comment.
func TestPostSourceResolutionAndRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, _ := postgresFixture(t)
	recordID := insertTestAssignmentRow(t, s, ctx, 7, 55)

	source, err := s.ResolvePostSource(ctx, 7, factory.PostSourceRecord, recordID)
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != factory.PostSourceRecord || source.ID != recordID || source.Issue != 55 {
		t.Fatalf("record source: %+v", source)
	}
	if _, err := s.ResolvePostSource(ctx, 8, factory.PostSourceRecord, recordID); err != ErrNotFound {
		t.Fatalf("cross-repository record err = %v, want not found", err)
	}
	if _, err := s.ResolvePostSource(ctx, 7, factory.PostSourceRecord, factory.NewID()); err != ErrNotFound {
		t.Fatalf("unknown record err = %v, want not found", err)
	}

	id, _, err := s.SaveFeedPostWithSource(ctx, factory.PostRequest{
		Repository: 7, Body: "more on the filter", SourceKind: factory.PostSourceRecord,
		SourceID: recordID, IdempotencyKey: "key-source",
	}, "42", "ada", 1729000000, source)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := s.FeedPost(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Source == nil || detail.Source.ID != recordID || detail.Source.Issue != 55 {
		t.Fatalf("source round trip lost: %+v", detail.Source)
	}
	// A mismatched snapshot refuses before any write.
	mismatch := *source
	mismatch.ID = factory.NewID()
	if _, _, err := s.SaveFeedPostWithSource(ctx, factory.PostRequest{
		Repository: 7, Body: "x", SourceKind: factory.PostSourceRecord,
		SourceID: recordID, IdempotencyKey: "key-mismatch",
	}, "42", "ada", 1729000000, &mismatch); err == nil {
		t.Fatal("mismatched source snapshot admitted")
	}
	// Post sources resolve to the stored post in the same repository.
	post, err := s.ResolvePostSource(ctx, 7, factory.PostSourcePost, id)
	if err != nil {
		t.Fatal(err)
	}
	if post.Summary != "more on the filter" {
		t.Fatalf("post source summary: %+v", post)
	}
}

// TestPendingPostPipelinesAndCandidates proves the service work
// list and the existing-work candidate read: pending requests list
// until they settle, and recent worked issues list newest-first per
// issue inside their repository only.
func TestPendingPostPipelinesAndCandidates(t *testing.T) {
	ctx := context.Background()
	s, _ := postgresFixture(t)
	id := saveTestPost(t, s, 7, "42", "route me", "key-1")
	if err := s.RecordPostIntake(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingPostIntakes(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].SourcePost != id {
		t.Fatalf("pending list: %+v", pending)
	}
	pipe, err := s.PostIntake(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	next := pipe
	next.Judgment = factory.PostJudgmentApplied
	next.Route = factory.PostRouteDiscussion
	if err := s.AdvancePostIntake(ctx, pipe, next); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PendingPostIntakes(ctx, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("settled discussion still listed: %+v", pending)
	}
	if _, err := s.PendingPostIntakes(ctx, 0); err == nil {
		t.Fatal("unbounded pending listing admitted")
	}
	exhausted := saveTestPost(t, s, 7, "42", "failed authoring", "exhausted")
	if err := s.RecordPostIntake(ctx, exhausted, 0); err != nil {
		t.Fatal(err)
	}
	exhaustedPipe, err := s.PostIntake(ctx, exhausted)
	if err != nil {
		t.Fatal(err)
	}
	authoring := exhaustedPipe
	authoring.Judgment, authoring.Route = factory.PostJudgmentApplied, factory.PostRouteNewIssue
	if err := s.AdvancePostIntake(ctx, exhaustedPipe, authoring); err != nil {
		t.Fatal(err)
	}
	for range factory.PostAttemptLimit {
		if err := s.NotePostAttempt(ctx, exhausted); err != nil {
			t.Fatal(err)
		}
	}
	details, err := s.FeedPostDetails(ctx, []string{exhausted})
	if err != nil || details[exhausted].Status != "draft:failed" {
		t.Fatalf("feed header hid exhausted authoring: %+v, %v", details, err)
	}
	ready := saveTestPost(t, s, 7, "42", "next request", "next")
	if err := s.RecordPostIntake(ctx, ready, 0); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PendingPostIntakes(ctx, 1)
	if err != nil || len(pending) != 1 || pending[0].SourcePost != ready {
		t.Fatalf("exhausted request consumed bounded service slot: %+v, %v", pending, err)
	}

	insertTestAssignmentRow(t, s, ctx, 7, 55)
	insertTestAssignmentRow(t, s, ctx, 7, 9)
	insertTestAssignmentRow(t, s, ctx, 7, 55)
	insertTestAssignmentRow(t, s, ctx, 8, 77)
	candidates, err := s.RecentPostWorkCandidates(ctx, 7, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].Issue != 55 || candidates[1].Issue != 9 {
		t.Fatalf("candidates: %+v", candidates)
	}
	for _, candidate := range candidates {
		if candidate.Origin != factory.PostCandidateRecent {
			t.Fatalf("candidate origin: %+v", candidate)
		}
	}
}

func TestPostStatusShowsExhaustionWithoutDiscardingConfirmedIssue(t *testing.T) {
	p := factory.PostIntake{Judgment: factory.PostJudgmentApplied, Route: factory.PostRouteNewIssue, Draft: factory.PostDraftPending, Effect: factory.PostEffectPending, Attempts: factory.PostAttemptLimit}
	if got := PostStatus(&p); got != "draft:failed" {
		t.Fatalf("exhausted authoring shown as %s", got)
	}
	p.Draft = factory.PostDraftApplied
	if got := PostStatus(&p); got != "effect:unconfirmed" {
		t.Fatalf("unknown native outcome shown as %s", got)
	}
	p.Effect, p.Issue = factory.PostEffectVerified, 9
	if got := PostStatus(&p); got != "issue:9" {
		t.Fatalf("confirmed issue lost: %s", got)
	}
	p.Judgment = factory.PostJudgmentPending
	if got := PostStatus(&p); got != "judgment:failed" {
		t.Fatalf("exhausted judgment shown as %s", got)
	}
}

func TestRetryFeedPostPreservesPostAndReconcilesUncertainEffect(t *testing.T) {
	ctx := context.Background()
	s, _ := postgresFixture(t)
	id := saveTestPost(t, s, 7, "42", "Original request", "retry-1")
	if err := s.RecordPostIntake(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	for range factory.PostAttemptLimit {
		if err := s.NotePostAttempt(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RetryFeedPost(ctx, id, "43", 0, "Other account", 1729000020); err == nil {
		t.Fatal("another author changed request")
	}
	if err := s.RetryFeedPost(ctx, id, "42", 0, "Clarified request", 1729000020); err != nil {
		t.Fatal(err)
	}
	detail, err := s.FeedPost(ctx, id)
	if err != nil || detail.Body != "Clarified request" || detail.Record.Revision != 1 {
		t.Fatalf("post lost: %+v %v", detail, err)
	}
	pipe, err := s.PostIntake(ctx, id)
	if err != nil || pipe.Attempts != 0 || pipe.Judgment != "pending" || pipe.Revision != 1 {
		t.Fatalf("retry state: %+v %v", pipe, err)
	}
	next := pipe
	next.Judgment = factory.PostJudgmentApplied
	next.Route = factory.PostRouteNewIssue
	next.Draft = factory.PostDraftApplied
	next.Effect = factory.PostEffectFailed
	if err := s.AdvancePostIntake(ctx, pipe, next); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryFeedPost(ctx, id, "42", 1, "Clarified request", 1729000030); err != nil {
		t.Fatal(err)
	}
	resumed, _ := s.PostIntake(ctx, id)
	if resumed.Revision != 1 || resumed.Draft != factory.PostDraftApplied || resumed.Effect != factory.PostEffectFailed {
		t.Fatalf("uncertain native effect was replaced: %+v", resumed)
	}
	done := resumed
	done.Effect = factory.PostEffectApplied
	done.Issue = 5
	if err := s.AdvancePostIntake(ctx, resumed, done); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryFeedPost(ctx, id, "42", 1, "Duplicate", 1729000040); err == nil {
		t.Fatal("confirmed issue was duplicated")
	}
	checkPostRetryReceipts(t, s)
}

// Exercise the ledger with real post/intake rows and simultaneous transactions,
// including the unchanged-revision branch that a head read cannot disambiguate.
func checkPostRetryReceipts(t *testing.T, s *Store) {
	t.Helper()
	ctx := t.Context()
	newRequest := func(key string) factory.FeedPostRetryCommand {
		t.Helper()
		post := saveTestPost(t, s, 7, "42", "Before retry", key)
		if err := s.RecordPostIntake(ctx, post, 0); err != nil {
			t.Fatal(err)
		}
		return factory.FeedPostRetryCommand{ID: factory.NewID(), Post: post, Repository: 7, Author: "42", Body: "After retry"}
	}
	snapshot := func(post string) string {
		t.Helper()
		var state string
		if err := s.db.QueryRowContext(ctx, `SELECT jsonb_build_array(p.revision,p.body,p.data,i.revision,i.judgment,i.route,i.draft,i.effect,i.issue,i.attempts,i.data)::text FROM factory_feed_posts p JOIN factory_post_intakes i ON i.post=p.id WHERE p.id=$1`, post).Scan(&state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	request := newRequest("receipt-main")
	// Maximum valid Unicode/control-character writing exceeds the ledger's
	// inline payload bound; the exact body digest binds it without truncation.
	request.Body = strings.Repeat("🫧\n", factory.PostBodyLimit/2)
	receipt, replayed, err := s.RetryIssueMakerFeedPostCommand(ctx, request, 1729000050)
	if err != nil || replayed || receipt.Revision != 1 || receipt.Validate(request) != nil {
		t.Fatal("first atomic receipt", receipt, replayed, err)
	}
	stored, err := s.FactoryCommand(ctx, request.ID)
	if err != nil || stored.Finished == "" || len(stored.Payload) > 8192 {
		t.Fatal("finished command missing", err)
	}
	if _, err := s.EditFeedPost(ctx, request.Post, "42", "Newer writing", 1, 1729000051); err != nil {
		t.Fatal(err)
	}
	before := snapshot(request.Post)
	again, replayed, err := s.RetryIssueMakerFeedPostCommand(ctx, request, 1729000052)
	if err != nil || !replayed || again != receipt || snapshot(request.Post) != before {
		t.Fatal("historical replay changed the current head", again, replayed, err)
	}
	for _, change := range []func(*factory.FeedPostRetryCommand){
		func(c *factory.FeedPostRetryCommand) { c.Body = "changed" },
		func(c *factory.FeedPostRetryCommand) { c.ExpectedRevision++ },
		func(c *factory.FeedPostRetryCommand) { c.Post = factory.NewID() },
		func(c *factory.FeedPostRetryCommand) { c.Repository++ },
		func(c *factory.FeedPostRetryCommand) { c.Author = "43" },
	} {
		wrong := request
		change(&wrong)
		if _, _, err := s.RetryIssueMakerFeedPostCommand(ctx, wrong, 1729000053); !errors.Is(err, ErrCommandConflict) {
			t.Fatal("changed command identity was reused", err)
		}
	}
	if snapshot(request.Post) != before {
		t.Fatal("refused command changed rows")
	}
	// New refused commands must leave no unfinished journal entry, regardless
	// of whether validation, scope, closed state or CAS refused the transition.
	for _, mode := range []string{"revision", "author", "repository", "closed", "body", "foreign-command"} {
		refused := newRequest("receipt-refuse-" + mode)
		switch mode {
		case "revision":
			refused.ExpectedRevision = 1
		case "author":
			refused.Author = "43"
		case "repository":
			refused.Repository = 8
		case "closed":
			if _, err := s.db.ExecContext(ctx, `UPDATE factory_feed_posts SET data=data || '{"deleted":true}'::jsonb WHERE id=$1`, refused.Post); err != nil {
				t.Fatal(err)
			}
		case "body":
			refused.Body = "   "
		case "foreign-command":
			command, err := refused.Command()
			if err != nil {
				t.Fatal(err)
			}
			command.Type = factory.CommandRetry
			command.Digest = factory.SettingsDigest(command.Type, command.Target, command.Payload)
			if _, err := s.db.ExecContext(ctx, `INSERT INTO factory_commands(id,type,target,principal,digest,payload,created) VALUES($1,$2,$3,$4,$5,$6,'2024-10-15T00:00:00Z')`, command.ID, command.Type, command.Target, command.Principal, command.Digest, command.Payload); err != nil {
				t.Fatal(err)
			}
		}
		before := snapshot(refused.Post)
		if _, _, err := s.RetryIssueMakerFeedPostCommand(ctx, refused, 1729000054); err == nil {
			t.Fatal("refused retry succeeded", mode)
		}
		if snapshot(refused.Post) != before {
			t.Fatal("refusal mutated rows", mode)
		}
		if mode != "foreign-command" {
			if _, err := s.FactoryCommand(ctx, refused.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("refusal left journal entry", mode, err)
			}
		}
	}
	// The existing applied-draft branch intentionally keeps the revision.
	unchanged := newRequest("receipt-unchanged")
	pipe, err := s.PostIntake(ctx, unchanged.Post)
	if err != nil {
		t.Fatal(err)
	}
	next := pipe
	next.Judgment, next.Route, next.Draft, next.Effect = factory.PostJudgmentApplied, factory.PostRouteNewIssue, factory.PostDraftApplied, factory.PostEffectFailed
	if err := s.AdvancePostIntake(ctx, pipe, next); err != nil {
		t.Fatal(err)
	}
	unchanged.Body = "" // Keep-body shorthand still records the resolved digest.
	first, replayed, err := s.RetryIssueMakerFeedPostCommand(ctx, unchanged, 1729000055)
	if err != nil || replayed || first.Revision != 0 || first.BodyDigest != factory.MCPInputDigest([]byte("Before retry")) {
		t.Fatal("unchanged revision receipt", first, replayed, err)
	}
	if err := s.NotePostAttempt(ctx, unchanged.Post); err != nil {
		t.Fatal(err)
	}
	before = snapshot(unchanged.Post)
	again, replayed, err = s.RetryIssueMakerFeedPostCommand(ctx, unchanged, 1729000056)
	if err != nil || !replayed || again != first || snapshot(unchanged.Post) != before {
		t.Fatal("unchanged replay reset later attempts", again, replayed, err)
	}
	for _, sameIdentity := range []bool{true, false} {
		concurrent := newRequest(fmt.Sprintf("receipt-concurrent-%t", sameIdentity))
		var wg sync.WaitGroup
		var mu sync.Mutex
		created, replays, stale := 0, 0, 0
		for range 8 {
			wg.Go(func() {
				command := concurrent
				if !sameIdentity {
					command.ID = factory.NewID()
				}
				r, replayed, err := s.RetryIssueMakerFeedPostCommand(ctx, command, 1729000057)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil && r.Validate(command) == nil && !replayed:
					created++
				case err == nil && r.Validate(command) == nil && replayed:
					replays++
				case errors.Is(err, ErrStaleRevision):
					stale++
					if _, err := s.FactoryCommand(ctx, command.ID); !errors.Is(err, ErrNotFound) {
						t.Error("concurrent refusal left command", err)
					}
				default:
					t.Error("concurrent retry failed", err)
				}
			})
		}
		wg.Wait()
		if created != 1 || (sameIdentity && (replays != 7 || stale != 0)) || (!sameIdentity && (stale != 7 || replays != 0)) {
			t.Fatal("concurrent retry counts", sameIdentity, created, replays, stale)
		}
		var revisions, history int
		if err := s.db.QueryRowContext(ctx, `SELECT revision,jsonb_array_length(data->'history') FROM factory_feed_posts WHERE id=$1`, concurrent.Post).Scan(&revisions, &history); err != nil || revisions != 1 || history != 1 {
			t.Fatal("concurrent retry repeated transition", revisions, history, err)
		}
	}
}

func TestPendingPostRequestsRotatePastWaitingWork(t *testing.T) {
	ctx := context.Background()
	s, _ := postgresFixture(t)
	first := saveTestPost(t, s, 7, "42", "Waiting", "fair-1")
	second := saveTestPost(t, s, 7, "42", "Ready", "fair-2")
	for _, id := range []string{first, second} {
		if err := s.RecordPostIntake(ctx, id, 0); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.PendingPostIntakes(ctx, 1)
	if err != nil || len(page) != 1 || page[0].SourcePost != first {
		t.Fatalf("first: %+v %v", page, err)
	}
	if err := s.NotePostProgress(ctx, first, "Waiting for setup"); err != nil {
		t.Fatal(err)
	}
	page, err = s.PendingPostIntakes(ctx, 1)
	if err != nil || len(page) != 1 || page[0].SourcePost != second {
		t.Fatalf("waiting work starved next request: %+v %v", page, err)
	}
}

func TestPostProductReviewKeepsCanonicalScopeAndOwner(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	idea := factory.ProductIdea{ID: "d888888888888888888888888", Description: "Track requests", Consequence: "Requests persist", Decision: "accept", Source: "requested", Provenance: "human", Actor: 42}
	accepted := factory.IntentRevision{ID: "d111111111111111111111111", Intent: "d000000000000000000000000", Owner: 42, Repository: 7, Objective: "Track equipment requests", State: "accepted", MaxIssues: 10, CreatedUnix: 1, Ideas: []factory.ProductIdea{idea}, Tasks: []factory.IntentTask{{Key: "d999999999999999999999999", Objective: "Record requests", Outcomes: []string{"Requests persist"}, IdeaIDs: []string{idea.ID}}}}
	if err := s.SaveIntentRevision(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	var post string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM factory_feed_posts WHERE data->>'event_identity'=$1`, "intent-revision:"+accepted.ID).Scan(&post); err != nil {
		t.Fatal(err)
	}
	source, err := s.ResolvePostSource(ctx, 7, factory.PostSourcePost, post)
	if err != nil || source.Product == nil || source.Product.Intent != accepted.Intent || source.Product.AcceptedRevision != accepted.ID {
		t.Fatalf("canonical source missing: %+v %v", source, err)
	}
	if _, err := s.ResolvePostSource(ctx, 8, factory.PostSourcePost, post); err == nil {
		t.Fatal("foreign repository acquired product source")
	}
	save := func(key string) string {
		t.Helper()
		id, _, err := s.SaveFeedPostWithSource(ctx, factory.PostRequest{Repository: 7, Body: "Require Finance approval above EUR 1000; retain existing requests.", SourceKind: source.Kind, SourceID: source.ID, IdempotencyKey: key}, "43", "Robin", 2, source)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.RecordPostIntake(ctx, id, 0); err != nil {
			t.Fatal(err)
		}
		p, err := s.PostIntake(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		next := p
		next.Judgment = factory.PostJudgmentApplied
		next.Route = factory.PostRouteNewIssue
		if err = s.AdvancePostIntake(ctx, p, next); err != nil {
			t.Fatal(err)
		}
		return id
	}
	id, stale := save("finance"), save("another-change")
	detail, err := s.FeedPost(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	pipe, err := s.PostIntake(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.PostProductChange(ctx, detail, pipe, 42)
	if err != nil || view == nil || view.State != "review_required" || !view.CanReview {
		t.Fatalf("review not available: %+v %v", view, err)
	}
	if other, err := s.PostProductChange(ctx, detail, pipe, 43); err != nil || other.CanReview {
		t.Fatalf("nonowner review authority: %+v %v", other, err)
	}
	if _, err = s.ReviewPostProductChange(ctx, id, 0, 43, 3); err == nil {
		t.Fatal("comment author became product owner")
	}
	if _, err = s.ReviewPostProductChange(ctx, id, 1, 42, 3); err == nil {
		t.Fatal("stale comment reviewed")
	}
	draft, err := s.ReviewPostProductChange(ctx, id, 0, 42, 3)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Intent != accepted.Intent || draft.Predecessor != accepted.ID || draft.State != "draft" || len(draft.Ideas) != 2 || len(draft.EffectiveOutcomes) != 2 || draft.EffectiveOutcomes[0] != idea.ID || draft.Tasks[0].Key != accepted.Tasks[0].Key {
		t.Fatalf("successor changed baseline: %+v", draft)
	}
	if !strings.Contains(draft.Ideas[1].Evidence, "43") || draft.Ideas[1].Description != detail.Body {
		t.Fatal("original request or attribution lost")
	}
	view, err = s.PostProductChange(ctx, detail, pipe, 42)
	if err != nil || view.State != "draft" || !view.CanReview || view.DraftRevision != draft.ID {
		t.Fatalf("owner cannot continue: %+v %v", view, err)
	}
	if other, err := s.PostProductChange(ctx, detail, pipe, 43); err != nil || other.CanReview {
		t.Fatalf("nonowner continuation authority: %+v %v", other, err)
	}
	replay, err := s.ReviewPostProductChange(ctx, id, 0, 42, 4)
	if err != nil || replay.ID != draft.ID || replay.CreatedUnix != draft.CreatedUnix {
		t.Fatalf("handoff duplicated: %+v %v", replay, err)
	}
	if _, err = s.ReviewPostProductChange(ctx, stale, 0, 42, 4); err == nil {
		t.Fatal("another comment replaced current review")
	}
	if _, err = s.EditFeedPost(ctx, id, "43", "Different request", 0, 5); err == nil {
		t.Fatal("admitted source rewritten")
	}
	if err = s.RetryFeedPost(ctx, id, "43", 0, "Different request", 5); err == nil {
		t.Fatal("admitted source retried")
	}
	queued, err := s.PendingPostIntakes(ctx, 20)
	if err != nil || len(queued) != 0 {
		t.Fatalf("review waits in automatic issue queue: %+v %v", queued, err)
	}
	inherited, err := s.ResolvePostSource(ctx, 7, factory.PostSourcePost, id)
	if err != nil || inherited.Product == nil || *inherited.Product != *source.Product {
		t.Fatalf("thread lost source lineage: %+v %v", inherited, err)
	}
	adopted := draft
	adopted.ID = "d222222222222222222222222"
	adopted.Predecessor = draft.ID
	adopted.State = "accepted"
	adopted.Tasks = append(append([]factory.IntentTask(nil), draft.Tasks...), factory.IntentTask{Key: "d777777777777777777777777", Objective: "Finance approval", Outcomes: []string{"Threshold is applied"}, IdeaIDs: []string{draft.Ideas[1].ID}})
	if err = s.SaveIntentRevision(ctx, adopted); err != nil {
		t.Fatal(err)
	}
	view, err = s.PostProductChange(ctx, detail, pipe, 42)
	if err != nil || view.State != "accepted" || !view.CanReview {
		t.Fatalf("owner cannot open accepted change: %+v %v", view, err)
	}
	if other, err := s.PostProductChange(ctx, detail, pipe, 43); err != nil || other.State != "accepted" || other.CanReview {
		t.Fatalf("collaborator gained accepted owner control: %+v %v", other, err)
	}
	replay, err = s.ReviewPostProductChange(ctx, id, 0, 42, 6)
	head, headErr := s.IntentRevision(ctx, accepted.Intent, 42)
	if err != nil || replay.ID != draft.ID || headErr != nil || head.ID != adopted.ID {
		t.Fatal("retry rolled back accepted head")
	}
	var revisions, graphs int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM factory_intent_revisions WHERE intent=$1`, accepted.Intent).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM factory_intent_generations`).Scan(&graphs); err != nil {
		t.Fatal(err)
	}
	if revisions != 3 || graphs != 0 {
		t.Fatalf("review silently generated scope: revisions=%d graphs=%d", revisions, graphs)
	}
	removed := adopted
	removed.ID, removed.Predecessor = "d333333333333333333333333", adopted.ID
	removed.Ideas = append([]factory.ProductIdea(nil), adopted.Ideas...)
	removed.Ideas[1].Decision = "decline"
	removed.EffectiveOutcomes = []string{idea.ID}
	removed.Tasks = accepted.Tasks
	if err = s.SaveIntentRevision(ctx, removed); err != nil {
		t.Fatal(err)
	}
	view, err = s.PostProductChange(ctx, detail, pipe, 42)
	if err != nil || view.State != "superseded" {
		t.Fatalf("declined Finance proposal still presented as accepted: %+v %v", view, err)
	}
	// A corrupted same-product binding is a projection error, never a
	// silent absence: the store surfaces the error so the API projects the
	// lane explicitly instead of falling back to detached generic progress.
	if _, err = s.db.ExecContext(ctx, `UPDATE factory_feed_posts SET data = data || '{"product_change": "bogus"}' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PostProductChange(ctx, detail, pipe, 42); err == nil {
		t.Fatal("corrupted product binding reads as a successful projection")
	}
}
