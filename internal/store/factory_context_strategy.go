package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/levitateos/sodaos/internal/factory"
)

func currentStrategyTx(ctx context.Context, tx *sql.Tx, a factory.Assignment, out *factory.CurrentContext) error {
	if a.Subject.DeliveryRepair != nil {
		return currentDeliveryRepairContextTx(ctx, tx, a, out)
	}
	if a.Subject.Completion == nil {
		return currentStallContextTx(ctx, tx, a, out)
	}
	bound := a.Subject.Completion
	var record ReassessmentRecord
	if err := contextJSONRow(ctx, tx, &record, `SELECT data FROM factory_reassessments WHERE cycle=$1`, bound.CycleID); err != nil {
		return err
	}
	if record.Validate() != nil || record.Cycle.Repository != a.Repository || record.Cycle.EventID != bound.CompletionID || record.Cycle.CycleID != bound.CycleID || record.InventoryDigest != bound.InventoryDigest || record.Local.SourceCommit != a.SourceCommit || record.EvidenceRevision != a.NativeRev || !slices.Contains(record.Cycle.Ledger.CoderRuns, a.Run) {
		return errors.New("sealed strategy input is outside assigned cycle")
	}
	out.Role.Strategy = &factory.CurrentStrategyContext{CompletionID: bound.CompletionID, CycleID: bound.CycleID, Generation: record.Cycle.Generation, InventoryDigest: record.InventoryDigest, EvidenceRevision: record.EvidenceRevision, Local: record.Local, Issues: record.Evidence, Complete: true}
	return nil
}
