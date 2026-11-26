package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/store"
)

// NativeExecution observes the supervisor's retained native execution owner.
// It never launches from an address, recovers a missing owner, or treats an
// unavailable observation as process retirement. Go retains product settlement.
type NativeExecution struct {
	Config   NativeConfig
	Instance string
	Store    *store.Store
}

var _ execution.RunObserver = NativeExecution{}
var _ execution.CustodyObserver = NativeExecution{}
var _ execution.ToolObserver = NativeExecution{}

func (p NativeExecution) InspectTool(ctx context.Context, family string) (execution.ToolObservation, error) {
	var empty execution.ToolObservation
	if family != project.FactoryHarnessMuse {
		return empty, execution.ErrProvenance
	}
	var selected struct {
		Tool          execution.NativeTool `json:"tool"`
		Channel       string               `json:"channel"`
		CheckedAtUnix int64                `json:"checked_at_unix"`
	}
	if err := nativeHostCall(ctx, p.Config, p.Instance, "/api/native/execution", struct {
		Operation string `json:"operation"`
		Harness   string `json:"harness"`
	}{"inspect_tool", family}, &selected); err != nil {
		return empty, err
	}
	checked := time.Unix(selected.CheckedAtUnix, 0)
	if selected.Channel != "installed" || time.Since(checked) > 5*time.Minute || checked.After(time.Now().Add(5*time.Second)) {
		return empty, execution.ErrProvenance
	}
	observed := execution.ToolObservation{Kind: collaboration.Native, Native: &selected.Tool}
	return observed, observed.ValidateFor(family)
}

type nativeExecutionSnapshot struct {
	IntentDigest   string                              `json:"intent_digest"`
	Receipt        *execution.NativeReceiptObservation `json:"receipt"`
	Custody        *execution.NativeCustodyObservation `json:"custody"`
	Live           *bool                               `json:"live"`
	Retired        bool                                `json:"retired"`
	Reason         *string                             `json:"reason"`
	Output         *string                             `json:"output"`
	OutputComplete bool                                `json:"output_complete"`
	CandidateState *string                             `json:"candidate_state"`
	Candidate      *string                             `json:"candidate"`
}

func (p NativeExecution) InspectRun(ctx context.Context, address execution.RunAddress) (execution.RunObservation, error) {
	return p.observe(ctx, address, "inspect")
}
func (p NativeExecution) RetireRun(ctx context.Context, address execution.RunAddress) (execution.RunObservation, error) {
	return p.observe(ctx, address, "cancel")
}
func (p NativeExecution) ReadCustody(ctx context.Context, address execution.RunAddress) (execution.CustodyObservation, error) {
	observed, err := p.InspectRun(ctx, address)
	if err != nil {
		if errors.Is(err, execution.ErrRunNotObserved) {
			return execution.CustodyObservation{}, execution.ErrCustodyNotObserved
		}
		return execution.CustodyObservation{}, err
	}
	run, err := p.Store.FactoryRun(ctx, address.Run)
	if err != nil || run.Native == nil {
		return execution.CustodyObservation{}, execution.ErrCustodyNotObserved
	}
	custody := observed.Native.Custody
	result := execution.CustodyObservation{Run: address.Run, Workspace: address.Workspace,
		Kind: collaboration.Native, Sealed: run.Native.Sealed, Native: &custody}
	return result, result.ValidateFor(address)
}
func (p NativeExecution) SealCustody(ctx context.Context, address execution.RunAddress) error {
	if _, err := p.RetireRun(ctx, address); err != nil {
		return err
	}
	run, err := p.Store.FactoryRun(ctx, address.Run)
	if err != nil || run.Native == nil || !run.Native.Sealed {
		return execution.ErrCustodyNotObserved
	}
	return nil
}

func (p NativeExecution) observe(ctx context.Context, address execution.RunAddress, operation string) (execution.RunObservation, error) {
	var empty execution.RunObservation
	if p.Store == nil || address.Validate() != nil {
		return empty, execution.ErrRunNotObserved
	}
	run, err := p.Store.FactoryRun(ctx, address.Run)
	if err != nil {
		return empty, err
	}
	if run.ProjectID != address.Workspace || run.Native == nil ||
		run.Native.Provenance.Profile != p.Config.Profile {
		return empty, execution.ErrRunChanged
	}
	if run.Native.Intent == "" {
		if run.Reconciled && run.Native.Settled() {
			return empty, execution.ErrRunNotObserved
		}
		return empty, execution.ErrRunChanged
	}
	reconciled := run.Reconciled
	retained := run.Native.Provenance.InstanceID != p.Instance || reconciled
	if retained {
		// Startup recovery owns root retirement. An old instance grants no
		// live ownership or cancellation; only its acknowledged terminal facts
		// can be read through the current authenticated native channel.
		if !run.Native.Settled() {
			return empty, execution.ErrRunNotObserved
		}
		operation = "inspect_retained"
	}
	intent := run.Native.Intent
	var snapshot nativeExecutionSnapshot
	if err = nativeHostCall(ctx, p.Config, p.Instance, "/api/native/execution", struct {
		Intent    string `json:"intent"`
		Operation string `json:"operation"`
	}{intent, operation}, &snapshot); err != nil {
		return empty, execution.ErrRunNotObserved
	}
	digest := sha256.Sum256([]byte(intent))
	if snapshot.IntentDigest != hex.EncodeToString(digest[:]) || snapshot.Receipt == nil || snapshot.Custody == nil {
		return empty, execution.ErrRunNotObserved
	}
	custodyDone := snapshot.Custody.Sealed && (snapshot.Custody.State == execution.Released || run.Native.Provenance.TrustMode == execution.NativeCLIProvider && snapshot.Custody.State == execution.NotAcquired)
	if snapshot.Retired && (snapshot.Live == nil || *snapshot.Live || !custodyDone) {
		return empty, execution.ErrObservation
	}
	if retained && (snapshot.Live == nil || *snapshot.Live || !custodyDone) {
		return empty, execution.ErrObservation
	}
	if reconciled {
		if _, err = execution.ObserveNativeReceipt(*run.Native, *snapshot.Receipt, snapshot.Custody, os.Geteuid()); err != nil {
			return empty, err
		}
	} else if err = p.Store.ObserveNativeRecovery(ctx, p.Config.Profile, intent, *snapshot.Receipt, snapshot.Custody, os.Geteuid()); err != nil {
		return empty, err
	}
	if snapshot.Retired && !reconciled {
		if err = p.Store.RecordNativeRootRetirement(ctx, p.Config.Profile, intent, snapshot.Receipt.Invocation, snapshot.Custody.AcquisitionID); err != nil {
			return empty, err
		}
	}
	// Reload after callback I/O and fact recording. A changed/reconciled run
	// cannot inherit an earlier process or credential observation.
	run, err = p.Store.FactoryRun(ctx, address.Run)
	if err != nil {
		return empty, err
	}
	if run.Native == nil || run.Native.Intent != intent || run.Reconciled != reconciled {
		return empty, execution.ErrRunChanged
	}
	native := *run.Native
	live := snapshot.Live != nil && *snapshot.Live
	phase := project.FactoryUncertain
	if snapshot.Retired || retained && native.Retirement == execution.Retired {
		phase = project.FactoryFailed
		candidateValid := run.Role != project.RoleCoder || snapshot.CandidateState != nil && (*snapshot.CandidateState == "unchanged" && snapshot.Candidate == nil ||
			*snapshot.CandidateState == "captured" && snapshot.Candidate != nil && project.ValidCommit(*snapshot.Candidate) && *snapshot.Candidate != run.InputSHA)
		if snapshot.Reason != nil && *snapshot.Reason == "exited" && snapshot.Receipt.ExitCode != nil && *snapshot.Receipt.ExitCode == 0 && snapshot.OutputComplete && candidateValid {
			phase = project.FactoryCompleted
		} else if snapshot.Reason != nil && *snapshot.Reason == "cancelled" {
			phase = project.FactoryStopped
		}
	} else if live {
		if native.Sealed || native.Provenance.Process.ExitCode != nil || native.Provenance.Process.ExitSignal != nil {
			return empty, execution.ErrRunChanged
		}
		native.Retirement, phase = execution.Running, project.FactoryRunning
	}
	observed := execution.RunObservation{Run: run.ID, Workspace: run.ProjectID, Role: run.Role, Kind: collaboration.Native,
		Phase: phase, Live: live, Retirement: native.Retirement, Native: &native.Provenance,
		ExitCode: native.Provenance.Process.ExitCode, OutputComplete: snapshot.OutputComplete}
	if snapshot.Output != nil {
		observed.Output = *snapshot.Output
	}
	if snapshot.Reason != nil {
		observed.Reason = *snapshot.Reason
	}
	return observed, observed.ValidateFor(address)
}

type nativeOutputRead struct {
	IntentDigest string  `json:"intent_digest"`
	Offset       int64   `json:"offset"`
	Total        int64   `json:"total"`
	Data         string  `json:"data"`
	Live         bool    `json:"live"`
	Terminal     bool    `json:"terminal"`
	Reason       *string `json:"reason"`
	ExitCode     *int    `json:"exit_code"`
}

// ReadOutput reads this instance's run output from cursor through the
// supervisor's bounded output query. It spends the read budget, never a
// liveness query, and never inspects, cancels, settles or records the run.
// The status it reports is a display projection; InspectRun owns settlement.
func (p NativeExecution) ReadOutput(ctx context.Context, run factory.Run, cursor int64) (project.FactoryOutputState, error) {
	var empty project.FactoryOutputState
	if run.Native == nil || run.Native.Intent == "" || run.Reconciled ||
		run.Native.Provenance.Profile != p.Config.Profile || run.Native.Provenance.InstanceID != p.Instance {
		return empty, execution.ErrRunNotObserved
	}
	var read nativeOutputRead
	if err := nativeHostCall(ctx, p.Config, p.Instance, "/api/native/execution", struct {
		Intent    string `json:"intent"`
		Operation string `json:"operation"`
		Offset    int64  `json:"offset"`
	}{run.Native.Intent, "output", cursor}, &read); err != nil {
		return empty, execution.ErrRunNotObserved
	}
	digest := sha256.Sum256([]byte(run.Native.Intent))
	data, err := base64.StdEncoding.DecodeString(read.Data)
	if read.IntentDigest != hex.EncodeToString(digest[:]) || err != nil || read.Offset != min(cursor, read.Total) ||
		read.Offset+int64(len(data)) > read.Total || read.Total > project.MaxFactoryOutputOffset || read.Terminal && read.Live {
		return empty, execution.ErrObservation
	}
	phase := project.FactoryUncertain
	switch {
	case read.Live:
		phase = project.FactoryRunning
	case read.Terminal && read.Reason != nil && *read.Reason == "exited" && read.ExitCode != nil && *read.ExitCode == 0:
		phase = project.FactoryCompleted
	case read.Terminal && read.Reason != nil && *read.Reason == "cancelled":
		phase = project.FactoryStopped
	case read.Terminal:
		phase = project.FactoryFailed
	}
	out := project.FactoryOutputState{ID: run.ID, Project: run.ProjectID, Phase: phase, Invocation: run.InvocationID(),
		Live: read.Live, Terminal: read.Terminal, Total: read.Total, Offset: read.Offset, Next: read.Offset + int64(len(data)),
		Data: read.Data, Gap: read.Offset != cursor}
	if read.Terminal {
		out.ExitCode = read.ExitCode
		if read.Reason != nil {
			out.Reason = *read.Reason
		}
	}
	return out, nil
}
