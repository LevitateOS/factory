# Go and Rust responsibility audit

Date: 2026-11-03. Source baseline: `cde3b4a1c5245ea5c7432d7aaf4b095f78c56dff`,
plus the in-progress native GitHub issue-reading changes visible during inspection.
Three independent reviewers inspected native resources, engine/storage, and
API/provider boundaries. Their conclusions were challenged across those scopes.
This is a source-based allocation audit, not implementation or runtime qualification.
No builds or test suites were run. Existing work in progress was preserved.

## Selected allocation

The owner has adopted this allocation as the implementation target. The owners
below are decisions, including Rust ownership of the complete authenticated
desktop GitHub connector. Remaining migration work does not reopen ownership.

Keep one Go owner for durable product decisions and transactions. Give Rust
substantial ownership of native execution, resource lifetimes, and the complete
authenticated desktop provider session. Keep the Rust runtime usable without
Tauri so the desktop shell does not become a dependency of headless execution.

The useful distinction is between a **product decision** and a **native effect**.
Go decides which admitted work should happen and records its intent and outcome.
Rust performs bounded native operations, enforces its local capability/resource
constraints, and returns observations and receipts. Neither language should
duplicate the other's authoritative state machine.

This allocation is based on responsibility cohesion, not the existing
language policy or the amount of code already written. Both languages can
implement workflows, networking, and SQL. The inspected source does not establish
a performance or memory requirement that warrants replacing the whole Go engine.

## Responsibility owners

| Responsibility | Owner | Why | Current action |
| --- | --- | --- | --- |
| HTTP API, worker MCP, request decoding, response projection | Go | Cohesive service interfaces and network concurrency; serves desktop and Forge consumers | Retain; keep transport separate from domain decisions |
| Current product authorization, accepted revisions, budgets and sponsorship | Go | Decisions require canonical state and transactional admission | Retain with the ledger; native operations independently check their own capabilities |
| Planning, durable queue priority, workflow scheduling, publication/recovery decisions | Go | Database-backed reconciliation across provider and worker observations, rather than OS process scheduling | Retain one coordinator; remove direct native mechanisms from its implementation |
| Canonical repository/account/actor registry | Go | Provider-neutral mappings must be recorded consistently with product identity | Retain; Rust supplies account-bound provider observations |
| PostgreSQL product schema, migrations, transactions and connection pool | Go | Aggregate transitions need one transaction owner | Retain together; no independent Rust writes to allowance, assignment or workflow tables |
| PostgreSQL server provisioning, private cluster files, startup and shutdown | Rust | Child-process and filesystem lifetime belong to the native runtime | Retain; distinguish cluster administration from SQL product persistence |
| Native worker processes, process trees, PTYs, hard deadlines and retirement | Rust | Explicit handle ownership, bounded native resources and independent cleanup | Implement the missing native executor behind semantic operation contracts |
| Worktrees, Git effects, local artifacts, executable inspection and output collection | Rust | These effects share workspace/process/resource custody with execution | Implement with the executor; Go requests operations and retains returned facts |
| OS keyring, withdrawal, refresh reservation, secret derivation/delivery | Rust | Keep native credential and capability lifetime under one authority | Retain existing custody; resolve dormant signing-key ownership before enabling execution |
| Native GitHub OAuth, refresh, authenticated requests and provider-local decoding | Rust | Co-locates the complete authenticated operation with credential lifetime; removes the token round trip through Go | Move the complete connector as one cohesive cutover, preserving its existing behavior |
| Fountain native collaboration adapter | Go in its owning host integration | Uses Fountain's Go SDK and server authority; it is not a desktop keyring session | Retain outside the standalone engine's dependency closure |
| Tauri windows, IPC caller admission, native events and runtime generations | Rust | Direct native host integration and resource lifetime | Retain; keep product policy out of the shell |
| Shared presentation and drafts | TypeScript/Lit | Existing shared UI for both hosts | Retain; keep authority and secrets outside browser state |
| Build orchestration | Existing scripts/tooling | Calling Go, Cargo and frontend builders does not require another language migration | Retain unless an observed packaging need requires a change |

## Findings and evidence

### 1. The Go engine is a durable service owner, not a native executor

Observed: [Application](../../../pkg/factory/service/application.go#L38) composes
one Store and coordinator. Its explicit [Start](../../../pkg/factory/service/application.go#L68)
requires admitted runtime ports. The [progress loop](../../../pkg/factory/control/progress.go#L73)
reconciles recorded work; dispatch records admission before asking the host to
launch. [RecordDispatchPacket](../../../pkg/store/factory_dispatch_packet.go#L36)
atomically owns assignment, reservation, run and display binding.

Allocation: keep service scheduling, spending and durable workflow transitions
together in Go. Rust owns process scheduling, containment and deadlines even if
the Go process becomes unavailable. Moving just the scheduler while retaining
its transactional state in Go would add a communication boundary without moving
a coherent responsibility.

Rust can express some state combinations more strongly. For example,
[assignment subjects](../../../pkg/factory/assignment.go#L106) use a string kind
and optional payloads; Rust enums could represent those alternatives directly.
That helps in-memory state modeling but does not replace current-authority,
retained-record, temporal or database validation. It is not by itself a reason
to port every domain record.

### 2. Native execution and connected product composition are missing

Observed: the [native Go server](../../../pkg/factory/runtime/native_server.go#L54)
constructs the application with only Store and a nil product binder. It performs
retained assessment recovery but does not start the execution coordinator.
The [desktop entry](../../../frontend/desktop/main.ts#L18) mounts the shared app
and clears its context on retirement, but does not assign a connected product
context. The [Tauri query surface](../../../cmd/soda-desktop/src/bridge.rs#L4)
is also only a subset of the shared product API.

Allocation: implement actual Rust execution and complete native account,
product and runtime-port composition. Go's execution records and ports must not
be mistaken for an existing Go implementation of worktree/process control.
A language rewrite would not supply these missing connections by itself.

### 3. Rust already owns meaningful native lifetimes

Observed: the [supervisor](../../../lib/desktop-runtime/src/supervisor.rs#L62)
owns a dedicated spawning thread, PostgreSQL and Go children, private inputs,
child monitoring, and ordered retirement. [Database provisioning](../../../lib/desktop-runtime/src/database.rs#L26)
owns the private cluster and its process. [Credential custody](../../../lib/desktop-runtime/src/credentials.rs#L181)
owns OS keyring access, capability lifetime, withdrawal and bounded token use.

Allocation: retain and extend these Rust owners into a headless native
execution service. Keep process exit, process-tree retirement, artifact sealing
and credential release distinct; one successful process exit cannot stand in
for all four observations.

### 4. Slow product I/O shares Rust's lifecycle control path

Observed: [run_owner](../../../lib/desktop-runtime/src/supervisor.rs#L183)
handles commands sequentially. A product request performs health I/O and then
a synchronous request with a fifteen-second deadline before checking children
again. [Desktop::call](../../../cmd/soda-desktop/src/main.rs#L142) holds the
runtime mutex while it waits; shutdown also needs that runtime owner.

Implication: ordinary requests can delay stop and health handling. This is a
source-derived blocking risk, not a reproduced runtime failure or evidence
against Rust.

Allocation: retain one owner of children and capabilities, while moving
bounded request I/O into cancellable work whose retirement is joined explicitly.
The [async Unix transport](../../../lib/unix-http/src/lib.rs#L109) already supplies
a useful primitive. Address this before attaching a busy native executor.

### 5. Native credential storage does not mean credentials stay in Rust

Observed: [Rust GitHub reads](../../../lib/desktop-runtime/src/github.rs#L632)
borrow a token and send it to the Go sidecar in a private HTTP header.
[Go's native handler](../../../pkg/github/native.go#L56) consumes that token and
uses it in the outgoing provider request. Device exchange and
[refresh](../../../pkg/github/refresh.go#L30) follow the same Rust-to-Go provider
route. Rust also interprets the replies. This is deliberately bounded, but
credential-bearing operations are split across both languages.

Allocation: Rust owns the complete native GitHub connector: authorization,
refresh, current-account verification, authenticated transport and provider-local
response decoding together. Go receives bounded nonsecret observations bound
to the selected connection, and alone applies canonical registry transactions.

This is a cohesion and secret-lifetime argument, not a claim that Rust has a
better HTTP library. The current [Go provider client](../../../pkg/github/client.go#L60)
is a viable implementation. Moving it has real cost: pagination, response bounds,
errors, cancellation, private access, identity checks and consistency rules must
move with it. Do not retain competing native GitHub implementations or add a
generic Rust signer that turns each Go HTTP request into another IPC round trip.

### 6. Checking belongs at several boundaries, with one business-policy owner

Keep these separate:

- Go API: decode requests, enforce request bounds and current service admission.
- Go domain/store: enforce accepted revisions, budgets, identity and atomic state transitions.
- Rust IPC/native runtime: admit the caller and capability, validate resource selectors,
  enforce native limits, and reject retired work before applying local effects.
- Provider adapter: verify external identity and consistency before returning observations.

Repeated checks that cover different trust boundaries or changing state are
appropriate. Reimplementing the same product policy independently is not.
Current examples include [Tauri admission](../../../cmd/soda-desktop/src/main.rs#L167),
[Go private-channel admission](../../../pkg/factory/runtime/native_server.go#L138),
and [collaboration issue services](../../../pkg/factory/service/collaboration_issues.go#L38).
The growing Rust canonical-page decoders should remain boundary validation,
not become a second product model. Keep one canonical wire definition and use
derived DTO/schema checks where practical without adding a new framework solely
for this audit.

### 7. Keep transaction ownership cohesive; distinguish database meanings

Observed: [Store](../../../pkg/store/store.go#L27) owns its pool, schema and row
operations. [Reservation transitions](../../../pkg/store/factory_reservations.go#L55)
and dispatch admission rely on locks, uniqueness and conditional SQL.

Allocation: Rust owns the PostgreSQL *process*; Go owns the product
*transactions*. Rust ownership rules do not prove freshness or atomicity across
two independent writers. If an aggregate ever moves to Rust, move its complete
transition, persistence, migration and recovery together. Do not split admission
into a Go read followed by unrelated Rust writes.

### 8. Small Go OS operations are not a reason to add native RPCs

Observed: Go secures and drains its own [Unix listener](../../../pkg/factory/runtime/native_listener_linux.go#L15)
and validates [private bootstrap input](../../../pkg/factory/runtime/native_config_linux.go#L27).
Its coordinator uses a local ownership lock; product transactions use database
locks for their different scope.

Allocation: retain coherent endpoint lifecycle in Go. Moving every file read
or lock into Rust would increase coupling. A local file lock is also not a
distributed lease, regardless of its implementation language; multi-host
coordination would require an explicit different ownership design.

The [MCP signing-key helper](../../../pkg/factory/control/mcp_key.go#L13) is a
separate activation concern. No non-test caller was found in extracted Factory's
native composition, so this is not a verified active native leak. Before using
the [worker-secret derivation](../../../pkg/factory/control/dispatch_launch.go#L717)
path, assign native key custody and verifiable proof delivery explicitly. Model
provider key-file access likewise needs a deliberate authenticated-adapter owner
when those workflow ports are enabled.

## Alternatives considered

| Design | Assessment |
| --- | --- |
| Keep the current split exactly | Rejected: leaves native execution/composition gaps, lifecycle blocking and split authenticated-provider operations unresolved |
| Thin Go API over a complete Rust engine | Rejected: requires moving domain, scheduling, registry, transactions and recovery together, including headless Forge consumers, while retaining IPC and deployment complexity if Go remains a forwarding process. No inspected workload requirement justifies that broad port |
| Targeted redistribution | Selected: preserve Go's durable service owner, implement substantive Rust native execution, isolate lifecycle control, and move the complete native authenticated connector |

Tauri supports [external binaries](https://v2.tauri.app/develop/sidecar/), so using
it neither requires nor forbids a Go engine. Go's [HTTP](https://pkg.go.dev/net/http)
and [SQL](https://pkg.go.dev/database/sql) facilities make service ownership a
natural fit. Rust's [ownership and scope-based cleanup](https://doc.rust-lang.org/book/ch04-01-what-is-ownership.html)
support native resource lifetime. These are technical capabilities; the allocation
above is the adopted engineering decision, not a language benchmark result.

## Dependency-ordered implementation packets

These packets implement the selected allocation; they are not work performed by
this audit. Integrate
them into the canonical task queue before implementing; do not create a competing
progress ledger.

| Packet | Dependency | Coherent result and cutover |
| --- | --- | --- |
| N1: independent native control | None | Product I/O no longer blocks the child owner; retirement cancels and joins requests without stale replies |
| N2: native product composition | Current account/registry producers | Bind the shared UI and Go product services to the selected account/repository; supply explicit unavailable states for missing execution |
| N3: Rust execution authority | N1 and admitted execution contract | Implement complete local process/workspace/Git/custody operations; keep hard deadlines and cleanup enforceable without Go progress polling |
| N4: execution consumer cutover | N2, N3 | Supply the Go native execution ports and explicitly start the single coordinator only when dependencies are admitted; preserve one ledger and reconcile returned evidence |
| P1: cohesive native provider session | Account contract | Move the complete authenticated GitHub connector to Rust; keep canonical mapping and SQL in Go; retire the old native provider implementation and token-carrying route together |
| C1: shared contract consolidation | Relevant operation owner selected | Keep current wire bounds and typed outcomes; remove duplicated product semantics while preserving independent boundary checks |

Native work and the connector work can be developed independently only with
separate source ownership and agreed contracts. Their current shared Rust
credential/supervisor files require a single writer or serial cutovers. No new
test hierarchy is proposed. Subsequent implementation should be checked through
the actual affected application operation, with no claim of installed or
cross-platform completion from source inspection alone.

## Evidence limits and decision status

All three reviewers support retaining Go's durable service/transaction owner and
substantial Rust native ownership. The initial engine reviewer qualified its
signing-key finding after independent review established the native caller is
dormant. Independent review also rejected treating an older SodaOS API caller as
a defect solely because it differs from Factory HEAD: its pinned module revision
must be considered.

The connector move is selected and has a real migration cost. It is not yet
implemented; the decision follows responsibility ownership, rather than a Tauri
requirement. No measurements establish that
either language would make the current engine faster. This audit covers the named
language boundaries and representative state/effect paths, not every source file
or every correctness question. Provider source was changing during inspection;
line references describe that observed working tree. Production packaging,
Linux/macOS parity, live provider journeys and the native execution journey remain
outside the performed evidence.
