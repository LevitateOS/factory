package factory

import (
	"errors"
	"strings"

	"github.com/levitateos/sodaos/internal/project"
)

// IntentPlanningOptions names the pre-issue planning operation that derives
// candidate issue graphs from planner outputs. It enters only through the
// decompose phase with an intent subject and the coder role (FW11
// admission); this vocabulary states the operation, it does not admit it.
const IntentPlanningOptions = "decomposition_options"

// SemanticCandidate is one planner-produced candidate issue graph: bounded
// tasks with a rationale. Candidates carry decision-ID task keys only;
// native issue IDs are never chosen by the planner.
type SemanticCandidate struct {
	Key       string       `json:"key"`
	Rationale string       `json:"rationale"`
	Tasks     []IntentTask `json:"tasks"`
}

// SemanticPlannerInput binds one semantic planning run to its exact inputs:
// the admitted operation, the authoritative draft head, the planner outputs
// it consumes (authored proposal content and alternatives), and the source
// investigation the plan was built from. Accepted graphs and history are
// never inputs: planning consumes planner outputs only.
type SemanticPlannerInput struct {
	IssueMaker   *IssueMakerScope `json:",omitempty"`
	Operation    string
	Head         IntentRevision
	Content      ProposalContent
	Alternatives AlternativeSet
	Inspected    []InspectedSource
	// RequireWalkthroughs is recorded in newly assembled native input. Older
	// stored attempts omit it and remain readable as missing product prose.
	RequireWalkthroughs bool
}

// SemanticPlannerOutput is one bounded semantic planning answer: one to
// three assessed candidates, or a decomposition_needs_attention reason with
// no candidates. Attention records the product decision the planner needs;
// it never triggers another batch automatically.
type SemanticPlannerOutput struct {
	Intent                       string                        `json:"intent"`
	Revision                     string                        `json:"revision"`
	Operation                    string                        `json:"operation"`
	Candidates                   []SemanticCandidate           `json:"candidates,omitempty"`
	Attention                    string                        `json:"attention,omitempty"`
	VerificationManifestProposal *VerificationManifestProposal `json:"verification_manifest_proposal,omitempty"`
	Walkthroughs                 *ProposalContent              `json:"walkthroughs,omitempty"`
}

// Validate checks the planner input shape and bindings without running any
// provider: operation vocabulary, draft planning scope, proposal content
// bound to the head, supported alternatives for the options operation, and
// the investigation evidence planning was built from.
func (in SemanticPlannerInput) Validate() error {
	if in.IssueMaker != nil {
		if err := in.IssueMaker.Validate(in.Head); err != nil {
			return err
		}
	}
	if !ValidIntentPlanningOperation(in.Operation) {
		return errors.New("unknown intent planning operation")
	}
	if err := ValidateDraftPlanningScope(in.Head); err != nil {
		return err
	}
	if err := in.Content.Validate(); err != nil {
		return err
	}
	if in.Content.Intent != in.Head.Intent || in.Content.Revision != in.Head.ID {
		return errors.New("proposal content does not bind the planning head")
	}
	if err := in.Alternatives.Validate(); err != nil {
		return err
	}
	if in.Operation == IntentPlanningOptions && !in.Alternatives.Supported {
		return errors.New("decomposition options need supported alternatives")
	}
	if len(in.Inspected) == 0 || len(in.Inspected) > MaxInspectedRefs {
		return errors.New("planning needs its source investigation first")
	}
	for _, ref := range in.Inspected {
		if !validOwnershipEntry(ref.Path) || strings.HasSuffix(ref.Path, "/") {
			return errors.New("inspected source is not an exact file")
		}
		if !ValidCommit(ref.Commit) {
			return errors.New("inspected source lacks its commit")
		}
		// Server-assembled planning input carries the complete approved
		// native file; the separate report/prose bound does not truncate it.
		if ref.Evidence == "" || len(ref.Evidence) > project.MaxFactoryContextFileBytes {
			return errors.New("inspected evidence outside its bound")
		}
	}
	return nil
}

// Validate checks the planner output shape: revision binding, operation
// match, and attention XOR candidates. Candidate task graphs are assessed
// by AssessSemanticOutput against the planning input.
func (out SemanticPlannerOutput) Validate() error {
	if !project.ValidDecisionID(out.Intent) || !project.ValidDecisionID(out.Revision) {
		return errors.New("invalid semantic output identity")
	}
	if !ValidIntentPlanningOperation(out.Operation) {
		return errors.New("unknown intent planning operation")
	}
	if out.Attention != "" {
		if len(out.Candidates) != 0 || out.VerificationManifestProposal != nil || out.Walkthroughs != nil {
			return errors.New("attention carries no new candidates")
		}
		if strings.TrimSpace(out.Attention) == "" || len(out.Attention) > 4000 {
			return errors.New("invalid attention reason")
		}
		return nil
	}
	if len(out.Candidates) == 0 || len(out.Candidates) > 3 {
		return errors.New("semantic output needs one to three candidates")
	}
	seen := map[string]bool{}
	for _, candidate := range out.Candidates {
		if strings.TrimSpace(candidate.Key) == "" || len(candidate.Key) > 200 || seen[candidate.Key] {
			return errors.New("invalid semantic candidate identity")
		}
		seen[candidate.Key] = true
		if !intentText(candidate.Rationale, 4000) {
			return errors.New("invalid semantic candidate rationale")
		}
	}
	return nil
}

// AssessSemanticOutput validates one planner answer against the exact input
// that produced it: revision/operation binding, then every candidate task
// graph against the draft head. Broad multi-task graphs and single-task
// graphs both assess; uncovered authorized outcomes, false dependencies,
// revision changes and over-broad answers refuse. Attention outputs assess
// without candidates. Assessment is pure: replaying the same input and
// output reaches the same verdict and spends nothing.
func AssessSemanticOutput(in SemanticPlannerInput, out SemanticPlannerOutput) error {
	if err := in.Validate(); err != nil {
		return err
	}
	if err := out.Validate(); err != nil {
		return err
	}
	if out.Intent != in.Head.Intent || out.Revision != in.Head.ID || out.Operation != in.Operation {
		return errors.New("semantic output does not bind its planning input")
	}
	if out.Attention != "" {
		return nil
	}
	if in.RequireWalkthroughs && out.Walkthroughs == nil {
		return errors.New("completed planning must author its product walkthroughs")
	}
	if out.Walkthroughs != nil {
		if err := ValidatePlanningWalkthroughs(in.Head, *out.Walkthroughs); err != nil {
			return err
		}
	}
	if out.VerificationManifestProposal != nil {
		if err := out.VerificationManifestProposal.ValidateOutcomes(in.Head); err != nil {
			return err
		}
	}
	for _, candidate := range out.Candidates {
		if err := AssessProposedTasks(in.Head, candidate.Tasks, in.Head.MaxIssues); err != nil {
			return err
		}
	}
	return nil
}
