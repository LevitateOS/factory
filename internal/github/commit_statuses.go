package github

import (
	"context"
	"strconv"
	"time"

	sdk "github.com/google/go-github/v93/github"
)

// Commit statuses are legacy status observations, not check runs or required
// gates. Repeated contexts retain each actual status ID in provider history order.
type CommitStatusRecord struct {
	ExternalID    string      `json:"external_id"`
	Head          string      `json:"head"`
	Context       *string     `json:"context"`
	ProviderState *string     `json:"provider_state"`
	State         string      `json:"state"`
	Description   *string     `json:"description"`
	TargetURL     *string     `json:"target_url"`
	Creator       *IssueActor `json:"creator"`
	CreatedAt     *time.Time  `json:"created_at"`
	UpdatedAt     *time.Time  `json:"updated_at"`
}

type CommitStatusPage struct {
	Repository string               `json:"repository"`
	Name       string               `json:"name"`
	Pull       DiscussionSubject    `json:"pull"`
	Head       string               `json:"head"`
	Items      []CommitStatusRecord `json:"items"`
	NextPage   int                  `json:"next_page"`
}

// PullCommitStatuses reads one newest-first history page, not latest-per-context
// aggregation. Continuations require the captured full PR head. New statuses may
// still arrive for that head; this is not an atomic history or a merge decision.
func (c *Client) PullCommitStatuses(ctx context.Context, repository int64, number, page, limit int, head string) (CommitStatusPage, error) {
	if repository < 1 || number < 1 || !validCheckHeadQuery(page, limit, head) {
		return CommitStatusPage{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	owner, name, err := c.issueLocator(ctx, repository)
	if err != nil {
		return CommitStatusPage{}, err
	}
	before, err := c.checkHead(ctx, repository, owner, name, number)
	if err != nil {
		return CommitStatusPage{}, err
	}
	if head != "" && head != before.Head {
		return CommitStatusPage{}, ErrChecksChanged
	}
	statuses, response, err := c.sdk.Repositories.ListStatuses(ctx, owner, name, before.Head, &sdk.ListOptions{Page: page, PerPage: limit})
	info, err := result(ctx, response, err)
	if err != nil {
		return CommitStatusPage{}, err
	}
	if statuses == nil || len(statuses) > limit || info.NextPage != 0 && (len(statuses) == 0 || info.NextPage <= page || info.NextPage > 1_000_000) {
		return CommitStatusPage{}, ErrInvalidResponse
	}
	r := CommitStatusPage{Repository: strconv.FormatInt(repository, 10), Name: owner + "/" + name, Pull: before.Pull, Head: before.Head, Items: make([]CommitStatusRecord, 0, len(statuses)), NextPage: info.NextPage}
	seen := map[int64]bool{}
	for _, status := range statuses {
		if status == nil || status.GetID() < 1 || seen[status.GetID()] || status.GetURL() != "https://api.github.com/repos/"+owner+"/"+name+"/statuses/"+before.Head {
			return CommitStatusPage{}, ErrInvalidResponse
		}
		seen[status.GetID()] = true
		record := CommitStatusRecord{ExternalID: strconv.FormatInt(status.GetID(), 10), Head: before.Head, Context: status.Context, ProviderState: status.State, State: "unknown", Description: status.Description, TargetURL: status.TargetURL, CreatedAt: issueTime(status.CreatedAt), UpdatedAt: issueTime(status.UpdatedAt)}
		switch status.GetState() {
		case "pending", "success", "failure", "error":
			record.State = status.GetState()
		}
		if status.Creator != nil {
			actor, err := issueActor(status.Creator)
			if err != nil {
				return CommitStatusPage{}, err
			}
			record.Creator = &actor
		}
		r.Items = append(r.Items, record)
	}
	after, err := c.checkHead(ctx, repository, owner, name, number)
	if err != nil {
		return CommitStatusPage{}, err
	}
	if after != before {
		return CommitStatusPage{}, ErrChecksChanged
	}
	if err := c.checkIssueLocator(ctx, repository, owner, name); err != nil {
		return CommitStatusPage{}, err
	}
	return r, nil
}
