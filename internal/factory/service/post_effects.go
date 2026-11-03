package service

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/sodaos/internal/collaboration"
	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// PostEffectStore reads the actual source and its recorded authored draft.
// Effect receipts remain in the existing provider operation journal.
type PostEffectStore interface {
	FeedPost(context.Context, string) (store.FeedPostDetail, error)
	PostDraft(context.Context, string, int64) (factory.IssueDraft, error)
}

type PostEffectGraph interface {
	factory.GraphCodec
	CheckIntentOwner(context.Context, int64, int64, int64) (int64, error)
	SubmitGraph(context.Context, factory.IntentGeneration, int64) (factory.GraphOperationOutcome, error)
	LookupGraph(context.Context, factory.IntentGeneration, int64) (factory.GraphOperationLookup, error)
}

// PostIssueEffects binds deterministic issue/feedback effects to the current
// recorded human source. Graph supplies current author/publishing admission and
// provider encoding; saving a feed post grants no execution authority.
type PostIssueEffects struct {
	Store PostEffectStore
	Graph PostEffectGraph
	Actor int64
}

var _ PostIssueCreator = (*PostIssueEffects)(nil)

var (
	ErrPostEffectUnavailable = errors.New("post issue delivery is unavailable")
	ErrPostEffectSource      = errors.New("post issue source changed or is unavailable")
	ErrPostEffectUnknown     = errors.New("post issue operation outcome is unknown")
	ErrPostEffectReceipt     = errors.New("post issue receipt does not match its operation and scope")
)

func (s *PostIssueEffects) source(ctx context.Context, repository int64, id string) (store.FeedPostDetail, int64, error) {
	var empty store.FeedPostDetail
	if s == nil || s.Store == nil || s.Graph == nil || s.Actor <= 0 {
		return empty, 0, ErrPostEffectUnavailable
	}
	if err := ctx.Err(); err != nil {
		return empty, 0, err
	}
	post, err := s.Store.FeedPost(ctx, id)
	if err != nil {
		return empty, 0, err
	}
	author, authorErr := strconv.ParseInt(post.Record.Author, 10, 64)
	if repository <= 0 || post.Record.Validate() != nil || post.Record.ID != id ||
		post.Record.Repository != repository || post.Record.Deleted || post.Record.Revoked || authorErr != nil || author <= 0 || strconv.FormatInt(author, 10) != post.Record.Author {
		return empty, 0, ErrPostEffectSource
	}
	return post, author, nil
}

func (s *PostIssueEffects) current(ctx context.Context, post store.FeedPostDetail, draft *factory.IssueDraft) (int64, error) {
	latest, author, err := s.source(ctx, post.Record.Repository, post.Record.ID)
	if err != nil {
		return 0, err
	}
	if latest.Record != post.Record || latest.Body != post.Body {
		return 0, ErrPostEffectSource
	}
	if draft != nil {
		saved, err := s.Store.PostDraft(ctx, post.Record.ID, post.Record.Revision)
		if err != nil {
			return 0, err
		}
		if saved != *draft || draft.SourcePost != post.Record.ID || draft.Revision != post.Record.Revision {
			return 0, ErrPostEffectSource
		}
	}
	revision, err := s.Graph.CheckIntentOwner(ctx, post.Record.Repository, author, s.Actor)
	if err != nil {
		return 0, err
	}
	// Current permission reads may race a local edit/revocation. Re-read the
	// source after that I/O before returning data or submitting an effect.
	latest, _, err = s.source(ctx, post.Record.Repository, post.Record.ID)
	if err != nil {
		return 0, err
	}
	if latest.Record != post.Record || latest.Body != post.Body {
		return 0, ErrPostEffectSource
	}
	if draft != nil {
		saved, err := s.Store.PostDraft(ctx, post.Record.ID, post.Record.Revision)
		if err != nil {
			return 0, err
		}
		if saved != *draft {
			return 0, ErrPostEffectSource
		}
	}
	if revision < 1 {
		return 0, ErrPostEffectUnavailable
	}
	return revision, ctx.Err()
}

func postIssueOperationID(post string) string {
	// Preserve historical operation identities across the vocabulary/package move.
	return "commentissue-" + post
}

func (s *PostIssueEffects) receiptIndex(ctx context.Context, repository int64, id string, outcome factory.GraphOperationOutcome) (int64, error) {
	if outcome.OperationID != postIssueOperationID(id) || outcome.Effect != collaboration.EffectCommitted || len(outcome.Receipt) == 0 {
		return 0, ErrPostEffectUnknown
	}
	facts, err := s.Graph.DecodeGraphReceipt(ctx, outcome.Receipt)
	if err != nil {
		return 0, errors.Join(ErrPostEffectReceipt, err)
	}
	issue, exists := facts.Creates[id]
	if facts.Repository != repository || facts.Actor != s.Actor || !exists || len(facts.Creates) != 1 || len(facts.Edits) != 0 ||
		issue.IssueID <= 0 || issue.IssueIndex <= 0 || issue.ContentVersion < 0 {
		return 0, ErrPostEffectReceipt
	}
	return issue.IssueIndex, nil
}

func (s *PostIssueEffects) CreatePostIssue(ctx context.Context, draft factory.IssueDraft, repository int64) (int64, error) {
	if err := draft.Validate(); err != nil {
		return 0, err
	}
	post, _, err := s.source(ctx, repository, draft.SourcePost)
	if err != nil {
		return 0, err
	}
	// Semantic content has one owner; the adapter encodes its actual provider
	// payload while retaining the exact historical Fountain operation identity.
	payload, err := s.Graph.EncodeGraph(collaboration.GraphIntent{Creates: []collaboration.GraphCreate{{
		Key: draft.SourcePost, Title: draft.Title, Body: PostIssueBody(draft),
	}}}, repository)
	if err != nil {
		return 0, err
	}
	revision, err := s.current(ctx, post, &draft)
	if err != nil {
		return 0, err
	}
	generation := factory.IntentGeneration{
		OperationID: postIssueOperationID(draft.SourcePost), ActorID: s.Actor, NativeRev: revision,
		AuthRevision: "comment/" + draft.SourcePost + "/r" + strconv.FormatInt(draft.Revision, 10),
		NotAfter:     time.Now().Add(5 * time.Minute).Unix(), Payload: payload,
	}
	outcome, err := s.Graph.SubmitGraph(ctx, generation, repository)
	if err != nil {
		return 0, err
	}
	return s.receiptIndex(ctx, repository, draft.SourcePost, outcome)
}

func (s *PostIssueEffects) LookupPostIssue(ctx context.Context, repository int64, id string) (int64, error) {
	post, _, err := s.source(ctx, repository, id)
	if err != nil {
		return 0, err
	}
	if _, err = s.current(ctx, post, nil); err != nil {
		return 0, err
	}
	lookup, err := s.Graph.LookupGraph(ctx, factory.IntentGeneration{OperationID: postIssueOperationID(id), ActorID: s.Actor}, repository)
	if err != nil {
		return 0, err
	}
	var issue int64
	switch {
	case lookup.State == collaboration.LookupNotObserved && lookup.Record == nil:
		// Only confirmed absence allows the driver to attempt this same operation.
	case lookup.State == collaboration.LookupFound && lookup.Record != nil:
		issue, err = s.receiptIndex(ctx, repository, id, *lookup.Record)
	default:
		err = ErrPostEffectUnknown
	}
	if err != nil {
		return 0, err
	}
	if _, err = s.current(ctx, post, nil); err != nil {
		return 0, err
	}
	return issue, nil
}

func (s *PostIssueEffects) DeliverPostFeedback(ctx context.Context, comment store.FeedPostDetail, issue int64) error {
	post, _, err := s.source(ctx, comment.Record.Repository, comment.Record.ID)
	if err != nil {
		return err
	}
	if issue <= 0 || post.Record != comment.Record || post.Body != comment.Body {
		return ErrPostEffectSource
	}
	writer, ok := s.Graph.(interface {
		DeliverIssueFeedback(context.Context, int64, int64, int64, string, string) error
	})
	if !ok {
		return ErrPostEffectUnavailable
	}
	if _, err := s.current(ctx, post, nil); err != nil {
		return err
	}
	marker := "<!-- soda-feed:" + post.Record.ID + ":r" + strconv.FormatInt(post.Record.Revision, 10) + " -->"
	body := "Feedback from @" + post.AuthorName + " in the project feed:\n\n" + post.Body
	return writer.DeliverIssueFeedback(ctx, post.Record.Repository, issue, s.Actor, marker, body)
}
