package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// InspectedSource binds one examined repository path to the exact commit
// observed and the evidence taken from it.
type InspectedSource struct {
	Path     string `json:"path"`
	Commit   string `json:"commit"`
	Evidence string `json:"evidence"`
}

// RejectedAlternative records one considered approach and why it lost.
type RejectedAlternative struct {
	Approach string `json:"approach"`
	Reason   string `json:"reason"`
}

// InvestigationPlan is the immutable investigated plan revision consumed by
// one implementation run: accepted scope and input digests, source and
// prerequisite identities, inspected evidence, findings, approach,
// considered alternatives, acceptance coverage, and the validated
// task/lane/check graph with its canonical semantic digest. Listed
// unresolved facts are mandatory: Executable refuses while any remain.
type InvestigationPlan struct {
	ID                 string                `json:"id"`
	AttemptRoot        string                `json:"attempt_root"`
	ScopeDigest        string                `json:"scope_digest"`
	InputDigest        string                `json:"input_digest"`
	SourceCommit       string                `json:"source_commit"`
	IntegrationCommit  string                `json:"integration_commit"`
	Preparation        string                `json:"preparation"`
	Approach           string                `json:"approach"`
	Digest             string                `json:"digest"`
	PrerequisiteDigest []string              `json:"prerequisite_digests,omitempty"`
	Inspected          []InspectedSource     `json:"inspected"`
	Findings           []string              `json:"findings"`
	Unresolved         []string              `json:"unresolved_facts,omitempty"`
	Rejected           []RejectedAlternative `json:"rejected_alternatives,omitempty"`
	AcceptanceCoverage []string              `json:"acceptance_coverage"`
	Tasks              []PlanTask            `json:"tasks"`
	Lanes              []PlanLane            `json:"lanes"`
	Checks             []CheckSpec           `json:"checks"`
	Generation         int                   `json:"generation"`
}

// PlanDigest returns the canonical semantic digest over plan content. The
// stored Digest must equal this value; FW07 persists revisions keyed by
// (attempt_root, plan_id) with a separate current pointer, so the original
// consumed plan always survives replacement.
func PlanDigest(plan InvestigationPlan) (string, error) {
	plan.Digest = ""
	data, err := json.Marshal(struct {
		Version string
		Plan    InvestigationPlan
	}{"investigation-plan-v1", plan})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Validate checks the plan revision shape, bounds and graph. It does not
// admit the plan for coding: Executable additionally requires every
// mandatory fact resolved, and FW12 revalidates source, prerequisite and
// preparation bindings against current state before launch.
func (p InvestigationPlan) Validate() error {
	if err := p.validatePlanIdentity(); err != nil {
		return err
	}
	if err := p.validatePlanPrerequisites(); err != nil {
		return err
	}
	if err := p.validateInspectedRefs(); err != nil {
		return err
	}
	if err := p.validatePlanTextBounds(); err != nil {
		return err
	}
	if err := ValidatePlanGraph(p.Tasks, p.Lanes, p.Checks); err != nil {
		return err
	}
	digest, err := PlanDigest(p)
	if err != nil || digest != p.Digest {
		return errors.New("plan digest does not match its content")
	}
	encoded, err := json.Marshal(p)
	if err != nil || len(encoded) > MaxEncodedPlan {
		return errors.New("encoded plan exceeds its bound")
	}
	return nil
}

// validatePlanIdentity refuses plans outside the admitted identity,
// digests and source commits.
func (p InvestigationPlan) validatePlanIdentity() error {
	if !ValidID(p.ID) || !ValidID(p.AttemptRoot) {
		return errors.New("invalid plan identity")
	}
	if p.Generation < 0 {
		return errors.New("invalid plan generation")
	}
	if !ValidDigest(p.ScopeDigest) || !ValidDigest(p.InputDigest) {
		return errors.New("invalid plan scope or input digest")
	}
	if !ValidCommit(p.SourceCommit) || !ValidCommit(p.IntegrationCommit) {
		return errors.New("invalid plan source identity")
	}
	return nil
}

// validatePlanPrerequisites refuses plans with malformed prerequisite
// digests or preparation references.
func (p InvestigationPlan) validatePlanPrerequisites() error {
	for _, digest := range p.PrerequisiteDigest {
		if !ValidDigest(digest) {
			return errors.New("invalid plan prerequisite digest")
		}
	}
	if !ValidPreparationRef(p.Preparation) {
		return errors.New("invalid plan preparation")
	}
	return nil
}

// validateInspectedRefs refuses plans whose inspected sources are not
// exact committed files with bounded evidence.
func (p InvestigationPlan) validateInspectedRefs() error {
	if len(p.Inspected) > MaxInspectedRefs {
		return errors.New("plan inspects too many sources")
	}
	for _, ref := range p.Inspected {
		if !validOwnershipEntry(ref.Path) || strings.HasSuffix(ref.Path, "/") {
			return errors.New("inspected source is not an exact file")
		}
		if !ValidCommit(ref.Commit) {
			return errors.New("inspected source lacks its commit")
		}
		if ref.Evidence == "" || len(ref.Evidence) > MaxPlanTextBytes {
			return errors.New("inspected evidence outside its bound")
		}
	}
	return nil
}

// validatePlanTextBounds refuses plans with unbounded findings,
// approach, rejected alternatives or acceptance coverage.
func (p InvestigationPlan) validatePlanTextBounds() error {
	for _, text := range append(append([]string(nil), p.Findings...), p.Unresolved...) {
		if text == "" || len(text) > MaxPlanTextBytes {
			return errors.New("plan finding outside its bound")
		}
	}
	if p.Approach == "" || len(p.Approach) > MaxPlanTextBytes {
		return errors.New("plan approach outside its bound")
	}
	for _, alt := range p.Rejected {
		if alt.Approach == "" || len(alt.Approach) > MaxPlanTextBytes || alt.Reason == "" || len(alt.Reason) > MaxPlanTextBytes {
			return errors.New("rejected alternative outside its bound")
		}
	}
	if len(p.AcceptanceCoverage) == 0 {
		return errors.New("plan lacks acceptance coverage")
	}
	for _, text := range p.AcceptanceCoverage {
		if text == "" || len(text) > MaxPlanTextBytes {
			return errors.New("plan acceptance coverage outside its bound")
		}
	}
	return nil
}

// Executable reports whether the validated plan may launch implementation.
// Stale source, prerequisite or preparation bindings are rechecked by FW12
// admission; this gate covers the plan's own completeness.
func (p InvestigationPlan) Executable() error {
	if err := p.Validate(); err != nil {
		return err
	}
	if len(p.Unresolved) != 0 {
		return errors.New("plan carries unresolved mandatory facts")
	}
	return nil
}
