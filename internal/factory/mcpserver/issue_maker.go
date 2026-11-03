package mcpserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/mcpwire"
	"github.com/levitateos/sodaos/internal/store"
)

func (s *factoryCatalogServer) serveIssueMaker(ctx context.Context, tool factory.MCPTool, args mcpCallArgs) (*mcp.CallToolResult, any, error) {
	cap, release, err := s.admitCall(ctx, tool, args)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	if err := mcpwire.CheckPayload(tool, args.Payload); err != nil {
		return nil, nil, err
	}
	if _, frozen, err := s.deps.Submissions.TerminalMCPProposal(ctx, cap.AssignmentID); err != nil {
		return nil, nil, err
	} else if frozen {
		return nil, nil, store.ErrMCPSubmissionFrozen
	}
	if s.deps.IssueMaker == nil {
		return nil, nil, errors.New("Issue Maker native tools are unavailable")
	}
	if err := s.requireCurrentWork(ctx, cap); err != nil {
		return nil, nil, err
	}
	result, err := s.deps.IssueMaker(ctx, cap, tool, args.Payload, args.SubmissionID)
	if err != nil {
		return nil, nil, err
	}
	return mcpJSONResult(result)
}
