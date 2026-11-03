package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/collaboration"

	"github.com/levitateos/sodaos/internal/factory/execution"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/project"
)

// ApprovedProjectCheckCatalog reads the existing protected preparation. It
// grants no executable strings or fixtures supplied by a worker.
func (c *Coordinator) ApprovedProjectCheckCatalog(ctx context.Context, a factory.Assignment) (factory.ApprovedProjectCheckCatalog, error) {
	var empty factory.ApprovedProjectCheckCatalog
	if c == nil || c.Store == nil || c.Host == nil || a.Phase != project.ExecutionDecompose || a.Role != project.RoleCoder || a.Subject.Kind != factory.SubjectIntent || a.Subject.Intent == nil {
		return empty, errors.New("approved planning check catalog is unavailable")
	}
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	proj, err := c.Store.NativeProjectForCollaborationRepository(bounded, collaboration.RepositoryID(a.Repository))
	if err != nil {
		return empty, err
	}
	if proj.ID != a.ProjectID {
		return empty, errors.New("planning catalog project differs from repository")
	}
	stored, err := c.Store.Preparation(bounded, a.Preparation)
	if err != nil {
		return empty, err
	}
	p := stored.Preparation
	if p.Validate() != nil || p.Project != a.ProjectID || p.Role != a.Role || p.SourceCommit != a.SourceCommit {
		return empty, errors.New("planning catalog preparation differs from assignment")
	}
	checkHeads := func() error {
		requirements, err := c.Store.RequirementHead(bounded, p.Project)
		if err != nil {
			return err
		}
		approval, err := c.Store.ApprovalHead(bounded, p.Project)
		if err != nil {
			return err
		}
		if requirements != p.Requirements.ID || approval != p.Approval.ID {
			return errors.New("planning catalog approval changed")
		}
		return nil
	}
	if err := checkHeads(); err != nil {
		return empty, err
	}
	state, err := c.Host.InspectWorkspacePreparation(bounded, stored.Address())
	if err != nil {
		return empty, err
	}
	if state.ValidateFor(stored.Address()) != nil || state.ID != p.ID || state.Project != p.Project || state.Role != p.Role || state.SourceCommit != p.SourceCommit || state.SetupDigest != p.SetupDigest || !state.Ready || state.Stopped || state.Phase != project.PrepareReady || state.SetupExit == nil || *state.SetupExit != 0 || state.CheckExit == nil || *state.CheckExit != 0 {
		return empty, errors.New("planning catalog requires a native ready preparation")
	}
	request := project.FactoryPreparationContextRequest{Project: p.Project, ID: p.ID, SourceCommit: p.SourceCommit, ApprovedBase: p.SourceCommit, DiffBase: p.SourceCommit, Candidate: p.SourceCommit, NotAfter: time.Now().Add(20 * time.Second)}
	if err := request.Validate(); err != nil {
		return empty, err
	}
	scoped := execution.PreparationContextRequest{Input: request, Role: p.Role, Native: stored.Native}
	observed, err := c.Host.ReadWorkspaceContext(bounded, scoped)
	material := observed.Material
	if err != nil {
		return empty, err
	}
	if err := observed.ValidateFor(scoped); err != nil {
		return empty, err
	}
	if len(material.Check) == 0 {
		return empty, errors.New("approved planning check is empty")
	}
	if err := checkHeads(); err != nil {
		return empty, err
	}
	catalog := factory.ApprovedProjectCheckCatalog{Project: p.Project, Preparation: p.ID, Requirements: p.Requirements.ID, Approval: p.Approval.ID, SourceCommit: p.SourceCommit, PreparationDigest: factory.DeliveryPreparationDigest(p), CheckDigest: factory.MCPInputDigest(material.Check)}
	return catalog, catalog.Validate()
}
