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
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory/control"
)

type nativeOperationKey struct{}
type nativeOperation struct {
	config              NativeConfig
	instance, operation string
}

// NativeCollaboration uses a privately admitted product request or the exact
// saved account selected by native policy for admitted background operations.
// It sends semantic selectors to Rust, never provider URLs or tokens.
// Current proves current custody, not remote identity; Identity must succeed
// before the product owner transactionally maps an actor.
type NativeCollaboration struct {
	operation  nativeOperation
	background *nativeScope
	mu         sync.Mutex
	scope      *nativeScope
}

type nativeScope struct {
	FountainRepository string
	Connection         NativeConnection
	Repository         string
}

type NativeConnection struct {
	Connection string `json:"connection"`
	Provider   string `json:"provider"`
	Origin     string `json:"origin"`
	Account    string `json:"account"`
}

func nativeConnectionValid(c NativeConnection) bool {
	if !nativeOpaqueID(c.Connection) || !nativeRemoteID(c.Account) {
		return false
	}
	if c.Provider == "github" {
		return c.Origin == "https://github.com"
	}
	if c.Provider != "fountain" {
		return false
	}
	u, err := url.Parse(c.Origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return false
	}
	canonical, err := collaboration.CanonicalAPIBase(c.Origin)
	return err == nil && canonical == c.Origin
}

type NativeIdentity struct {
	Connection NativeConnection
	Account    collaboration.ObservedActor
	// SelectedRepository is the native operation's admitted remote scope.
	// Product request bodies and query parameters cannot select this authority.
	SelectedRepository string
}
type NativeRepository struct {
	MergeMethods  map[string]*bool `json:"merge_methods,omitempty"`
	ExternalID    string           `json:"external_id"`
	Name          string           `json:"name"`
	Private       *bool            `json:"private"`
	DefaultBranch string           `json:"default_branch"`
	// Push is a provider observation, not Factory execution or spending authority.
	Push  *bool `json:"push"`
	Admin *bool `json:"admin"`
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

// CollaborationReadFailure supplies only bounded failure facts to the shared
// Go response projector, without coupling it to a desktop or provider SDK.
func (e *NativePortError) CollaborationReadFailure() (int, *uint64, *uint64) {
	return e.Status, e.RetryAfter, e.Reset
}

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
	scope, err := p.currentScope(ctx)
	return scope.Connection, err
}
func (p *NativeCollaboration) currentScope(ctx context.Context) (nativeScope, error) {
	var observation json.RawMessage
	scope, err := p.call(ctx, map[string]any{"kind": "current"}, &observation)
	if err == nil && !bytes.Equal(bytes.TrimSpace(observation), []byte("null")) {
		err = errors.New("invalid native observation")
	}
	return scope, err
}
func (p *NativeCollaboration) Identity(ctx context.Context) (NativeIdentity, error) {
	var observation struct {
		Kind    string                      `json:"kind"`
		Account collaboration.ObservedActor `json:"account"`
	}
	scope, err := p.call(ctx, map[string]any{"kind": "read", "query": map[string]any{"kind": "identity"}}, &observation)
	if err == nil && (observation.Kind != "identity" || !nativeActor(observation.Account) || observation.Account.ExternalID != scope.Connection.Account) {
		err = errors.New("invalid native account observation")
	}
	return NativeIdentity{Connection: scope.Connection, Account: observation.Account, SelectedRepository: scope.Repository}, err
}

// Selection obtains one provider observation for the native-admitted scope.
// Repository already proves its current account; a separate identity read
// immediately before it would only repeat that same authenticated observation.
func (p *NativeCollaboration) Selection(ctx context.Context) (NativeIdentity, *NativeRepository, error) {
	scope, err := p.currentScope(ctx)
	if err != nil {
		return NativeIdentity{}, nil, err
	}
	if scope.Repository == "" {
		identity, err := p.Identity(ctx)
		return identity, nil, err
	}
	// Preserve the separately owned Fountain gateway observation protocol.
	if scope.Connection.Provider != "github" {
		identity, err := p.Identity(ctx)
		if err != nil {
			return NativeIdentity{}, nil, err
		}
		observed, err := p.Repository(ctx, scope.Repository)
		if err != nil {
			return NativeIdentity{}, nil, err
		}
		if !sameNativeAccount(identity, observed.NativeIdentity) {
			return NativeIdentity{}, nil, errors.New("native account changed")
		}
		return observed.NativeIdentity, &observed.Repository, nil
	}
	observed, err := p.Repository(ctx, scope.Repository)
	if err != nil {
		return NativeIdentity{}, nil, err
	}
	return observed.NativeIdentity, &observed.Repository, nil
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
	scope, err := p.call(ctx, map[string]any{"kind": "read", "query": map[string]any{"kind": "repository", "repository": repository}}, &observation)
	if err == nil && (observation.Kind != "repository" || !nativeActor(observation.Account) || observation.Account.ExternalID != scope.Connection.Account || scope.Repository != repository || observation.Repository.ExternalID != repository || !nativeObservedText(observation.Repository.Name, 512, false) || !nativeObservedText(observation.Repository.DefaultBranch, 1024, true)) {
		err = errors.New("invalid native repository observation")
	}
	return NativeRepositoryObservation{NativeIdentity: NativeIdentity{Connection: scope.Connection, Account: observation.Account, SelectedRepository: scope.Repository}, Repository: observation.Repository}, err
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
func (p *NativeCollaboration) call(ctx context.Context, effect any, observation any) (nativeScope, error) {
	var zero nativeScope
	if ctx.Err() != nil {
		return zero, errors.New("native operation cancelled")
	}
	var payload any = map[string]any{"operation": p.operation.operation, "call": effect}
	route := "/api/native/collaboration"
	if p.background != nil {
		payload = map[string]any{"connection": p.background.Connection.Connection, "account": p.background.Connection.Account, "repository": p.background.Repository, "call": effect}
		if p.background.FountainRepository != "" {
			payload.(map[string]any)["fountain_repository"] = p.background.FountainRepository
		}
		route += "/background"
	}
	maximum := 64
	switch read := effect.(type) {
	case nativeInlinePreparationEffect:
		maximum = 384
	case nativeReadEffect:
		switch read.Query.Kind {
		case "inline_preparation", "pull_threads":
			maximum = 384
		case "setup_source":
			maximum = 80
		}
	}
	body, err := json.Marshal(payload)
	limit, responseLimit := 8192, 2<<20
	switch read := effect.(type) {
	case nativeReadEffect:
		limit, responseLimit = read.Query.envelopeLimits()
	case nativeInlinePreparationEffect:
		limit = 64 << 10
	case nativeBranchPublishEffect:
		limit = 1024 << 10
	case nativeInlineCreateEffect, nativeMergeEffect, nativeInlineDraftEffect, nativeCommentEffect, nativeIssueCreateEffect, nativePullCreateEffect, nativeReviewCreateEffect, nativePendingReviewEffect, nativeEditEffect, nativeLockEffect:
		limit = 512 << 10
	}
	if err != nil || len(body) > limit {
		return zero, errors.New("invalid native operation")
	}
	// Leave room to receive the native owner's bounded refusal. Paired CI reads
	// have a longer native deadline than other operations.
	timeout := 9 * time.Second
	switch effect.(type) {
	case nativeReadEffect, nativeInlinePreparationEffect:
		timeout = 16 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return nativePortDial(ctx, p.operation.config)
		},
		DisableKeepAlives: true, MaxResponseHeaderBytes: 16384, ResponseHeaderTimeout: timeout,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://soda-native"+route, bytes.NewReader(body))
	if err != nil {
		return zero, errors.New("native operation unavailable")
	}
	allowance, settle := control.ReserveProviderSends(ctx, maximum)
	if allowance == 0 {
		return zero, control.ErrReadinessPassPending
	}
	request.Header.Set("Authorization", "Bearer "+hex.EncodeToString(p.operation.config.IPCKey()))
	request.Header.Set("X-Soda-Native-Instance", p.operation.instance)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Factory-Wire-Allowance", strconv.Itoa(allowance))
	response, err := client.Do(request)
	if err != nil {
		return zero, errors.New("native operation unavailable")
	}
	defer response.Body.Close()
	used := -1
	if values := response.Header.Values("X-Factory-Wire-Attempts"); len(values) == 1 {
		if count, err := strconv.Atoi(values[0]); err == nil && count >= 0 && count <= allowance {
			used = count
		}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, int64(responseLimit)+1))
	if err != nil || len(raw) > responseLimit || ctx.Err() != nil {
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
		settle(used)
		return zero, &NativePortError{Status: response.StatusCode, Code: failure.Code, RetryAfter: failure.RetryAfter, Reset: failure.Reset}
	}
	var envelope struct {
		Connection  NativeConnection `json:"connection"`
		Repository  *string          `json:"repository"`
		Observation json.RawMessage  `json:"observation"`
	}
	if nativeDecode(raw, &envelope) != nil || !nativeConnectionValid(envelope.Connection) || nativeDecode(envelope.Observation, observation) != nil {
		return zero, errors.New("invalid native observation")
	}
	scope := nativeScope{Connection: envelope.Connection}
	if envelope.Repository != nil {
		if !nativeRemoteID(*envelope.Repository) {
			return zero, errors.New("invalid native repository scope")
		}
		scope.Repository = *envelope.Repository
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.scope != nil && *p.scope != scope {
		return zero, errors.New("native account scope changed")
	}
	p.scope = &scope
	settle(used)
	return scope, nil
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
