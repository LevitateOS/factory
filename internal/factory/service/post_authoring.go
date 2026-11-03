package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/control"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

// Captured requests use the dedicated Issue Maker profile for AI refinement
// after their initial native receipt. Retained pre-capture requests continue
// their recorded Product Lead planning transport without rewriting history.
//
// There is no synchronous free-text LLM client in this codebase: the
// TypeSafe transport answers Choice judgments only (free, used for
// routing), and the one real paid LLM authoring transport is the
// intent-generation funding path — a funded decompose assignment
// answered by the enrolled host model account through
// AssembleIntentPlanningInput + DispatchIntentPlan, settled as a
// typed SemanticPlannerOutput. This drafter reuses exactly that
// path. Paid authoring accounting stays where the intent path puts
// it (the dispatch packet's intent attempt allowance and run usage),
// separate from free JEV judgment reservations. No new transport and
// no deterministic draft fabrication: when the planner offers no
// usable answer, drafting fails honestly and the pipeline waits.
//
// One comment source revision binds one durable draft intent through
// deterministic identities derived from the comment, so a retry, a
// lost response or a restart resumes the same intent and its single
// funded planning attempt instead of spending twice: the packet
// refuses a second funded attempt for the same intent/revision/owner
// triple, and this drafter reuses the recorded attempt whenever one
// exists. A multi-candidate answer refuses as ambiguous rather than
// silently taking the first candidate — choosing among semantic
// alternatives needs a funded judgment, and the accepted-direction
// chooser does not serve a draft comment intent (its own contract).
// An attention answer likewise refuses: the planner asked for a
// product decision a comment draft cannot invent.

// PostDraftStore is the narrow store joint behind comment
// authoring. *store.Store implements it; tests stub it.
type PostDraftStore interface {
	IntentRevision(ctx context.Context, intent string, owner int64) (factory.IntentRevision, error)
	SaveIntentRevision(ctx context.Context, r factory.IntentRevision) error
	IntentPlanAttempt(ctx context.Context, intentID, revisionID string) (factory.Assignment, error)
	FactoryRun(ctx context.Context, id string) (factory.Run, error)
}

// PostPlanTransport is the existing intent-generation funding and
// launch transport. *control.Coordinator implements it; tests stub
// it with a fake transport so authoring logic runs without enrolled
// model credentials.
type PostPlanTransport interface {
	AssembleIntentPlanningInput(ctx context.Context, head factory.IntentRevision, operation string) (factory.SemanticPlannerInput, project.Preparation, *control.DispatchWait, error)
	DispatchIntentPlan(ctx context.Context, intentID string, owner int64, operation string, in factory.SemanticPlannerInput, source, preparationID string) control.DispatchReport
}

var (
	_ PostDraftStore    = (*store.Store)(nil)
	_ PostPlanTransport = (*control.Coordinator)(nil)
	_ PostDrafter       = (*IntentPostDrafter)(nil)
)

func pendingPostDraft(state, reason, detail, intent, run string, started int64) error {
	return &PostDraftPending{Progress: factory.PostAuthoring{State: state, Reason: reason, Detail: detail, Intent: intent, Run: run, StartedUnix: started}}
}

// IntentPostDrafter authors issue drafts from feed comments
// through the funded Product Lead planning path.
type IntentPostDrafter struct {
	IssueMaker *factory.IssueMakerScope
	Store      PostDraftStore
	Transport  PostPlanTransport
	// Now overrides the clock for tests.
	Now func() time.Time
}

// postSourceContext renders the recorded work context one
// comment was submitted in — its resolved source snapshot and/or its
// bound issue scope — as planner-facing evidence text. Only
// server-recorded facts render; an unbound top-level comment has no
// context to add.
func postSourceContext(comment store.FeedPostDetail) string {
	var facts []string
	if source := comment.Source; source != nil {
		fact := "Submitted against " + source.Kind + " " + source.ID
		if source.Issue > 0 {
			fact += " tracking issue #" + strconv.FormatInt(source.Issue, 10)
		}
		if source.PR > 0 {
			fact += " via pull request #" + strconv.FormatInt(source.PR, 10)
		}
		if source.Status != "" {
			fact += " (status " + source.Status + ")"
		}
		if source.Summary != "" {
			fact += ": " + source.Summary
		}
		facts = append(facts, fact)
	}
	if comment.Record.Issue > 0 && (comment.Source == nil || comment.Source.Issue != comment.Record.Issue) {
		facts = append(facts, "Bound to native issue #"+strconv.FormatInt(comment.Record.Issue, 10)+".")
	}
	return strings.Join(facts, " ")
}

// postDraftIntent builds the durable draft intent one comment
// source revision authors against: the human request as one accepted
// requested idea, bounded to a single derived issue. The framing
// consequence states only what the pipeline itself does with the
// request; every semantic word of the draft comes from the planner.
// When the submission carried recorded work context, one additional
// pending evidence-supported idea states that context so the planner
// authors inside the actual project work; it is never an effective
// outcome and candidate tasks never have to cover it.
func postDraftIntent(comment store.FeedPostDetail, author int64, createdUnix int64) (factory.IntentRevision, error) {
	body := comment.Body
	if utf8.RuneCountInString(body) > 4000 {
		return factory.IntentRevision{}, errors.New("comment body exceeds the planning idea bound")
	}
	postID, revision := comment.Record.ID, comment.Record.Revision
	ideaID := factory.PostDecisionID("idea", postID, revision)
	ideas := []factory.ProductIdea{{
		ID: ideaID, Description: body,
		Consequence: "The requested change is tracked as work and completed.",
		Decision:    "accept", Source: "requested", Provenance: "human", Actor: author,
	}}
	if context := postSourceContext(comment); context != "" {
		ideas = append(ideas, factory.ProductIdea{
			ID: factory.PostDecisionID("source", postID, revision), Description: context,
			Consequence: "Authored work fits alongside this existing tracked work.",
			Decision:    "pending", Source: "supported", Provenance: "autonomous", Evidence: context,
		})
	}
	head := factory.IntentRevision{
		ID:         factory.PostDecisionID("revision", postID, revision),
		Intent:     factory.PostDecisionID("intent", postID, revision),
		Owner:      author,
		Repository: comment.Record.Repository,
		Objective:  body,
		Ideas:      ideas,
		State:      "draft", MaxIssues: 1, CreatedUnix: createdUnix,
		EffectiveOutcomes: []string{ideaID},
	}
	if err := head.Validate(); err != nil {
		return factory.IntentRevision{}, err
	}
	return head, nil
}

// ensurePostDraftIntent loads the comment's durable draft intent,
// saving it first when this drive is its first authoring attempt. A
// concurrent save of the identical deterministic revision replays
// through the read-back instead of failing the drive.
func (d *IntentPostDrafter) ensurePostDraftIntent(ctx context.Context, comment store.FeedPostDetail, author int64) (factory.IntentRevision, error) {
	intentID := factory.PostDecisionID("intent", comment.Record.ID, comment.Record.Revision)
	if head, err := d.Store.IntentRevision(ctx, intentID, author); err == nil {
		return head, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return factory.IntentRevision{}, err
	}
	created := comment.At
	if created <= 0 {
		created = d.now().Unix()
	}
	head, err := postDraftIntent(comment, author, created)
	if err != nil {
		return factory.IntentRevision{}, err
	}
	if err := d.Store.SaveIntentRevision(ctx, head); err != nil {
		if saved, readErr := d.Store.IntentRevision(ctx, intentID, author); readErr == nil {
			return saved, nil
		}
		return factory.IntentRevision{}, err
	}
	return head, nil
}

func (d *IntentPostDrafter) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// DraftPostIssue authors one issue draft from a routed comment:
// ensure the comment-bound draft intent, reuse or launch its one
// funded planning attempt, return pending until settlement, then map
// the single assessed candidate task to the issue draft. The caller
// (drivePostDraft) validates the draft before any native effect
// and settles malformed drafts invalid with their bytes wiped.
func (d *IntentPostDrafter) DraftPostIssue(ctx context.Context, comment store.FeedPostDetail) (factory.IssueDraft, error) {
	var empty factory.IssueDraft
	if d == nil || d.Store == nil || d.Transport == nil {
		return empty, errors.New("comment authoring transport is unavailable")
	}
	author, err := strconv.ParseInt(comment.Record.Author, 10, 64)
	if err != nil || author <= 0 {
		return empty, errors.New("comment author is not a canonical owner")
	}
	if comment.Record.Repository <= 0 || strings.TrimSpace(comment.Body) == "" {
		return empty, errors.New("comment draft needs its repository and body")
	}
	head, err := d.ensurePostDraftIntent(ctx, comment, author)
	if err != nil {
		return empty, err
	}
	attempt, err := d.postPlanAttempt(ctx, head, author)
	if err != nil {
		return empty, err
	}
	// The coordinator also settles this run. Returning pending lets its
	// next progress pass do that work instead of waiting inside its lock.
	if attempt.Stage != factory.AssignmentFinished || attempt.Result == nil {
		if attempt.Stage == factory.AssignmentFinished {
			return empty, pendingPostDraft(factory.PostAuthoringSettling, "result_pending", "The Product Lead run ended. Waiting for its recorded result.", head.Intent, attempt.Run, attempt.CreatedUnix)
		}
		if attempt.Run == "" {
			return empty, pendingPostDraft(factory.PostAuthoringQueued, "launch_pending", "The Product Lead is queued. No run has started yet.", head.Intent, "", 0)
		}
		run, err := d.Store.FactoryRun(ctx, attempt.Run)
		if err != nil {
			return empty, pendingPostDraft(factory.PostAuthoringUnknown, "status_unavailable", "The Product Lead run status is unavailable. It will be checked again.", head.Intent, attempt.Run, 0)
		}
		if run.ContextPending {
			return empty, pendingPostDraft(factory.PostAuthoringQueued, "launch_pending", "The Product Lead is queued. No run has started yet.", head.Intent, attempt.Run, 0)
		}
		if run.Outcome != "" {
			return empty, pendingPostDraft(factory.PostAuthoringSettling, "result_pending", "The Product Lead run ended. Its result is being checked before creating an issue.", head.Intent, attempt.Run, run.Started.Unix())
		}
		return empty, pendingPostDraft(factory.PostAuthoringRunning, "run_started", "A Product Lead run has started. Waiting for its result.", head.Intent, attempt.Run, run.Started.Unix())
	}
	switch attempt.Outcome {
	case factory.Failed:
		return empty, fmt.Errorf("comment planning run failed: %s", attempt.Result.Summary)
	case factory.Cancelled:
		return empty, fmt.Errorf("comment planning run was cancelled: %s", attempt.Result.Summary)
	}
	return postDraftFromAttempt(comment, head, attempt)
}

// postPlanAttempt returns the recorded planning attempt behind
// the comment intent, launching it through the funded transport when
// none exists yet. A launch that lands as a wait or a duplicate
// refusal reconciles through the recorded attempt instead of
// launching again.
func (d *IntentPostDrafter) postPlanAttempt(ctx context.Context, head factory.IntentRevision, author int64) (factory.Assignment, error) {
	var empty factory.Assignment
	if attempt, err := d.Store.IntentPlanAttempt(ctx, head.Intent, head.ID); err == nil {
		return attempt, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return empty, err
	}
	in, prep, wait, err := d.Transport.AssembleIntentPlanningInput(ctx, head, factory.IntentPlanningPropose)
	if err != nil {
		return empty, err
	}
	if wait != nil {
		return empty, pendingPostDraft(factory.PostAuthoringWaiting, wait.Reason, wait.Detail, head.Intent, "", 0)
	}
	if d.IssueMaker != nil {
		in.IssueMaker = d.IssueMaker
		in.RequireWalkthroughs = false
	}
	report := d.Transport.DispatchIntentPlan(ctx, head.Intent, author, factory.IntentPlanningPropose, in, prep.SourceCommit, prep.ID)
	// A launch error can follow a committed funded attempt. Recover that
	// attempt before interpreting the report so an unknown reply stays pending.
	attempt, err := d.Store.IntentPlanAttempt(ctx, head.Intent, head.ID)
	if err != nil {
		if len(report.Errors) != 0 {
			return empty, errors.New(report.Errors[0].Detail)
		}
		if len(report.Waits) != 0 {
			return empty, pendingPostDraft(factory.PostAuthoringWaiting, report.Waits[0].Reason, report.Waits[0].Detail, head.Intent, "", 0)
		}
		return empty, err
	}
	return attempt, nil
}

// postDraftFromAttempt maps one settled planning attempt to the
// issue draft its single assessed candidate authors. The answer is
// re-assessed against its exact staged input here, so replaying the
// same recorded attempt reaches the same draft and spends nothing.
// Attention, multi-candidate and multi-task answers refuse: none can
// become one issue draft without an authority this stage lacks. A
// mapped draft that exceeds the issue-draft bounds returns as-is so
// the caller's validation settles it invalid before any effect.
func postDraftFromAttempt(comment store.FeedPostDetail, head factory.IntentRevision, attempt factory.Assignment) (factory.IssueDraft, error) {
	var empty factory.IssueDraft
	if attempt.Subject.Intent == nil || attempt.Subject.Intent.OwnerID != head.Owner {
		return empty, errors.New("recorded comment plan answers another owner")
	}
	in, staged, err := control.ParseStagedPlanningInput(attempt.Prompt)
	if err != nil || !staged {
		return empty, errors.New("recorded comment planning input is missing or corrupt")
	}
	if in.Head.Intent != head.Intent || in.Head.ID != head.ID {
		return empty, errors.New("recorded comment plan answers another intent revision")
	}
	result := attempt.Result
	if result == nil || !result.Reported || result.PhaseResult == nil ||
		result.PhaseResult.Status != "completed" || result.PhaseResult.Output == nil ||
		result.PhaseResult.Output.Decomposition == nil {
		return empty, errors.New("recorded comment plan carries no completed decomposition")
	}
	out := *result.PhaseResult.Output.Decomposition
	if out.Attention != "" {
		return empty, fmt.Errorf("comment planning needs a product decision first: %s", out.Attention)
	}
	if err := factory.AssessSemanticOutput(in, out); err != nil {
		return empty, err
	}
	if len(out.Candidates) != 1 {
		return empty, fmt.Errorf("comment planning offered %d candidates; choosing among them needs a funded judgment", len(out.Candidates))
	}
	candidate := out.Candidates[0]
	if len(candidate.Tasks) != 1 {
		return empty, fmt.Errorf("comment planning offered %d tasks for one issue", len(candidate.Tasks))
	}
	task := candidate.Tasks[0]
	fields := map[string]string{
		"source": "feed_comment", "intent": head.Intent, "revision": head.ID,
		"candidate": candidate.Key, "task": task.Key,
	}
	if comment.Source != nil {
		fields["source_kind"], fields["source_id"] = comment.Source.Kind, comment.Source.ID
		if comment.Source.Issue > 0 {
			fields["source_issue"] = strconv.FormatInt(comment.Source.Issue, 10)
		}
	}
	metadata, err := json.Marshal(fields)
	if err != nil {
		return empty, err
	}
	return factory.IssueDraft{
		SourcePost: comment.Record.ID,
		Revision:   comment.Record.Revision,
		Title:      PostDraftTitle(task.Objective),
		Body:       postDraftBody(task, candidate.Rationale),
		Objective:  task.Objective,
		Acceptance: strings.Join(task.Outcomes, "\n"),
		Metadata:   string(metadata),
	}, nil
}

// PostDraftTitle labels one draft from the authored task
// objective: the objective itself within the title bound, otherwise
// its longest whole-word prefix that fits. The full objective stays
// in the draft objective and body verbatim.
func PostDraftTitle(objective string) string {
	title := strings.Join(strings.Fields(objective), " ")
	if len(title) <= 255 {
		return title
	}
	trimmed := title[:255]
	for !utf8.ValidString(trimmed) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if cut := strings.LastIndex(trimmed, " "); cut > 0 {
		trimmed = trimmed[:cut]
	}
	return strings.TrimSpace(trimmed)
}

// postDraftBody renders the authored issue body from the planner
// output alone: the task objective, the candidate rationale and the
// outcome list. The creator appends the source-comment footer.
func postDraftBody(task factory.IntentTask, rationale string) string {
	var b strings.Builder
	b.WriteString(task.Objective)
	if strings.TrimSpace(rationale) != "" {
		b.WriteString("\n\n" + rationale)
	}
	if len(task.Outcomes) != 0 {
		b.WriteString("\n\nOutcomes:")
		for _, outcome := range task.Outcomes {
			b.WriteString("\n- " + outcome)
		}
	}
	return b.String()
}
