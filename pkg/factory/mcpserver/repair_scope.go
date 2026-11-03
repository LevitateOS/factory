package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/levitateos/factory/pkg/factory"
	factoryhttpapi "github.com/levitateos/factory/pkg/factory/httpapi"
)

type factoryMCPReassessmentScopeSource interface {
	ReassessmentScope(context.Context, factory.MCPCapability, int64) (factory.RepairScopeView, error)
}

type factoryMCPRepairObservationSource interface {
	ReadRepairObservation(context.Context, factory.MCPCapability, string, int) (factory.RepairObservationPage, error)
}

func (s *factoryCatalogServer) serveReassessmentScope(ctx context.Context, args mcpCallArgs) (*mcp.CallToolResult, any, error) {
	var payload struct {
		After  int64   `json:"after"`
		Cursor *string `json:"cursor"`
		Record string  `json:"record"`
		Offset int     `json:"offset"`
	}
	if args.Payload != nil {
		raw, err := json.Marshal(args.Payload)
		if err != nil {
			return nil, nil, err
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&payload) != nil || payload.After < 0 || payload.Cursor != nil && len(*payload.Cursor) > 1024 || payload.Offset < 0 || payload.Record != "" && !factory.ValidID(payload.Record) || payload.Record == "" && payload.Offset != 0 {
			return nil, nil, errors.New("invalid reassessment scope cursor")
		}
	}
	read, err := s.loadReadContext(ctx, factory.MCPToolReassessmentScope, args)
	if err != nil {
		return nil, nil, err
	}
	defer read.release()
	source, ok := s.deps.Submissions.(factoryMCPReassessmentScopeSource)
	if !ok {
		return nil, nil, errors.New("reassessment scope is unavailable")
	}
	view, err := source.ReassessmentScope(ctx, read.Capability, payload.After)
	if err != nil {
		return nil, nil, err
	}
	foregrounded, reason, fingerprint := s.workerContext().Foreground(ctx, read.WorkerContextRead)
	body := read.Projection(foregrounded)
	body["route"] = factoryhttpapi.WorkerRoute
	action := "serve"
	if reason != "" {
		action = "hold"
	}
	body["context_selection"] = map[string]any{"bundle": foregrounded, "action": action, "reason_code": reason, "fingerprint": fingerprint}
	body["reassessment_scope"] = view
	if payload.Record != "" {
		reader, ok := s.deps.Submissions.(factoryMCPRepairObservationSource)
		if !ok {
			return nil, nil, errors.New("repair observation is unavailable")
		}
		page, err := reader.ReadRepairObservation(ctx, read.Capability, payload.Record, payload.Offset)
		if err != nil {
			return nil, nil, err
		}
		body["record_page"] = page
	}
	contextArgs := args
	contextArgs.Payload = nil
	if payload.Cursor != nil {
		contextArgs.Payload = map[string]any{"cursor": *payload.Cursor}
	}
	if err := s.addCurrentContext(ctx, read, body, contextArgs); err != nil {
		return nil, nil, err
	}
	return mcpJSONResult(body)
}
