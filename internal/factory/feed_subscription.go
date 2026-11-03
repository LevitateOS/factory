package factory

import (
	"errors"
	"sort"
)

// FeedSubscription is one reader's combined-feed preferences: the
// repository allow-list and kind filter applied inside already
// permitted activity. A subscription never grants access; the serving
// layer intersects it with the caller's permitted scope and rechecks
// visibility per entry. Revision guards concurrent preference writes:
// a stale write refuses instead of dropping another update.
type FeedSubscription struct {
	Subscriber   string   `json:"subscriber"`
	Repositories []int64  `json:"repositories"`
	Kinds        []string `json:"kinds,omitempty"`
	Revision     int64    `json:"revision"`
}

// Validate admits one feed subscription with a positive revision and
// scope.
func (s FeedSubscription) Validate() error {
	if s.Subscriber == "" {
		return errors.New("feed subscription needs its subscriber")
	}
	if len(s.Repositories) == 0 {
		return errors.New("feed subscription needs its repository scope")
	}
	for _, repository := range s.Repositories {
		if repository <= 0 {
			return errors.New("feed subscription needs its positive repository scope")
		}
	}
	for _, kind := range s.Kinds {
		if kind != FeedKindRecord && kind != FeedKindPost {
			return errors.New("feed subscription needs its known kinds")
		}
	}
	if s.Revision < 1 {
		return errors.New("feed subscription needs its positive revision")
	}
	return nil
}

// ApplyFeedSubscription narrows one feed query to the intersection of
// its scope and kinds with the subscription, preserving the window and
// cursor. An empty intersection serves no entries; it never widens
// to the original query scope.
func ApplyFeedSubscription(query FeedQuery, sub FeedSubscription) FeedQuery {
	member := make(map[int64]bool, len(sub.Repositories))
	for _, repository := range sub.Repositories {
		member[repository] = true
	}
	narrowed := query
	narrowed.Repositories = nil
	for _, repository := range query.Repositories {
		if member[repository] {
			narrowed.Repositories = append(narrowed.Repositories, repository)
		}
	}
	sort.Slice(narrowed.Repositories, func(i, j int) bool { return narrowed.Repositories[i] < narrowed.Repositories[j] })
	if len(sub.Kinds) > 0 {
		if len(query.Kinds) == 0 {
			narrowed.Kinds = append([]string(nil), sub.Kinds...)
		} else {
			allowed := make(map[string]bool, len(sub.Kinds))
			for _, kind := range sub.Kinds {
				allowed[kind] = true
			}
			narrowed.Kinds = nil
			for _, kind := range query.Kinds {
				if allowed[kind] {
					narrowed.Kinds = append(narrowed.Kinds, kind)
				}
			}
			if len(narrowed.Kinds) == 0 {
				// Empty query kinds mean all kinds to the reader. An empty
				// intersection must instead take the existing no-scope path.
				narrowed.Repositories = nil
			}
		}
	}
	return narrowed
}
