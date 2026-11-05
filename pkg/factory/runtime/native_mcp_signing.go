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
	"time"

	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/factory"
)

// NativeMCPSecretDeriver asks the Rust owner for one recorded capability's
// scoped secret. The Go coordinator retains capability and run admission;
// the persistent signing key never enters this process. Config must come from
// LoadNative and instance from the current owned backend health response.
func NativeMCPSecretDeriver(config NativeConfig, instance string) factory.MCPSecretDeriver {
	return func(ctx context.Context, capability factory.MCPCapability) (string, error) {
		if capability.Validate() != nil {
			return "", errors.New("native MCP signing unavailable")
		}
		var result struct {
			Secret string `json:"secret"`
		}
		err := nativeMCPCall(ctx, config, instance, "/api/native/mcp-sign", struct {
			CapabilityID string `json:"capability_id"`
			ScopeDigest  string `json:"scope_digest"`
		}{capability.ID, capability.ScopeDigest()}, &result)
		if err != nil || !factory.ValidMCPSecret(result.Secret) {
			return "", errors.New("native MCP signing unavailable")
		}
		return result.Secret, nil
	}
}

// NativeMCPSecretVerifier keeps the expected worker secret in Rust. A false
// result authenticates nothing; current Go capability and run admission still
// follow a positive result. No root key or expected secret is returned.
func NativeMCPSecretVerifier(config NativeConfig, instance string) factory.MCPSecretVerifier {
	return func(ctx context.Context, capability factory.MCPCapability, presented string) (bool, error) {
		if capability.Validate() != nil {
			return false, errors.New("native MCP verification unavailable")
		}
		if !factory.ValidMCPSecret(presented) {
			return false, nil
		}
		var result struct {
			Valid *bool `json:"valid"`
		}
		err := nativeMCPCall(ctx, config, instance, "/api/native/mcp-verify", struct {
			CapabilityID string `json:"capability_id"`
			ScopeDigest  string `json:"scope_digest"`
			Secret       string `json:"secret"`
		}{capability.ID, capability.ScopeDigest(), presented}, &result)
		if err != nil || result.Valid == nil {
			return false, errors.New("native MCP verification unavailable")
		}
		return *result.Valid, nil
	}
}

func nativeMCPCall(ctx context.Context, config NativeConfig, instance, route string, payload, result any) error {
	if ctx.Err() != nil || !collaboration.ValidID(instance) || (route != "/api/native/mcp-sign" && route != "/api/native/mcp-verify") {
		return errors.New("native MCP callback unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	body, err := json.Marshal(payload)
	if err != nil {
		return errors.New("native MCP callback unavailable")
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return nativePortDial(ctx, config)
		},
		DisableKeepAlives: true, MaxResponseHeaderBytes: 4096, ResponseHeaderTimeout: 5 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://soda-native"+route, bytes.NewReader(body))
	if err != nil {
		return errors.New("native MCP callback unavailable")
	}
	request.Header.Set("Authorization", "Bearer "+hex.EncodeToString(config.IPCKey()))
	request.Header.Set("X-Soda-Native-Instance", instance)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return errors.New("native MCP callback unavailable")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 257))
	if err != nil || len(raw) > 256 || response.StatusCode != http.StatusOK || ctx.Err() != nil || nativeDecode(raw, result) != nil {
		return errors.New("native MCP callback unavailable")
	}
	return nil
}
