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

// PostCapture stores provider-owned authorization and issue-context evidence.
// It grants no general repository/execution capability and starts no worker.
type PostCapture struct {
	Authorization func(context.Context, collaboration.ActorID, string, int64) error
	Context       func(context.Context, collaboration.ActorID, string, int64) error
}

type PostCommands struct {
	Posts   Posts
	Capture PostCapture
}

var (
	ErrPostInputInvalid       = errors.New("invalid feed post")
	ErrPostSourceNotFound     = errors.New("post source is not visible")
	ErrPostAuthorRequired     = errors.New("only the author can revise this request")
	ErrPostRetryConflict      = errors.New("post retry conflicts with its recorded state")
	ErrPostCaptureUnavailable = errors.New("post context capture is unavailable")
)

func (s PostCommands) Submit(ctx context.Context, actor collaboration.ActorID, authorName string, req factory.PostRequest) (factory.PostResponse, error) {
	var empty factory.PostResponse
	if err := req.Validate(); err != nil {
		return empty, errors.Join(ErrPostInputInvalid, err)
	}
	if err := s.Posts.visible(ctx, actor, req.Repository); err != nil {
		return empty, err
	}
	author := strconv.FormatInt(int64(actor), 10)
	id, revision, err := s.Posts.Store.FeedPostSubmission(ctx, req.Repository, author, req.IdempotencyKey)
	if errors.Is(err, store.ErrNotFound) {
		var source *factory.PostSource
		if req.ParentID != "" {
			resolved, err := s.Posts.Store.ResolvePostParent(ctx, req.Repository, req.ParentID)
			if errors.Is(err, store.ErrNotFound) {
				return empty, ErrPostSourceNotFound
			}
			if err != nil {
				return empty, err
			}
			source = resolved
			req.SourceKind, req.SourceID = resolved.Kind, resolved.ID
		}
		id, revision, err = s.Posts.Store.SaveFeedPostWithSource(ctx, req, author, authorName, time.Now().Unix(), source)
	}
	if err != nil {
		return empty, err
	}
	if err := s.Posts.Store.RecordIssueMakerIntake(ctx, id, revision); err != nil {
		return empty, err
	}
	if err := s.refresh(ctx, actor, id, revision, "Could not read this repository's issues using your current access. Retry the request."); err != nil {
		return empty, err
	}
	return s.response(ctx, id, true)
}

func (s PostCommands) retryAuthor(ctx context.Context, actor collaboration.ActorID, id string, repository int64) (string, error) {
	if actor <= 0 {
		return "", ErrPostNotFound
	}
	if s.Posts.Store == nil || s.Posts.Visibility == nil {
		return "", ErrPostViewUnavailable
	}
	detail, err := s.Posts.Store.FeedPost(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrPostNotFound
	}
	if err != nil {
		return "", err
	}
	if err := s.Posts.visible(ctx, actor, detail.Record.Repository); err != nil {
		return "", err
	}
	if detail.Record.Repository != repository {
		return "", ErrPostNotFound
	}
	if detail.Record.Author != strconv.FormatInt(int64(actor), 10) {
		return "", ErrPostAuthorRequired
	}
	return detail.Record.Author, nil
}

func (s PostCommands) Retry(ctx context.Context, actor collaboration.ActorID, id string, in factory.FeedPostRetryRequest) (factory.FeedPostRetryReceipt, error) {
	var empty factory.FeedPostRetryReceipt
	request := factory.FeedPostRetryCommand{ID: in.CommandID, Post: id, Repository: in.Repository, Author: strconv.FormatInt(int64(actor), 10), ExpectedRevision: in.ExpectedRevision, Body: in.Body}
	if err := request.Validate(); err != nil {
		return empty, errors.Join(ErrPostInputInvalid, err)
	}
	if _, err := s.retryAuthor(ctx, actor, id, in.Repository); err != nil {
		return empty, err
	}
	receipt, replayed, err := s.Posts.Store.RetryIssueMakerFeedPostCommand(ctx, request, time.Now().Unix())
	if err != nil {
		return empty, errors.Join(ErrPostRetryConflict, err)
	}
	if _, err := s.retryAuthor(ctx, actor, id, in.Repository); err != nil {
		return empty, err
	}
	if !replayed {
		// Only this newly committed transition owns best-effort capture. A
		// historical receipt never recaptures or mutates a later intake/head.
		message := "Could not refresh this request's issues using your current repository access. Try again."
		if err := s.refresh(ctx, actor, id, receipt.Revision, message); err != nil {
			if err := s.waitForContext(ctx, id, receipt.Revision, message); err != nil && !errors.Is(err, store.ErrStaleRevision) {
				return empty, err
			}
		}
	}
	if _, err := s.retryAuthor(ctx, actor, id, in.Repository); err != nil {
		return empty, err
	}
	return receipt, nil
}

// RetryReceipt is read-only recovery. An absent ID does not create a command or
// retry work, and a retained receipt cannot substitute for current authority.
func (s PostCommands) RetryReceipt(ctx context.Context, actor collaboration.ActorID, id, commandID string, repository int64) (factory.FeedPostRetryReceipt, error) {
	var empty factory.FeedPostRetryReceipt
	if !factory.ValidID(id) || !factory.ValidID(commandID) || repository <= 0 {
		return empty, ErrPostInputInvalid
	}
	author, err := s.retryAuthor(ctx, actor, id, repository)
	if err != nil {
		return empty, err
	}
	command, err := s.Posts.Store.FactoryCommand(ctx, commandID)
	if errors.Is(err, store.ErrNotFound) {
		return empty, ErrPostNotFound
	}
	if err != nil {
		return empty, err
	}
	receipt, err := factory.ReadFeedPostRetryReceipt(command, id, repository, author)
	if err != nil {
		return empty, ErrPostNotFound
	}
	if _, err := s.retryAuthor(ctx, actor, id, repository); err != nil {
		return empty, err
	}
	return receipt, nil
}

func (s PostCommands) refresh(ctx context.Context, actor collaboration.ActorID, id string, revision int64, message string) error {
	captured, err := s.Posts.Store.PostUsesIssueMaker(ctx, id, revision)
	if err != nil {
		return err
	}
	if captured && s.Capture.Authorization != nil {
		// Provider proof capture is best effort. The recorded captured path
		// cannot create an issue without that proof, and never becomes legacy.
		_ = s.Capture.Authorization(ctx, actor, id, revision)
	}
	err = ErrPostCaptureUnavailable
	if s.Capture.Context != nil {
		err = s.Capture.Context(ctx, actor, id, revision)
	}
	if err != nil {
		return s.waitForContext(ctx, id, revision, message)
	}
	return nil
}

func (s PostCommands) waitForContext(ctx context.Context, id string, revision int64, message string) error {
	return s.Posts.Store.NotePostContextWait(ctx, id, revision, PostStalledContext, message)
}

func (s PostCommands) response(ctx context.Context, id string, includeSource bool) (factory.PostResponse, error) {
	pipe, err := s.Posts.Store.PostIntake(ctx, id)
	if err != nil {
		return factory.PostResponse{}, err
	}
	response := PostResponse(pipe)
	if includeSource {
		detail, err := s.Posts.Store.FeedPost(ctx, id)
		if err != nil {
			return factory.PostResponse{}, err
		}
		response.Source = detail.Source
	}
	return response, nil
}
