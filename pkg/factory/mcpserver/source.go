package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/project"
	"github.com/levitateos/factory/pkg/strictjson"
)

func (s *factoryCatalogServer) serveSource(ctx context.Context, tool factory.MCPTool, args mcpCallArgs) (*mcp.CallToolResult, any, error) {
	cap, release, err := s.admitCall(ctx, tool, args)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	if args.SubmissionID != "" {
		return nil, nil, errors.New("source inspection takes no submission_id")
	}
	var result map[string]any
	if tool == factory.MCPToolListSource {
		path, ok := args.Payload["path"].(string)
		if !ok || len(args.Payload) != 1 {
			return nil, nil, errors.New("source listing requires only payload.path")
		}
		if s.deps.ListSource == nil {
			return nil, nil, errors.New("prepared source listing unavailable")
		}
		result, err = s.deps.ListSource(ctx, cap, path)
	} else {
		selection := project.FactoryContextReadPage{Limit: project.DefaultFactorySourcePageBytes}
		raw, encodeErr := json.Marshal(args.Payload)
		if encodeErr != nil || strictjson.Decode(bytes.NewReader(raw), &selection) != nil || selection.Validate() != nil {
			return nil, nil, errors.New("source read requires a relative path, optional byte offset and limit from 4 to 32768")
		}
		if s.deps.ReadSource == nil {
			return nil, nil, errors.New("prepared source reader unavailable")
		}
		result, err = s.deps.ReadSource(ctx, cap, selection)
	}
	if err != nil {
		return nil, nil, err
	}
	return mcpJSONResult(result)
}
