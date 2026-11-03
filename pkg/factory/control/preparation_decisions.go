package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// DecisionResult reports the saved preparation decision and current head.
// Approvals also report whether the maintenance hold remains active.
type DecisionResult struct {
	DecisionID string `json:"decision_id"`
	Head       string `json:"head"`
	Depth      int64  `json:"depth"`
	HoldActive bool   `json:"hold_active,omitempty"`
}

// AdmitRequirement saves the maintainer's exact-input acceptance. The decision
// ID and predecessor are owned by the immutable requirement chain.
func (c *Coordinator) AdmitRequirement(ctx context.Context, decision project.RequirementDecision) (DecisionResult, error) {
	bounded, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	if err := c.Store.AdmitRequirementDecision(bounded, decision); err != nil {
		return DecisionResult{}, err
	}
	head, err := c.Store.RequirementHead(bounded, decision.Project)
	if err != nil {
		return DecisionResult{}, err
	}
	depth, err := c.Store.RequirementDepth(bounded, decision.Project)
	if err != nil {
		return DecisionResult{}, err
	}
	return DecisionResult{DecisionID: decision.ID, Head: head, Depth: depth}, nil
}

// AdmitApproval saves privileged-effect approval for the current requirement
// head. Releasing the maintenance hold remains a separate explicit action.
func (c *Coordinator) AdmitApproval(ctx context.Context, decision project.ApprovalDecision) (DecisionResult, error) {
	bounded, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	requirement, err := c.Store.RequirementHead(bounded, decision.Project)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return DecisionResult{}, store.ErrCommandConflict
		}
		return DecisionResult{}, err
	}
	if decision.Requirement != requirement {
		return DecisionResult{}, store.ErrCommandConflict
	}
	if err = c.Store.AdmitApprovalDecision(bounded, decision); err != nil {
		return DecisionResult{}, err
	}
	head, err := c.Store.ApprovalHead(bounded, decision.Project)
	if err != nil {
		return DecisionResult{}, err
	}
	depth, err := c.Store.ApprovalDepth(bounded, decision.Project)
	if err != nil {
		return DecisionResult{}, err
	}
	held, err := c.holdActive(bounded, decision.Project)
	if err != nil {
		return DecisionResult{}, err
	}
	return DecisionResult{DecisionID: decision.ID, Head: head, Depth: depth, HoldActive: held}, nil
}

// holdActive reports whether the project maintenance hold is currently set.
func (c *Coordinator) holdActive(ctx context.Context, projectID string) (bool, error) {
	held, err := c.Store.MaintenanceHold(ctx, projectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return held.Hold, nil
}
