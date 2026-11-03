package control

import (
	"context"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/execution"
)

func (c *Coordinator) exportCandidate(ctx context.Context, p factory.Publication, run factory.Run) ([]byte, *exportTerminal, *planWait) {
	state, err := c.Host.ExportCandidate(ctx, execution.CandidateRequest{Address: execution.RunAddress{Workspace: p.ProjectID, Run: p.Run}, Role: p.Role, Preparation: p.Preparation, Candidate: p.Candidate})
	if err != nil {
		if errors.Is(err, execution.ErrCandidateInvalid) || errors.Is(err, execution.ErrCandidateBounds) || isHostNotFound(err) || isHostStale(err) {
			return nil, &exportTerminal{Reason: factory.PublishReasonExportFailed}, nil
		}
		return nil, nil, waitFor("host_unavailable", "candidate export unconfirmed")
	}
	if state.Address.Run != p.Run || state.Address.Workspace != p.ProjectID || state.Candidate != p.Candidate {
		return nil, nil, waitFor("host_unavailable", "candidate export identity differs")
	}
	return state.Bundle, nil, nil
}

func isHostStale(err error) bool { return errors.Is(err, execution.ErrRunChanged) }
