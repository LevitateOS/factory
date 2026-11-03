package service

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/execution"
	"github.com/levitateos/sodaos/internal/project"
	"github.com/levitateos/sodaos/internal/store"
)

// WorkerVerification reads protected checks for an already admitted worker.
// Sources observe existing preparations/receipts; they grant no execution or
// adoption authority. The controller repeats its own evidence reads on adoption.
type WorkerVerification struct {
	Store   *store.Store
	Catalog func(context.Context, factory.Assignment) (factory.ApprovedProjectCheckCatalog, error)
	Read    func(context.Context, factory.Assignment) (factory.CurrentVerificationEvidence, error)
}

func (s WorkerVerification) current(ctx context.Context, admitted factory.MCPCapability) (factory.Assignment, execution.PreparationRecord, error) {
	var a factory.Assignment
	var p execution.PreparationRecord
	if s.Store == nil || admitted.State != factory.MCPCapabilityActive {
		return a, p, ErrWorkerBinding
	}
	cap, a, run, err := (WorkerAdmission{Store: s.Store}).current(ctx, admitted)
	if err != nil {
		return a, p, err
	}
	if cap.State != factory.MCPCapabilityActive || a.Stage != factory.AssignmentAssigned {
		return a, p, ErrWorkerBinding
	}
	p, err = s.Store.Preparation(ctx, a.Preparation)
	if err != nil {
		return a, p, err
	}
	if p.Preparation.Project != a.ProjectID || p.Preparation.Role != a.Role || p.Preparation.SourceCommit != a.SourceCommit || p.Preparation.SetupDigest != cap.PreparationDigest {
		return a, p, ErrWorkerBinding
	}
	if (p.Native == nil) != (run.Native == nil) {
		return a, p, ErrWorkerBinding
	}
	if p.Native != nil && (run.Native.Sealed || run.Native.Retirement != execution.Running || run.Native.Provenance.Profile != p.Native.Profile || run.Native.Provenance.InstanceID != p.Native.InstanceID || run.Native.Provenance.WorktreeID != p.Native.WorktreeID) {
		return a, p, ErrWorkerBinding
	}
	return a, p, ctx.Err()
}

func (s WorkerVerification) planning(ctx context.Context, cap factory.MCPCapability) (factory.Assignment, execution.PreparationRecord, error) {
	a, p, err := s.current(ctx, cap)
	if err != nil {
		return a, p, err
	}
	if a.Phase != project.ExecutionDecompose || a.Role != project.RoleCoder || a.Subject.Kind != factory.SubjectIntent || a.Subject.Intent == nil {
		return a, p, errors.New("planning catalog assignment differs from capability")
	}
	head, err := s.Store.IntentRevision(ctx, a.Subject.Intent.IntentID, a.Subject.Intent.OwnerID)
	if err != nil {
		return a, p, err
	}
	if head.ID != a.Subject.Intent.RevisionID || head.Repository != a.Repository || head.State != "draft" || head.Digest() != a.Subject.Intent.ScopeDigest {
		return a, p, errors.New("planning catalog draft changed")
	}
	requirements, err := s.Store.RequirementHead(ctx, a.ProjectID)
	if err != nil {
		return a, p, err
	}
	approval, err := s.Store.ApprovalHead(ctx, a.ProjectID)
	if err != nil {
		return a, p, err
	}
	if requirements != p.Preparation.Requirements.ID || approval != p.Preparation.Approval.ID {
		return a, p, errors.New("planning catalog approval changed")
	}
	return a, p, ctx.Err()
}

// ApprovedCatalog binds the protected check to the current draft and preparation
// both before and after runtime I/O. A worker cannot supply executable check text.
func (s WorkerVerification) ApprovedCatalog(ctx context.Context, cap factory.MCPCapability) (factory.ApprovedCatalog, error) {
	if s.Catalog == nil || (cap.Profile != factory.MCPProfileProductLead && cap.Profile != factory.MCPProfileIssueMaker) {
		return nil, errors.New("approved check catalog is unavailable for this worker")
	}
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	a, _, err := s.planning(bounded, cap)
	if err != nil {
		return nil, err
	}
	catalog, readErr := s.Catalog(bounded, a)
	_, record, err := s.planning(bounded, cap)
	if err != nil {
		return nil, err
	}
	if readErr != nil {
		return nil, readErr
	}
	p := record.Preparation
	if catalog.Validate() != nil || catalog.Project != p.Project || catalog.Preparation != p.ID || catalog.Requirements != p.Requirements.ID || catalog.Approval != p.Approval.ID || catalog.SourceCommit != p.SourceCommit || catalog.PreparationDigest != factory.DeliveryPreparationDigest(p) {
		return nil, errors.New("approved check catalog differs from admitted preparation")
	}
	return catalog, nil
}

func (s WorkerVerification) reviewing(ctx context.Context, cap factory.MCPCapability) (factory.Assignment, error) {
	a, record, err := s.current(ctx, cap)
	if err != nil {
		return a, err
	}
	if a.Phase != project.ExecutionVerify || a.Role != project.RoleReviewer || a.Subject.Kind != factory.SubjectDelivery || a.Subject.Delivery == nil || factory.DeliveryPreparationDigest(record.Preparation) != a.Subject.Delivery.PreparationDigest {
		return a, errors.New("verification assignment does not match this worker")
	}
	return a, nil
}

// Evidence preserves missing/failed-check readback for a current Test Pilot.
// Revocation, changed scope and malformed successful observations return no view.
func (s WorkerVerification) Evidence(ctx context.Context, cap factory.MCPCapability) (map[string]any, error) {
	if s.Read == nil || cap.Profile != factory.MCPProfileTestPilot {
		return nil, errors.New("application verification evidence is unavailable for this worker")
	}
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	a, err := s.reviewing(bounded, cap)
	if err != nil {
		return nil, err
	}
	evidence, readErr := s.Read(bounded, a)
	a, err = s.reviewing(bounded, cap)
	if err != nil {
		return nil, err
	}
	if readErr != nil {
		return map[string]any{"available": false, "reason": readErr.Error(), "next_step": "Keep the adopted approved check definitions current, or supply the fixed check.sh soda-app-verification declaration; prepare the exact integrated source for the reviewer and resolve missing delivered work or failed checks."}, nil
	}
	if evidence.ValidateFor(*a.Subject.Delivery) != nil || evidence.ApprovedContext.Project != a.ProjectID || evidence.ApprovedContext.ID != a.Preparation || (evidence.Execution != nil && evidence.Execution.Run != a.Run) {
		return nil, errors.New("verification evidence differs from admitted delivery")
	}
	_, prerequisites, err := s.Store.ApplicationVerificationEvidenceInputs(bounded, a)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(*evidence.DeliveryPrerequisites, prerequisites) {
		return nil, errors.New("verification delivery prerequisites changed")
	}
	outcome := "native_check_passed"
	if evidence.ExecutionRequired {
		outcome = "native_preparation_check_passed"
		if evidence.ExecutionReceipt != nil {
			outcome = "native_manifest_passed"
		}
	}
	return map[string]any{"available": true, "evidence": evidence, "delivery_id": a.Subject.Delivery.DeliveryID, "input_digest": cap.ScopeDigest(), "outcome": outcome}, nil
}
