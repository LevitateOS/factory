package store

import (
	"context"
	"testing"

	"github.com/levitateos/sodaos/internal/factory"
)

func TestFeedSubscriptionRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, _ := postgresFixture(t)
	sub, revision, err := s.FeedSubscription(ctx, "reader-1")
	if err != nil || revision != 0 || len(sub.Repositories) != 0 {
		t.Fatalf("missing subscription = %+v %d %v", sub, revision, err)
	}
	first := factory.FeedSubscription{Subscriber: "reader-1", Repositories: []int64{9, 7}, Kinds: []string{factory.FeedKindRecord}}
	if err := s.SetFeedSubscription(ctx, "reader-1", 0, first); err != nil {
		t.Fatal(err)
	}
	stored, revision, err := s.FeedSubscription(ctx, "reader-1")
	if err != nil || revision != 1 || len(stored.Repositories) != 2 {
		t.Fatalf("stored subscription = %+v %d %v", stored, revision, err)
	}
	if err := stored.Validate(); err != nil {
		t.Fatalf("stored subscription invalid: %v", err)
	}
	narrower := factory.FeedSubscription{Subscriber: "reader-1", Repositories: []int64{7}}
	if err := s.SetFeedSubscription(ctx, "reader-1", 0, narrower); err == nil {
		t.Fatal("stale subscription write admitted")
	}
	if err := s.SetFeedSubscription(ctx, "reader-1", 1, narrower); err != nil {
		t.Fatal(err)
	}
	if _, revision, err := s.FeedSubscription(ctx, "reader-1"); err != nil || revision != 2 {
		t.Fatalf("revision = %d %v", revision, err)
	}
	if err := s.SetFeedSubscription(ctx, "", 2, narrower); err == nil {
		t.Fatal("empty subscriber admitted")
	}
}
