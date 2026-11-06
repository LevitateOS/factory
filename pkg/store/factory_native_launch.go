package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
)

// AuthorizeNativeLaunch rechecks the recorded packet and current grants at the
// native effect boundary. This grants no process observation or MCP activation.
func (s *Store) AuthorizeNativeLaunch(ctx context.Context, request execution.LaunchRequest) (factory.MCPCapability, error) {
	var empty factory.MCPCapability
	if request.Validate() != nil || request.Kind != collaboration.Native || request.Input.MCP == nil || request.Input.MCP.Capability != request.Input.Run.ID || !factory.ValidMCPSecret(request.Input.MCP.Secret) {
		return empty, execution.ErrLaunch
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	var raw []byte
	var state string
	if err = tx.QueryRowContext(ctx, `SELECT state,data FROM factory_mcp_capabilities WHERE id=$1`, request.Input.MCP.Capability).Scan(&state, &raw); err != nil {
		return empty, err
	}
	var cap factory.MCPCapability
	if err = json.Unmarshal(raw, &cap); err != nil {
		return empty, err
	}
	cap.State = state
	if cap.Validate() != nil || cap.Expired(time.Now()) || cap.State != factory.MCPCapabilityStaged && cap.State != factory.MCPCapabilityActive {
		return empty, ErrAdmissionChanged
	}
	if err = checkMCPWorkActiveTx(ctx, tx, cap); err != nil {
		return empty, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_runs WHERE id=$1`, request.Input.Run.ID).Scan(&raw); err != nil {
		return empty, err
	}
	var run factory.Run
	if err = json.Unmarshal(raw, &run); err != nil {
		return empty, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT data FROM factory_assignments WHERE id=$1`, cap.AssignmentID).Scan(&raw); err != nil {
		return empty, err
	}
	var assignment factory.Assignment
	if err = json.Unmarshal(raw, &assignment); err != nil {
		return empty, err
	}
	expected, err := run.LaunchRequest(assignment, request.Input)
	if err != nil {
		return empty, err
	}
	intent, err := execution.EncodeNativeIntent(request)
	if err != nil || run.Native == nil || run.Native.Intent != intent || expected.Native == nil || request.Native == nil || *expected.Native != *request.Native || cap.RunID != run.ID || cap.Project != run.ProjectID || cap.Repository != assignment.Repository || cap.SourceDigest != assignment.SourceCommit || cap.Profile != factory.MCPProfileOfAssignment(assignment) || !time.Now().Before(run.Deadline) {
		return empty, ErrAdmissionChanged
	}
	grants, err := loadAdmissionGrantsTx(ctx, tx, assignment.Repository, assignment.Connection)
	if err != nil {
		return empty, err
	}
	account := grants.sponsorship.Native
	if account == nil || account.Validate() != nil || account.Profile != request.Native.Workspace.Profile || account.CredentialSHA256 != request.Native.CredentialSHA256 {
		return empty, ErrAdmissionChanged
	}
	if err = checkAssignmentAuthorityTx(ctx, tx, assignment.Authority, assignment, grants); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return cap, nil
}
