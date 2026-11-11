package control

// Role consumers for MCP finish proposals.
//
// Each finish tool has one synchronous mapping from (assignment, payload) to
// the finished assignment. The MCP handler runs it before anything is
// recorded (CheckMCPTerminalFinish): a refusal goes straight back to the
// worker, which corrects and finishes again. The settler runs the same
// mapping on the recorded proposal and finishes the assignment through the
// joints every result shares (phase binding and decompose semantic settlement).
// Nothing here coerces worker prose into settlement facts,
// and no downstream effect runs here: finished typed results advance through
// the existing dispatch, publication, review and adoption consumers.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
)

// mcpReviewTuple is the strict carrier shape for a review finish's
// tuple field: the cycle the verdict answers plus the exact head and
// base it covered. SodaOS fills it from the review cycle.
type mcpReviewTuple struct {
	CycleID string `json:"cycle_id"`
	Head    string `json:"head"`
	BaseOID string `json:"base_oid"`
}

// mapFW40Terminal maps one typed role finish to its finished assignment
// or refuses it with the defect the worker must correct.
func mapFW40Terminal(assigned factory.Assignment, submission factory.MCPSubmission, now time.Time) (factory.Assignment, string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(submission.Payload, &fields); err != nil {
		return factory.Assignment{}, "", fmt.Errorf("finish payload is not a JSON object: %w", err)
	}
	switch submission.Tool {
	case factory.MCPToolFinishProductPlan:
		return settleProductPlan(assigned, submission, fields, now)
	case factory.MCPToolFinishIssuePlan:
		return settleIssuePlan(assigned, submission, fields, now)
	case factory.MCPToolFinishReview:
		return settleReviewFinish(assigned, submission, fields, now)
	case factory.MCPToolFinishReassessment:
		return settleReassessmentFinish(assigned, submission, fields, now)
	case factory.MCPToolFinishAppVerify:
		return settleAppVerification(assigned, submission, fields, now)
	case factory.MCPToolFinishCorrection:
		return settleCorrectionFinish(assigned, submission, fields, now)
	default:
		return factory.Assignment{}, "", fmt.Errorf("%s is not a typed role finish", submission.Tool)
	}
}

// stringField decodes one payload field as a JSON string. Any other
// shape reports absent: the settler never coerces structured values
// into text facts.
func stringField(fields map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := fields[key]
	if !ok {
		return "", false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", false
	}
	return text, true
}

// strictDecodeField decodes one carrier field with unknown fields
// refused and no trailing content.
func strictDecodeField(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("carrier field carries trailing content")
	}
	return nil
}

// decodeRequired strictly decodes one required carrier field, naming the
// field in the refusal.
func decodeRequired(fields map[string]json.RawMessage, key, shape string, target any) error {
	raw, ok := fields[key]
	if !ok {
		return fmt.Errorf("%s is required: send %s", key, shape)
	}
	if err := strictDecodeField(raw, target); err != nil {
		return fmt.Errorf("%s must be %s: %w", key, shape, err)
	}
	return nil
}

// decodeOptional strictly decodes one optional carrier field when present.
func decodeOptional(fields map[string]json.RawMessage, key, shape string, target any) error {
	raw, ok := fields[key]
	if !ok {
		return nil
	}
	if err := strictDecodeField(raw, target); err != nil {
		return fmt.Errorf("%s must be %s: %w", key, shape, err)
	}
	return nil
}

// requirePhase refuses a finish sent on an assignment of another phase.
func requirePhase(assigned factory.Assignment, phase string, tool factory.MCPTool) error {
	if assigned.Phase != phase {
		return fmt.Errorf("%s does not answer this %s assignment", tool, assigned.Phase)
	}
	return nil
}

// phaseResultFor builds one completed strict v2 phase result naming
// its MCP tool and submission as its summary. The summary is a
// controller label, never evidence.
func phaseResultFor(submission factory.MCPSubmission, phase, fallback string, output *factory.PhaseOutput) factory.PhaseResult {
	return factory.PhaseResult{
		Version: factory.PhaseResultVersion, Phase: phase, Status: "completed",
		Summary: mcpTerminalSummary(submission.Tool, submission.SubmissionID, "", fallback),
		Output:  output,
	}
}

// finishMCPBlocked finishes one assignment from a worker's own
// incomplete verdict as a synthesized blocked phased result. The
// summary names the tool and submission behind the verdict.
func finishMCPBlocked(assigned factory.Assignment, submission factory.MCPSubmission, now time.Time, fallback string) factory.Assignment {
	result := synthesizePhased(assigned, "blocked",
		mcpTerminalSummary(submission.Tool, submission.SubmissionID, "", fallback), now.Unix())
	finished := assigned
	finished.Stage, finished.Outcome, finished.Reason = factory.AssignmentFinished, factory.NeedsHuman, factory.AssignReasonNoReport
	finished.FinishedUnix = now.Unix()
	finished.Result = &result
	return finished
}

// finishFromMCPPhaseResult finishes one assignment from a typed phase
// result through the shared builders and the decompose semantic joint.
// A result the assignment cannot carry refuses with its defect.
func finishFromMCPPhaseResult(assigned factory.Assignment, parsed factory.PhaseResult, now time.Time) (factory.Assignment, error) {
	if !factory.ValidPhaseRole(assigned.Phase, assigned.Role) {
		return factory.Assignment{}, errors.New("assignment role does not match its execution phase")
	}
	if err := parsed.BindAssignment(assigned); err != nil {
		return factory.Assignment{}, fmt.Errorf("result does not fit this assignment: %w", err)
	}
	result, outcome, reason := buildPhasedCoderResult(assigned, parsed, now.Unix())
	if !result.Reported {
		return factory.Assignment{}, errors.New(result.Summary)
	}
	finished := assigned
	finished.Stage, finished.Outcome, finished.Reason = factory.AssignmentFinished, outcome, reason
	finished.FinishedUnix = now.Unix()
	finished.Result = &result
	if finished.Phase == project.ExecutionDecompose && finished.Subject.Intent != nil {
		if err := checkDecomposeSemantic(finished); err != nil {
			return factory.Assignment{}, err
		}
	}
	return finished, nil
}

// settleProductPlan settles one Product Lead finish. Intent, revision and
// operation bind from the assignment subject and its staged planning input,
// never from worker payload. A verification manifest proposal remains a
// typed proposal until exact accepted graph adoption.
func settleProductPlan(assigned factory.Assignment, submission factory.MCPSubmission, fields map[string]json.RawMessage, now time.Time) (factory.Assignment, string, error) {
	if err := requirePhase(assigned, project.ExecutionDecompose, submission.Tool); err != nil {
		return factory.Assignment{}, "", err
	}
	in, staged, err := ParseStagedPlanningInput(assigned.Prompt)
	if err != nil || !staged || assigned.Subject.Intent == nil {
		return factory.Assignment{}, "", errors.New("this assignment has no staged planning input; finish with factory.finish_incomplete")
	}
	var candidates []factory.SemanticCandidate
	if err := decodeRequired(fields, "candidates", "an array of structured semantic candidate objects", &candidates); err != nil {
		return factory.Assignment{}, "", err
	}
	out := factory.SemanticPlannerOutput{
		Intent: assigned.Subject.Intent.IntentID, Revision: assigned.Subject.Intent.RevisionID,
		Operation: in.Operation, Candidates: candidates,
	}
	if raw, present := fields["walkthroughs"]; present {
		var content factory.ProposalContent
		if err := strictDecodeField(raw, &content); err != nil {
			return factory.Assignment{}, "", fmt.Errorf("walkthroughs must be one ProposalContent object: %w", err)
		}
		if err := factory.ValidatePlanningWalkthroughs(in.Head, content); err != nil {
			return factory.Assignment{}, "", fmt.Errorf("product walkthrough differs from its planning scope: %w", err)
		}
		out.Walkthroughs = &content
	}
	if raw, present := fields["verification_manifest_proposal"]; present {
		proposal, err := factory.ParseVerificationManifestProposal(raw)
		if err != nil {
			return factory.Assignment{}, "", fmt.Errorf("verification_manifest_proposal refused: %w", err)
		}
		if err := proposal.ValidateOutcomes(in.Head); err != nil {
			return factory.Assignment{}, "", fmt.Errorf("verification manifest differs from its planning scope: %w", err)
		}
		out.VerificationManifestProposal = &proposal
	}
	parsed := phaseResultFor(submission, project.ExecutionDecompose, "worker proposes product plan",
		&factory.PhaseOutput{Decomposition: &out})
	finished, err := finishFromMCPPhaseResult(assigned, parsed, now)
	return finished, "product plan settled", err
}

// settleIssuePlan settles one Architect finish carrying the full typed
// investigation plan in tasks. SodaOS fills the plan's recorded bindings
// before this runs, so a binding mismatch here names a real defect.
func settleIssuePlan(assigned factory.Assignment, submission factory.MCPSubmission, fields map[string]json.RawMessage, now time.Time) (factory.Assignment, string, error) {
	if err := requirePhase(assigned, project.ExecutionInvestigate, submission.Tool); err != nil {
		return factory.Assignment{}, "", err
	}
	var plan factory.InvestigationPlan
	if err := decodeRequired(fields, "tasks", "the complete typed InvestigationPlan object", &plan); err != nil {
		return factory.Assignment{}, "", err
	}
	refuse := func(reason string) (factory.Assignment, string, error) {
		hint := ""
		if digest, err := factory.PlanDigest(plan); err == nil {
			hint = "; expected_plan_digest=" + digest + " for these exact typed plan fields (recompute after editing)"
		}
		return factory.Assignment{}, "", fmt.Errorf("issue plan refused: %s%s", reason, hint)
	}
	if err := plan.Validate(); err != nil {
		return refuse(err.Error())
	}
	if plan.AttemptRoot != assigned.AttemptRoot || plan.SourceCommit != assigned.SourceCommit || plan.Preparation != assigned.Preparation {
		return refuse("attempt_root, source_commit and preparation must equal this assignment's recorded bindings")
	}
	if approach, _ := stringField(fields, "approach"); approach != plan.Approach {
		return refuse("approach must equal tasks.approach")
	}
	review, _ := stringField(fields, "plan_review")
	if review != "" && !factory.ValidDigest(review) {
		return refuse("plan_review must identify the recorded selected-source opinion")
	}
	parsed := phaseResultFor(submission, project.ExecutionInvestigate, "worker proposes issue plan",
		&factory.PhaseOutput{Plan: &plan, PlanReview: review})
	finished, err := finishFromMCPPhaseResult(assigned, parsed, now)
	return finished, "issue plan settled", err
}

// settleReviewFinish settles one Inspector finish. Incomplete settles as a
// blocked result. Clear and findings need the review tuple and, for clear,
// the coverage digest SodaOS fills from the cycle; silence or prose
// coverage never clears work.
func settleReviewFinish(assigned factory.Assignment, submission factory.MCPSubmission, fields map[string]json.RawMessage, now time.Time) (factory.Assignment, string, error) {
	if err := requirePhase(assigned, project.ExecutionReview, submission.Tool); err != nil {
		return factory.Assignment{}, "", err
	}
	conclusion, _ := stringField(fields, "conclusion")
	switch conclusion {
	case "incomplete":
		return finishMCPBlocked(assigned, submission, now, "worker reports incomplete review"), "incomplete review settled", nil
	case "clear", "findings":
	default:
		return factory.Assignment{}, "", errors.New("conclusion must be clear, findings or incomplete")
	}
	var tuple mcpReviewTuple
	if err := decodeRequired(fields, "tuple", "the review cycle tuple", &tuple); err != nil {
		return factory.Assignment{}, "", fmt.Errorf("the review cycle is unavailable (%v); finish with factory.finish_incomplete", err)
	}
	context := assigned.ReviewContext
	if context == nil || context.ValidateFor(assigned) != nil {
		return factory.Assignment{}, "", errors.New("this review has no granted context; inspect factory.context or finish with factory.finish_incomplete")
	}
	if tuple.CycleID != context.Cycle.ID || tuple.Head != context.Cycle.Head || tuple.BaseOID != context.Cycle.BaseOID {
		return factory.Assignment{}, "", errors.New("review tuple differs from the assigned cycle")
	}
	var assessments []factory.FindingAssessment
	if err := decodeOptional(fields, "carried_assessments", "an array of finding assessment objects", &assessments); err != nil {
		return factory.Assignment{}, "", err
	}
	if conclusion == "clear" {
		coverage, _ := stringField(fields, "coverage")
		if !factory.ValidDigest(coverage) {
			return factory.Assignment{}, "", errors.New("the review cycle has no clear coverage digest; finish with conclusion findings or factory.finish_incomplete")
		}
		report := factory.ClearReviewReport{
			Conclusion: factory.ClearReviewConclusion, CycleID: tuple.CycleID,
			Head: tuple.Head, BaseOID: tuple.BaseOID, CoverageDigest: coverage,
			Assessments: assessments,
		}
		content, err := factory.FindingContentDigest(context.CarriedFindings)
		if err != nil {
			return factory.Assignment{}, "", err
		}
		expected, err := factory.ClearCoverageDigest(tuple.Head, tuple.BaseOID, content)
		if err != nil || coverage != expected {
			return factory.Assignment{}, "", errors.New("clear coverage differs from the assigned finding set")
		}
		if err := report.Validate(context.CarriedFindings); err != nil {
			return factory.Assignment{}, "", fmt.Errorf("clear review must verify each carried actionable finding in carried_assessments: %w", err)
		}
		parsed := phaseResultFor(submission, project.ExecutionReview, "worker reports clear review",
			&factory.PhaseOutput{Clear: &report})
		finished, err := finishFromMCPPhaseResult(assigned, parsed, now)
		return finished, "clear review settled", err
	}
	var findings []factory.Finding
	if err := decodeOptional(fields, "findings", "an array of structured finding objects", &findings); err != nil {
		return factory.Assignment{}, "", err
	}
	if len(findings) == 0 && len(assessments) == 0 {
		return factory.Assignment{}, "", errors.New("conclusion findings needs structured findings or carried_assessments")
	}
	report := factory.ReviewCycleReport{CycleID: tuple.CycleID, NewFindings: findings, Assessments: assessments}
	if err := report.Validate(context.CarriedFindings); err != nil {
		return factory.Assignment{}, "", err
	}
	parsed := phaseResultFor(submission, project.ExecutionReview, "worker reports review findings",
		&factory.PhaseOutput{Review: &report})
	finished, err := finishFromMCPPhaseResult(assigned, parsed, now)
	return finished, "review findings settled", err
}

// settleReassessmentFinish settles one Strategist finish whose report
// holds the typed report its admitted phase and subject select: a D13
// reassessment report for reassess, a stall or delivery repair report for
// repair. The report is a proposal to existing adoption consumers.
func settleReassessmentFinish(assigned factory.Assignment, submission factory.MCPSubmission, fields map[string]json.RawMessage, now time.Time) (factory.Assignment, string, error) {
	var output *factory.PhaseOutput
	switch {
	case assigned.Phase == project.ExecutionReassess:
		var report factory.ReassessmentReport
		if err := decodeRequired(fields, "report", "the full typed ReassessmentReport", &report); err != nil {
			return factory.Assignment{}, "", err
		}
		output = &factory.PhaseOutput{Reassessment: &report}
	case assigned.Phase == project.ExecutionRepair && assigned.Subject.Kind == factory.SubjectStall:
		var report factory.StallRepairReport
		if err := decodeRequired(fields, "report", "the full typed StallRepairReport", &report); err != nil {
			return factory.Assignment{}, "", err
		}
		output = &factory.PhaseOutput{Repair: &report}
	case assigned.Phase == project.ExecutionRepair && assigned.Subject.Kind == factory.SubjectDeliveryRepair:
		var report factory.DeliveryRepairReport
		if err := decodeRequired(fields, "report", "the full typed DeliveryRepairReport", &report); err != nil {
			return factory.Assignment{}, "", err
		}
		output = &factory.PhaseOutput{DeliveryRepair: &report}
	default:
		return factory.Assignment{}, "", fmt.Errorf("%s does not answer this %s assignment", submission.Tool, assigned.Phase)
	}
	parsed := phaseResultFor(submission, assigned.Phase, "worker proposes reassessment", output)
	finished, err := finishFromMCPPhaseResult(assigned, parsed, now)
	return finished, "reassessment settled", err
}

// settleAppVerification settles one Test Pilot finish. Incomplete settles
// as a blocked result. Verified and failed need the delivery and input
// digest SodaOS fills plus the structured check receipts and coverage.
// The recorded report is a claim for the trusted-manifest consumers: it
// never deploys anything.
func settleAppVerification(assigned factory.Assignment, submission factory.MCPSubmission, fields map[string]json.RawMessage, now time.Time) (factory.Assignment, string, error) {
	if err := requirePhase(assigned, project.ExecutionVerify, submission.Tool); err != nil {
		return factory.Assignment{}, "", err
	}
	outcome, _ := stringField(fields, "outcome")
	switch outcome {
	case factory.AppVerificationIncomplete:
		return finishMCPBlocked(assigned, submission, now, "worker reports incomplete verification"), "incomplete verification settled", nil
	case factory.AppVerificationVerified, factory.AppVerificationFailed:
	default:
		return factory.Assignment{}, "", errors.New("outcome must be verified, failed or incomplete")
	}
	deliveryID, _ := stringField(fields, "delivery_id")
	inputDigest, _ := stringField(fields, "input_digest")
	if !factory.ValidID(deliveryID) || !factory.ValidDigest(inputDigest) {
		return factory.Assignment{}, "", errors.New("this assignment has no recorded delivery; finish with outcome incomplete")
	}
	report := factory.AppVerificationReport{DeliveryID: deliveryID, InputDigest: inputDigest, Outcome: outcome}
	if err := decodeRequired(fields, "check_receipts", "an array of observed check receipt objects", &report.CheckReceipts); err != nil {
		return factory.Assignment{}, "", err
	}
	if err := decodeRequired(fields, "coverage", "an array of outcome coverage objects", &report.Coverage); err != nil {
		return factory.Assignment{}, "", err
	}
	if err := decodeOptional(fields, "failures", "an array of check failure objects", &report.Failures); err != nil {
		return factory.Assignment{}, "", err
	}
	if err := decodeOptional(fields, "artifact_refs", "an array of artifact digests", &report.ArtifactRefs); err != nil {
		return factory.Assignment{}, "", err
	}
	if err := decodeOptional(fields, "evidence_refs", "an array of evidence references", &report.EvidenceRefs); err != nil {
		return factory.Assignment{}, "", err
	}
	parsed := phaseResultFor(submission, project.ExecutionVerify, "worker reports app verification",
		&factory.PhaseOutput{Verification: &report})
	finished, err := finishFromMCPPhaseResult(assigned, parsed, now)
	return finished, "app verification settled", err
}

// settleCorrectionFinish keeps the typed correction report from the
// authenticated proposal to the existing correction admission. Findings
// come from the granted canonical context, never a worker replacement set.
// Dispositions and fix evidence remain nominations; native final/check/tree
// evidence is independently required before settlement and publication.
func settleCorrectionFinish(assigned factory.Assignment, submission factory.MCPSubmission, fields map[string]json.RawMessage, now time.Time) (factory.Assignment, string, error) {
	if err := requirePhase(assigned, project.ExecutionCorrect, submission.Tool); err != nil {
		return factory.Assignment{}, "", err
	}
	if assigned.ReviewContext == nil || assigned.ReviewContext.ValidateFor(assigned) != nil {
		return factory.Assignment{}, "", errors.New("this correction has no granted review context; finish with factory.finish_incomplete")
	}
	context := assigned.ReviewContext
	digest, _ := stringField(fields, "finding_digest")
	if digest != context.Cycle.FindingDigest {
		return factory.Assignment{}, "", errors.New("finding_digest differs from the supplied finding set; omit it and SodaOS fills it")
	}
	candidate, _ := stringField(fields, "candidate")
	if !factory.ValidCommit(candidate) {
		return factory.Assignment{}, "", errors.New("candidate must be the exact 40-hex commit of your resulting tree")
	}
	report := factory.CorrectionReport{SetDigest: digest, NewCandidate: candidate, Findings: append([]factory.Finding(nil), context.CarriedFindings...)}
	if err := decodeRequired(fields, "dispositions", "an array of {key, disposition} objects", &report.Dispositions); err != nil {
		return factory.Assignment{}, "", err
	}
	if err := decodeOptional(fields, "fix_evidence", "an array of {key, changed_paths, commits} objects", &report.Evidence); err != nil {
		return factory.Assignment{}, "", err
	}
	if err := report.Validate(); err != nil {
		return factory.Assignment{}, "", fmt.Errorf("correction report refused: %w", err)
	}
	parsed := phaseResultFor(submission, project.ExecutionCorrect, "worker proposes correction dispositions", &factory.PhaseOutput{Correction: &report})
	finished, err := finishFromMCPPhaseResult(assigned, parsed, now)
	return finished, "typed correction finish settled", err
}

// settleImplementation settles one Builder finish as a typed
// implementation receipt naming its exact candidate commit.
func settleImplementation(assigned factory.Assignment, submission factory.MCPSubmission, fields map[string]json.RawMessage, now time.Time) (factory.Assignment, string, error) {
	if err := requirePhase(assigned, project.ExecutionImplement, submission.Tool); err != nil {
		return factory.Assignment{}, "", err
	}
	candidate, _ := stringField(fields, "candidate")
	if !factory.ValidCommit(candidate) {
		return factory.Assignment{}, "", errors.New("candidate must be the exact 40-hex commit the executed plan produced")
	}
	parsed := phaseResultFor(submission, project.ExecutionImplement, "worker nominates "+candidate,
		&factory.PhaseOutput{Implementation: &factory.ImplementationReceipt{Candidate: candidate}})
	finished, err := finishFromMCPPhaseResult(assigned, parsed, now)
	return finished, "code finish settled", err
}
