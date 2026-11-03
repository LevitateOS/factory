package mcpserver

import (
	"context"

	"github.com/levitateos/factory/pkg/factory/mcpwire"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

func (s *factoryCatalogServer) workerInputs() factoryservice.WorkerInputs {
	source, _ := s.deps.Submissions.(factoryservice.WorkerCurrentContextSource)
	return factoryservice.WorkerInputs{Source: source, Verification: s.deps.VerificationEvidence, Now: s.now}
}

func (s *factoryCatalogServer) addCurrentContext(ctx context.Context, read *readContext, body map[string]any, args mcpCallArgs) error {
	inputs := s.workerInputs()
	current, err := inputs.Read(ctx, read.Capability)
	if err != nil {
		return err
	}
	if inputs.Source == nil {
		body["current"] = current
		markRequiredContextHold(body)
		return nil
	}
	if current.Run != nil && s.deps.Observed != nil {
		current.Run.ObservedState = "unavailable"
		observation, observeErr := s.observed(read.Capability)
		if observeErr == nil && !observation.Stopped && observation.RunID == read.Capability.RunID && observation.AssignmentID == read.Capability.AssignmentID && observation.Authority == read.Capability.Authority {
			current.Run.ObservedState = "confirmed"
		}
	}
	view, err := mcpwire.RenderCurrent(current, read.Capability, args.Payload, s.deps.boundCapability != nil && s.deps.NativeTask == nil)
	if err != nil {
		return err
	}
	body["current"], body["current_role_page"] = view.Current, view.Page
	if view.Hold {
		markRequiredContextHold(body)
	}
	return nil
}

func markRequiredContextHold(body map[string]any) {
	if selection, ok := body["context_selection"].(map[string]any); ok && selection["action"] != "stop" {
		selection["action"], selection["reason_code"] = "hold", "required_current_unavailable"
	}
}
