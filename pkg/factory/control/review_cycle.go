package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// reviewCyclePassLimit bounds one review-cycle sweep: published
// publications reconsidered for checks and merge progress.
const reviewCyclePassLimit = 256

// CheckPass assesses native checks for every published publication that
// lacks a passing assessment on its current head: missing verdicts,
// verdicts on a superseded head, and non-passing verdicts that later CI
// may flip. Publications already passing on their head are left alone;
// the merge still re-verifies checks live before submitting, so a stale
// pass can never ride through. While no check assessor is wired, the
// pass reports unavailable instead of guessing.
func (c *Coordinator) CheckPass(ctx context.Context) CheckReport {
	ctx, unlock, err := c.progressPass(ctx)
	if err != nil {
		report := CheckReport{Assessed: []CheckLink{}}
		checkError(&report, "", "progress_pass_cancelled")
		return report
	}
	defer unlock()
	ctx, _, cancelPass := c.readinessPass(ctx)
	defer cancelPass()
	report := CheckReport{Assessed: []CheckLink{}}
	if c.Checks == nil {
		children := PublishReport{}
		c.producePublishedChildren(ctx, &children)
		report.Unavailable = true
		return report
	}
	// A recorded merge can still be waiting for fresh policy/check evidence.
	// Its existence must not remove the active candidate from assessment.
	pubs, err := c.Store.CheckablePublications(ctx, reviewCyclePassLimit)
	if err != nil {
		checkError(&report, "", "store_unavailable")
		return report
	}
	for _, p := range pubs {
		if c.checkCurrent(ctx, p) {
			continue
		}
		policy, err := c.Store.RepositoryPolicy(ctx, p.Repository)
		if err != nil {
			checkError(&report, p.ID, "store_unavailable")
			continue
		}
		one := c.AssessPublicationChecks(ctx, c.Checks, policy.Merge.ActorID, p.AssignmentID)
		report.Assessed = append(report.Assessed, one.Assessed...)
		report.Waits = append(report.Waits, one.Waits...)
		report.Errors = append(report.Errors, one.Errors...)
		for _, link := range one.Assessed {
			if link.Verdict != factory.CheckPass || c.Merges == nil {
				continue
			}
			if !c.currentPublicationApproved(ctx, p) {
				continue
			}
			// A fresh pass opens the merge row: the merge pass
			// itself still demands the independent review and
			// re-verifies checks live before submitting.
			merges := MergeReport{Merged: []MergeLink{}}
			c.mergeOne(ctx, p, &merges)
		}
	}
	children := PublishReport{}
	c.producePublishedChildren(ctx, &children)
	return report
}

// checkCurrent reports whether the latest stored assessment already passes
// on the exact native target and current adopted policy. A head/base match
// alone must not strand a publication behind an obsolete check definition.
func (c *Coordinator) checkCurrent(ctx context.Context, p factory.Publication) bool {
	assessment, ok := c.currentChildCheckAssessment(ctx, p)
	return ok && assessment.Verdict == factory.CheckPass
}

// progressAfterPublish advances one published assignment toward merge: a
// fresh check assessment on its head, then its merge row once the head
// passes. Merge rows open only behind a passing verdict: opening one on a
// head that evidence already dooms would fail it terminally and strand
// the publication. Reviews gate the merge inside the merge pass itself.
func (c *Coordinator) progressAfterPublish(ctx context.Context, assignmentID string) {
	p, err := c.Store.PublicationByAssignment(ctx, assignmentID)
	if err != nil || p.Stage != factory.PublicationPublished {
		return
	}
	if updated, _, err := c.recordCleanDeliveryAcceptance(ctx, p); err == nil {
		p = updated
	}
	if c.Checks != nil {
		if policy, err := c.Store.RepositoryPolicy(ctx, p.Repository); err == nil && !c.checkCurrent(ctx, p) {
			c.AssessPublicationChecks(ctx, c.Checks, policy.Merge.ActorID, p.AssignmentID)
			if fresh, err := c.Store.PublicationByAssignment(ctx, assignmentID); err == nil {
				p = fresh
			}
		}
	}
	children := PublishReport{}
	c.producePublishedChildren(ctx, &children)
	if fresh, err := c.Store.PublicationByAssignment(ctx, assignmentID); err == nil {
		p = fresh
	}
	if c.Merges == nil || !c.checkCurrent(ctx, p) || !c.currentPublicationApproved(ctx, p) {
		return
	}
	report := MergeReport{Merged: []MergeLink{}}
	c.mergeOne(ctx, p, &report)
}

func (c *Coordinator) currentPublicationApproved(ctx context.Context, p factory.Publication) bool {
	review, ok, pending, refused := c.currentReviewReport(ctx, p)
	return ok && !pending && !refused && review.Verdict == "approve"
}

// ResolvedReviewVerdict is the canonical consumer view of one reviewer
// assignment's recorded verdict: exactly one typed verdict (findings
// verdict or explicit clear). Findings verdicts always map to
// request-changes (their shape guarantees blocking content); only an
// explicit clear maps to approve.
type ResolvedReviewVerdict struct {
	Verdict string // "approve" | "request-changes"
	Review  *factory.ReviewCycleReport
	Clear   *factory.ClearReviewReport
}

// resolveAssignmentVerdict resolves one reviewer assignment's recorded
// verdict to its single canonical source: review assignments resolve the
// typed v2 verdict. Anything else — missing report, both sources, neither
// source, or a non-review phase — refuses.
func resolveAssignmentVerdict(assignment factory.Assignment) (ResolvedReviewVerdict, error) {
	var empty ResolvedReviewVerdict
	if assignment.Result == nil || !assignment.Result.Reported {
		return empty, errors.New("review lacks its recorded report")
	}
	result := assignment.Result
	if assignment.Phase != project.ExecutionReview {
		return empty, errors.New("review assignment carries another phase")
	}
	if result.PhaseResult == nil || result.PhaseResult.Output == nil {
		return empty, errors.New("phased review lacks its typed verdict")
	}
	output := result.PhaseResult.Output
	if (output.Review == nil) == (output.Clear == nil) {
		return empty, errors.New("phased review carries no single verdict")
	}
	if result.Review != nil {
		return empty, errors.New("phased review carries a string report")
	}
	if output.Review != nil {
		return ResolvedReviewVerdict{Verdict: "request-changes", Review: output.Review}, nil
	}
	return ResolvedReviewVerdict{Verdict: "approve", Clear: output.Clear}, nil
}

// reviewCarriedFindings rebuilds the canonical carried set one review
// cycle must answer: the first correction journal's findings when a
// correction already answered this cycle, otherwise the bounded canonical
// set across this publication attempt's typed history. A candidate change
// invalidates acceptances but NEVER erases unresolved canonical finding
// identity, content, evidence or disposition: prior-round phased findings on
// superseded heads and every correction journal's finding keys stay carried
// — oldest content wins, so same-key changed content never silently
// replaces the original provenance. A key retires only on attributable
// bound verification evidence: exactly one FindingVerified assessment in a
// recorded review report for this publication. Correction dispositions
// alone — fixed, unresolved, disputed or declined — never retire a key; an
// agent claim is not disappearance. Same-head prior rounds that resolved
// with unkeyed string defects refuse: their defects carry no stable key,
// so a typed verdict cannot prove coverage.
// A cross-head prior round whose reviewer assignment cannot be
// reconstructed holds the cycle incomplete instead of succeeding empty.
type carriedCollector struct {
	carried []factory.Finding
	seen    map[string]bool
	content map[string]factory.Finding
}

// add merges one attributable finding into the canonical set in
// chronological order. Same-key changed content holds conflict: only
// exact content equality re-affirms an already recorded key, so a
// rewritten finding can never silently replace original provenance.
func (k *carriedCollector) add(finding factory.Finding) error {
	if prior, ok := k.content[finding.Key]; ok {
		if !reflect.DeepEqual(prior, finding) {
			return errors.New("canonical carried history conflicts on its finding content")
		}
		if !k.seen[finding.Key] {
			k.seen[finding.Key] = true
			k.carried = append(k.carried, finding)
		}
		return nil
	}
	k.content[finding.Key] = finding
	k.seen[finding.Key] = true
	k.carried = append(k.carried, finding)
	return nil
}

func (k *carriedCollector) retire(key string) {
	if !k.seen[key] {
		return
	}
	k.seen[key] = false
	for i, finding := range k.carried {
		if finding.Key == key {
			k.carried = append(k.carried[:i], k.carried[i+1:]...)
			return
		}
	}
}

func (k *carriedCollector) snapshot() []factory.Finding {
	return append([]factory.Finding(nil), k.carried...)
}

func (k *carriedCollector) addJournal(journals []factory.CorrectionJournal, cycleID string) error {
	for _, journal := range journals {
		if journal.CycleID != cycleID {
			continue
		}
		for _, finding := range journal.Report.Findings {
			if err := k.add(finding); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkPriorCycleBindings(p factory.Publication, prior factory.ReviewCycle, reviewer factory.Assignment) error {
	// Cycle bindings: the recorded round belongs to this publication
	// and attempt, with a derived identity, and the reviewer
	// assignment belongs to that exact cycle and historical head.
	if prior.PublicationID != p.ID || prior.AttemptRoot == "" || prior.Round < 1 {
		return errors.New("canonical carried history is incomplete: step01")
	}
	if factory.ReviewCycleID(prior.PublicationID, prior.AttemptRoot, prior.Round, prior.Phase) != prior.ID {
		return errors.New("canonical carried history is incomplete: step02")
	}
	if reviewer.ID != prior.ReviewerAssignment || reviewer.Role != project.RoleReviewer ||
		reviewer.Phase != project.ExecutionReview || reviewer.AttemptRoot != prior.AttemptRoot ||
		reviewer.SourceCommit != prior.Head {
		return errors.New("canonical carried history is incomplete: step03")
	}
	if reviewer.Subject.Kind != factory.SubjectIssue || reviewer.Subject.Issue == nil ||
		reviewer.Subject.Issue.Issue != reviewer.Issue {
		return errors.New("canonical carried history is incomplete: step04")
	}
	return nil
}

func checkPriorReviewerEvidence(p factory.Publication, prior factory.ReviewCycle, reviewer factory.Assignment) error {
	// The canonical finished/succeeded/reported assignment validator
	// owns stage/outcome/reason/finished-time, run-history binding and
	// result dispatch binding: the collector never duplicates it.
	// Retirement evidence additionally needs a succeeded outcome —
	// failed or cancelled runs never verify findings.
	if err := reviewer.Validate(); err != nil {
		return errors.New("canonical carried history is incomplete: step05")
	}
	if reviewer.Outcome != factory.Succeeded {
		return errors.New("canonical carried history is incomplete: step06")
	}
	out := reviewer.Result.PhaseResult
	if out == nil || out.Output == nil ||
		(out.Output.Review == nil && out.Output.Clear == nil) {
		if reviewer.Result.Review != nil {
			return errors.New("prior review carries an unkeyed string report")
		}
		return errors.New("canonical carried history is incomplete: step07")
	}
	// Independence: the verifying reviewer run differs from every
	// correction run recorded for this cycle, when both are known.
	for _, journal := range p.CorrectionJournals {
		if journal.CycleID != prior.ID || journal.RunID == "" || reviewer.Run == "" {
			continue
		}
		if journal.RunID == reviewer.Run {
			return errors.New("canonical carried history is incomplete: step08")
		}
	}
	return nil
}

func (c *Coordinator) checkPriorRunJoins(ctx context.Context, p factory.Publication, reviewer factory.Assignment) error {
	// Recorded run with canonical owner joins: the claimed run
	// must exist as a recorded reconciled run with the same outcome, and the
	// canonical publicationAssignmentsForRun join must resolve that
	// run to this exact reviewer assignment and its publication
	// owner (role/project/input bound to the assignment, owner
	// bound to repo/issue/project/acceptance/root). A run recorded
	// for another project/input, an unreconciled run, and a missing
	// join or view receipt all hold incomplete; nothing synthetic
	// is injected as proof.
	recorded, err := c.Store.FactoryRun(ctx, reviewer.Run)
	if err != nil || recorded.ID != reviewer.Run ||
		(recorded.Outcome != reviewer.Outcome && !(recorded.Outcome == factory.Succeeded && reviewer.Outcome == factory.NeedsHuman && reviewer.Reason == factory.AssignReasonNoReport)) || !recorded.Reconciled {
		return errors.New("canonical carried history is incomplete: step18")
	}
	bound, owner, err := c.publicationAssignmentsForRun(ctx, recorded)
	if err != nil || bound.ID != reviewer.ID {
		return errors.New("canonical carried history is incomplete: step19")
	}
	// Publication lineage: the joined owner must be this
	// publication's own root assignment, so history from another
	// publication's owner can never retire this publication's keys.
	if owner.ID != p.AssignmentID {
		return errors.New("canonical carried history is incomplete: step20")
	}
	return nil
}

func (c *Coordinator) checkPriorReviewBindings(ctx context.Context, p factory.Publication, prior factory.ReviewCycle, reviewer factory.Assignment) error {
	if err := checkPriorCycleBindings(p, prior, reviewer); err != nil {
		return err
	}
	if err := checkPriorReviewerEvidence(p, prior, reviewer); err != nil {
		return err
	}
	return c.checkPriorRunJoins(ctx, p, reviewer)
}

func (c *Coordinator) processPriorReview(ctx context.Context, p factory.Publication, k *carriedCollector, recorded factory.ReviewCycle, reviewer factory.Assignment) error {
	// A settled failed/cancelled session without a verdict verifies nothing.
	// Keep its original cycle/run joins, while carrying every prior finding.
	if reviewer.Finished() && (reviewer.Outcome == factory.Failed || reviewer.Outcome == factory.Cancelled || reviewer.Outcome == factory.NeedsHuman) &&
		reviewer.Result != nil && !reviewer.Result.Reported && reviewer.Result.Review == nil && len(reviewer.Result.Findings) == 0 &&
		(reviewer.Result.PhaseResult == nil || reviewer.Result.PhaseResult.Output == nil) {
		if reviewer.Validate() != nil {
			return errors.New("canonical carried history is incomplete: step05")
		}
		if err := checkPriorCycleBindings(p, recorded, reviewer); err != nil {
			return err
		}
		return c.checkPriorRunJoins(ctx, p, reviewer)
	}
	if err := c.checkPriorReviewBindings(ctx, p, recorded, reviewer); err != nil {
		return err
	}
	out := reviewer.Result.PhaseResult.Output
	carriedBefore := k.snapshot()
	if out.Review != nil {
		if err := c.processPriorTypedReview(recorded, k, out, carriedBefore); err != nil {
			return err
		}
	}
	if out.Clear != nil {
		if err := c.processPriorClearReview(recorded, k, out, carriedBefore); err != nil {
			return err
		}
	}
	return nil
}

func (c *Coordinator) processPriorTypedReview(recorded factory.ReviewCycle, k *carriedCollector, out *factory.PhaseOutput, carriedBefore []factory.Finding) error {
	if out.Review.CycleID != recorded.ID {
		return errors.New("canonical carried history is incomplete: step09")
	}
	if err := out.Review.Validate(carriedBefore); err != nil {
		return errors.New("canonical carried history is incomplete: step10")
	}
	for _, finding := range out.Review.NewFindings {
		if err := k.add(finding); err != nil {
			return err
		}
	}
	for _, assessment := range out.Review.Assessments {
		if assessment.Status == factory.FindingVerified {
			k.retire(assessment.Key)
		}
	}
	return nil
}

func (c *Coordinator) processPriorClearReview(recorded factory.ReviewCycle, k *carriedCollector, out *factory.PhaseOutput, carriedBefore []factory.Finding) error {
	if out.Clear.CycleID != recorded.ID || out.Clear.Head != recorded.Head ||
		out.Clear.BaseOID != recorded.BaseOID {
		return errors.New("canonical carried history is incomplete: step11")
	}
	content, err := factory.FindingContentDigest(carriedBefore)
	if err != nil {
		return errors.New("canonical carried history is incomplete: step12")
	}
	coverage, err := factory.ClearCoverageDigest(recorded.Head, recorded.BaseOID, content)
	if err != nil {
		return errors.New("canonical carried history is incomplete: step13")
	}
	if out.Clear.CoverageDigest != coverage {
		return errors.New("canonical carried history is incomplete: step14")
	}
	if err := out.Clear.Validate(carriedBefore); err != nil {
		return errors.New("canonical carried history is incomplete: step15")
	}
	for _, assessment := range out.Clear.Assessments {
		if assessment.Status == factory.FindingVerified {
			k.retire(assessment.Key)
		}
	}
	return nil
}

// An explicitly funded predecessor can retain a legacy clear that finished
// before carried-assessment validation reached the terminal mapper. If no
// native operation ever attributed that verdict, its missing assessments verify
// nothing. Recognize only that exact shape; all other malformed history still
// passes through the ordinary validator and refuses.
func (c *Coordinator) unsubmittedHistoricalClear(ctx context.Context, p factory.Publication, recorded factory.ReviewCycle, reviewer factory.Assignment, carried []factory.Finding) (bool, error) {
	if !reviewer.UnassessedClear() {
		return false, nil
	}
	for _, operation := range p.ReviewOperations {
		if operation.RunID == reviewer.Run {
			return false, nil
		}
	}
	assigned := reviewer.ReviewContext
	if assigned.Publication.ID != p.ID || assigned.Publication.AssignmentID != p.AssignmentID ||
		!reflect.DeepEqual(assigned.Cycle, recorded) ||
		factory.CheckSubmittedFindings(carried, assigned.CarriedFindings) != nil {
		return false, nil
	}
	if err := c.checkPriorReviewBindings(ctx, p, recorded, reviewer); err != nil {
		return false, err
	}
	return true, nil
}

func (c *Coordinator) reviewCarriedFindings(ctx context.Context, p factory.Publication, cycle factory.ReviewCycle) ([]factory.Finding, error) {
	k := &carriedCollector{carried: []factory.Finding{}, seen: map[string]bool{}, content: map[string]factory.Finding{}}
	// applyAssessments retires keys only on bound attributable verification
	// evidence from the recorded reviewer result of one exact cycle: a
	// finished independent reviewer run whose assignment identity matches
	// the cycle, with typed output and a valid verified assessment. A
	// verified assessment for a key the canonical set never recorded is
	// malformed evidence and holds incomplete. Still-open assessments
	// affirm nothing: the key stays carried through its finding.

	// processPrior merges one recorded prior round through the canonical
	// validators at the correct point: the typed report validates against
	// the carried snapshot BEFORE this round, then its new findings merge
	// and its verified assessments retire. A report that omits required
	// coverage, rewrites the tuple or breaks its digest refuses here with
	// zero downstream effect — the collector never performs semantic
	// validation on a parallel path. A result that resolved with unkeyed
	// string defects refuses: they carry no stable key, so a typed
	// verdict cannot prove coverage.
	// The canonical set rebuilds in bounded monotonic review-cycle order
	// within this publication and attempt: each attributable review and
	// each correction journal merges at its recorded cycle and round, so
	// older verification evidence can never erase a newer occurrence and
	// later rediscovery re-adds. There is no shortcut to any journal —
	// not even the current cycle's: every supplied set still passes the
	// same conflict and verification pipeline.
	history, err := c.Store.PublicationReviewHistory(ctx, p)
	if err != nil {
		return nil, err
	}
	for _, source := range history {
		recorded := map[string]bool{}
		for _, recordedCycle := range source.ReviewCycles {
			recorded[recordedCycle.ID] = true
		}
		for _, journal := range source.CorrectionJournals {
			if !recorded[journal.CycleID] {
				return nil, errors.New("canonical carried history is incomplete: step16")
			}
		}
		ordered := append([]factory.ReviewCycle(nil), source.ReviewCycles...)
		sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Round < ordered[j].Round })
		for _, recorded := range ordered {
			if source.ID == p.ID && recorded.Round > cycle.Round {
				continue
			}
			// The current cycle has no prior verdict. Correction rounds have
			// a synthetic reviewer marker and contribute only their journal.
			if recorded.ID != cycle.ID && recorded.CorrectionAssignment == "" {
				reviewer, err := c.Store.Assignment(ctx, recorded.ReviewerAssignment)
				if err != nil {
					return nil, errors.New("canonical carried history is incomplete: step17")
				}
				recovered := false
				// Only predecessors admitted by PublicationReviewHistory qualify;
				// the current publication must still reject its incomplete result.
				if source.ID != p.ID {
					recovered, err = c.unsubmittedHistoricalClear(ctx, source, recorded, reviewer, k.snapshot())
					if err != nil {
						return nil, err
					}
				}
				if !recovered {
					if err := c.processPriorReview(ctx, source, k, recorded, reviewer); err != nil {
						return nil, err
					}
				}
			}
			if err := k.addJournal(source.CorrectionJournals, recorded.ID); err != nil {
				return nil, err
			}
		}
	}
	return k.carried, nil
}

// bindPhasedReviewVerdict binds one resolved typed verdict to the exact
// submission it authorizes: the current reviewer assignment, the exact
// candidate, base and review cycle, and the complete carried findings.
// Wrong head or base, a cycle that is unrecorded or bound to another
// reviewer or tuple, and a missing carried assessment all refuse with zero
// downstream effect: no native call happens before every check passes. It
// returns the native event and body for the bound verdict.
func resolveVerdictCycleID(p factory.Publication, assignment factory.Assignment, head string, verdict ResolvedReviewVerdict) (string, error) {
	if verdict.Review == nil && verdict.Clear == nil {
		return "", errors.New("phased binding needs its typed verdict")
	}
	if assignment.Role != project.RoleReviewer || assignment.SourceCommit != head || head != p.Candidate {
		return "", errors.New("reviewer finished on another candidate")
	}
	if verdict.Review != nil {
		return verdict.Review.CycleID, nil
	}
	if verdict.Clear.Head != head || verdict.Clear.BaseOID != p.PullTarget().BaseOID {
		return "", errors.New("clear review answers another candidate")
	}
	return verdict.Clear.CycleID, nil
}

func findReviewCycle(p factory.Publication, cycleID string) (factory.ReviewCycle, bool) {
	for _, recorded := range p.ReviewCycles {
		if recorded.ID == cycleID {
			return recorded, true
		}
	}
	return factory.ReviewCycle{}, false
}

func (c *Coordinator) bindPhasedReviewVerdict(ctx context.Context, p factory.Publication, assignment factory.Assignment, head string, verdict ResolvedReviewVerdict) (string, string, error) {
	cycleID, err := resolveVerdictCycleID(p, assignment, head, verdict)
	if err != nil {
		return "", "", err
	}
	cycle, found := findReviewCycle(p, cycleID)
	if !found {
		return "", "", errors.New("review verdict consumes an unrecorded cycle")
	}
	if cycle.PublicationID != p.ID || cycle.ReviewerAssignment != assignment.ID ||
		cycle.Head != head || cycle.BaseOID != p.PullTarget().BaseOID {
		return "", "", errors.New("review verdict answers another round")
	}
	carried, err := c.reviewCarriedFindings(ctx, p, cycle)
	if err != nil {
		return "", "", err
	}
	if verdict.Review != nil {
		if err := verdict.Review.Validate(carried); err != nil {
			return "", "", err
		}
		return "REQUEST_CHANGES", renderTypedReviewBody(head, cycle.BaseOID, verdict), nil
	}
	if err := verdict.Clear.Validate(carried); err != nil {
		return "", "", err
	}
	return "APPROVED", renderTypedReviewBody(head, cycle.BaseOID, verdict), nil
}

// renderTypedReviewBody renders one bound typed verdict as the native
// review prose: the exact head and base, every new defect with its
// evidence and required outcome, and every carried assessment with its
// state and evidence. Deterministic: the same verdict always renders the
// same bytes. Native inline finding comments stay with the FW16/FW18
// review operation work, never this submission.
func renderTypedReviewBody(head, base string, verdict ResolvedReviewVerdict) string {
	var cycleID string
	if verdict.Review != nil {
		cycleID = verdict.Review.CycleID
	} else if verdict.Clear != nil {
		cycleID = verdict.Clear.CycleID
	}
	var b strings.Builder
	if verdict.Clear != nil {
		b.WriteString("Explicit clear of " + head + " at base " + base + " for cycle " + cycleID + ".\n")
		for _, assessment := range verdict.Clear.Assessments {
			b.WriteString("- [" + assessment.Key + "] verified: " + assessment.Evidence + "\n")
		}
		return b.String()
	}
	b.WriteString("Review findings on " + head + " at base " + base + " for cycle " + cycleID + ".\n")
	for _, finding := range verdict.Review.NewFindings {
		b.WriteString("- [" + finding.Key + "] (" + finding.Severity + "/" + finding.FixKind + ") defect: " + finding.Defect +
			" evidence: " + finding.Evidence + " required outcome: " + finding.RequiredOutcome + "\n")
	}
	for _, assessment := range verdict.Review.Assessments {
		b.WriteString("- [" + assessment.Key + "] " + assessment.Status + ": " + assessment.Evidence + "\n")
	}
	return b.String()
}

// SubmitReviewForRun submits one settled reviewer run's genuine verdict
// through the separate reviewer actor: it re-reads the recorded
// publication, refuses a superseded head, observes the exact native
// target and adopts the committed receipt. Each reviewer run reviews the
// head it started from, carried in its recorded input; a correction that
// landed first makes the verdict stale, never submitted. While no
// reviewer is wired, submission reports unavailable instead of guessing.
func (c *Coordinator) replaySubmittedReview(ctx context.Context, p *factory.Publication, run factory.Run) (factory.ReviewOutcome, bool, error) {
	var empty factory.ReviewOutcome
	for i := len(p.ReviewOperations) - 1; i >= 0; i-- {
		op := p.ReviewOperations[i]
		if op.RunID != run.ID {
			continue
		}
		if err := op.ValidateForPublication(*p); err != nil {
			return empty, false, err
		}
		// Recovery keeps the registered tuple. Only an executor with its own
		// durable send proof can resume it; generic lookup absence still holds.
		outcome, err := c.recoverRecordedReview(ctx, *p, op)
		if err != nil {
			return empty, false, err
		}
		if outcome.NotObserved {
			return empty, false, errors.New("recorded review outcome remains unresolved")
		}
		updated, err := op.WithOutcome(outcome)
		if err != nil {
			return empty, false, err
		}
		if !sameOperationOutcome(op.Outcome, updated.Outcome) {
			p.ReviewOperations[i] = updated
			p.Revision++
			if err := c.Store.UpdatePublication(ctx, *p); err != nil {
				return empty, false, err
			}
		}
		if outcome.Effect != factory.OpEffectCommitted || outcome.Completion != factory.OpCompletionComplete {
			return empty, false, errors.New("recorded review submission is not complete")
		}
		adopted, err := c.Reviews.AdoptReview(ctx, op.Work, outcome)
		if err != nil {
			return empty, false, err
		}
		if adopted.ReviewerID != op.Work.ActorID || adopted.Event != op.Work.Event || adopted.HeadOID != op.Work.HeadOID {
			return empty, false, errors.New("review receipt differs from its recorded work")
		}
		if err := c.recordReviewCandidateTuple(ctx, p, adopted); err != nil {
			return empty, false, err
		}
		return adopted, true, nil
	}
	return empty, false, nil
}

func (c *Coordinator) SubmitReviewForRun(ctx context.Context, runID string) (factory.ReviewOutcome, error) {
	var empty factory.ReviewOutcome
	if c.Reviews == nil {
		return empty, errors.New("review submission unavailable")
	}
	run, err := c.Store.FactoryRun(ctx, runID)
	if err != nil {
		return empty, factory.ErrNotFound
	}
	if run.Role != project.RoleReviewer {
		return empty, errors.New("run is not a review")
	}
	if !run.Reconciled {
		return empty, errors.New("review run is not settled")
	}
	assignment, owner, err := c.publicationAssignmentsForRun(ctx, run)
	if err != nil {
		return empty, err
	}
	p, err := c.Store.PublicationByAssignment(ctx, owner.ID)
	if err != nil {
		return empty, err
	}
	if adopted, replayed, err := c.replaySubmittedReview(ctx, &p, run); err != nil {
		return empty, err
	} else if replayed {
		return adopted, nil
	}
	if p.Stage != factory.PublicationPublished || !p.HasPullTarget() {
		return empty, errors.New("review target is not published")
	}
	head := run.InputSHA
	if !factory.ValidCommit(head) {
		return empty, errors.New("review run lacks its exact head")
	}
	if head != p.Candidate {
		return empty, errors.New("review head superseded by a correction")
	}
	if !assignment.Succeeded() {
		return empty, errors.New("review lacks its recorded report")
	}
	w, err := c.buildReviewWork(ctx, p, owner, run, assignment, head)
	if err != nil {
		return empty, err
	}
	return c.submitFreshReview(ctx, &p, run.ID, w)
}

func (c *Coordinator) buildReviewWork(ctx context.Context, p factory.Publication, owner factory.Assignment, run factory.Run, assignment factory.Assignment, head string) (factory.ReviewWork, error) {
	var empty factory.ReviewWork
	resolved, err := resolveAssignmentVerdict(assignment)
	if err != nil {
		return empty, err
	}
	policy, err := c.Store.RepositoryPolicy(ctx, p.Repository)
	if err != nil {
		return empty, err
	}
	event, body, err := c.bindPhasedReviewVerdict(ctx, p, assignment, head, resolved)
	if err != nil {
		return empty, err
	}
	w := factory.ReviewWork{
		OperationID: "review-" + run.ID, AuthRevision: factory.ReviewAuthRevision(owner.ID, run.ID),
		Repository: p.Repository, ActorID: policy.Review.ActorID,
		PRNumber: p.PRNumber, PRID: p.PRID, IssueID: p.PullTarget().IssueID, PRAuthorID: p.PullTarget().AuthorID,
		HeadRef: p.PullTarget().HeadRef, BaseRef: p.PullTarget().BaseRef, HeadOID: head, BaseOID: p.PullTarget().BaseOID,
		Event: event, Body: body,
	}
	if p.PullTarget().HeadRepository != p.Repository {
		w.HeadRepository = p.PullTarget().HeadRepository
	}
	if policy.AutomaticReviewEvent(w.PRAuthorID, event) == "COMMENT" {
		w.AgentVerdict, w.Event = event, "COMMENT"
	} else {
		binding := policy.Review
		w.VoteBinding = &binding
	}
	observed, err := c.Reviews.ObserveReview(ctx, w)
	if err != nil {
		return empty, err
	}
	notAfter, err := c.Store.CapAttemptDeadline(ctx, owner.ID, time.Now().Add(5*time.Minute).Unix())
	if err != nil {
		return empty, err
	}
	w.NativeRev, w.NotAfter = observed.NativeRev, notAfter
	if err := w.Validate(); err != nil {
		return empty, err
	}
	return w, nil
}

func (c *Coordinator) submitFreshReview(ctx context.Context, p *factory.Publication, runID string, w factory.ReviewWork) (factory.ReviewOutcome, error) {
	var empty factory.ReviewOutcome
	reviewOperation := factory.ReviewOperation{RunID: runID, Work: w}
	if err := p.CanAppendReviewOperation(reviewOperation); err != nil {
		return empty, err
	}
	// Persist the exact authorization before the only submission call. The
	// store transaction shares the withdrawal gate, so a refused registration
	// has no native effect.
	p.ReviewOperations = append(p.ReviewOperations, reviewOperation)
	p.Revision++
	if err := c.Store.UpdatePublication(ctx, *p); err != nil {
		return empty, err
	}
	open, _, _, err := c.Store.DispatchState(ctx, p.Repository)
	if err != nil {
		return empty, errors.New("review dispatch state is unconfirmed after registration")
	}
	if !open {
		return empty, errors.New("review dispatch closed after registration")
	}
	outcome, err := c.Reviews.SubmitReview(ctx, w)
	if err != nil {
		return empty, err
	}
	updated, err := p.ReviewOperations[len(p.ReviewOperations)-1].WithOutcome(outcome)
	if err != nil {
		return empty, err
	}
	p.ReviewOperations[len(p.ReviewOperations)-1] = updated
	p.Revision++
	if err := c.Store.UpdatePublication(ctx, *p); err != nil {
		return empty, err
	}
	adopted, err := c.Reviews.AdoptReview(ctx, w, outcome)
	if err != nil {
		return empty, err
	}
	if outcome.Completion != factory.OpCompletionComplete || adopted.ReviewerID != w.ActorID || adopted.Event != w.Event || adopted.HeadOID != w.HeadOID {
		return empty, errors.New("review submission incomplete")
	}
	if err := c.recordReviewCandidateTuple(ctx, p, adopted); err != nil {
		return empty, err
	}
	return adopted, nil
}

// recordReviewCandidateTuple persists the first honestly observed
// merge-base for the publication's current candidate (R20): the
// review receipt natively observed head, base and their merge-base at
// submission, so the candidate tuple seeds exactly those values at
// revision zero and advances under CAS when a later review observes a
// new base. An unobserved merge-base seeds nothing: existing flows
// keep working and no value is invented. A moved base or a superseded
// head also seeds nothing here: base advances own their bounded
// correction flow (R21), and this joint never rewrites acceptance or
// persists a stale snapshot. An unchanged triple writes nothing:
// snapshots never churn.
func (c *Coordinator) recordReviewCandidateTuple(ctx context.Context, p *factory.Publication, adopted factory.ReviewOutcome) error {
	if !factory.ValidCommit(adopted.HeadOID) || !factory.ValidCommit(adopted.BaseOID) || !factory.ValidCommit(adopted.MergeBaseOID) {
		return nil
	}
	if adopted.HeadOID != p.Candidate || adopted.BaseOID != p.PullTarget().BaseOID {
		return nil
	}
	if p.Tuple != nil && p.Tuple.Head == adopted.HeadOID && p.Tuple.Base == adopted.BaseOID && p.Tuple.DiffBase == adopted.MergeBaseOID {
		return nil
	}
	tuple := factory.CandidateTuple{Head: adopted.HeadOID, Base: adopted.BaseOID, DiffBase: adopted.MergeBaseOID}
	if p.Tuple != nil {
		advanced, err := p.Tuple.Advance(p.Tuple.Revision, tuple.Head, tuple.Base, tuple.DiffBase)
		if err != nil {
			return err
		}
		tuple = advanced
	}
	if err := tuple.Validate(); err != nil {
		return err
	}
	next := *p
	next.Tuple = &tuple
	next.Revision++
	if err := c.Store.UpdatePublication(ctx, next); err != nil {
		return err
	}
	*p = next
	return nil
}

func sameOperationOutcome(a, b factory.OperationOutcome) bool {
	return a.OperationID == b.OperationID && a.InstallationID == b.InstallationID && a.Kind == b.Kind &&
		a.ActorID == b.ActorID && a.RepositoryID == b.RepositoryID && bytes.Equal(a.Receipt, b.Receipt) &&
		a.Effect == b.Effect && a.Cancellation == b.Cancellation && a.Completion == b.Completion &&
		a.Reason == b.Reason && a.NotObserved == b.NotObserved
}

func (c *Coordinator) recoverRecordedReview(ctx context.Context, p factory.Publication, op factory.ReviewOperation) (factory.OperationOutcome, error) {
	var empty factory.OperationOutcome
	if err := op.ValidateForPublication(p); err != nil {
		return empty, err
	}
	outcome, err := c.Reviews.LookupOp(ctx, op.Work.OperationID)
	if err != nil || !outcome.NotObserved {
		return outcome, err
	}
	recovery, ok := c.Reviews.(ReviewRecovery)
	if !ok {
		return outcome, nil
	}
	if p.Stage != factory.PublicationPublished || p.WithdrawRequested || p.Candidate != op.Work.HeadOID {
		return empty, &factory.PublicationWait{Reason: "review_recovery_closed"}
	}
	if time.Now().Unix() >= op.Work.NotAfter {
		return empty, &factory.PublicationWait{Reason: "review_authorization_expired"}
	}
	open, _, _, err := c.Store.DispatchState(ctx, p.Repository)
	if err != nil {
		return empty, err
	}
	if !open {
		return empty, &factory.PublicationWait{Reason: "dispatch_closed"}
	}
	return recovery.ResumeReview(ctx, op.Work)
}

func (c *Coordinator) reconcileRecordedReviews(ctx context.Context, p *factory.Publication, report *PublishReport) {
	for i := range p.ReviewOperations {
		op := p.ReviewOperations[i]
		prior := op.Outcome
		if prior.Effect == factory.OpEffectCommitted && prior.Completion == factory.OpCompletionComplete {
			// A crash can persist the completed review before its candidate tuple.
			// Reuse that receipt; never submit another review to fill the gap.
			if c.Reviews != nil && op.Work.HeadOID == p.Candidate && op.Work.BaseOID == p.PullTarget().BaseOID &&
				(p.Tuple == nil || p.Tuple.Head != op.Work.HeadOID || p.Tuple.Base != op.Work.BaseOID) {
				adopted, err := c.Reviews.AdoptReview(ctx, op.Work, prior)
				if err != nil || adopted.ReviewerID != op.Work.ActorID || adopted.Event != op.Work.Event || adopted.HeadOID != op.Work.HeadOID {
					publicationError(report, op.Work.OperationID, "review_receipt_unattributed")
					continue
				}
				if err := c.recordReviewCandidateTuple(ctx, p, adopted); err != nil {
					publicationError(report, op.Work.OperationID, "review_tuple_unrecorded")
				}
			}
			continue
		}
		if prior.Effect == factory.OpEffectNotCommitted && prior.Cancellation == factory.OpCancelCancelled {
			continue
		}
		if c.Reviews == nil {
			publicationWait(report, op.Work.OperationID, "review_reconciliation_unavailable")
			continue
		}
		outcome, err := c.recoverRecordedReview(ctx, *p, op)
		if err != nil {
			c.publicationCallError(report, op.Work.OperationID, err)
			continue
		}
		if outcome.NotObserved {
			publicationWait(report, op.Work.OperationID, "review_outcome_unresolved")
			continue
		}
		updated, err := op.WithOutcome(outcome)
		if err != nil {
			publicationError(report, op.Work.OperationID, "review_outcome_unattributed")
			continue
		}
		if !sameOperationOutcome(prior, updated.Outcome) {
			p.ReviewOperations[i] = updated
			p.Revision++
			if err := c.Store.UpdatePublication(ctx, *p); err != nil {
				publicationError(report, op.Work.OperationID, "store_unavailable")
				return
			}
		}
		if outcome.Effect == factory.OpEffectCommitted {
			adopted, err := c.Reviews.AdoptReview(ctx, op.Work, outcome)
			if err != nil || adopted.ReviewerID != op.Work.ActorID || adopted.Event != op.Work.Event || adopted.HeadOID != op.Work.HeadOID {
				publicationError(report, op.Work.OperationID, "review_receipt_unattributed")
				continue
			}
			if outcome.Completion != factory.OpCompletionComplete {
				publicationWait(report, op.Work.OperationID, "review_completion_pending")
			} else if err := c.recordReviewCandidateTuple(ctx, p, adopted); err != nil {
				publicationError(report, op.Work.OperationID, "review_tuple_unrecorded")
			}
		} else if outcome.Effect != factory.OpEffectNotCommitted {
			publicationWait(report, op.Work.OperationID, "review_effect_pending")
		}
	}
}

// publicationAssignmentsForRun resolves the run's own assignment and the
// coder assignment that owns its publication. Both scopes remain immutable;
// the native operation uses the publication owner while usage belongs to the run.
// reviewCyclePhase is the single D07 phase for publication review cycles.
// Reviewer and correction assignments for one round share the same cycle
// identity derivation; their assignment IDs stay distinct via role.
const reviewCyclePhase = "review"

// reviewCycleEvidenceDigest binds one cycle's inputs for the resolved
// current verdict: the v2 digest for typed verdicts.
func reviewCycleEvidenceDigest(head, base string, review ResolvedReviewVerdict, checks *factory.CheckAssessment) string {
	return reviewCycleInputDigestTyped(head, base, review.Review, review.Clear, checks)
}

// reviewCycleInputDigest binds one cycle's inputs: exact head/base plus the
// outstanding review/check evidence the child must answer. Fresh independent
// reviews bind head/base only; corrections and same-head re-reviews bind the
// prior review verdict/findings and check verdict. Sorted findings keep the
// digest stable; the hex encoding satisfies ValidDigest. New decisions change
// the digest (new round, fresh IDs); replay reuses the same digest (same
// round, same IDs).
func reviewCycleInputDigest(head, base string, review *factory.ReviewReport, checks *factory.CheckAssessment) string {
	h := sha256.New()
	h.Write([]byte("review-cycle-input-v1\x00" + head + "\x00" + base + "\x00"))
	if review != nil {
		h.Write([]byte(review.Verdict + "\x00" + review.Summary + "\x00" + review.Body + "\x00"))
		sorted := append([]string(nil), review.Findings...)
		sort.Strings(sorted)
		for _, f := range sorted {
			h.Write([]byte(f + "\x00"))
		}
	} else {
		h.Write([]byte("\x00"))
	}
	if checks != nil {
		h.Write([]byte(checks.Verdict + "\x00" + checks.Reason + "\x00"))
	} else {
		h.Write([]byte("\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// reviewCycleInputDigestTyped binds one cycle's inputs for a typed current
// verdict: exact head/base plus the typed review or clear evidence the
// child must answer, with the check verdict. Typed rounds stay distinct
// by digest domain; new decisions change the digest
// (new round, fresh IDs) and replay reuses it. Exactly one of review and
// clear is set; nil evidence means a fresh independent review.
func reviewCycleInputDigestTyped(head, base string, review *factory.ReviewCycleReport, clear *factory.ClearReviewReport, checks *factory.CheckAssessment) string {
	h := sha256.New()
	h.Write([]byte("review-cycle-input-v2\x00" + head + "\x00" + base + "\x00"))
	if review != nil {
		h.Write([]byte("review\x00" + review.CycleID + "\x00"))
		keys := make([]string, 0, len(review.NewFindings)+len(review.Assessments))
		for _, finding := range review.NewFindings {
			h.Write([]byte(finding.Key + "\x00" + finding.Severity + "\x00" + finding.FixKind + "\x00" +
				finding.Defect + "\x00" + finding.Evidence + "\x00" + finding.RequiredOutcome + "\x00"))
			keys = append(keys, finding.Key)
		}
		for _, assessment := range review.Assessments {
			h.Write([]byte(assessment.Key + "\x00" + assessment.Status + "\x00" + assessment.Evidence + "\x00"))
			keys = append(keys, assessment.Key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			h.Write([]byte(key + "\x00"))
		}
	} else if clear != nil {
		h.Write([]byte("clear\x00" + clear.CycleID + "\x00" + clear.Head + "\x00" + clear.BaseOID + "\x00" + clear.CoverageDigest + "\x00"))
		for _, assessment := range clear.Assessments {
			h.Write([]byte(assessment.Key + "\x00" + assessment.Status + "\x00" + assessment.Evidence + "\x00"))
		}
	} else {
		h.Write([]byte("\x00"))
	}
	if checks != nil {
		h.Write([]byte(checks.Verdict + "\x00" + checks.Reason + "\x00"))
	} else {
		h.Write([]byte("\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func maxReviewCycleRound(p factory.Publication) int {
	max := 0
	for _, c := range p.ReviewCycles {
		if c.Round > max {
			max = c.Round
		}
	}
	return max
}

func reviewCycleHeadSeen(p factory.Publication, head string) bool {
	for _, c := range p.ReviewCycles {
		if c.Head == head {
			return true
		}
	}
	return false
}

// reviewCycleAssignmentID derives fresh reviewer/correction assignment IDs
// from the cycle identity and role: publication, attempt root, round and
// phase via the cycle ID, plus role for reviewer/corrector distinctness. New
// rounds are distinct; replay of a round is identical.
func reviewCycleAssignmentID(cycleID, role string) string {
	sum := sha256.Sum256([]byte("soda-factory-cycle-child-v1\x00" + cycleID + "\x00" + role))
	return hex.EncodeToString(sum[:16])
}

// reviewCyclePreparationID derives a fresh reviewer preparation identity
// from the cycle identity. Reviewer sessions are fresh per same-head round;
// native corrections derive their own candidate checkout from the assignment.
func reviewCyclePreparationID(cycleID string) string {
	sum := sha256.Sum256([]byte("soda-factory-review-preparation-v1\x00" + cycleID))
	return "f" + hex.EncodeToString(sum[:12])
}

// ensureReviewCycle persists one durable D07 review cycle for the child being
// dispatched and returns the cycle plus the exact child/preparation IDs to
// use. First occurrence of a head reuses the head-based IDs so
// pre-cycle publications keep their reviewer binding; repeats on the same
// head use round-derived fresh IDs (new decision distinct, replay identical).
// Reviewer rounds leave CorrectionAssignment empty; correction rounds record
// a fresh (unused) reviewer ID alongside the used correction ID so the cycle
// always validates while reviewer/corrector sessions stay distinct.
func replayReviewCycle(p *factory.Publication, owner factory.Assignment, role, digest, inputDigest string, nativeCorrection bool) (factory.ReviewCycle, string, string, bool, error) {
	var empty factory.ReviewCycle
	if n := len(p.ReviewCycles); n > 0 {
		last := p.ReviewCycles[n-1]
		if last.Head == p.Candidate && last.BaseOID == p.PullTarget().BaseOID && last.FindingDigest == digest && last.InputDigest == inputDigest {
			var childID, prepID string
			if role == project.RoleReviewer {
				childID = last.ReviewerAssignment
				// Replay keeps the recorded IDs: first occurrence of a head
				// used head-based IDs, repeats used round-derived IDs.
				// Infer from the stored child ID so new-head rounds
				// (even when round > 1) replay their preparation.
				if childID == publicationChildID(p.ID, p.Candidate, project.RoleReviewer) {
					prepID = publicationPreparationID(p.ID, p.Candidate)
				} else {
					prepID = reviewCyclePreparationID(last.ID)
				}
			} else {
				childID = last.CorrectionAssignment
				if childID == "" {
					return empty, "", "", false, errors.New("recorded cycle lacks its correction assignment")
				}
				prepID = owner.Preparation
				if nativeCorrection {
					prepID = factory.NativeCorrectionPreparationID(childID)
				}
			}
			if childID == "" {
				return empty, "", "", false, errors.New("recorded cycle lacks its child assignment")
			}
			return last, childID, prepID, true, nil
		}
	}
	return empty, "", "", false, nil
}

func (c *Coordinator) ensureReviewCycle(ctx context.Context, p *factory.Publication, owner factory.Assignment, role, digest, inputDigest, triggerDecision, triggerReason string) (factory.ReviewCycle, string, string, error) {
	var empty factory.ReviewCycle
	origin, err := c.Store.PublicationExecutionOwner(ctx, *p, owner)
	if err != nil {
		return empty, "", "", err
	}
	nativeCorrection := origin.NativeTool != nil
	if !factory.ValidDigest(digest) || strings.TrimSpace(triggerDecision) == "" || strings.TrimSpace(triggerReason) == "" {
		return empty, "", "", errors.New("review cycle needs its digest and triggering decision")
	}
	if inputDigest != "" && !factory.ValidDigest(inputDigest) {
		return empty, "", "", errors.New("review cycle needs its valid input digest")
	}
	if role != project.RoleReviewer && role != project.RoleCoder {
		return empty, "", "", errors.New("review cycle needs its reviewer or corrector role")
	}
	// Replay: the latest round already binds these exact inputs. Both
	// the finding-set binding and the complete input fingerprint must
	// match: same keys with changed review/check inputs open a new round
	// instead of silently reusing the prior admitted correction.
	if last, childID, prepID, replayed, err := replayReviewCycle(p, owner, role, digest, inputDigest, nativeCorrection); err != nil {
		return empty, "", "", err
	} else if replayed {
		return last, childID, prepID, nil
	}
	round := maxReviewCycleRound(*p) + 1
	if round < 1 {
		round = 1
	}
	cycleID := factory.ReviewCycleID(p.ID, owner.AttemptRoot, round, reviewCyclePhase)
	firstHead := !reviewCycleHeadSeen(*p, p.Candidate)
	var reviewerAssignment, correctionAssignment, childID, prepID string
	if role == project.RoleReviewer {
		if firstHead && round == 1 {
			childID = publicationChildID(p.ID, p.Candidate, project.RoleReviewer)
			prepID = publicationPreparationID(p.ID, p.Candidate)
		} else if firstHead {
			// New head after prior rounds: head-based IDs keep the
			// reviewer binding stable per head (fresh via new head).
			childID = publicationChildID(p.ID, p.Candidate, project.RoleReviewer)
			prepID = publicationPreparationID(p.ID, p.Candidate)
		} else {
			childID = reviewCycleAssignmentID(cycleID, project.RoleReviewer)
			prepID = reviewCyclePreparationID(cycleID)
		}
		reviewerAssignment, correctionAssignment = childID, ""
	} else {
		if firstHead {
			// First correction on a head that only has reviewer cycles so
			// far still gets a head-stable ID when the head is new;
			// same-head repeats use round-derived fresh IDs below.
			// Corrections share the head with their reviewer round, so a
			// head seen only by reviewer rounds still counts as seen.
			// Use derived IDs whenever any cycle exists for this head to
			// keep reviewer/corrector distinct per round.
			if len(p.ReviewCycles) > 0 {
				reviewerAssignment = reviewCycleAssignmentID(cycleID, project.RoleReviewer)
				correctionAssignment = reviewCycleAssignmentID(cycleID, project.RoleCoder)
				childID = correctionAssignment
			} else {
				reviewerAssignment = publicationChildID(p.ID, p.Candidate, project.RoleReviewer)
				correctionAssignment = publicationChildID(p.ID, p.Candidate, project.RoleCoder)
				childID = correctionAssignment
			}
		} else {
			reviewerAssignment = reviewCycleAssignmentID(cycleID, project.RoleReviewer)
			correctionAssignment = reviewCycleAssignmentID(cycleID, project.RoleCoder)
			childID = correctionAssignment
		}
		prepID = owner.Preparation
		if nativeCorrection {
			prepID = factory.NativeCorrectionPreparationID(childID)
		}
	}
	cycle := factory.ReviewCycle{
		PublicationID: p.ID, AttemptRoot: owner.AttemptRoot, ID: cycleID, Phase: reviewCyclePhase,
		TriggerDecision: triggerDecision, TriggerReason: triggerReason,
		Head: p.Candidate, BaseOID: p.PullTarget().BaseOID, FindingDigest: digest, InputDigest: inputDigest,
		ReviewerAssignment: reviewerAssignment, CorrectionAssignment: correctionAssignment, Round: round,
	}
	if err := cycle.Validate(); err != nil {
		return empty, "", "", err
	}
	p.ReviewCycles = append(p.ReviewCycles, cycle)
	p.Revision++
	if err := c.Store.UpdatePublication(ctx, *p); err != nil {
		// Drop the optimistic append; the next pass re-reads and replays.
		p.ReviewCycles = p.ReviewCycles[:len(p.ReviewCycles)-1]
		p.Revision--
		return empty, "", "", err
	}
	return cycle, childID, prepID, nil
}

func (c *Coordinator) admitRunAssignment(ctx context.Context, run factory.Run, view factory.RunView) (factory.Assignment, error) {
	assignment, err := c.Store.Assignment(ctx, view.Attempt)
	if err != nil {
		return factory.Assignment{}, err
	}
	if assignment.Validate() != nil || assignment.Run != run.ID || assignment.ProjectID != run.ProjectID ||
		assignment.Role != run.Role || assignment.SourceCommit != run.InputSHA ||
		assignment.Repository != view.Repository || assignment.Issue != view.Issue {
		return factory.Assignment{}, errors.New("run differs from its recorded assignment")
	}
	return assignment, nil
}

func (c *Coordinator) admitPublicationOwner(ctx context.Context, assignment factory.Assignment) (factory.Assignment, error) {
	owner := assignment
	if assignment.PublicationAssignment != assignment.ID {
		var err error
		owner, err = c.Store.Assignment(ctx, assignment.PublicationAssignment)
		if err != nil {
			return factory.Assignment{}, err
		}
	}
	if owner.Validate() != nil || owner.PublicationAssignment != owner.ID || owner.Role != project.RoleCoder ||
		owner.Repository != assignment.Repository || owner.Issue != assignment.Issue ||
		owner.ProjectID != assignment.ProjectID || owner.Acceptance != assignment.Acceptance ||
		owner.AttemptRoot != assignment.AttemptRoot ||
		(assignment.Role == project.RoleCoder && !assignment.PublicationCoderPreparation(owner)) {
		return factory.Assignment{}, errors.New("assignment differs from its publication owner")
	}
	return owner, nil
}

func (c *Coordinator) publicationAssignmentsForRun(ctx context.Context, run factory.Run) (factory.Assignment, factory.Assignment, error) {
	view, err := c.Store.FactoryRunView(ctx, run.ID)
	if err != nil {
		return factory.Assignment{}, factory.Assignment{}, err
	}
	assignment, err := c.admitRunAssignment(ctx, run, view)
	if err != nil {
		return factory.Assignment{}, factory.Assignment{}, err
	}
	owner, err := c.admitPublicationOwner(ctx, assignment)
	if err != nil {
		return factory.Assignment{}, factory.Assignment{}, err
	}
	return assignment, owner, nil
}
