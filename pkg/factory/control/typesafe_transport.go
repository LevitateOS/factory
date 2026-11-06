package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

var errSystemOneRequestFailed = errors.New("System One request failed")

func systemOneModel(model string) string {
	if model == "" {
		return "jev-latest"
	}
	return model
}

func postSystemOne(ctx context.Context, client *http.Client, model string, state any, questions map[string]any, key string) (*http.Response, error) {
	body, err := json.Marshal(map[string]any{"model": systemOneModel(model), "state": state, "questions": questions})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.typesafe.ai/v1/systemone", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, errSystemOneRequestFailed
	}
	return response, nil
}
