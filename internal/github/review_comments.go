package github

import (
	"context"
	"strconv"
	"time"

	sdk "github.com/google/go-github/v93/github"
	"github.com/levitateos/sodaos/internal/collaboration"
)

// ReviewCommentRecord retains raw inline-review evidence. A reply may reference
// a comment outside this page. OriginalCommit is a review commit, NOT a diff
// base; nullable positions do not by themselves prove outdatedness/resolution.
type ReviewCommentRecord struct {
	ExternalID        string      `json:"external_id"`
	ReviewID          *string     `json:"review_id"`
	ReplyTo           *string     `json:"reply_to"`
	Author            *IssueActor `json:"author"`
	AuthorAssociation *string     `json:"author_association"`
	Body              *string     `json:"body"`
	Path              *string     `json:"path"`
	DiffHunk          *string     `json:"diff_hunk"`
	Commit            *string     `json:"commit"`
	OriginalCommit    *string     `json:"original_commit"`
	Line              *int        `json:"line"`
	StartLine         *int        `json:"start_line"`
	OriginalLine      *int        `json:"original_line"`
	OriginalStartLine *int        `json:"original_start_line"`
	Side              *string     `json:"side"`
	StartSide         *string     `json:"start_side"`
	Position          *int        `json:"position"`
	OriginalPosition  *int        `json:"original_position"`
	SubjectType       *string     `json:"subject_type"`
	HTMLURL           *string     `json:"html_url"`
	CreatedAt         *time.Time  `json:"created_at"`
	UpdatedAt         *time.Time  `json:"updated_at"`
}

type ReviewCommentPage struct {
	Repository string                `json:"repository"`
	Name       string                `json:"name"`
	Pull       DiscussionSubject     `json:"pull"`
	Items      []ReviewCommentRecord `json:"items"`
	NextPage   int                   `json:"next_page"`
}

func (c *Client) PullReviewComments(ctx context.Context, repository int64, number, page, limit int) (ReviewCommentPage, error) {
	if repository < 1 || number < 1 || !pageQuery(page, limit) {
		return ReviewCommentPage{}, ErrInvalidRequest
	}
	return c.pullReviewComments(ctx, repository, number, page, limit, 0)
}
func (c *Client) PullReviewComment(ctx context.Context, repository int64, number int, comment int64) (ReviewCommentPage, error) {
	if repository < 1 || number < 1 || comment < 1 {
		return ReviewCommentPage{}, ErrInvalidRequest
	}
	return c.pullReviewComments(ctx, repository, number, 0, 1, comment)
}

func (c *Client) pullReviewComments(ctx context.Context, repository int64, number, page, limit int, comment int64) (ReviewCommentPage, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	owner, name, err := c.issueLocator(ctx, repository)
	if err != nil {
		return ReviewCommentPage{}, err
	}
	parent, err := c.discussionSubject(ctx, repository, owner, name, "pull", number)
	if err != nil {
		return ReviewCommentPage{}, err
	}
	var items []*sdk.PullRequestComment
	var response *sdk.Response
	if comment == 0 {
		items, response, err = c.sdk.PullRequests.ListComments(ctx, owner, name, number, &sdk.PullRequestListCommentsOptions{Sort: "created", Direction: "asc", ListOptions: sdk.ListOptions{Page: page, PerPage: limit}})
	} else {
		var item *sdk.PullRequestComment
		item, response, err = c.sdk.PullRequests.GetComment(ctx, owner, name, comment)
		items = []*sdk.PullRequestComment{item}
	}
	info, err := result(ctx, response, err)
	if err != nil {
		return ReviewCommentPage{}, err
	}
	if items == nil || len(items) > limit || info.NextPage != 0 && (comment != 0 || len(items) == 0 || info.NextPage <= page || info.NextPage > 1_000_000) {
		return ReviewCommentPage{}, ErrInvalidResponse
	}
	r := ReviewCommentPage{Repository: strconv.FormatInt(repository, 10), Name: owner + "/" + name, Pull: parent, Items: make([]ReviewCommentRecord, 0, len(items)), NextPage: info.NextPage}
	seen := map[int64]bool{}
	for _, item := range items {
		if item == nil || item.GetID() < 1 || seen[item.GetID()] || comment != 0 && item.GetID() != comment || item.GetURL() != "https://api.github.com/repos/"+owner+"/"+name+"/pulls/comments/"+strconv.FormatInt(item.GetID(), 10) {
			return ReviewCommentPage{}, ErrInvalidResponse
		}
		if item.GetPullRequestURL() != "https://api.github.com/repos/"+owner+"/"+name+"/pulls/"+strconv.Itoa(number) {
			return ReviewCommentPage{}, ErrNotVisible
		}
		seen[item.GetID()] = true
		record := ReviewCommentRecord{ExternalID: strconv.FormatInt(item.GetID(), 10), Body: item.Body, Path: item.Path, DiffHunk: item.DiffHunk, Commit: item.CommitID, OriginalCommit: item.OriginalCommitID,
			Line: item.Line, StartLine: item.StartLine, OriginalLine: item.OriginalLine, OriginalStartLine: item.OriginalStartLine, Side: item.Side, StartSide: item.StartSide, Position: item.Position, OriginalPosition: item.OriginalPosition, SubjectType: item.SubjectType, AuthorAssociation: item.AuthorAssociation, HTMLURL: item.HTMLURL, CreatedAt: issueTime(item.CreatedAt), UpdatedAt: issueTime(item.UpdatedAt)}
		for _, commit := range []*string{record.Commit, record.OriginalCommit} {
			if commit != nil && !collaboration.ValidCommitID(collaboration.CommitID(*commit)) {
				return ReviewCommentPage{}, ErrInvalidResponse
			}
		}
		for _, line := range []*int{record.Line, record.StartLine, record.OriginalLine, record.OriginalStartLine} {
			if line != nil && *line < 1 {
				return ReviewCommentPage{}, ErrInvalidResponse
			}
		}
		for _, position := range []*int{record.Position, record.OriginalPosition} {
			if position != nil && *position < 0 {
				return ReviewCommentPage{}, ErrInvalidResponse
			}
		}
		for _, pair := range []struct {
			native *int64
			local  **string
		}{{item.PullRequestReviewID, &record.ReviewID}, {item.InReplyTo, &record.ReplyTo}} {
			if pair.native != nil {
				if *pair.native < 1 {
					return ReviewCommentPage{}, ErrInvalidResponse
				}
				value := strconv.FormatInt(*pair.native, 10)
				*pair.local = &value
			}
		}
		if item.User != nil {
			author, err := issueActor(item.User)
			if err != nil {
				return ReviewCommentPage{}, err
			}
			record.Author = &author
		}
		r.Items = append(r.Items, record)
	}
	after, err := c.discussionSubject(ctx, repository, owner, name, "pull", number)
	if err != nil {
		return ReviewCommentPage{}, err
	}
	if after != parent {
		return ReviewCommentPage{}, ErrInvalidResponse
	}
	if err := c.checkIssueLocator(ctx, repository, owner, name); err != nil {
		return ReviewCommentPage{}, err
	}
	return r, nil
}
