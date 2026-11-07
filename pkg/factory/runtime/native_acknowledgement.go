package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
)

// The native inventory contains at most eight retained completed entries.
// Go alone checks final product/accounting state; Rust alone releases memory.
// A lost request leaves the entry for the next existing progress pass. A lost
// reply after removal needs no new durable flag or second settlement journal.
func (p NativeExecution) AcknowledgeSettledRuns(ctx context.Context) error {
	if p.Store == nil {
		return execution.ErrObservation
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var inventory struct {
		Items []struct {
			Intent    string `json:"intent"`
			Reference string `json:"reference"`
		} `json:"items"`
	}
	if err := nativeHostCall(ctx, p.Config, p.Instance, "/api/native/execution", struct {
		Operation string `json:"operation"`
	}{"completed"}, &inventory); err != nil {
		return err
	}
	if len(inventory.Items) > 8 {
		return execution.ErrObservation
	}
	for _, item := range inventory.Items {
		intent, err := execution.ParseNativeIntent(item.Intent)
		if err != nil || intent.Native.Workspace.Profile != p.Config.Profile || intent.Native.Workspace.InstanceID != p.Instance {
			return execution.ErrObservation
		}
		run, err := p.Store.FactoryRun(ctx, intent.Run)
		if err != nil {
			return err
		}
		if !run.Reconciled || run.Native == nil || !run.Native.Settled() || run.Native.Intent != item.Intent {
			continue
		}
		a, err := p.Store.AssignmentByRun(ctx, run.ID)
		if err != nil {
			return err
		}
		reservation, err := p.Store.Reservation(ctx, a.ID)
		if err != nil {
			return err
		}
		if a.Stage != factory.AssignmentFinished || a.Run != run.ID || reservation.State != factory.ReservationConsumed {
			continue
		}
		reference := run.Native.Provenance.Custody.AcquisitionID
		if connector := run.Native.Provenance.Connector; connector != nil {
			reference = connector.Service
		}
		if reference == "" || reference != item.Reference {
			return execution.ErrObservation
		}
		var reply struct {
			IntentDigest string `json:"intent_digest"`
			Retained     bool   `json:"retained"`
		}
		if err = nativeHostCall(ctx, p.Config, p.Instance, "/api/native/execution", struct {
			Operation string `json:"operation"`
			Intent    string `json:"intent"`
			Reference string `json:"reference"`
		}{"acknowledge", item.Intent, reference}, &reply); err != nil {
			return err
		}
		digest := sha256.Sum256([]byte(item.Intent))
		if reply.Retained || reply.IntentDigest != hex.EncodeToString(digest[:]) {
			return execution.ErrObservation
		}
	}
	return nil
}
