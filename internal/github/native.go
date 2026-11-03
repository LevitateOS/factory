package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/strictjson"
)

const NativeCredentialHeader = "X-Soda-GitHub-Credential"

type nativeRead struct {
	Kind       string `json:"kind"`
	Account    string `json:"account"`
	Connection string `json:"connection"`
	Repository string `json:"repository,omitempty"`
	Page       int    `json:"page,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	State      string `json:"state,omitempty"`
	Number     int    `json:"number,omitempty"`
	Base       string `json:"base,omitempty"`
	Head       string `json:"head,omitempty"`
	ReviewID   string `json:"review_id,omitempty"`
	CommentID  string `json:"comment_id,omitempty"`
}

// NewNativeReadHandler belongs behind the native host's peer/key/instance and
// admission lifetime gates. It neither binds a Factory account nor stores a
// credential. The native host supplies the token for this one bounded read.
func NewNativeReadHandler(network http.RoundTripper) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		fail := func(status int, code string) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
		}
		if network == nil {
			fail(503, "github_unavailable")
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/native/github/read" || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
			fail(400, "invalid_native_read")
			return
		}
		values := r.Header.Values(NativeCredentialHeader)
		if len(values) != 1 || len(values[0]) < 1 || len(values[0]) > 8192 {
			fail(401, "credential_required")
			return
		}
		for _, b := range []byte(values[0]) {
			if b < 33 || b > 126 {
				fail(401, "credential_required")
				return
			}
		}
		token := []byte(values[0])
		r.Header.Del(NativeCredentialHeader)
		defer clear(token)
		var query nativeRead
		if strictjson.Decode(http.MaxBytesReader(w, r.Body, 4096), &query) != nil {
			fail(400, "invalid_native_read")
			return
		}
		discussion := query.Kind == "issue_comments" || query.Kind == "pull_comments"
		files := query.Kind == "pull_files"
		reviews := query.Kind == "pull_reviews" || query.Kind == "pull_review" || query.Kind == "pull_review_comments" || query.Kind == "pull_review_comment"
		checks := query.Kind == "pull_check_runs" || query.Kind == "pull_commit_statuses"
		numbered := query.Kind == "issue" || query.Kind == "pull" || query.Kind == "pull_diff" || files || reviews || checks || discussion
		paged := query.Kind == "issues" || query.Kind == "pulls" || files || query.Kind == "pull_reviews" || query.Kind == "pull_review_comments" || checks || discussion
		stateful := query.Kind == "issues" || query.Kind == "pulls"
		if !stateful && query.State != "" || !numbered && query.Number != 0 {
			fail(400, "invalid_native_read")
			return
		}
		var review int64
		if query.Kind == "pull_review" {
			parsed, parseErr := strconv.ParseInt(query.ReviewID, 10, 64)
			if parseErr != nil || parsed < 1 || strconv.FormatInt(parsed, 10) != query.ReviewID {
				fail(400, "invalid_native_read")
				return
			}
			review = parsed
		} else if query.ReviewID != "" {
			fail(400, "invalid_native_read")
			return
		}
		var comment int64
		if query.Kind == "pull_review_comment" {
			parsed, parseErr := strconv.ParseInt(query.CommentID, 10, 64)
			if parseErr != nil || parsed < 1 || strconv.FormatInt(parsed, 10) != query.CommentID {
				fail(400, "invalid_native_read")
				return
			}
			comment = parsed
		} else if query.CommentID != "" {
			fail(400, "invalid_native_read")
			return
		}
		if query.Kind == "pull_diff" && !validDiffPair(query.Base, query.Head) || files && !validFilesQuery(query.Page, query.Limit, query.Base, query.Head) || checks && (query.Base != "" || !validCheckHeadQuery(query.Page, query.Limit, query.Head)) || query.Kind != "pull_diff" && !files && !checks && (query.Base != "" || query.Head != "") {
			fail(400, "invalid_native_read")
			return
		}
		var account int64
		var err error
		if query.Kind != "verify" {
			account, err = strconv.ParseInt(query.Account, 10, 64)
			if err != nil || account < 1 || strconv.FormatInt(account, 10) != query.Account || !collaboration.ValidID(query.Connection) {
				fail(400, "invalid_account")
				return
			}
		}
		var repository int64
		switch query.Kind {
		case "verify":
			// Bootstrap observes /user with an unpersisted native token. It must
			// not accept a claimed account/connection or read a repository.
			if query.Account != "" || query.Connection != "" || query.Repository != "" || query.Page != 0 || query.Limit != 0 {
				fail(400, "invalid_native_read")
				return
			}
		case "identity":
			if query.Repository != "" || query.Page != 0 || query.Limit != 0 {
				fail(400, "invalid_native_read")
				return
			}
		case "repositories":
			if query.Repository != "" || !pageQuery(query.Page, query.Limit) {
				fail(400, "invalid_native_read")
				return
			}
		case "repository", "issues", "issue", "pulls", "pull", "pull_diff", "pull_files", "pull_reviews", "pull_review", "pull_review_comments", "pull_review_comment", "pull_check_runs", "pull_commit_statuses", "issue_comments", "pull_comments":
			repository, err = strconv.ParseInt(query.Repository, 10, 64)
			if err != nil || repository < 1 || strconv.FormatInt(repository, 10) != query.Repository ||
				paged && !pageQuery(query.Page, query.Limit) || !paged && (query.Page != 0 || query.Limit != 0) || numbered && query.Number < 1 ||
				stateful && query.State != "" && query.State != "open" && query.State != "closed" && query.State != "all" {
				fail(400, "invalid_native_read")
				return
			}
		default:
			fail(400, "invalid_native_read")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
		defer cancel()
		client, err := New(borrowedTransport{network: network, token: token})
		if err != nil {
			fail(503, "github_unavailable")
			return
		}
		providerError := func(err error) {
			status, code := 503, "github_unavailable"
			switch {
			case errors.Is(err, ErrCredentials):
				status, code = 401, "github_sign_in_required"
			case errors.Is(err, ErrNotVisible):
				status, code = 404, "github_not_visible"
			case errors.Is(err, ErrForbidden):
				status, code = 403, "github_forbidden"
			case errors.Is(err, ErrRateLimited):
				status, code = 429, "github_rate_limited"
			case errors.Is(err, ErrRedirect):
				status, code = 409, "github_resource_moved"
			case errors.Is(err, ErrDiffChanged):
				status, code = 409, "github_diff_changed"
			case errors.Is(err, ErrFilesTooLarge):
				status, code = 413, "github_files_too_large"
			case errors.Is(err, ErrChecksChanged):
				status, code = 409, "github_checks_changed"
			case errors.Is(err, ErrInvalidRequest):
				status, code = 400, "invalid_native_read"
			case errors.Is(err, context.DeadlineExceeded):
				status, code = 504, "github_deadline"
			}
			fail(status, code)
		}
		user, _, err := client.AuthenticatedUser(ctx)
		if err != nil {
			providerError(err)
			return
		}
		if query.Kind != "verify" && user.GetID() != account {
			fail(409, "github_account_changed")
			return
		}
		result := map[string]any{"kind": query.Kind, "account": map[string]string{"external_id": strconv.FormatInt(user.GetID(), 10), "login": user.GetLogin()}}
		if query.Kind != "verify" {
			result["connection"] = query.Connection
		}
		snapshot := func(id int64, name string, private bool, branch string) map[string]any {
			return map[string]any{"external_id": strconv.FormatInt(id, 10), "name": name, "private": private, "default_branch": branch}
		}
		switch query.Kind {
		case "repositories":
			repos, page, err := client.Repositories(ctx, query.Page, query.Limit)
			if err != nil {
				providerError(err)
				return
			}
			items := make([]map[string]any, 0, len(repos))
			for _, repo := range repos {
				items = append(items, snapshot(repo.GetID(), repo.GetFullName(), repo.GetPrivate(), repo.GetDefaultBranch()))
			}
			result["items"], result["next_page"] = items, page.NextPage
		case "repository":
			repo, _, err := client.RepositoryByID(ctx, repository)
			if err != nil {
				providerError(err)
				return
			}
			result["repository"] = snapshot(repo.GetID(), repo.GetFullName(), repo.GetPrivate(), repo.GetDefaultBranch())
		case "issues", "issue":
			var issues IssuePage
			if query.Kind == "issues" {
				issues, err = client.IssueList(ctx, repository, query.Page, query.Limit, query.State)
			} else {
				issues, err = client.Issue(ctx, repository, query.Number)
			}
			if err != nil {
				providerError(err)
				return
			}
			result["issues"] = issues
		case "pulls", "pull":
			var pulls PullPage
			if query.Kind == "pulls" {
				pulls, err = client.PullList(ctx, repository, query.Page, query.Limit, query.State)
			} else {
				pulls, err = client.Pull(ctx, repository, query.Number)
			}
			if err != nil {
				providerError(err)
				return
			}
			result["pulls"] = pulls
		case "pull_diff":
			diff, err := client.PullDiff(ctx, repository, query.Number, query.Base, query.Head)
			if err != nil {
				providerError(err)
				return
			}
			result["diff"] = diff
		case "pull_files":
			files, err := client.PullFiles(ctx, repository, query.Number, query.Page, query.Limit, query.Base, query.Head)
			if err != nil {
				providerError(err)
				return
			}
			result["files"] = files
		case "pull_reviews", "pull_review":
			var reviews ReviewPage
			if query.Kind == "pull_reviews" {
				reviews, err = client.PullReviews(ctx, repository, query.Number, query.Page, query.Limit)
			} else {
				reviews, err = client.PullReview(ctx, repository, query.Number, review)
			}
			if err != nil {
				providerError(err)
				return
			}
			result["reviews"] = reviews
		case "pull_check_runs":
			checks, err := client.PullCheckRuns(ctx, repository, query.Number, query.Page, query.Limit, query.Head)
			if err != nil {
				providerError(err)
				return
			}
			result["checks"] = checks
		case "pull_review_comments", "pull_review_comment":
			var comments ReviewCommentPage
			var err error
			if query.Kind == "pull_review_comment" {
				comments, err = client.PullReviewComment(ctx, repository, query.Number, comment)
			} else {
				comments, err = client.PullReviewComments(ctx, repository, query.Number, query.Page, query.Limit)
			}
			if err != nil {
				providerError(err)
				return
			}
			result["review_comments"] = comments
		case "pull_commit_statuses":
			statuses, err := client.PullCommitStatuses(ctx, repository, query.Number, query.Page, query.Limit, query.Head)
			if err != nil {
				providerError(err)
				return
			}
			result["statuses"] = statuses
		case "issue_comments", "pull_comments":
			kind := "issue"
			if query.Kind == "pull_comments" {
				kind = "pull"
			}
			discussion, err := client.Discussion(ctx, repository, kind, query.Number, query.Page, query.Limit)
			if err != nil {
				providerError(err)
				return
			}
			result["discussion"] = discussion
		}
		if ctx.Err() != nil {
			providerError(ctx.Err())
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	})
}

// The borrowed header lives only in this request's native call. No SDK client,
// token source, retry worker, environment or backend configuration retains it.
type borrowedTransport struct {
	network http.RoundTripper
	token   []byte
}

func (t borrowedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Context().Err() != nil {
		return nil, r.Context().Err()
	}
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(t.token))
	return t.network.RoundTrip(r)
}
