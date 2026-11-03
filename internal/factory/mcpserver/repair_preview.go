package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/levitateos/sodaos/internal/factory"
	"github.com/levitateos/sodaos/internal/factory/mcpwire"
)

func (s *factoryCatalogServer) serveRepairPreview(ctx context.Context, args mcpCallArgs) (*mcp.CallToolResult, any, error) {
	cap, release, err := s.admitCall(ctx, factory.MCPToolPreviewRepair, args)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	if s.deps.RepairPreview == nil {
		return nil, nil, errors.New("repair preview is unavailable")
	}
	raw, err := mcpwire.CanonicalPayload(args.Payload)
	if err != nil {
		return nil, nil, err
	}
	var payload struct {
		Report json.RawMessage `json:"report"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&payload) != nil || len(payload.Report) == 0 || bytes.Equal(payload.Report, []byte("null")) {
		return nil, nil, errors.New("repair preview needs a typed report")
	}
	preview, err := s.deps.RepairPreview(ctx, cap, payload.Report)
	if err != nil {
		return nil, nil, err
	}
	return mcpJSONResult(preview)
}
