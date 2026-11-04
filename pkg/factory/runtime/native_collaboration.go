package runtime

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/levitateos/factory/pkg/collaboration"
)

type nativeOperationKey struct{}
type nativeOperation struct {
	config              NativeConfig
	instance, operation string
}

// NativeCollaboration is available only inside a privately admitted product
// request. It sends semantic selectors to Rust, never provider URLs or tokens.
// Current proves current custody, not remote identity; Identity must succeed
// before the product owner transactionally maps an actor.
type NativeCollaboration struct {
	operation  nativeOperation
	mu         sync.Mutex
	connection *NativeConnection
}

type NativeConnection struct {
	Connection string `json:"connection"`
	Provider   string `json:"provider"`
	Origin     string `json:"origin"`
	Account    string `json:"account"`
}
type NativeIdentity struct {
	Connection NativeConnection
	Account    collaboration.ObservedActor
}
type NativeRepository struct {
	ExternalID    string `json:"external_id"`
	Name          string `json:"name"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
}
type NativeRepositoryObservation struct {
	NativeIdentity
	Repository NativeRepository
}

// NativePortError retains only the port's bounded code/status/rate facts.
// It never retains an HTTP error, provider body, URL or credential material.
type NativePortError struct {
	Status     int
	Code       string
	RetryAfter *uint64
	Reset      *uint64
}

func (e *NativePortError) Error() string { return e.Code }

// NativeRequestPort rejects ambient/readiness-only authority. Its context value
// is set after the existing same-user/key/instance HTTP admission, and Rust
// independently validates the operation against its live capability registry.
func NativeRequestPort(ctx context.Context) (*NativeCollaboration, error) {
	op, ok := ctx.Value(nativeOperationKey{}).(nativeOperation)
	if !ok || ctx.Err() != nil {
		return nil, errors.New("native operation required")
	}
	return &NativeCollaboration{operation: op}, nil
}
func nativeOperationContext(r *http.Request, config NativeConfig, instance string) (*http.Request, bool) {
	values := r.Header.Values("X-Soda-Native-Operation")
	if len(values) == 0 {
		return r, true
	}
	if len(values) != 1 || !nativeOpaqueID(values[0]) {
		return r, false
	}
	op := nativeOperation{config: config, instance: instance, operation: values[0]}
	return r.WithContext(context.WithValue(r.Context(), nativeOperationKey{}, op)), true
}
func nativeOpaqueID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func nativeRemoteID(value string) bool {
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == value
}
func nativeActor(actor collaboration.ObservedActor) bool {
	return nativeRemoteID(actor.ExternalID) && nativeObservedText(actor.Login, 512, false)
}
func nativeObservedText(value string, bound int, empty bool) bool {
	if (!empty && value == "") || len(value) > bound || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func (p *NativeCollaboration) Current(ctx context.Context) (NativeConnection, error) {
	var observation json.RawMessage
	connection, err := p.call(ctx, map[string]any{"kind": "current"}, &observation)
	if err == nil && !bytes.Equal(bytes.TrimSpace(observation), []byte("null")) {
		err = errors.New("invalid native observation")
	}
	return connection, err
}
func (p *NativeCollaboration) Identity(ctx context.Context) (NativeIdentity, error) {
	var observation struct {
		Kind    string                      `json:"kind"`
		Account collaboration.ObservedActor `json:"account"`
	}
	connection, err := p.call(ctx, map[string]any{"kind": "read", "query": map[string]any{"kind": "identity"}}, &observation)
	if err == nil && (observation.Kind != "identity" || !nativeActor(observation.Account) || observation.Account.ExternalID != connection.Account) {
		err = errors.New("invalid native account observation")
	}
	return NativeIdentity{Connection: connection, Account: observation.Account}, err
}
func (p *NativeCollaboration) Repository(ctx context.Context, repository string) (NativeRepositoryObservation, error) {
	if !nativeRemoteID(repository) {
		return NativeRepositoryObservation{}, errors.New("invalid native repository selector")
	}
	var observation struct {
		Kind       string                      `json:"kind"`
		Account    collaboration.ObservedActor `json:"account"`
		Repository NativeRepository            `json:"repository"`
	}
	connection, err := p.call(ctx, map[string]any{"kind": "read", "query": map[string]any{"kind": "repository", "repository": repository}}, &observation)
	if err == nil && (observation.Kind != "repository" || !nativeActor(observation.Account) || observation.Account.ExternalID != connection.Account || observation.Repository.ExternalID != repository || !nativeObservedText(observation.Repository.Name, 512, false) || !nativeObservedText(observation.Repository.DefaultBranch, 1024, true)) {
		err = errors.New("invalid native repository observation")
	}
	return NativeRepositoryObservation{NativeIdentity: NativeIdentity{Connection: connection, Account: observation.Account}, Repository: observation.Repository}, err
}
func nativeDecode(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return errors.New("invalid native observation")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid native observation")
	}
	return nil
}
func (p *NativeCollaboration) call(ctx context.Context, effect any, observation any) (NativeConnection, error) {
	var zero NativeConnection
	if ctx.Err() != nil {
		return zero, errors.New("native operation cancelled")
	}
	body, err := json.Marshal(map[string]any{"operation": p.operation.operation, "call": effect})
	if err != nil || len(body) > 8192 {
		return zero, errors.New("invalid native operation")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return nativePortDial(ctx, p.operation.config)
		},
		DisableKeepAlives: true, MaxResponseHeaderBytes: 16384, ResponseHeaderTimeout: 8 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://soda-native/api/native/collaboration", bytes.NewReader(body))
	if err != nil {
		return zero, errors.New("native operation unavailable")
	}
	request.Header.Set("Authorization", "Bearer "+hex.EncodeToString(p.operation.config.IPCKey()))
	request.Header.Set("X-Soda-Native-Instance", p.operation.instance)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return zero, errors.New("native operation unavailable")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 || ctx.Err() != nil {
		return zero, errors.New("native observation unavailable")
	}
	if response.StatusCode != http.StatusOK {
		var failure struct {
			Code       string  `json:"error"`
			RetryAfter *uint64 `json:"retry_after"`
			Reset      *uint64 `json:"reset"`
		}
		if nativeDecode(raw, &failure) != nil || !nativeErrorCode(failure.Code) {
			return zero, errors.New("invalid native operation outcome")
		}
		return zero, &NativePortError{Status: response.StatusCode, Code: failure.Code, RetryAfter: failure.RetryAfter, Reset: failure.Reset}
	}
	var envelope struct {
		Connection  NativeConnection `json:"connection"`
		Observation json.RawMessage  `json:"observation"`
	}
	if nativeDecode(raw, &envelope) != nil || !nativeOpaqueID(envelope.Connection.Connection) || envelope.Connection.Provider != "github" || envelope.Connection.Origin != "https://github.com" || !nativeRemoteID(envelope.Connection.Account) || nativeDecode(envelope.Observation, observation) != nil {
		return zero, errors.New("invalid native observation")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.connection != nil && *p.connection != envelope.Connection {
		return zero, errors.New("native account scope changed")
	}
	connection := envelope.Connection
	p.connection = &connection
	return connection, nil
}
func nativeErrorCode(code string) bool {
	if len(code) == 0 || len(code) > 96 {
		return false
	}
	for _, c := range code {
		if !(c >= 'a' && c <= 'z' || c == '_') {
			return false
		}
	}
	return true
}
func nativePortDial(ctx context.Context, config NativeConfig) (net.Conn, error) {
	if nativePrivateDirectory(config.Directory) != nil {
		return nil, errors.New("native port directory unavailable")
	}
	path := filepath.Join(config.Directory, "host.sock")
	before, err := os.Lstat(path)
	if err != nil || before.Mode()&os.ModeSocket == 0 || before.Mode().Perm()&0o077 != 0 || !nativeOwnedFile(before) {
		return nil, errors.New("native port unavailable")
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, errors.New("native port unavailable")
	}
	after, err := os.Lstat(path)
	peer, _ := nativePeerContext(ctx, connection).Value(nativePeerKey{}).(bool)
	if err != nil || !os.SameFile(before, after) || !peer {
		_ = connection.Close()
		return nil, errors.New("native port identity unavailable")
	}
	return connection, nil
}
