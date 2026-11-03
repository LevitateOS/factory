// Package github owns GitHub REST translation. Credentials remain in native
// custody; reading a GitHub record grants no Factory execution authority.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	sdk "github.com/google/go-github/v93/github"
)

const APIVersion = "2026-03-10"
const timeout = 8 * time.Second
const responseLimit = 8 << 20

var (
	ErrInvalidRequest   = errors.New("invalid GitHub read request")
	ErrCredentials      = errors.New("GitHub sign-in is required")
	ErrNotVisible       = errors.New("GitHub resource is not visible")
	ErrForbidden        = errors.New("GitHub permission is unavailable")
	ErrRateLimited      = errors.New("GitHub rate limit reached")
	ErrRedirect         = errors.New("GitHub resource moved; resolve its stable identity")
	ErrUnavailable      = errors.New("GitHub is unavailable")
	ErrInvalidResponse  = errors.New("GitHub returned an invalid read response")
	ErrResponseTooLarge = errors.New("GitHub read response exceeds the limit")
	ErrDiffChanged      = errors.New("GitHub pull diff changed")
	ErrFilesTooLarge    = errors.New("GitHub pull exceeds the file listing limit")
	ErrChecksChanged    = errors.New("GitHub pull check head changed")
)

// Failure retains status/retry facts without provider prose, bodies or URL errors.
type Failure struct {
	Kind       error
	Status     int
	RetryAfter time.Duration
	Reset      time.Time
}

func (e *Failure) Error() string { return e.Kind.Error() }
func (e *Failure) Unwrap() error { return e.Kind }

// PageInfo describes one provider page, not an atomic repository catalogue.
// NextPage is a number, never an arbitrary Link URL passed to credential custody.
type PageInfo struct {
	NextPage   int
	LastPage   int
	Remaining  int
	Reset      time.Time
	ETag       string
	APIVersion string
}

type Client struct{ sdk *sdk.Client }

// New requires a trusted native transport. There is no default credential,
// token argument, environment lookup or implicit anonymous fallback. The native
// consumer must freshly admit its connection and honor request cancellation.
// This client is not exposed as a generic webview HTTP bridge.
func New(native http.RoundTripper) (*Client, error) {
	if native == nil {
		return nil, ErrCredentials
	}
	client, err := sdk.NewClient(sdk.WithHTTPClient(&http.Client{
		Transport: readTransport{native}, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}), sdk.WithUserAgent("soda-factory"))
	if err != nil {
		return nil, ErrUnavailable
	}
	return &Client{sdk: client}, nil
}

type readTransport struct{ native http.RoundTripper }

func (t readTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	u := request.URL
	if request.Method != http.MethodGet || request.Body != nil && request.Body != http.NoBody || u == nil || u.Scheme != "https" || u.Host != "api.github.com" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || !strings.HasPrefix(u.Path, "/") {
		return nil, ErrInvalidRequest
	}
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	request = request.Clone(request.Context())
	request.Header.Set("X-GitHub-Api-Version", APIVersion)
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := t.native.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response == nil || response.Body == nil {
		return nil, ErrInvalidResponse
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return nil, ErrUnavailable
	}
	if len(body) > responseLimit {
		return nil, ErrResponseTooLarge
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 && (!json.Valid(body) || bytes.Equal(bytes.TrimSpace(body), []byte("null"))) {
		return nil, ErrInvalidResponse
	}
	if err := request.Context().Err(); err != nil {
		return nil, err
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}

func result(ctx context.Context, response *sdk.Response, err error) (PageInfo, error) {
	info := PageInfo{}
	if response != nil {
		info = PageInfo{NextPage: response.NextPage, LastPage: response.LastPage, Remaining: response.Rate.Remaining, Reset: response.Rate.Reset.Time, ETag: response.Header.Get("ETag"), APIVersion: response.Header.Get("X-GitHub-Api-Version-Selected")}
	}
	if err == nil {
		return info, nil
	}
	if ctx.Err() != nil {
		return info, ctx.Err()
	}
	for _, kind := range []error{ErrInvalidRequest, ErrInvalidResponse, ErrResponseTooLarge} {
		if errors.Is(err, kind) {
			return info, kind
		}
	}
	failure := &Failure{Kind: ErrUnavailable, Reset: info.Reset}
	if response != nil {
		failure.Status = response.StatusCode
		if seconds, parseErr := strconv.ParseUint(response.Header.Get("Retry-After"), 10, 32); parseErr == nil {
			failure.RetryAfter = time.Duration(seconds) * time.Second
		}
		switch response.StatusCode {
		case http.StatusUnauthorized:
			failure.Kind = ErrCredentials
		case http.StatusNotFound:
			failure.Kind = ErrNotVisible
		case http.StatusForbidden:
			failure.Kind = ErrForbidden
		case http.StatusTooManyRequests:
			failure.Kind = ErrRateLimited
		case http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
			failure.Kind = ErrRedirect
		case http.StatusBadRequest, http.StatusUnprocessableEntity:
			failure.Kind = ErrInvalidRequest
		default:
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				failure.Kind = ErrInvalidResponse
			}
		}
	}
	var primary *sdk.RateLimitError
	var secondary *sdk.AbuseRateLimitError
	if errors.As(err, &primary) {
		failure.Kind, failure.Reset = ErrRateLimited, primary.Rate.Reset.Time
		if primary.Response != nil {
			failure.Status = primary.Response.StatusCode
		}
	}
	if errors.As(err, &secondary) {
		failure.Kind = ErrRateLimited
		if secondary.RetryAfter != nil {
			failure.RetryAfter = *secondary.RetryAfter
		}
		if secondary.Response != nil {
			failure.Status = secondary.Response.StatusCode
		}
	}
	return info, failure
}

// AuthenticatedUser observes /user, never a supplied username or public profile.
// Only a successful native credential-backed result can later bind an account.
func (c *Client) AuthenticatedUser(ctx context.Context) (*sdk.User, PageInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	user, response, err := c.sdk.Users.Get(ctx, "")
	info, err := result(ctx, response, err)
	if err != nil {
		return nil, info, err
	}
	if user == nil || user.GetID() < 1 || user.GetLogin() == "" {
		return nil, info, ErrInvalidResponse
	}
	return user, info, nil
}

func (c *Client) RepositoryByID(ctx context.Context, id int64) (*sdk.Repository, PageInfo, error) {
	if id < 1 {
		return nil, PageInfo{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	repo, response, err := c.sdk.Repositories.GetByID(ctx, id)
	info, err := result(ctx, response, err)
	if err != nil {
		return nil, info, err
	}
	if !validRepository(repo) || repo.GetID() != id {
		return nil, info, ErrInvalidResponse
	}
	return repo, info, nil
}

// Repository resolves an entered locator; callers retain the returned ID as
// identity. A mutable owner/name is never a substitute for that identity.
func (c *Client) Repository(ctx context.Context, owner, name string) (*sdk.Repository, PageInfo, error) {
	if !segment(owner) || !segment(name) {
		return nil, PageInfo{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	repo, response, err := c.sdk.Repositories.Get(ctx, owner, name)
	info, err := result(ctx, response, err)
	if err != nil {
		return nil, info, err
	}
	if !validRepository(repo) {
		return nil, info, ErrInvalidResponse
	}
	return repo, info, nil
}

// Repositories is the connected account's catalogue. It never falls back to a
// public user's catalogue after authentication or permission failure.
func (c *Client) Repositories(ctx context.Context, page, limit int) ([]*sdk.Repository, PageInfo, error) {
	if !pageQuery(page, limit) {
		return nil, PageInfo{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	repos, response, err := c.sdk.Repositories.ListByAuthenticatedUser(ctx, &sdk.RepositoryListByAuthenticatedUserOptions{Visibility: "all", Affiliation: "owner,collaborator,organization_member", Sort: "created", Direction: "asc", ListOptions: sdk.ListOptions{Page: page, PerPage: limit}})
	return repositoryPage(ctx, repos, response, err, limit)
}

// PublicRepositories is an explicit public lookup, never account admission.
func (c *Client) PublicRepositories(ctx context.Context, owner string, page, limit int) ([]*sdk.Repository, PageInfo, error) {
	if !segment(owner) || !pageQuery(page, limit) {
		return nil, PageInfo{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	repos, response, err := c.sdk.Repositories.ListByUser(ctx, owner, &sdk.RepositoryListByUserOptions{Sort: "created", Direction: "asc", ListOptions: sdk.ListOptions{Page: page, PerPage: limit}})
	return repositoryPage(ctx, repos, response, err, limit)
}
func repositoryPage(ctx context.Context, repos []*sdk.Repository, response *sdk.Response, err error, limit int) ([]*sdk.Repository, PageInfo, error) {
	info, err := result(ctx, response, err)
	if err != nil {
		return nil, info, err
	}
	if repos == nil || len(repos) > limit {
		return nil, info, ErrInvalidResponse
	}
	seen := map[int64]bool{}
	for _, repo := range repos {
		if !validRepository(repo) || seen[repo.GetID()] {
			return nil, info, ErrInvalidResponse
		}
		seen[repo.GetID()] = true
	}
	return repos, info, nil
}
func validRepository(repo *sdk.Repository) bool {
	return repo != nil && repo.GetID() > 0 && repo.GetFullName() != "" && repo.GetOwner().GetID() > 0
}
func pageQuery(page, limit int) bool {
	return page > 0 && page <= 1_000_000 && limit > 0 && limit <= 50
}
func segment(value string) bool {
	if value == "" || len(value) > 100 || value == "." || value == ".." {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}
