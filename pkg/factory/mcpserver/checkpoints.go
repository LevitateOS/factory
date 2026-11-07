package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

func (s *factoryCatalogServer) workerResults() factoryservice.WorkerResults {
	return factoryservice.WorkerResults{Assignments: s.deps.Assignments, Runs: s.deps.Runs, Current: s.deps.CurrentContext, Receipts: s.deps.CheckpointReceipts, Catalog: s.deps.Catalog, Now: s.now}
}

func (s *factoryCatalogServer) addCheckpointContext(ctx context.Context, read *readContext, body map[string]any, after string) error {
	until := time.Now().Add(10 * time.Second)
	for {
		execution, err := s.workerResults().CheckpointContext(ctx, read.WorkerContextRead)
		if err != nil {
			return err
		}
		if execution == nil {
			return nil
		}
		raw, err := json.Marshal(execution)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(raw)
		revision := hex.EncodeToString(digest[:])
		if after == "" || revision != after || !time.Now().Before(until) {
			if after != "" {
				if err := s.requireCurrentWork(ctx, read.Capability); err != nil {
					return err
				}
			}
			body["execution"], body["execution_revision"] = execution, revision
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
