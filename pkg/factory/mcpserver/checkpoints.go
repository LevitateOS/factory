package mcpserver

import (
	"context"

	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

func (s *factoryCatalogServer) workerResults() factoryservice.WorkerResults {
	return factoryservice.WorkerResults{Assignments: s.deps.Assignments, Runs: s.deps.Runs, Current: s.deps.CurrentContext, Receipts: s.deps.CheckpointReceipts, Catalog: s.deps.Catalog, Now: s.now}
}

func (s *factoryCatalogServer) addCheckpointContext(ctx context.Context, read *readContext, body map[string]any) error {
	execution, err := s.workerResults().CheckpointContext(ctx, read.WorkerContextRead)
	if err != nil {
		return err
	}
	if execution != nil {
		body["execution"] = execution
	}
	return nil
}
