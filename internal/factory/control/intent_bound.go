package control

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
)

// ErrIntentBoundTransition reports an intent-bound run attempting to leave
// its admitted decompose phase. An admitted intent plan never becomes
// implemented work here: native graph writes happen only through FW09
// adoption of an accepted revision, and D13 completion joints stay with
// FW30. The caller revises the product direction or adopts the accepted
// graph instead of transitioning this run.
var ErrIntentBoundTransition = errors.New("intent-bound planning cannot transition to implementation")

// AdmitIntentBoundLaunch is the FW06B FW11 admission joint for intent-bound
// pre-issue planning launches. It binds the planning operation to the FW11
// subject/phase/role triple before delegating to AdmitIntentPlanning for
// head binding, draft scope, repository association, investigation and
// allowance. Only the ProposeIntent and DecompositionOptions operations
// launch; no accepted graph or history is consulted at any step, and none
// is taken: the signature carries no Store, Host or Graph dependency, so
// this boundary launches no repository session and grants no publication
// capability. Recording the plan-intent command, the assignment and root
// allowance, the reservation and the run stays with the FW11 dispatch
// packet boundary; the staged launch rides the existing host path through
// StageIntentBoundLaunch.
func AdmitIntentBoundLaunch(subject factory.ExecutionSubject, phase, role, operation string, head factory.IntentRevision, allowanceAvailable bool, in factory.SemanticPlannerInput) error {
	if !factory.ValidIntentPlanningOperation(operation) {
		return errors.New("unknown intent planning operation")
	}
	if in.Operation != operation {
		return errors.New("planner inputs do not bind the launched operation")
	}
	if phase != project.ExecutionDecompose {
		return ErrIntentBoundTransition
	}
	return AdmitIntentPlanning(subject, phase, role, head, allowanceAvailable, in)
}

// AdmitIntentBoundResult gates one intent-bound decompose answer at the
// FW11 result boundary. The subject must still be the admitted intent kind
// and the phase still decompose: an allowed planning run never transitions
// to implemented work through its result. Semantic assessment stays
// planner-outputs-only through factory.AssessSemanticOutput, so broad
// multi-task graphs and small single-task graphs both admit, uncovered
// authorized outcomes and false dependencies refuse, and an attention
// answer admits with no candidates and triggers no further batch. Results
// spend no allowance; exhaustion and Retry stay with the launch boundary.
func AdmitIntentBoundResult(subject factory.ExecutionSubject, phase string, in factory.SemanticPlannerInput, out factory.SemanticPlannerOutput) error {
	if subject.Kind != factory.SubjectIntent || phase != project.ExecutionDecompose {
		return ErrIntentBoundTransition
	}
	if out.Operation != in.Operation {
		return errors.New("semantic output does not bind its planning input")
	}
	return factory.AssessSemanticOutput(in, out)
}

// StageIntentPlanningPrompt renders the caller-staged prompt for one
// admitted intent-bound decompose run from its complete planning input:
// the operation, the head digest the subject binds, and the full typed
// input — composed content with corrections and declined meanings,
// alternatives, effective scope and inspected sources — closed by the
// decompose report contract. A digest alone cannot communicate reviewed
// corrections or rejected behavior absent from the objective, so the
// prompt preserves the exact input serialization; the prompt carries
// evidence references, never credentials, and stays within the factory
// prompt bound. Anything larger refuses instead of truncating
// requirements.
func StageIntentPlanningPrompt(head factory.IntentRevision, in factory.SemanticPlannerInput) ([]byte, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	if in.Head.Digest() != head.Digest() {
		return nil, errors.New("planning prompt answers another head")
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("## Intent planning\n\n")
	b.WriteString("Operation: " + in.Operation + "\n")
	b.WriteString("Intent: " + head.Intent + "\n")
	b.WriteString("Revision: " + head.ID + "\n")
	b.WriteString("Head digest: " + head.Digest() + "\n")
	// The resolved scope and task bound are stated outright: deriving them
	// from the Head JSON is where planners slip (accepted assumptions are
	// in scope and every task graph must cite them).
	if resolved := factory.ResolvedScope(in.Head); len(resolved) > 0 {
		b.WriteString("Authorized idea IDs: " + strings.Join(resolved, ", ") + "\n")
	}
	b.WriteString("Tasks per candidate: at most " + strconv.Itoa(in.Head.MaxIssues) + "\n")
	b.WriteString("\n## Admitted planning input\n\n")
	b.WriteString(string(encoded) + "\n")
	b.WriteString(phasedReportContract(project.ExecutionDecompose))
	if in.IssueMaker != nil {
		b.WriteString("\n## Issue Maker assignment\n\nYou are Issue Maker, a dedicated intake role. The original request is already tracked as native issue #" + strconv.FormatInt(in.IssueMaker.Issue, 10) + " (key request). Your own progress post is " + in.IssueMaker.Post + ". Refine that issue with factory.refine_issue, using key request, title and body including concrete acceptance criteria. If this request needs additional distinct issues, create each immediately with factory.create_issue using a stable lowercase key, title and body; do not wait for the entire plan to finish. Each native receipt publishes one comment under your post. Never create another issue for the same deliverable or implement code. Your tools grant no implementation, deployment or accepted product changes. Post useful progress as replies under your progress post. Finish with exactly one candidate and one task describing the original requested outcome; this result refines the initial request issue, it does not create a second copy. Product walkthrough alternatives are unnecessary for this intake assignment.\n")
	} else {
		b.WriteString("\n## Application delivery contract\n\n" + factory.ApplicationBundleGuidance + "\n")
		b.WriteString("\n## Product walkthrough authored by this planning agent\n\n")
		b.WriteString("For a completed intent plan, also send walkthroughs to factory.finish_product_plan. This is your authored product explanation, not prose generated by JEV. Use the person's actual actors, actions, records, decisions and visible results from the admitted objective and authorized ideas; do not copy the generic starter alternatives. Offer exactly two coherent experience alternatives within the same authorized scope. Each must describe the complete useful path and meaningful failure/rejection branches. Keep unknown names or policies explicit rather than inventing facts.\n")
		b.WriteString("walkthroughs has the ProposalContent shape: producer must be soda-planning-agent-v1; supported true; copy intent, revision, objective, facts, excluded and exclusions exactly from the admitted Content. Write unknowns as 1–8 bounded statements. Each alternative has key (lowercase letters/digits/underscore, <=64), name, audience, summary, visibility, consequences, evidence, steps (1–8), outcomes (1–20), and idea_ids. Each step has actor, action, result and optional branches [{outcome,result}] (up to four). Copy the admitted objective and facts exactly: the objective runs to 16000 characters and each fact runs to 16000, with facts[0] byte-equal to the objective — never shorten, truncate or substitute them. Every other prose field you author (unknowns, exclusion meanings, names, summaries, steps, branches and outcomes after the first) stays <=4000 characters each. Outcomes[0] is the review label for the objective: the objective verbatim when it fits in 4000 UTF-8 bytes, else exactly 'Implement the recorded product objective'. Each alternative's idea_ids contains exactly the resolved scope derived from Head (the explicit nonempty EffectiveOutcomes verbatim, else the accept/edit idea IDs in revision order), with no duplicates: the Authorized idea IDs line above. Outcomes then name observable product results. Evidence names the actual admitted source/intent basis, not fabricated research. This content proposes experience only; it grants no new integrations, access, expenditure or publication. JEV comparison and consistency checking occur through the explicit review command before acceptance.\n")
		b.WriteString("\n## Product plan finish arguments\n\n")
		b.WriteString(FinishProductPlanArgsGuidance + "\n")
	}
	prompt := []byte(b.String())
	if len(prompt) == 0 {
		return nil, errors.New("staged intent prompt is empty")
	}
	// The bound rides the admitted decompose phase policy, so staging
	// can never drift from the launch wire or prompt entry: decompose
	// admits only intent subjects, hence this headroom is exactly
	// intent-scoped.
	if len(prompt) > project.MaxPromptForPhase(project.ExecutionDecompose) {
		return nil, ErrIntentPlanningPromptBound
	}
	return prompt, nil
}

// FinishProductPlanArgsGuidance tells a Product Lead worker which
// top-level finish arguments settle: intent, revision, operation,
// objective and facts already bind from the staged planning input,
// so those immutable identity/facts fields are the ONLY top-level
// fields the worker must never send. Every documented carrier stays
// admissible: required candidates, coverage and evidence_refs plus
// optional walkthroughs, questions and verification_manifest_proposal.
// Unknown top-level fields are refused before
// settlement, so an inlined identity/facts field fails instead of
// settling no matter how small it is.
const FinishProductPlanArgsGuidance = "Send only candidates, coverage, evidence_refs, walkthroughs, questions and verification_manifest_proposal as finish arguments: intent, revision, operation, objective and facts already bind from the staged planning input, so never send intent, revision, operation, objective or facts as top-level finish arguments — those immutable top-level fields are refused before settlement no matter how small they are. Send candidates as an array of JSON objects and walkthroughs as one JSON object carrying the exact nested objective and facts from the admitted Content (facts[0] byte-equal to the objective; objective and each fact to 16000 characters) — never as strings. Optional carriers questions and verification_manifest_proposal stay admissible alongside the required fields. Authored walkthrough prose stays within its own <=4000-character bounds; those bounds never shorten the preserved objective or facts."

// stagedPlanningInputMarker opens the exact typed planning input the
// staged prompt preserves; the shared phased report marker closes it.
const stagedPlanningInputMarker = "## Admitted planning input\n\n"

// ParseStagedPlanningInput recovers the exact typed input a staged
// prompt recorded. Prompts without the marker predate staged inputs
// and report unstaged; a marker without a parseable input reports
// corrupt.
func ParseStagedPlanningInput(prompt []byte) (in factory.SemanticPlannerInput, staged bool, err error) {
	text := string(prompt)
	start := strings.Index(text, stagedPlanningInputMarker)
	if start < 0 {
		return in, false, nil
	}
	rest := text[start+len(stagedPlanningInputMarker):]
	end := strings.Index(rest, phasedReportMarker)
	if end < 0 {
		return in, true, errors.New("staged planning input lacks its report close")
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(rest[:end])), &in); err != nil {
		return in, true, err
	}
	return in, true, nil
}

// IntentBoundLaunchInput carries the dispatch-verified identities one
// admitted intent-bound decompose run launches under. Every identity was
// verified at admission; this joint only binds them to the existing host
// launch shape.
type IntentBoundLaunchInput struct {
	Prompt        []byte
	Deadline      time.Time
	RunID         string
	ProjectID     string
	PreparationID string
	Harness       string
	HarnessVers   string
	Model         string
	PromptSHA     string
	SourceCommit  string
	Connection    string
	HarnessSHA256 string
	ActorID       int64
}

// StageIntentBoundLaunch binds one admitted intent-bound decompose run to
// the existing host launch path. The launch pins the coder role and the
// decompose phase with no execution manifest: planning phases carry no
// native publication or mutation capability, and implement runs stage
// their manifest through their own owning boundary. Validation here
// refuses construction drift before any host call.
func StageIntentBoundLaunch(subject factory.ExecutionSubject, in IntentBoundLaunchInput) (project.FactoryLaunch, error) {
	if subject.Kind != factory.SubjectIntent || subject.Intent == nil {
		return project.FactoryLaunch{}, ErrIntentBoundTransition
	}
	launch := project.FactoryLaunch{
		Run: project.FactoryRun{
			ID: in.RunID, Project: in.ProjectID, Role: project.RoleCoder,
			Preparation: in.PreparationID,
			Harness:     in.Harness, HarnessVers: in.HarnessVers,
			Model:      in.Model,
			Assignment: in.PromptSHA, SourceCommit: in.SourceCommit,
			Connection: in.Connection, Actor: in.ActorID,
			Deadline: in.Deadline, ExecutionPhase: project.ExecutionDecompose,
		},
		Prompt: in.Prompt, HarnessSHA256: in.HarnessSHA256,
	}
	if err := launch.Validate(); err != nil {
		return project.FactoryLaunch{}, err
	}
	if launch.Run.ExecutionPhase != project.ExecutionDecompose || launch.Run.Role != project.RoleCoder || len(launch.ExecutionManifest) != 0 {
		return project.FactoryLaunch{}, ErrIntentBoundTransition
	}
	return launch, nil
}
