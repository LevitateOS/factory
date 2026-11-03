package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/levitateos/sodaos/internal/factory"
	factoryservice "github.com/levitateos/sodaos/internal/factory/service"
	"github.com/levitateos/sodaos/internal/strictjson"
)

// parseFeedPostBody admits one comment POST body: scope, bounded
// text and idempotency key. The author is the host-admitted caller.
func parseFeedPostBody(data []byte) (factory.PostRequest, error) {
	var req factory.PostRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return factory.PostRequest{}, factoryservice.ErrFeedQueryInvalid
	}
	if err := req.Validate(); err != nil {
		return factory.PostRequest{}, err
	}
	return req, nil
}

// submitPost adapts one admitted durable submission. Only background
// continuation judges, authors or applies effects.
func (s Feed) submitPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "invalid_request", "Feed posts use POST only.")
		return
	}
	var raw json.RawMessage
	if err := strictjson.Decode(http.MaxBytesReader(w, r.Body, 65536), &raw); err != nil {
		writeError(w, 400, "invalid_request", "Feed post body is invalid.")
		return
	}
	req, err := parseFeedPostBody(raw)
	if err != nil {
		writeError(w, 400, "invalid_request", "Feed post body is invalid.")
		return
	}
	if !s.current(w, r) {
		return
	}
	response, err := s.Commands.Submit(r.Context(), s.Actor, s.AuthorName, req)
	if err != nil {
		switch {
		case errors.Is(err, factoryservice.ErrPostInputInvalid):
			writeError(w, 400, "invalid_request", "Feed post body is invalid.")
		case errors.Is(err, factoryservice.ErrPostVisibility):
			writeError(w, 404, "repository_not_found", "Repository is not visible.")
		case errors.Is(err, factoryservice.ErrPostSourceNotFound):
			writeError(w, 404, "source_not_found", "The parent post is not visible.")
		default:
			writeError(w, 503, "store_unavailable", "Could not save the feed post request.")
		}
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, response)
	}
}

// readPost serves stored post/record facts through current
// visibility. Reads do not fund, judge, draft or apply effects.
func (s Feed) readPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, "invalid_request", "Feed post reads only.")
		return
	}
	view, err := s.Posts.Read(r.Context(), s.Actor, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, factoryservice.ErrPostNotFound) || errors.Is(err, factoryservice.ErrPostVisibility) {
			writeError(w, 404, "comment_not_found", "Feed post is not found.")
		} else {
			writeError(w, 503, "store_unavailable", "Could not read the post request.")
		}
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, view)
	}
}

func (s Feed) replies(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repository, err := strconv.ParseInt(q.Get("repository"), 10, 64)
	if err != nil || repository <= 0 {
		writeError(w, 400, "invalid_request", "Choose a project.")
		return
	}
	var before int64
	if raw := q.Get("before"); raw != "" {
		before, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || before <= 0 {
			writeError(w, 400, "invalid_cursor", "Refresh the conversation.")
			return
		}
	}
	result, err := s.Posts.Replies(r.Context(), s.Actor, repository, r.PathValue("id"), before)
	if err != nil {
		if errors.Is(err, factoryservice.ErrPostNotFound) || errors.Is(err, factoryservice.ErrPostVisibility) {
			writeError(w, 404, "source_not_found", "Conversation is not visible.")
		} else {
			writeError(w, 503, "store_unavailable", "Could not read replies.")
		}
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, result)
	}
}

func (s Feed) retryPost(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CommandID        string `json:"command_id"`
		Repository       string `json:"repository"`
		ExpectedRevision *int64 `json:"expected_revision"`
		Body             string `json:"body"`
	}
	if !decodeObject(w, r, &in) || !s.current(w, r) {
		return
	}
	repository, err := strconv.ParseInt(in.Repository, 10, 64)
	if err != nil || repository <= 0 || strconv.FormatInt(repository, 10) != in.Repository || in.ExpectedRevision == nil {
		writeError(w, 400, "invalid_request", "Choose a canonical repository and expected revision.")
		return
	}
	response, err := s.Commands.Retry(r.Context(), s.Actor, r.PathValue("id"), factory.FeedPostRetryRequest{CommandID: in.CommandID, Repository: repository, ExpectedRevision: *in.ExpectedRevision, Body: in.Body})
	s.retryAnswer(w, r, response, err)
}

func (s Feed) retryReceipt(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("repository")
	repository, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || repository <= 0 || strconv.FormatInt(repository, 10) != raw {
		writeError(w, 400, "invalid_request", "Choose a canonical repository.")
		return
	}
	response, err := s.Commands.RetryReceipt(r.Context(), s.Actor, r.PathValue("id"), r.PathValue("command"), repository)
	s.retryAnswer(w, r, response, err)
}

func (s Feed) retryAnswer(w http.ResponseWriter, r *http.Request, response factory.FeedPostRetryReceipt, err error) {
	if err != nil {
		switch {
		case errors.Is(err, factoryservice.ErrPostInputInvalid):
			writeError(w, 400, "invalid_request", "Post retry scope or body is invalid.")
		case errors.Is(err, factoryservice.ErrPostNotFound), errors.Is(err, factoryservice.ErrPostVisibility):
			writeError(w, 404, "comment_not_found", "Post not found.")
		case errors.Is(err, factoryservice.ErrPostAuthorRequired):
			writeError(w, 403, "comment_author_required", "Only the author can revise this request.")
		case errors.Is(err, factoryservice.ErrPostRetryConflict):
			writeError(w, 409, "comment_retry_conflict", err.Error())
		default:
			writeError(w, 503, "store_unavailable", "Could not read the updated request.")
		}
		return
	}
	if s.current(w, r) {
		writeJSON(w, 200, response)
	}
}

// decodeObject decodes strict bounded JSON without host authentication.
func decodeObject(w http.ResponseWriter, r *http.Request, target any) bool {
	err := strictjson.Decode(http.MaxBytesReader(w, r.Body, 65536), target)
	if err == nil {
		return true
	}
	var oversized *http.MaxBytesError
	if errors.As(err, &oversized) {
		writeError(w, 413, "body_too_large", "Request exceeds 64 KiB.")
	} else {
		writeError(w, 400, "invalid_json", "JSON fields or values are invalid.")
	}
	return false
}
