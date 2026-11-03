package control

import (
	"context"
	"errors"
	"fmt"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

// Pre-issue semantic planning errors. Exhaustion and cancellation stay
// visible to the caller; a missing funded capability is a precise external
// gate, never a substitute output.
var (
	ErrIntentPlanningExhausted   = errors.New("intent planning allowance is exhausted")
	ErrIntentPlanningExternal    = errors.New("funded semantic planning capability is unavailable")
	ErrIntentPlanningCancelled   = errors.New("intent planning was cancelled")
	ErrIntentPlanningInvestigate = errors.New("intent planning needs source investigation first")
	ErrIntentPlanningAttention   = errors.New("intent planning needs a product decision first")
	ErrIntentPlanningAmbiguous   = errors.New("intent planning needs a funded single choice")
	ErrIntentPlanningPromptBound = errors.New("staged intent prompt is over its bound")
)

// SemanticPlanner produces candidate issue graphs from planner outputs.
// The funded implementation calls the enrolled model account; tests and
// admission use deterministic stubs. A nil planner means no funded
// account or capability is enrolled, which is an explicit external gate.
type SemanticPlanner interface {
	PlanSemantic(ctx context.Context, in factory.SemanticPlannerInput) (factory.SemanticPlannerOutput, error)
}

// AdmitIntentPlanning is the pure FW11 decompose admission for pre-issue
// semantic planning. Checks run in fixed order so the same state reports
// the same reason: subject shape, decompose phase with the coder role,
// revision/owner/scope binding against the authoritative head, draft
// planning scope, repository association, then planner input (including
// its source investigation) and finite allowance. No Store, Host or Graph
// is touched: planning launches no repository session, writes no native
// state, invents no fake issue and grants no publication capability. The dispatch boundary loads
// the head and allowance availability before calling; recording the
// assignment and spending the allowance stay with that boundary (FW11
// dispatch files), not this handler.
func AdmitIntentPlanning(subject factory.ExecutionSubject, phase, role string, head factory.IntentRevision, allowanceAvailable bool, in factory.SemanticPlannerInput) error {
	if err := subject.Validate(); err != nil {
		return err
	}
	if subject.Kind != factory.SubjectIntent || subject.Intent == nil {
		return errors.New("intent planning needs its intent subject")
	}
	if phase != project.ExecutionDecompose || !factory.SubjectKindAllowed(subject.Kind, phase) {
		return errors.New("intent planning enters only through decompose")
	}
	if !factory.ValidPhaseRole(phase, role) {
		return errors.New("intent planning needs the coder role")
	}
	if subject.Intent.IntentID != head.Intent || subject.Intent.RevisionID != head.ID {
		return store.ErrStaleRevision
	}
	if subject.Intent.OwnerID != head.Owner || head.Owner <= 0 {
		return errors.New("intent planning owner mismatch")
	}
	if subject.Intent.ScopeDigest != head.Digest() {
		return store.ErrStaleRevision
	}
	if err := factory.ValidateDraftPlanningScope(head); err != nil {
		return err
	}
	// Pre-issue planning runs only after repository association: an
	// unassociated draft stays a private editable draft and cannot enter
	// the generative run. Full repository authority stays with the FW11
	// dispatch packet boundary; this check keeps the planner
	// repository-bound without consulting any accepted graph or history.
	if head.Repository <= 0 {
		return errors.New("intent planning needs its repository association")
	}
	// Investigation before planning is enforced ahead of other input
	// checks so missing inspection surfaces distinctly and directs work
	// to source inspection rather than another planning attempt.
	if len(in.Inspected) == 0 {
		return ErrIntentPlanningInvestigate
	}
	if err := in.Validate(); err != nil {
		return err
	}
	if in.Head.Digest() != head.Digest() {
		return store.ErrStaleRevision
	}
	if !allowanceAvailable {
		return ErrIntentPlanningExhausted
	}
	return nil
}

// ChosenIntentPlan is the single planner candidate the Generate work command
// adopts exactly. It binds the assessed planning input (intent, revision,
// operation) to the one chosen candidate key and its task graph. Native
// adoption consumes these tasks verbatim: no second triplet judgment or
// template restructuring rewrites them on the serving path.
type ChosenIntentPlan struct {
	Intent    string
	Revision  string
	Operation string
	Key       string
	Tasks     []factory.IntentTask
	// Attempt is the recorded planning assignment the choice was
	// resolved from. Empty on hand-built choices; ResolveAcceptedPlanChoice
	// always stamps it, and generation binds it to the operation.
	Attempt                      string
	VerificationManifestProposal *factory.VerificationManifestProposal
	VerificationManifest         *factory.AdoptedVerificationManifest
}

// ChooseSingleCandidate reduces one planner answer to its single adopted
// candidate. Assessment is pure and re-runs here, so replaying the same
// input and output reaches the same choice and spends nothing. An attention
// answer carries no candidates and reports ErrIntentPlanningAttention: the
// named product decision needs human review, not another automatic batch.
// A multi-candidate answer reports ErrIntentPlanningAmbiguous: choosing
// among semantic alternatives needs funded judgment, and silently taking
// the first candidate would invent authority.
func ChooseSingleCandidate(in factory.SemanticPlannerInput, out factory.SemanticPlannerOutput) (ChosenIntentPlan, error) {
	if err := factory.AssessSemanticOutput(in, out); err != nil {
		return ChosenIntentPlan{}, err
	}
	if out.Attention != "" {
		return ChosenIntentPlan{}, fmt.Errorf("%w: %s", ErrIntentPlanningAttention, out.Attention)
	}
	if len(out.Candidates) != 1 {
		return ChosenIntentPlan{}, fmt.Errorf("%w: planner offered %d candidates", ErrIntentPlanningAmbiguous, len(out.Candidates))
	}
	candidate := out.Candidates[0]
	chosen := ChosenIntentPlan{
		Intent: in.Head.Intent, Revision: in.Head.ID, Operation: in.Operation,
		Key: candidate.Key, Tasks: append([]factory.IntentTask(nil), candidate.Tasks...),
		VerificationManifestProposal: out.VerificationManifestProposal,
	}
	if chosen.Key == "" {
		return ChosenIntentPlan{}, errors.New("chosen planner candidate lacks its key")
	}
	return chosen, nil
}

// plannedChoiceRubric judges planner-produced candidates against the
// accepted product direction. Every candidate already preserves accepted
// outcomes, idea bindings and bounds (assessed); choose the task
// boundaries and prerequisite edges that best remove overlap or
// fragmentation without changing approved outcomes. Judge only supplied
// facts; grant no authority, access or spending.
const plannedChoiceRubric = "Compare the supplied planner candidates against the accepted product direction. Every candidate preserves accepted outcomes, idea bindings and bounds; choose the task boundaries and prerequisite edges that best remove overlap or fragmentation. Judge only supplied facts; grant no authority, access or spending."

// ChoosePlannedCandidate resolves one planner answer to its adopted
// candidate at serve time, against the current accepted head. A single
// candidate adopts directly with no comparative call; multiple
// alternatives resolve through exactly one finite funded choice among the
// planner's own candidates — reserved against the planning allowance,
// fingerprinted and replay-cached, so resumes spend nothing further. An
// uncertain or failed funded choice refuses visibly instead of guessing,
// and attention stays visible instead of triggering another batch. The
// returned choice binds the planning input head (usually the reviewed
// draft); the accepted successor adopts it as its predecessor.
func (c *Coordinator) ChoosePlannedCandidate(ctx context.Context, r factory.IntentRevision, in factory.SemanticPlannerInput, out factory.SemanticPlannerOutput) (ChosenIntentPlan, error) {
	if err := r.Validate(); err != nil {
		return ChosenIntentPlan{}, err
	}
	if r.State != "accepted" {
		return ChosenIntentPlan{}, errors.New("planned choice serves the accepted direction")
	}
	if err := factory.AssessSemanticOutput(in, out); err != nil {
		return ChosenIntentPlan{}, err
	}
	if out.Attention != "" {
		return ChosenIntentPlan{}, fmt.Errorf("%w: %s", ErrIntentPlanningAttention, out.Attention)
	}
	if len(out.Candidates) == 1 {
		return ChooseSingleCandidate(in, out)
	}
	if len(out.Candidates) == 0 {
		return ChosenIntentPlan{}, fmt.Errorf("%w: planner offered no candidates", ErrIntentPlanningAmbiguous)
	}
	byKey := map[string]factory.SemanticCandidate{}
	options := make(map[string]any, len(out.Candidates))
	for _, candidate := range out.Candidates {
		byKey[candidate.Key] = candidate
		options[candidate.Key] = candidate.Rationale
	}
	type choiceEvidence struct {
		Current    factory.IntentRevision      `json:"current"`
		Candidates []factory.SemanticCandidate `json:"candidates"`
	}
	state := choiceEvidence{Current: effectiveDecompositionScope(r), Candidates: out.Candidates}
	answer, _, err := c.AdvisePurpose(ctx, PurposeAdvice{
		Purpose:  factory.JudgmentDecomposition,
		State:    state,
		Question: plannedChoiceRubric,
		Options:  options,
		Outcome:  "planned-choice",
		Admit: func(ctx context.Context, snapshot string) (bool, error) {
			return true, c.Store.CheckIntentJudgment(ctx, r)
		},
	})
	if err != nil {
		return ChosenIntentPlan{}, err
	}
	if answer.Confidence < 0.5 {
		return ChosenIntentPlan{}, fmt.Errorf("%w: funded choice is uncertain", ErrIntentPlanningAmbiguous)
	}
	candidate, ok := byKey[answer.Selected]
	if !ok {
		return ChosenIntentPlan{}, errors.New("funded choice selected an unknown candidate")
	}
	return ChosenIntentPlan{
		Intent: in.Head.Intent, Revision: in.Head.ID, Operation: in.Operation,
		Key: candidate.Key, Tasks: append([]factory.IntentTask(nil), candidate.Tasks...),
		VerificationManifestProposal: out.VerificationManifestProposal,
	}, nil
}
