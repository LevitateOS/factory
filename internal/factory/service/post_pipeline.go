package service

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// ErrPostDraftPending keeps an admitted planning attempt pending without spending another attempt.
var ErrPostDraftPending = errors.New("comment planning run still settling")

// PostDraftPending supplies retained authoring progress from a planning adapter.
type PostDraftPending struct{ Progress factory.PostAuthoring }

func (e *PostDraftPending) Error() string { return e.Progress.Detail }
func (e *PostDraftPending) Unwrap() error { return ErrPostDraftPending }

// ErrPostIssueContext reports unavailable current repository context without consuming a judgment attempt.
var ErrPostIssueContext = errors.New("Refresh this request to read its issues using your current repository access.")

const PostStalledContext = "context"

// PostStalled names a missing collaborator for the next admitted stage.
func PostStalled(pipe factory.PostIntake, wired *PostCollaborators) string {
	if wired == nil {
		wired = &PostCollaborators{}
	}
	switch {
	case pipe.AdmitPostJudgment() && wired.Judge == nil:
		return "jev"
	case pipe.AdmitPostDraft() && wired.Drafter == nil:
		return "product_lead"
	case pipe.AdmitPostEffect() && wired.Creator == nil:
		return "forge"
	}
	return ""
}

// PostBody hides the text of closed sources while keeping their
// tombstone resolvable.
func PostBody(detail store.FeedPostDetail) string {
	if detail.Record.Deleted || detail.Record.Revoked {
		return ""
	}
	return detail.Body
}

// DrivePostIntake advances one durable comment request through
// judgment, draft and native effect, reloading after every step so a
// concurrent advance wins honestly through CAS instead of being
// dropped. Each stage checks its own admission; a refused stage waits
// in pending without spending.
func DrivePostIntake(ctx context.Context, st *store.Store, wired *PostCollaborators, id string) (factory.PostResponse, error) {
	var empty factory.PostResponse
	if st == nil {
		return empty, errors.New("comment store is unavailable")
	}
	// One drive flows a fresh submission through every wired stage
	// but consumes at most one attempt per stage: a failed call waits
	// for the next replay instead of burning the bounded retries in one
	// request. A concurrent advance wins honestly through CAS; the
	// response below always reloads stored truth.
	for range 8 {
		pipe, err := st.PostIntake(ctx, id)
		if err != nil {
			return empty, err
		}
		detail, err := st.FeedPost(ctx, id)
		if err != nil {
			return empty, err
		}
		if detail.Record.Deleted || detail.Record.Revoked {
			// The write path already settles the effect as blocked;
			// report the stored states without spending anything.
			return PostResponse(pipe), nil
		}
		if detail.Source != nil && detail.Source.Product != nil && pipe.Judgment == factory.PostJudgmentApplied && pipe.Route == factory.PostRouteNewIssue {
			// Product revisions enter the existing explicit review/adoption
			// owner. Creating a detached issue here would duplicate its graph.
			return PostResponse(pipe), nil
		}
		if pipe.Judgment == factory.PostJudgmentApplied && pipe.Route == factory.PostRouteNewIssue {
			captured, err := st.PostUsesIssueMaker(ctx, id, pipe.Revision)
			if err != nil {
				return empty, err
			}
			if captured {
				if wired == nil || wired.IssueMaker == nil {
					_ = st.NotePostProgress(ctx, id, "Issue Maker is unavailable. Restore its connection to continue this saved request.")
					return PostResponse(pipe), nil
				}
				return wired.IssueMaker(ctx, pipe, detail)
			}
		}
		var advanced bool
		switch {
		case pipe.AdmitPostJudgment() && wired != nil && wired.Judge != nil:
			advanced, err = drivePostJudgment(ctx, st, wired, pipe, detail)
		case pipe.AdmitPostDraft() && wired != nil && wired.Drafter != nil:
			advanced, err = drivePostDraft(ctx, st, wired.Drafter, pipe, detail)
		case pipe.AdmitPostEffect() && wired != nil && wired.Creator != nil:
			advanced, err = DrivePostEffect(ctx, st, wired.Creator, pipe, detail)
		case pipe.Route == factory.PostRouteExistingWork && pipe.Judgment == factory.PostJudgmentApplied && pipe.Effect == factory.PostEffectPending && pipe.Attempts < factory.PostAttemptLimit && wired != nil:
			advanced, err = drivePostFeedback(ctx, st, wired.Creator, pipe, detail)
		default:
			return PostResponse(pipe), nil
		}
		if err != nil {
			// A concurrent advance wins honestly: report its stored
			// outcome instead of failing the replay.
			if errors.Is(err, store.ErrStaleRevision) {
				fresh, reloadErr := st.PostIntake(ctx, id)
				if reloadErr != nil {
					return empty, reloadErr
				}
				return PostResponse(fresh), nil
			}
			return empty, err
		}
		if !advanced {
			fresh, err := st.PostIntake(ctx, id)
			if err != nil {
				return empty, err
			}
			return PostResponse(fresh), nil
		}
	}
	pipe, err := st.PostIntake(ctx, id)
	if err != nil {
		return empty, err
	}
	return PostResponse(pipe), nil
}

// postCandidateLimit bounds the existing-work candidates one
// routing judgment may weigh.
const postCandidateLimit = 8

// CollectPostJudgeInput assembles one judgment's complete input
// from recorded state: the comment with its persisted source
// snapshot, its reply parent's recorded evidence, and the
// existing-work candidates merged bound-first (bound issue scope,
// source snapshot, reply parent, recent repository work), deduped by
// issue with recorded summaries filled from whichever origin has
// them. A failed recent-work read fails the judgment instead of
// routing blind; an unreadable or closed parent simply contributes
// no evidence.
func CollectPostJudgeInput(ctx context.Context, st *store.Store, detail store.FeedPostDetail) (PostJudgeInput, error) {
	input := PostJudgeInput{SourcePost: detail}
	recent, err := st.RecentPostWorkCandidates(ctx, detail.Record.Repository, postCandidateLimit)
	if err != nil {
		return PostJudgeInput{}, err
	}
	byIssue := map[int64]*factory.PostWorkCandidate{}
	var ordered []*factory.PostWorkCandidate
	add := func(candidate factory.PostWorkCandidate) {
		if candidate.Issue <= 0 {
			return
		}
		if existing, ok := byIssue[candidate.Issue]; ok {
			if existing.Summary == "" {
				existing.Summary = candidate.Summary
			}
			if existing.Status == "" {
				existing.Status = candidate.Status
			}
			return
		}
		merged := candidate
		byIssue[candidate.Issue] = &merged
		ordered = append(ordered, &merged)
	}
	if detail.Record.Issue > 0 {
		add(factory.PostWorkCandidate{Issue: detail.Record.Issue, Origin: factory.PostCandidateBound})
	}
	if detail.Source != nil && detail.Source.Issue > 0 {
		add(factory.PostWorkCandidate{
			Issue: detail.Source.Issue, Summary: detail.Source.Summary,
			Status: detail.Source.Status, Origin: factory.PostCandidateSource,
		})
	}
	if detail.Record.ParentID != "" {
		parent := &PostParentEvidence{SourcePost: detail.Record.ParentID}
		if parentDetail, err := st.FeedPost(ctx, detail.Record.ParentID); err == nil &&
			parentDetail.Record.Repository == detail.Record.Repository &&
			!parentDetail.Record.Deleted && !parentDetail.Record.Revoked {
			parent.Body = store.TruncateFeedSummary(parentDetail.Body)
			parent.Issue = parentDetail.Record.Issue
		}
		if parentPipe, err := st.PostIntake(ctx, detail.Record.ParentID); err == nil && parent.Issue <= 0 {
			parent.Issue = parentPipe.Issue
		}
		input.Parent = parent
		if parent.Issue > 0 {
			add(factory.PostWorkCandidate{Issue: parent.Issue, Origin: factory.PostCandidateParent})
		}
	}
	for _, candidate := range recent {
		add(candidate)
	}
	for _, candidate := range ordered {
		if err := candidate.Validate(); err != nil {
			return PostJudgeInput{}, err
		}
		input.Candidates = append(input.Candidates, *candidate)
		if len(input.Candidates) == postCandidateLimit {
			break
		}
	}
	return input, nil
}

// applyPostJudgment advances one pipeline with a settled routing
// outcome. An existing_work judgment persists which work it selected
// in the pipeline's issue link; every other route clears it.
func drivePostFeedback(ctx context.Context, st *store.Store, creator PostIssueCreator, pipe factory.PostIntake, detail store.FeedPostDetail) (bool, error) {
	requested, err := st.PostFeedbackRequested(ctx, pipe.SourcePost, pipe.Revision)
	if err != nil || !requested {
		return false, err
	}
	writer, ok := creator.(interface {
		DeliverPostFeedback(context.Context, store.FeedPostDetail, int64) error
	})
	if !ok {
		_ = st.NotePostProgress(ctx, pipe.SourcePost, "Feedback delivery is unavailable. Restore Factory’s connection before retrying.")
		return false, nil
	}
	if err = writer.DeliverPostFeedback(ctx, detail, pipe.Issue); err != nil {
		_ = st.NotePostProgress(ctx, pipe.SourcePost, "Could not confirm delivery to the issue discussion. Check repository access and refresh before retrying.")
		return false, notePostFailure(ctx, st, pipe, false)
	}
	next := pipe
	next.Effect = factory.PostEffectApplied
	if err = st.AdvancePostIntake(ctx, pipe, next); err != nil {
		return false, err
	}
	_ = st.NotePostProgress(ctx, pipe.SourcePost, "Feedback added to the issue discussion. Accepted requirements have not been changed.")
	return true, nil
}

func applyPostJudgment(ctx context.Context, st *store.Store, pipe factory.PostIntake, judgment PostJudgment) error {
	next := pipe
	next.Judgment = factory.PostJudgmentApplied
	next.Route = judgment.Route
	next.Issue = 0
	if judgment.Route == factory.PostRouteExistingWork {
		next.Issue = judgment.Issue
	}
	if err := st.AdvancePostIntake(ctx, pipe, next); err != nil {
		return err
	}
	if judgment.Route == factory.PostRouteNewIssue {
		_ = st.NotePostAuthoring(ctx, pipe.SourcePost, pipe.Revision, factory.PostAuthoring{State: factory.PostAuthoringQueued, Detail: "JEV identified new work. Waiting for the Product Lead to prepare the issue."}, time.Now().Unix())
	}
	if judgment.Routing != nil {
		if err := st.NotePostRouting(ctx, pipe.SourcePost, *judgment.Routing); err != nil {
			return err
		}
	}
	if judgment.Route == factory.PostRouteClarify {
		question := judgment.Question
		if question == "" {
			question = postClarificationQuestions["clarify_result"]
		}
		return st.NotePostProgress(ctx, pipe.SourcePost, question)
	}
	return nil
}

// drivePostJudgment routes one comment with one JEV Choice and stores
// the answer on the request row. A failed call retries on a later pass,
// bounded by the attempt count.
func drivePostJudgment(ctx context.Context, st *store.Store, wired *PostCollaborators, pipe factory.PostIntake, detail store.FeedPostDetail) (bool, error) {
	// Gathering context reads this server and Forge only; a failure there
	// (Forge restarting, say) spends no JEV attempt. The request waits
	// with the reason visible and the next pass reads again.
	input, err := CollectPostJudgeInput(ctx, st, detail)
	if err != nil {
		_ = st.NotePostProgress(ctx, pipe.SourcePost, "The original request context could not be read yet. It is read again shortly.")
		return false, nil
	}
	if wired.Prepare != nil {
		if input, err = wired.Prepare(ctx, input); err != nil {
			_ = st.NotePostProgress(ctx, pipe.SourcePost, err.Error())
			stall := ""
			if errors.Is(err, ErrPostIssueContext) {
				stall = PostStalledContext
			}
			_ = st.NotePostStall(ctx, pipe.SourcePost, stall)
			return false, nil
		}
	}
	_ = st.NotePostStall(ctx, pipe.SourcePost, "")
	judgment, err := wired.Judge.JudgePost(ctx, input)
	if err != nil {
		_ = st.NotePostProgress(ctx, pipe.SourcePost, "JEV could not assess this request. Check the System One connection and retry assessment.")
		return false, notePostFailure(ctx, st, pipe, true)
	}
	switch judgment.Route {
	case factory.PostRouteNewIssue, factory.PostRouteDiscussion, factory.PostRouteClarify:
		if judgment.Issue != 0 {
			return false, notePostFailure(ctx, st, pipe, true)
		}
	case factory.PostRouteExistingWork:
		if _, err := PostJudgmentFromSelection(postExistingChoice(judgment.Issue), input.Candidates); err != nil {
			return false, notePostFailure(ctx, st, pipe, true)
		}
	default:
		return false, notePostFailure(ctx, st, pipe, true)
	}
	if err := applyPostJudgment(ctx, st, pipe, judgment); err != nil {
		return false, err
	}
	return true, nil
}

// drivePostDraft runs one Product Lead LLM authoring call for a
// new_issue route. A malformed draft settles the draft as invalid
// before any native effect; a failed call keeps its attempt for retry.
func drivePostDraft(ctx context.Context, st *store.Store, drafter PostDrafter, pipe factory.PostIntake, detail store.FeedPostDetail) (bool, error) {
	draft, err := drafter.DraftPostIssue(ctx, detail)
	if err != nil {
		var pending *PostDraftPending
		if errors.As(err, &pending) {
			_ = st.NotePostAuthoring(ctx, pipe.SourcePost, pipe.Revision, pending.Progress, time.Now().Unix())
		} else if ctx.Err() == nil {
			state := factory.PostAuthoringFailed
			if errors.Is(err, ErrPostDraftPending) {
				state = factory.PostAuthoringUnknown
			}
			_ = st.NotePostAuthoring(ctx, pipe.SourcePost, pipe.Revision, factory.PostAuthoring{State: state, Detail: err.Error()}, time.Now().Unix())
		}
		// A launched planning run that is still settling — or a drive
		// interrupted by service shutdown — is not an authoring
		// failure: the draft stays pending with its bounded attempts
		// intact and the next service pass resumes the same funded
		// attempt instead of spending again.
		if errors.Is(err, ErrPostDraftPending) || ctx.Err() != nil {
			_ = st.NotePostProgress(ctx, pipe.SourcePost, err.Error())
			return false, nil
		}
		_ = st.NotePostProgress(ctx, pipe.SourcePost, err.Error())
		return false, notePostFailure(ctx, st, pipe, false)
	}
	draft.SourcePost = pipe.SourcePost
	draft.Revision = pipe.Revision
	if err := st.RecordPostDraft(ctx, draft); err != nil {
		if !errors.Is(err, store.ErrInvalidPostDraft) {
			return false, err
		}
		next := pipe
		next.Draft = factory.PostDraftInvalid
		next.Effect = factory.PostEffectBlocked
		if advanceErr := st.AdvancePostIntake(ctx, pipe, next); advanceErr != nil {
			return false, advanceErr
		}
		return true, nil
	}
	next := pipe
	next.Draft = factory.PostDraftApplied
	if err := st.AdvancePostIntake(ctx, pipe, next); err != nil {
		return false, err
	}
	return true, nil
}

// DrivePostEffect creates the native issue behind an applied draft.
// It reconciles first: an issue already created from this comment (lost
// effect reply) settles without duplicating, and the graph operation is
// idempotent. The draft must still bind the current source revision. An
// unavailable lookup is unknown and cannot authorize another creation.
func DrivePostEffect(ctx context.Context, st *store.Store, creator PostIssueCreator, pipe factory.PostIntake, detail store.FeedPostDetail) (bool, error) {
	if st == nil || creator == nil {
		return false, errors.New("post issue creator is unavailable")
	}
	if detail.Record.Revision != pipe.Revision {
		return false, nil
	}
	draft, err := st.PostDraft(ctx, pipe.SourcePost, pipe.Revision)
	if err != nil {
		return false, notePostFailure(ctx, st, pipe, false)
	}
	issue, err := creator.LookupPostIssue(ctx, detail.Record.Repository, pipe.SourcePost)
	if err == nil && issue <= 0 {
		issue, err = creator.CreatePostIssue(ctx, draft, detail.Record.Repository)
	}
	if err != nil || issue <= 0 {
		_ = st.NotePostProgress(ctx, pipe.SourcePost, "Could not confirm issue creation. Retry checks for an existing issue before creating one.")
		return false, notePostFailure(ctx, st, pipe, false)
	}
	next := pipe
	next.Effect = factory.PostEffectVerified
	next.Issue = issue
	if err := st.AdvancePostIntake(ctx, pipe, next); err != nil {
		return false, err
	}
	return true, nil
}

// notePostFailure records one retry attempt; when the bounded
// attempts run out the open stage settles honestly (judgment failed,
// otherwise the draft/effect stays retryable only while attempts
// remain, and the caller sees pending until then).
func notePostFailure(ctx context.Context, st *store.Store, pipe factory.PostIntake, failJudgment bool) error {
	if err := st.NotePostAttempt(ctx, pipe.SourcePost); err != nil {
		if failJudgment {
			next := pipe
			next.Judgment = factory.PostJudgmentFailed
			_ = st.AdvancePostIntake(ctx, pipe, next)
		}
		return nil
	}
	fresh, err := st.PostIntake(ctx, pipe.SourcePost)
	if err != nil {
		return err
	}
	if fresh.Attempts < factory.PostAttemptLimit {
		return nil
	}
	next := fresh
	if failJudgment && next.Judgment == factory.PostJudgmentPending {
		next.Judgment = factory.PostJudgmentFailed
	}
	if advanceErr := st.AdvancePostIntake(ctx, fresh, next); advanceErr != nil {
		return advanceErr
	}
	return nil
}

// postExistingIssue names the already-tracked issue one pipeline
// linked on the existing_work route, zero on every other route.
func PostExistingIssue(pipe factory.PostIntake) int64 {
	if pipe.Route == factory.PostRouteExistingWork {
		return pipe.Issue
	}
	return 0
}

// PostResponse renders one stored pipeline row as the POST answer.
func PostResponse(pipe factory.PostIntake) factory.PostResponse {
	return factory.PostResponse{
		Attempts: pipe.Attempts, AttemptLimit: factory.PostAttemptLimit,
		SourcePost: pipe.SourcePost, Revision: pipe.Revision, Intake: pipe.Intake,
		Judgment: pipe.Judgment, Route: pipe.Route, Draft: pipe.Draft,
		Effect: pipe.Effect, Issue: pipe.Issue, ExistingIssue: PostExistingIssue(pipe),
	}
}

// PostDriveLimit bounds the stored comment requests one service
// pass advances: authoring and native effects are expensive, and the
// coordinator pass must stay responsive to factory work.
const PostDriveLimit = 4

// DrivePendingPosts advances one bounded page of durable comment
// requests through their wired stages and reports how many drove
// without error. Per-request failures keep their stored state for a
// later pass; exactly-once advancement rests on the pipeline CAS, the
// single funded planning attempt and the deterministic native operation
// identity.
func DrivePendingPosts(ctx context.Context, st *store.Store, wired *PostCollaborators, limit int) int {
	if st == nil {
		return 0
	}
	pending, err := st.PendingPostIntakes(ctx, limit)
	if err != nil {
		return 0
	}
	driven := 0
	for _, pipe := range pending {
		if ctx.Err() != nil {
			break
		}
		_ = st.NotePostProgress(ctx, pipe.SourcePost, "")
		if _, err := DrivePostIntake(ctx, st, wired, pipe.SourcePost); err == nil {
			driven++
		}
	}
	return driven
}
