package service

import (
	"context"
	"errors"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// PostIssueQuery requests an open-issue page, or one explicit issue (including
// closed issues). A number is local to the admitted canonical repository.
type PostIssueQuery struct {
	Cursor string
	Number int64
	Limit  int
}

// PostIssueReader reads current human-visible discussion context. Only scope,
// number, observation, title/body and state are consumed; unobserved external
// record IDs are not fabricated and this read supplies no execution proof.
type PostIssueReader func(context.Context, collaboration.ActorID, collaboration.RepositoryID, PostIssueQuery) (collaboration.Page[collaboration.Issue], error)

type PostContext struct {
	Posts  Posts
	Issues PostIssueReader
}

var ErrPostContextPage = errors.New("issue context page is unavailable or did not advance")

// Prepare uses the exact captured revision, never a background provider read.
// Missing human context waits without spending a routing judgment.
func (s PostContext) Prepare(ctx context.Context, in PostJudgeInput) (PostJudgeInput, error) {
	if s.Posts.Store == nil {
		return in, ErrPostIssueContext
	}
	candidates, err := s.Posts.Store.PostIssueContext(ctx, in.SourcePost.Record.ID, in.SourcePost.Record.Revision)
	if err != nil {
		return in, ErrPostIssueContext
	}
	in.Candidates = candidates
	return in, nil
}

// Capture saves a complete bounded context for this exact author's revision.
// It never consults a model, creates an issue or enrolls execution authority.
func (s PostContext) Capture(ctx context.Context, actor collaboration.ActorID, id string, revision int64) error {
	if actor < 1 {
		return ErrPostNotFound
	}
	if s.Posts.Store == nil || s.Issues == nil {
		return ErrPostCaptureUnavailable
	}
	detail, err := s.Posts.Store.FeedPost(ctx, id)
	if err != nil {
		return err
	}
	if detail.Record.Deleted || detail.Record.Revoked {
		return ErrPostNotFound
	}
	if detail.Record.Author != strconv.FormatInt(int64(actor), 10) {
		return ErrPostAuthorRequired
	}
	if detail.Record.Revision != revision {
		return store.ErrStaleRevision
	}
	repository := detail.Record.Repository
	if err := s.Posts.visible(ctx, actor, repository); err != nil {
		return err
	}
	input, err := CollectPostJudgeInput(ctx, s.Posts.Store, detail)
	if err != nil {
		return err
	}
	read := func(query PostIssueQuery) (collaboration.Page[collaboration.Issue], error) {
		var empty collaboration.Page[collaboration.Issue]
		if err := s.Posts.visible(ctx, actor, repository); err != nil {
			return empty, err
		}
		page, err := s.Issues(ctx, actor, collaboration.RepositoryID(repository), query)
		if err != nil {
			return empty, err
		}
		if err := ctx.Err(); err != nil {
			return empty, err
		}
		if page.Observation.State != collaboration.ReadAvailable || page.Complete != (page.Next == "") || len(page.Items) > query.Limit {
			return empty, ErrPostContextPage
		}
		for _, issue := range page.Items {
			if issue.Observation.State != collaboration.ReadAvailable || issue.Ref.Kind != collaboration.SubjectIssue || issue.Ref.Repository != collaboration.RepositoryID(repository) || issue.Ref.Number < 1 || issue.State != "open" && issue.State != "closed" {
				return empty, ErrPostContextPage
			}
			if query.Number > 0 && issue.Ref.Number != query.Number {
				return empty, ErrPostContextPage
			}
		}
		return page, nil
	}
	byIssue := map[int64]factory.PostWorkCandidate{}
	order := []int64{}
	add := func(issue collaboration.Issue) error {
		number := issue.Ref.Number
		candidate := factory.PostWorkCandidate{Issue: number, Title: issue.Title, Objective: issue.Body, Summary: store.TruncateFeedSummary(issue.Title + "\n" + issue.Body), Status: issue.State, Origin: factory.PostCandidateRecent}
		for _, previous := range input.Candidates {
			if previous.Issue == number {
				candidate.Origin, candidate.WorkerStatus = previous.Origin, previous.Status
				break
			}
		}
		if err := candidate.Validate(); err != nil {
			return err
		}
		if _, exists := byIssue[number]; !exists {
			order = append(order, number)
		}
		byIssue[number] = candidate
		return nil
	}
	cursor := ""
	seen := map[string]bool{"": true}
	for {
		page, err := read(PostIssueQuery{Cursor: cursor, Limit: 50})
		if err != nil {
			return err
		}
		for _, issue := range page.Items {
			if err := add(issue); err != nil {
				return err
			}
		}
		if len(order) > 250 {
			return errors.New("Choose the issue this request concerns; there are too many open issues to assess together.")
		}
		if page.Next == "" {
			break
		}
		if len(page.Items) == 0 || seen[page.Next] || len(seen) >= 250 {
			return ErrPostContextPage
		}
		seen[page.Next] = true
		cursor = page.Next
	}
	for _, candidate := range input.Candidates {
		if candidate.Origin == factory.PostCandidateRecent {
			continue
		}
		if _, exists := byIssue[candidate.Issue]; exists {
			continue
		}
		page, err := read(PostIssueQuery{Number: candidate.Issue, Limit: 1})
		if err != nil {
			return err
		}
		if len(page.Items) != 1 || !page.Complete {
			return ErrPostContextPage
		}
		if err := add(page.Items[0]); err != nil {
			return err
		}
	}
	candidates := make([]factory.PostWorkCandidate, 0, len(order))
	for _, number := range order {
		candidates = append(candidates, byIssue[number])
	}
	if err := s.Posts.visible(ctx, actor, repository); err != nil {
		return err
	}
	return s.Posts.Store.SavePostIssueContext(ctx, id, revision, candidates)
}
