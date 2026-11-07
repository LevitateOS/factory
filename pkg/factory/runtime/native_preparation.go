package runtime

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"time"

	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

var _ execution.PreparationObserver = NativeExecution{}
var _ execution.PreparationContextReader = NativeExecution{}
var _ execution.PreparationLauncher = NativeExecution{}

func (p NativeExecution) PrepareWorkspaceCandidate(ctx context.Context, request execution.CandidatePreparationRequest) (execution.PreparationState, error) {
	var empty execution.PreparationState
	if p.Store == nil || request.Validate() != nil || !p.currentPreparationAddress(request.Address()) || request.Input.Preparation.Credential != "" || !time.Now().Before(request.Input.Deadline) {
		return empty, execution.ErrPreparation
	}
	stored, err := p.Store.CandidatePreparation(ctx, request.Input.Preparation.ID)
	if err != nil {
		return empty, err
	}
	actual, wanted := request.Input.Preparation, stored.Preparation.Preparation
	actual.Revision, wanted.Revision = 0, 0
	if !reflect.DeepEqual(actual, wanted) || stored.Preparation.Native == nil || *stored.Preparation.Native != *request.Native || !reflect.DeepEqual(request.Input.SourceRun, stored.Admission.SourceRun) {
		return empty, execution.ErrPreparation
	}
	source, err := p.Store.Preparation(ctx, request.Input.SourcePreparation)
	if err != nil {
		return empty, err
	}
	base := source.Preparation
	if source.Native == nil || !p.currentPreparationAddress(source.Address()) || base.Project != wanted.Project || base.SetupDigest != wanted.SetupDigest || !base.Requirements.Equal(wanted.Requirements) || base.Approval != wanted.Approval || !slices.Equal(base.Tools, wanted.Tools) || !source.State.Ready || source.State.Stopped {
		return empty, execution.ErrPreparation
	}
	notAfter, err := p.Store.AuthorizeCandidatePreparation(ctx, wanted.ID)
	if err != nil {
		return empty, err
	}
	if request.Input.Deadline.After(time.Unix(notAfter, 0)) {
		return empty, execution.ErrPreparation
	}
	if request.Input.SourceRun != nil {
		ref := request.Input.SourceRun
		exported, exportErr := p.ExportCandidate(ctx, execution.CandidateRequest{Address: execution.RunAddress{Workspace: ref.Project, Run: ref.ID}, Role: ref.Role, Preparation: ref.Preparation, Candidate: ref.Candidate})
		if exportErr != nil {
			return empty, exportErr
		}
		request.Input.Bundle = exported.Bundle
	} else if wanted.Role == project.RoleCoder && wanted.SourceCommit != base.SourceCommit {
		return empty, execution.ErrPreparation
	}
	request.Input.Preparation.Revision = 0
	bounded, cancel := context.WithDeadline(ctx, request.Input.Deadline)
	defer cancel()
	var state execution.PreparationState
	err = nativeHostCall(bounded, p.Config, p.Instance, "/api/native/preparation", struct {
		Operation string                                `json:"operation"`
		Request   execution.CandidatePreparationRequest `json:"request"`
		Source    execution.PreparationAddress          `json:"source"`
	}{"candidate", request, source.Address()}, &state)
	if err != nil {
		return empty, err
	}
	if _, err = p.Store.AuthorizeCandidatePreparation(ctx, wanted.ID); err != nil {
		return empty, err
	}
	if err = state.ValidateFor(request.Address()); err != nil {
		return empty, err
	}
	if state.Role != wanted.Role || state.SourceCommit != wanted.SourceCommit || state.SetupDigest != wanted.SetupDigest {
		return empty, execution.ErrPreparation
	}
	return state, nil
}

func (p NativeExecution) ReadWorkspaceContext(ctx context.Context, request execution.PreparationContextRequest) (execution.PreparationContext, error) {
	var result execution.PreparationContext
	if p.Store == nil || request.Validate() != nil || !p.currentPreparationAddress(request.Address()) || !time.Now().Before(request.Input.NotAfter) {
		return result, execution.ErrPreparationContext
	}
	record, err := p.Store.Preparation(ctx, request.Input.ID)
	if err != nil {
		return result, err
	}
	if record.Native == nil || *record.Native != *request.Native || record.Preparation.Project != request.Input.Project || record.Preparation.Role != request.Role || record.Preparation.SourceCommit != request.Input.SourceCommit || !record.State.Ready || record.State.Stopped {
		return result, execution.ErrPreparationContext
	}
	bounded, cancel := context.WithDeadline(ctx, request.Input.NotAfter)
	defer cancel()
	// An empty selection still reads instructions and the candidate diff.
	// The native protocol represents that selection as an array, not null.
	if request.Input.Paths == nil {
		request.Input.Paths = []string{}
	}
	err = nativeHostCall(bounded, p.Config, p.Instance, "/api/native/preparation", struct {
		Operation string                              `json:"operation"`
		Request   execution.PreparationContextRequest `json:"request"`
	}{"context", request}, &result)
	if err != nil {
		return execution.PreparationContext{}, err
	}
	if err = bounded.Err(); err != nil {
		return execution.PreparationContext{}, err
	}
	if err = result.ValidateFor(request); err != nil {
		return execution.PreparationContext{}, err
	}
	return result, nil
}

// Approval and current enrollment stay in Go. The native owner retains the
// source/setup bytes and reports its real isolated preparation observations.
func (p NativeExecution) PrepareWorkspace(ctx context.Context, request execution.StandingPreparationRequest) (execution.PreparationState, error) {
	var empty execution.PreparationState
	if p.Store == nil || request.Validate() != nil || !p.currentPreparationAddress(request.Address()) || request.Input.Preparation.Credential != "" {
		return empty, execution.ErrPreparation
	}
	record := execution.PreparationRecord{Preparation: request.Input.Preparation, Native: request.Native}
	if err := p.Store.AuthorizeStandingPreparation(ctx, record); err != nil {
		return empty, err
	}
	// Observation revision is a Go CAS counter, not part of the native job's
	// immutable inputs. Approval revisions and all actual inputs stay captured.
	request.Input.Preparation.Revision = 0
	var state execution.PreparationState
	err := nativeHostCall(ctx, p.Config, p.Instance, "/api/native/preparation", struct {
		Operation string                               `json:"operation"`
		Request   execution.StandingPreparationRequest `json:"request"`
	}{"prepare", request}, &state)
	if err != nil {
		return empty, err
	}
	if err = p.Store.AuthorizeStandingPreparation(ctx, record); err != nil {
		return empty, err
	}
	if err = state.ValidateFor(request.Address()); err != nil {
		return empty, err
	}
	if state.SourceCommit != record.Preparation.SourceCommit || state.SetupDigest != record.Preparation.SetupDigest || state.Role != record.Preparation.Role {
		return empty, execution.ErrPreparation
	}
	return state, nil
}
func (p NativeExecution) currentPreparationAddress(address execution.PreparationAddress) bool {
	return address.Validate() == nil && address.Native != nil && address.Native.Profile == p.Config.Profile && address.Native.InstanceID == p.Instance
}
func (p NativeExecution) InspectWorkspacePreparation(ctx context.Context, address execution.PreparationAddress) (execution.PreparationState, error) {
	return p.preparationObservation(ctx, address, "inspect")
}
func (p NativeExecution) RetireWorkspacePreparation(ctx context.Context, address execution.PreparationAddress) (execution.PreparationState, error) {
	return p.preparationObservation(ctx, address, "retire")
}
func (p NativeExecution) preparationObservation(ctx context.Context, address execution.PreparationAddress, operation string) (execution.PreparationState, error) {
	var empty execution.PreparationState
	if p.Store == nil || !p.currentPreparationAddress(address) {
		return empty, execution.ErrPreparation
	}
	record, err := p.Store.Preparation(ctx, address.ID)
	if err != nil {
		return empty, err
	}
	if record.Address().Workspace != address.Workspace || record.Native == nil || *record.Native != *address.Native {
		return empty, execution.ErrPreparation
	}
	var state execution.PreparationState
	err = nativeHostCall(ctx, p.Config, p.Instance, "/api/native/preparation", struct {
		Operation string                       `json:"operation"`
		Address   execution.PreparationAddress `json:"address"`
	}{operation, address}, &state)
	if err != nil {
		return empty, errors.Join(execution.ErrPreparationNotObserved, err)
	}
	if err = state.ValidateFor(address); err != nil {
		return empty, err
	}
	observed := record
	observed.State = state
	if err = observed.Validate(); err != nil {
		return empty, err
	}
	return state, nil
}
