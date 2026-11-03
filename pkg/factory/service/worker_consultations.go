package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/store"
)

// WorkerSystemOne supplies judgments, never workflow or execution authority.
type WorkerSystemOne interface {
	Evaluate(context.Context, string, any, any, any) (map[string]any, error)
	EvaluateQuestions(context.Context, string, any, map[string]factory.SystemOneWorkerQuestion) (map[string]any, error)
}

type WorkerConsultationJournal interface {
	RecordConsultationRequest(context.Context, store.SystemOneConsultation) error
	RecordConsultationResult(context.Context, string, string, string, map[string]any) error
}

// WorkerConsultationInput contains content only. Identity comes from the
// authenticated capability, outside this object and the worker's request body.
type WorkerConsultationInput struct {
	State             any                                        `json:"state"`
	Instructions      any                                        `json:"instructions"`
	Criteria          any                                        `json:"criteria"`
	Questions         map[string]factory.SystemOneWorkerQuestion `json:"questions,omitempty"`
	ConsultationGroup string                                     `json:"consultation_group,omitempty"`
}

// WorkerConsultations owns bounded question validation, provider dispatch and
// existing work-discussion recording. Callers supply an already admitted worker.
// Production supplies the store journal; journal-less callers retain the existing
// unrecorded advice behavior. No provider call occurs during construction.
type WorkerConsultations struct {
	Provider WorkerSystemOne
	Journal  WorkerConsultationJournal
	Now      func() time.Time
}

func (s WorkerConsultations) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Evaluate issues one single/batch consultation. Explicit repeats call the
// provider again; the conversation snapshot is not a funding/replay receipt.
func (s WorkerConsultations) Evaluate(ctx context.Context, cap factory.MCPCapability, tool factory.MCPTool, input WorkerConsultationInput) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cap.Validate() != nil || cap.State != factory.MCPCapabilityActive || cap.Expired(s.now()) || !factory.MCPToolAdmitted(cap.Profile, tool) {
		return nil, ErrWorkerBinding
	}
	switch tool {
	case factory.MCPToolSystemOneNoul, factory.MCPToolSystemOneChoice, factory.MCPToolSystemOneScore:
	default:
		return nil, errors.New("unsupported System One consultation tool")
	}
	if input.ConsultationGroup != "" && !factory.ValidID(input.ConsultationGroup) {
		return nil, errors.New("consultation group must be a 32-character lowercase hex identity")
	}
	raw, err := json.Marshal(input)
	if err != nil || len(raw) > factory.MCPMaxSubmissionPayload {
		return nil, errors.New("System One question exceeds 64 KiB")
	}
	kind := strings.TrimPrefix(string(tool), "systemone.")
	var snapshot string
	var questions map[string]any
	var call func() (map[string]any, error)
	if len(input.Questions) > 0 {
		if input.Instructions != nil || input.Criteria != nil {
			return nil, errors.New("single question and batch questions are mutually exclusive")
		}
		if len(input.Questions) > factory.SystemOneMaxBatchQuestions {
			return nil, errors.New("System One batch needs 1 to 8 questions")
		}
		for _, question := range input.Questions {
			if err := consultationInstructions(question.Instructions); err != nil {
				return nil, err
			}
		}
		snapshot, err = factory.SystemOneWorkerBatchSnapshot(cap.ID, tool, input.State, input.Questions)
		questions = make(map[string]any, len(input.Questions))
		for id, question := range input.Questions {
			questions[id] = map[string]any{"instructions": question.Instructions, "criteria": question.Criteria}
		}
		call = func() (map[string]any, error) {
			return s.Provider.EvaluateQuestions(ctx, kind, input.State, input.Questions)
		}
	} else {
		if err := consultationInstructions(input.Instructions); err != nil {
			return nil, err
		}
		if kind == "choice" {
			criteria, ok := input.Criteria.(map[string]any)
			if !ok || len(criteria) < 2 || len(criteria) > 255 {
				return nil, errors.New("choice requires 2–255 alternatives")
			}
		}
		if kind == "score" {
			levels, ok := input.Criteria.([]any)
			if !ok || len(levels) < 2 || len(levels) > 10 {
				return nil, errors.New("score requires 2–10 levels")
			}
		}
		snapshot, err = factory.SystemOneWorkerSnapshot(cap.ID, tool, input.State, input.Instructions, input.Criteria)
		call = func() (map[string]any, error) {
			return s.Provider.Evaluate(ctx, kind, input.State, input.Instructions, input.Criteria)
		}
	}
	if err != nil {
		return nil, errors.New("System One question is not hashable")
	}
	group := input.ConsultationGroup
	if group == "" {
		group = snapshot
	}
	if s.Journal != nil {
		revision := cap.AcceptedRevision
		if revision == "" {
			revision = cap.SourceDigest
		}
		err = s.Journal.RecordConsultationRequest(ctx, store.SystemOneConsultation{Repository: cap.Repository, Role: cap.Profile, FeedConsultation: factory.FeedConsultation{Snapshot: snapshot, GroupID: group, Assignment: cap.AssignmentID, SourceRevision: revision, Tool: string(tool), Kind: kind, State: input.State, Instructions: input.Instructions, Criteria: input.Criteria, Questions: questions, Status: store.ConsultationStatusPending}})
		if err != nil {
			return nil, err
		}
	}
	if s.Provider == nil {
		err = errors.New("System One provider is not configured")
		return nil, errors.Join(err, s.record(ctx, snapshot, nil, err))
	}
	answer, err := call()
	if err != nil {
		return nil, errors.Join(err, s.record(ctx, snapshot, nil, err))
	}
	if err := s.record(ctx, snapshot, answer, nil); err != nil {
		return nil, err
	}
	result := make(map[string]any, len(answer)+1)
	for key, value := range answer {
		result[key] = value
	}
	result["consultation"] = map[string]any{"post_id": factory.FeedConsultationPostID(snapshot), "work_post": map[string]any{"kind": "record", "id": cap.AssignmentID}, "follow_up": "Reply to this consultation with your interpretation and chosen next action. Use factory.record_decision for a durable decision. JEV output above is advice, not an implemented or authorized change."}
	return result, nil
}

func consultationInstructions(value any) error {
	if value == nil {
		return errors.New("instructions are required")
	}
	if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
		return errors.New("instructions are empty")
	}
	return nil
}

func (s WorkerConsultations) record(ctx context.Context, snapshot string, result map[string]any, callErr error) error {
	if s.Journal == nil {
		return nil
	}
	// A worker disconnect must not erase an actual answer or an uncertain call.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if callErr != nil {
		status := store.ConsultationStatusFailed
		if strings.Contains(callErr.Error(), "not configured") {
			status = store.ConsultationStatusUnavailable
		}
		if errors.Is(callErr, context.DeadlineExceeded) || errors.Is(callErr, context.Canceled) || strings.Contains(callErr.Error(), "request failed") {
			status = store.ConsultationStatusUncertain
		}
		return s.Journal.RecordConsultationResult(writeCtx, snapshot, "", status, nil)
	}
	model := ""
	if result != nil {
		model, _ = result["model"].(string)
	}
	return s.Journal.RecordConsultationResult(writeCtx, snapshot, model, store.ConsultationStatusReturned, result)
}
