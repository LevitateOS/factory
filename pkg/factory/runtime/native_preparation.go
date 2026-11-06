package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory/execution"
)

var _ execution.PreparationObserver = NativeExecution{}
var _ execution.PreparationContextReader = NativeExecution{}

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
