package control

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// PrepareContextBundleSelection records one bundle selection for a staged
// capability at preparation time, before the first read. A recorded
// selection is reused; a failed consultation leaves no receipt and the next
// preparation asks again. Reads retain required facts and an explicit hold.
func PrepareContextBundleSelection(ctx context.Context, st *store.Store, chooser ProductJudgment, a factory.Assignment, run factory.Run, verify currentVerificationSource) (bundle, reason string, err error) {
	if st == nil {
		return factory.ContextBundleSnapshotHold, "unavailable", ErrPriorityUnavailable
	}
	preparation, _, err := recordedMCPPreparation(ctx, st, a.Preparation)
	if err != nil {
		return factory.ContextBundleSnapshotHold, "unavailable", err
	}
	capability, err := BuildMCPCapability(a, run, preparation)
	if err != nil {
		return factory.ContextBundleSnapshotHold, "unavailable", err
	}
	recorded, err := st.GetMCPCapability(ctx, capability.ID)
	if err != nil || recorded.ScopeDigest() != capability.ScopeDigest() {
		return factory.ContextBundleSnapshotHold, "context_selection_unavailable", ErrPriorityUnavailable
	}
	capability = recorded
	state, attemptEligible, lineageEligible, err := loadContextBundleState(ctx, st, capability, a, verify)
	if err != nil {
		return factory.ContextBundleSnapshotHold, "required_current_unavailable", err
	}
	if state.Current.Availability != "complete" {
		return factory.ContextBundleSnapshotHold, "required_current_unavailable", nil
	}
	if resolution := capability.ContextCurrentResolution; resolution != nil {
		current, currentErr := st.FactoryCurrentContext(ctx, capability, time.Now())
		if currentErr != nil {
			return factory.ContextBundleSnapshotHold, "required_current_unavailable", currentErr
		}
		digest, digestErr := factory.CurrentRequiredSemanticDigest(current.Role)
		if resolution.ValidateFor(capability) != nil || digestErr != nil || digest != resolution.RequiredDigest {
			return factory.ContextBundleSnapshotHold, "context_selection_stale", nil
		}
		return factory.ContextBundleCurrent, "", nil
	}
	if factory.ValidDigest(capability.ContextSelectionSnapshot) {
		receipt, err := st.PriorityJudgment(ctx, capability.ContextSelectionSnapshot)
		if err == nil {
			bundle, reason := ContextBundleFromReceipt(receipt, capability, a, attemptEligible, lineageEligible)
			return bundle, reason, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return factory.ContextBundleSnapshotHold, "context_selection_unavailable", err
		}
		// The earlier call left no receipt: ask again.
	}
	if chooser == nil {
		return factory.ContextBundleSnapshotHold, "context_selection_unavailable", ErrPriorityUnavailable
	}
	coord := &Coordinator{Store: st, Planning: &PlanningJudgments{Chooser: chooser}}
	admit := func(ctx context.Context, snapshot string) (bool, error) {
		return st.BindContextBundleSelection(ctx, capability, snapshot)
	}
	return coord.selectContextBundleState(ctx, a.Repository, state, attemptEligible, lineageEligible, admit)
}
