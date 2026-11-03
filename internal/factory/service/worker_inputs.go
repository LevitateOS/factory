package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

// WorkerInputs reads required work facts for an already admitted worker. It
// joins existing verification observations without a judgment or workflow write.
// Transport owns capability admission and wire rendering.
type WorkerInputs struct {
	Source       WorkerCurrentContextSource
	Verification func(context.Context, factory.MCPCapability) (map[string]any, error)
	Now          func() time.Time
}

func (s WorkerInputs) Read(ctx context.Context, cap factory.MCPCapability) (factory.CurrentContext, error) {
	if err := ctx.Err(); err != nil {
		return factory.CurrentContext{}, err
	}
	if s.Source == nil {
		return factory.CurrentContext{Version: "context-current-v1", Role: factory.CurrentRoleContext{Kind: cap.Profile}, Evidence: factory.CurrentContextEvidence{RequiredStreams: []string{"required_current"}, Availability: "unknown", MissingReasons: []string{"required_current_source_unwired"}}}, nil
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	current, err := s.Source.FactoryCurrentContext(ctx, cap, now)
	if err != nil {
		return factory.CurrentContext{}, err
	}
	if cap.Profile == factory.MCPProfileTestPilot {
		if err := s.joinVerification(ctx, cap, &current); err != nil {
			return factory.CurrentContext{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return factory.CurrentContext{}, err
	}
	return current, nil
}

func (s WorkerInputs) joinVerification(ctx context.Context, cap factory.MCPCapability, current *factory.CurrentContext) error {
	missing := func() {
		current.Evidence.Availability = "incomplete"
		current.Evidence.MissingReasons = append(current.Evidence.MissingReasons, "current_native_verification_unavailable")
	}
	if current.Role.Delivery == nil || s.Verification == nil {
		missing()
		return nil
	}
	body, err := s.Verification(ctx, cap)
	if err != nil {
		return err
	}
	if body["available"] != true {
		missing()
		return nil
	}
	raw, err := json.Marshal(body["evidence"])
	if err != nil {
		return err
	}
	var evidence factory.CurrentVerificationEvidence
	if json.Unmarshal(raw, &evidence) != nil || evidence.ValidateFor(*current.Role.Delivery) != nil {
		return errors.New("current native verification is malformed")
	}
	if e := evidence.Execution; e != nil && (e.Run != cap.RunID || e.Project != cap.Project) {
		return errors.New("current native verification belongs to another worker")
	}
	current.Role.Verification = &evidence
	missingReasons := []string{}
	for _, reason := range current.Evidence.MissingReasons {
		if reason != "current_native_verification_unavailable" {
			missingReasons = append(missingReasons, reason)
		}
	}
	current.Evidence.MissingReasons = missingReasons
	if len(missingReasons) == 0 {
		current.Evidence.Availability = "complete"
	}
	return nil
}
