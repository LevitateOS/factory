package control

import (
	"context"
	"errors"
	"strconv"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/store"
)

// prereqAssessment is one valid acceptance's dependency verdict: route
// invalidations that deny authorization, pending prerequisites that block,
// refreshed endpoint heads for sweep skipping, and retained satisfaction
// memory for result occurrences.
type prereqAssessment struct {
	invalid   []factory.Blocker
	blockers  []factory.Blocker
	heads     map[string]string
	satisfied map[string]factory.PrereqSatisfaction
}

// assessPrerequisites evaluates every declared dependency of a valid
// acceptance against fresh endpoint evidence. Code prerequisites wait for
// attributable factory completion with a current valid endpoint route;
// result prerequisites wait for endpoint closure with accepted
// resolutions; hidden endpoints block without leaking. Closure alone
// never satisfies a code outcome.
func (a *issueAssessor) assessPrerequisites(ctx context.Context, decision *factory.Acceptance, fingerprint *factory.FingerprintInput) (prereqAssessment, error) {
	assessed := prereqAssessment{
		heads:     map[string]string{},
		satisfied: carrySatisfaction(a.current.Satisfied, a.head),
	}
	if !a.hasCurrent {
		assessed.satisfied = map[string]factory.PrereqSatisfaction{}
	}
	for _, prereq := range decision.Prerequisites {
		endpointHead, endpointDecision, err := a.endpointDecision(ctx, prereq)
		if err != nil {
			return assessed, err
		}
		assessed.heads[prereq.Occurrence] = endpointHead
		evidence, err := a.endpointEvidence(ctx, prereq, endpointDecision)
		if err != nil {
			handled, stop := assessEndpointError(prereq, endpointHead, err, &assessed, fingerprint)
			if handled {
				continue
			}
			if stop != nil {
				return assessed, stop
			}
		}
		if !evidence.Issue.Visible {
			assessed.blockers = append(assessed.blockers, factory.Blocker{
				Code:         factory.BlockerEndpointHidden,
				EndpointRepo: prereq.EndpointRepo, EndpointIssue: prereq.EndpointIssue,
				Resolution: factory.BlockerResolution(factory.BlockerEndpointHidden),
			})
			fingerprint.Prereqs = append(fingerprint.Prereqs, factory.FingerprintPrereq{
				Occurrence: prereq.Occurrence, Outcome: prereq.Outcome,
				EndpointHead: endpointHead, Blocker: factory.BlockerEndpointHidden,
			})
			continue
		}
		switch prereq.Outcome {
		case factory.PrereqCode:
			if err := a.assessCodePrereq(ctx, prereq, endpointHead, endpointDecision, evidence, &assessed, fingerprint); err != nil {
				return assessed, err
			}
		case factory.PrereqResult:
			a.assessResultPrereq(prereq, endpointHead, evidence, len(decision.Resolutions) != 0, &assessed, fingerprint)
		}
	}
	return assessed, nil
}

// assessEndpointError records an over-bound endpoint read as a blocker and
// reports whether assessment continues. Any other failure stops the issue.
func assessEndpointError(prereq factory.AcceptedPrerequisite, endpointHead string, err error, assessed *prereqAssessment, fingerprint *factory.FingerprintInput) (handled bool, stop error) {
	var refusal *AcceptanceRefusal
	if errors.As(err, &refusal) && refusal.Reason == RefusalIncompleteEvidence {
		assessed.blockers = append(assessed.blockers, factory.Blocker{
			Code:         factory.BlockerEvidenceIncomplete,
			EndpointRepo: prereq.EndpointRepo, EndpointIssue: prereq.EndpointIssue,
			Resolution: factory.BlockerResolution(factory.BlockerEvidenceIncomplete),
		})
		fingerprint.Prereqs = append(fingerprint.Prereqs, factory.FingerprintPrereq{
			Occurrence: prereq.Occurrence, Outcome: prereq.Outcome,
			EndpointHead: endpointHead, Blocker: factory.BlockerEvidenceIncomplete,
		})
		return true, nil
	}
	return false, err
}

// endpointDecision loads the endpoint's head acceptance when one exists.
func (a *issueAssessor) endpointDecision(ctx context.Context, prereq factory.AcceptedPrerequisite) (string, *factory.Acceptance, error) {
	head, err := a.coordinator.Store.AcceptanceHead(ctx, prereq.EndpointRepo, prereq.EndpointIssue)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", nil, nil
		}
		return "", nil, err
	}
	decision, err := a.coordinator.Store.AcceptanceDecision(ctx, head)
	if err != nil {
		return "", nil, err
	}
	return head, &decision, nil
}

// endpointEvidence brackets one endpoint's issue, edges and — when the
// endpoint carries a head acceptance — its selected comment revisions, so
// endpoint validity compares exact revisions instead of assuming them.
func (a *issueAssessor) endpointEvidence(ctx context.Context, prereq factory.AcceptedPrerequisite, endpointDecision *factory.Acceptance) (AcceptanceEvidence, error) {
	decision := factory.Acceptance{
		Repository: prereq.EndpointRepo, IssueIndex: strconv.FormatInt(prereq.EndpointIssue, 10),
	}
	if endpointDecision != nil {
		for _, section := range [][]factory.SelectedSource{endpointDecision.Sources, endpointDecision.Resolutions} {
			for _, source := range section {
				// Bare IDs select by-issue revisions for the read;
				// digests are compared from the recorded decision.
				decision.Sources = append(decision.Sources, factory.SelectedSource{ID: source.ID})
			}
		}
	}
	return a.coordinator.readAcceptanceEvidence(ctx, decision)
}

// assessCodePrereq evaluates one code prerequisite route: the recorded
// endpoint acceptance must still exist, head the endpoint and stay valid,
// or the dependent relation needs readoption. Satisfaction itself waits
// for attributable factory completion: a confirmed merge on the bound
// acceptance and its still-current original issue closing occurrence.
// A current route without both still pends.
func (a *issueAssessor) assessCodePrereq(ctx context.Context, prereq factory.AcceptedPrerequisite, endpointHead string, endpointDecision *factory.Acceptance, evidence AcceptanceEvidence, assessed *prereqAssessment, fingerprint *factory.FingerprintInput) error {
	input := factory.FingerprintPrereq{
		Occurrence: prereq.Occurrence, Outcome: prereq.Outcome, EndpointHead: endpointHead,
		Closed: evidence.Issue.Closed, ClosingOccurrence: evidence.Issue.ClosingOccurrence,
	}
	invalid := func(detail string) {
		input.Blocker, input.Detail = factory.BlockerAcceptanceInvalid, detail
		assessed.invalid = append(assessed.invalid, factory.Blocker{
			Code: factory.BlockerAcceptanceInvalid, Detail: detail,
			EndpointRepo: prereq.EndpointRepo, EndpointIssue: prereq.EndpointIssue,
			Resolution: factory.BlockerResolution(factory.BlockerAcceptanceInvalid),
		})
	}
	switch {
	case endpointDecision == nil || endpointHead != prereq.PrereqAcceptance:
		invalid(RefusalPrereqStale)
	default:
		withdrawn, _, err := a.coordinator.Store.AcceptanceWithdrawn(ctx, prereq.EndpointRepo, prereq.EndpointIssue, prereq.PrereqAcceptance)
		if err != nil {
			return err
		}
		switch {
		case withdrawn:
			invalid(factory.ResultDetailAcceptanceWithdrawn)
		default:
			valid, err := a.coordinator.endpointAcceptanceValid(ctx, *endpointDecision, evidence)
			if err != nil {
				return err
			}
			if !valid {
				invalid(factory.DetailPrereqAcceptanceInvalid)
			} else {
				satisfied, err := a.codePrereqCompleted(ctx, prereq, evidence.Issue)
				if err != nil {
					return err
				}
				if !satisfied {
					input.Blocker = factory.BlockerCodePending
					assessed.blockers = append(assessed.blockers, factory.Blocker{
						Code:         factory.BlockerCodePending,
						EndpointRepo: prereq.EndpointRepo, EndpointIssue: prereq.EndpointIssue,
						Resolution: factory.BlockerResolution(factory.BlockerCodePending),
					})
				} else {
					input.Satisfied = true
				}
			}
		}
	}
	fingerprint.Prereqs = append(fingerprint.Prereqs, input)
	return nil
}

// codePrereqCompleted reports whether attributable factory completion
// delivered the endpoint's accepted route: a confirmed merge whose
// acceptance is still the prerequisite's bound head and whose original
// closing occurrence still closes the endpoint. Reclosing cannot revive it.
func (a *issueAssessor) codePrereqCompleted(ctx context.Context, prereq factory.AcceptedPrerequisite, issue AcceptanceIssueView) (bool, error) {
	if !issue.Closed || issue.ClosedUnix <= 0 || !factory.ValidNativeOccurrence(issue.ClosingOccurrence) {
		return false, nil
	}
	completed, err := a.coordinator.Store.IssueMergeCompletion(ctx, prereq.EndpointRepo, prereq.EndpointIssue)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return completed.Acceptance == prereq.PrereqAcceptance && completed.OriginalClosedUnix > 0 &&
		factory.ValidNativeOccurrence(completed.OriginalClosingOccurrence) && completed.OriginalClosingOccurrence == issue.ClosingOccurrence, nil
}

// assessResultPrereq evaluates one result prerequisite: the endpoint must
// be closed with an accepted resolution under the current occurrence. A
// reopen ends the occurrence; closing again cannot revive it without a
// new dependent acceptance head. Pending states retain the recorded
// occurrence so a later closure still compares against it.
func (a *issueAssessor) assessResultPrereq(prereq factory.AcceptedPrerequisite, endpointHead string, evidence AcceptanceEvidence, hasResolutions bool, assessed *prereqAssessment, fingerprint *factory.FingerprintInput) {
	input := factory.FingerprintPrereq{
		Occurrence: prereq.Occurrence, Outcome: prereq.Outcome,
		EndpointHead:      endpointHead,
		Closed:            evidence.Issue.Closed,
		ClosingOccurrence: evidence.Issue.ClosingOccurrence,
	}
	pending := func(detail string) {
		input.Blocker, input.Detail = factory.BlockerResultPending, detail
		assessed.blockers = append(assessed.blockers, factory.Blocker{
			Code: factory.BlockerResultPending, Detail: detail,
			EndpointRepo: prereq.EndpointRepo, EndpointIssue: prereq.EndpointIssue,
			Resolution: factory.BlockerResolution(factory.BlockerResultPending),
		})
	}
	switch {
	case evidence.Issue.Closed && !factory.ValidNativeOccurrence(evidence.Issue.ClosingOccurrence):
		input.Blocker = factory.BlockerEvidenceIncomplete
		assessed.blockers = append(assessed.blockers, factory.Blocker{
			Code:         factory.BlockerEvidenceIncomplete,
			EndpointRepo: prereq.EndpointRepo, EndpointIssue: prereq.EndpointIssue,
			Resolution: factory.BlockerResolution(factory.BlockerEvidenceIncomplete),
		})
	case !hasResolutions:
		pending(factory.ResultDetailNoResolution)
	case !evidence.Issue.Closed:
		pending(factory.ResultDetailAwaitingClosure)
	default:
		recorded, ok := assessed.satisfied[prereq.Occurrence]
		if ok && recorded.ClosingOccurrence != evidence.Issue.ClosingOccurrence {
			pending(factory.ResultDetailReopened)
			break
		}
		input.Satisfied = true
		assessed.satisfied[prereq.Occurrence] = factory.PrereqSatisfaction{
			ClosingOccurrence: evidence.Issue.ClosingOccurrence,
			Acceptance:        a.head,
		}
	}
	fingerprint.Prereqs = append(fingerprint.Prereqs, input)
}

// endpointAcceptanceValid checks one endpoint's head acceptance against
// its fresh evidence one level deep: exact revisions must still match,
// the decision must not be withdrawn, and its own code routes must still
// name current heads. Deeper transitivity belongs to the endpoint's own
// assessment, not this comparison.
func (c *Coordinator) endpointAcceptanceValid(ctx context.Context, decision factory.Acceptance, evidence AcceptanceEvidence) (bool, error) {
	if len(assessAcceptance(decision, evidence)) != 0 {
		return false, nil
	}
	repository, issue := decision.Repository, mustIssueIndex(decision.IssueIndex)
	if withdrawn, _, err := c.Store.AcceptanceWithdrawn(ctx, repository, issue, decision.ID); err != nil || withdrawn {
		return false, err
	}
	for _, prereq := range decision.Prerequisites {
		if prereq.Outcome != factory.PrereqCode {
			continue
		}
		head, err := c.Store.AcceptanceHead(ctx, prereq.EndpointRepo, prereq.EndpointIssue)
		if err != nil || head != prereq.PrereqAcceptance {
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return false, err
			}
			return false, nil
		}
	}
	return true, nil
}
