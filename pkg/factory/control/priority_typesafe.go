package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/levitateos/factory/pkg/factory"
)

// JEVWait is a JEV decision that waits without spending an attempt. Reason is
// one of the jev_* stall reasons; RetryAt (Unix seconds) is set when JEV is
// busy or offline and the decision starts again by itself.
type JEVWait struct {
	Reason  string
	RetryAt int64
	Detail  string
}

func (w *JEVWait) Error() string { return "JEV decision waits: " + w.Reason }

// DecodeChoiceReply checks one TypeSafe System One reply whose answer under key
// is a Choice over options. Every JEV choice passes through it, whichever
// transport carried the reply. It checks coverage and normalization, not the
// model's factual accuracy.
func DecodeChoiceReply(reply []byte, key string, options map[string]any) (factory.PriorityAnswer, error) {
	request := factory.PriorityRequest{}
	for id := range options {
		request.Candidates = append(request.Candidates, factory.PriorityCandidate{ID: id})
	}
	if len(request.Candidates) < 2 || len(request.Candidates) > 255 || len(reply) > 1024*1024 {
		return factory.PriorityAnswer{}, ErrPriorityUnavailable
	}
	var out struct {
		Model   string `json:"model"`
		Answers map[string]struct {
			Type          string             `json:"type"`
			Choice        string             `json:"choice"`
			Confidence    float64            `json:"confidence"`
			Probabilities map[string]float64 `json:"probabilities"`
		} `json:"answers"`
		Usage struct {
			Input  int64 `json:"input_tokens"`
			Output int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	decoder := json.NewDecoder(bytes.NewReader(reply))
	if err := decoder.Decode(&out); err != nil {
		return factory.PriorityAnswer{}, ErrPriorityUnavailable
	}
	if decoder.Decode(new(any)) != io.EOF {
		return factory.PriorityAnswer{}, ErrPriorityUnavailable
	}
	a, found := out.Answers[key]
	answer := factory.PriorityAnswer{Selected: a.Choice, Confidence: a.Confidence, Probabilities: a.Probabilities, Model: out.Model, InputTokens: out.Usage.Input, OutputTokens: out.Usage.Output}
	if !found || a.Type != "choice" {
		return answer, errors.New("JEV reply has no choice answer")
	}
	return answer, answer.Validate(request)
}
