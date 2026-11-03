package factory

import (
	"encoding/json"
	"errors"

	"github.com/levitateos/sodaos/internal/project"
)

// AcceptedContext retains structured bytes already verified by dispatch against
// the immutable acceptance. It is part of the atomic assignment packet, never
// reconstructed from the rendered prompt or refreshed from changed native text.
type AcceptedContext struct {
	AcceptanceID      string                             `json:"acceptance_id"`
	Repository        int64                              `json:"repository"`
	Issue             int64                              `json:"issue"`
	NativeRevision    int64                              `json:"native_revision"`
	SourceCommit      string                             `json:"source_commit"`
	Preparation       string                             `json:"preparation"`
	Title             string                             `json:"title"`
	Body              string                             `json:"body"`
	Sources           []AcceptedContextSource            `json:"sources"`
	Resolutions       []AcceptedContextSource            `json:"resolutions"`
	RepositoryContext *project.FactoryPreparationContext `json:"repository_context"`
	RequiredChecks    []string                           `json:"required_checks"`
	Prerequisites     []AcceptedPrerequisite             `json:"prerequisites"`
	PrerequisiteHeads map[string]string                  `json:"prerequisite_heads"`
	Satisfied         map[string]PrereqSatisfaction      `json:"satisfied"`
	PlanPrerequisites []string                           `json:"plan_prerequisites"`
	CheckAssessment   *CheckAssessment                   `json:"check_assessment,omitempty"`
}

type AcceptedContextSource struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

func CaptureAcceptedContext(in PromptInputs) *AcceptedContext {
	sources := make([]AcceptedContextSource, 0, len(in.Sources))
	for _, source := range in.Sources {
		sources = append(sources, AcceptedContextSource{ID: source.ID, Content: source.Content})
	}
	resolutions := make([]AcceptedContextSource, 0, len(in.Resolutions))
	for _, source := range in.Resolutions {
		resolutions = append(resolutions, AcceptedContextSource{ID: source.ID, Content: source.Content})
	}
	heads, satisfied := map[string]string{}, map[string]PrereqSatisfaction{}
	for key, value := range in.Control.EndpointHeads {
		heads[key] = value
	}
	for key, value := range in.Control.Satisfied {
		satisfied[key] = value
	}
	requiredChecks := append([]string{}, in.RequiredChecks...)
	prerequisites := append([]AcceptedPrerequisite{}, in.Prerequisites...)
	planPrerequisites := append([]string{}, in.PlanPrerequisites...)
	return &AcceptedContext{AcceptanceID: in.AcceptanceID, Repository: in.Repository, Issue: in.Issue, NativeRevision: in.NativeRev, SourceCommit: in.SourceCommit, Preparation: in.Preparation, Title: in.Title, Body: in.Body, Sources: sources, Resolutions: resolutions, RepositoryContext: in.RepositoryContext, RequiredChecks: requiredChecks, Prerequisites: prerequisites, PrerequisiteHeads: heads, Satisfied: satisfied, PlanPrerequisites: planPrerequisites, CheckAssessment: in.CheckAssessment}
}

func (c AcceptedContext) ValidateFor(a Assignment) error {
	if c.AcceptanceID != a.Acceptance || c.Repository != a.Repository || c.Issue != a.Issue || c.NativeRevision <= 0 || c.NativeRevision > a.NativeRev || c.SourceCommit != a.SourceCommit || c.Preparation != a.Preparation || c.Title == "" || len(c.Sources) > MaxAcceptedSources || len(c.Resolutions) > MaxAcceptedSources {
		return errors.New("accepted context differs from assignment")
	}
	if c.RepositoryContext != nil && (c.RepositoryContext.Project != a.ProjectID || c.RepositoryContext.ID != a.Preparation || c.RepositoryContext.Candidate != a.SourceCommit || c.RepositoryContext.ValidateContent(a.Role) != nil) {
		return errors.New("accepted repository context differs from assignment")
	}
	raw, err := json.Marshal(c)
	if err != nil || len(raw) > MaxEncodedPlan {
		return errors.New("accepted context exceeds its bound")
	}
	return nil
}
