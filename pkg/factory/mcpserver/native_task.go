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

func (s *factoryCatalogServer) serveWorkspace(ctx context.Context, args mcpCallArgs) (*mcp.CallToolResult, any, error) {
	cap, release, err := s.admitCall(ctx, factory.MCPToolWorkspace, args)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	var input project.FactoryNativeTaskArguments
	raw, err := json.Marshal(args.Payload)
	if err != nil || strictjson.Decode(bytes.NewReader(raw), &input) != nil || input.OperationID != "" {
		return nil, nil, errors.New("invalid workspace payload")
	}
	if input.Operation == "read" && input.Limit == 0 {
		input.Limit = 8192
	}
	if input.Operation != "read" {
		if !factory.ValidID(args.SubmissionID) {
			return nil, nil, errors.New("workspace mutations require a stable submission_id; reuse it for exact retries")
		}
		input.OperationID = args.SubmissionID
	}
	if input.Operation == "edit" {
		if _, ok := args.Payload["expected"]; !ok {
			return nil, nil, errors.New("workspace edit requires expected text, including an empty string for insertion")
		}
		if _, ok := args.Payload["replacement"]; !ok {
			return nil, nil, errors.New("workspace edit requires replacement text")
		}
	}
	if err := input.ValidateWorkspace(); err != nil {
		return nil, nil, err
	}
	if s.deps.Workspace == nil {
		return nil, nil, errors.New("native task workspace unavailable")
	}
	if err := s.requireCurrentWork(ctx, cap); err != nil {
		return nil, nil, err
	}
	result, err := s.deps.Workspace(ctx, cap, input)
	if err != nil {
		return nil, nil, err
	}
	return mcpJSONResult(result)
}
