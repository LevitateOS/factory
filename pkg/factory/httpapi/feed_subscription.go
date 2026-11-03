package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
	"github.com/levitateos/factory/pkg/strictjson"
)

// feedSubscriptionBody carries one subscription write: the scope and
// kinds with the revision the write expects. The subscriber is the
// host-admitted caller, never a body field.
type feedSubscriptionBody struct {
	Repositories []string `json:"repositories"`
	Kinds        []string `json:"kinds,omitempty"`
	Revision     int64    `json:"expected_revision"`
}

// parseFeedSubscriptionBody admits one subscription write for its
// caller, binding the subscriber and returning the expected revision.
func parseFeedSubscriptionBody(data []byte, subscriber string) (factory.FeedSubscription, int64, error) {
	var empty factory.FeedSubscription
	var body feedSubscriptionBody
	if err := json.Unmarshal(data, &body); err != nil {
		return empty, 0, factoryservice.ErrFeedQueryInvalid
	}
	if subscriber == "" {
		return empty, 0, factoryservice.ErrFeedQueryInvalid
	}
	sub := factory.FeedSubscription{Subscriber: subscriber, Kinds: body.Kinds, Revision: body.Revision + 1}
	for _, value := range body.Repositories {
		repository, err := strconv.ParseInt(value, 10, 64)
		if err != nil || repository <= 0 || strconv.FormatInt(repository, 10) != value {
			return empty, 0, factoryservice.ErrFeedQueryInvalid
		}
		sub.Repositories = append(sub.Repositories, repository)
	}
	if body.Revision < 0 {
		return empty, 0, factoryservice.ErrFeedQueryInvalid
	}
	if err := sub.Validate(); err != nil {
		return empty, 0, err
	}
	return sub, body.Revision, nil
}

// subscription reads and writes the caller's own
// combined-feed subscription: the scope and kind filter applied
// inside already permitted activity, never a grant.
func (s Feed) subscription(w http.ResponseWriter, r *http.Request) {
	subscriber := strconv.FormatInt(int64(s.Actor), 10)
	switch r.Method {
	case http.MethodGet:
		sub, revision, err := s.Service.Subscription(r.Context(), s.Actor)
		if err != nil {
			writeError(w, 503, "store_unavailable", "Could not read feed subscription.")
			return
		}
		// Keep the retained ledger's int64 representation; browser IDs travel
		// as decimal strings so every positive BIGINT remains exact.
		repositories := make([]string, 0, len(sub.Repositories))
		for _, repository := range sub.Repositories {
			repositories = append(repositories, strconv.FormatInt(repository, 10))
		}
		if s.current(w, r) {
			writeJSON(w, 200, map[string]any{"subscriber": subscriber, "repositories": repositories, "kinds": sub.Kinds, "revision": revision})
		}
	case http.MethodPut:
		var raw json.RawMessage
		if err := strictjson.Decode(http.MaxBytesReader(w, r.Body, 65536), &raw); err != nil {
			writeError(w, 400, "invalid_request", "Feed subscription body is invalid.")
			return
		}
		sub, expected, err := parseFeedSubscriptionBody(raw, subscriber)
		if err != nil {
			writeError(w, 400, "invalid_request", "Feed subscription body is invalid.")
			return
		}
		if !s.current(w, r) {
			return
		}
		if err := s.Service.SaveSubscription(r.Context(), s.Actor, expected, sub); err != nil {
			if err == store.ErrStaleRevision {
				writeError(w, 409, "stale_revision", "Feed subscription changed underneath the write.")
				return
			}
			writeError(w, 503, "store_unavailable", "Could not write feed subscription.")
			return
		}
		if s.current(w, r) {
			writeJSON(w, 200, map[string]any{"subscriber": subscriber, "revision": expected + 1})
		}
	default:
		writeError(w, 405, "invalid_request", "Feed subscription reads and writes only.")
	}
}
