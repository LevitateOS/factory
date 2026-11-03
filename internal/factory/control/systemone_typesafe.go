package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/levitateos/sodaos/internal/factory"
)

// TypeSafeSystemOne exposes the three generic judgments. Results are advice;
// callers cannot substitute them for controller acceptance or native receipts.
type TypeSafeSystemOne struct {
	APIKeyFile string
	Model      string
	Client     *http.Client
}

func (p *TypeSafeSystemOne) Evaluate(ctx context.Context, kind string, state, instructions, criteria any) (map[string]any, error) {
	key, err := readPriorityKeyFile(p.APIKeyFile)
	if err != nil {
		return nil, errors.New("System One provider is not configured")
	}
	model := p.Model
	if model == "" {
		model = "jev-latest"
	}
	q := map[string]any{"type": kind, "instructions": instructions}
	if criteria != nil {
		q["criteria"] = criteria
	}
	return p.evaluateQuestionMap(ctx, key, model, state, map[string]any{"judgment": q}, func(out map[string]any) (map[string]any, error) {
		answers, _ := out["answers"].(map[string]any)
		answer, _ := answers["judgment"].(map[string]any)
		if answer["type"] != kind {
			return nil, errors.New("System One returned a different question type")
		}
		if err := validateSystemOneAnswer(kind, answer, criteria); err != nil {
			return nil, err
		}
		if err := validateSystemOneEnvelope(out); err != nil {
			return nil, err
		}
		return map[string]any{"answer": answer, "model": out["model"], "usage": out["usage"]}, nil
	})
}

// validSystemOneQuestionID keeps batch question identities short code
// keys the provider echoes back for answer matching.
func validSystemOneQuestionID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if r != '-' && r != '_' && (r < '0' || r > '9') && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

// EvaluateQuestions judges one batch of independent same-primitive
// questions over shared state in a single provider round trip. Every
// answer validates against its own instructions and criteria; unused
// extra answers are ignored, while a missing or mistyped answer for a
// requested question refuses the batch. A question needing an earlier
// result or new evidence belongs in a later call, not this batch.
func (p *TypeSafeSystemOne) EvaluateQuestions(ctx context.Context, kind string, state any, questions map[string]factory.SystemOneWorkerQuestion) (map[string]any, error) {
	key, err := readPriorityKeyFile(p.APIKeyFile)
	if err != nil {
		return nil, errors.New("System One provider is not configured")
	}
	model := p.Model
	if model == "" {
		model = "jev-latest"
	}
	if len(questions) == 0 || len(questions) > factory.SystemOneMaxBatchQuestions {
		return nil, errors.New("System One batch needs 1 to 8 questions")
	}
	wire := make(map[string]any, len(questions))
	for id, question := range questions {
		if !validSystemOneQuestionID(id) {
			return nil, errors.New("System One batch question identity is not a short code key")
		}
		if (kind == "choice" || kind == "score") && question.Criteria == nil {
			return nil, errors.New("System One batch question is missing its criteria")
		}
		entry := map[string]any{"type": kind, "instructions": question.Instructions}
		if question.Criteria != nil {
			entry["criteria"] = question.Criteria
		}
		wire[id] = entry
	}
	return p.evaluateQuestionMap(ctx, key, model, state, wire, func(out map[string]any) (map[string]any, error) {
		answers, _ := out["answers"].(map[string]any)
		batch := make(map[string]any, len(questions))
		for id, question := range questions {
			answer, _ := answers[id].(map[string]any)
			if answer["type"] != kind {
				return nil, errors.New("System One batch is missing a typed answer")
			}
			if err := validateSystemOneAnswer(kind, answer, question.Criteria); err != nil {
				return nil, err
			}
			batch[id] = answer
		}
		if err := validateSystemOneEnvelope(out); err != nil {
			return nil, err
		}
		return map[string]any{"answers": batch, "model": out["model"], "usage": out["usage"]}, nil
	})
}

// evaluateQuestionMap posts one question envelope and finishes the
// decoded contract envelope through the caller's answer projection.
func (p *TypeSafeSystemOne) evaluateQuestionMap(ctx context.Context, key, model string, state any, questions map[string]any, finish func(map[string]any) (map[string]any, error)) (map[string]any, error) {
	body, err := json.Marshal(map[string]any{"model": model, "state": state, "questions": questions})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.typesafe.ai/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("System One request failed")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, errors.New("System One provider refused the request")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, errors.New("System One response exceeds bound")
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil, errors.New("System One returned invalid JSON")
	}
	return finish(out)
}

// systemOneDistributionTolerance matches the priority answer contract:
// provider distributions sum to approximately 1.
const systemOneDistributionTolerance = 0.001

func systemOneConfidence(answer map[string]any) (float64, error) {
	value, ok := answer["confidence"].(float64)
	if !ok || math.IsNaN(value) || value < 0 || value > 1 {
		return 0, errors.New("System One answer is missing finite 0-1 confidence")
	}
	return value, nil
}

// systemOneDistribution requires the named probabilities to cover
// exactly the expected keys with finite 0-1 values summing to
// approximately 1.
func systemOneDistribution(answer map[string]any, expected []string) (map[string]float64, error) {
	raw, ok := answer["probabilities"].(map[string]any)
	if !ok {
		return nil, errors.New("System One answer is missing its probabilities")
	}
	if len(raw) != len(expected) {
		return nil, errors.New("System One probabilities do not cover the requested options")
	}
	dist := make(map[string]float64, len(expected))
	sum := 0.0
	for _, key := range expected {
		value, ok := raw[key].(float64)
		if !ok || math.IsNaN(value) || value < 0 || value > 1 {
			return nil, errors.New("System One probabilities do not cover the requested options")
		}
		dist[key] = value
		sum += value
	}
	if math.Abs(sum-1) > systemOneDistributionTolerance {
		return nil, errors.New("System One probabilities do not sum to 1")
	}
	return dist, nil
}

func validateSystemOneAnswer(kind string, answer map[string]any, criteria any) error {
	switch kind {
	case "noul":
		// The noul primitive carries no separate confidence field;
		// 0.5 expresses uncertainty.
		value, ok := answer["noul"].(float64)
		if !ok || math.IsNaN(value) || value < 0 || value > 1 {
			return errors.New("System One noul answer is not a 0-1 probability")
		}
	case "choice":
		value, ok := answer["choice"].(string)
		var options map[string]json.RawMessage
		raw, err := json.Marshal(criteria)
		if err != nil || json.Unmarshal(raw, &options) != nil {
			return errors.New("System One choice criteria are not named options")
		}
		if _, present := options[value]; !ok || !present {
			return errors.New("System One choice is not a requested option")
		}
		names := make([]string, 0, len(options))
		for name := range options {
			names = append(names, name)
		}
		dist, err := systemOneDistribution(answer, names)
		if err != nil {
			return err
		}
		for _, p := range dist {
			if p > dist[value]+systemOneDistributionTolerance {
				return errors.New("System One choice is not the highest-probability option")
			}
		}
		if _, err := systemOneConfidence(answer); err != nil {
			return err
		}
	case "score":
		value, ok := answer["score"].(float64)
		var levels []json.RawMessage
		raw, err := json.Marshal(criteria)
		if err != nil || json.Unmarshal(raw, &levels) != nil || len(levels) < 2 {
			return errors.New("System One score criteria are not ordered levels")
		}
		if !ok || math.IsNaN(value) || value < 0 || value > float64(len(levels)-1) {
			return errors.New("System One score is outside its level range")
		}
		legend, ok := answer["legend"].(map[string]any)
		if !ok {
			return errors.New("System One score answer is missing its legend")
		}
		keys := make([]string, 0, len(levels))
		for i := range levels {
			key := strconv.Itoa(i)
			if _, present := legend[key]; !present {
				return errors.New("System One legend does not cover every level")
			}
			keys = append(keys, key)
		}
		if len(legend) != len(keys) {
			return errors.New("System One legend does not cover every level")
		}
		dist, err := systemOneDistribution(answer, keys)
		if err != nil {
			return err
		}
		weighted := 0.0
		for i := range levels {
			weighted += float64(i) * dist[strconv.Itoa(i)]
		}
		if math.Abs(weighted-value) > systemOneDistributionTolerance {
			return errors.New("System One score is not its probability-weighted average")
		}
		if _, err := systemOneConfidence(answer); err != nil {
			return err
		}
	default:
		return errors.New("System One returned an unknown question type")
	}
	return nil
}

// validateSystemOneEnvelope requires the provider/model identity and
// nonnegative token usage the response contract promises alongside
// the typed answers.
func validateSystemOneEnvelope(out map[string]any) error {
	model, ok := out["model"].(string)
	if !ok || model == "" {
		return errors.New("System One response is missing its model identity")
	}
	usage, ok := out["usage"].(map[string]any)
	if !ok {
		return errors.New("System One response is missing its token usage")
	}
	for _, field := range []string{"input_tokens", "output_tokens"} {
		tokens, ok := usage[field].(float64)
		if !ok || math.IsNaN(tokens) || tokens < 0 || tokens != math.Trunc(tokens) {
			return errors.New("System One token usage is not a nonnegative count")
		}
	}
	return nil
}
