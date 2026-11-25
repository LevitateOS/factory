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
)

// NativeJEV asks JEV through the desktop's TypeSafe connector. Rust holds the
// key and the per-Mac gate. This owner journals every decision in
// factory_commands, so a paid answer is reused, a waiting decision starts
// again on a later coordinator pass, and a request TypeSafe refused is never
// sent again. Purpose names the decision (for example post_route).
type NativeJEV struct {
	NativeExecution
	Purpose string
}

var _ control.ProductJudgment = NativeJEV{}

// jevChoiceKey is the question ID of a single-Choice ask.
const jevChoiceKey = "choice"

// jevReceipt is the outcome column of one jev.ask row.
type jevReceipt struct {
	// asking, waiting, answered, refused (TypeSafe will not take this request)
	// or problem (an unexpected reply).
	State    string                  `json:"state"`
	Reason   string                  `json:"reason,omitempty"`
	RetryAt  int64                   `json:"retry_at,omitempty"`
	Attempts int                     `json:"attempts"`
	Status   int                     `json:"http_status,omitempty"`
	Detail   string                  `json:"detail,omitempty"`
	Answer   *factory.PriorityAnswer `json:"answer,omitempty"`
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

func (j NativeJEV) jev(ctx context.Context, payload, result any) error {
	var raw json.RawMessage
	if err := nativeHostCall(ctx, j.Config, j.Instance, "/api/native/jev", payload, &raw); err != nil {
		return err
	}
	return json.Unmarshal(raw, result)
}

// Select asks one Choice. It returns *control.JEVWait while the decision waits
// for a key, for TypeSafe or for the person to change the request.
func (j NativeJEV) Select(ctx context.Context, state any, question string, choices map[string]any) (factory.PriorityAnswer, error) {
	var empty factory.PriorityAnswer
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
	questions := map[string]any{jevChoiceKey: map[string]any{"type": "choice", "instructions": question, "criteria": choices}}
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
		return settledJEV(receipt)
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
	wait := func(reason string, retryAt int64) (factory.PriorityAnswer, error) {
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
		answer, err := control.DecodeChoiceReply(asked.Reply, jevChoiceKey, choices)
		if err != nil {
			receipt.State, receipt.Detail = "problem", "JEV’s answer did not cover the options it was given."
			_ = note(true)
			return empty, err
		}
		receipt.State, receipt.Answer = "answered", &answer
		return answer, note(true)
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
		return settledJEV(receipt)
	default:
		receipt.State = "problem"
		_ = note(true)
		return settledJEV(receipt)
	}
}

// settledJEV replays a finished journal row without contacting TypeSafe.
func settledJEV(receipt jevReceipt) (factory.PriorityAnswer, error) {
	switch receipt.State {
	case "answered":
		if receipt.Answer != nil {
			return *receipt.Answer, nil
		}
	case "refused":
		return factory.PriorityAnswer{}, &control.JEVWait{Reason: "jev_request_refused", Detail: receipt.Detail}
	}
	return factory.PriorityAnswer{}, fmt.Errorf("JEV answered with an unexpected error (HTTP %d)", receipt.Status)
}
