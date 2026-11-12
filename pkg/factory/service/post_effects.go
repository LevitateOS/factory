package service

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// PostEffectStore reads the actual source and its recorded authored draft.
// Effect receipts remain in the existing provider operation journal.
type PostEffectStore interface {
	FeedPost(context.Context, string) (store.FeedPostDetail, error)
	PostDraft(context.Context, string, int64) (factory.IssueDraft, error)
	BeginPostIssueCreation(context.Context, string, int64) error
	RecordPostIssueCreation(context.Context, string, int64, int64) error
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
	if err := s.Store.BeginPostIssueCreation(ctx, draft.SourcePost, draft.Revision); err != nil {
		return 0, err
	}
	outcome, err := s.Graph.SubmitGraph(ctx, generation, repository)
	if err != nil {
		return 0, err
	}
	if outcome.OperationID == generation.OperationID && outcome.Effect == collaboration.EffectNotCommitted {
		if err := s.Store.RecordPostIssueCreation(ctx, draft.SourcePost, draft.Revision, -1); err != nil {
			return 0, err
		}
	}
	issue, err := s.receiptIndex(ctx, repository, draft.SourcePost, outcome)
	if err != nil {
		return 0, err
	}
	return issue, s.Store.RecordPostIssueCreation(ctx, draft.SourcePost, draft.Revision, issue)
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
		if lookup.Record.OperationID == postIssueOperationID(id) && lookup.Record.Effect == collaboration.EffectNotCommitted {
			if err = s.Store.BeginPostIssueCreation(ctx, id, post.Record.Revision); err != nil {
				return 0, err
			}
			if err = s.Store.RecordPostIssueCreation(ctx, id, post.Record.Revision, -1); err != nil {
				return 0, err
			}
		}
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
	if issue > 0 {
		// Older operations may predate the origin journal. Establish the same
		// source record before adopting their reconciled result.
		if err = s.Store.BeginPostIssueCreation(ctx, id, post.Record.Revision); err != nil {
			return 0, err
		}
		if err = s.Store.RecordPostIssueCreation(ctx, id, post.Record.Revision, issue); err != nil {
			return 0, err
		}
	}
	return issue, nil
}

type postFeedbackJournal interface {
	PostFeedback(context.Context, string, int64) (factory.PostFeedbackDelivery, error)
	BeginPostFeedback(context.Context, factory.PostFeedbackIntent) (factory.PostFeedbackDelivery, bool, error)
	SavePostFeedback(context.Context, factory.PostFeedbackDelivery, factory.PostFeedbackDelivery) error
}
type postFeedbackWriter interface {
	FeedbackHost() collaboration.HostID
	DeliverIssueFeedback(context.Context, factory.PostFeedbackDelivery, bool, func(context.Context) error) (factory.PostFeedbackDelivery, error)
}

var ErrPostFeedbackPending = errors.New("feedback receipt lookup remains pending")

func (s *PostIssueEffects) DeliverPostFeedback(ctx context.Context, comment store.FeedPostDetail, issue int64) error {
	post, _, err := s.source(ctx, comment.Record.Repository, comment.Record.ID)
	if err != nil {
		return err
	}
	if issue <= 0 || post.Record != comment.Record || post.Body != comment.Body {
		return ErrPostEffectSource
	}
	writer, ok := s.Graph.(postFeedbackWriter)
	if !ok {
		return ErrPostEffectUnavailable
	}
	journal, ok := s.Store.(postFeedbackJournal)
	if !ok {
		return ErrPostEffectUnavailable
	}
	marker := "<!-- soda-feed:" + post.Record.ID + ":r" + strconv.FormatInt(post.Record.Revision, 10) + " -->"
	body := "Feedback from @" + post.AuthorName + " in the project feed:\n\n" + post.Body
	intent := factory.PostFeedbackIntent{Host: string(writer.FeedbackHost()), SourcePost: post.Record.ID, Revision: post.Record.Revision, Repository: post.Record.Repository, Issue: issue, Actor: s.Actor, Marker: marker, Body: body, Digest: factory.PostFeedbackDigest(marker + "\n" + body)}
	prior, priorErr := journal.PostFeedback(ctx, intent.SourcePost, intent.Revision)
	if priorErr == nil {
		if prior.Intent != intent {
			return ErrPostEffectReceipt
		}
		if prior.Complete {
			if prior.Attention == "content_changed" {
				return ErrPostEffectReceipt
			}
			if prior.Receipt != "" {
				return nil
			}
			return ErrPostEffectUnknown
		}
		if prior.NotBefore > time.Now().Unix() {
			return ErrPostFeedbackPending
		}
	} else if !errors.Is(priorErr, store.ErrNotFound) {
		return priorErr
	}
	if _, err = s.current(ctx, post, nil); err != nil {
		if priorErr == nil {
			next := prior
			next.NotBefore = time.Now().Add(5 * time.Minute).Unix()
			if saved := journal.SavePostFeedback(ctx, prior, next); saved != nil {
				return saved
			}
			return ErrPostFeedbackPending
		}
		return err
	}
	delivery, send, err := journal.BeginPostFeedback(ctx, intent)
	if err != nil {
		return err
	}
	if delivery.Complete {
		if delivery.Attention == "content_changed" {
			return ErrPostEffectReceipt
		}
		if delivery.Receipt != "" {
			return nil
		}
		return ErrPostEffectUnknown
	}
	if !send {
		if delivery.NotBefore > time.Now().Unix() {
			return ErrPostFeedbackPending
		}
		claimed := delivery
		claimed.NotBefore = time.Now().Add(5 * time.Minute).Unix()
		if err = journal.SavePostFeedback(ctx, delivery, claimed); err != nil {
			if errors.Is(err, store.ErrStaleRevision) {
				return ErrPostFeedbackPending
			}
			return err
		}
		delivery = claimed
	}
	before := func(ctx context.Context) error {
		latest, _, err := s.source(ctx, post.Record.Repository, post.Record.ID)
		if err != nil {
			return err
		}
		if latest.Record != post.Record || latest.Body != post.Body {
			return ErrPostEffectSource
		}
		return ctx.Err()
	}
	observed, err := writer.DeliverIssueFeedback(ctx, delivery, send, before)
	if !observed.Complete {
		observed.NotBefore = time.Now().Add(5 * time.Minute).Unix()
	}
	if observed.Validate() == nil && observed.Intent == delivery.Intent {
		if saveErr := journal.SavePostFeedback(ctx, delivery, observed); saveErr != nil {
			if observed.Receipt != "" && errors.Is(saveErr, store.ErrStaleRevision) {
				latest, lookupErr := journal.PostFeedback(ctx, intent.SourcePost, intent.Revision)
				if lookupErr == nil && latest.Intent == intent && latest.Receipt == "" && latest.Attention == "not_found" {
					observed.Cursor = max(observed.Cursor, latest.Cursor)
					return journal.SavePostFeedback(ctx, latest, observed)
				}
			}
			return saveErr
		}
	} else if err == nil {
		return ErrPostEffectReceipt
	}
	if observed.Attention == "content_changed" {
		return ErrPostEffectReceipt
	}
	if err != nil {
		if !observed.Complete {
			return ErrPostFeedbackPending
		}
		return err
	}
	if observed.Receipt != "" && observed.Complete {
		return nil
	}
	if observed.Complete {
		return ErrPostEffectUnknown
	}
	return ErrPostFeedbackPending
}
