package github

import (
	"context"
	"strconv"
	"time"

	sdk "github.com/google/go-github/v93/github"
	"github.com/levitateos/factory/pkg/collaboration"
)

// Review submissions are distinct from ordinary comments and inline threads.
// These are provider-local facts; no canonical identity or action is inferred.
type ReviewRecord struct {
	ExternalID    string      `json:"external_id"`
	Author        *IssueActor `json:"author"`
	Body          string      `json:"body"`
	State         string      `json:"state"`
	ProviderState string      `json:"provider_state"`
	Commit        *string     `json:"commit"`
	SubmittedAt   *time.Time  `json:"submitted_at"`
}
type ReviewPage struct {
	Repository string            `json:"repository"`
	Name       string            `json:"name"`
	Pull       DiscussionSubject `json:"pull"`
	Items      []ReviewRecord    `json:"items"`
	NextPage   int               `json:"next_page"`
}

func (c *Client) PullReviews(ctx context.Context, repository int64, number, page, limit int) (ReviewPage, error) {
	if repository < 1 || number < 1 || !pageQuery(page, limit) {
		return ReviewPage{}, ErrInvalidRequest
	}
	return c.pullReviews(ctx, repository, number, page, limit, 0)
}
func (c *Client) PullReview(ctx context.Context, repository int64, number int, review int64) (ReviewPage, error) {
	if repository < 1 || number < 1 || review < 1 {
		return ReviewPage{}, ErrInvalidRequest
	}
	return c.pullReviews(ctx, repository, number, 0, 1, review)
}

// Both list and detail bracket the selected actual PR identity and repository
// visibility. They are live pages, not an atomic history or a latest-head vote.
func (c *Client) pullReviews(ctx context.Context, repository int64, number, page, limit int, review int64) (ReviewPage, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	owner, name, err := c.issueLocator(ctx, repository)
	if err != nil {
		return ReviewPage{}, err
	}
	parent, err := c.discussionSubject(ctx, repository, owner, name, "pull", number)
	if err != nil {
		return ReviewPage{}, err
	}
	var reviews []*sdk.PullRequestReview
	var response *sdk.Response
	if review == 0 {
		reviews, response, err = c.sdk.PullRequests.ListReviews(ctx, owner, name, number, &sdk.ListOptions{Page: page, PerPage: limit})
	} else {
		var item *sdk.PullRequestReview
		item, response, err = c.sdk.PullRequests.GetReview(ctx, owner, name, number, review)
		reviews = []*sdk.PullRequestReview{item}
	}
	info, err := result(ctx, response, err)
	if err != nil {
		return ReviewPage{}, err
	}
	if reviews == nil || len(reviews) > limit || info.NextPage != 0 && (review != 0 || info.NextPage <= page || info.NextPage > 1_000_000) {
		return ReviewPage{}, ErrInvalidResponse
	}
	result := ReviewPage{Repository: strconv.FormatInt(repository, 10), Name: owner + "/" + name, Pull: parent, Items: make([]ReviewRecord, 0, len(reviews)), NextPage: info.NextPage}
	seen := map[int64]bool{}
	for _, item := range reviews {
		if item == nil || item.GetID() < 1 || seen[item.GetID()] || review != 0 && item.GetID() != review || item.GetPullRequestURL() != "https://api.github.com/repos/"+owner+"/"+name+"/pulls/"+strconv.Itoa(number) {
			return ReviewPage{}, ErrInvalidResponse
		}
		seen[item.GetID()] = true
		record := ReviewRecord{ExternalID: strconv.FormatInt(item.GetID(), 10), Body: item.GetBody(), State: "unknown", ProviderState: item.GetState(), SubmittedAt: issueTime(item.SubmittedAt)}
		switch item.GetState() {
		case "PENDING":
			record.State = "pending"
		case "APPROVED":
			record.State = "approved"
		case "CHANGES_REQUESTED":
			record.State = "changes_requested"
		case "COMMENTED":
			record.State = "commented"
		case "DISMISSED":
			record.State = "dismissed"
		}
		if record.State == "pending" && record.SubmittedAt != nil {
			return ReviewPage{}, ErrInvalidResponse
		}
		if item.CommitID != nil {
			if !collaboration.ValidCommitID(collaboration.CommitID(*item.CommitID)) {
				return ReviewPage{}, ErrInvalidResponse
			}
			record.Commit = item.CommitID
		}
		if item.User != nil {
			actor, err := issueActor(item.User)
			if err != nil {
				return ReviewPage{}, err
			}
			record.Author = &actor
		}
		result.Items = append(result.Items, record)
	}
	after, err := c.discussionSubject(ctx, repository, owner, name, "pull", number)
	if err != nil {
		return ReviewPage{}, err
	}
	if after != parent {
		return ReviewPage{}, ErrNotVisible
	}
	if err := c.checkIssueLocator(ctx, repository, owner, name); err != nil {
		return ReviewPage{}, err
	}
	return result, nil
}
