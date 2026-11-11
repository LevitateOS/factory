package service

import (
	"errors"
	"strconv"
	"strings"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// Recorded issue-work journey read model. It renders one issue's recorded work
// journey for UI/API consumers: execution phases, tasks with their lanes,
// commit/PR evidence, structured findings with corrections, Jev judgments
// with effective waits, D13 completion gates with proposals, adoption with
// its task binding, delivered-task evidence and the verified application
// revision. Every section carries IDs, commits, digests and bounded codes
// only; native prose and evidence bytes stay out.
//
// These builders validate recorded input against the FW06A, FW06C
// and FW30 contracts but read no store and spend nothing: absence of a
// section means nothing was recorded, never a guessed state. The
// persisted readback binds this same shape; fixture UI must never be
// read as the journey.

// Journey section caps. Totals are exact; only recorded entries render.
const (
	maxJourneyPhases      = 8
	maxJourneyTasks       = 32
	maxJourneyCommits     = 16
	maxJourneyFindings    = 64
	maxJourneyCorrections = 20
	maxJourneyJudgments   = 8
	maxJourneyGates       = 8
	maxJourneyAdoptions   = 16
	maxJourneyWaitChars   = 256
)

// journeyStates name one phase or task's recorded position. Pending work
// has no evidence yet; attention needs a recorded decision.
const (
	journeyPending   = "pending"
	journeyActive    = "active"
	journeyComplete  = "complete"
	journeyAttention = "attention"
)

// journeyLanes name the parallel native work lane behind a task.
// Unassigned tasks have no lane yet.
const (
	journeyLaneA          = "a"
	journeyLaneB          = "b"
	journeyLaneC          = "c"
	journeyLaneUnassigned = "unassigned"
)

// journeyCanonicalPhases orders the admitted execution phases for the
// recorded journey: decompose, investigate, implement, review, correct,
// reassess, repair, verify. The journey renders one current state per
// phase in this stable order, never in row-recency order, so a newer
// retry storm on one phase cannot displace another phase's current state.
var journeyCanonicalPhases = []string{
	project.ExecutionDecompose, project.ExecutionInvestigate, project.ExecutionImplement,
	project.ExecutionReview, project.ExecutionCorrect, project.ExecutionReassess,
	project.ExecutionRepair, project.ExecutionVerify,
}

// journeyPhaseView renders one execution phase's recorded CURRENT
// state: the frozen phase vocabulary with its position plus the exact
// contributing assignment, run and AttemptRoot behind that state, so
// the read names whose recorded work produced the current state instead of
// leaving the phase anonymous. Payload bytes stay out.
type journeyPhaseView struct {
	Phase       string `json:"phase"`
	State       string `json:"state"`
	Assignment  string `json:"assignment,omitempty"`
	Run         string `json:"run,omitempty"`
	AttemptRoot string `json:"attempt_root,omitempty"`
}

// journeyPhaseDTO validates one fixture phase against the frozen
// execution vocabulary.
func journeyPhaseDTO(phase, state string) (journeyPhaseView, error) {
	if !project.ValidExecutionPhase(phase) {
		return journeyPhaseView{}, errors.New("journey phase is not an admitted execution phase")
	}
	if !validJourneyState(state) {
		return journeyPhaseView{}, errors.New("journey phase lacks its recorded state")
	}
	return journeyPhaseView{Phase: phase, State: state}, nil
}

// journeyPhaseEntryDTO validates one recorded current-phase entry: an
// admitted canonical phase with its recorded state and the exact
// contributing assignment, run and AttemptRoot refs. A phase without
// its refs is malformed history, never an anonymous current state.
func journeyPhaseEntryDTO(phase, state, assignment, run, root string) (journeyPhaseView, error) {
	view, err := journeyPhaseDTO(phase, state)
	if err != nil {
		return journeyPhaseView{}, err
	}
	if assignment == "" || run == "" || root == "" {
		return journeyPhaseView{}, errors.New("journey phase lacks its contributing refs")
	}
	view.Assignment, view.Run, view.AttemptRoot = assignment, run, root
	return view, nil
}

// journeyAssignmentState reduces one recorded assignment to its
// journey position: attention for finished work that failed, was
// cancelled, or needs a human; complete ONLY for genuinely
// succeeded finished work; active otherwise. An unknown or invalid
// recorded outcome must never fabricate successful completion.
func journeyAssignmentState(assignment factory.Assignment) string {
	switch {
	case assignment.Finished() &&
		(assignment.Outcome == factory.Failed || assignment.Outcome == factory.Cancelled ||
			assignment.Outcome == factory.NeedsHuman ||
			assignment.Reason == factory.AssignReasonRunFailed || assignment.Reason == factory.AssignReasonCancelled):
		return journeyAttention
	case assignment.Succeeded():
		return journeyComplete
	case assignment.Finished():
		return journeyAttention
	default:
		return journeyActive
	}
}

func validJourneyState(state string) bool {
	switch state {
	case journeyPending, journeyActive, journeyComplete, journeyAttention:
		return true
	default:
		return false
	}
}

func validJourneyLane(lane string) bool {
	switch lane {
	case journeyLaneA, journeyLaneB, journeyLaneC, journeyLaneUnassigned:
		return true
	default:
		return false
	}
}

// validJourneyTaskKey enforces the bounded stable task identity shared
// by planned tasks, adoption bindings and delivered-task evidence: a
// bounded trimmed key with no control characters.
func validJourneyTaskKey(key string) error {
	if len(key) == 0 || len(key) > 128 || strings.TrimSpace(key) != key {
		return errors.New("journey task lacks its bounded key")
	}
	if strings.ContainsFunc(key, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		return errors.New("journey task key carries control characters")
	}
	return nil
}

// journeyTaskView renders one planned task with its lane binding and
// recorded state. The key is the stable task identity; bodies come from
// the accepted revision, never a speculative graph.
type journeyTaskView struct {
	Key   string `json:"key"`
	Issue string `json:"issue,omitempty"`
	Lane  string `json:"lane"`
	State string `json:"state"`
}

// journeyTaskDTO validates one fixture task: a bounded key, a known
// lane and a recorded state, with an optional decimal native issue.
func journeyTaskDTO(key, lane, issue, state string) (journeyTaskView, error) {
	if err := validJourneyTaskKey(key); err != nil {
		return journeyTaskView{}, err
	}
	if !validJourneyLane(lane) {
		return journeyTaskView{}, errors.New("journey task lacks its recorded lane")
	}
	if !validJourneyState(state) {
		return journeyTaskView{}, errors.New("journey task lacks its recorded state")
	}
	if issue != "" && !validJourneyDecimal(issue) {
		return journeyTaskView{}, errors.New("journey task names an invalid native issue")
	}
	return journeyTaskView{Key: key, Issue: issue, Lane: lane, State: state}, nil
}

func validJourneyDecimal(value string) bool {
	parsed, err := strconv.ParseInt(value, 10, 64)
	return err == nil && parsed > 0 && strconv.FormatInt(parsed, 10) == value
}

// journeyCommitView renders one commit/PR evidence link: the exact head
// and base with the native pull request behind them.
type journeyCommitView struct {
	Commit   string `json:"commit"`
	Base     string `json:"base"`
	PRNumber string `json:"pr_number"`
	PRID     string `json:"pr_id"`
}

// journeyCommitDTO validates one fixture commit reference: exact fresh
// commits with decimal native pull linkage.
func journeyCommitDTO(commit, base, prNumber, prID string) (journeyCommitView, error) {
	if !factory.ValidCommit(commit) || !factory.ValidCommit(base) || commit == base {
		return journeyCommitView{}, errors.New("journey commit is not a fresh exact commit")
	}
	if !validJourneyDecimal(prNumber) || !validJourneyDecimal(prID) {
		return journeyCommitView{}, errors.New("journey commit lacks its native pull linkage")
	}
	return journeyCommitView{Commit: commit, Base: base, PRNumber: prNumber, PRID: prID}, nil
}

// journeyFindingView renders one structured finding: its stable key,
// severity, fix kind and bounded defect prose. Evidence bytes stay on
// the native thread; the key correlates them.
type journeyFindingView struct {
	Key      string `json:"key"`
	Severity string `json:"severity"`
	Defect   string `json:"defect"`
	FixKind  string `json:"fix_kind"`
}

// journeyFindingDTO validates one fixture finding against the frozen
// FW06C finding contract and renders its UI facts.
func journeyFindingDTO(finding factory.Finding) (journeyFindingView, error) {
	if err := finding.Validate(); err != nil {
		return journeyFindingView{}, err
	}
	return journeyFindingView{
		Key: finding.Key, Severity: finding.Severity,
		Defect: finding.Defect, FixKind: finding.FixKind,
	}, nil
}

// journeyCorrectionView renders one native branch correction: the
// candidate it published and its operation effect. Receipt bytes stay
// out; the candidate commit identifies the corrected content.
type journeyCorrectionView struct {
	Candidate  string `json:"candidate,omitempty"`
	Effect     string `json:"effect,omitempty"`
	Completion string `json:"completion,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// journeyCorrectionDTO validates one fixture correction: an optional
// exact candidate with bounded operation effect and completion codes.
func journeyCorrectionDTO(candidate, effect, completion, reason string) (journeyCorrectionView, error) {
	if candidate != "" && !factory.ValidCommit(candidate) {
		return journeyCorrectionView{}, errors.New("journey correction names an invalid candidate")
	}
	if effect != "" && !factory.ValidOpEffect(collaboration.EffectState(effect)) {
		return journeyCorrectionView{}, errors.New("journey correction carries an unknown effect")
	}
	if completion != "" && !factory.ValidOpCompletion(collaboration.CompletionState(completion)) {
		return journeyCorrectionView{}, errors.New("journey correction carries an unknown completion")
	}
	if reason != "" && (len(reason) > maxJourneyWaitChars || strings.TrimSpace(reason) == "") {
		return journeyCorrectionView{}, errors.New("journey correction carries an invalid reason")
	}
	return journeyCorrectionView{
		Candidate: candidate, Effect: effect, Completion: completion, Reason: reason,
	}, nil
}

// journeyJudgmentKinds name the Jev judgments the journey renders:
// next-work priority, delivery acceptance, and the two D13 funded
// calls (gate decision and proposal assessment).
const (
	journeyJudgmentPriority         = "priority"
	journeyJudgmentDelivery         = "delivery"
	journeyJudgmentReassessDecision = "reassessment-decision"
	journeyJudgmentReassessProposal = "reassessment-proposal"
	journeyJudgmentDeliveryReview   = "review_again"
	journeyJudgmentDeliveryReady    = "ready_to_merge"
)

// journeyJudgmentView renders one Jev judgment with its effective wait:
// the recorded choice, its uncertainty, and the bounded reason while it
// waits. A non-waiting judgment carries no reason, never a guess.
type journeyJudgmentView struct {
	CycleID     string  `json:"cycle_id,omitempty"`
	CycleState  string  `json:"cycle_state,omitempty"`
	Selected    string  `json:"selected"`
	Model       string  `json:"model,omitempty"`
	WaitReason  string  `json:"wait_reason,omitempty"`
	Kind        string  `json:"kind"`
	Confidence  float64 `json:"confidence,omitempty"`
	Probability float64 `json:"probability,omitempty"`
	Wait        bool    `json:"wait"`
}

// journeyJudgmentDTO validates one fixture judgment: a known kind, its
// frozen choice vocabulary and actionable thresholds, and a required bounded
// reason while it waits. Held judgments may carry scores below those thresholds.
func journeyJudgmentDTO(kind, selected, model string, confidence, probability float64, wait bool, waitReason string, cycle *factory.ReassessmentCycle) (journeyJudgmentView, error) {
	if err := validJourneyJudgmentChoice(kind, selected); err != nil {
		return journeyJudgmentView{}, err
	}
	if model != "" && (len(model) > 128 || strings.ContainsFunc(model, func(r rune) bool { return r < ' ' || r == 0x7f })) {
		return journeyJudgmentView{}, errors.New("journey judgment carries an invalid model")
	}
	closed := cycle != nil && (cycle.State == factory.ReassessmentSuperseded || cycle.State == factory.ReassessmentReleased)
	if err := validJourneyScores(kind, confidence, probability, wait || closed); err != nil {
		return journeyJudgmentView{}, err
	}
	if wait && (len(waitReason) == 0 || len(waitReason) > maxJourneyWaitChars || strings.TrimSpace(waitReason) == "") {
		return journeyJudgmentView{}, errors.New("journey wait needs its bounded reason")
	}
	if !wait && waitReason != "" {
		return journeyJudgmentView{}, errors.New("journey judgment without a wait carries no reason")
	}
	view := journeyJudgmentView{
		Selected: selected, Model: model,
		WaitReason:  waitReason,
		Kind:        kind,
		Confidence:  confidence,
		Probability: probability,
		Wait:        wait,
	}
	if cycle != nil {
		if err := cycle.Validate(); err != nil || (kind != journeyJudgmentReassessDecision && kind != journeyJudgmentReassessProposal) || (closed && wait) {
			return journeyJudgmentView{}, errors.New("journey judgment has invalid cycle context")
		}
		view.CycleID, view.CycleState = cycle.CycleID, cycle.State
	}
	return view, nil
}

func validJourneyJudgmentChoice(kind, selected string) error {
	switch kind {
	case journeyJudgmentPriority:
		if len(selected) == 0 || len(selected) > 128 {
			return errors.New("priority judgment lacks its selected candidate")
		}
		return nil
	case journeyJudgmentDelivery:
		switch selected {
		case journeyJudgmentDeliveryReview, journeyJudgmentDeliveryReady:
			return nil
		default:
			return errors.New("delivery judgment lacks its frozen choice")
		}
	case journeyJudgmentReassessDecision:
		switch selected {
		case factory.ReassessmentOptionReassess, factory.ReassessmentOptionContinue:
			return nil
		default:
			return errors.New("reassessment decision lacks its frozen choice")
		}
	case journeyJudgmentReassessProposal:
		switch selected {
		case factory.ReassessmentOptionAccept, factory.ReassessmentOptionNeedsAttention:
			return nil
		default:
			return errors.New("reassessment proposal lacks its frozen choice")
		}
	default:
		return errors.New("journey judgment names an unknown kind")
	}
}

func validJourneyScores(kind string, confidence, probability float64, wait bool) error {
	for _, score := range []float64{confidence, probability} {
		if score < 0 || score > 1 {
			return errors.New("journey judgment score is outside its distribution")
		}
	}
	if wait {
		return nil
	}
	switch kind {
	case journeyJudgmentReassessDecision:
		if confidence < factory.MinReassessmentDecisionConfidence {
			return errors.New("reassessment decision is below its confidence threshold")
		}
	case journeyJudgmentReassessProposal:
		if probability < factory.MinReassessmentProposalProbability || confidence < factory.MinReassessmentProposalConfidence {
			return errors.New("reassessment proposal is below its funded thresholds")
		}
	}
	return nil
}

// journeyGateView renders one D13 completion gate: the frozen
// ReassessmentView facts with the gate decision, assessment progress,
// exact proposal effects and the wait/retry reason. Digests travel,
// evidence bytes do not.
type journeyGateView = factory.ReassessmentView

// journeyGateDTO validates one fixture completion gate against the
// frozen FW30 view contract.
func journeyGateDTO(view factory.ReassessmentView) (journeyGateView, error) {
	if err := view.Validate(); err != nil {
		return journeyGateView{}, err
	}
	return view, nil
}

// journeyAdoptionStates name one D13 proposal's adoption position:
// proposed by the reassess run, adopted into a per-intent revision, or
// released by the follow-up decision.
const (
	journeyAdoptionProposed = "proposed"
	journeyAdoptionAdopted  = "adopted"
	journeyAdoptionReleased = "released"
)

// journeyAdoptionView renders one D13 proposal adoption: the intent and
// revision it binds with its recorded position, plus the exact task
// binding that adopted the native issue (task key, native issue and the
// acceptance behind it). The binding is the adoption proof: it exists
// only after native readback confirmed the issue. Adoption never mutates
// source or publication effects; it only names the bound revision.
type journeyAdoptionView struct {
	Intent     string `json:"intent"`
	Revision   string `json:"revision"`
	State      string `json:"state"`
	Task       string `json:"task"`
	IssueID    string `json:"issue_id"`
	Acceptance string `json:"acceptance"`
}

// journeyAdoptionDTO validates one fixture adoption: exact intent and
// revision identities with a recorded position and the exact task
// binding behind the adoption.
func journeyAdoptionDTO(intent, revision, state, task, issueID, acceptance string) (journeyAdoptionView, error) {
	if !project.ValidDecisionID(intent) || !project.ValidDecisionID(revision) {
		return journeyAdoptionView{}, errors.New("journey adoption lacks its exact intent or revision")
	}
	switch state {
	case journeyAdoptionProposed, journeyAdoptionAdopted, journeyAdoptionReleased:
	default:
		return journeyAdoptionView{}, errors.New("journey adoption lacks its recorded position")
	}
	if err := validJourneyTaskKey(task); err != nil {
		return journeyAdoptionView{}, err
	}
	if !validJourneyDecimal(issueID) {
		return journeyAdoptionView{}, errors.New("journey adoption names an invalid native issue")
	}
	if !project.ValidDecisionID(acceptance) {
		return journeyAdoptionView{}, errors.New("journey adoption lacks its exact acceptance")
	}
	return journeyAdoptionView{
		Intent: intent, Revision: revision, State: state,
		Task: task, IssueID: issueID, Acceptance: acceptance,
	}, nil
}

// journeyDeliveredTaskView renders the delivered-task evidence behind
// one adoption binding: the bound task and acceptance with the exact
// canonical completion event (identity, publication and generation)
// that delivered it. Evidence identities travel; evidence bytes do not.
type journeyDeliveredTaskView struct {
	Task        string `json:"task"`
	Issue       string `json:"issue"`
	Acceptance  string `json:"acceptance"`
	Completion  string `json:"completion"`
	Publication string `json:"publication"`
	Generation  int64  `json:"generation"`
}

// journeyDeliveredTaskDTO validates one delivered-task evidence row:
// the bound task, its native issue and acceptance, and the exact
// canonical completion behind them.
func journeyDeliveredTaskDTO(task, issue, acceptance, completion, publication string, generation int64) (journeyDeliveredTaskView, error) {
	if err := validJourneyTaskKey(task); err != nil {
		return journeyDeliveredTaskView{}, err
	}
	if !validJourneyDecimal(issue) {
		return journeyDeliveredTaskView{}, errors.New("journey delivery names an invalid native issue")
	}
	if !project.ValidDecisionID(acceptance) {
		return journeyDeliveredTaskView{}, errors.New("journey delivery lacks its exact acceptance")
	}
	if !factory.ValidID(completion) || !factory.ValidID(publication) {
		return journeyDeliveredTaskView{}, errors.New("journey delivery lacks its exact completion evidence")
	}
	if generation < 1 {
		return journeyDeliveredTaskView{}, errors.New("journey delivery lacks its recorded generation")
	}
	return journeyDeliveredTaskView{
		Task: task, Issue: issue, Acceptance: acceptance,
		Completion: completion, Publication: publication, Generation: generation,
	}, nil
}

// journeyVerifiedAppView renders the verified application revision
// behind one adopted intent revision: the exact revision whose trusted
// application check passed, with the recorded source commit and the
// verification manifest digest. Only a recorded F10 verification
// renders here; worker claims never do.
type journeyVerifiedAppView struct {
	Intent       string `json:"intent"`
	Revision     string `json:"revision"`
	SourceCommit string `json:"source_commit"`
	Manifest     string `json:"manifest"`
}

// journeyVerifiedAppDTO validates one verified-application row: exact
// intent and revision identities with the recorded source commit and
// manifest digest behind the trusted check.
func journeyVerifiedAppDTO(intent, revision, sourceCommit, manifest string) (journeyVerifiedAppView, error) {
	if !project.ValidDecisionID(intent) || !project.ValidDecisionID(revision) {
		return journeyVerifiedAppView{}, errors.New("journey verification lacks its exact intent or revision")
	}
	if !factory.ValidCommit(sourceCommit) {
		return journeyVerifiedAppView{}, errors.New("journey verification lacks its exact source commit")
	}
	if !factory.ValidDigest(manifest) {
		return journeyVerifiedAppView{}, errors.New("journey verification lacks its exact manifest")
	}
	return journeyVerifiedAppView{
		Intent: intent, Revision: revision, SourceCommit: sourceCommit, Manifest: manifest,
	}, nil
}

func nonNilJourney[T any](got, fallback []T) []T {
	if got == nil {
		return fallback
	}
	return got
}

// Journey reason codes name every incomplete or unavailable fact behind
// one recorded issue journey. Codes carry no raw data and stay bounded:
// absence of a code means its section rendered whole, never a guess.
// Adoption, delivered-task, verification and delivery sections read
// unavailable until their recorded linkage renders (intent task
// bindings; canonical completions behind those bindings; F10 verified
// applications behind the adopted revisions; delivery acceptances with
// their funded receipts); without that proof the full merged journey
// never reports complete here. Delivered and verified evidence is only
// expected once an adoption exists: an issue nothing adopted has no
// downstream chain to miss.
const (
	journeyReasonNoAttempt                = "no_attempt"
	journeyReasonChainPageBound           = "chain_page_bound"
	journeyReasonLatestAcceptanceMismatch = "latest_acceptance_mismatch"
	journeyReasonPhasesInvalidRow         = "phases_invalid_row"
	journeyReasonPhasesTruncated          = "phases_truncated"
	journeyReasonTasksInvalidRow          = "tasks_invalid_row"
	journeyReasonTasksTruncated           = "tasks_truncated"
	journeyReasonCommitsInvalidRow        = "commits_invalid_row"
	journeyReasonCorrectionsInvalidRow    = "corrections_invalid_row"
	journeyReasonCorrectionsTruncated     = "corrections_truncated"
	journeyReasonFindingsInvalidRow       = "findings_invalid_row"
	journeyReasonFindingsTruncated        = "findings_truncated"
	journeyReasonJudgmentsInvalidRow      = "judgments_invalid_row"
	journeyReasonJudgmentsTruncated       = "judgments_truncated"
	journeyReasonGatesUnrenderable        = "gates_unrenderable"
	journeyReasonGatesTruncated           = "gates_truncated"
	journeyReasonAdoptionsUnavailable     = "adoptions_unavailable"
	journeyReasonAdoptionsInvalidRow      = "adoptions_invalid_row"
	journeyReasonAdoptionsTruncated       = "adoptions_truncated"
	journeyReasonDeliveriesUnavailable    = "deliveries_unavailable"
	journeyReasonDeliveriesInvalidRow     = "deliveries_invalid_row"
	journeyReasonVerificationsUnavailable = "verifications_unavailable"
	journeyReasonVerificationsInvalidRow  = "verifications_invalid_row"
	journeyReasonDeliveryUnavailable      = "delivery_unavailable"
)

// maxJourneyReasons caps the reported reason set; codes are a fixed
// vocabulary so ordinary journeys carry at most a few.
const maxJourneyReasons = 16

// journeyLineageView binds one recorded journey to its exact persisted
// scope: the current acceptance head against the selected assignment
// acceptance (stale when the head moved on), the actual attempt root
// with its root and latest assignments and runs (the latest may be a
// child; the journey never implicitly re-roots), the recorded source
// and candidate with the recorded historical head/base tuple, and the
// native operation refs behind them. Recorded bases are history, never
// live-fresh reads. NextCursor resumes a bound cut; ChainCursors trails
// the stable keyset pages behind the rendered window.
type journeyLineageView struct {
	Head             string   `json:"head_acceptance,omitempty"`
	Acceptance       string   `json:"acceptance,omitempty"`
	AttemptRoot      string   `json:"attempt_root,omitempty"`
	RootAssignment   string   `json:"root_assignment,omitempty"`
	LatestAssignment string   `json:"latest_assignment,omitempty"`
	Run              string   `json:"run,omitempty"`
	LatestRun        string   `json:"latest_run,omitempty"`
	SourceCommit     string   `json:"source_commit,omitempty"`
	Candidate        string   `json:"candidate,omitempty"`
	TupleHead        string   `json:"tuple_head,omitempty"`
	TupleBase        string   `json:"tuple_base,omitempty"`
	TupleDiffBase    string   `json:"tuple_diff_base,omitempty"`
	PublishOp        string   `json:"publish_op,omitempty"`
	PRCreateOp       string   `json:"prcreate_op,omitempty"`
	PRNumber         string   `json:"pr_number,omitempty"`
	PRID             string   `json:"pr_id,omitempty"`
	NextCursor       string   `json:"next_cursor,omitempty"`
	ChainCursors     []string `json:"chain_cursors,omitempty"`
	Stale            bool     `json:"stale,omitempty"`
	RunHistory       []string `json:"run_history,omitempty"`
}

// IssueJourney is the one recorded journey subsection on the
// existing issue read: every recorded section from real persisted
// records with empty sections explicit, newest evidence last, plus the
// bounded completeness metadata. Available is false only when no
// attempt was ever recorded (explicit, never a 500). Complete holds
// only when every bounded read finished within its cap with no
// malformed row and no producer-absent section; any reason present
// means incomplete. Read endpoints spend nothing.
type IssueJourney struct {
	Lineage       journeyLineageView         `json:"lineage"`
	Phases        []journeyPhaseView         `json:"phases"`
	Tasks         []journeyTaskView          `json:"tasks"`
	Commits       []journeyCommitView        `json:"commits"`
	Findings      []journeyFindingView       `json:"findings"`
	Corrections   []journeyCorrectionView    `json:"corrections"`
	Judgments     []journeyJudgmentView      `json:"judgments"`
	Gates         []journeyGateView          `json:"gates"`
	Adoptions     []journeyAdoptionView      `json:"adoptions"`
	Delivered     []journeyDeliveredTaskView `json:"delivered_tasks"`
	Verifications []journeyVerifiedAppView   `json:"verifications"`
	Reasons       []string                   `json:"reasons,omitempty"`
	Repository    string                     `json:"repository"`
	Issue         string                     `json:"issue"`
	Available     bool                       `json:"available"`
	Complete      bool                       `json:"complete"`
	ObservedUnix  int64                      `json:"observed_unix"`
}

// addJourneyReason records one bounded reason code, deduplicated.
func addJourneyReason(reasons []string, code string) []string {
	for _, seen := range reasons {
		if seen == code {
			return reasons
		}
	}
	if len(reasons) >= maxJourneyReasons {
		return reasons
	}
	return append(reasons, code)
}
