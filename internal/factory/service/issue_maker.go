package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// IssueMakerEffects supplies current request-scoped authority and conditional
// issue effects. Implementations own provider proofs, receipts and native IDs.
type IssueMakerEffects interface {
	PostIssueCreator
	AuthorizeIssueMaker(context.Context, factory.IssueMakerScope) error
	RefineIssueMaker(context.Context, factory.IssueMakerScope, factory.IssueDraft) error
}

// IssueMaker captures the original request before funded AI refinement.
// Construction starts no work. Drive resumes the existing intake and journals.
type IssueMaker struct {
	Store     *store.Store
	Effects   IssueMakerEffects
	Transport PostPlanTransport
}

// Issue Maker captures first, then refines. Native creation is not held behind
// a model launch, source preparation or a product decomposition result.
func originalRequestIssue(comment store.FeedPostDetail) factory.IssueDraft {
	objective := comment.Body
	if len(objective) > 2048 {
		objective = "Implement the original request preserved in this issue."
	}
	title := PostDraftTitle(comment.Body)
	for len(title) > 255 || !utf8.ValidString(title) {
		title = title[:len(title)-1]
	}
	return factory.IssueDraft{
		SourcePost: comment.Record.ID, Revision: comment.Record.Revision, Title: title,
		Body:      comment.Body,
		Objective: objective, Acceptance: "Pending refinement from the original request.", Metadata: `{"source":"feed_comment","role":"issue_maker","refinement":"pending"}`,
	}
}

func (s *IssueMaker) Drive(ctx context.Context, pipe factory.PostIntake, comment store.FeedPostDetail) (factory.PostResponse, error) {
	progress, err := s.Store.StartPostIssueMaker(ctx, comment)
	if err != nil {
		return factory.PostResponse{}, err
	}
	save := func(state, detail string) error {
		progress.State, progress.Detail = state, detail
		return s.Store.SaveIssueMakerProgress(ctx, progress)
	}
	if pipe.Effect != factory.PostEffectVerified {
		if s.Effects == nil {
			return PostResponse(pipe), save("waiting", "Issue Maker is waiting for repository issue-write access. No issue has been created yet.")
		}
		if err := save("creating", "Recording your original request as an issue. AI refinement follows afterward."); err != nil {
			return factory.PostResponse{}, err
		}
		if pipe.AdmitPostDraft() {
			draft := originalRequestIssue(comment)
			if err = s.Store.RecordPostDraft(ctx, draft); err != nil {
				if errors.Is(err, store.ErrInvalidPostDraft) {
					if saveErr := save("failed", err.Error()); saveErr != nil {
						return factory.PostResponse{}, saveErr
					}
					return PostResponse(pipe), err
				}
				return factory.PostResponse{}, err
			}
			next := pipe
			next.Draft = factory.PostDraftApplied
			if err = s.Store.AdvancePostIntake(ctx, pipe, next); err != nil {
				return factory.PostResponse{}, err
			}
			pipe = next
		}
		creator := s.Effects
		if _, err = DrivePostEffect(ctx, s.Store, creator, pipe, comment); err != nil {
			return factory.PostResponse{}, err
		}
		pipe, err = s.Store.PostIntake(ctx, pipe.SourcePost)
		if err != nil {
			return factory.PostResponse{}, err
		}
		if pipe.Effect != factory.PostEffectVerified {
			next := pipe
			next.Effect = factory.PostEffectFailed
			if err = s.Store.AdvancePostIntake(ctx, pipe, next); err == nil {
				pipe = next
			}
			state := "waiting"
			if pipe.Attempts >= factory.PostAttemptLimit {
				state = "failed"
			}
			return PostResponse(pipe), save(state, "Issue Maker could not confirm native issue creation. Restore issue-write access and retry the original request; the recorded operation is reconciled before another creation.")
		}
	}
	if progress.Scope.Issue == 0 {
		draft, err := s.Store.PostDraft(ctx, pipe.SourcePost, pipe.Revision)
		if err != nil {
			return factory.PostResponse{}, err
		}
		if err = s.Store.RecordIssueMakerIssue(ctx, progress.Scope, "request", 0, pipe.Issue, draft.Title, PostIssueBody(draft)); err != nil {
			return factory.PostResponse{}, err
		}
		progress, err = s.Store.PostIssueMaker(ctx, pipe.SourcePost, pipe.Revision)
		if err != nil {
			return factory.PostResponse{}, err
		}
		// Return after the first durable receipt so it is visible before any
		// preparation or model call is attempted on the next service pass.
		return PostResponse(pipe), save("queued", fmt.Sprintf("Issue #%d is created. Issue Maker is queued to refine it; no AI run has started yet.", pipe.Issue))
	}
	if progress.State == "completed" || progress.State == "failed" {
		return PostResponse(pipe), nil
	}
	if s.Effects == nil {
		return PostResponse(pipe), save("waiting", "The issue exists. Issue Maker is waiting for repository issue-write access.")
	}
	if err := s.Effects.AuthorizeIssueMaker(ctx, progress.Scope); err != nil {
		return PostResponse(pipe), save("failed", "The issue exists. Its request-scoped write authorization expired before refinement; submit a follow-up request to continue.")
	}
	if s.Transport == nil {
		return PostResponse(pipe), save("waiting", "The initial issue exists. AI refinement is waiting for an available model account.")
	}
	drafter := &IntentPostDrafter{Store: s.Store, Transport: s.Transport, IssueMaker: &progress.Scope}
	draft, err := drafter.DraftPostIssue(ctx, comment)
	if err != nil {
		var pending *PostDraftPending
		if errors.As(err, &pending) {
			state := pending.Progress.State
			progress.Run = pending.Progress.Run
			detail := strings.ReplaceAll(pending.Progress.Detail, "Product Lead", "Issue Maker")
			if err := save(state, fmt.Sprintf("Issue #%d exists. %s", pipe.Issue, detail)); err != nil {
				return factory.PostResponse{}, err
			}
			return PostResponse(pipe), s.Store.NotePostAuthoring(ctx, pipe.SourcePost, pipe.Revision, pending.Progress, time.Now().Unix())
		}
		if ctx.Err() != nil {
			return PostResponse(pipe), ctx.Err()
		}
		return PostResponse(pipe), save("failed", fmt.Sprintf("Issue #%d exists, but refinement failed: %s", pipe.Issue, store.TruncateFeedSummary(err.Error())))
	}
	if err = draft.Validate(); err != nil {
		return PostResponse(pipe), save("failed", "The issue exists, but Issue Maker returned invalid refinement.")
	}
	if err = s.Effects.RefineIssueMaker(ctx, progress.Scope, draft); err != nil {
		// The provider may have committed before losing its reply. Keep this
		// exact operation eligible for recovery through the existing journal.
		return PostResponse(pipe), save("unknown", "The issue exists. Its refinement could not be confirmed; the recorded operation will be checked again: "+store.TruncateFeedSummary(err.Error()))
	}
	return PostResponse(pipe), save("completed", "Issue Maker finished. Each issue it created is listed in a comment below.")
}

// PostIssueBody renders the issue body: the recorded draft
// plus a bounded Soda footer preserving the source-comment link and
// the work metadata native creation validates.
func PostIssueBody(draft factory.IssueDraft) string {
	var footer strings.Builder
	footer.WriteString("\n\n---\nSoda-Comment: " + draft.SourcePost + " r" + strconv.FormatInt(draft.Revision, 10) + "\n")
	footer.WriteString("Objective: " + draft.Objective + "\n")
	footer.WriteString("Acceptance: " + draft.Acceptance + "\n")
	footer.WriteString("Work-Metadata: " + draft.Metadata + "\n")
	body := draft.Body + footer.String()
	if len(body) > 16384+4096 {
		body = body[:16384+4096]
	}
	return body
}
