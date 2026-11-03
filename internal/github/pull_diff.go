package github

import (
	"context"
	"strconv"

	sdk "github.com/google/go-github/v93/github"
	"github.com/levitateos/sodaos/internal/collaboration"
)

// Provider-local PR diff identity. PullBase is the captured PR-payload base;
// Base is the verified merge base with Head. Current target-branch tip/protection
// facts and canonical identity mapping belong to their consuming readers.
type PullDiffScope struct {
	Repository string            `json:"repository"`
	Name       string            `json:"name"`
	Pull       DiscussionSubject `json:"pull"`
	Base       string            `json:"base"`
	Head       string            `json:"head"`
	PullBase   string            `json:"pull_base"`
	BaseBranch string            `json:"base_branch"`
}

// PullDiff resolves the actual merge base of a PR's captured base and head.
// A supplied full pair must still match; it cannot substitute a historical or
// arbitrary comparison. Both discovery and pinned reads recheck the live PR and
// repository before returning. The whole operation shares one finite budget.
func (c *Client) PullDiff(ctx context.Context, repository int64, number int, base, head string) (PullDiffScope, error) {
	if repository < 1 || number < 1 || !validDiffPair(base, head) {
		return PullDiffScope{}, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	owner, name, err := c.issueLocator(ctx, repository)
	if err != nil {
		return PullDiffScope{}, err
	}
	before, err := c.pullDiffState(ctx, repository, owner, name, number)
	if err != nil {
		return PullDiffScope{}, err
	}
	if head != "" && head != before.Head {
		return PullDiffScope{}, ErrDiffChanged
	}
	scope, err := c.resolvePullDiff(ctx, owner, name, number, before, base)
	if err != nil {
		return PullDiffScope{}, err
	}
	after, err := c.pullDiffState(ctx, repository, owner, name, number)
	if err != nil {
		return PullDiffScope{}, err
	}
	if after != before {
		return PullDiffScope{}, ErrDiffChanged
	}
	if err := c.checkIssueLocator(ctx, repository, owner, name); err != nil {
		return PullDiffScope{}, err
	}
	return scope, nil
}

func (c *Client) resolvePullDiff(ctx context.Context, owner, name string, number int, before pullDiffState, base string) (PullDiffScope, error) {
	// Compare metadata includes merge_base_commit on paged responses. Files
	// appear only on page one, so page two avoids fetching unrelated patches or
	// confusing the comparison's 300-file ceiling with a PR file catalogue.
	comparison, response, err := c.sdk.Repositories.CompareCommits(ctx, owner, name, before.Base, before.Head, &sdk.ListOptions{Page: 2, PerPage: 1})
	if _, err := result(ctx, response, err); err != nil {
		return PullDiffScope{}, err
	}
	if comparison == nil || comparison.BaseCommit == nil || comparison.BaseCommit.GetSHA() != before.Base || comparison.MergeBaseCommit == nil ||
		!collaboration.ValidCommitID(collaboration.CommitID(comparison.MergeBaseCommit.GetSHA())) {
		return PullDiffScope{}, ErrInvalidResponse
	}
	mergeBase := comparison.MergeBaseCommit.GetSHA()
	if base != "" && base != mergeBase {
		return PullDiffScope{}, ErrDiffChanged
	}
	return PullDiffScope{Repository: before.Repository, Name: owner + "/" + name,
		Pull: DiscussionSubject{Kind: "pull", ExternalID: before.Pull, Number: number},
		Base: mergeBase, Head: before.Head, PullBase: before.Base, BaseBranch: before.Branch}, nil
}

type pullDiffState struct {
	Repository string
	Pull       string
	Base       string
	Head       string
	Branch     string
}

func (c *Client) pullDiffState(ctx context.Context, repository int64, owner, name string, number int) (pullDiffState, error) {
	pull, err := c.readDiffPull(ctx, repository, owner, name, number)
	if err != nil {
		return pullDiffState{}, err
	}
	return diffState(repository, pull), nil
}

func (c *Client) readDiffPull(ctx context.Context, repository int64, owner, name string, number int) (*sdk.PullRequest, error) {
	pull, response, err := c.sdk.PullRequests.Get(ctx, owner, name, number)
	if _, err := result(ctx, response, err); err != nil {
		return nil, err
	}
	if !validPull(pull, repository, owner, name) || pull.GetNumber() != number {
		return nil, ErrInvalidResponse
	}
	if pull.Head == nil || pull.Base.GetRef() == "" ||
		!collaboration.ValidCommitID(collaboration.CommitID(pull.Base.GetSHA())) ||
		!collaboration.ValidCommitID(collaboration.CommitID(pull.Head.GetSHA())) {
		return nil, ErrUnavailable
	}
	return pull, nil
}

func diffState(repository int64, pull *sdk.PullRequest) pullDiffState {
	return pullDiffState{Repository: strconv.FormatInt(repository, 10), Pull: strconv.FormatInt(pull.GetID(), 10), Base: pull.Base.GetSHA(), Head: pull.Head.GetSHA(), Branch: pull.Base.GetRef()}
}

func validDiffPair(base, head string) bool {
	return base == "" && head == "" || collaboration.ValidCommitID(collaboration.CommitID(base)) && collaboration.ValidCommitID(collaboration.CommitID(head))
}
