package control

import (
	"context"
	"errors"
	"strconv"

	"github.com/levitateos/sodaos/internal/factory"
)

// readAttemptInputs reads the accepted objective, every selected comment
// revision and the target tip through the protected path, then verifies
// every digest against the acceptance head. Anything changed, hidden,
// closed or locked waits; the stale acceptance never authorizes a run.
func readAttemptInputs(ctx context.Context, deps DispatchDeps, plan *attemptPlan, repository, issue int64) (*planWait, *planWait) {
	if deps.Reads == nil {
		return waitFor(WaitInputsUnavailable, "accepted-input reads are not wired"), nil
	}
	inputs, failure := fetchAttemptInputs(ctx, deps, plan, repository, issue)
	if failure != nil {
		// Refusals are waits in the first slot; only a host
		// failure is a hard error in the second slot.
		if failure.Reason == DispatchErrHost {
			return nil, failure
		}
		return failure, nil
	}
	if wait := checkAttemptIssueState(inputs); wait != nil {
		return wait, nil
	}
	if wait := checkAttemptInputDigests(plan, inputs); wait != nil {
		return wait, nil
	}
	if wait := checkAttemptComments(plan, inputs); wait != nil {
		return wait, nil
	}
	if inputs.TipRef != plan.policy.TargetBranch || !factory.ValidCommit(inputs.Tip) {
		return waitFor(WaitSource, "target branch tip is unavailable"), nil
	}
	plan.inputs = inputs
	return nil, nil
}

// fetchAttemptInputs reads the accepted objective, every selected
// comment revision and the target tip through the protected path.
func fetchAttemptInputs(ctx context.Context, deps DispatchDeps, plan *attemptPlan, repository, issue int64) (DispatchInputs, *planWait) {
	var none DispatchInputs
	seen := make(map[string]bool)
	var commentIDs []string
	for _, section := range [][]factory.SelectedSource{plan.acceptance.Sources, plan.acceptance.Resolutions} {
		for _, source := range section {
			if !seen[source.ID] {
				seen[source.ID] = true
				commentIDs = append(commentIDs, source.ID)
			}
		}
	}
	inputs, err := deps.Reads.ReadDispatchInputs(ctx,
		strconv.FormatInt(repository, 10), strconv.FormatInt(issue, 10),
		commentIDs, plan.policy.TargetBranch)
	if err != nil {
		return none, routeAttemptInputError(err)
	}
	return inputs, nil
}

// routeAttemptInputError maps one input read failure to its wait or
// host failure.
func routeAttemptInputError(err error) *planWait {
	var refusal *AcceptanceRefusal
	if errors.As(err, &refusal) {
		switch refusal.Reason {
		case RefusalNativeBusy:
			return waitFor(WaitInputsBusy, "native writers are busy")
		case RefusalStaleEvidence:
			return waitFor(WaitInputsStale, "native state changed during the read")
		case RefusalIncompleteEvidence:
			return waitFor(WaitInputsIncomplete, "native evidence is incomplete")
		case RefusalSnapshotUnavailable:
			return waitFor(WaitInputsUnavailable, "native evidence is unavailable")
		case RefusalIssueHidden:
			return waitFor(WaitInputsHidden, "the bound actor cannot see the issue")
		}
	}
	return waitFor(DispatchErrHost, "accepted-input read failed")
}

// checkAttemptIssueState requires a visible open unlocked issue.
func checkAttemptIssueState(inputs DispatchInputs) *planWait {
	if !inputs.Issue.Visible {
		return waitFor(WaitInputsHidden, "the bound actor cannot see the issue")
	}
	if inputs.Issue.IsPull {
		return waitFor(WaitInputsChanged, "target is not an issue")
	}
	if inputs.Issue.Closed {
		return waitFor(WaitIssueClosed, "issue is closed")
	}
	if inputs.Issue.Locked {
		return waitFor(WaitIssueLocked, "issue is locked")
	}
	return nil
}

// checkAttemptInputDigests verifies every digest against the
// acceptance head.
func checkAttemptInputDigests(plan *attemptPlan, inputs DispatchInputs) *planWait {
	decision := plan.acceptance
	if !factory.ValidTitleOccurrence(inputs.Issue.TitleOccurrence) {
		return waitFor(WaitInputsIncomplete, "native title history is incomplete")
	}
	if inputs.Issue.TitleDigest != decision.TitleDigest || inputs.Issue.TitleOccurrence != decision.TitleOccurrence || inputs.Issue.ContentDigest != decision.ContentDigest ||
		inputs.Issue.ContentVer != decision.ContentVersion || inputs.Revision < 1 {
		return waitFor(WaitInputsChanged, "accepted objective changed")
	}
	return nil
}

// checkAttemptComments verifies every selected comment revision.
func checkAttemptComments(plan *attemptPlan, inputs DispatchInputs) *planWait {
	byID := make(map[string]DispatchComment, len(inputs.Comments))
	for _, comment := range inputs.Comments {
		byID[comment.ID] = comment
	}
	for _, section := range [][]factory.SelectedSource{plan.acceptance.Sources, plan.acceptance.Resolutions} {
		for _, source := range section {
			got, ok := byID[source.ID]
			if !ok || !got.Visible || got.Digest != source.Digest || got.ContentVer != source.ContentVersion {
				return waitFor(WaitInputsChanged, "selected comment "+source.ID+" changed")
			}
		}
	}
	return nil
}

func promptSections(selected []factory.SelectedSource, comments []DispatchComment) []factory.PromptSource {
	byID := make(map[string]string, len(comments))
	for _, comment := range comments {
		byID[comment.ID] = comment.Content
	}
	var out []factory.PromptSource
	for _, source := range selected {
		content, ok := byID[source.ID]
		if !ok {
			continue
		}
		out = append(out, factory.PromptSource{ID: source.ID, Content: content})
	}
	return out
}
