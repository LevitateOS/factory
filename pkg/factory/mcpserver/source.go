package mcpserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/levitateos/factory/pkg/factory"
)

func (s *factoryCatalogServer) serveSource(ctx context.Context, args mcpCallArgs) (*mcp.CallToolResult, any, error) {
	cap, release, err := s.admitCall(ctx, factory.MCPToolReadSource, args)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	path, ok := args.Payload["path"].(string)
	if !ok || path == "" || len(args.Payload) != 1 || args.SubmissionID != "" {
		return nil, nil, errors.New("source read requires only payload.path")
	}
	if s.deps.ReadSource == nil {
		return nil, nil, errors.New("prepared source reader unavailable")
	}
	result, err := s.deps.ReadSource(ctx, cap, path)
	if err != nil {
		return nil, nil, err
	}
	return mcpJSONResult(result)
}
