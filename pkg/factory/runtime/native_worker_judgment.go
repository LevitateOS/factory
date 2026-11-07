package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	"github.com/levitateos/factory/pkg/factory/service"
	"github.com/levitateos/factory/pkg/store"
)

// ConsultationsForWorker binds funding and admission to the authenticated worker,
// never to a repository or account supplied in its question. Each explicit tool
// invocation gets its own command; polling that command cannot repeat inference.
func (p NativeExecution) ConsultationsForWorker(cap factory.MCPCapability) service.WorkerSystemOne {
	chooser := nativeRepositoryJudgment{NativeExecution: p, repository: cap.Repository, subject: "consultation:" + factory.NewID(), worker: &cap}
	chooser.admit = func(ctx context.Context) error {
		_, err := (service.WorkerAdmission{Store: p.Store, Inspect: p.InspectRun}).Prepare(ctx, cap)
		return err
	}
	return nativeWorkerJudgments{chooser}
}

type nativeWorkerJudgments struct{ nativeRepositoryJudgment }

func (s nativeWorkerJudgments) Evaluate(ctx context.Context, kind string, state, instructions, criteria any) (map[string]any, error) {
	result, err := s.EvaluateQuestions(ctx, kind, state, map[string]factory.SystemOneWorkerQuestion{"judgment": {Instructions: instructions, Criteria: criteria}})
	if err != nil {
		return nil, err
	}
	answers := result["answers"].(map[string]any)
	result["answer"] = answers["judgment"]
	delete(result, "answers")
	return result, nil
}

func (s nativeWorkerJudgments) EvaluateQuestions(ctx context.Context, kind string, state any, questions map[string]factory.SystemOneWorkerQuestion) (map[string]any, error) {
	prompt, err := factory.NativeConsultationPrompt(kind, state, questions)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(30 * time.Second)
	if s.worker != nil && time.Unix(s.worker.DeadlineUnix, 0).Before(deadline) {
		deadline = time.Unix(s.worker.DeadlineUnix, 0)
	}
	bounded, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	for {
		outcome, err := s.infer(bounded, prompt, "", "consultation", nil)
		if err == nil {
			if outcome.Output == nil {
				return nil, control.ErrPriorityUnavailable
			}
			var envelope struct {
				Answers map[string]map[string]any `json:"answers"`
			}
			if nativeDecode(outcome.Output, &envelope) != nil || len(envelope.Answers) != len(questions) {
				return nil, errors.New("consultation returned a different question set")
			}
			answers := make(map[string]any, len(questions))
			for id, question := range questions {
				answer := envelope.Answers[id]
				if answer["type"] != kind {
					return nil, errors.New("consultation returned a different question type")
				}
				if err := control.ValidateSystemOneAnswer(kind, answer, question.Criteria); err != nil {
					return nil, err
				}
				answers[id] = answer
			}
			// The immutable command, rather than a currently selected account,
			// identifies the model and actual elapsed usage of this answer.
			cmd, err := s.Store.FactoryCommand(bounded, outcome.Process.Run)
			var capture factory.NativeJudgmentCapture
			if err != nil || nativeDecode([]byte(cmd.Payload), &capture) != nil {
				return nil, control.ErrPriorityUnavailable
			}
			return map[string]any{"answers": answers, "model": capture.Selection.Model, "usage": map[string]any{"elapsed_milliseconds": *outcome.Process.StoppedUnixMS - outcome.Process.StartedUnixMS}}, nil
		}
		if !errors.Is(err, service.ErrPostJudgmentPending) {
			return nil, err
		}
		cmd, readErr := s.Store.FactoryCommand(bounded, factory.NativeJudgmentID(s.Config.Profile, s.repository, s.subject))
		if errors.Is(readErr, store.ErrNotFound) {
			return nil, errors.New("selected judgment provider is not configured or available")
		}
		if readErr != nil {
			return nil, readErr
		}
		var capture factory.NativeJudgmentCapture
		if nativeDecode([]byte(cmd.Payload), &capture) != nil || s.Store.CheckNativeJudgmentSource(bounded, capture) != nil {
			return nil, control.ErrPriorityUnavailable
		}
		if cmd.Finished != "" {
			continue
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-bounded.Done():
			timer.Stop()
			return nil, bounded.Err()
		case <-timer.C:
		}
	}
}
