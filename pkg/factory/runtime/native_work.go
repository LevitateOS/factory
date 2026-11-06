package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
)

var _ execution.WorkRecoverer = NativeExecution{}
var _ control.HostFactory = NativeExecution{}

func (p NativeExecution) RecoverWork(ctx context.Context, request execution.WorkRecoveryRequest) (execution.WorkRecoveryReceipt, error) {
	var empty execution.WorkRecoveryReceipt
	if p.Store == nil || request.Validate() != nil || request.Native == nil || request.Native.Target.Profile != p.Config.Profile {
		return empty, execution.ErrRecovery
	}
	run, err := p.Store.FactoryRun(ctx, request.Address.Run)
	if err != nil {
		return empty, err
	}
	if run.ProjectID != request.Address.Workspace || !run.Reconciled || run.Native == nil || !run.Native.Settled() || run.Native.Intent == "" {
		return empty, execution.ErrRecovery
	}
	native := run.Native.Provenance
	if native.Profile != request.Native.Source.Profile || native.InstanceID != request.Native.Source.InstanceID || native.WorktreeID != request.Native.Source.WorktreeID {
		return empty, execution.ErrRecovery
	}
	var result struct {
		IntentDigest string `json:"intent_digest"`
		CheckoutID   string `json:"checkout_id"`
		Candidate    string `json:"candidate"`
		Reused       bool   `json:"reused"`
	}
	err = nativeHostCall(ctx, p.Config, p.Instance, "/api/native/execution", struct {
		Operation  string `json:"operation"`
		Intent     string `json:"intent"`
		CheckoutID string `json:"checkout_id"`
	}{"recover", run.Native.Intent, request.Native.Target.CheckoutID}, &result)
	if err != nil {
		return empty, err
	}
	digest := sha256.Sum256([]byte(run.Native.Intent))
	if result.IntentDigest != hex.EncodeToString(digest[:]) || result.CheckoutID != request.Native.Target.CheckoutID || !project.ValidCommit(result.Candidate) {
		return empty, execution.ErrRecovery
	}
	current, err := p.Store.FactoryRun(ctx, run.ID)
	if err != nil {
		return empty, err
	}
	if current.Native == nil || !current.Reconciled || !current.Native.Settled() || current.Native.Intent != run.Native.Intent {
		return empty, execution.ErrRecovery
	}
	receipt := execution.WorkRecoveryReceipt{Request: request, Reused: result.Reused}
	return receipt, receipt.ValidateFor(request)
}
