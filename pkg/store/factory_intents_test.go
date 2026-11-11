package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

func TestIntentRepositoryBindingSurvivesNonAcceptedHeads(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	idea := factory.ProductIdea{ID: "d888888888888888888888888", Description: "Track", Consequence: "Visibility", Decision: "accept", Source: "requested", Provenance: "human", Actor: 1}
	task := factory.IntentTask{Key: "d999999999999999999999999", Objective: "Entry", Outcomes: []string{"Capture"}, IdeaIDs: []string{idea.ID}}
	committed := factory.IntentRevision{ID: "d111111111111111111111111", Intent: "d000000000000000000000000", Owner: 1, Repository: 42, Objective: "Tracker", State: "accepted", MaxIssues: 10, CreatedUnix: 1, Ideas: []factory.ProductIdea{idea}, Tasks: []factory.IntentTask{task}}
	if err := s.SaveIntentRevision(ctx, committed); err != nil {
		t.Fatal(err)
	}
	refused := func(r factory.IntentRevision) {
		t.Helper()
		if err := s.SaveIntentRevision(ctx, r); err == nil || !strings.Contains(err.Error(), "repository change needs a separate build request") {
			t.Fatalf("repository retarget accepted: %v", err)
		}
	}
	draft := committed
	draft.ID = "d222222222222222222222222"
	draft.Predecessor = committed.ID
	draft.State = "draft"
	if err := s.SaveIntentRevision(ctx, draft); err != nil {
		t.Fatal(err)
	}
	retarget := draft
	retarget.ID = "d333333333333333333333333"
	retarget.Predecessor = draft.ID
	retarget.Repository = 43
	refused(retarget)
	head, err := s.IntentRevision(ctx, committed.Intent, 1)
	if err != nil || head.ID != draft.ID {
		t.Fatalf("refused save moved the head: %+v %v", head, err)
	}
	next := draft
	next.ID = "d444444444444444444444444"
	next.Predecessor = draft.ID
	if err := s.SaveIntentRevision(ctx, next); err != nil {
		t.Fatal(err)
	}
	rejected := next
	rejected.ID = "d555555555555555555555555"
	rejected.Predecessor = next.ID
	rejected.State = "rejected"
	if err := s.SaveIntentRevision(ctx, rejected); err != nil {
		t.Fatal(err)
	}
	afterRejected := rejected
	afterRejected.ID = "d666666666666666666666666"
	afterRejected.Predecessor = rejected.ID
	afterRejected.State = "draft"
	afterRejected.Repository = 43
	refused(afterRejected)
	if err := s.SaveIntentRevision(ctx, rejected); err != nil {
		t.Fatal("exact replay of pinned head refused")
	}
	same := rejected
	same.ID = "d777777777777777777777777"
	same.Predecessor = rejected.ID
	same.State = "accepted"
	if err := s.SaveIntentRevision(ctx, same); err != nil {
		t.Fatal(err)
	}
	free := factory.IntentRevision{ID: "d000000000000000000000001", Intent: "dffffffffffffffffffffffff", Owner: 1, Repository: 7, Objective: "Tracker", State: "draft", MaxIssues: 10, CreatedUnix: 1}
	if err := s.SaveIntentRevision(ctx, free); err != nil {
		t.Fatal(err)
	}
	choice := free
	choice.ID = "d000000000000000000000002"
	choice.Predecessor = free.ID
	choice.Repository = 8
	if err := s.SaveIntentRevision(ctx, choice); err != nil {
		t.Fatal("uncommitted repository choice refused")
	}
	first := choice
	first.ID = "d000000000000000000000003"
	first.Predecessor = choice.ID
	first.State = "accepted"
	first.Ideas = []factory.ProductIdea{idea}
	first.Tasks = []factory.IntentTask{task}
	if err := s.SaveIntentRevision(ctx, first); err != nil {
		t.Fatal(err)
	}
	late := first
	late.ID = "d000000000000000000000004"
	late.Predecessor = first.ID
	late.State = "draft"
	late.Repository = 7
	refused(late)
}

func TestIntentRevisionCASAndPrivacy(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	d := factory.Department{ID: "d111111111111111111111111", Owner: 1, Name: "Operations", Description: "Coordinate requests"}
	if err := s.CreateDepartment(ctx, d); err != nil {
		t.Fatal(err)
	}
	r := factory.IntentRevision{ID: "d222222222222222222222222", Intent: "d333333333333333333333333", Owner: 1, Department: d.ID, Objective: "Build request tracker", State: "draft", MaxIssues: 10, CreatedUnix: 1}
	if err := s.SaveIntentRevision(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveIntentRevision(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Objective = "Changed objective"
	if !errors.Is(s.SaveIntentRevision(ctx, r), ErrCommandConflict) {
		t.Fatal("changed replay accepted")
	}
	r.ID = "d444444444444444444444444"
	if !errors.Is(s.SaveIntentRevision(ctx, r), ErrStaleRevision) {
		t.Fatal("stale predecessor accepted")
	}
	r.Predecessor = "d222222222222222222222222"
	if err := s.SaveIntentRevision(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IntentRevision(ctx, r.Intent, 2); !errors.Is(err, ErrNotFound) {
		t.Fatal("private intent visible")
	}
	r.Owner = 2
	r.Intent = "d555555555555555555555555"
	r.Predecessor = ""
	if s.SaveIntentRevision(ctx, r) == nil {
		t.Fatal("other owner department accepted")
	}
}

func TestListIntentRevisionsIsOwnerPrivateAndBounded(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	a := factory.IntentRevision{ID: "d111111111111111111111111", Intent: "d222222222222222222222222", Owner: 1, Objective: "First", State: "draft", MaxIssues: 10, CreatedUnix: 1}
	b := factory.IntentRevision{ID: "d333333333333333333333333", Intent: "d444444444444444444444444", Owner: 1, Objective: "Second", State: "draft", MaxIssues: 10, CreatedUnix: 1}
	other := factory.IntentRevision{ID: "d555555555555555555555555", Intent: "d666666666666666666666666", Owner: 2, Objective: "Other", State: "draft", MaxIssues: 10, CreatedUnix: 1}
	for _, r := range []factory.IntentRevision{a, b, other} {
		if err := s.SaveIntentRevision(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	got, next, err := s.ListIntentRevisions(ctx, 1, factory.IntentListFilter{Limit: 1})
	if err != nil || len(got) != 1 || next == "" {
		t.Fatalf("bounded list %v %v %v", got, next, err)
	}
	got, next, err = s.ListIntentRevisions(ctx, 1, factory.IntentListFilter{Cursor: next, Limit: 10})
	if err != nil || len(got) != 1 || next != "" || got[0].Intent == other.Intent {
		t.Fatal("cursor leaked or missed")
	}
	got, _, err = s.ListIntentRevisions(ctx, 1, factory.IntentListFilter{State: "accepted", Limit: 10})
	if err != nil || len(got) != 0 {
		t.Fatal("empty filter not empty")
	}
}

func TestIntentHistoryAndRepositoryIdentity(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	a := factory.IntentRevision{ID: "d111111111111111111111111", Intent: "d222222222222222222222222", Owner: 1, Objective: "Tracker", State: "draft", MaxIssues: 10, CreatedUnix: 1}
	if err := s.SaveIntentRevision(ctx, a); err != nil {
		t.Fatal(err)
	}
	b := a
	b.ID = "d333333333333333333333333"
	b.Predecessor = a.ID
	b.Repository = 42
	b.State = "accepted"
	b.Ideas = []factory.ProductIdea{{ID: "d444444444444444444444444", Description: "Track", Consequence: "Visibility", Decision: "accept", Source: "requested", Provenance: "human", Actor: 1}}
	b.Tasks = []factory.IntentTask{{Key: "d555555555555555555555555", Objective: "Entry", Outcomes: []string{"Capture"}, IdeaIDs: []string{"d444444444444444444444444"}}}
	if err := s.SaveIntentRevision(ctx, b); err != nil {
		t.Fatal(err)
	}
	history, err := s.IntentHistory(ctx, a.Intent, 1, 10)
	if err != nil || len(history) != 2 || history[0].ID != b.ID {
		t.Fatalf("history %v %v", history, err)
	}
	c := b
	c.ID = "d666666666666666666666666"
	c.Predecessor = b.ID
	c.Repository = 43
	if s.SaveIntentRevision(ctx, c) == nil {
		t.Fatal("repository change kept same request identity")
	}
}

func TestPriorityJudgmentsStoreCompletedChoices(t *testing.T) {
	s, dsn := postgresFixture(t)
	ctx := context.Background()
	for _, snapshot := range []string{strings.Repeat("a", 64), strings.Repeat("b", 64)} {
		if _, err := s.PriorityJudgment(ctx, snapshot); err != ErrNotFound {
			t.Fatal("unexpected completed judgment", err)
		}
		judgment := factory.PriorityReceipt{Snapshot: snapshot, CreatedUnix: 1, Selected: "oldest", Outcome: "fairness"}
		if err := s.RecordPriorityJudgment(ctx, judgment); err != nil {
			t.Fatal(err)
		}
		got, err := s.PriorityJudgment(ctx, snapshot)
		if err != nil || got.Selected != judgment.Selected || got.Outcome != judgment.Outcome {
			t.Fatal("completed choice lost", got, err)
		}
		if err := s.RecordPriorityJudgment(ctx, judgment); err != ErrCommandConflict {
			t.Fatal("completed choice overwritten", err)
		}
	}

	// Membership exceeds one ordinary dispatch batch and interleaves two
	// repositories in age order; each capture must still contain its full set.
	for issue := int64(65); issue > 0; issue-- {
		candidate := readinessCandidate()
		candidate.Issue = issue
		if _, _, err := s.RecordIssueAssessment(ctx, candidate, time.Unix(200, 0)); err != nil {
			t.Fatal(err)
		}
	}
	for issue, seen := range map[int64]int64{1: 100, 2: 300} {
		candidate := readinessCandidate()
		candidate.Repository, candidate.Issue = 9, issue
		if _, _, err := s.RecordIssueAssessment(ctx, candidate, time.Unix(seen, 0)); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.PriorityRepositoryAfter(ctx, 0, 0, false)
	if err != nil || first.Repository != 9 || first.FirstSeenUnix != 100 {
		t.Fatalf("oldest repository group: %+v %v", first, err)
	}
	next, err := s.PriorityRepositoryAfter(ctx, first.FirstSeenUnix, first.Repository, true)
	if err != nil || next.Repository != 7 || next.FirstSeenUnix != 200 {
		t.Fatalf("next complete repository group: %+v %v", next, err)
	}
	if _, err := s.PriorityRepositoryAfter(ctx, next.FirstSeenUnix, next.Repository, true); !errors.Is(err, ErrNotFound) {
		t.Fatal("repository rotation did not reach its end:", err)
	}
	seven, err := s.CapturePriorityInventory(ctx, 7)
	if err != nil || len(seven.Subjects) != 65 {
		t.Fatalf("complete repository 7 inventory: %d %v", len(seven.Subjects), err)
	}
	for i, subject := range seven.Subjects {
		if subject.Repository != 7 || subject.Issue != int64(i+1) || subject.FirstSeenUnix != 200 {
			t.Fatalf("repository 7 membership/order: %+v", seven.Subjects)
		}
	}
	nine, err := s.CapturePriorityInventory(ctx, 9)
	if err != nil || !reflect.DeepEqual(nine.Subjects, []PriorityInventorySubject{{Repository: 9, Issue: 1, FirstSeenUnix: 100}, {Repository: 9, Issue: 2, FirstSeenUnix: 300}}) {
		t.Fatalf("complete repository 9 inventory: %+v %v", nine.Subjects, err)
	}
	legacy, err := s.CapturePriorityInventory(ctx, 0)
	if err != nil || len(legacy.Subjects) != 67 {
		t.Fatalf("retained global inventory: %d %v", len(legacy.Subjects), err)
	}
	if _, err := s.SavePriorityScan(ctx, 0, 3, legacy); err != nil {
		t.Fatal(err)
	}
	legacyObservation := PriorityCandidateObservation{Subject: PrioritySubjectKey(9, 1), Repository: 9, Issue: 1, Exclusion: "retained_unavailable", Fingerprint: "retained"}
	if err := s.RecordPriorityProgress(ctx, 0, 3, legacy.LocalDigest, []PriorityCandidateObservation{legacyObservation}, 1); err != nil {
		t.Fatal(err)
	}
	observations := func(inventory PriorityInventory) []PriorityCandidateObservation {
		t.Helper()
		out := make([]PriorityCandidateObservation, 0, len(inventory.Subjects))
		for _, subject := range inventory.Subjects {
			control, err := s.IssueControl(ctx, subject.Repository, subject.Issue)
			if err != nil {
				t.Fatal(err)
			}
			key := PrioritySubjectKey(subject.Repository, subject.Issue)
			data, err := json.Marshal(factory.PriorityCandidate{ID: key, Control: control, Objective: "Retained repository candidate"})
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, PriorityCandidateObservation{Subject: key, Repository: subject.Repository, Issue: subject.Issue, Candidate: data, Fingerprint: control.Fingerprint})
		}
		return out
	}
	sevenObservations, nineObservations := observations(seven), observations(nine)
	for repo, inventory := range map[int64]PriorityInventory{7: seven, 9: nine} {
		if resumed, err := s.SavePriorityScan(ctx, repo, 5, inventory); err != nil || resumed {
			t.Fatalf("fresh repository %d scan resumed: %t %v", repo, resumed, err)
		}
	}
	if err := s.RecordPriorityProgress(ctx, 7, 5, seven.LocalDigest, sevenObservations[:1], 1); err != nil {
		t.Fatal(err)
	}
	partial, err := s.LoadPriorityScan(ctx, 7)
	if err != nil || partial.Cursor != 1 || len(partial.Observations) != 1 {
		t.Fatalf("repository progress was not retained: %+v %v", partial, err)
	}
	if resumed, err := s.SavePriorityScan(ctx, 7, 5, seven); err != nil || !resumed {
		t.Fatalf("identical JSONB inventory did not resume: %t %v", resumed, err)
	}
	if got, err := s.LoadPriorityScan(ctx, 7); err != nil || !reflect.DeepEqual(got, partial) {
		t.Fatalf("resume replaced repository progress: %+v %v", got, err)
	}
	if err := s.RecordPriorityProgress(ctx, 7, 5, seven.LocalDigest, sevenObservations[1:], len(seven.Subjects)); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPriorityProgress(ctx, 9, 5, nine.LocalDigest, nineObservations, len(nine.Subjects)); err != nil {
		t.Fatal(err)
	}
	retained := map[int64]PriorityScanState{}
	for _, repo := range []int64{0, 7, 9} {
		scan, err := s.LoadPriorityScan(ctx, repo)
		if err != nil {
			t.Fatal(err)
		}
		if repo != 0 && (scan.Cursor != len(scan.Subjects) || len(scan.Observations) != len(scan.Subjects)) {
			t.Fatalf("repository %d observations incomplete: %+v", repo, scan)
		}
		retained[repo] = scan
	}
	if retained[0].ScanID != PriorityInventoryScanID || retained[7].ScanID == retained[9].ScanID || retained[7].ScanID == retained[0].ScanID {
		t.Fatal("repository scans reused a retained journal identity")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	assertRetained := func(repo int64) {
		t.Helper()
		if got, err := s.LoadPriorityScan(ctx, repo); err != nil || !reflect.DeepEqual(got, retained[repo]) {
			t.Fatalf("repository %d journal changed: %+v %v", repo, got, err)
		}
	}
	for _, repo := range []int64{0, 7, 9} {
		assertRetained(repo)
	}
	if _, err := s.SavePriorityScan(ctx, 7, 5, nine); err == nil {
		t.Fatal("foreign inventory replaced repository 7")
	}
	if err := s.RecordPriorityProgress(ctx, 7, 5, seven.LocalDigest, nineObservations, 1); err == nil {
		t.Fatal("foreign observations entered repository 7")
	}
	changed := readinessCandidate()
	changed.Repository, changed.Issue, changed.Fingerprint = 9, 1, strings.Repeat("c", 64)
	if _, _, err := s.RecordIssueAssessment(ctx, changed, time.Unix(400, 0)); err != nil {
		t.Fatal(err)
	}
	freshNine, err := s.CapturePriorityInventory(ctx, 9)
	if err != nil || freshNine.LocalDigest == nine.LocalDigest {
		t.Fatalf("repository change did not invalidate its inputs: %+v %v", freshNine, err)
	}
	if freshSeven, err := s.CapturePriorityInventory(ctx, 7); err != nil || !reflect.DeepEqual(freshSeven, seven) {
		t.Fatalf("foreign input change invalidated repository 7: %+v %v", freshSeven, err)
	}
	if resumed, err := s.SavePriorityScan(ctx, 9, 6, freshNine); err != nil || resumed {
		t.Fatalf("changed repository scan resumed: %t %v", resumed, err)
	}
	if got, err := s.LoadPriorityScan(ctx, 9); err != nil || got.Cursor != 0 || len(got.Observations) != 0 {
		t.Fatalf("changed scan retained invalid observations: %+v %v", got, err)
	}
	for _, stale := range []struct {
		revision int64
		digest   string
	}{{5, freshNine.LocalDigest}, {6, nine.LocalDigest}} {
		if err := s.RecordPriorityProgress(ctx, 9, stale.revision, stale.digest, nineObservations, 2); !errors.Is(err, ErrStaleRevision) {
			t.Fatal("stale repository progress was accepted:", err)
		}
	}
	if err := s.RecordPriorityProgress(ctx, 9, 6, freshNine.LocalDigest, observations(freshNine), 2); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePriorityScan(ctx, 9); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadPriorityScan(ctx, 9); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted repository scan remained:", err)
	}
	assertRetained(0)
	assertRetained(7)
	for _, snapshot := range []string{strings.Repeat("a", 64), strings.Repeat("b", 64)} {
		if got, err := s.PriorityJudgment(ctx, snapshot); err != nil || got.Selected != "oldest" || got.Outcome != "fairness" {
			t.Fatalf("completed judgment changed after repository retirement: %+v %v", got, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO issue_controls(repository,issue,revision,data)
		SELECT 9,generated.issue,1,original.data || jsonb_build_object('issue',generated.issue::text,'revision',1)
		FROM issue_controls original CROSS JOIN generate_series(3,$1) AS generated(issue)
		WHERE original.repository=9 AND original.issue=1`, MaxPriorityInventorySubjects+1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CapturePriorityInventory(ctx, 9); !errors.Is(err, ErrPriorityInventoryTooLarge) {
		t.Fatal("oversized repository elected a partial inventory:", err)
	}
	if got, err := s.CapturePriorityInventory(ctx, 7); err != nil || !reflect.DeepEqual(got, seven) {
		t.Fatalf("oversized foreign inventory blocked repository 7: %+v %v", got, err)
	}
}
