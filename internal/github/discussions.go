package github

import (
	"context"
	"strconv"
	"time"

	sdk "github.com/google/go-github/v93/github"
)

// Ordinary discussion only. Review submissions, inline threads and lifecycle
// events belong to separate readers. Identities here are provider-local facts.
type DiscussionSubject struct {
	Kind       string `json:"kind"`
	ExternalID string `json:"external_id"`
	Number     int    `json:"number"`
}
type CommentRecord struct {
	ExternalID      string      `json:"external_id"`
	Author          *IssueActor `json:"author"`
	Body            string      `json:"body"`
	CreatedAt       *time.Time  `json:"created_at"`
	UpdatedAt       *time.Time  `json:"updated_at"`
	MinimizedReason *string     `json:"minimized_reason"`
}
type DiscussionPage struct {
	Repository string            `json:"repository"`
	Name       string            `json:"name"`
	Subject    DiscussionSubject `json:"subject"`
	Items      []CommentRecord   `json:"items"`
	NextPage   int               `json:"next_page"`
}

// Discussion reads exactly one page of ordinary comments, including for a closed
// parent. Parent IDs come from the matching issue/pull endpoint, not route
// numbers or a PR's issue-wrapper ID. Post-I/O parent/repository checks must
// succeed before any body returns. The whole operation shares one deadline.
func (c *Client) Discussion(ctx context.Context, repository int64, kind string, number, page, limit int) (DiscussionPage, error) {
	if repository < 1 || number < 1 || kind != "issue" && kind != "pull" || !pageQuery(page, limit) {
		return DiscussionPage{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	owner, name, err := c.issueLocator(ctx, repository)
	if err != nil {
		return DiscussionPage{}, err
	}
	subject, err := c.discussionSubject(ctx, repository, owner, name, kind, number)
	if err != nil {
		return DiscussionPage{}, err
	}
	sort, direction := "created", "asc"
	comments, response, err := c.sdk.Issues.ListComments(ctx, owner, name, number, &sdk.IssueListCommentsOptions{
		Sort: &sort, Direction: &direction, ListOptions: sdk.ListOptions{Page: page, PerPage: limit},
	})
	info, err := result(ctx, response, err)
	if err != nil {
		return DiscussionPage{}, err
	}
	if comments == nil || len(comments) > limit || info.NextPage != 0 && (info.NextPage <= page || info.NextPage > 1_000_000) {
		return DiscussionPage{}, ErrInvalidResponse
	}
	result := DiscussionPage{Repository: strconv.FormatInt(repository, 10), Name: owner + "/" + name, Subject: subject, Items: make([]CommentRecord, 0, len(comments)), NextPage: info.NextPage}
	parentURL := "https://api.github.com/repos/" + owner + "/" + name + "/issues/" + strconv.Itoa(number)
	seen := map[int64]bool{}
	for _, comment := range comments {
		if comment == nil || comment.GetID() < 1 || seen[comment.GetID()] || comment.GetIssueURL() != parentURL ||
			comment.GetURL() != "https://api.github.com/repos/"+owner+"/"+name+"/issues/comments/"+strconv.FormatInt(comment.GetID(), 10) {
			return DiscussionPage{}, ErrInvalidResponse
		}
		seen[comment.GetID()] = true
		record := CommentRecord{ExternalID: strconv.FormatInt(comment.GetID(), 10), Body: comment.GetBody(), CreatedAt: issueTime(comment.CreatedAt), UpdatedAt: issueTime(comment.UpdatedAt)}
		if comment.User != nil {
			author, err := issueActor(comment.User)
			if err != nil {
				return DiscussionPage{}, err
			}
			record.Author = &author
		}
		if comment.Minimized != nil {
			record.MinimizedReason = comment.Minimized.Reason
		}
		result.Items = append(result.Items, record)
	}
	after, err := c.discussionSubject(ctx, repository, owner, name, kind, number)
	if err != nil {
		return DiscussionPage{}, err
	}
	if after != subject {
		return DiscussionPage{}, ErrNotVisible
	}
	if err := c.checkIssueLocator(ctx, repository, owner, name); err != nil {
		return DiscussionPage{}, err
	}
	return result, nil
}

func (c *Client) discussionSubject(ctx context.Context, repository int64, owner, name, kind string, number int) (DiscussionSubject, error) {
	if kind == "issue" {
		issue, response, err := c.sdk.Issues.Get(ctx, owner, name, number)
		if _, err := result(ctx, response, err); err != nil {
			return DiscussionSubject{}, err
		}
		if !validIssue(issue, owner, name) || issue.GetNumber() != number {
			return DiscussionSubject{}, ErrInvalidResponse
		}
		if issue.IsPullRequest() {
			return DiscussionSubject{}, ErrNotVisible
		}
		return DiscussionSubject{Kind: kind, ExternalID: strconv.FormatInt(issue.GetID(), 10), Number: number}, nil
	}
	pull, response, err := c.sdk.PullRequests.Get(ctx, owner, name, number)
	if _, err := result(ctx, response, err); err != nil {
		return DiscussionSubject{}, err
	}
	if pull == nil || pull.GetID() < 1 || pull.GetNumber() != number || pull.Base == nil || pull.Base.Repo == nil || pull.Base.Repo.GetID() != repository ||
		pull.GetURL() != "https://api.github.com/repos/"+owner+"/"+name+"/pulls/"+strconv.Itoa(number) {
		return DiscussionSubject{}, ErrInvalidResponse
	}
	return DiscussionSubject{Kind: kind, ExternalID: strconv.FormatInt(pull.GetID(), 10), Number: number}, nil
}
