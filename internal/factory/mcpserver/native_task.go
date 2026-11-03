package mcpserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/levitateos/sodaos/internal/factory"
)

func (s *factoryCatalogServer) serveNativeTask(ctx context.Context, tool factory.MCPTool, args mcpCallArgs) (*mcp.CallToolResult, any, error) {
	cap, release, err := s.admitCall(ctx, tool, args)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	taskID, ok := args.Payload["task_id"].(string)
	if !ok || !factory.ValidPlanKey(taskID) || len(args.Payload) != 1 || args.SubmissionID != "" {
		return nil, nil, errors.New("native task request requires only payload.task_id")
	}
	if _, frozen, err := s.deps.Submissions.TerminalMCPProposal(ctx, cap.AssignmentID); err != nil {
		return nil, nil, err
	} else if frozen {
		return nil, nil, errors.New("assignment already recorded a terminal proposal")
	}
	if s.deps.NativeTask == nil {
		return nil, nil, errors.New("live native task controller unavailable")
	}
	if err := s.requireCurrentWork(ctx, cap); err != nil {
		return nil, nil, err
	}
	action := "start_task"
	if tool == factory.MCPToolReleaseOutput {
		action = "release_output"
	}
	// Admission and output recovery belong to the live native task owner. Do
	// not create a second submission ledger or infer a grant from model prose.
	result, err := s.deps.NativeTask(ctx, cap, action, taskID)
	if err != nil {
		return nil, nil, err
	}
	return mcpJSONResult(result)
}
