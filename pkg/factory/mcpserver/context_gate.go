package mcpserver

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
)

// requireCurrentWork reads shared required facts without rendering pages or
// changing the optional context-selection receipt.
func (s *factoryCatalogServer) requireCurrentWork(ctx context.Context, cap factory.MCPCapability) error {
	inputs := s.workerInputs()
	if inputs.Source == nil {
		return errors.New("required work inputs are unavailable")
	}
	current, err := inputs.Read(ctx, cap)
	if err != nil {
		return err
	}
	if current.Evidence.Availability != "complete" {
		return errors.New("required work inputs are unavailable; read factory.context for the missing inputs")
	}
	return nil
}
