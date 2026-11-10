package control

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// CorrectionLink is one publication advanced to a corrected head: the new
// exact head, the committed correction operation and the stored revision.
type CorrectionLink struct {
	AssignmentID  string `json:"assignment_id"`
	PublicationID string `json:"publication_id"`
	HeadOID       string `json:"head_oid"`
	OperationID   string `json:"operation_id"`
	Revision      int64  `json:"revision"`
}

// CorrectionWait is one correction that did not advance this call and why.
type CorrectionWait struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
	Detail string `json:"Detail,omitempty"`
}

// CorrectionError is one unexpected correction infrastructure failure. The
// affected publication keeps its recorded state; a later call retries.
type CorrectionError struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
	Detail string `json:"Detail,omitempty"`
}

// CorrectionReport is the durable outcome of one correction call: the
// publication it advanced and every publication it fenced, or why the
// correction waited or errored.
type CorrectionReport struct {
	Corrected []CorrectionLink  `json:"corrected"`
	Fenced    []string          `json:"fenced,omitempty"`
	Waits     []CorrectionWait  `json:"waits,omitempty"`
	Errors    []CorrectionError `json:"errors,omitempty"`
}

func correctionError(report *CorrectionReport, id, reason string) {
	report.Errors = append(report.Errors, CorrectionError{ID: id, Reason: reason})
}

func correctionWait(report *CorrectionReport, id, reason string) {
	report.Waits = append(report.Waits, CorrectionWait{ID: id, Reason: reason})
}

// PublishCorrection advances one published publication to the exact head a
// reconciled coder child reported for its parent publication. The
// run output nominates the candidate; the host export attests it, and the
// conditional branch operation commits it to the same branch and PR. One
// correction identity per run: a finished correction for a run never
// reopens, and only a newer run's different candidate opens the next
// identity. The initial publication receipts stay untouched; corrections
// chain behind them. The stored head always follows the latest committed
// correction, so every store point satisfies publication validation.
// correctionAdmission binds one correction run to its linked
// publication and reported result.
type correctionAdmission struct {
	run        factory.Run
	assignment factory.Assignment
	a          factory.Assignment
	p          factory.Publication
	reported   factory.Result
}

func (c *Coordinator) PublishCorrection(ctx context.Context, runID string) CorrectionReport {
	report := CorrectionReport{Corrected: []CorrectionLink{}}
	admission, ok := c.admitCorrectionRequest(ctx, runID, &report)
	if !ok {
		return report
	}
	if !c.checkRecordedCorrection(ctx, admission, runID, &report) {
		return report
	}
	continuing, ok := checkCorrectionIdentity(admission, &report)
	if !ok {
		return report
	}
	work, ok := c.prepareCorrectionWork(ctx, admission, runID, continuing, &report)
	if !ok {
		return report
	}
	c.driveCorrectionOperation(ctx, admission, work, &report)
	return report
}

// admitCorrectionRequest binds one correction run to its linked
// publication and reported candidate.
func (c *Coordinator) admitCorrectionRequest(ctx context.Context, runID string, report *CorrectionReport) (*correctionAdmission, bool) {
	if c.Publication == nil {
		correctionError(report, runID, "operations_unavailable")
		return nil, false
	}
	run, err := c.Store.FactoryRun(ctx, runID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			correctionWait(report, runID, "run_missing")
		} else {
			correctionError(report, runID, "store_unavailable")
		}
		return nil, false
	}
	if !run.Reconciled {
		correctionWait(report, runID, "run_unsettled")
		return nil, false
	}
	assignment, a, err := c.publicationAssignmentsForRun(ctx, run)
	if err != nil || assignment.ID == a.ID || assignment.Role != project.RoleCoder {
		correctionWait(report, runID, "run_unlinked")
		return nil, false
	}
	p, err := c.Store.PublicationByAssignment(ctx, a.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			correctionWait(report, a.ID, "publication_missing")
		} else {
			correctionError(report, a.ID, "store_unavailable")
		}
		return nil, false
	}
	admission := &correctionAdmission{run: run, assignment: assignment, a: a, p: p, reported: assignment.Result.ToResult()}
	if !checkCorrectionPublished(admission, report) {
		return nil, false
	}
	open, _, _, err := c.Store.DispatchState(ctx, p.Repository)
	if err != nil {
		correctionError(report, p.ID, "store_unavailable")
		return nil, false
	}
	if p.WithdrawRequested || !open {
		correctionWait(report, p.ID, "correction_withdrawn")
		return nil, false
	}
	return admission, true
}

// checkCorrectionPublished requires the linked publication and the
// corrector's reported candidate behind one correction.
func checkCorrectionPublished(admission *correctionAdmission, report *CorrectionReport) bool {
	p, assignment := admission.p, admission.assignment
	if p.Stage != factory.PublicationPublished || !p.HasPullTarget() || p.InitialCandidate()=="" {
		correctionWait(report, p.ID, "publication_unpublished")
		return false
	}
	if assignment.Stage != factory.AssignmentFinished || assignment.Outcome != factory.Succeeded ||
		assignment.Result == nil || !assignment.Result.Reported || assignment.Result.Status != "completed" ||
		!factory.ValidCommit(assignment.Result.Candidate) {
		correctionWait(report, p.ID, "candidate_invalid")
		return false
	}
	return true
}

// checkRecordedCorrection reconciles the in-flight recorded
// correction, refusing new writes while one already settled this
// call or the source head went stale.
func (c *Coordinator) checkRecordedCorrection(ctx context.Context, admission *correctionAdmission, runID string, report *CorrectionReport) bool {
	c.reconcileRecordedCorrection(ctx, &admission.p, admission.a, report)
	if len(report.Errors) != 0 || len(report.Fenced) != 0 || len(report.Waits) != 0 || len(report.Corrected) != 0 {
		return false
	}
	// A child admitted for an older publication head may finish after a
	// different correction advances the branch. Its recorded intent above
	// still gets reconciled, but stale source work cannot open a new write.
	if admission.p.Run != runID && admission.assignment.SourceCommit != admission.p.Candidate {
		correctionWait(report, admission.p.ID, "candidate_superseded")
		return false
	}
	return true
}

// checkCorrectionIdentity continues a recorded correction under its
// identity or requires a genuinely newer candidate.
func checkCorrectionIdentity(admission *correctionAdmission, report *CorrectionReport) (bool, bool) {
	p := admission.p
	// One identity per run: a recorded correction for this same candidate
	// either continues under its identity or stays finished. Only a
	// different candidate opens the next identity.
	if latest := len(p.Corrections); latest > 0 && p.Corrections[latest-1].Work != nil &&
		p.Corrections[latest-1].Work.Candidate == admission.reported.Candidate {
		switch p.Corrections[latest-1].Effect {
		case "", factory.OpEffectPending:
			return true, true
		case factory.OpEffectCommitted:
			correctionWait(report, p.ID, "correction_recorded")
			return false, false
		default:
			correctionWait(report, p.ID, "correction_refused")
			return false, false
		}
	}
	// An unchanged head is not a correction and fences nothing: the run
	// reported no newer candidate, so correction waits without mutating
	// the publication. Fencing stays for genuinely invalid observations.
	if admission.reported.Candidate == p.Candidate || admission.reported.Candidate == p.BaseSHA {
		correctionWait(report, p.ID, "candidate_invalid")
		return false, false
	}
	return false, true
}

// prepareCorrectionWork charges usage, resolves the export and the
// authority bracket, and records the correction attempt.
func (c *Coordinator) prepareCorrectionWork(ctx context.Context, admission *correctionAdmission, runID string, continuing bool, report *CorrectionReport) (factory.PublicationWork, bool) {
	var work factory.PublicationWork
	p := admission.p
	if !continuing && len(admission.run.ExecutionManifest) > 0 {
		if reason := c.correctionCheckpointGate(ctx, admission); reason != "" {
			correctionWait(report, p.ID, reason)
			return work, false
		}
	}
	if err := recordConfirmedUsage(ctx, c.Store, admission.assignment, admission.run, time.Now()); err != nil {
		correctionError(report, p.ID, "store_unavailable")
		return work, false
	}
	q := p
	q.Run, q.Candidate = runID, admission.reported.Candidate
	bundle, terminal, wait := c.exportCandidate(ctx, q, admission.run)
	if wait != nil {
		correctionWait(report, p.ID, wait.Reason)
		return work, false
	}
	if terminal != nil {
		correctionError(report, p.ID, terminal.Reason)
		return work, false
	}
	scratch := PublishReport{}
	policy, revision, allowed := c.publicationAuthority(ctx, p, &scratch)
	if !allowed {
		// The authority check reports exactly one wait or error.
		for _, w := range scratch.Waits {
			correctionWait(report, p.ID, w.Reason)
		}
		for _, e := range scratch.Errors {
			correctionError(report, p.ID, e.Reason)
		}
		return work, false
	}
	work = correctionAttemptWork(admission, p, runID, bundle, policy, len(p.Corrections))
	if continuing {
		// A crash between record and push resumes under the recorded
		// identity with a fresh bundle; the recorded intent stands.
		recorded := p.Corrections[len(p.Corrections)-1]
		return recorded.Work.Apply(work), true
	}
	recorded, ok := c.observeCorrectionAttempt(ctx, admission, runID, work, revision, report)
	if !ok {
		return work, false
	}
	return recorded, true
}

func (c *Coordinator) correctionCheckpointGate(ctx context.Context, admission *correctionAdmission) string {
	run := admission.run
	manifest, err := project.ParseExecutionManifest(run.ExecutionManifest)
	var plan factory.InvestigationPlan
	if err != nil || manifest.Phase != project.ExecutionCorrect || manifest.Run != run.ID ||
		json.Unmarshal(manifest.Plan, &plan) != nil || plan.AttemptRoot != admission.assignment.AttemptRoot ||
		plan.SourceCommit != admission.assignment.SourceCommit {
		return PlanRefuseEvidence
	}
	state, err := c.Host.InspectRun(ctx, execution.RunAddress{Workspace: run.ProjectID, Run: run.ID})
	if err != nil {
		return PlanWaitEvidenceUnavailable
	}
	drained, _, err := c.importRunTaskEvents(ctx, run, state)
	if err != nil || !drained {
		return PlanWaitTasksIncomplete
	}
	receipts, err := c.Store.PlanTaskReceipts(ctx, plan.AttemptRoot, plan.ID)
	if err != nil {
		return PlanWaitEvidenceUnavailable
	}
	verdict := judgePlanPublication(plan, receipts, admission.reported.Candidate)
	if verdict.RefuseReason != "" {
		return verdict.RefuseReason
	}
	if !verdict.Ready {
		return verdict.WaitReason
	}
	return finalCheckpointGate(run, plan, state, admission.reported.Candidate)
}

// correctionAttemptWork builds the fresh correction work behind one
// admitted correction.
func correctionAttemptWork(admission *correctionAdmission, p factory.Publication, runID string, bundle []byte, policy factory.RepositoryPolicy, corrections int) factory.PublicationWork {
	a := admission.a
	return factory.PublicationWork{
		Bundle: bundle, AssignmentID: a.ID, Publication: p.ID, RunID: runID, Run: admission.run,
		Candidate: admission.reported.Candidate, BaseSHA: p.BaseSHA, TargetBranch: p.TargetBranch,
		OperationID:  factory.PublicationOperationID(p.ID, factory.OpRefPublish, corrections+2),
		AuthRevision: factory.AuthRevisionFor(a.ID, p.ID, p.Revision),
		ExpectedOld:  p.Candidate, ComparisonRef: p.TargetBranch,
		// The PR title/body stay the linked PR's own: a correction moves
		// the branch, never the recorded PR description.
		PRTitle:    factory.PRTitleFor(a.Issue),
		PRBody:     factory.PRBodyFor(a.Issue, a.ID, a.Acceptance, a.Run, p.Candidate, a.SourceCommit),
		Repository: p.Repository, Issue: p.Issue, ActorID: policy.Publish.ActorID,
		CorrectionNumber: p.PRNumber, CorrectionAuthor: p.PullTarget().AuthorID,
	}
}

// observeCorrectionAttempt observes the native branch and records one
// correction attempt behind the fresh work.
func (c *Coordinator) observeCorrectionAttempt(ctx context.Context, admission *correctionAdmission, runID string, work factory.PublicationWork, revision int64, report *CorrectionReport) (factory.PublicationWork, bool) {
	observation, err := c.Publication.ObservePublication(ctx, work)
	if err != nil {
		c.correctionCallError(report, admission.p.ID, err)
		return work, false
	}
	if !c.checkCorrectionObservation(ctx, admission, observation, revision, report) {
		return work, false
	}
	now := time.Now()
	notAfter, err := c.Store.CapAttemptDeadline(ctx, admission.p.AssignmentID, now.Add(10*time.Minute).Unix())
	if err != nil {
		correctionWait(report, admission.p.ID, "attempt_deadline_unavailable")
		return work, false
	}
	work.NativeRev, work.ComparisonOID, work.NotAfter = observation.NativeRev, observation.Comparison, notAfter
	intent := work.Intent()
	op := factory.PublicationOperation{
		Work: &intent, RunID: runID, OperationID: work.OperationID, Kind: factory.OpRefPublish,
		Attempts: 1, UpdatedUnix: now.Unix(),
	}
	p := admission.p
	p.Corrections = append(p.Corrections, op)
	if !c.storeCorrection(ctx, &p, report) {
		return work, false
	}
	admission.p = p
	return p.Corrections[len(p.Corrections)-1].Work.Apply(work), true
}

// checkCorrectionObservation matches one correction observation
// against the accepted revision, branch and recorded head.
func (c *Coordinator) checkCorrectionObservation(ctx context.Context, admission *correctionAdmission, observation factory.PublicationObservation, revision int64, report *CorrectionReport) bool {
	if observation.NativeRev != revision {
		correctionWait(report, admission.p.ID, "native_inputs_changed")
		return false
	}
	if observation.TargetRef != admission.p.PublicationRef() || !factory.ValidCommit(observation.Comparison) {
		correctionWait(report, admission.p.ID, "observation_invalid")
		return false
	}
	if observation.TargetTip != admission.p.Candidate {
		c.fenceCorrection(ctx, report, &admission.p, factory.PublishReasonBadObservation)
		return false
	}
	return true
}

// driveCorrectionOperation submits, pushes and settles the recorded
// correction operation.
func (c *Coordinator) driveCorrectionOperation(ctx context.Context, admission *correctionAdmission, work factory.PublicationWork, report *CorrectionReport) {
	op := admission.p.Corrections[len(admission.p.Corrections)-1]
	if op.Cancellation != "" && op.Cancellation != factory.OpCancelNone {
		correctionWait(report, admission.p.ID, "cancellation_pending")
		return
	}
	// Recheck the local gate after recording. A concurrent withdrawal
	// cancels this same recorded identity.
	open, _, _, err := c.Store.DispatchState(ctx, admission.p.Repository)
	if err != nil {
		correctionError(report, admission.p.ID, "store_unavailable")
		return
	}
	if admission.p.WithdrawRequested || !open {
		c.cancelRecordedCorrection(ctx, &admission.p, report)
		return
	}
	if !c.submitCorrectionOp(ctx, admission, work, op, report) {
		return
	}
	op = admission.p.Corrections[len(admission.p.Corrections)-1]
	if !c.pushCorrectionBranch(ctx, admission, work, op, report) {
		return
	}
	op = admission.p.Corrections[len(admission.p.Corrections)-1]
	c.settleCorrectionOp(ctx, admission, op, report)
}

// submitCorrectionOp submits one recorded correction whose native
// effect is still empty.
func (c *Coordinator) submitCorrectionOp(ctx context.Context, admission *correctionAdmission, work factory.PublicationWork, op factory.PublicationOperation, report *CorrectionReport) bool {
	if op.Effect != "" {
		return true
	}
	outcome, err := c.Publication.SubmitPublish(ctx, work)
	if err != nil {
		c.correctionCallError(report, admission.p.ID, err)
		return false
	}
	if outcome.NotObserved {
		correctionWait(report, admission.p.ID, "submit_unconfirmed")
		return false
	}
	return c.adoptCorrectionOutcome(ctx, &admission.p, outcome, report)
}

// pushCorrectionBranch pushes one pending correction branch.
func (c *Coordinator) pushCorrectionBranch(ctx context.Context, admission *correctionAdmission, work factory.PublicationWork, op factory.PublicationOperation, report *CorrectionReport) bool {
	if op.Effect != factory.OpEffectPending || (op.Cancellation != "" && op.Cancellation != factory.OpCancelNone) {
		return true
	}
	outcome, err := c.Publication.PushBranch(ctx, work)
	if err != nil {
		c.correctionCallError(report, admission.p.ID, err)
		return false
	}
	if outcome.NotObserved {
		correctionWait(report, admission.p.ID, "push_unconfirmed")
		return false
	}
	return c.adoptCorrectionOutcome(ctx, &admission.p, outcome, report)
}

// settleCorrectionOp links, refuses, fences or waits on one
// submitted correction by its effect.
func (c *Coordinator) settleCorrectionOp(ctx context.Context, admission *correctionAdmission, op factory.PublicationOperation, report *CorrectionReport) {
	switch op.Effect {
	case factory.OpEffectCommitted:
		c.linkCorrection(ctx, &admission.p, admission.a, report)
	case factory.OpEffectNotCommitted:
		correctionWait(report, admission.p.ID, "correction_refused")
	case factory.OpEffectIndeterminate:
		c.fenceCorrection(ctx, report, &admission.p, factory.PublishReasonFenced)
	default:
		correctionWait(report, admission.p.ID, "operation_pending")
	}
}

// PublishCorrectionWithReport advances one corrector run's candidate like
// PublishCorrection after journaling the run's parsed correction report
// through the canonical gate. The caller parses the corrector output's
// correction block (ParseCorrectionReport) and supplies the report with
// its fix evidence and check assessment; this entry resolves the review
// cycle the corrector answered, admits the report, and records exactly
// one journal entry per run over the cycle's exact finding-set digest.
// The canonical binding applies when the cycle already carries a journal:
// a retry must resubmit byte-identical findings, so a rewritten defect
// refuses instead of replacing the recorded set. A disputed report still
// journals — the dispute is evidence for the bounded D08 decision — but
// the head does not advance on contested feedback: the entry waits for
// the funded decision instead of launching another run, and the routing
// leg reads the journaled dispositions live. Any other refused report
// waits without mutating; admitted reports delegate the head advance to
// PublishCorrection. Runs without a parsed report keep the legacy entry;
// this entry never guesses dispositions.
func (c *Coordinator) PublishCorrectionWithReport(ctx context.Context, runID string, report factory.CorrectionReport, evidence []factory.CorrectionFixEvidence, checks *factory.CheckAssessment) CorrectionReport {
	served := CorrectionReport{Corrected: []CorrectionLink{}}
	admission, ok := c.admitCorrectionReportRun(ctx, runID, &served)
	if !ok {
		return served
	}
	if result, handled := c.replayCorrectionJournal(ctx, admission.p, runID, report, &served); handled {
		return result
	}
	if !checkCorrectionCandidate(admission.assignment, admission.p.ID, &served) {
		return served
	}
	newCandidate := admission.assignment.Result.Candidate
	cycle, ok := findCorrectionCycle(admission.p, admission.assignment.ID, &served)
	if !ok {
		return served
	}
	if !c.admitCorrectionFindings(ctx, &admission.p, cycle, runID, newCandidate, report, evidence, checks, &served) {
		return served
	}
	if journalErr := c.journalCorrectionReport(ctx, &admission.p, cycle, runID, report); journalErr != nil {
		correctionError(&served, admission.p.ID, "store_unavailable")
		return served
	}
	return c.PublishCorrection(ctx, runID)
}

// admitCorrectionReportRun binds one reported correction run to its
// linked publication.
func (c *Coordinator) admitCorrectionReportRun(ctx context.Context, runID string, served *CorrectionReport) (*correctionAdmission, bool) {
	if c.Store == nil {
		correctionError(served, runID, "operations_unavailable")
		return nil, false
	}
	run, err := c.Store.FactoryRun(ctx, runID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			correctionWait(served, runID, "run_missing")
		} else {
			correctionError(served, runID, "store_unavailable")
		}
		return nil, false
	}
	if !run.Reconciled {
		correctionWait(served, runID, "run_unsettled")
		return nil, false
	}
	assignment, owner, err := c.publicationAssignmentsForRun(ctx, run)
	if err != nil || assignment.ID == owner.ID || assignment.Role != project.RoleCoder {
		correctionWait(served, runID, "run_unlinked")
		return nil, false
	}
	p, err := c.Store.PublicationByAssignment(ctx, owner.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			correctionWait(served, owner.ID, "publication_missing")
		} else {
			correctionError(served, owner.ID, "store_unavailable")
		}
		return nil, false
	}
	return &correctionAdmission{run: run, assignment: assignment, a: owner, p: p}, true
}

// replayCorrectionJournal replays the recorded journal behind a run
// that already reported, refusing rewritten replays and holding
// journaled disputes.
func (c *Coordinator) replayCorrectionJournal(ctx context.Context, p factory.Publication, runID string, report factory.CorrectionReport, served *CorrectionReport) (CorrectionReport, bool) {
	// Replay reuses the recorded journal: a run that already reported
	// never journals twice, and the head advance below replays too. A
	// replay must preserve the entire recorded report, including its
	// dispositions, candidate and evidence. A journaled dispute remains
	// held; recording the report did not authorize its candidate.
	for _, journal := range p.CorrectionJournals {
		if journal.RunID == runID {
			if !reflect.DeepEqual(journal.Report, report) {
				correctionWait(served, p.ID, "correction_refused")
				served.Waits[len(served.Waits)-1].Detail = "correction replay differs from its recorded report"
				return *served, true
			}
			for _, disposition := range journal.Report.Dispositions {
				if disposition.Disposition == factory.DispositionDisputed {
					correctionWait(served, p.ID, "delivery_judgment_required")
					return *served, true
				}
			}
			return c.PublishCorrection(ctx, runID), true
		}
	}
	return CorrectionReport{}, false
}

// checkCorrectionCandidate requires the corrector's finished,
// reported candidate behind one correction report.
func checkCorrectionCandidate(assignment factory.Assignment, publicationID string, served *CorrectionReport) bool {
	if assignment.Stage != factory.AssignmentFinished || assignment.Outcome != factory.Succeeded ||
		assignment.Result == nil || !assignment.Result.Reported || assignment.Result.Status != "completed" ||
		!factory.ValidCommit(assignment.Result.Candidate) {
		correctionWait(served, publicationID, "candidate_invalid")
		return false
	}
	return true
}

// findCorrectionCycle resolves the review cycle one corrector run
// answered, requiring a current cycle head.
func findCorrectionCycle(p factory.Publication, assignmentID string, served *CorrectionReport) (factory.ReviewCycle, bool) {
	var none factory.ReviewCycle
	for i := range p.ReviewCycles {
		if p.ReviewCycles[i].CorrectionAssignment == assignmentID {
			cycle := p.ReviewCycles[i]
			// A superseded cycle answers another tree: the publication moved on
			// before this report arrived, so no new journal may append merely
			// because the eventual ref push would refuse. Already-recorded
			// operations keep their replay recovery above; only first effects
			// wait here.
			if cycle.Head != p.Candidate || cycle.BaseOID != p.PullTarget().BaseOID {
				correctionWait(served, p.ID, "correction_cycle_superseded")
				return none, false
			}
			return cycle, true
		}
	}
	correctionWait(served, p.ID, "correction_cycle_unknown")
	return none, false
}

// admitCorrectionFindings admits one correction report against the
// cycle's canonical findings, journaling disputes for D08 routing.
func (c *Coordinator) admitCorrectionFindings(ctx context.Context, p *factory.Publication, cycle factory.ReviewCycle, runID, newCandidate string, report factory.CorrectionReport, evidence []factory.CorrectionFixEvidence, checks *factory.CheckAssessment, served *CorrectionReport) bool {
	var canonical []factory.Finding
	for _, journal := range p.CorrectionJournals {
		if journal.CycleID == cycle.ID {
			canonical = journal.Report.Findings
			break
		}
	}
	var err error
	if canonical != nil {
		_, err = AdmitCorrectionReportCanonical(cycle, runID, newCandidate, canonical, report, evidence, checks)
	} else {
		// First admission binds the report to the owning history's
		// canonical carried findings, not just the round's finding-set
		// digest: same-key rewritten content, omissions and changed
		// origins refuse before any journal/ref effect.
		_, err = c.admitFirstCorrectionReport(ctx, *p, cycle, runID, newCandidate, report, evidence, checks)
	}
	if err == nil {
		return true
	}
	var block *CorrectionBlock
	if errors.As(err, &block) {
		if journalErr := c.journalCorrectionReport(ctx, p, cycle, runID, report); journalErr != nil {
			correctionError(served, p.ID, "store_unavailable")
			return false
		}
		correctionWait(served, p.ID, "delivery_judgment_required")
		return false
	}
	correctionWait(served, p.ID, "correction_refused")
	served.Waits[len(served.Waits)-1].Detail = err.Error()
	return false
}

// journalCorrectionReport records one structurally valid corrector report
// as the publication's next immutable correction journal entry. Refused
// admissions never reach it; admitted and disputed reports do, so the
// D08 routing leg always reads serving-journaled dispositions.
func (c *Coordinator) journalCorrectionReport(ctx context.Context, p *factory.Publication, cycle factory.ReviewCycle, runID string, report factory.CorrectionReport) error {
	if err := appendCorrectionJournal(p, cycle, runID, report); err != nil {
		return err
	}
	p.Revision++
	if err := c.Store.UpdatePublication(ctx, *p); err != nil {
		return err
	}
	return nil
}

// reconcileRecordedCorrection reconciles one in-flight recorded correction
// by lookup before any new write, and links a recovered commit exactly
// like the live path. Anything unattributed fences the publication.
func (c *Coordinator) reconcileRecordedCorrection(ctx context.Context, p *factory.Publication, a factory.Assignment, report *CorrectionReport) {
	if len(p.Corrections) == 0 {
		return
	}
	op := &p.Corrections[len(p.Corrections)-1]
	if op.Attempts == 0 || op.Effect == factory.OpEffectNotCommitted || op.Effect == factory.OpEffectIndeterminate ||
		(op.Effect == factory.OpEffectCommitted && op.Completion == factory.OpCompletionComplete) {
		return
	}
	outcome, err := c.Publication.LookupOp(ctx, op.OperationID)
	if err != nil {
		c.correctionCallError(report, p.ID, err)
		return
	}
	if outcome.NotObserved {
		correctionWait(report, p.ID, "lookup_unconfirmed")
		return
	}
	// The correction row carries the child run independently of the
	// credential-free native intent, so recovery attributes the new head
	// to the same settled result that registered it.
	if !c.adoptCorrectionOutcome(ctx, p, outcome, report) {
		return
	}
	if p.Corrections[len(p.Corrections)-1].Effect == factory.OpEffectCommitted {
		c.linkCorrection(ctx, p, a, report)
	}
}

// adoptCorrectionOutcome adopts one observed correction outcome into the
// latest recorded correction. A commit advances the stored head before
// the store, so the record always satisfies publication validation; an
// unattributed outcome fences the publication instead.
func (c *Coordinator) adoptCorrectionOutcome(ctx context.Context, p *factory.Publication, outcome factory.OperationOutcome, report *CorrectionReport) bool {
	op := &p.Corrections[len(p.Corrections)-1]
	if !c.adoptObserved(op, outcome, time.Now()) {
		c.fenceCorrection(ctx, report, p, factory.PublishReasonUnattributed)
		return false
	}
	if op.Effect == factory.OpEffectCommitted {
		if op.Work == nil || op.RunID == "" {
			c.fenceCorrection(ctx, report, p, factory.PublishReasonUnattributed)
			return false
		}
		p.Candidate = op.Work.Candidate
		p.Run = op.RunID
	}
	return c.storeCorrection(ctx, p, report)
}

// linkCorrection verifies one committed correction receipt against its
// recorded intent and links it. A receipt that does not match fences the
// publication instead of linking a moved head.
func (c *Coordinator) linkCorrection(ctx context.Context, p *factory.Publication, a factory.Assignment, report *CorrectionReport) {
	op := &p.Corrections[len(p.Corrections)-1]
	adopt := op.Work.Apply(factory.PublicationWork{AssignmentID: a.ID, Publication: p.ID})
	if _, err := c.Publication.AdoptBranch(ctx, adopt, operationOutcomeOf(*op)); err != nil {
		c.fenceCorrection(ctx, report, p, factory.PublishReasonUnattributed)
		return
	}
	report.Corrected = append(report.Corrected, CorrectionLink{
		AssignmentID: p.AssignmentID, PublicationID: p.ID,
		HeadOID: p.Candidate, OperationID: op.OperationID, Revision: p.Revision,
	})
}

// cancelRecordedCorrection cancels the just-recorded correction after a
// concurrent withdrawal closed the gate.
func (c *Coordinator) cancelRecordedCorrection(ctx context.Context, p *factory.Publication, report *CorrectionReport) {
	op := &p.Corrections[len(p.Corrections)-1]
	outcome, err := c.Publication.CancelOp(ctx, op.OperationID)
	if err != nil {
		c.correctionCallError(report, p.ID, err)
		return
	}
	if !outcome.NotObserved && !c.adoptObserved(op, outcome, time.Now()) {
		c.fenceCorrection(ctx, report, p, factory.PublishReasonUnattributed)
		return
	}
	if !outcome.NotObserved && op.Effect == factory.OpEffectCommitted {
		if op.Work == nil {
			c.fenceCorrection(ctx, report, p, factory.PublishReasonUnattributed)
			return
		}
		p.Candidate = op.Work.Candidate
		p.Run = op.RunID
		work := op.Work.Apply(factory.PublicationWork{AssignmentID: p.AssignmentID, Publication: p.ID})
		if _, err := c.Publication.AdoptBranch(ctx, work, operationOutcomeOf(*op)); err != nil {
			c.fenceCorrection(ctx, report, p, factory.PublishReasonUnattributed)
			return
		}
	}
	if !c.storeCorrection(ctx, p, report) {
		return
	}
	correctionWait(report, p.ID, "correction_withdrawn")
}

func (c *Coordinator) fenceCorrection(ctx context.Context, report *CorrectionReport, p *factory.Publication, reason string) {
	p.Stage, p.Outcome, p.Reason, p.FinishedUnix = factory.PublicationFenced, factory.NeedsHuman, reason, time.Now().Unix()
	if !c.storeCorrection(ctx, p, report) {
		return
	}
	report.Fenced = append(report.Fenced, p.ID)
}

func (c *Coordinator) storeCorrection(ctx context.Context, p *factory.Publication, report *CorrectionReport) bool {
	p.Revision++
	if err := c.Store.UpdatePublication(ctx, *p); err != nil {
		correctionError(report, p.ID, "store_conflict")
		return false
	}
	return true
}

func (c *Coordinator) correctionCallError(report *CorrectionReport, id string, err error) {
	var wait *factory.PublicationWait
	var refused *factory.PublicationRefusal
	switch {
	case errors.As(err, &wait):
		correctionWait(report, id, wait.Reason)
	case errors.As(err, &refused):
		correctionWait(report, id, refused.Reason)
	default:
		correctionError(report, id, "native_unavailable")
	}
}
