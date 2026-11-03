package factory

import (
	"encoding/json"
	"errors"

	"github.com/levitateos/sodaos/internal/project"
)

// CurrentPlanningContext retains the exact admitted planner input. Owner and
// actor identities stay in the assignment's protected input, outside this DTO.
type CurrentPlanningContext struct {
	IssueMaker   *IssueMakerScope  `json:"issue_maker,omitempty"`
	Operation    string            `json:"operation"`
	Content      ProposalContent   `json:"content"`
	Alternatives AlternativeSet    `json:"alternatives"`
	Inspected    []InspectedSource `json:"inspected"`
}

func (in SemanticPlannerInput) ValidateAssignment(a Assignment) error {
	if in.Validate() != nil || a.Subject.Intent == nil || in.Head.Intent != a.Subject.Intent.IntentID || in.Head.ID != a.Subject.Intent.RevisionID || in.Head.Digest() != a.Subject.Intent.ScopeDigest || in.Head.Owner != a.ActorID || in.Head.Repository != a.Repository || a.Phase != "decompose" {
		return errors.New("planning context differs from admitted assignment")
	}
	for _, ref := range in.Inspected {
		if ref.Commit != a.SourceCommit {
			return errors.New("planning context source differs from admitted assignment")
		}
	}
	// The encoded planning input persists on the assignment row beside
	// the staged prompt. This validator already admits only
	// decompose-phase intent-subject assignments, so the bound rides the
	// admitted decompose phase policy exactly like staging: the 128 KiB
	// intent-decompose headroom, never the other-phase bound.
	bound := MaxEncodedPlan
	if a.Phase == project.ExecutionDecompose && a.Subject.Kind == SubjectIntent && a.Subject.Intent != nil {
		bound = project.MaxDecomposePrompt
	}
	raw, err := json.Marshal(in)
	if err != nil || len(raw) > bound {
		return errors.New("planning context exceeds its bound")
	}
	return nil
}

func (in SemanticPlannerInput) CurrentContext() *CurrentPlanningContext {
	return &CurrentPlanningContext{IssueMaker: in.IssueMaker, Operation: in.Operation, Content: in.Content, Alternatives: in.Alternatives, Inspected: in.Inspected}
}
