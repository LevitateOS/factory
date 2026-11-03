package execution

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/project"
)

var (
	ErrPreparationContext       = errors.New("invalid workspace preparation context")
	ErrPreparationContextBounds = errors.New("workspace preparation context exceeds bounds")
)

// PreparationContextRequest binds transient approved files/diffs to the captured
// checkout owner. Repository paths are relative selections, never host paths.
type PreparationContextRequest struct {
	Input  project.FactoryPreparationContextRequest `json:"input"`
	Role   string                                   `json:"role"`
	Native *NativeWorkspaceRef                      `json:"native,omitempty"`
}

func (r PreparationContextRequest) Address() PreparationAddress {
	return PreparationAddress{Workspace: r.Input.Project, ID: r.Input.ID, Native: r.Native}
}

func (r PreparationContextRequest) Validate() error {
	if r.Input.Validate() != nil || r.Address().Validate() != nil || !project.ValidFactoryRole(r.Role) {
		return ErrPreparationContext
	}
	return nil
}

type PreparationContext struct {
	Material project.FactoryPreparationContext `json:"material"`
	Native   *NativeWorkspaceRef               `json:"native,omitempty"`
}

func (c PreparationContext) ValidateFor(r PreparationContextRequest) error {
	if r.Validate() != nil || c.Material.ValidateFor(r.Input, r.Role) != nil ||
		(r.Native == nil) != (c.Native == nil) || r.Native != nil && *r.Native != *c.Native {
		return ErrPreparationContext
	}
	return nil
}

type PreparationContextReader interface {
	ReadWorkspaceContext(context.Context, PreparationContextRequest) (PreparationContext, error)
}
