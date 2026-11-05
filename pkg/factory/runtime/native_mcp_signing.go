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
		if ctx.Err() != nil || !collaboration.ValidID(instance) || capability.Validate() != nil {
			return "", errors.New("native MCP signing unavailable")
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		body, err := json.Marshal(struct {
			CapabilityID string `json:"capability_id"`
			ScopeDigest  string `json:"scope_digest"`
		}{capability.ID, capability.ScopeDigest()})
		if err != nil {
			return "", errors.New("native MCP signing unavailable")
		}
		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return nativePortDial(ctx, config)
			},
			DisableKeepAlives: true, MaxResponseHeaderBytes: 4096, ResponseHeaderTimeout: 5 * time.Second,
		}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://soda-native/api/native/mcp-sign", bytes.NewReader(body))
		if err != nil {
			return "", errors.New("native MCP signing unavailable")
		}
		request.Header.Set("Authorization", "Bearer "+hex.EncodeToString(config.IPCKey()))
		request.Header.Set("X-Soda-Native-Instance", instance)
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			return "", errors.New("native MCP signing unavailable")
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, 257))
		if err != nil || len(raw) > 256 || response.StatusCode != http.StatusOK || ctx.Err() != nil {
			return "", errors.New("native MCP signing unavailable")
		}
		var result struct {
			Secret string `json:"secret"`
		}
		if nativeDecode(raw, &result) != nil || !factory.ValidMCPSecret(result.Secret) {
			return "", errors.New("native MCP signing unavailable")
		}
		return result.Secret, nil
	}
}
