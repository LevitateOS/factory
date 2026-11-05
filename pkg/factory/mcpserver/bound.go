package mcpserver

import (
	"context"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	factoryhttpapi "github.com/levitateos/factory/pkg/factory/httpapi"
	"github.com/levitateos/factory/pkg/factory/mcpwire"
)

// NewHandler authenticates the relay outside the tool payload.
// The same shared service exposes a separately filtered interface for each run.
// Stateless requests reuse one rate limiter, not a new allowance per connection.
func NewHandler(deps Dependencies, key []byte) http.Handler {
	key = append([]byte(nil), key...)
	var derive factory.MCPSecretDeriver
	if len(key) >= 32 {
		derive = func(_ context.Context, capability factory.MCPCapability) (string, error) {
			return factory.DeriveMCPCapabilitySecret(key, capability)
		}
	}
	return NewHandlerWithSecretDeriver(deps, derive)
}

// NewHandlerWithSecretDeriver preserves the older host adapter contract.
func NewHandlerWithSecretDeriver(deps Dependencies, derive factory.MCPSecretDeriver) http.Handler {
	return newBoundary(deps).HandlerWithSecretDeriver(derive)
}

// NewHandlerWithSecretVerifier keeps expected secret bytes in the Rust native
// owner. Go still owns capability lookup and current run/tool admission.
func NewHandlerWithSecretVerifier(deps Dependencies, verify factory.MCPSecretVerifier) http.Handler {
	return newBoundary(deps).HandlerWithSecretVerifier(verify)
}

func newBoundary(deps Dependencies) factoryhttpapi.Workers {
	deps.limiter = newMCPRateLimiter()
	boundary := factoryhttpapi.Workers{Capabilities: deps.Capabilities, Now: deps.Now}
	boundary.Admit = func(ctx context.Context, capability factory.MCPCapability) (factory.MCPCapability, error) {
		var err error
		if deps.Prepare != nil {
			capability, err = deps.Prepare(ctx, capability)
			if err != nil {
				return factory.MCPCapability{}, err
			}
		}
		observed := control.MCPRunObservation{}
		if deps.Observed != nil {
			observed, err = deps.Observed(capability)
		}
		if err != nil {
			return factory.MCPCapability{}, err
		}
		now := time.Now()
		if deps.Now != nil {
			now = deps.Now()
		}
		return capability, control.AdmitMCPCapability(capability, factory.MCPToolContext, now, observed)
	}
	boundary.Protocol = func(ctx context.Context, capability factory.MCPCapability) (http.Handler, error) {
		bound := deps
		bound.boundCapability = &capability
		if bound.NativeTask != nil {
			if bound.Runs == nil {
				bound.NativeTask = nil
			} else if run, err := bound.Runs.FactoryRun(ctx, capability.RunID); err != nil || !run.SupportsTaskCommands() {
				bound.NativeTask = nil
			}
		}
		server := newFactoryCatalogServer(bound)
		return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}), nil
	}
	return boundary
}

// mcpTakesSubmission reports whether a tool needs a request key. Reads,
// previews, native task requests and offset-based uploads do not.
func mcpTakesSubmission(tool factory.MCPTool) bool {
	return !factory.MCPIsRead(tool) && tool != factory.MCPToolDiscussion &&
		tool != factory.MCPToolReadLibraryItem &&
		tool != factory.MCPToolPreviewRepair && tool != factory.MCPToolPublishArtifact && !mcpwire.IsNativeTask(tool)
}
