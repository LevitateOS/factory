package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// Feed comment backend (R32.2/R32.14): one comment POST persists the
// comment identity, revision and scope plus its durable intake request
// and answers promptly with the stored pipeline states. The funded JEV
// routing, Product Lead LLM draft and native effect advance
// service-side behind the stored request rows (DrivePendingPosts,
// bound to the coordinator lifecycle), never inside the browser
// request: a request that times out or a service that restarts resumes
// the same stored request exactly once, and the comment GET re-reads
// the settled outcome. Unwired stages wait truthfully in pending:
// nothing spends and nothing dispatches without its collaborator.
// Reads never drive: GET serves stored state only. No per-comment
// approvals exist; a discussion route only ever records discussion.

// PostJudgment is one routing outcome: the assessed route plus,
// for existing_work, the already-tracked issue the judgment selected.
type PostJudgment struct {
	Route    string
	Issue    int64
	Question string
	// Routing is the Choice behind this outcome, kept for the feed.
	Routing *factory.PostRouting
}

// postRoutingLabels names the base routing options for people.
var postRoutingLabels = map[string]string{
	factory.PostRouteNewIssue:   "New work",
	factory.PostRouteDiscussion: "Just discussion",
	"clarify_target":            "Ask which app or issue",
	"clarify_result":            "Ask what the result should be",
	"clarify_conflict":          "Ask which requirement wins",
}

// PostRouting records one routing answer with every offered option.
func PostRouting(answer factory.PriorityAnswer, keys []string, candidates []factory.PostWorkCandidate) *factory.PostRouting {
	labels := make(map[string]string, len(keys))
	for key, label := range postRoutingLabels {
		labels[key] = label
	}
	for _, candidate := range candidates {
		label := fmt.Sprintf("Issue #%d", candidate.Issue)
		if name := strings.TrimSpace(candidate.Title); name != "" {
			label += " · " + name
		} else if objective := strings.TrimSpace(candidate.Objective); objective != "" {
			label += " · " + objective
		}
		labels[postExistingChoice(candidate.Issue)] = store.TruncateFeedSummary(label)
	}
	routing := &factory.PostRouting{Selected: answer.Selected, Confidence: answer.Confidence}
	for _, key := range keys {
		routing.Options = append(routing.Options, factory.PostRoutingOption{
			Key: key, Label: labels[key], Probability: answer.Probabilities[key],
		})
	}
	return routing
}

var postClarificationQuestions = map[string]string{
	"clarify_target":   "Which app, page, or existing issue should this request apply to?",
	"clarify_result":   "What should change, and what should the result look like?",
	"clarify_conflict": "Which of the conflicting requirements should take precedence?",
}

// PostParentEvidence carries the recorded reply-parent facts one
// judgment may weigh: the parent comment identity, a bounded preview
// of its stored text and the native issue its own pipeline linked.
type PostParentEvidence struct {
	SourcePost string
	Body       string
	Issue      int64
}

// PostJudgeInput carries everything one routing judgment may
// weigh: the comment with its persisted source snapshot, its reply
// parent's recorded evidence, and the existing-work candidates.
type PostJudgeInput struct {
	SourcePost store.FeedPostDetail
	Parent     *PostParentEvidence
	Candidates []factory.PostWorkCandidate
}

// PostJudge routes one comment through one JEV Choice.
type PostJudge interface {
	JudgePost(ctx context.Context, input PostJudgeInput) (PostJudgment, error)
}

// PostDrafter authors one issue draft from a comment through the
// Product Lead LLM: title, body, objective and acceptance plus the Soda
// work metadata the native creation validates.
type PostDrafter interface {
	DraftPostIssue(ctx context.Context, comment store.FeedPostDetail) (factory.IssueDraft, error)
}

// PostIssueCreator validates and creates native issues from drafts
// through the existing native issue/graph ops, and looks up the issue
// already created from a comment so a lost effect reply reconciles
// instead of duplicating.
type PostIssueCreator interface {
	CreatePostIssue(ctx context.Context, draft factory.IssueDraft, repository int64) (int64, error)
	LookupPostIssue(ctx context.Context, repository int64, postID string) (int64, error)
}

// PostCollaborators carries the wired Lane B stages. Nil stages wait
// in pending; the driver never spends or dispatches through a nil.
type PostCollaborators struct {
	IssueMaker func(context.Context, factory.PostIntake, store.FeedPostDetail) (factory.PostResponse, error)
	Judge      PostJudge
	// Prepare adds the native issue context the judge weighs.
	Prepare func(context.Context, PostJudgeInput) (PostJudgeInput, error)
	Drafter PostDrafter
	Creator PostIssueCreator
}

// postRouteRubric is the single JEV Choice rubric behind comment
// routing: code supplies the complete comment state — body, bound
// issue scope, resolved source snapshot, reply-parent evidence and
// the existing-work candidates — and the explicit outcomes.
const PostRouteRubric = "Route one feed comment to exactly one outcome: " +
	"new_issue when it requests a concrete change that needs new tracked work; " +
	"existing_<issue> for one listed candidate when the comment belongs to that already tracked work; " +
	"discussion when it is an acknowledgement or discussion that must never create an issue; " +
	"clarify_target when the target is ambiguous, clarify_result when the desired result is unclear, or clarify_conflict when requirements conflict. " +
	"Match the requested deliverable against actual issue objectives. Project membership, recent activity, generic worker status, or being a reply do not establish an objective match. " +
	"Use existing work for the same deliverable or a correction of that deliverable; a distinct requested change needs new work even when the comment replies to an older post."

// postExistingChoice names the Choice key selecting one
// existing-work candidate issue.
func postExistingChoice(issue int64) string {
	return "existing_" + strconv.FormatInt(issue, 10)
}

// PostChoiceKeys lists the admitted Choice keys for one judgment:
// the three base routes plus one key per existing-work candidate.
// With no candidates there is no existing-work key to select, so a
// comment can never be routed to work the server never listed.
func PostChoiceKeys(candidates []factory.PostWorkCandidate) []string {
	keys := []string{factory.PostRouteNewIssue, factory.PostRouteDiscussion, "clarify_target", "clarify_result", "clarify_conflict"}
	for _, candidate := range candidates {
		keys = append(keys, postExistingChoice(candidate.Issue))
	}
	return keys
}

// PostJudgmentFromSelection parses one recorded Choice selection
// into its route and selected issue. Only selections the choice
// actually offered admit: an existing-work key outside the candidate
// set refuses instead of linking unlisted work.
func PostJudgmentFromSelection(selected string, candidates []factory.PostWorkCandidate) (PostJudgment, error) {
	if question, ok := postClarificationQuestions[selected]; ok {
		return PostJudgment{Route: factory.PostRouteClarify, Question: question}, nil
	}
	if selected == factory.PostRouteNewIssue || selected == factory.PostRouteDiscussion {
		return PostJudgment{Route: selected}, nil
	}
	for _, candidate := range candidates {
		if selected == postExistingChoice(candidate.Issue) {
			return PostJudgment{Route: factory.PostRouteExistingWork, Issue: candidate.Issue}, nil
		}
	}
	return PostJudgment{}, errors.New("comment judgment selected an outcome it was never offered")
}

// PostJudgeState renders the complete supplied state for one
// routing Choice: the comment, its persisted source snapshot, its
// reply parent's recorded evidence and the existing-work candidates.
func PostJudgeState(input PostJudgeInput) map[string]any {
	comment := input.SourcePost
	state := map[string]any{
		"repository": comment.Record.Repository, "issue": comment.Record.Issue,
		"parent_id": comment.Record.ParentID, "body": comment.Body,
	}
	if comment.Source != nil {
		state["source"] = map[string]any{
			"kind": comment.Source.Kind, "id": comment.Source.ID,
			"issue": comment.Source.Issue, "pr": comment.Source.PR,
			"ref": comment.Source.Ref, "status": comment.Source.Status,
			"summary": comment.Source.Summary,
		}
	}
	if input.Parent != nil {
		state["parent"] = map[string]any{
			"comment": input.Parent.SourcePost, "body": input.Parent.Body,
			"issue": input.Parent.Issue,
		}
	}
	candidates := make([]map[string]any, 0, len(input.Candidates))
	for _, candidate := range input.Candidates {
		candidates = append(candidates, map[string]any{
			"issue": candidate.Issue, "summary": candidate.Summary,
			"status": candidate.Status, "origin": candidate.Origin,
			"title": candidate.Title, "objective": candidate.Objective, "execution_status": candidate.WorkerStatus,
		})
	}
	state["candidates"] = candidates
	return state
}

// PostClarificationQuestion supplies the routing policy wording to model adapters.
func PostClarificationQuestion(key string) (string, bool) {
	question, ok := postClarificationQuestions[key]
	return question, ok
}
