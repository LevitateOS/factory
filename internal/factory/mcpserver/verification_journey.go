package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/levitateos/sodaos/internal/factory"
)

type factoryMCPVerificationJourneySource interface {
	VerificationScope(context.Context, factory.MCPCapability, int64) (factory.VerificationScopeView, error)
}

func (s *factoryCatalogServer) serveVerificationScope(ctx context.Context, args mcpCallArgs) (*mcp.CallToolResult, any, error) {
	cap, release, err := s.admitCall(ctx, factory.MCPToolVerificationScope, args)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	source, ok := s.deps.Submissions.(factoryMCPVerificationJourneySource)
	if !ok {
		return nil, nil, errors.New("verification scope is unavailable")
	}
	var page struct {
		After int64 `json:"after"`
	}
	if args.Payload != nil {
		raw, err := json.Marshal(args.Payload)
		if err != nil {
			return nil, nil, err
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&page) != nil || page.After < 0 {
			return nil, nil, errors.New("invalid verification scope cursor")
		}
	}
	view, err := source.VerificationScope(ctx, cap, page.After)
	if err != nil {
		return nil, nil, err
	}
	return mcpJSONResult(view)
}
