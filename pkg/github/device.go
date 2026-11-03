package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/levitateos/factory/pkg/strictjson"
	"golang.org/x/oauth2"
)

// DefaultOAuthClientID is public application identity, never a client secret.
const DefaultOAuthClientID = "Ov23liI9YQMPuYjBL6nn"

const devicePath = "/api/native/github/device/"

type deviceAttempt struct {
	Code     string `json:"device_code"`
	Expires  int64  `json:"expires_at"`
	Interval int64  `json:"interval"`
}

// NewNativeHandler composes private GitHub reads, device authorization and refresh. It
// must only be mounted behind the native peer/key/instance/admission gates.
// Without a trusted account scope, canonical operations are unavailable.
func NewNativeHandler(network http.RoundTripper) http.Handler {
	return newNativeHandler(network, nil)
}

// NewNativeAccountHandler adds canonical observations under the owned runtime's
// profile/registry scope. It does not activate an account or admit Factory work.
func NewNativeAccountHandler(network http.RoundTripper, scope NativeAccountScope) http.Handler {
	return newNativeHandler(network, &scope)
}

func newNativeHandler(network http.RoundTripper, scope *NativeAccountScope) http.Handler {
	read := newNativeReadHandler(network, scope)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/native/github/refresh" {
			refreshAuthorization(network, w, r)
			return
		}
		if r.URL.Path == "/api/native/github/read" {
			read.ServeHTTP(w, r)
			return
		}
		deviceAuthorization(network, w, r)
	})
}

func deviceAuthorization(network http.RoundTripper, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	fail := func(status int, code string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
	}
	if r.Method != http.MethodPost || (r.URL.Path != devicePath+"start" && r.URL.Path != devicePath+"wait") || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" || len(r.Header.Values(NativeCredentialHeader)) != 0 || len(r.Header.Values(NativeRefreshHeader)) != 0 {
		fail(400, "invalid_device_request")
		return
	}
	if network == nil {
		fail(503, "github_unavailable")
		return
	}
	var attempt deviceAttempt
	if strictjson.Decode(http.MaxBytesReader(w, r.Body, 4096), &attempt) != nil {
		fail(400, "invalid_device_request")
		return
	}
	start := time.Now()
	deadline := start.Add(8 * time.Second)
	if r.URL.Path == devicePath+"start" {
		if attempt != (deviceAttempt{}) {
			fail(400, "invalid_device_request")
			return
		}
	} else {
		deadline = time.Unix(attempt.Expires, 0)
		if !deviceSecret(attempt.Code) || attempt.Interval < 1 || attempt.Interval > 60 || !deadline.After(start) || deadline.After(start.Add(15*time.Minute)) {
			fail(400, "invalid_device_request")
			return
		}
		// Only this authenticated, bounded long-poll changes connection deadlines.
		// Clear the body-read deadline after consuming the entire strict request;
		// peer disconnect and host shutdown still cancel the request context.
		controller := http.NewResponseController(w)
		if controller.SetReadDeadline(time.Time{}) != nil || controller.SetWriteDeadline(deadline.Add(2*time.Second)) != nil {
			fail(503, "github_unavailable")
			return
		}
	}
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	ctx, config := nativeOAuth(ctx, network)
	config.Scopes = []string{"repo", "read:user"}
	if r.URL.Path == devicePath+"start" {
		challenge, err := config.DeviceAuth(ctx)
		if err != nil {
			deviceFailure(fail, err, ctx)
			return
		}
		if challenge == nil || !deviceSecret(challenge.DeviceCode) || len(challenge.UserCode) != 9 || challenge.UserCode[4] != '-' || !deviceSecret(challenge.UserCode) || challenge.VerificationURI != "https://github.com/login/device" || challenge.VerificationURIComplete != "" || !challenge.Expiry.After(time.Now()) || challenge.Expiry.After(time.Now().Add(15*time.Minute)) || challenge.Interval < 0 || challenge.Interval > 60 {
			fail(502, "invalid_device_response")
			return
		}
		if challenge.Interval == 0 {
			challenge.Interval = 5
		}
		_ = json.NewEncoder(w).Encode(struct {
			deviceAttempt
			UserCode string `json:"user_code"`
			URI      string `json:"verification_uri"`
		}{deviceAttempt{challenge.DeviceCode, challenge.Expiry.Unix(), challenge.Interval}, challenge.UserCode, challenge.VerificationURI})
		return
	}
	grant, err := config.DeviceAccessToken(ctx, &oauth2.DeviceAuthResponse{DeviceCode: attempt.Code, Expiry: deadline, Interval: attempt.Interval})
	if err != nil {
		deviceFailure(fail, err, ctx)
		return
	}
	// Preserve optional refresh and expiration facts for native custody. Neither
	// this endpoint nor its opaque Rust consumer can claim a stored login yet.
	value, err := nativeOAuthGrant(grant, start)
	if err != nil {
		fail(502, "invalid_device_response")
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

type nativeGrant struct {
	Access         string `json:"access_token"`
	Refresh        string `json:"refresh_token,omitempty"`
	Expires        int64  `json:"expires_at,omitempty"`
	RefreshExpires int64  `json:"refresh_expires_at,omitempty"`
	Scope          string `json:"scope"`
}

func nativeOAuthGrant(grant *oauth2.Token, start time.Time) (nativeGrant, error) {
	invalid := func() (nativeGrant, error) { return nativeGrant{}, errors.New("invalid GitHub grant") }
	if grant == nil || !deviceSecret(grant.AccessToken) || !strings.EqualFold(grant.TokenType, "bearer") || (grant.RefreshToken != "" && !deviceSecret(grant.RefreshToken)) {
		return invalid()
	}
	var expires, refreshExpires int64
	if !grant.Expiry.IsZero() {
		expires = grant.Expiry.Unix()
		if expires <= time.Now().Unix() || grant.RefreshToken == "" {
			return invalid()
		}
	}
	if value := grant.Extra("refresh_token_expires_in"); value != nil {
		var seconds int64
		switch value := value.(type) {
		case float64:
			if value <= 0 || value > 366*24*60*60 || float64(int64(value)) != value {
				return invalid()
			}
			seconds = int64(value)
		case int64:
			seconds = value
		default:
			return invalid()
		}
		if seconds <= 0 || seconds > 366*24*60*60 || grant.RefreshToken == "" {
			return invalid()
		}
		refreshExpires = start.Add(time.Duration(seconds) * time.Second).Unix()
		if refreshExpires <= time.Now().Unix() {
			return invalid()
		}
	}
	scope, ok := grant.Extra("scope").(string)
	if grant.Extra("scope") != nil && !ok {
		return invalid()
	}
	if len(scope) > 1024 || strings.ContainsAny(scope, "\r\n\x00") {
		return invalid()
	}
	return nativeGrant{grant.AccessToken, grant.RefreshToken, expires, refreshExpires, scope}, nil
}

func nativeOAuth(ctx context.Context, network http.RoundTripper) (context.Context, *oauth2.Config) {
	client := &http.Client{Transport: oauthNetwork{context: ctx, network: network}, Timeout: 8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return context.WithValue(ctx, oauth2.HTTPClient, client), &oauth2.Config{ClientID: DefaultOAuthClientID,
		Endpoint: oauth2.Endpoint{DeviceAuthURL: "https://github.com/login/device/code", TokenURL: "https://github.com/login/oauth/access_token", AuthStyle: oauth2.AuthStyleInParams},
	}
}

func deviceSecret(value string) bool {
	if len(value) < 1 || len(value) > 8192 {
		return false
	}
	for _, b := range []byte(value) {
		if b < 33 || b > 126 {
			return false
		}
	}
	return true
}

func deviceFailure(fail func(int, string), err error, ctx context.Context) {
	var provider *oauth2.RetrieveError
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		fail(408, "device_expired")
	case ctx.Err() != nil:
		fail(499, "device_cancelled")
	case errors.As(err, &provider) && provider.ErrorCode == "access_denied":
		fail(403, "device_denied")
	case errors.As(err, &provider) && provider.ErrorCode == "expired_token":
		fail(408, "device_expired")
	default:
		// Never return upstream bodies/errors: they may include secret grants.
		fail(502, "device_authorization_failed")
	}
}

// Bind cancellation even for oauth2.DeviceAuth's request without a context.
// Bound upstream bodies before the library decodes them and refuse redirects,
// arbitrary origins, cookies and any secret-bearing authentication header.
type oauthNetwork struct {
	context context.Context
	network http.RoundTripper
}

func (t oauthNetwork) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodPost || r.URL.Scheme != "https" || r.URL.Host != "github.com" || r.URL.User != nil || r.URL.RawQuery != "" || r.URL.RawPath != "" || (r.URL.Path != "/login/device/code" && r.URL.Path != "/login/oauth/access_token") || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
		return nil, errors.New("invalid device endpoint")
	}
	ctx, cancel := context.WithTimeout(t.context, 8*time.Second)
	defer cancel()
	response, err := t.network.RoundTrip(r.Clone(ctx))
	if err != nil || response == nil || response.Body == nil {
		return nil, errors.New("device network unavailable")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil || len(body) > 16384 {
		return nil, errors.New("invalid device response")
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}
