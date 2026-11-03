package control_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	extensions "forgejo.org/extension-sdk"

	"github.com/levitateos/sodaos/internal/forgejo"
)

type fixedDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c fixedDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }

func TestNativeMergeCallContextBoundsProductionBackgroundRequest(t *testing.T) {
	path, err := os.CreateTemp(os.TempDir(), "q5-")
	if err != nil {
		t.Fatal(err)
	}
	socket := path.Name() + ".sock"
	if err := path.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path.Name()); err != nil {
		t.Fatal(err)
	}
	if len(socket) >= 100 {
		t.Fatalf("Unix socket path is too long for sockaddr_un: %d bytes", len(socket))
	}
	t.Cleanup(func() { _ = os.Remove(socket) })
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	const operationID = "deadline-same-operation"
	var mu sync.Mutex
	var ids []string
	requestStarted := make(chan struct{}, 1)
	requestCanceled := make(chan struct{}, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case extensions.BackgroundBootstrapPath:
			_, _ = w.Write([]byte(`{"admission":"` + strings.Repeat("a", 43) + `","installation_id":"test-installation"}`))
		case extensions.BackgroundRevisionPath:
			_, _ = w.Write([]byte(`{"revision":1}`))
		case extensions.BackgroundGetPath:
			var request struct {
				OperationID string `json:"operation_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			mu.Lock()
			ids = append(ids, request.OperationID)
			attempt := len(ids)
			mu.Unlock()
			if attempt == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			requestStarted <- struct{}{}
			<-r.Context().Done()
			requestCanceled <- struct{}{}
		default:
			http.NotFound(w, r)
		}
	})
	server := &http.Server{Handler: handler}
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = server.Close()
		select {
		case <-serveDone:
		case <-time.After(time.Second):
			t.Error("Unix callback server did not stop")
		}
	})

	background := forgejo.NewServiceBackground(socket, uint32(os.Getuid()), "test-installation")
	bootstrapCtx, cancelBootstrap := context.WithTimeout(context.Background(), time.Second)
	if _, err := background.ReadNativeRevision(bootstrapCtx); err != nil {
		cancelBootstrap()
		t.Fatalf("bootstrap through production background client: %v", err)
	}
	cancelBootstrap()

	ctx, cancel := context.WithTimeout(context.Background(), 650*time.Millisecond)
	defer cancel()
	_, err = nativeMergeCallContext(ctx, func(callCtx context.Context) (extensions.OperationLookup, error) {
		return background.GetOperation(callCtx, operationID)
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bounded call error = %v, want deadline exceeded", err)
	}
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("busy retry did not reach the stalled production request")
	}
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("production request handler did not observe deadline cancellation")
	}
	mu.Lock()
	gotIDs := append([]string(nil), ids...)
	mu.Unlock()
	if len(gotIDs) != 2 || gotIDs[0] != operationID || gotIDs[1] != operationID {
		t.Fatalf("busy retry operation identities = %v, want same identity %q twice", gotIDs, operationID)
	}
}

func TestNativeMergeCallContextRejectsLateSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	type result struct{ value string }
	got, err := nativeMergeCallContext(ctx, func(callCtx context.Context) (result, error) {
		<-callCtx.Done()
		return result{value: "late"}, nil
	})
	if !errors.Is(err, context.DeadlineExceeded) || got.value != "" {
		t.Fatalf("late success = (%+v, %v), want zero result and deadline", got, err)
	}
}

func TestNativeMergeCallContextRejectsElapsedDeadlineBeforeCancellation(t *testing.T) {
	ctx := fixedDeadlineContext{
		Context:  context.Background(),
		deadline: time.Now().Add(100 * time.Millisecond),
	}
	type result struct{ value string }
	called := false
	got, err := nativeMergeCallContext(ctx, func(context.Context) (result, error) {
		called = true
		delay := time.Until(ctx.deadline)
		if delay > 0 {
			time.Sleep(delay)
		}
		return result{value: "late"}, nil
	})
	if !called || !errors.Is(err, context.DeadlineExceeded) || got.value != "" {
		t.Fatalf("elapsed-deadline call = called:%t result:%+v err:%v, want called, zero result, and deadline", called, got, err)
	}
}

func TestNativeAPIContextCancelsStalledHTTPRequest(t *testing.T) {
	token, err := os.CreateTemp(os.TempDir(), "q5-token-")
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := token.Name()
	if err := token.Close(); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tokenPath)
	if err := os.Chmod(tokenPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte("fixture-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":`))
		w.(http.Flusher).Flush()
		started <- struct{}{}
		<-r.Context().Done()
		canceled <- struct{}{}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	var result struct {
		ID int `json:"id"`
	}
	err = nativeAPIContext(ctx, nativeST09Config{FountainURL: server.URL, TokenFile: tokenPath}, tokenPath,
		http.MethodGet, "/stall", nil, &result)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stalled request error = %v, want deadline exceeded", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("local HTTP handler did not receive request")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("local HTTP handler did not observe request cancellation")
	}
}
