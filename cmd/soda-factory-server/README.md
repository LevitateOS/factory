# soda-factory-server — Soda Factory

Run the software factory independently of the Soda Forge backend. Factory owns
intake, planning, assignments, execution coordination, policy, verification,
publication, review, merge eligibility and the durable work ledger. Its HTTP,
operator and worker MCP lifetimes belong to this process.

## Native Linux build

The `factory_native` build selects the native network/process host, excluding
the appliance web, Fountain SDK, host and identity clients from its dependency closure:

```sh
mkdir -p .artifacts/native
go build -tags=factory_native -o .artifacts/native/soda-factory-server ./cmd/soda-factory-server
.artifacts/native/soda-factory-server --config=/absolute/owned-profile/backend.json
```

The Rust [native lifecycle](../../lib/desktop-runtime/README.md#private-backend-inputs)
owns PostgreSQL and prepares this private bootstrap before starting the Go child.
The native binary accepts only its explicit configuration path, with no appliance
defaults or TCP-listener flags. It exposes authenticated private Unix readiness;
the runtime [contract](../../internal/factory/runtime/README.md) owns admission,
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

## Appliance build

The default build keeps the deployed Forge integration and its existing command
identity and flags. Both hosts consume the same extracted Factory services;
building the native process does not qualify a connected desktop application.

```sh
go build -o .artifacts/bin/soda-factory-server ./cmd/soda-factory-server
.artifacts/bin/soda-factory-server --config=/etc/soda/factory.json \
  --extension-socket=/run/soda/factory/extension/service.sock \
  --control-socket=/run/soda/factory/control/control.sock \
  --operator-socket=/run/soda/factory-operator/operator.sock --operator-uid=0
```

Provision the socket directories for the service user before starting. Factory
requires PostgreSQL and the configured Fountain collaboration integration.
Execution uses `soda-host` and the separately running identity broker. Those
services must provide the existing Project, credential and native authority
contracts. This extraction does not implement other forge or execution backends.

The appliance executable targets Linux. Work still executes through `soda-host`
in persistent Project containers. Native execution on the operator's computer,
including macOS, is not implemented by the process split. The current Fountain
SDK dependency also uses Linux-only Unix peer-credential APIs, so this executable
does not cross-compile for macOS. A native host executor and portable service
connections are required for that product configuration.

The appliance configuration schema is shared with Forge. A minimal Factory JSON
contains:

```json
{
  "listen": "127.0.0.1:8081",
  "forgejo_url": "https://forge.example.test",
  "forgejo_internal_url": "http://127.0.0.1:3000",
  "database_dsn_file": "/etc/soda/postgres/soda.dsn",
  "host_socket": "/run/soda/host/host.sock",
  "identity_socket": "/run/soda/identity/admin.sock",
  "operator_id": 1,
  "factory_publication_root": "/var/lib/soda/dashboard/publication"
}
```

The credential file is private and contains the PostgreSQL connection URL.
Native background operation bindings, their credential files, provider access
and repository policy must be configured before automatic work can proceed; a
healthy unconfigured service does not claim readiness to execute an assignment.
See [configuration](../../docs/reference/configuration.md),
[operator enrollment](../../docs/reference/factory.md), and
[credentials](../../docs/reference/credentials.md).

## Listeners and integration

| Listener | Admission and purpose |
| --- | --- |
| Configured loopback HTTP address | Health and HMAC-authenticated intake. `--listen` overrides the configured address. |
| `--extension-socket` | Factory API for the Fountain extension; native browser authority is verified by Factory. |
| `--control-socket` | Project stop/start and authority operations; only the service's OS peer UID is admitted. Never mount in Projects. |
| `--operator-socket` | Status, stop and reconciliation through the configured `--operator-uid`. |
| Fixed `factory-ipc/mcp.sock` under the data root | Worker MCP; every call requires a live run capability. |

The Fountain extension connects directly using its `--factory-socket` flag and
reuses the existing browser UI. Factory browser requests do not pass through the
Forge backend. Forge uses only `factory_control_socket` in its JSON or
`--factory-control-socket` for Project lifecycle coordination. Mount the
extension and control socket directories separately. Factory continues running
when the Forge backend stops, and the frontend reconnects after Factory restarts.
Requests are never silently retried or run by a fallback coordinator.

Caddy sends the public `/-/soda/api/factory/intake` route directly to Factory's
loopback listener on port 8081. Enrolled native issue hooks address that same
Factory listener and retain HMAC authentication.

## Appliance packaging and retained state

`soda-factory.service` and `soda-dashboard.service` are separately supervised
containers. The appliance packages both commands in its existing backend image
and uses the operator-generated `/etc/soda/dashboard.json` for their shared
integration inputs. Factory overrides its listener to port 8081. A separately
installed Factory uses `/etc/soda/factory.json` by default.

Both products currently share the existing Soda PostgreSQL schema and Project
records. Factory retains the publication/data root under `/var/lib/soda/dashboard`
so existing worker mounts, keys and ledger locks continue to identify the same
work. This is not an independent database extraction. The broker keeps its
separate custody database.

SIGINT/SIGTERM closes admission, drains requests and retires the coordinator
lifetime. Inspect an appliance with:

```sh
systemctl status soda-factory.service
journalctl -u soda-factory.service
soda-factory --socket /run/soda/factory-operator/operator.sock status
```
