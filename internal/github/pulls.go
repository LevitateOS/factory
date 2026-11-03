package github

import (
	"context"
	"strconv"
	"time"

	sdk "github.com/google/go-github/v93/github"
	"github.com/levitateos/sodaos/internal/collaboration"
)

// Provider-local references, not canonical identities or execution authority.
// A deleted/unavailable head repository stays nil even if its ref/SHA survives.
type PullRepository struct {
	ExternalID string `json:"external_id"`
	Name       string `json:"name"`
}
type PullBranch struct {
	Repository *PullRepository `json:"repository"`
	Name       string          `json:"name"`
	Commit     *string         `json:"commit"`
}
type PullRecord struct {
	ExternalID     string       `json:"external_id"`
	Number         int          `json:"number"`
	Title          string       `json:"title"`
	Body           string       `json:"body"`
	State          string       `json:"state"`
	Draft          *bool        `json:"draft"`
	Locked         *bool        `json:"locked"`
	Author         *IssueActor  `json:"author"`
	Assignees      []IssueActor `json:"assignees"`
	Labels         []IssueLabel `json:"labels"`
	Base           *PullBranch  `json:"base"`
	Head           *PullBranch  `json:"head"`
	CreatedAt      *time.Time   `json:"created_at"`
	UpdatedAt      *time.Time   `json:"updated_at"`
	ClosedAt       *time.Time   `json:"closed_at"`
	MergedAt       *time.Time   `json:"merged_at"`
	Merged         *bool        `json:"merged"`
	Mergeable      *bool        `json:"mergeable"`
	MergeableState *string      `json:"mergeable_state"`
	Rebaseable     *bool        `json:"rebaseable"`
}
type PullPage struct {
	Repository string       `json:"repository"`
	Name       string       `json:"name"`
	Items      []PullRecord `json:"items"`
	NextPage   int          `json:"next_page"`
}

// PullList reads one provider page. List responses omit detail-only fields;
// nil mergeability/merged facts remain unknown, never false or merge permission.
func (c *Client) PullList(ctx context.Context, repository int64, page, limit int, state string) (PullPage, error) {
	if repository < 1 || !pageQuery(page, limit) || state != "" && state != "open" && state != "closed" && state != "all" {
		return PullPage{}, ErrInvalidRequest
	}
	if state == "" {
		state = "open"
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	owner, name, err := c.issueLocator(ctx, repository)
	if err != nil {
		return PullPage{}, err
	}
	pulls, response, err := c.sdk.PullRequests.List(ctx, owner, name, &sdk.PullRequestListOptions{
		State: state, Sort: "created", Direction: "asc", ListOptions: sdk.ListOptions{Page: page, PerPage: limit},
	})
	info, err := result(ctx, response, err)
	if err != nil {
		return PullPage{}, err
	}
	if pulls == nil || len(pulls) > limit || info.NextPage != 0 && (info.NextPage <= page || info.NextPage > 1_000_000) {
		return PullPage{}, ErrInvalidResponse
	}
	pageResult := PullPage{Repository: strconv.FormatInt(repository, 10), Name: owner + "/" + name, Items: make([]PullRecord, 0, len(pulls)), NextPage: info.NextPage}
	seen := map[int64]bool{}
	numbers := map[int]bool{}
	for _, pull := range pulls {
		if !validPull(pull, repository, owner, name) || seen[pull.GetID()] || numbers[pull.GetNumber()] || state != "all" && pull.GetState() != state {
			return PullPage{}, ErrInvalidResponse
		}
		seen[pull.GetID()], numbers[pull.GetNumber()] = true, true
		record, err := pullRecord(pull)
		if err != nil {
			return PullPage{}, err
		}
		pageResult.Items = append(pageResult.Items, record)
	}
	if err := c.checkIssueLocator(ctx, repository, owner, name); err != nil {
		return PullPage{}, err
	}
	return pageResult, nil
}

// Pull reads any-state detail, without retrying unknown mergeability. Branch
// SHAs describe this response, not an immutable diff pair or mutation admission.
func (c *Client) Pull(ctx context.Context, repository int64, number int) (PullPage, error) {
	if repository < 1 || number < 1 {
		return PullPage{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	owner, name, err := c.issueLocator(ctx, repository)
	if err != nil {
		return PullPage{}, err
	}
	pull, response, err := c.sdk.PullRequests.Get(ctx, owner, name, number)
	if _, err := result(ctx, response, err); err != nil {
		return PullPage{}, err
	}
	if !validPull(pull, repository, owner, name) || pull.GetNumber() != number {
		return PullPage{}, ErrInvalidResponse
	}
	record, err := pullRecord(pull)
	if err != nil {
		return PullPage{}, err
	}
	if err := c.checkIssueLocator(ctx, repository, owner, name); err != nil {
		return PullPage{}, err
	}
	return PullPage{Repository: strconv.FormatInt(repository, 10), Name: owner + "/" + name, Items: []PullRecord{record}}, nil
}

func validPull(pull *sdk.PullRequest, repository int64, owner, name string) bool {
	return pull != nil && pull.GetID() > 0 && pull.GetNumber() > 0 && pull.Title != nil &&
		(pull.GetState() == "open" || pull.GetState() == "closed") && pull.Base != nil && pull.Base.Repo != nil && pull.Base.Repo.GetID() == repository &&
		pull.GetURL() == "https://api.github.com/repos/"+owner+"/"+name+"/pulls/"+strconv.Itoa(pull.GetNumber())
}
func pullRecord(pull *sdk.PullRequest) (PullRecord, error) {
	record := PullRecord{ExternalID: strconv.FormatInt(pull.GetID(), 10), Number: pull.GetNumber(), Title: pull.GetTitle(), Body: pull.GetBody(), State: pull.GetState(), Draft: pull.Draft, Locked: pull.Locked, Assignees: []IssueActor{}, Labels: []IssueLabel{}, CreatedAt: issueTime(pull.CreatedAt), UpdatedAt: issueTime(pull.UpdatedAt), ClosedAt: issueTime(pull.ClosedAt), MergedAt: issueTime(pull.MergedAt), Merged: pull.Merged, Mergeable: pull.Mergeable, MergeableState: pull.MergeableState, Rebaseable: pull.Rebaseable}
	if pull.GetMerged() || pull.MergedAt != nil {
		if record.State != "closed" || pull.Merged != nil && !pull.GetMerged() {
			return PullRecord{}, ErrInvalidResponse
		}
		record.State = "merged"
	}
	var err error
	if record.Base, err = pullBranch(pull.Base); err != nil {
		return PullRecord{}, err
	}
	if record.Head, err = pullBranch(pull.Head); err != nil {
		return PullRecord{}, err
	}
	if pull.User != nil {
		author, err := issueActor(pull.User)
		if err != nil {
			return PullRecord{}, err
		}
		record.Author = &author
	}
	for _, user := range pull.Assignees {
		actor, err := issueActor(user)
		if err != nil {
			return PullRecord{}, err
		}
		record.Assignees = append(record.Assignees, actor)
	}
	for _, label := range pull.Labels {
		if label == nil || label.GetID() < 1 || label.GetName() == "" {
			return PullRecord{}, ErrInvalidResponse
		}
		record.Labels = append(record.Labels, IssueLabel{ExternalID: strconv.FormatInt(label.GetID(), 10), Name: label.GetName(), Color: label.GetColor()})
	}
	return record, nil
}
func pullBranch(branch *sdk.PullRequestBranch) (*PullBranch, error) {
	if branch == nil {
		return nil, nil
	}
	if branch.SHA != nil && !collaboration.ValidCommitID(collaboration.CommitID(*branch.SHA)) {
		return nil, ErrInvalidResponse
	}
	record := &PullBranch{Name: branch.GetRef(), Commit: branch.SHA}
	if branch.Repo != nil {
		if branch.Repo.GetID() < 1 || branch.Repo.GetFullName() == "" {
			return nil, ErrInvalidResponse
		}
		record.Repository = &PullRepository{ExternalID: strconv.FormatInt(branch.Repo.GetID(), 10), Name: branch.Repo.GetFullName()}
	}
	return record, nil
}
