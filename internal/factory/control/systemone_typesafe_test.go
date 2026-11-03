package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/levitateos/sodaos/internal/factory"
)

// The test transport directs only the real provider request to its private
// loopback HTTP server. No credential or request leaves the test fixture.
type stubSystemOneTransport struct {
	host string
}

func (s stubSystemOneTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != "https" || request.URL.Host != "api.typesafe.ai" || request.URL.Path != "/v1/systemone" {
		return nil, errors.New("unexpected System One fixture endpoint")
	}
	request = request.Clone(request.Context())
	request.URL.Scheme, request.URL.Host = "http", s.host
	request.Host = ""
	return http.DefaultTransport.RoundTrip(request)
}

func stubSystemOneProvider(t *testing.T, body string, status int) *TypeSafeSystemOne {
	t.Helper()
	keyPath := filepath.Join(t.TempDir(), "systemone.key")
	if err := os.WriteFile(keyPath, []byte("test-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var envelope map[string]any
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "Bearer test-key" || json.NewDecoder(io.LimitReader(r.Body, 65537)).Decode(&envelope) != nil {
			t.Error("System One HTTP request does not match the provider contract")
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		questions, ok := envelope["questions"].(map[string]any)
		if !ok || len(questions) == 0 || len(questions) > factory.SystemOneMaxBatchQuestions || len(envelope) != 3 || envelope["model"] == "" {
			t.Error("System One HTTP request omitted its bounded question envelope")
			http.Error(w, "invalid fixture envelope", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if status != 0 {
			w.WriteHeader(status)
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return &TypeSafeSystemOne{APIKeyFile: keyPath, Client: &http.Client{Transport: stubSystemOneTransport{host: server.Listener.Addr().String()}}}
}

// TestSystemOneAdapterAcceptsContractAnswers proves the three typed
// answers validate against the documented response contract,
// including an uncertain choice whose distribution is retained.
func TestSystemOneAdapterAcceptsContractAnswers(t *testing.T) {
	ctx := context.Background()
	noul := stubSystemOneProvider(t, `{"model":"jev-latest","answers":{"judgment":{"type":"noul","noul":0.7}},"usage":{"input_tokens":120,"output_tokens":12}}`, 0)
	got, err := noul.Evaluate(ctx, "noul", map[string]any{"task": "demo"}, "Judge it", nil)
	if err != nil {
		t.Fatalf("noul: %v", err)
	}
	if answer, _ := got["answer"].(map[string]any); answer["noul"] != 0.7 {
		t.Fatalf("noul answer: %+v", got)
	}
	choice := stubSystemOneProvider(t, `{"model":"jev-latest","answers":{"judgment":{"type":"choice","choice":"a","confidence":0.3,"probabilities":{"a":0.45,"b":0.4,"c":0.15}}},"usage":{"input_tokens":120,"output_tokens":12}}`, 0)
	got, err = choice.Evaluate(ctx, "choice", map[string]any{"task": "demo"}, "Pick one", map[string]any{"a": "A", "b": "B", "c": "C"})
	if err != nil {
		t.Fatalf("uncertain choice: %v", err)
	}
	if answer, _ := got["answer"].(map[string]any); answer["choice"] != "a" || answer["confidence"] != 0.3 {
		t.Fatalf("choice answer: %+v", got)
	}
	score := stubSystemOneProvider(t, `{"model":"jev-latest","answers":{"judgment":{"type":"score","score":1.7,"confidence":0.55,"legend":{"0":"Can wait","1":"Needs attention this week","2":"Needs attention today"},"probabilities":{"0":0.1,"1":0.1,"2":0.8}}},"usage":{"input_tokens":120,"output_tokens":12}}`, 0)
	got, err = score.Evaluate(ctx, "score", map[string]any{"task": "demo"}, "Rate it", []string{"Can wait", "Needs attention this week", "Needs attention today"})
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if answer, _ := got["answer"].(map[string]any); answer["score"] != 1.7 {
		t.Fatalf("score answer: %+v", got)
	}
}

// TestSystemOneAdapterBatchAnswers proves FA10: one round trip judges
// independent same-primitive questions over shared state, each answer
// validating against its own criteria, while a missing or mistyped
// answer refuses the batch.
func TestSystemOneAdapterBatchAnswers(t *testing.T) {
	ctx := context.Background()
	questions := map[string]factory.SystemOneWorkerQuestion{
		"q1": {Instructions: "Pick one", Criteria: map[string]any{"a": "A", "b": "B"}},
		"q2": {Instructions: "Pick another", Criteria: map[string]any{"c": "C", "d": "D"}},
	}
	provider := stubSystemOneProvider(t, `{"model":"jev-latest","answers":{"q1":{"type":"choice","choice":"a","confidence":0.6,"probabilities":{"a":0.6,"b":0.4}},"q2":{"type":"choice","choice":"d","confidence":0.8,"probabilities":{"c":0.2,"d":0.8}}},"usage":{"input_tokens":10,"output_tokens":5}}`, 0)
	got, err := provider.EvaluateQuestions(ctx, "choice", map[string]any{"task": "demo"}, questions)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	answers, _ := got["answers"].(map[string]any)
	q1, _ := answers["q1"].(map[string]any)
	q2, _ := answers["q2"].(map[string]any)
	if q1["choice"] != "a" || q2["choice"] != "d" {
		t.Fatalf("batch answers: %+v", got)
	}
	missing := stubSystemOneProvider(t, `{"model":"jev-latest","answers":{"q1":{"type":"choice","choice":"a","confidence":0.6,"probabilities":{"a":0.6,"b":0.4}}},"usage":{"input_tokens":10,"output_tokens":5}}`, 0)
	if _, err := missing.EvaluateQuestions(ctx, "choice", map[string]any{"task": "demo"}, questions); err == nil {
		t.Fatal("batch with a missing answer accepted")
	}
	mistyped := stubSystemOneProvider(t, `{"model":"jev-latest","answers":{"q1":{"type":"choice","choice":"a","confidence":0.6,"probabilities":{"a":0.6,"b":0.4}},"q2":{"type":"score","score":1.0,"confidence":0.8,"legend":{"0":"low","1":"high"},"probabilities":{"0":0.2,"1":0.8}}},"usage":{"input_tokens":10,"output_tokens":5}}`, 0)
	if _, err := mistyped.EvaluateQuestions(ctx, "choice", map[string]any{"task": "demo"}, questions); err == nil {
		t.Fatal("batch with a mistyped answer accepted")
	}
	badID := map[string]factory.SystemOneWorkerQuestion{"not a key!": {Instructions: "Pick"}}
	if _, err := provider.EvaluateQuestions(ctx, "noul", map[string]any{"task": "demo"}, badID); err == nil {
		t.Fatal("batch with an invalid question identity accepted")
	}
	if _, err := provider.EvaluateQuestions(ctx, "noul", map[string]any{"task": "demo"}, map[string]factory.SystemOneWorkerQuestion{}); err == nil {
		t.Fatal("empty batch accepted")
	}
	many := map[string]factory.SystemOneWorkerQuestion{}
	for i := 0; i < factory.SystemOneMaxBatchQuestions+1; i++ {
		many["q"+strconv.Itoa(i)] = factory.SystemOneWorkerQuestion{Instructions: "Pick"}
	}
	if _, err := provider.EvaluateQuestions(ctx, "noul", map[string]any{"task": "demo"}, many); err == nil {
		t.Fatal("over-bound batch accepted")
	}
	noCriteria := map[string]factory.SystemOneWorkerQuestion{"q1": {Instructions: "Pick"}}
	if _, err := provider.EvaluateQuestions(ctx, "choice", map[string]any{"task": "demo"}, noCriteria); err == nil {
		t.Fatal("choice batch question without criteria accepted")
	}
}

// TestSystemOneAdapterRefusesMalformedAnswers proves every FA03
// defect class refuses with an explicit error: wrong type, unknown
// option, missing confidence/probabilities/legend, unnormalized or
// inconsistent distributions, missing model identity and bad usage.
func TestSystemOneAdapterRefusesMalformedAnswers(t *testing.T) {
	ctx := context.Background()
	choiceCriteria := map[string]any{"a": "A", "b": "B"}
	scoreCriteria := []string{"low", "high"}
	cases := map[string]struct {
		kind     string
		criteria any
		body     string
		status   int
	}{
		"wrong type": {
			kind: "noul", body: `{"model":"jev-latest","answers":{"judgment":{"type":"box","box":{}}},"usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		"noul out of range": {
			kind: "noul", body: `{"model":"jev-latest","answers":{"judgment":{"type":"noul","noul":1.5}},"usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		"choice outside options": {
			kind: "choice", criteria: choiceCriteria,
			body: `{"model":"jev-latest","answers":{"judgment":{"type":"choice","choice":"z","confidence":0.9,"probabilities":{"a":0.5,"b":0.5}}},"usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		"choice missing confidence": {
			kind: "choice", criteria: choiceCriteria,
			body: `{"model":"jev-latest","answers":{"judgment":{"type":"choice","choice":"a","probabilities":{"a":0.6,"b":0.4}}},"usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		"choice missing probabilities": {
			kind: "choice", criteria: choiceCriteria,
			body: `{"model":"jev-latest","answers":{"judgment":{"type":"choice","choice":"a","confidence":0.9}},"usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		"choice probabilities short": {
			kind: "choice", criteria: choiceCriteria,
			body: `{"model":"jev-latest","answers":{"judgment":{"type":"choice","choice":"a","confidence":0.9,"probabilities":{"a":1.0}}},"usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		"choice probabilities unnormalized": {
			kind: "choice", criteria: choiceCriteria,
			body: `{"model":"jev-latest","answers":{"judgment":{"type":"choice","choice":"a","confidence":0.9,"probabilities":{"a":0.3,"b":0.3}}},"usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		"choice not maximal": {
			kind: "choice", criteria: choiceCriteria,
			body: `{"model":"jev-latest","answers":{"judgment":{"type":"choice","choice":"b","confidence":0.9,"probabilities":{"a":0.7,"b":0.3}}},"usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		"score out of range": {
			kind: "score", criteria: scoreCriteria,
			body: `{"model":"jev-latest","answers":{"judgment":{"type":"score","score":2.5,"confidence":0.9,"legend":{"0":"low","1":"high"},"probabilities":{"0":0.5,"1":0.5}}},"usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		"score missing legend": {
			kind: "score", criteria: scoreCriteria,
			body: `{"model":"jev-latest","answers":{"judgment":{"type":"score","score":0.5,"confidence":0.9,"probabilities":{"0":0.5,"1":0.5}}},"usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		"score legend gap": {
			kind: "score", criteria: scoreCriteria,
			body: `{"model":"jev-latest","answers":{"judgment":{"type":"score","score":0.5,"confidence":0.9,"legend":{"0":"low"},"probabilities":{"0":0.5,"1":0.5}}},"usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		"score off weighted average": {
			kind: "score", criteria: scoreCriteria,
			body: `{"model":"jev-latest","answers":{"judgment":{"type":"score","score":0.1,"confidence":0.9,"legend":{"0":"low","1":"high"},"probabilities":{"0":0.2,"1":0.8}}},"usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		"missing model": {
			kind: "noul", body: `{"answers":{"judgment":{"type":"noul","noul":0.7}},"usage":{"input_tokens":1,"output_tokens":1}}`,
		},
		"missing usage": {
			kind: "noul", body: `{"model":"jev-latest","answers":{"judgment":{"type":"noul","noul":0.7}}}`,
		},
		"negative usage": {
			kind: "noul", body: `{"model":"jev-latest","answers":{"judgment":{"type":"noul","noul":0.7}},"usage":{"input_tokens":-1,"output_tokens":0}}`,
		},
		"refused status": {
			kind: "noul", status: http.StatusUnprocessableEntity, body: `{"detail":[]}`,
		},
		"invalid json": {
			kind: "noul", body: `{`,
		},
	}
	for name, tc := range cases {
		provider := stubSystemOneProvider(t, tc.body, tc.status)
		if _, err := provider.Evaluate(ctx, tc.kind, map[string]any{"task": "demo"}, "Judge it", tc.criteria); err == nil {
			t.Fatalf("%s: malformed answer accepted", name)
		}
	}
}
