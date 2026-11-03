package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/levitateos/factory/pkg/factory"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

func (s *factoryCatalogServer) serveLibraryRead(ctx context.Context, tool factory.MCPTool, args mcpCallArgs) (*mcp.CallToolResult, any, error) {
	cap, release, err := s.admitCall(ctx, tool, args)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	source, ok := s.deps.Submissions.(factoryservice.LibrarySource)
	if !ok {
		return nil, nil, errors.New("library is unavailable")
	}
	var in struct {
		Query       string `json:"query"`
		Kind        string `json:"kind"`
		Cursor      string `json:"cursor"`
		ID          string `json:"id"`
		CurrentOnly bool   `json:"current_only"`
	}
	raw, err := json.Marshal(args.Payload)
	if err != nil {
		return nil, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&in); err != nil {
		return nil, nil, err
	}
	if tool == factory.MCPToolReadLibraryItem {
		if !factory.ValidID(in.ID) {
			return nil, nil, errors.New("library item id required")
		}
		item, err := source.LibraryRead(ctx, cap.Repository, in.ID, false)
		if err != nil {
			return nil, nil, err
		}
		return mcpJSONResult(item)
	}
	page, err := source.LibrarySearch(ctx, cap.Repository, in.Query, in.Kind, in.Cursor, false, in.CurrentOnly)
	if err != nil {
		return nil, nil, err
	}
	return mcpJSONResult(page)
}
