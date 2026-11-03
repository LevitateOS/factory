package control

import (
	"errors"
	"reflect"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
)

// ClearAdmissionInput is the controller's complete explicit-clear input:
// the publication and its recorded review cycle, the actual completed
// typed reviewer assignment behind the verdict, the controller's
// canonical carried findings, and the controller-observed scope/policy/
// check evidence the acceptance binds. The clear verdict is read from
// the reviewer's recorded typed result — never a parallel report, and
// never a faked candidate: review outputs stay candidate-free. No
// allowance, chooser or delivery-call reservation travels here: a clear
// review creates acceptance with zero delivery calls by construction.
type ClearAdmissionInput struct {
	Publication    factory.Publication
	Cycle          factory.ReviewCycle
	Reviewer       factory.Assignment
	Carried        []factory.Finding
	ScopeDigest    string
	PolicyRevision string
	CheckDigest    string
	DiffBase       string
}

// AdmitClearReview admits one completed independent explicit clear review
// as clean_review delivery acceptance and journals it on the publication.
// Every D07/T41-T43 gate runs here:
//
//   - the cycle is recorded on this publication and validates;
//   - the reviewer assignment is the cycle's independent reviewer,
//     distinct from any corrector session, finished succeeded with a
//     reported completed typed result;
//   - the recorded verdict is the reviewer's explicit clear over the
//     controller's canonical carried set (silence, native approval,
//     empty output, incomplete coverage, omitted carried findings and
//     stale tuples refuse);
//   - the coverage digest recomputed from the canonical set must equal the
//     reported one, so same-key rewritten content invalidates;
//   - the verdict answers the cycle's exact head/base, which must still be
//     the publication's current candidate and PR creation base, and must
//     equal the reviewer's admitted source commit: the reviewer finished
//     on the exact head through its assignment binding, not a result
//     candidate field.
//
// The funded jev_acceptance variant is constructed by FW36 alongside D08
// routing; the union type and its validation already admit both kinds.
func AdmitClearReview(in ClearAdmissionInput) (factory.Publication, factory.DeliveryAcceptance, error) {
	var empty factory.DeliveryAcceptance
	published := in.Publication
	if err := in.Cycle.Validate(); err != nil {
		return published, empty, err
	}
	if !recordedClearCycle(published, in.Cycle) {
		return published, empty, errors.New("clear review consumes an unrecorded cycle")
	}
	if !recordedClearCycleMatches(published, in.Cycle) {
		return published, empty, errors.New("clear review consumes a substituted cycle")
	}
	report, err := checkClearReviewer(published, in)
	if err != nil {
		return published, empty, err
	}
	content, coverage, err := checkClearCoverage(in, report)
	if err != nil {
		return published, empty, err
	}
	return finishClearAcceptance(published, in, content, coverage)
}

// recordedClearCycle reports whether one cycle is recorded on its publication.
func recordedClearCycle(published factory.Publication, cycle factory.ReviewCycle) bool {
	for _, recorded := range published.ReviewCycles {
		if recorded.ID == cycle.ID {
			return true
		}
	}
	return false
}

// recordedClearCycleMatches reports whether the recorded row binds
// every immutable cycle field, not just its ID: a same-ID substituted
// cycle with another head, base, digest, round, phase, trigger or
// assignment takes no admission.
func recordedClearCycleMatches(published factory.Publication, cycle factory.ReviewCycle) bool {
	for _, recorded := range published.ReviewCycles {
		if recorded.ID != cycle.ID {
			continue
		}
		return recorded.PublicationID == published.ID &&
			recorded.AttemptRoot == cycle.AttemptRoot &&
			recorded.Round == cycle.Round &&
			recorded.Phase == cycle.Phase &&
			recorded.Head == cycle.Head &&
			recorded.BaseOID == cycle.BaseOID &&
			recorded.FindingDigest == cycle.FindingDigest &&
			recorded.ReviewerAssignment == cycle.ReviewerAssignment &&
			recorded.CorrectionAssignment == cycle.CorrectionAssignment &&
			recorded.TriggerDecision == cycle.TriggerDecision &&
			recorded.TriggerReason == cycle.TriggerReason
	}
	return false
}

// checkClearReviewer requires the cycle's independent phased reviewer
// with its recorded typed verdict, returning the verdict.
func checkClearReviewer(published factory.Publication, in ClearAdmissionInput) (factory.ClearReviewReport, error) {
	var empty factory.ClearReviewReport
	reviewer := in.Reviewer
	if err := reviewer.Validate(); err != nil {
		return empty, err
	}
	if reviewer.ID != in.Cycle.ReviewerAssignment {
		return empty, errors.New("clear review needs its independent reviewer assignment")
	}
	// The reviewer belongs to this publication's lineage: its owner
	// assignment and attempt root must match the recorded publication
	// and cycle, so a foreign attempt's reviewer takes no admission.
	if reviewer.PublicationAssignment != published.AssignmentID {
		return empty, errors.New("clear review needs its publication reviewer")
	}
	if reviewer.AttemptRoot != in.Cycle.AttemptRoot {
		return empty, errors.New("clear review needs its attempt reviewer")
	}
	if reviewer.Role != project.RoleReviewer || reviewer.Phase != project.ExecutionReview {
		return empty, errors.New("clear review needs its phased reviewer assignment")
	}
	if reviewer.Stage != factory.AssignmentFinished || reviewer.Outcome != factory.Succeeded {
		return empty, errors.New("clear review needs its finished reviewer")
	}
	if reviewer.Result == nil || !reviewer.Result.Reported || reviewer.Result.Status != "completed" {
		return empty, errors.New("clear review needs its reported reviewer result")
	}
	if reviewer.Result.PhaseResult == nil || reviewer.Result.PhaseResult.Output == nil ||
		reviewer.Result.PhaseResult.Output.Clear == nil {
		return empty, errors.New("clear review needs its recorded typed verdict")
	}
	if in.Cycle.CorrectionAssignment != "" && in.Cycle.CorrectionAssignment == reviewer.ID {
		return empty, errors.New("reviewer and corrector sessions must stay distinct")
	}
	return *reviewer.Result.PhaseResult.Output.Clear, nil
}

// checkClearCoverage binds one clear verdict to the canonical carried
// set, returning its content and coverage digests.
func checkClearCoverage(in ClearAdmissionInput, report factory.ClearReviewReport) (string, string, error) {
	if err := report.Validate(in.Carried); err != nil {
		return "", "", err
	}
	if report.CycleID != in.Cycle.ID {
		return "", "", errors.New("clear review answers another cycle")
	}
	content, err := factory.FindingContentDigest(in.Carried)
	if err != nil {
		return "", "", err
	}
	coverage, err := factory.ClearCoverageDigest(in.Cycle.Head, in.Cycle.BaseOID, content)
	if err != nil {
		return "", "", err
	}
	if report.CoverageDigest != coverage {
		return "", "", errors.New("clear review coverage answers another input")
	}
	if report.Head != in.Cycle.Head || report.BaseOID != in.Cycle.BaseOID {
		return "", "", errors.New("clear review answers a superseded tuple")
	}
	return content, coverage, nil
}

// finishClearAcceptance binds one verified clear verdict to its tuple
// and journals it on the publication.
func finishClearAcceptance(published factory.Publication, in ClearAdmissionInput, content, coverage string) (factory.Publication, factory.DeliveryAcceptance, error) {
	var empty factory.DeliveryAcceptance
	if in.Cycle.Head != published.Candidate || in.Cycle.BaseOID != published.PRCreate.BaseOID {
		return published, empty, errors.New("clear review answers a superseded tuple")
	}
	if in.Reviewer.SourceCommit != in.Cycle.Head {
		return published, empty, errors.New("reviewer finished on another candidate")
	}
	if !factory.ValidDigest(in.ScopeDigest) || !factory.ValidDigest(in.CheckDigest) {
		return published, empty, errors.New("clear review lacks its bound evidence digests")
	}
	tuple, err := deliveryAcceptanceTuple(published, in.Cycle.Head, in.Cycle.BaseOID, in.DiffBase)
	if err != nil {
		return published, empty, err
	}
	acceptance := factory.DeliveryAcceptance{
		Tuple: tuple, ID: factory.DeliveryAcceptanceID(published.ID, in.Cycle.AttemptRoot, in.Cycle.Round, factory.AcceptanceCleanReview),
		PublicationID: published.ID, AttemptRoot: in.Cycle.AttemptRoot,
		ScopeDigest: in.ScopeDigest, FindingDigest: content, CoverageDigest: coverage, CheckDigest: in.CheckDigest,
		PolicyRevision: in.PolicyRevision, OriginReceipt: in.Reviewer.ID,
		Kind: factory.AcceptanceCleanReview, Round: in.Cycle.Round,
	}
	if err := acceptance.Validate(); err != nil {
		return published, empty, err
	}
	if err := appendDeliveryAcceptance(&published, acceptance); err != nil {
		return published, empty, err
	}
	return published, acceptance, nil
}

// deliveryAcceptanceTuple reuses the publication's recorded tuple on
// the same head and base, else validates a fresh one.
func deliveryAcceptanceTuple(published factory.Publication, head, base, diffBase string) (factory.CandidateTuple, error) {
	tuple := factory.CandidateTuple{Head: head, Base: base, DiffBase: diffBase}
	if published.Tuple != nil && published.Tuple.Head == head && published.Tuple.Base == base {
		return *published.Tuple, nil
	}
	if err := tuple.Validate(); err != nil {
		return factory.CandidateTuple{}, err
	}
	return tuple, nil
}

// JevAdmissionInput is the controller's complete funded-decision input:
// the publication and its recorded review cycle, the controller's
// canonical carried findings, the exact fingerprint evidence the funded
// decision judged with its fingerprint, the decided funded delivery
// decision, the funded call identity behind it, the controller-observed
// scope/policy/check evidence the acceptance binds, and the intended
// native resolution operations. No new funded call happens here: the
// decision is the already-paid evidence this acceptance records.
type JevAdmissionInput struct {
	Publication       factory.Publication
	Cycle             factory.ReviewCycle
	Carried           []factory.Finding
	FingerprintInput  factory.DeliveryFingerprintInput
	Fingerprint       string
	Decision          factory.DeliveryDecision
	DecisionID        string
	ScopeDigest       string
	PolicyRevision    string
	CheckDigest       string
	DiffBase          string
	ResolutionIntents []string
}

// AdmitJevAcceptance admits one funded ready_to_merge delivery decision
// as jev_acceptance delivery acceptance and journals it on the
// publication. Every D07/D08 gate runs here:
//
//   - the cycle is recorded on this publication and validates, over the
//     publication's exact current candidate and PR creation base;
//   - the fingerprint evidence recomputes exactly and matches both the
//     supplied fingerprint and the decision's judged fingerprint, over
//     this publication, cycle round, head and base, so stale semantic
//     evidence never rides a prior decision;
//   - the decision validates with the funded model behind it and its
//     effective action is exactly ready_to_merge: review_again,
//     judgment_attention and unavailable decisions never create
//     acceptance — only positive evidence does;
//   - the funded call identity is a valid charged request distinct from
//     the origin receipt, which names the cycle's independent reviewer
//     assignment behind the judged round;
//   - the finding digest is always the D06 complete content digest
//     computed from canonical findings at admission, and the coverage
//     digest recomputes from it exactly like the clear path.
//
// The union type and its validation already admit both kinds; merge-entry
// gating consumes the journal in FW37.
func AdmitJevAcceptance(in JevAdmissionInput) (factory.Publication, factory.DeliveryAcceptance, error) {
	var empty factory.DeliveryAcceptance
	published := in.Publication
	if err := in.Cycle.Validate(); err != nil {
		return published, empty, err
	}
	if !recordedClearCycle(published, in.Cycle) || in.Cycle.PublicationID != published.ID {
		return published, empty, errors.New("funded decision consumes an unrecorded cycle")
	}
	if in.Cycle.Head != published.Candidate || in.Cycle.BaseOID != published.PRCreate.BaseOID {
		return published, empty, errors.New("funded decision answers a superseded tuple")
	}
	if err := checkJevFingerprint(published, in); err != nil {
		return published, empty, err
	}
	if err := checkJevDecision(in); err != nil {
		return published, empty, err
	}
	content, err := factory.FindingContentDigest(in.Carried)
	if err != nil {
		return published, empty, err
	}
	coverage, err := factory.ClearCoverageDigest(in.Cycle.Head, in.Cycle.BaseOID, content)
	if err != nil {
		return published, empty, err
	}
	if !factory.ValidDigest(in.ScopeDigest) || !factory.ValidDigest(in.CheckDigest) {
		return published, empty, errors.New("funded acceptance lacks its bound evidence digests")
	}
	tuple, err := deliveryAcceptanceTuple(published, in.Cycle.Head, in.Cycle.BaseOID, in.DiffBase)
	if err != nil {
		return published, empty, err
	}
	acceptance := factory.DeliveryAcceptance{
		Tuple: tuple, ID: factory.DeliveryAcceptanceID(published.ID, in.Cycle.AttemptRoot, in.Cycle.Round, factory.AcceptanceJevAcceptance),
		PublicationID: published.ID, AttemptRoot: in.Cycle.AttemptRoot,
		ScopeDigest: in.ScopeDigest, FindingDigest: content, CoverageDigest: coverage, CheckDigest: in.CheckDigest,
		PolicyRevision: in.PolicyRevision, DecisionID: in.DecisionID, OriginReceipt: in.Cycle.ReviewerAssignment,
		ResolutionIntents: in.ResolutionIntents,
		Kind:              factory.AcceptanceJevAcceptance, Round: in.Cycle.Round,
	}
	if err := acceptance.Validate(); err != nil {
		return published, empty, err
	}
	if err := appendDeliveryAcceptance(&published, acceptance); err != nil {
		return published, empty, err
	}
	return published, acceptance, nil
}

// checkJevFingerprint binds the funded decision's fingerprint to its
// recomputed evidence over this publication, round, head and base.
func checkJevFingerprint(published factory.Publication, in JevAdmissionInput) error {
	if err := in.FingerprintInput.Validate(); err != nil {
		return err
	}
	bound, err := factory.DeliveryFingerprint(in.FingerprintInput)
	if err != nil {
		return err
	}
	if !factory.ValidDigest(in.Fingerprint) || bound != in.Fingerprint {
		return errors.New("funded decision evidence differs from its fingerprint")
	}
	if in.FingerprintInput.PublicationID != published.ID || in.FingerprintInput.AttemptRoot != in.Cycle.AttemptRoot ||
		in.FingerprintInput.Round != in.Cycle.Round || in.FingerprintInput.Head != in.Cycle.Head ||
		in.FingerprintInput.Base != in.Cycle.BaseOID {
		return errors.New("funded decision judged another round")
	}
	return nil
}

// checkJevDecision requires a ready funded decision with its funded
// call identity behind the cycle's independent reviewer run.
func checkJevDecision(in JevAdmissionInput) error {
	if err := in.Decision.Validate(); err != nil {
		return err
	}
	if in.Decision.Answer.Fingerprint != in.Fingerprint || in.Decision.Answer.Model != in.FingerprintInput.Model {
		return errors.New("funded decision judged different evidence")
	}
	if in.Decision.Effective != factory.DeliveryEffectiveReady {
		return errors.New("only a ready funded decision creates acceptance")
	}
	if !factory.ValidID(in.DecisionID) {
		return errors.New("funded acceptance lacks its funded call identity")
	}
	if !factory.ValidID(in.Cycle.ReviewerAssignment) {
		return errors.New("funded acceptance lacks its independent reviewer run")
	}
	if in.Cycle.CorrectionAssignment != "" && in.Cycle.CorrectionAssignment == in.Cycle.ReviewerAssignment {
		return errors.New("reviewer and corrector sessions must stay distinct")
	}
	return nil
}

// appendDeliveryAcceptance records one admitted acceptance as the
// publication's next immutable journal entry: the acceptance validates,
// belongs to this publication and carries a fresh derived identity. A
// replay reuses the identical entry; changed content under an already
// consumed identity refuses instead of replacing its evidence.
func appendDeliveryAcceptance(p *factory.Publication, acceptance factory.DeliveryAcceptance) error {
	if err := acceptance.Validate(); err != nil {
		return err
	}
	if acceptance.PublicationID != p.ID {
		return errors.New("delivery acceptance belongs to another publication")
	}
	for _, recorded := range p.DeliveryAcceptances {
		if recorded.ID == acceptance.ID {
			if reflect.DeepEqual(recorded, acceptance) {
				return nil
			}
			return errors.New("delivery acceptance identity carries different evidence")
		}
	}
	p.DeliveryAcceptances = append(p.DeliveryAcceptances, acceptance)
	return nil
}

// AdmitCorrectionReportCanonical admits one corrector run's report against
// its review cycle exactly like AdmitCorrectionReport, after binding the
// submitted findings to the controller's canonical set byte-for-byte. An
// agent submission that rewrites any canonical defect, provenance,
// severity, actionability, outcome, fix kind or location refuses before
// disposition rules run. Callers without a canonical set keep the legacy
// entrypoint; FW37 wires this canonical gate into every serving entry.
func AdmitCorrectionReportCanonical(cycle factory.ReviewCycle, runID, newCandidate string, canonical []factory.Finding, report factory.CorrectionReport, evidence []factory.CorrectionFixEvidence, checks *factory.CheckAssessment) (CorrectionAdmission, error) {
	var empty CorrectionAdmission
	if err := factory.CheckSubmittedFindings(canonical, report.Findings); err != nil {
		return empty, err
	}
	return AdmitCorrectionReport(cycle, runID, newCandidate, report, evidence, checks)
}
