package mcpserver

import (
	"context"

	factoryservice "github.com/levitateos/sodaos/internal/factory/service"
)

func (s *factoryCatalogServer) workerResults() factoryservice.WorkerResults {
	current, _ := s.deps.Submissions.(factoryservice.WorkerCurrentContextSource)
	receipts, _ := s.deps.Submissions.(factoryservice.WorkerCheckpointSource)
	return factoryservice.WorkerResults{Assignments: s.deps.Assignments, Runs: s.deps.Runs, Current: current, Receipts: receipts, Catalog: s.deps.Catalog, Now: s.now}
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
