package mcpserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/levitateos/factory/pkg/factory"
)

func (s *factoryCatalogServer) serveSource(ctx context.Context, tool factory.MCPTool, args mcpCallArgs) (*mcp.CallToolResult, any, error) {
	cap, release, err := s.admitCall(ctx, tool, args)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	path, ok := args.Payload["path"].(string)
	if !ok || tool == factory.MCPToolReadSource && path == "" || len(args.Payload) != 1 || args.SubmissionID != "" {
		return nil, nil, errors.New("source read requires only payload.path")
	}
	read := s.deps.ReadSource
	if tool == factory.MCPToolListSource {
		read = s.deps.ListSource
	}
	if read == nil {
		return nil, nil, errors.New("prepared source reader unavailable")
	}
	result, err := read(ctx, cap, path)
	if err != nil {
		return nil, nil, err
	}
	return mcpJSONResult(result)
}
