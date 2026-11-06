package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/levitateos/factory/pkg/factory"
)

// TypeSafePriority uses a server-only secret file and does not retry requests.
type TypeSafePriority struct {
	APIKeyFile string
	Model      string
	Client     *http.Client
}

func (p *TypeSafePriority) Choose(ctx context.Context, request factory.PriorityRequest) (factory.PriorityAnswer, error) {
	choices := make(map[string]any, len(request.Candidates))
	for _, candidate := range request.Candidates {
		choices[candidate.ID] = candidate
	}
	return p.Select(ctx, request, request.Rubric, choices)
}

func admitPriorityCandidates(choices map[string]any) (factory.PriorityRequest, error) {
	request := factory.PriorityRequest{}
	for id := range choices {
		request.Candidates = append(request.Candidates, factory.PriorityCandidate{ID: id})
	}
	if len(request.Candidates) < 2 || len(request.Candidates) > 255 {
		return factory.PriorityRequest{}, errors.New("invalid priority choice size")
	}
	return request, nil
}

// TypeSafeKeyReadable reports whether a usable JEV key is stored at path.
func TypeSafeKeyReadable(path string) bool {
	_, err := readPriorityKeyFile(path)
	return err == nil
}

func readPriorityKeyFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", ErrPriorityUnavailable
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", ErrPriorityUnavailable
	}
	key, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(key) > 65536 || len(bytes.TrimSpace(key)) == 0 {
		return "", ErrPriorityUnavailable
	}
	return strings.TrimSpace(string(key)), nil
}

func postPriorityRequest(ctx context.Context, client *http.Client, model string, state any, question string, choices map[string]any, key string) (*http.Response, error) {
	response, err := postSystemOne(ctx, client, model, state, map[string]any{"priority": map[string]any{"type": "choice", "instructions": question, "criteria": choices}}, key)
	if errors.Is(err, errSystemOneRequestFailed) {
		return nil, ErrPriorityUnavailable
	}
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, ErrPriorityUnavailable
	}
	return response, nil
}

func decodePriorityResponse(response *http.Response, request factory.PriorityRequest) (factory.PriorityAnswer, error) {
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
	responseBytes, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if err != nil || len(responseBytes) > 1024*1024 {
		return factory.PriorityAnswer{}, ErrPriorityUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBytes))
	if err := decoder.Decode(&out); err != nil {
		return factory.PriorityAnswer{}, ErrPriorityUnavailable
	}
	if decoder.Decode(new(any)) != io.EOF {
		return factory.PriorityAnswer{}, ErrPriorityUnavailable
	}
	a := out.Answers["priority"]
	answer := factory.PriorityAnswer{Selected: a.Choice, Confidence: a.Confidence, Probabilities: a.Probabilities, Model: out.Model, InputTokens: out.Usage.Input, OutputTokens: out.Usage.Output}
	if a.Type != "choice" {
		return answer, ErrPriorityUnavailable
	}
	return answer, answer.Validate(request)
}

// Select grants no authority; its caller checks work scope and allowances.
func (p *TypeSafePriority) Select(ctx context.Context, state any, question string, choices map[string]any) (factory.PriorityAnswer, error) {
	request, err := admitPriorityCandidates(choices)
	if err != nil {
		return factory.PriorityAnswer{}, err
	}
	key, err := readPriorityKeyFile(p.APIKeyFile)
	if err != nil {
		return factory.PriorityAnswer{}, err
	}
	response, err := postPriorityRequest(ctx, p.Client, p.Model, state, question, choices, key)
	if err != nil {
		return factory.PriorityAnswer{}, err
	}
	defer func() { _ = response.Body.Close() }()
	return decodePriorityResponse(response, request)
}
