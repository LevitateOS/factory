package service

import (
	"context"
	"errors"
	"strconv"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// journeyChainPageLimit reuses the assignment paging window behind one
// acceptance; journeyChainMaxPages bounds how many windows one issue
// read follows. A cut chain reports chain_page_bound with its resume
// cursor instead of rendering a partial head as the whole lineage.
const (
	journeyChainPageLimit = 256
	journeyChainMaxPages  = 4
)

// assemble renders the one recorded journey subsection for
// the existing issue read from real persisted records only. It binds
// the repository and issue, the current acceptance head, the selected
// owner assignment acceptance, and the actual attempt root (the latest
// row may be a child; the journey never implicitly re-roots). It reads
// only and spends nothing: no judgment, provision or dispatch.
//
// Storage failures return an error for the existing 503 pattern, except
// a missing attempt, which renders explicit available/complete=false.
// Rows that decode but fail their journey DTO name their incomplete
// reason instead of dropping silently; backing that was never recorded
// (no plan, no PR, pending funded calls) leaves its section empty.
func (s WorkJourneys) assemble(ctx context.Context, repository, index int64, issue string, owner *factory.Assignment, publication *factory.Publication, observedUnix int64) (*IssueJourney, error) {
	view := &IssueJourney{
		Phases:        []journeyPhaseView{},
		Tasks:         []journeyTaskView{},
		Commits:       []journeyCommitView{},
		Findings:      []journeyFindingView{},
		Corrections:   []journeyCorrectionView{},
		Judgments:     []journeyJudgmentView{},
		Gates:         []journeyGateView{},
		Adoptions:     []journeyAdoptionView{},
		Delivered:     []journeyDeliveredTaskView{},
		Verifications: []journeyVerifiedAppView{},
		Repository:    strconv.FormatInt(repository, 10), Issue: issue,
		ObservedUnix: observedUnix,
	}
	head, err := s.Store.AcceptanceHead(ctx, repository, index)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		head = ""
	}
	latest, err := s.Store.LatestIssueAttempt(ctx, repository, index)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if owner == nil {
		if err == nil {
			return nil, errors.New("journey attempt lacks its root assignment")
		}
		view.Lineage.Head = head
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonNoAttempt)
		return view, nil
	}
	view.Available = true
	view.Lineage.Head, view.Lineage.Acceptance = head, owner.Acceptance
	view.Lineage.AttemptRoot, view.Lineage.RootAssignment = owner.AttemptRoot, owner.ID
	view.Lineage.Run, view.Lineage.SourceCommit = owner.Run, owner.SourceCommit
	view.Lineage.RunHistory = append([]string(nil), owner.RunHistory...)
	view.Lineage.Stale = head != "" && head != owner.Acceptance
	if err == nil {
		view.Lineage.LatestAssignment, view.Lineage.LatestRun = latest.ID, latest.Run
		if latest.Acceptance != owner.Acceptance {
			view.Reasons = addJourneyReason(view.Reasons, journeyReasonLatestAcceptanceMismatch)
		}
	}
	if publication != nil {
		applyJourneyPublicationLineage(&view.Lineage, publication)
	}
	if err := s.journeyPhaseSection(ctx, view, repository, index, owner); err != nil {
		return nil, err
	}
	if err := s.journeyTaskSection(ctx, view, owner.AttemptRoot, issue); err != nil {
		return nil, err
	}
	applyJourneyPublicationSections(view, publication)
	if err := s.journeyCycleSections(ctx, view, repository, index); err != nil {
		return nil, err
	}
	if err := s.journeyEvidenceSections(ctx, view, repository, index, owner, publication); err != nil {
		return nil, err
	}
	view.Complete = len(view.Reasons) == 0
	return view, nil
}

// journeyEvidenceSections closes the four evidence joins from canonical
// records. Adoptions render from the intent task bindings that adopted
// this native issue (intent + revision with the exact task binding,
// state adopted: a binding is recorded only after native readback
// confirmed the issue). Delivered-task evidence renders the canonical
// completion behind each adoption binding. Verified applications render
// the F10 trusted check behind each adopted revision. Delivery proof
// renders from the publication's journaled delivery acceptances:
// a clean-review acceptance is its own evidence and renders no funded
// judgment; a Jev acceptance renders its funded delivery judgment only
// when the recorded receipt joins on the exact publication, attempt
// root, round and tuple. A section without rendered proof keeps its
// unavailable reason, so the full merged journey never reports
// complete on missing evidence. Delivered and verified evidence is only
// expected once an adoption exists.
func (s WorkJourneys) journeyEvidenceSections(ctx context.Context, view *IssueJourney, repository, index int64, owner *factory.Assignment, publication *factory.Publication) error {
	recorded, err := s.journeyAdoptionSection(ctx, view, repository, index)
	if err != nil {
		return err
	}
	if err := s.journeyDeliveredSection(ctx, view, repository, recorded); err != nil {
		return err
	}
	if err := s.journeyVerifiedSection(ctx, view, repository, recorded); err != nil {
		return err
	}
	return s.journeyDeliverySection(ctx, view, owner, publication)
}

func (s WorkJourneys) journeyAdoptionSection(ctx context.Context, view *IssueJourney, repository, index int64) ([]store.JourneyAdoption, error) {
	recorded, truncated, err := s.Store.JourneyAdoptions(ctx, repository, index, maxJourneyAdoptions)
	if err != nil {
		return nil, err
	}
	adoptions := make([]journeyAdoptionView, 0, len(recorded))
	invalid := false
	for _, adoption := range recorded {
		rendered, err := journeyAdoptionDTO(adoption.Intent, adoption.Revision, journeyAdoptionAdopted,
			adoption.Binding.TaskKey, strconv.FormatInt(adoption.Binding.IssueIndex, 10), adoption.Binding.AcceptanceID)
		if err != nil {
			invalid = true
			continue
		}
		adoptions = append(adoptions, rendered)
	}
	if invalid {
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonAdoptionsInvalidRow)
	}
	if truncated {
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonAdoptionsTruncated)
	}
	if len(adoptions) == 0 {
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonAdoptionsUnavailable)
	}
	view.Adoptions = nonNilJourney(adoptions, []journeyAdoptionView{})
	return recorded, nil
}

// journeyDeliveredSection renders the delivered-task evidence behind
// the recorded adoptions: the canonical completion event for each
// adoption binding's native issue and acceptance. Rows render in
// adoption order; an unchanged task that rebinds a successor revision
// with the same issue and acceptance yields one completion, not one
// row per historical binding: delivered evidence deduplicates by the
// exact task, issue, acceptance and completion identity while the
// adoption section keeps both bindings visible as history. When an
// adoption has no recorded completion the section keeps its
// unavailable reason instead of passing a partial chain as the whole
// lineage. With no adoptions there is no adopted task to deliver, so
// the section stays empty without a reason.
func (s WorkJourneys) journeyDeliveredSection(ctx context.Context, view *IssueJourney, repository int64, recorded []store.JourneyAdoption) error {
	if len(recorded) == 0 {
		return nil
	}
	delivered := make([]journeyDeliveredTaskView, 0, len(recorded))
	seen := make(map[string]bool, len(recorded))
	invalid := false
	missing := false
	for _, adoption := range recorded {
		event, err := s.Store.JourneyDeliveredCompletion(ctx, repository, adoption.Binding.IssueIndex, adoption.Binding.AcceptanceID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				missing = true
				continue
			}
			return err
		}
		rendered, err := journeyDeliveredTaskDTO(adoption.Binding.TaskKey,
			strconv.FormatInt(adoption.Binding.IssueIndex, 10), adoption.Binding.AcceptanceID,
			event.ID, event.PublicationID, event.Generation)
		if err != nil {
			invalid = true
			continue
		}
		key := adoption.Binding.TaskKey + "\x00" + strconv.FormatInt(adoption.Binding.IssueIndex, 10) + "\x00" + adoption.Binding.AcceptanceID + "\x00" + event.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		delivered = append(delivered, rendered)
	}
	if invalid {
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonDeliveriesInvalidRow)
	}
	if missing || len(delivered) == 0 {
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonDeliveriesUnavailable)
	}
	view.Delivered = nonNilJourney(delivered, []journeyDeliveredTaskView{})
	return nil
}

// journeyVerifiedSection renders the verified application revision
// behind each adopted intent revision: the exact revision whose
// trusted application check passed, once per revision in adoption
// order. When an adopted revision has no recorded F10 verification
// the section keeps its unavailable reason instead of reporting the
// chain verified on worker claims. With no adoptions there is no
// adopted revision to verify, so the section stays empty without
// a reason.
func (s WorkJourneys) journeyVerifiedSection(ctx context.Context, view *IssueJourney, repository int64, recorded []store.JourneyAdoption) error {
	if len(recorded) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(recorded))
	verifications := make([]journeyVerifiedAppView, 0, len(recorded))
	invalid := false
	missing := false
	for _, adoption := range recorded {
		if seen[adoption.Revision] {
			continue
		}
		seen[adoption.Revision] = true
		verified, err := s.Store.JourneyVerifiedApp(ctx, repository, adoption.Intent, adoption.Revision)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				missing = true
				continue
			}
			return err
		}
		rendered, err := journeyVerifiedAppDTO(verified.Intent, verified.Revision, verified.SourceCommit, verified.Manifest)
		if err != nil {
			invalid = true
			continue
		}
		verifications = append(verifications, rendered)
	}
	if invalid {
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonVerificationsInvalidRow)
	}
	if missing || len(verifications) == 0 {
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonVerificationsUnavailable)
	}
	view.Verifications = nonNilJourney(verifications, []journeyVerifiedAppView{})
	return nil
}

func (s WorkJourneys) journeyDeliverySection(ctx context.Context, view *IssueJourney, owner *factory.Assignment, publication *factory.Publication) error {
	if publication == nil || len(publication.DeliveryAcceptances) == 0 {
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonDeliveryUnavailable)
		return nil
	}
	var receipts []factory.DeliveryReceipt
	receiptsTruncated := false
	for _, acceptance := range publication.DeliveryAcceptances {
		if acceptance.Kind != factory.AcceptanceJevAcceptance {
			continue
		}
		var err error
		receipts, receiptsTruncated, err = s.Store.DeliveryJudgmentsForPublication(
			ctx, publication.ID, owner.AttemptRoot, maxJourneyJudgments)
		if err != nil {
			return err
		}
		break
	}
	if receiptsTruncated {
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonJudgmentsTruncated)
	}
	linked, unlinked := false, false
	deliveries := make([]journeyJudgmentView, 0, len(publication.DeliveryAcceptances))
	for _, acceptance := range publication.DeliveryAcceptances {
		if acceptance.Validate() != nil || acceptance.PublicationID != publication.ID ||
			acceptance.AttemptRoot != owner.AttemptRoot {
			unlinked = true
			continue
		}
		if acceptance.Kind == factory.AcceptanceCleanReview {
			linked = true
			continue
		}
		rendered, ok := journeyDeliveryJudgment(acceptance, receipts)
		if !ok {
			unlinked = true
			continue
		}
		deliveries = append(deliveries, rendered)
		linked = true
	}
	if len(deliveries) > 0 {
		// Delivery precedes completion reassessment in the loop, so its
		// judgments render before the cycle judgments.
		view.Judgments = append(deliveries, view.Judgments...)
		if len(view.Judgments) > maxJourneyJudgments {
			view.Judgments = view.Judgments[len(view.Judgments)-maxJourneyJudgments:]
			view.Reasons = addJourneyReason(view.Reasons, journeyReasonJudgmentsTruncated)
		}
	}
	if !linked || unlinked {
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonDeliveryUnavailable)
	}
	return nil
}

// journeyDeliveryJudgment renders the funded delivery judgment behind
// one Jev delivery acceptance: the unique decided receipt over the
// acceptance's exact round and tuple whose effective action is ready.
// A missing, undecided or mismatched receipt renders nothing, never a
// fabricated judgment from the acceptance alone.
func journeyDeliveryJudgment(acceptance factory.DeliveryAcceptance, receipts []factory.DeliveryReceipt) (journeyJudgmentView, bool) {
	for _, receipt := range receipts {
		if receipt.Outcome != factory.DeliveryOutcomeDecided ||
			receipt.Decision.Effective != factory.DeliveryEffectiveReady ||
			receipt.Input.Round != acceptance.Round ||
			receipt.Input.Head != acceptance.Tuple.Head ||
			receipt.Input.Base != acceptance.Tuple.Base {
			continue
		}
		answer := receipt.Decision.Answer
		rendered, err := journeyJudgmentDTO(journeyJudgmentDelivery, answer.Selected, answer.Model,
			answer.Confidence, answer.Probability, false, "", nil)
		if err != nil {
			return journeyJudgmentView{}, false
		}
		return rendered, true
	}
	return journeyJudgmentView{}, false
}

// applyJourneyPublicationLineage binds the recorded candidate, the
// recorded historical head/base tuple and the native operation refs.
// Recorded bases render as history; the read never re-resolves them.
func applyJourneyPublicationLineage(lineage *journeyLineageView, publication *factory.Publication) {
	lineage.Candidate = publication.Candidate
	lineage.PublishOp, lineage.PRCreateOp = publication.Publish.OperationID, publication.PRCreate.OperationID
	if publication.PRNumber > 0 {
		lineage.PRNumber = strconv.FormatInt(publication.PRNumber, 10)
	}
	if publication.PRID > 0 {
		lineage.PRID = strconv.FormatInt(publication.PRID, 10)
	}
	if publication.Tuple != nil {
		lineage.TupleHead, lineage.TupleBase, lineage.TupleDiffBase = publication.Tuple.Head, publication.Tuple.Base, publication.Tuple.DiffBase
	}
}

// journeyPhaseSection renders the CURRENT state of the selected owner
// attempt lineage: exactly one state per canonical phase, each bound to
// the newest recorded assignment carrying that phase under the
// selected AttemptRoot, in stable canonical order. A newer retry storm
// on one phase cannot displace another phase, a sibling AttemptRoot
// sharing the acceptance cannot leak in, and no old capped prefix ever
// stands in for current state. Unknown or empty phases and ref-less
// contributing rows name phases_invalid_row instead of dropping
// silently and never fabricate a phase. A cut group read names
// phases_truncated. Unreadable storage returns its error for the
// existing 503 pattern.
func (s WorkJourneys) journeyPhaseSection(ctx context.Context, view *IssueJourney, repository, index int64, owner *factory.Assignment) error {
	trail, next, pageBound, err := s.journeyChain(ctx, repository, index, owner.Acceptance)
	if err != nil {
		return err
	}
	view.Lineage.ChainCursors = trail
	if pageBound {
		view.Lineage.NextCursor = next
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonChainPageBound)
	}
	groups, truncated, err := s.Store.LatestPhaseAttempts(ctx, repository, index, owner.Acceptance, owner.AttemptRoot)
	if err != nil {
		return err
	}
	if truncated {
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonPhasesTruncated)
	}
	current := make(map[string]factory.Assignment, len(journeyCanonicalPhases))
	for _, group := range groups {
		assignment := group.Assignment
		if (assignment.RetainedPublication != nil || assignment.ExternalPublication != nil) && assignment.Validate() == nil {
			continue // This publication command made no execution phase or run.
		}
		if !project.ValidExecutionPhase(assignment.Phase) {
			view.Reasons = addJourneyReason(view.Reasons, journeyReasonPhasesInvalidRow)
			continue
		}
		current[assignment.Phase] = assignment
	}
	phases := make([]journeyPhaseView, 0, len(journeyCanonicalPhases))
	for _, phase := range journeyCanonicalPhases {
		assignment, ok := current[phase]
		if !ok {
			continue
		}
		rendered, err := journeyPhaseEntryDTO(phase, journeyAssignmentState(assignment),
			assignment.ID, assignment.Run, assignment.AttemptRoot)
		if err != nil {
			view.Reasons = addJourneyReason(view.Reasons, journeyReasonPhasesInvalidRow)
			continue
		}
		phases = append(phases, rendered)
	}
	view.Phases = nonNilJourney(phases, []journeyPhaseView{})
	return nil
}

// journeyChain pages one acceptance lineage oldest-first through the
// stable seq keyset until the window stops advancing. It returns the
// page cursor trail, the resume cursor and whether the page bound cut
// the read. The trail is retained lineage history behind the rendered
// window; the phase section itself always reads current-per-phase, so
// a cut here never passes old head rows as current.
func (s WorkJourneys) journeyChain(ctx context.Context, repository, index int64, acceptance string) ([]string, string, bool, error) {
	var trail []string
	after := ""
	for page := 0; ; page++ {
		if page == journeyChainMaxPages {
			return trail, after, true, nil
		}
		got, next, err := s.Store.AcceptanceAssignmentsAfter(ctx, repository, index, acceptance, after, journeyChainPageLimit)
		if err != nil {
			return nil, "", false, err
		}
		// The trail holds every advanced cursor, so a cut names the
		// newest cursor reached as its resume ref.
		advanced := next != "" && next != after
		if advanced {
			after = next
			trail = append(trail, after)
		}
		if len(got) < journeyChainPageLimit || !advanced {
			return trail, after, false, nil
		}
	}
}

// journeyTaskSection renders the admitted plan's tasks with their
// receipt states. A missing head, revision or receipt set means no
// plan was recorded and leaves the section empty; malformed task rows
// name their reason. Only the tail window renders past the cap.
func (s WorkJourneys) journeyTaskSection(ctx context.Context, view *IssueJourney, attemptRoot, issue string) error {
	headID, _, err := s.Store.PlanHead(ctx, attemptRoot)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	plan, err := s.Store.InvestigationPlan(ctx, attemptRoot, headID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}
	receipts, err := s.Store.PlanTaskReceipts(ctx, attemptRoot, headID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			receipts = nil
		} else {
			return err
		}
	}
	byTask := make(map[string]store.PlanTaskReceipt, len(receipts))
	for _, receipt := range receipts {
		byTask[receipt.TaskID] = receipt
	}
	tasks := make([]journeyTaskView, 0, len(plan.Tasks))
	invalid := false
	for _, task := range plan.Tasks {
		state := journeyPending
		if receipt, ok := byTask[task.ID]; ok {
			switch receipt.State {
			case store.PlanTaskCompleted:
				state = journeyComplete
			case store.PlanTaskFailed, store.PlanTaskCancelled:
				state = journeyAttention
			case store.PlanTaskRunning, store.PlanTaskOutputReady:
				state = journeyActive
			}
		}
		lane := task.Lane
		if lane != journeyLaneA && lane != journeyLaneB && lane != journeyLaneC {
			lane = journeyLaneUnassigned
		}
		rendered, err := journeyTaskDTO(task.ID, lane, issue, state)
		if err != nil {
			invalid = true
			continue
		}
		tasks = append(tasks, rendered)
	}
	if invalid {
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonTasksInvalidRow)
	}
	if len(tasks) > maxJourneyTasks {
		tasks = tasks[len(tasks)-maxJourneyTasks:]
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonTasksTruncated)
	}
	view.Tasks = nonNilJourney(tasks, []journeyTaskView{})
	return nil
}

// applyJourneyPublicationSections renders the publication candidate,
// its correction operations and its journaled correction findings from
// the passed record. The candidate commit binds with the RECORDED
// creation base; the read never calls that base live-fresh. A linked
// PR that fails its DTO is malformed history, while no linked PR yet
// means nothing was recorded. Unsubmitted operations carry no
// observations and stay out.
func applyJourneyPublicationSections(view *IssueJourney, publication *factory.Publication) {
	if publication == nil {
		return
	}
	if publication.PRNumber > 0 {
		commit, err := journeyCommitDTO(publication.Candidate, publication.PullTarget().BaseOID,
			strconv.FormatInt(publication.PRNumber, 10), strconv.FormatInt(publication.PRID, 10))
		if err != nil {
			view.Reasons = addJourneyReason(view.Reasons, journeyReasonCommitsInvalidRow)
		} else {
			view.Commits = []journeyCommitView{commit}
		}
	}
	corrections := make([]journeyCorrectionView, 0, len(publication.Corrections))
	invalidCorrection := false
	for _, op := range publication.Corrections {
		if op.Work == nil && op.Effect == "" && op.Completion == "" && op.Reason == "" {
			continue
		}
		candidate := ""
		if op.Work != nil {
			candidate = op.Work.Candidate
		}
		correction, err := journeyCorrectionDTO(candidate, string(op.Effect), string(op.Completion), op.Reason)
		if err != nil {
			invalidCorrection = true
			continue
		}
		corrections = append(corrections, correction)
	}
	if invalidCorrection {
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonCorrectionsInvalidRow)
	}
	if len(corrections) > maxJourneyCorrections {
		corrections = corrections[len(corrections)-maxJourneyCorrections:]
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonCorrectionsTruncated)
	}
	view.Corrections = nonNilJourney(corrections, []journeyCorrectionView{})
	findings := make([]journeyFindingView, 0)
	invalidFinding := false
	for _, journal := range publication.CorrectionJournals {
		for _, finding := range journal.Report.Findings {
			rendered, err := journeyFindingDTO(finding)
			if err != nil {
				invalidFinding = true
				continue
			}
			findings = append(findings, rendered)
		}
	}
	if invalidFinding {
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonFindingsInvalidRow)
	}
	if len(findings) > maxJourneyFindings {
		findings = findings[len(findings)-maxJourneyFindings:]
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonFindingsTruncated)
	}
	view.Findings = nonNilJourney(findings, []journeyFindingView{})
}

// journeyCycleSections renders funded reassessment decisions and D13
// gate cycles from one bounded per-issue read, so every section shares
// the same lineage. Cycles awaiting their funded receipt hold without
// a judgment; malformed rows and cap cuts name their reasons.
func (s WorkJourneys) journeyCycleSections(ctx context.Context, view *IssueJourney, repository, index int64) error {
	cycles, truncated, err := s.Store.JourneyReassignmentCycles(ctx, repository, index, maxJourneyJudgments)
	if err != nil {
		return err
	}
	judgments := make([]journeyJudgmentView, 0, len(cycles))
	invalidJudgment := false
	for _, record := range cycles {
		if record.EvidenceFingerprint == "" {
			continue
		}
		fingerprint, err := control.DecisionFingerprintFromRecord(record)
		if err != nil {
			invalidJudgment = true
			continue
		}
		receipt, err := s.Store.PriorityJudgment(ctx, fingerprint)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return err
		}
		if len(receipt.Calls) != 1 {
			continue
		}
		answer := receipt.Calls[0].Answer
		probability, ok := answer.Probabilities[answer.Selected]
		if !ok {
			invalidJudgment = true
			continue
		}
		wait := record.Cycle.State == factory.ReassessmentAttention
		rendered, err := journeyJudgmentDTO(journeyJudgmentReassessDecision, answer.Selected, answer.Model,
			answer.Confidence, probability, wait, record.WaitReason, &record.Cycle)
		if err != nil {
			invalidJudgment = true
			continue
		}
		judgments = append(judgments, rendered)
	}
	if invalidJudgment {
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonJudgmentsInvalidRow)
	}
	if truncated || len(judgments) > maxJourneyJudgments {
		if len(judgments) > maxJourneyJudgments {
			judgments = judgments[len(judgments)-maxJourneyJudgments:]
		}
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonJudgmentsTruncated)
	}
	view.Judgments = nonNilJourney(judgments, []journeyJudgmentView{})
	gates := make([]journeyGateView, 0, len(cycles))
	unrenderable := false
	for _, record := range cycles {
		rendered, err := journeyGateDTO(factory.ReassessmentView{
			CompletionID: record.Completion.ID, CycleID: record.Cycle.CycleID,
			State: record.Cycle.State, Decision: record.Cycle.Decision, Proposal: record.Cycle.Proposal,
			Repository: repository, Generation: record.Cycle.Generation, WaitReason: record.WaitReason,
			InventoryDigest: record.InventoryDigest, Retryable: record.Cycle.State == factory.ReassessmentAttention,
		})
		if err != nil {
			unrenderable = true
			continue
		}
		gates = append(gates, rendered)
	}
	if unrenderable {
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonGatesUnrenderable)
	}
	if truncated || len(gates) > maxJourneyGates {
		if len(gates) > maxJourneyGates {
			gates = gates[len(gates)-maxJourneyGates:]
		}
		view.Reasons = addJourneyReason(view.Reasons, journeyReasonGatesTruncated)
	}
	view.Gates = nonNilJourney(gates, []journeyGateView{})
	return nil
}
