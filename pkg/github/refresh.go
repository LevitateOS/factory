package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/levitateos/factory/pkg/strictjson"
	"golang.org/x/oauth2"
)

const NativeRefreshHeader = "X-Soda-GitHub-Refresh"

// Native custody must durably reserve before calling this non-retried exchange.
// It can invalidate the previous pair even if the native client loses its reply.
func refreshAuthorization(network http.RoundTripper, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	fail := func(status int, code string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
	}
	if r.Method != http.MethodPost || r.URL.Path != "/api/native/github/refresh" || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" || len(r.Header.Values(NativeCredentialHeader)) != 0 {
		fail(400, "invalid_refresh_request")
		return
	}
	values := r.Header.Values(NativeRefreshHeader)
	if len(values) != 1 || !deviceSecret(values[0]) {
		fail(401, "refresh_required")
		return
	}
	refresh := []byte(values[0])
	r.Header.Del(NativeRefreshHeader)
	defer clear(refresh)
	var empty struct{}
	if strictjson.Decode(http.MaxBytesReader(w, r.Body, 128), &empty) != nil {
		fail(400, "invalid_refresh_request")
		return
	}
	if network == nil {
		fail(503, "github_unavailable")
		return
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	ctx, config := nativeOAuth(ctx, network)
	// Fixed AuthStyleInParams avoids credential-style probing/replay. No scope
	// or shared client secret is sent for a device-issued refresh token.
	grant, err := config.TokenSource(ctx, &oauth2.Token{RefreshToken: string(refresh)}).Token()
	if err != nil {
		var provider *oauth2.RetrieveError
		if errors.As(err, &provider) && provider.ErrorCode == "bad_refresh_token" {
			fail(401, "github_sign_in_required")
		} else {
			fail(502, "github_refresh_unconfirmed")
		}
		return
	}
	// oauth2 preserves the previous refresh when a response omits it. GitHub's
	// rotating pair contract requires an actual new response value instead.
	if grant == nil {
		fail(502, "invalid_refresh_response")
		return
	}
	rawRefresh, _ := grant.Extra("refresh_token").(string)
	if rawRefresh == "" || rawRefresh == string(refresh) || grant.Expiry.IsZero() || grant.Extra("refresh_token_expires_in") == nil {
		fail(502, "invalid_refresh_response")
		return
	}
	value, err := nativeOAuthGrant(grant, start)
	if err != nil {
		fail(502, "invalid_refresh_response")
		return
	}
	access := []byte(value.Access)
	defer clear(access)
	client, err := New(borrowedTransport{network: network, token: access})
	if err != nil {
		fail(503, "github_unavailable")
		return
	}
	user, _, err := client.AuthenticatedUser(ctx)
	if err != nil {
		if errors.Is(err, ErrCredentials) {
			fail(401, "github_sign_in_required")
		} else {
			fail(502, "github_refresh_unconfirmed")
		}
		return
	}
	_ = json.NewEncoder(w).Encode(struct {
		Grant   nativeGrant       `json:"grant"`
		Account map[string]string `json:"account"`
	}{value, map[string]string{"external_id": strconv.FormatInt(user.GetID(), 10), "login": user.GetLogin()}})
}
