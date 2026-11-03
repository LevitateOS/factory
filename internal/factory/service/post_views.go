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

// PostVisibility checks current access for the admitted canonical actor.
// Every served post/reply, including repeated repository scopes, is checked.
type PostVisibility func(context.Context, collaboration.ActorID, int64) error

type Posts struct {
	Store         *store.Store
	Visibility    PostVisibility
	WriteAccess   RequestWriteAccess
	Collaborators *PostCollaborators
}

var (
	ErrPostNotFound        = errors.New("post is not available")
	ErrPostViewUnavailable = errors.New("post view is unavailable")
	ErrPostVisibility      = errors.New("post repository is not visible")
	ErrPostRepliesInvalid  = errors.New("invalid conversation scope or cursor")
	ErrPostProductNotFound = errors.New("product change is not available")
)

type PostReplies struct {
	Items      []map[string]any `json:"items"`
	NextBefore int64            `json:"next_before"`
}

func (s Posts) visible(ctx context.Context, actor collaboration.ActorID, repository int64) error {
	if actor <= 0 {
		return ErrPostNotFound
	}
	if s.Store == nil || s.Visibility == nil {
		return ErrPostViewUnavailable
	}
	if err := s.Visibility(ctx, actor, repository); err != nil {
		return errors.Join(ErrPostVisibility, err)
	}
	return nil
}

// Read projects stored facts only. It cannot judge, draft, create an issue or
// dispatch work. Visibility is current even when a caller already read a parent.
func (s Posts) Read(ctx context.Context, actor collaboration.ActorID, id string) (map[string]any, error) {
	if actor <= 0 {
		return nil, ErrPostNotFound
	}
	if s.Store == nil || s.Visibility == nil {
		return nil, ErrPostViewUnavailable
	}
	entry, err := s.Store.FeedEntryByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrPostNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := s.visible(ctx, actor, entry.Repository); err != nil {
		return nil, err
	}
	if entry.Kind == factory.FeedKindRecord {
		entries := []factory.FeedEntry{entry}
		EnrichRecords(ctx, s.Store, nil, entries)
		EnrichParents(ctx, s.Store, entries)
		post := entries[0]
		return map[string]any{"post_id": post.ID, "kind": post.Kind, "repository": strconv.FormatInt(post.Repository, 10), "parent_id": post.ParentID, "parent_author": post.ParentAuthor, "author_name": post.Author, "actor": post.Actor, "at": post.At, "body": post.Summary, "status": post.Status, "issue_scope": post.Issue, "payload": post.Payload, "assignment": post.Assignment}, nil
	}
	detail, err := s.Store.FeedPost(ctx, entry.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrPostNotFound
	}
	if err != nil {
		return nil, err
	}
	if detail.Contribution != nil || detail.Consultation != nil || (detail.Payload != nil && detail.Payload.Communication != nil) {
		return WorkerPostResponse(detail), nil
	}
	pipe, err := s.Store.PostIntake(ctx, detail.Record.ID)
	if err != nil {
		return nil, err
	}
	progress, _ := s.Store.PostProgress(ctx, detail.Record.ID)
	return s.project(ctx, actor, detail, pipe, progress), nil
}

func (s Posts) Replies(ctx context.Context, actor collaboration.ActorID, repository int64, parent string, before int64) (PostReplies, error) {
	var empty PostReplies
	if repository <= 0 || before < 0 {
		return empty, ErrPostRepliesInvalid
	}
	if err := s.visible(ctx, actor, repository); err != nil {
		return empty, err
	}
	source, err := s.Store.ResolvePostParent(ctx, repository, parent)
	if errors.Is(err, store.ErrNotFound) {
		return empty, ErrPostNotFound
	}
	if err != nil {
		return empty, err
	}
	ids, next, err := s.Store.FeedReplyIDs(ctx, repository, source.Kind, source.ID, before)
	if err != nil {
		return empty, err
	}
	result := PostReplies{Items: []map[string]any{}, NextBefore: next}
	for _, id := range ids {
		item, err := s.Read(ctx, actor, id)
		if err != nil {
			return empty, err
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// ReviewProduct opens the source-bound draft through the existing store CAS.
// Current repository write authority and private intent ownership are separate
// prerequisites. This handoff does not accept, generate or execute work.
func (s Posts) ReviewProduct(ctx context.Context, actor collaboration.ActorID, id string, revision int64) (map[string]any, error) {
	if actor <= 0 {
		return nil, ErrPostProductNotFound
	}
	if s.Store == nil || s.Visibility == nil {
		return nil, ErrPostViewUnavailable
	}
	detail, err := s.Store.FeedPost(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrPostProductNotFound
	}
	if err != nil {
		return nil, err
	}
	if detail.Source == nil || detail.Source.Product == nil {
		return nil, ErrPostProductNotFound
	}
	if err := s.visible(ctx, actor, detail.Record.Repository); err != nil {
		return nil, err
	}
	requests := Requests{Store: s.Store, WriteAccess: s.WriteAccess}
	if err := requests.authorizeWrite(ctx, actor, detail.Record.Repository); err != nil {
		return nil, err
	}
	if _, err := requests.Revision(ctx, actor, detail.Source.Product.Intent); err != nil {
		if errors.Is(err, ErrRequestNotFound) {
			return nil, ErrPostProductNotFound
		}
		return nil, err
	}
	if _, err := s.Store.ReviewPostProductChange(ctx, id, revision, int64(actor), time.Now().Unix()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrPostProductNotFound
		}
		return nil, err
	}
	return s.Read(ctx, actor, id)
}

func (s Posts) canOpenProduct(ctx context.Context, actor collaboration.ActorID, repository int64, view *store.PostProductChange) bool {
	if view.State == "accepted" {
		_, err := (Requests{Store: s.Store}).Revision(ctx, actor, view.Intent)
		return err == nil
	}
	return s.WriteAccess != nil && s.WriteAccess(ctx, actor, repository) == nil
}

func (s Posts) project(ctx context.Context, actor collaboration.ActorID, detail store.FeedPostDetail, pipe factory.PostIntake, progress string) map[string]any {
	author := strconv.FormatInt(int64(actor), 10)
	response := feedPostResponse(detail, pipe, progress, author)
	if maker, err := s.Store.PostIssueMaker(ctx, detail.Record.ID, pipe.Revision); err == nil {
		response["issue_maker"] = map[string]any{"post": maker.Scope.Post, "state": maker.State, "detail": maker.Detail, "checked": maker.Checked}
	} else if pipe.Judgment == factory.PostJudgmentApplied && pipe.Route == factory.PostRouteNewIssue && (detail.Source == nil || detail.Source.Product == nil) {
		if captured, err := s.Store.PostUsesIssueMaker(ctx, detail.Record.ID, pipe.Revision); err == nil && captured {
			response["issue_maker"] = map[string]any{"post": "", "state": "queued"}
		}
	}
	if pipe.Route == factory.PostRouteNewIssue && pipe.Draft == factory.PostDraftPending {
		if authoring, err := s.Store.PostAuthoring(ctx, detail.Record.ID); err == nil && authoring != nil {
			copy := *authoring
			if detail.Record.Author != author {
				copy.Intent = ""
			}
			response["authoring"] = copy
			if copy.State == factory.PostAuthoringWaiting && copy.Run == "" && detail.Record.Author == author {
				response["can_retry"] = true
			}
		}
	}
	if pipe.AdmitPostJudgment() && detail.Record.Author == author && !detail.Record.Deleted && !detail.Record.Revoked {
		if _, err := s.Store.PostIssueContext(ctx, detail.Record.ID, detail.Record.Revision); err != nil {
			response["stalled"] = PostStalledContext
			response["can_retry"] = true
		}
	}
	if routing, err := s.Store.PostRouting(ctx, detail.Record.ID); err == nil && routing != nil {
		response["routing"] = routing
	}
	if !detail.Record.Deleted && !detail.Record.Revoked {
		if stalled := PostStalled(pipe, s.Collaborators); stalled != "" {
			response["stalled"] = stalled
		} else if stall, err := s.Store.PostStall(ctx, detail.Record.ID); err == nil && stall != "" && pipe.AdmitPostJudgment() && response["stalled"] == nil {
			response["stalled"] = stall
		}
	}
	if view, err := s.Store.PostProductChange(ctx, detail, pipe, int64(actor)); err == nil && view != nil {
		view.CanReview = view.CanReview && s.canOpenProduct(ctx, actor, detail.Record.Repository, view)
		response["product_change"] = view
		response["can_retry"] = false
	} else if err != nil && isPostProductLane(detail, pipe) {
		// The lane is same-product but the read failed: project the miss
		// explicitly so the comment never falls back to detached generic
		// progress that would claim unrelated native work.
		response["product_change"] = store.UnavailablePostProductChange(detail.Source.Product.Intent, detail.Source.Product.AcceptedRevision)
		response["can_retry"] = false
	}
	return response
}

// isPostProductLane reports whether the comment sits on the same-product
// review handoff: an accepted source behind an applied new-issue pipeline
// with no linked native issue. Only this lane may carry product_change, so
// only this lane maps a projection error to the explicit unavailable view.
func isPostProductLane(detail store.FeedPostDetail, pipe factory.PostIntake) bool {
	return detail.Source != nil && detail.Source.Product != nil &&
		pipe.Judgment == factory.PostJudgmentApplied && pipe.Route == factory.PostRouteNewIssue && pipe.Issue == 0
}

func feedPostResponse(detail store.FeedPostDetail, pipe factory.PostIntake, progress, actor string) map[string]any {
	return map[string]any{
		"post_id": detail.Record.ID, "revision": detail.Record.Revision,
		"repository": strconv.FormatInt(detail.Record.Repository, 10), "author": detail.Record.Author,
		"author_name": detail.AuthorName, "at": detail.At, "issue_scope": detail.Record.Issue,
		"parent_id": detail.Record.ParentID, "edited": detail.Record.Edited,
		"deleted": detail.Record.Deleted, "revoked": detail.Record.Revoked,
		"body": PostBody(detail), "status": store.PostStatus(&pipe),
		"intake": pipe.Intake, "judgment": pipe.Judgment, "route": pipe.Route,
		"draft": pipe.Draft, "effect": pipe.Effect, "issue": pipe.Issue,
		"existing_issue": PostExistingIssue(pipe), "source": detail.Source,
		"attempts": pipe.Attempts, "attempt_limit": factory.PostAttemptLimit,
		"detail":    progress,
		"can_retry": detail.Record.Author == actor && !detail.Record.Deleted && !detail.Record.Revoked && pipe.RetryAllowed(),
	}
}

func WorkerPostResponse(detail store.FeedPostDetail) map[string]any {
	var communication *factory.FeedCommunication
	if detail.Payload != nil {
		communication = detail.Payload.Communication
	}
	// The retained contribution keeps its int64 scope. Project the HTTP
	// identity as a string alongside the enclosing post's repository.
	var contribution any
	if detail.Contribution != nil {
		contribution = struct {
			*factory.ContributionReview
			Repository string `json:"repository"`
		}{detail.Contribution, strconv.FormatInt(detail.Contribution.Repository, 10)}
	}
	return map[string]any{
		"post_id": detail.Record.ID, "revision": detail.Record.Revision, "repository": strconv.FormatInt(detail.Record.Repository, 10),
		"author": detail.Record.Author, "author_name": detail.AuthorName, "actor": detail.Actor, "at": detail.At,
		"issue_scope": detail.Record.Issue, "parent_id": detail.Record.ParentID, "body": PostBody(detail),
		"status": detail.Status, "deleted": detail.Record.Deleted, "revoked": detail.Record.Revoked, "edited": false,
		"can_retry": false, "communication": communication, "consultation": detail.Consultation,
		"assignment": detail.Assignment, "source": detail.Source,
		"contribution": contribution,
	}
}
