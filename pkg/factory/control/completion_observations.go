package control

import (
	"context"
	"encoding/binary"
	"reflect"
)

// GitHub has no global issue clock or idle signal. Compare complete observations
// and unchanged local material; never synthesize either provider guarantee.
func bracketIssueObservations(ctx context.Context, deps CompletionDriveDeps, repository, generation int64) bracketResult {
	if !chargeBracketCapturesAndObserves(ctx) {
		return bracketResult{hold: HoldInventoryInProgress}
	}
	local, err := deps.CaptureLocal(ctx, repository)
	if err != nil {
		return bracketResult{hold: HoldInventoryUnavailable}
	}
	if local.Validate() != nil {
		return bracketResult{hold: HoldInventoryMaterial}
	}
	first, err := deps.ReadIssues(ctx, repository)
	if err != nil {
		return bracketResult{hold: routeInventoryPageError(err).Error()}
	}
	if first == nil {
		return bracketResult{hold: HoldInventoryUnavailable}
	}
	// Validate bounds before asking the provider for a confirming traversal.
	if err = checkInventoryIssues(first, 0, 0, 0); err != nil {
		return bracketResult{hold: err.Error()}
	}
	second, err := deps.ReadIssues(ctx, repository)
	if err != nil {
		return bracketResult{hold: routeInventoryPageError(err).Error()}
	}
	if !reflect.DeepEqual(first, second) {
		return bracketResult{hold: HoldInventoryRevision}
	}
	confirmed, err := deps.CaptureLocal(ctx, repository)
	if err != nil {
		return bracketResult{hold: HoldInventoryUnavailable}
	}
	if !local.Equal(confirmed) {
		return bracketResult{hold: HoldInventoryMaterial}
	}
	running := foldInventoryPage([32]byte{}, repository, generation, first)
	// The retained numeric field is an observation token, not a remote clock.
	// The seal also binds the complete 256-bit content chain and exact bodies.
	revision := int64(binary.BigEndian.Uint64(running[:8]) & 0x7fffffffffffffff)
	// Zero names missing evidence in the retained record. The complete empty
	// observation has a zero chain, but still needs a positive observation token.
	if revision == 0 {
		revision = 1
	}
	g := inventoryGather{repository: repository, generation: generation, local: local, revision: revision, next: InventoryCollector{Issues: first, Running: running, Local: local, Revision: revision}}
	for _, issue := range first {
		g.evidence += len(issue.Data)
	}
	sealed, _, _, err := g.sealInventory()
	if err != nil {
		return bracketResult{hold: err.Error()}
	}
	return bracketResult{sealed: sealed}
}
