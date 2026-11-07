package execution

import (
	"strconv"
	"time"
)

// NativeReceiptObservation carries bounded facts from the Rust receipt reader.
// It carries no output, prompt, credential, placement selector or root-retirement
// claim. The canonical tool/workspace/instance come from the retained record.
type NativeReceiptObservation struct {
	WorkerUID      int    `json:"worker_uid"`
	ConnectorUID   *int   `json:"connector_uid,omitempty"`
	Service        string `json:"service,omitempty"`
	Invocation     string `json:"invocation"`
	SystemInstance string `json:"system_instance"`
	StartIdentity  string `json:"start_identity"`
	PID            int    `json:"namespace_pid"`
	HostPID        int    `json:"host_pid,omitempty"`
	StartedUnixMS  int64  `json:"started_unix_ms"`
	ExitCode       *int   `json:"exit_code"`
	ExitSignal     *int   `json:"exit_signal"`
}

type NativeCustodyObservation struct {
	NativeCustody
	Sealed bool `json:"sealed"`
}

// ObserveNativeReceipt preserves captured identity and lets the defining
// NativeRecord advance validator decide whether the observation conflicts.
// A receipt is at most exited evidence; root retirement stays independent.
func ObserveNativeReceipt(prior NativeRecord, receipt NativeReceiptObservation, custody *NativeCustodyObservation, desktopUID int) (NativeRecord, error) {
	if prior.Validate() != nil || prior.Intent == "" || receipt.StartedUnixMS <= 0 ||
		receipt.ExitCode != nil && (*receipt.ExitCode < 0 || *receipt.ExitCode > 255) ||
		receipt.ExitSignal != nil && (*receipt.ExitSignal < 1 || *receipt.ExitSignal > 64) ||
		receipt.ExitCode != nil && receipt.ExitSignal != nil {
		return NativeRecord{}, ErrObservation
	}
	next := prior
	next.Provenance.Process = &NativeProcess{InvocationID: receipt.Invocation, SystemInstance: receipt.SystemInstance,
		StartIdentity: receipt.StartIdentity, PID: receipt.PID, StartedAt: time.UnixMilli(receipt.StartedUnixMS).UTC(),
		Executable: prior.Tool.Executable, ExecutableSHA256: prior.Tool.SHA256, ExitCode: receipt.ExitCode}
	if receipt.ExitSignal != nil {
		// Native signal numbers have one lossless canonical string projection.
		signal := strconv.Itoa(*receipt.ExitSignal)
		next.Provenance.Process.ExitSignal = &signal
	}
	if prior.Provenance.TrustMode == NativeCLIProvider {
		if receipt.WorkerUID != 0 || receipt.PID != 0 || receipt.HostPID <= 0 || receipt.ConnectorUID == nil || *receipt.ConnectorUID != desktopUID {
			return NativeRecord{}, ErrObservation
		}
		next.Provenance.Connector = &NativeConnector{UID: desktopUID, Service: receipt.Service}
		next.Provenance.Process.PID = receipt.HostPID
	} else {
		if receipt.ConnectorUID != nil || receipt.Service != "" || receipt.HostPID != 0 {
			return NativeRecord{}, ErrObservation
		}
		next.Provenance.Isolation = &NativeIsolation{DesktopUID: desktopUID, WorkerUID: receipt.WorkerUID}
	}
	if prior.Retirement != Retired {
		next.Retirement = Uncertain
		if receipt.ExitCode != nil || receipt.ExitSignal != nil {
			next.Retirement = Exited
		}
	}
	if custody != nil {
		if custody.State == NotAcquired && prior.Provenance.TrustMode != NativeCLIProvider {
			return NativeRecord{}, ErrObservation
		}
		next.Provenance.Custody, next.Sealed = custody.NativeCustody, custody.Sealed
	}
	if !next.Advances(prior) {
		return NativeRecord{}, ErrRunChanged
	}
	return next, nil
}
