package mcpserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/mcpwire"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

type systemOneArgs struct {
	factoryservice.WorkerConsultationInput
	CapabilityID string `json:"capability_id,omitempty"`
}

func (s *factoryCatalogServer) addSystemOneTool(server *mcp.Server, tool factory.MCPTool) {
	mcp.AddTool(server, mcpwire.Tool(tool, s.deps.boundCapability), func(ctx context.Context, _ *mcp.CallToolRequest, args systemOneArgs) (*mcp.CallToolResult, any, error) {
		cap, release, err := s.admitCall(ctx, tool, mcpCallArgs{CapabilityID: args.CapabilityID})
		if err != nil {
			return nil, nil, err
		}
		defer release()
		// Preserve the transport's complete envelope bound, including identity in
		// the unbound protocol fixture. The shared service also bounds content.
		raw, err := json.Marshal(args)
		if err != nil || len(raw) > factory.MCPMaxSubmissionPayload {
			return nil, nil, errors.New("System One question exceeds 64 KiB")
		}
		journal, _ := s.deps.PriorityJudgments.(factoryservice.WorkerConsultationJournal)
		consultations := factoryservice.WorkerConsultations{Provider: s.deps.SystemOne, Journal: journal, Now: s.deps.Now}
		result, err := consultations.Evaluate(ctx, cap, tool, args.WorkerConsultationInput)
		if err != nil {
			return nil, nil, err
		}
		return mcpJSONResult(result)
	})
}
