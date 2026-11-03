package factory

import (
	"errors"
	"strconv"
	"strings"

	"github.com/levitateos/sodaos/internal/project"
)

func promptStage(in PromptInputs) string {
	if in.Role == project.RoleReviewer {
		return "review"
	}
	if in.PublicationAssignment != "" {
		return "correction"
	}
	return "coding"
}

func validatePromptStage(in PromptInputs) error {
	if err := validatePromptRole(in); err != nil {
		return err
	}
	if in.PublicationAssignment == "" {
		return nil
	}
	if !ValidID(in.PublicationAssignment) || !ValidCommit(in.BaseCommit) {
		return errors.New("child prompt needs its publication and verified base")
	}
	if err := validatePromptReviewEvidence(in); err != nil {
		return err
	}
	if err := validatePromptCheckEvidence(in); err != nil {
		return err
	}
	return validateCorrectionPrompt(in)
}

// validatePromptRole admits the prompt role with its base, requiring
// initial coding prompts to carry no child evidence.
func validatePromptRole(in PromptInputs) error {
	if (in.Role != project.RoleCoder && in.Role != project.RoleReviewer) || !ValidCommit(in.ApprovedBase) {
		return errors.New("invalid prompt role or approved base")
	}
	if in.PublicationAssignment == "" {
		if in.Role != project.RoleCoder || in.BaseCommit != "" || in.CheckAssessment != nil ||
			in.TypedReview != nil || in.TypedClear != nil {
			return errors.New("initial coding prompt carries child evidence")
		}
	}
	return nil
}

// validatePromptReviewEvidence admits the carried review evidence: a
// single valid typed verdict. Fresh independent reviews carry no prior
// verdict (caller passes nil); same-head re-reviews carry the full
// outstanding review/check evidence as carried input to independently
// assess, never to rubber-stamp. Distinct reviewer/corrector sessions
// are retained by the dispatcher.
func validatePromptReviewEvidence(in PromptInputs) error {
	if in.TypedReview != nil || in.TypedClear != nil {
		if (in.TypedReview == nil) == (in.TypedClear == nil) {
			return errors.New("dispatch prompt carries no single typed verdict")
		}
		if in.TypedReview != nil {
			return validateReviewShape(in.TypedReview)
		}
		return validateClearShape(in.TypedClear)
	}
	return nil
}

// validatePromptCheckEvidence binds the check assessment to its exact
// candidate and required checks.
func validatePromptCheckEvidence(in PromptInputs) error {
	if a := in.CheckAssessment; a != nil {
		if a.Validate() != nil || a.Repository != in.Repository || a.HeadOID != in.SourceCommit || a.BaseOID != in.PRBase ||
			a.BaseRef != in.TargetBranch || a.ChecksDigest != ChecksDigest(in.RequiredChecks) {
			return errors.New("prompt check evidence does not match its exact candidate and checks")
		}
	}
	return nil
}

// validateCorrectionPrompt admits one correction prompt's consolidated
// review and failed-check evidence. Corrections carry the findings
// verdict (always blocking) or the explicit clear beside failing checks
// (fix the checks). Anything else is not a correction.
func validateCorrectionPrompt(in PromptInputs) error {
	if in.Role != project.RoleCoder {
		return nil
	}
	if in.CheckAssessment == nil ||
		(in.CheckAssessment.Verdict != CheckPass && in.CheckAssessment.Verdict != CheckFailed) {
		return errors.New("correction needs consolidated current review and failed-check evidence")
	}
	switch {
	case in.TypedReview != nil:
	case in.TypedClear != nil:
		if in.CheckAssessment.Verdict != CheckFailed {
			return errors.New("correction needs consolidated current review and failed-check evidence")
		}
	default:
		return errors.New("correction needs consolidated current review and failed-check evidence")
	}
	return nil
}

func writePromptStage(b *strings.Builder, in PromptInputs) {
	if in.CheckAssessment != nil {
		a := in.CheckAssessment
		b.WriteString("\n## Recorded check evidence\n\n")
		b.WriteString("Head: " + a.HeadOID + " base: " + a.BaseOID + "; verdict: " + a.Verdict + "; reason: " + a.Reason + "\n")
		b.WriteString("Assessment revision: " + strconv.FormatInt(a.Revision, 10) + " native revision: " + strconv.FormatInt(a.NativeRev, 10) + "\n")
		for _, result := range a.Results {
			b.WriteString("- " + strconv.Quote(result.Context) + ": " + strconv.Quote(result.State) + "; passed: " + strconv.FormatBool(result.Passed) + "\n")
		}
	}
	writePromptTypedVerdict(b, in)
}

// writePromptMCPReviewContract directs a review to its finish_review call.
func writePromptMCPReviewContract(b *strings.Builder) {
	b.WriteString("Begin with factory.context and read current_role_page continuations until collection_complete. Its required review input names the exact cycle, admitted/current candidate tuple, canonical carried findings and clear_coverage_digest. Inspect the prepared candidate and diff independently. The review.implementation field contains the original published implementation's admitted task/check definitions, committed task receipts and trusted final checkpoint; review.correction_facts separately describes later corrections on the current tuple. review.check_assessment supplies the settled native CI status evidence required before admission on this exact head/base; refresh context before reporting missing required evidence. These receipts are evidence, not a coding conversation or a worker's claimed approval.\n\n")
	b.WriteString("Finish through factory.finish_review using the advertised current.role.result_schema. SodaOS fills the review tuple and coverage from review.cycle; supply evidence_refs for evidence actually inspected. Use conclusion clear only after complete inspection with no remaining findings; include carried_assessments proving every carried actionable finding verified. Otherwise use conclusion findings with structured findings and carried_assessments, preserving stable keys and required outcomes. A missing local file named after a CI status context is not a failed required check. If required evidence is unavailable, use factory.finish_incomplete with the concrete missing input. End your work after the finish tool succeeds; Factory processes the posted result.\n\n")
	b.WriteString("Do not substitute an approve string, an empty finding list or process exit for this durable typed result. Never change the candidate or its checks to obtain approval.\n")
}

// writePromptTypedVerdict renders the phased current verdict a re-review
// or correction must answer: keyed findings with typed dispositions. The
// rendering is deterministic evidence, never a second authority: the
// consumer binds the same verdict by cycle, head and carried set.
func writePromptTypedVerdict(b *strings.Builder, in PromptInputs) {
	if in.TypedReview == nil && in.TypedClear == nil {
		return
	}
	if in.Role == project.RoleReviewer {
		b.WriteString("\n## Carried typed review verdict for independent re-review\n\n")
		b.WriteString("Assess every carried actionable finding explicitly as verified|still_open with evidence; report new defects separately with stable keys. Do not repost duplicate native comments for unchanged findings.\n")
	} else {
		b.WriteString("\n## Phased review evidence\n\n")
	}
	if in.TypedReview != nil {
		r := in.TypedReview
		b.WriteString("Findings verdict for cycle " + r.CycleID + " on candidate " + in.SourceCommit + " at base " + in.BaseCommit + ".\n")
		for _, finding := range r.NewFindings {
			b.WriteString("- [" + finding.Key + "] (" + finding.Severity + "/" + finding.FixKind + ") defect: " +
				finding.Defect + " evidence: " + finding.Evidence +
				" required outcome: " + finding.RequiredOutcome + "\n")
		}
		for _, assessment := range r.Assessments {
			b.WriteString("- [" + assessment.Key + "] " + assessment.Status + ": " + assessment.Evidence + "\n")
		}
		return
	}
	c := in.TypedClear
	b.WriteString("Explicit clear for cycle " + c.CycleID + " on candidate " + c.Head + " at base " + c.BaseOID + ".\n")
	for _, assessment := range c.Assessments {
		b.WriteString("- [" + assessment.Key + "] verified: " + assessment.Evidence + "\n")
	}
}

// Admit aggregate text before joining, quoting or copying it into the prompt.
// The final encoded prompt must also fit the existing transport cap.
func admitPromptText(in PromptInputs) error {
	budget := &promptTextBudget{remaining: project.MaxFactoryPrompt}
	if err := budget.admitFields(in); err != nil {
		return err
	}
	if err := budget.admitSources(in); err != nil {
		return err
	}
	if err := budget.admitChecks(in); err != nil {
		return err
	}
	if err := budget.admitTypedReview(in); err != nil {
		return err
	}
	if err := budget.admitTypedClear(in); err != nil {
		return err
	}
	return budget.admitCheckAssessment(in)
}

// promptTextBudget spends one transport bound across prompt input texts.
type promptTextBudget struct {
	remaining int
}

// add charges one text with its quoting overhead, refusing when the
// bound is exceeded.
func (b *promptTextBudget) add(text string, overhead int) bool {
	if len(text) > b.remaining || overhead > b.remaining-len(text) {
		return false
	}
	b.remaining -= len(text) + overhead
	return true
}

// admit charges a batch of texts with one shared overhead.
func (b *promptTextBudget) admit(texts []string, overhead int) error {
	for _, text := range texts {
		if !b.add(text, overhead) {
			return errors.New("dispatch prompt input exceeds the transport bound")
		}
	}
	return nil
}

// admitFields charges the scalar prompt input fields.
func (b *promptTextBudget) admitFields(in PromptInputs) error {
	return b.admit([]string{
		in.Title, in.Body, in.AcceptanceID, in.TargetBranch, in.SourceCommit,
		in.ApprovedBase, in.BaseCommit, in.PublicationAssignment, in.Preparation, in.RequirementsID,
		in.ApprovalID, in.Harness, in.Model, in.Role, in.ProviderConnection,
	}, 0)
}

// admitSources charges prompt sources and resolutions.
func (b *promptTextBudget) admitSources(in PromptInputs) error {
	for _, sources := range [][]PromptSource{in.Sources, in.Resolutions} {
		for _, source := range sources {
			if !b.add(source.ID, 32) || !b.add(source.Content, 0) {
				return errors.New("dispatch prompt input exceeds the transport bound")
			}
		}
	}
	return nil
}

// admitChecks charges required checks and prerequisites.
func (b *promptTextBudget) admitChecks(in PromptInputs) error {
	for _, check := range in.RequiredChecks {
		if !b.add(check, 2) {
			return errors.New("dispatch prompt input exceeds the transport bound")
		}
	}
	for _, p := range in.Prerequisites {
		if err := b.admit([]string{p.Occurrence, p.DependsOn, p.Outcome, in.Control.EndpointHeads[p.Occurrence], in.Control.Fingerprint, in.Control.Satisfied[p.Occurrence].Acceptance}, 32); err != nil {
			return err
		}
	}
	return nil
}

// admitTypedReview charges the carried typed findings verdict.
func (b *promptTextBudget) admitTypedReview(in PromptInputs) error {
	if r := in.TypedReview; r != nil {
		if !b.add(r.CycleID, 32) {
			return errors.New("dispatch prompt input exceeds the transport bound")
		}
		for _, finding := range r.NewFindings {
			if err := b.admit([]string{finding.Key, finding.Severity, finding.FixKind, finding.Defect, finding.Evidence, finding.RequiredOutcome}, 4); err != nil {
				return err
			}
		}
		for _, assessment := range r.Assessments {
			if err := b.admit([]string{assessment.Key, assessment.Status, assessment.Evidence}, 4); err != nil {
				return err
			}
		}
	}
	return nil
}

// admitTypedClear charges the carried explicit clear verdict.
func (b *promptTextBudget) admitTypedClear(in PromptInputs) error {
	if c := in.TypedClear; c != nil {
		if err := b.admit([]string{c.CycleID, c.Conclusion, c.Head, c.BaseOID, c.CoverageDigest}, 32); err != nil {
			return err
		}
		for _, assessment := range c.Assessments {
			if err := b.admit([]string{assessment.Key, assessment.Status, assessment.Evidence}, 4); err != nil {
				return err
			}
		}
	}
	return nil
}

// admitCheckAssessment charges the recorded check evidence.
func (b *promptTextBudget) admitCheckAssessment(in PromptInputs) error {
	if a := in.CheckAssessment; a != nil {
		if err := b.admit([]string{a.HeadOID, a.BaseOID, a.Verdict, a.Reason}, 32); err != nil {
			return err
		}
		for _, result := range a.Results {
			if !b.add(result.Context, 32) || !b.add(result.State, 8) {
				return errors.New("dispatch prompt input exceeds the transport bound")
			}
		}
	}
	return nil
}
