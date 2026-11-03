// Package service owns Factory application workflows over the canonical ledger.
// Hosts supply admitted actors and current authority through explicit ports;
// HTTP requests, native sessions and provider transports stay with their adapters.
package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

var (
	ErrFeedSubscriptionUnavailable = errors.New("feed subscription is unavailable")
	ErrFeedProjectScope            = errors.New("select one project")
	ErrFeedRepositoryUnavailable   = errors.New("feed repository is not visible")
)

// FeedVisibility checks current access to a canonical repository and returns
// its display name. Subscription membership never supplies this authority.
type FeedVisibility func(context.Context, int64) (string, error)

type Feed struct {
	Store *store.Store
}

func feedSubscriber(actor collaboration.ActorID) (string, error) {
	if actor < 1 {
		return "", ErrFeedQueryInvalid
	}
	return strconv.FormatInt(int64(actor), 10), nil
}

func (f Feed) Subscription(ctx context.Context, actor collaboration.ActorID) (factory.FeedSubscription, int64, error) {
	subscriber, err := feedSubscriber(actor)
	if err != nil {
		return factory.FeedSubscription{}, 0, err
	}
	if f.Store == nil {
		return factory.FeedSubscription{}, 0, ErrFeedUnavailable
	}
	return f.Store.FeedSubscription(ctx, subscriber)
}

// SaveSubscription always binds preferences to the admitted actor; a caller's
// supplied subscriber or revision cannot replace that ownership or CAS guard.
func (f Feed) SaveSubscription(ctx context.Context, actor collaboration.ActorID, expected int64, sub factory.FeedSubscription) error {
	subscriber, err := feedSubscriber(actor)
	if err != nil {
		return err
	}
	if f.Store == nil {
		return ErrFeedUnavailable
	}
	sub.Subscriber, sub.Revision = subscriber, expected+1
	if expected < 0 || sub.Validate() != nil {
		return ErrFeedQueryInvalid
	}
	return f.Store.SetFeedSubscription(ctx, subscriber, expected, sub)
}

// Read selects and projects a feed without an HTTP/native-session dependency.
// Project scope bypasses personal subscription narrowing only after current
// visibility admission; each returned entry is checked again, without caching
// one repository verdict across rows. Filtered rows still advance the cursor.
func (f Feed) Read(ctx context.Context, actor collaboration.ActorID, query factory.FeedQuery, projectScope bool, visible FeedVisibility) (FeedPage, error) {
	var empty FeedPage
	if err := query.Validate(); err != nil {
		return empty, err
	}
	if visible == nil {
		return empty, ErrFeedUnavailable
	}
	sub, _, err := f.Subscription(ctx, actor)
	if err != nil {
		return empty, fmt.Errorf("%w: %w", ErrFeedSubscriptionUnavailable, err)
	}
	if projectScope {
		if len(query.Repositories) != 1 {
			return empty, ErrFeedProjectScope
		}
		if _, err := visible(ctx, query.Repositories[0]); err != nil {
			return empty, ErrFeedRepositoryUnavailable
		}
		subscriber, _ := feedSubscriber(actor)
		sub = factory.FeedSubscription{Subscriber: subscriber, Revision: 1, Repositories: query.Repositories}
	}
	projects := make(map[int64]string)
	view, err := serveSubscribedFeed(ctx, f.Store, func(entry factory.FeedEntry) bool {
		name, err := visible(ctx, entry.Repository)
		if err != nil {
			return false
		}
		projects[entry.Repository] = name
		return true
	}, query, sub)
	if err != nil {
		return empty, err
	}
	EnrichPosts(ctx, f.Store, projects, view.Entries)
	EnrichRecords(ctx, f.Store, projects, view.Entries)
	EnrichParents(ctx, f.Store, view.Entries)
	return view, nil
}

// Page assembles the actual application response over one ledger. Posts supplies
// current private action readback and stage availability; this method binds its
// read visibility to the same admitted canonical actor/repository port as the
// feed. Known hidden rows still advance the source cursor. Unknown authority or
// detail reads return no partial page. No workflow is driven by these reads.
func (f Feed) Page(ctx context.Context, actor collaboration.ActorID, query factory.FeedQuery, projectScope bool, visible RepositoryVisibility, posts Posts) (FeedPage, error) {
	var empty FeedPage
	if f.Store == nil || posts.Store != f.Store || visible == nil {
		return empty, ErrFeedUnavailable
	}
	if actor <= 0 {
		return empty, ErrFeedQueryInvalid
	}
	var accessError error
	readVisible := func(ctx context.Context, repository int64) (string, error) {
		name, err := visible(ctx, actor, collaboration.RepositoryID(repository))
		if err != nil && !errors.Is(err, ErrRepositoryHidden) {
			accessError = errors.Join(accessError, err)
		}
		return name, err
	}
	view, err := f.Read(ctx, actor, query, projectScope, readVisible)
	if accessError != nil {
		return empty, accessError
	}
	if err != nil {
		return empty, err
	}
	posts.Visibility = func(ctx context.Context, admitted collaboration.ActorID, repository int64) error {
		if admitted != actor {
			return ErrPostVisibility
		}
		_, err := readVisible(ctx, repository)
		return err
	}
	joined := make([]factory.FeedEntry, 0, len(view.Entries))
	for _, entry := range view.Entries {
		if err := ctx.Err(); err != nil {
			return empty, err
		}
		if entry.Kind == factory.FeedKindPost && (entry.Payload == nil || entry.Payload.Communication == nil) {
			post, err := posts.Read(ctx, actor, entry.ID)
			if errors.Is(err, ErrPostNotFound) || errors.Is(err, ErrRepositoryHidden) {
				continue
			}
			if err != nil {
				return empty, err
			}
			if post["post_id"] != entry.ID || post["repository"] != strconv.FormatInt(entry.Repository, 10) {
				return empty, ErrPostViewUnavailable
			}
			entry.Post = post
		}
		joined = append(joined, entry)
	}
	// A later post/product/stage read may outlive an earlier repository check.
	// Seal every returned kind after ALL assembly, including records and system
	// posts that did not need the human-post detail reader.
	view.Entries = make([]factory.FeedEntry, 0, len(joined))
	for _, entry := range joined {
		if err := ctx.Err(); err != nil {
			return empty, err
		}
		name, err := readVisible(ctx, entry.Repository)
		if errors.Is(err, ErrRepositoryHidden) {
			continue
		}
		if err != nil {
			return empty, err
		}
		entry.Project = name
		view.Entries = append(view.Entries, entry)
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	return view, nil
}
