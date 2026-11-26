package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

// NativeJEV is Factory's one way to ask JEV. Every product decision (Feed
// routing, planning, next work, context, contributions, delivery) and every
// agent consultation, including plan review, goes through ask. Rust holds the
// key and the per-Mac gate. ask journals every request in factory_commands,
// so a paid answer is reused, a waiting request starts again later, and a
// request TypeSafe refused is never sent again. Purpose names the caller.
type NativeJEV struct {
	NativeExecution
	Purpose string
}

var (
	_ control.ProductJudgment       = NativeJEV{}
	_ control.PriorityChooser       = NativeJEV{}
	_ factoryservice.WorkerSystemOne = NativeJEV{}
)

// For names the caller whose decisions the journal records.
func (j NativeJEV) For(purpose string) NativeJEV {
	j.Purpose = purpose
	return j
}

// jevReceipt is the outcome column of one jev.ask row.
type jevReceipt struct {
	// asking, waiting, answered, refused (TypeSafe will not take this request)
	// or problem (an unexpected reply).
	State    string          `json:"state"`
	Reason   string          `json:"reason,omitempty"`
	RetryAt  int64           `json:"retry_at,omitempty"`
	Attempts int             `json:"attempts"`
	Status   int             `json:"http_status,omitempty"`
	Detail   string          `json:"detail,omitempty"`
	Reply    json.RawMessage `json:"reply,omitempty"`
	// Answer is the decoded Choice of rows written before replies were kept.
	Answer *factory.PriorityAnswer `json:"answer,omitempty"`
}

type jevStatus struct {
	State        string `json:"state"`
	DefaultModel string `json:"default_model"`
	Problem      *struct {
		Class   string `json:"class"`
		RetryAt int64  `json:"retry_at"`
	} `json:"problem"`
}

type jevAsked struct {
	Outcome    string          `json:"outcome"`
	RetryAt    int64           `json:"retry_at"`
	HTTPStatus int             `json:"http_status"`
	Detail     string          `json:"detail"`
	Reply      json.RawMessage `json:"reply"`
}

// jevAnswer is one settled ask: TypeSafe's reply and the journal row holding it.
type jevAnswer struct {
	Reply   json.RawMessage
	Journal string
	legacy  *factory.PriorityAnswer
}

func (j NativeJEV) jev(ctx context.Context, payload, result any) error {
	var raw json.RawMessage
	if err := nativeHostCall(ctx, j.Config, j.Instance, "/api/native/jev", payload, &raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, result)
}

// ask sends one TypeSafe request of up to eight questions (ID → {type,
// instructions, criteria}). It returns *control.JEVWait while the request waits
// for a key, for TypeSafe or for the person to change the request.
func (j NativeJEV) ask(ctx context.Context, state any, questions map[string]any) (jevAnswer, error) {
	var empty jevAnswer
	if j.Purpose == "" {
		return empty, errors.New("JEV purpose is unnamed")
	}
	var status jevStatus
	if err := j.jev(ctx, map[string]string{"operation": "inspect_jev"}, &status); err != nil {
		return empty, &control.JEVWait{Reason: "jev_not_connected"}
	}
	switch status.State {
	case "connected":
	case "refused":
		return empty, &control.JEVWait{Reason: "jev_key_refused"}
	default:
		return empty, &control.JEVWait{Reason: "jev_not_connected"}
	}
	model := status.DefaultModel
	sent, err := json.Marshal(map[string]any{"purpose": j.Purpose, "model": model, "state": state, "questions": questions})
	if err != nil {
		return empty, err
	}
	sum := sha256.Sum256(sent)
	digest := hex.EncodeToString(sum[:])
	payload, _ := json.Marshal(map[string]string{"purpose": j.Purpose, "model": model})
	now := time.Now()
	cmd, err := j.Store.BeginJEVAsk(ctx, factory.Command{ID: digest[:32], Type: factory.CommandJEVAsk, Target: "jev/" + j.Purpose,
		Principal: "native:" + string(j.Config.Profile), Digest: digest, Payload: string(payload), Created: now.UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return empty, err
	}
	var receipt jevReceipt
	if cmd.Outcome != "" && json.Unmarshal([]byte(cmd.Outcome), &receipt) != nil {
		return empty, errors.New("JEV journal row is unreadable")
	}
	if cmd.Finished != "" {
		return settledJEV(cmd.ID, receipt)
	}
	note := func(finished bool) error {
		raw, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		stamp := ""
		if finished {
			stamp = time.Now().UTC().Format(time.RFC3339Nano)
		}
		return j.Store.NoteJEVAsk(ctx, cmd.ID, string(raw), stamp)
	}
	wait := func(reason string, retryAt int64) (jevAnswer, error) {
		receipt.State, receipt.Reason, receipt.RetryAt = "waiting", reason, retryAt
		if err := note(false); err != nil {
			return empty, err
		}
		return empty, &control.JEVWait{Reason: reason, RetryAt: retryAt}
	}
	if receipt.State == "waiting" && receipt.RetryAt > now.Unix() {
		return empty, &control.JEVWait{Reason: receipt.Reason, RetryAt: receipt.RetryAt}
	}
	if status.Problem != nil && status.Problem.RetryAt > now.Unix() {
		return wait("jev_"+status.Problem.Class, status.Problem.RetryAt)
	}
	// Recorded before sending: TypeSafe has no idempotency key, so a crash
	// here sends once more and the receipt counts both attempts.
	receipt.Attempts++
	receipt.State, receipt.Reason, receipt.RetryAt = "asking", "", 0
	if err := note(false); err != nil {
		return empty, err
	}
	var asked jevAsked
	if err := j.jev(ctx, map[string]any{"operation": "jev_ask", "model": model, "state": state, "questions": questions}, &asked); err != nil {
		return wait("jev_offline", time.Now().Unix()+60)
	}
	receipt.Status, receipt.Detail = asked.HTTPStatus, asked.Detail
	switch asked.Outcome {
	case "answered":
		receipt.State, receipt.Reply = "answered", asked.Reply
		if err := note(true); err != nil {
			return empty, err
		}
		return jevAnswer{Reply: asked.Reply, Journal: cmd.ID}, nil
	case "busy", "offline":
		return wait("jev_"+asked.Outcome, asked.RetryAt)
	case "not_connected", "key_refused":
		return wait("jev_"+asked.Outcome, 0)
	case "too_large", "rejected":
		receipt.State, receipt.Reason = "refused", "jev_request_refused"
		if asked.Outcome == "too_large" {
			receipt.Detail = "too_large"
		}
		if err := note(true); err != nil {
			return empty, err
		}
		return settledJEV(cmd.ID, receipt)
	default:
		receipt.State = "problem"
		_ = note(true)
		return settledJEV(cmd.ID, receipt)
	}
}

// settledJEV replays a finished journal row without contacting TypeSafe.
func settledJEV(id string, receipt jevReceipt) (jevAnswer, error) {
	switch receipt.State {
	case "answered":
		if len(receipt.Reply) != 0 || receipt.Answer != nil {
			return jevAnswer{Reply: receipt.Reply, Journal: id, legacy: receipt.Answer}, nil
		}
	case "refused":
		return jevAnswer{}, &control.JEVWait{Reason: "jev_request_refused", Detail: receipt.Detail}
	}
	return jevAnswer{}, fmt.Errorf("JEV answered with an unexpected error (HTTP %d)", receipt.Status)
}

// jevChoiceKey is the question ID of a single-Choice ask.
const jevChoiceKey = "choice"

// Select asks one Choice for a product decision.
func (j NativeJEV) Select(ctx context.Context, state any, question string, choices map[string]any) (factory.PriorityAnswer, error) {
	settled, err := j.ask(ctx, state, map[string]any{jevChoiceKey: map[string]any{"type": "choice", "instructions": question, "criteria": choices}})
	if err != nil {
		return factory.PriorityAnswer{}, err
	}
	if len(settled.Reply) == 0 && settled.legacy != nil {
		return *settled.legacy, nil
	}
	return control.DecodeChoiceReply(settled.Reply, jevChoiceKey, choices)
}

// Choose picks the next work among one repository's candidates.
func (j NativeJEV) Choose(ctx context.Context, request factory.PriorityRequest) (factory.PriorityAnswer, error) {
	if len(request.Candidates) < 2 || len(request.Candidates) > 255 {
		return factory.PriorityAnswer{}, control.ErrPriorityUnavailable
	}
	options := make(map[string]any, len(request.Candidates))
	for _, candidate := range request.Candidates {
		if candidate.ID == "" {
			return factory.PriorityAnswer{}, control.ErrPriorityUnavailable
		}
		options[candidate.ID] = candidate.PreferenceFacts()
	}
	return j.Select(ctx, map[string]any{"snapshot": request.Snapshot, "repository": request.Repository}, request.Rubric, options)
}

// Evaluate answers one agent consultation question.
func (j NativeJEV) Evaluate(ctx context.Context, kind string, state, instructions, criteria any) (map[string]any, error) {
	result, err := j.EvaluateQuestions(ctx, kind, state, map[string]factory.SystemOneWorkerQuestion{"judgment": {Instructions: instructions, Criteria: criteria}})
	if err != nil {
		return nil, err
	}
	answers := result["answers"].(map[string]any)
	result["answer"] = answers["judgment"]
	delete(result, "answers")
	return result, nil
}

// EvaluateQuestions answers an agent's batch of same-kind questions in one ask.
// The result names the jev.ask journal row so a plan review can prove its source.
func (j NativeJEV) EvaluateQuestions(ctx context.Context, kind string, state any, questions map[string]factory.SystemOneWorkerQuestion) (map[string]any, error) {
	sent := make(map[string]any, len(questions))
	for id, question := range questions {
		sent[id] = map[string]any{"type": kind, "instructions": question.Instructions, "criteria": question.Criteria}
	}
	settled, err := j.ask(ctx, state, sent)
	if err != nil {
		return nil, err
	}
	var reply struct {
		Model   string                    `json:"model"`
		Answers map[string]map[string]any `json:"answers"`
		Usage   map[string]any            `json:"usage"`
	}
	if json.Unmarshal(settled.Reply, &reply) != nil || len(reply.Answers) != len(questions) {
		return nil, errors.New("JEV answered a different question set")
	}
	answers := make(map[string]any, len(questions))
	for id, question := range questions {
		answer := reply.Answers[id]
		if answer["type"] != kind {
			return nil, errors.New("JEV answered a different question type")
		}
		if err := control.ValidateSystemOneAnswer(kind, answer, question.Criteria); err != nil {
			return nil, err
		}
		answers[id] = answer
	}
	return map[string]any{"answers": answers, "model": reply.Model, "usage": reply.Usage, factory.JEVJournalKey: settled.Journal}, nil
}
