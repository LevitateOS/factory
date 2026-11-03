package github

import (
	"context"
	"strconv"
	"strings"
	"time"

	sdk "github.com/google/go-github/v93/github"
)

// These are provider-local observations. The connection consumer maps remote
// identities through the canonical registry before producing product records.
// No permissions, execution grants or mutation preconditions are inferred.
type IssueActor struct {
	ExternalID string `json:"external_id"`
	Login      string `json:"login"`
}
type IssueLabel struct {
	ExternalID string `json:"external_id"`
	Name       string `json:"name"`
	Color      string `json:"color"`
}
type IssueRecord struct {
	ExternalID string       `json:"external_id"`
	Number     int          `json:"number"`
	Title      string       `json:"title"`
	Body       string       `json:"body"`
	State      string       `json:"state"`
	Locked     *bool        `json:"locked"`
	Author     *IssueActor  `json:"author"`
	Assignees  []IssueActor `json:"assignees"`
	Labels     []IssueLabel `json:"labels"`
	CreatedAt  *time.Time   `json:"created_at"`
	UpdatedAt  *time.Time   `json:"updated_at"`
	ClosedAt   *time.Time   `json:"closed_at"`
}
type IssuePage struct {
	Repository string        `json:"repository"`
	Name       string        `json:"name"`
	Items      []IssueRecord `json:"items"`
	NextPage   int           `json:"next_page"`
}

// IssueList makes one provider page, not a scan to fill a UI page. GitHub mixes
// PRs into this endpoint; filtering may leave an empty page with a continuation.
// Stable ID lookup plus a final locator check refuse name reuse/rename and loss
// of repository visibility instead of releasing another repository's issues.
func (c *Client) IssueList(ctx context.Context, repository int64, page, limit int, state string) (IssuePage, error) {
	if repository < 1 || !pageQuery(page, limit) || state != "" && state != "open" && state != "closed" && state != "all" {
		return IssuePage{}, ErrInvalidRequest
	}
	if state == "" {
		state = "open"
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	owner, name, err := c.issueLocator(ctx, repository)
	if err != nil {
		return IssuePage{}, err
	}
	issues, response, err := c.sdk.Issues.ListByRepo(ctx, owner, name, &sdk.IssueListByRepoOptions{
		State: state, Sort: "created", Direction: "asc", ListOptions: sdk.ListOptions{Page: page, PerPage: limit},
	})
	info, err := result(ctx, response, err)
	if err != nil {
		return IssuePage{}, err
	}
	if issues == nil || len(issues) > limit || info.NextPage != 0 && (info.NextPage <= page || info.NextPage > 1_000_000) {
		return IssuePage{}, ErrInvalidResponse
	}
	result := IssuePage{Repository: strconv.FormatInt(repository, 10), Name: owner + "/" + name, Items: make([]IssueRecord, 0, len(issues)), NextPage: info.NextPage}
	seen := map[int64]bool{}
	numbers := map[int]bool{}
	for _, issue := range issues {
		if !validIssue(issue, owner, name) || seen[issue.GetID()] || numbers[issue.GetNumber()] || state != "all" && issue.GetState() != state {
			return IssuePage{}, ErrInvalidResponse
		}
		seen[issue.GetID()], numbers[issue.GetNumber()] = true, true
		if issue.IsPullRequest() {
			continue
		}
		record, err := issueRecord(issue)
		if err != nil {
			return IssuePage{}, err
		}
		result.Items = append(result.Items, record)
	}
	if err := c.checkIssueLocator(ctx, repository, owner, name); err != nil {
		return IssuePage{}, err
	}
	return result, nil
}

// Issue reads any-state detail. A PR's issue wrapper is not a normal issue and
// refuses; PR IDs must come from the separate pull endpoint/adapter.
func (c *Client) Issue(ctx context.Context, repository int64, number int) (IssuePage, error) {
	if repository < 1 || number < 1 {
		return IssuePage{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	owner, name, err := c.issueLocator(ctx, repository)
	if err != nil {
		return IssuePage{}, err
	}
	issue, response, err := c.sdk.Issues.Get(ctx, owner, name, number)
	if _, err := result(ctx, response, err); err != nil {
		return IssuePage{}, err
	}
	if !validIssue(issue, owner, name) || issue.GetNumber() != number {
		return IssuePage{}, ErrInvalidResponse
	}
	if issue.IsPullRequest() {
		return IssuePage{}, ErrNotVisible
	}
	record, err := issueRecord(issue)
	if err != nil {
		return IssuePage{}, err
	}
	if err := c.checkIssueLocator(ctx, repository, owner, name); err != nil {
		return IssuePage{}, err
	}
	return IssuePage{Repository: strconv.FormatInt(repository, 10), Name: owner + "/" + name, Items: []IssueRecord{record}}, nil
}

func (c *Client) issueLocator(ctx context.Context, repository int64) (string, string, error) {
	repo, _, err := c.RepositoryByID(ctx, repository)
	if err != nil {
		return "", "", err
	}
	owner, name, ok := strings.Cut(repo.GetFullName(), "/")
	if !ok || !segment(owner) || !segment(name) {
		return "", "", ErrInvalidResponse
	}
	return owner, name, nil
}
func (c *Client) checkIssueLocator(ctx context.Context, repository int64, owner, name string) error {
	repo, _, err := c.Repository(ctx, owner, name)
	if err != nil {
		return err
	}
	if repo.GetID() != repository {
		return ErrNotVisible
	}
	return ctx.Err()
}
func validIssue(issue *sdk.Issue, owner, name string) bool {
	return issue != nil && issue.GetID() > 0 && issue.GetNumber() > 0 && issue.Title != nil &&
		(issue.GetState() == "open" || issue.GetState() == "closed") &&
		issue.GetRepositoryURL() == "https://api.github.com/repos/"+owner+"/"+name
}
func issueRecord(issue *sdk.Issue) (IssueRecord, error) {
	record := IssueRecord{ExternalID: strconv.FormatInt(issue.GetID(), 10), Number: issue.GetNumber(), Title: issue.GetTitle(), Body: issue.GetBody(), State: issue.GetState(), Locked: issue.Locked, CreatedAt: issueTime(issue.CreatedAt), UpdatedAt: issueTime(issue.UpdatedAt), ClosedAt: issueTime(issue.ClosedAt)}
	if issue.Assignees != nil {
		record.Assignees = []IssueActor{}
	}
	if issue.Labels != nil {
		record.Labels = []IssueLabel{}
	}
	if issue.User != nil {
		author, err := issueActor(issue.User)
		if err != nil {
			return IssueRecord{}, err
		}
		record.Author = &author
	}
	for _, user := range issue.Assignees {
		actor, err := issueActor(user)
		if err != nil {
			return IssueRecord{}, err
		}
		record.Assignees = append(record.Assignees, actor)
	}
	for _, label := range issue.Labels {
		if label == nil || label.GetID() < 1 || label.GetName() == "" {
			return IssueRecord{}, ErrInvalidResponse
		}
		record.Labels = append(record.Labels, IssueLabel{ExternalID: strconv.FormatInt(label.GetID(), 10), Name: label.GetName(), Color: label.GetColor()})
	}
	return record, nil
}
func issueActor(user *sdk.User) (IssueActor, error) {
	if user == nil || user.GetID() < 1 || user.GetLogin() == "" {
		return IssueActor{}, ErrInvalidResponse
	}
	return IssueActor{ExternalID: strconv.FormatInt(user.GetID(), 10), Login: user.GetLogin()}, nil
}
func issueTime(value *sdk.Timestamp) *time.Time {
	if value == nil {
		return nil
	}
	return &value.Time
}
