package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/levitateos/factory/pkg/factory"
	"github.com/levitateos/factory/pkg/factory/control"
	factoryhttpapi "github.com/levitateos/factory/pkg/factory/httpapi"
	"github.com/levitateos/factory/pkg/project"
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
	deps = completeLegacyDependencies(deps)
	deps.limiter = newMCPRateLimiter()
	boundary := factoryhttpapi.Workers{Capabilities: deps.Capabilities, Now: deps.Now}
	boundary.Admit = func(ctx context.Context, capability factory.MCPCapability) (factory.MCPCapability, error) {
		if deps.Prepare == nil || deps.Observed == nil {
			return factory.MCPCapability{}, errors.New("factory MCP current admission unavailable")
		}
		prepared, err := deps.Prepare(ctx, capability)
		if err != nil || prepared.Validate() != nil || prepared.ScopeDigest() != capability.ScopeDigest() {
			return factory.MCPCapability{}, errors.New("factory MCP current work unavailable")
		}
		observed, err := deps.Observed(prepared)
		if err != nil || observed.RunID != prepared.RunID || observed.AssignmentID != prepared.AssignmentID || observed.Authority != prepared.Authority {
			return factory.MCPCapability{}, errors.New("factory MCP current run unavailable")
		}
		now := time.Now()
		if deps.Now != nil {
			now = deps.Now()
		}
		return prepared, control.AdmitMCPCapability(prepared, factory.MCPToolContext, now, observed)
	}
	boundary.Protocol = func(ctx context.Context, capability factory.MCPCapability) (http.Handler, error) {
		server := newBoundCatalogServer(ctx, deps, capability)
		return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}), nil
	}
	return boundary
}

func newBoundCatalogServer(ctx context.Context, deps Dependencies, capability factory.MCPCapability) *mcp.Server {
	bound := bindCatalog(ctx, deps, capability)
	return newCatalogServer(bound, catalogTools(bound, capability))
}

func bindCatalog(ctx context.Context, deps Dependencies, capability factory.MCPCapability) Dependencies {
	deps.boundCapability = &capability
	if deps.NativeTask != nil {
		if deps.Runs == nil {
			deps.NativeTask = nil
		} else if run, err := deps.Runs.FactoryRun(ctx, capability.RunID); err != nil || run.ID != capability.RunID || !run.SupportsTaskCommands() {
			deps.NativeTask = nil
		} else if manifest, err := project.ParseExecutionManifest(run.ExecutionManifest); err == nil {
			deps.sequentialTasks = manifest.MaxChildren == 0
		}
	}
	return deps
}
