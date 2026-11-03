package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

func factoryEventCards(t *testing.T, s *Store, repository int64) map[string][]FeedPostDetail {
	t.Helper()
	entries, _, _, err := s.SubscribedFeed(context.Background(), factory.FeedQuery{Repositories: []int64{repository}, Kinds: []string{factory.FeedKindRecord}, Limit: 256})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, entry := range entries {
		if entry.Kind == factory.FeedKindPost {
			ids = append(ids, entry.ID)
		}
	}
	details, err := s.FeedPostDetails(context.Background(), ids)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]FeedPostDetail{}
	for _, detail := range details {
		if detail.Actor == "factory" && detail.Payload != nil && detail.Payload.Communication != nil {
			id := detail.Payload.Communication.ComponentID
			out[id] = append(out[id], detail)
		}
	}
	return out
}

func TestFactoryFeedPreparationDependencyAndPauseProducers(t *testing.T) {
	s, ctx, _ := prepTestStore(t)
	p := prepTestPreparation()
	if _, _, err := s.AdmitPreparation(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.State = execution.PreparationState{Container: strings.Repeat("c", 64), SetupDigest: p.Preparation.SetupDigest, ID: p.Preparation.ID, Project: p.Preparation.Project, Role: p.Preparation.Role, Phase: project.PrepareReady, SourceCommit: p.Preparation.SourceCommit, Ready: true}
	if err := s.ObservePreparation(ctx, p); err != nil {
		t.Fatal(err)
	}
	blocked := readinessCandidate()
	blocked.Readiness, blocked.Reason = factory.ReadinessBlocked, factory.BlockerCodePending
	blocked.Blockers = []factory.Blocker{{Code: factory.BlockerCodePending, EndpointRepo: 7, EndpointIssue: 9, Resolution: factory.BlockerResolution(factory.BlockerCodePending)}}
	if _, _, err := s.RecordIssueAssessment(ctx, blocked, time.Now()); err != nil {
		t.Fatal(err)
	}
	ready := readinessCandidate()
	ready.Fingerprint = strings.Repeat("c", 64)
	if _, _, err := s.RecordIssueAssessment(ctx, ready, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RecordIssueAssessment(ctx, ready, time.Now()); err != nil {
		t.Fatal(err)
	}
	w, err := s.WithdrawDispatch(ctx, 7, "manual_pause", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.WithdrawDispatch(ctx, 7, "manual_pause", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReopenDispatch(ctx, 7, w.Revision); err != nil {
		t.Fatal(err)
	}
	cards := factoryEventCards(t, s, 7)
	if len(cards["F01"]) != 1 || len(cards["F07"]) != 1 || len(cards["F08"]) != 2 {
		t.Fatalf("missing/duplicated canonical events: %+v", cards)
	}
	if cards["F07"][0].Status != "dependency:released" || cards["F07"][0].Record.Issue != 3 {
		t.Fatalf("wrong dependency context: %+v", cards["F07"][0])
	}
	posts, _, _, err := s.SubscribedFeed(ctx, factory.FeedQuery{Repositories: []int64{7}, Kinds: []string{factory.FeedKindPost}, Limit: 20})
	if err != nil || len(posts) != 0 {
		t.Fatalf("Factory events leaked into human comments: %+v %v", posts, err)
	}
}

func TestFactoryFeedEventRollbackAndScope(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	write := func(repository int64, commit bool) {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = recordFactoryFeedEventTx(ctx, tx, repository, 4, "", "stable-event", "dispatch:assigned", factory.FeedCommunication{ComponentID: "F02", Title: "Assigned", Summary: "Worker assigned"}, 100); err != nil {
			t.Fatal(err)
		}
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	write(7, false)
	if len(factoryEventCards(t, s, 7)) != 0 {
		t.Fatal("rolled-back effect produced a card")
	}
	write(7, true)
	write(7, true)
	write(9, true)
	if len(factoryEventCards(t, s, 7)["F02"]) != 1 || len(factoryEventCards(t, s, 9)["F02"]) != 1 {
		t.Fatal("event replay or scope mismatch")
	}
}

func TestFactoryFeedNativeRunEndProducers(t *testing.T) {
	s := takeoverStore(t)
	ctx := context.Background()
	for _, test := range []struct {
		outcome   factory.Outcome
		component string
	}{{factory.Succeeded, "F02"}, {factory.Cancelled, "F08"}, {factory.Failed, "F09"}} {
		run := factoryRun(time.Now())
		run.ProjectID = takeoverFixture().Project
		if err := s.RecordFactoryRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		run.Outcome, run.Summary = test.outcome, "Observed native result"
		if err := s.SaveFactoryRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if len(factoryEventCards(t, s, 7)[test.component]) != 0 {
			t.Fatal("run end appeared before native resources settled")
		}
		run.Reconciled = true
		for range 2 {
			if err := s.SaveFactoryRun(ctx, run); err != nil {
				t.Fatal(err)
			}
		}
		cards := factoryEventCards(t, s, 7)[test.component]
		if len(cards) != 1 || cards[0].Status != "run:"+string(test.outcome) {
			t.Fatalf("native outcome presentation: %+v", cards)
		}
	}
}
