package service

import (
	"context"
	"errors"
	"unicode/utf8"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

var ErrWorkspaceSetupInput = errors.New("invalid reviewed workspace setup input")

// WorkspaceSetup is the exact source and editable recipe presented for owner
// review. Observations report actual native work separately from that consent.
type WorkspaceSetup struct {
	Available           bool                        `json:"available"`
	Reason              string                      `json:"reason,omitempty"`
	Source              string                      `json:"source,omitempty"`
	Target              string                      `json:"target,omitempty"`
	Files               map[string]string           `json:"files"`
	SourceFiles         map[string]string           `json:"source_files"`
	SourceInputs        map[string]string           `json:"source_inputs"`
	SelectedPaths       []string                    `json:"selected_paths"`
	MissingPaths        []string                    `json:"missing_paths"`
	Tools               []string                    `json:"tools"`
	SavedRecipe         bool                        `json:"saved_recipe"`
	Requirement         string                      `json:"requirement"`
	Approval            string                      `json:"approval"`
	EnvironmentRevision int64                       `json:"environment_revision"`
	Preparations        []WorkspaceSetupPreparation `json:"preparations"`
}

type WorkspaceSetupPreparation struct {
	State           execution.PreparationState `json:"state"`
	Current         bool                       `json:"current"`
	OutputTruncated bool                       `json:"output_truncated"`
}

type WorkspaceSetupApproval struct {
	ID                  string            `json:"command_id"`
	Source              string            `json:"source"`
	Target              string            `json:"target"`
	Files               map[string]string `json:"files"`
	SourceInputs        map[string]string `json:"source_inputs"`
	Tools               []string          `json:"tools"`
	Requirement         string            `json:"requirement"`
	Approval            string            `json:"approval"`
	EnvironmentRevision int64             `json:"environment_revision"`
}

// Validate rejects malformed input before recording consent or starting native
// effects. Current ownership/source checks remain with the host implementation.
func (in WorkspaceSetupApproval) Validate() error {
	if !factory.ValidID(in.ID) || !project.ValidCommit(in.Source) || !factory.ValidTargetBranch(in.Target) || len(in.Tools) > project.MaxPrepareTools || len(in.SourceInputs) == 0 || project.ValidateSourceInputs(in.SourceInputs) != nil {
		return execution.ErrPreparation
	}
	files := make(map[string][]byte, len(in.Files))
	for name, contents := range in.Files {
		if !utf8.ValidString(contents) {
			return execution.ErrPreparation
		}
		files[name] = []byte(contents)
	}
	if (project.ApprovedSetup{Files: files}).ValidateFiles() != nil {
		return execution.ErrPreparation
	}
	for _, tool := range in.Tools {
		if !project.ValidToolName(tool) {
			return execution.ErrPreparation
		}
	}
	return nil
}

// Hosts supply source acquisition and current local execution authority. The
// shared UI sends reviewed source/recipe facts, never credentials or host paths.
type WorkspaceSetupPort interface {
	ReadSetup(context.Context, collaboration.ActorID, collaboration.RepositoryID, []string) (WorkspaceSetup, error)
	ApproveSetup(context.Context, collaboration.ActorID, collaboration.RepositoryID, WorkspaceSetupApproval) (WorkspaceSetup, error)
}
