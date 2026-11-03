package mcpserver

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/levitateos/sodaos/internal/factory"
	factoryservice "github.com/levitateos/sodaos/internal/factory/service"
	"github.com/levitateos/sodaos/internal/store"
)

type factoryMCPPostWriter interface {
	RecordWorkerPost(context.Context, factory.MCPSubmission) (store.FeedPostDetail, error)
}

func (s *factoryCatalogServer) serveWorkerPost(ctx context.Context, tool factory.MCPTool, args mcpCallArgs) (*mcp.CallToolResult, any, error) {
	cap, release, err := s.admitCall(ctx, tool, args)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	s.workerResults().Bind(ctx, cap, tool, args.Payload)
	writer, ok := s.deps.Submissions.(factoryMCPPostWriter)
	if !ok {
		return nil, nil, errors.New("worker posts are unavailable")
	}
	raw, err := json.Marshal(args.Payload)
	if err != nil {
		return nil, nil, err
	}
	post, err := writer.RecordWorkerPost(ctx, factory.MCPSubmission{AssignmentID: cap.AssignmentID, CapabilityID: cap.ID, SubmissionID: args.SubmissionID, Tool: tool, Payload: raw})
	if err != nil {
		return nil, nil, err
	}
	return mcpJSONResult(factoryservice.WorkerPostResponse(post))
}
