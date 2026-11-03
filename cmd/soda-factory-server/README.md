# soda-factory-server — Soda Factory

Run the software factory independently of the Soda Forge backend. Factory owns
intake, planning, assignments, execution coordination, policy, verification,
publication, review, merge eligibility and the durable work ledger. Its HTTP,
operator and worker MCP lifetimes belong to this process.

## Native Linux build

The default build selects the native network/process host, excluding
the appliance web, Fountain SDK, host and identity clients from its dependency closure:

```sh
mkdir -p .artifacts/native
go build -o .artifacts/native/soda-factory-server ./cmd/soda-factory-server
.artifacts/native/soda-factory-server --config=/absolute/owned-profile/backend.json
```

The Rust [native lifecycle](../../lib/desktop-runtime/README.md#private-backend-inputs)
owns PostgreSQL and prepares this private bootstrap before starting the Go child.
The native binary accepts only its explicit configuration path, with no appliance
defaults or TCP-listener flags. It exposes authenticated private Unix readiness;
the runtime [contract](../../pkg/factory/runtime/README.md) owns admission,
exclusive profile use, cancellation and socket cleanup. Before readiness it
recovers proved interrupted contribution assessments through the shared
application, without replaying model calls or inviting agents. Live and unknown
historical claims remain unchanged; recovery failure prevents readiness. Store
opening and recovery share a cancellable ten-second startup budget.
It preserves unobserved execution records and never dispatches retained work.
Shared product routes are mounted through the same
application owner, but refuse without a current connector/account resolver.
The native composition also supplies the typed GitHub read handler behind its
authenticated private IPC/instance/lifetime gates. Rust lends credentials for
one bounded request; the Go SDK verifies the remote account before reads. This
transport does not itself establish login, bind the product or enable work.
Actual account/resource admission, execution observation/retirement, executor and the installed
Tauri supervisor remain subsequent consumers.
macOS native execution/qualification remains deferred.

## Forge integration

The appliance entrypoint and native Fountain adapters remain owned by
[SodaOS](https://github.com/LevitateOS/sodaos). Forge consumes the extracted public
Factory module for its shared engine and services. Fountain's native server
implementation belongs to [Fountain](https://github.com/veighnsche/fountain/tree/main).
This standalone command does not select either product's host implementation.
