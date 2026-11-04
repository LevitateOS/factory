# Open security gates and Daybreak handoff

Updated 2026-11-05. This is an evidence register, not a completion ledger. The
canonical order and statuses remain in
[implementation-tasks.md](implementation-tasks.md); the selected ownership remains
in [language-allocation.md](language-allocation.md). All gates below are open.
Normal coding agents can implement and verify routine product security work,
including these gates and ordinary checks. Use a user-started Daybreak session
only when work or a check looks like hacking; Daybreak has the anti-hacking audit
for that work. Do not infer a passed gate from a proposed design, a source build, a
fixture, or an earlier component probe. Do not place credentials or private
profile contents in this file.

The plan describes failed `connected-ui-v2` and `ui-v4` attempts in an external
`.factory-signin-checks` root. That root was not found under `/home/vince` in
this workspace on 2026-11-04. The recorded crash and the backtrace description
are historical plan evidence; the original logs, cores and exact input hashes
could not be re-inspected here. Retrieve them before claiming a diagnosis.
A prior worktree checkpoint passed normal and opt-in native desktop source
checks after the generated UI was built; those checks did not diagnose the
crash or qualify connected authentication and IPC.
On 2026-11-05 a repeat `cargo check` for the native desktop stopped before
compiling the app because the current environment has no pkg-config entries for
GDK Pixbuf, Pango, libsoup 3 or JavaScriptCoreGTK 4.1. The earlier successful
desktop checks remain historical evidence; restore the Linux desktop sysroot
before repeating them. The current system coredump index and user journal did
not contain the recorded WebKit crash artifact.
A production-bundle Chromium check with a local Tauri command fixture now proves
that a failed `credential_status` read removes stale saved-account UI rows and
repository options. It uses no real keyring, provider, consent or authenticated
IPC, so the connected gate remains open.

## Gate register

| Gate and dependent slices | Exact blocker and dependency | Relevant files | Proven so far | Still unverified; evidence needed to close |
| --- | --- | --- | --- | --- |
| Desktop authentication and IPC admission: T06-CONNECT, then T06-GITHUB-EVIDENCE, T06-GITHUB, T07-READ and T11-WRITES | The connected desktop journey has not completed. T06-CONNECT consumes the completed T06-CONNECT-BRIDGE, but real current-account selection, authenticated reads and per-window product IPC have not passed together. A retained pre-consent WebKit/heap crash interrupts the journey. Do not repeat a challenge merely to bypass the crash. | `cmd/soda-desktop/src/{main,signin,bridge,product}.rs`, `frontend/desktop/{main,accounts,connections,transport,product}.ts`, `lib/desktop-runtime/src/{github,credentials,supervisor}.rs`, `pkg/factory/runtime/{native_application,native_product}.go` | Source/component builds, shared selector/draft checks and bridge producer checks are recorded under T06-CONNECT. A current WebKit runtime passed an unapproved challenge/cancel journey. Earlier approved headless native GitHub reads and private account retention passed in the P1 producer. | Diagnose the crash without treating its detection stack as root cause; then run the actual connected Tauri/WebKit account, repository, issue/detail/feed, switch/reconnect, draft and retirement journey with current IPC admission. Capture exact runtime/source identity, successful and denied caller/resource observations, and a retained result. Successful connected consent or authenticated IPC has not been proved by the source checkpoint. |
| GitHub credential custody and revocation: T06-GITHUB and the connected GitHub consumers | The cohesive Rust connector is implemented, but the final provider gate depends on T06-CONNECT and T06-GITHUB-EVIDENCE. The connected desktop has not proved remote revocation and full private/paged/current-head observations. | `lib/desktop-runtime/src/credentials/`, `lib/desktop-runtime/src/github_provider/`, `lib/desktop-runtime/src/github.rs`, `pkg/factory/runtime/native_product_observations.go`, `pkg/factory/runtime/native_product_reads.go` | The Go native token route was retired; Rust owns OAuth, refresh, HTTPS, decoding and native credential lifetime. Approved native reads, private access, refresh and retained identity are recorded in the plan. | Prove final connected consent/identity/storage, current-account and canonical binding, private/paged observations, refresh if issued, remote revocation and local removal without credential disclosure. Retire or migrate historical Go-observer check consumers only with equivalent current evidence. |
| D1 standing GitHub automation policy: T11-INTAKE, T12-GITHUB-PRS and installed GitHub automation | T11-INTAKE depends on T09-EXECUTION, T11-WRITES and T12-PLACEMENT. Before automatic intake, select and enforce the explicit standing authorization policy or retain original-author creation evidence as mandatory; discovered work must wait for maintainer adoption until then. | `pkg/factory/runtime/placement.go`, `pkg/factory/control/dispatch_launch.go`, `pkg/factory/execution/`, `lib/desktop-runtime/src/github_provider/` | The plan identifies the policy choices and keeps automatic intake inactive. Existing canonical authorization and ledger owners remain Go. | Record the selected policy, eligible actor/fork/repository, limits and accepted revision; prove authenticated polling/cursors, wake reconciliation, refusal of unauthorized or self-triggered work, and the eligible issue-to-PR journey through the admitted isolated executor. |
| D2 Fountain desktop authentication and grant revocation: T10-GATEWAY, then T10-PUBLISH/T10-INTAKE and desktop Fountain journeys | T10-GATEWAY depends on T05-CREDENTIALS, T06-FOUNTAIN, T10-WRITES and T12-PLACEMENT. Supported native enrollment/login and revocable grants have not been proved. Browser authority must not be spoofed and the raw Unix socket must not be exposed. | `lib/desktop-runtime/src/credentials.rs`, `cmd/soda-desktop/src/{main,bridge}.rs`, `pkg/factory/runtime/placement.go`, Fountain host adapters described by T10-GATEWAY | Existing OAuth PKCE code is only spike evidence; Fountain SDK/native writes and shared UI components have separate completed producer evidence. | Prove supported actual desktop enrollment, current authenticated HTTPS reads and operations, scoped IPC admission, grant withdrawal/revocation, and centrally coordinated placement using the real host. Include denial after withdrawal and retained recovery. |
| D3 isolated native worker and execution admission: T09-NATIVE-EXECUTOR and T09-EXECUTION; then T10-PUBLISH, T11-INTAKE, packaging and installed automation | The selected worker must have a distinct host identity with no access to the desktop user's files, processes, session bus or keyring. The root broker must bind the exact admitted run, role, snapshot, worktree, process, credential and retirement. T09-EXECUTION additionally requires T06-CONNECT and T12-PLACEMENT. Product execution must remain unavailable until this gate closes. | `cmd/soda-factory-broker/`, `cmd/soda-factory-worker/`, `lib/native-process/`, `lib/desktop-runtime/src/{execution,supervisor,credentials}.rs`, `pkg/factory/execution/`, `pkg/factory/control/{dispatch_launch,mcp_key}.go`, `pkg/factory/runtime/{native_application,placement}.go` | The plan records live distinct-UID service, file/keyring denial probes, captured ELF transfer, bounded process/cgroup retirement, broker staging/preparation/relay/agent probes, exact-run attestation, owner-death teardown, and retained Go isolated-run validation. The headless process primitive is outside the production desktop dependency graph. These are component probes, some using dummy credentials or a fake provider. | Complete and qualify production Go admission and semantic Rust ports to the single coordinator; role-limited tool execution and exact worktree/candidate/artifact capture; strict workspace storage bound; cross-run denial; scoped credential delivery/withdrawal and MCP signing-key custody; durable transactional receipt/outcome reconciliation across crash/reboot; production Quit; and a real finite provider run. On the actual launch path capture worker UID, file/process/keyring/session denials, exact mounts and role capabilities, deadline/tree cleanup, credential withdrawal and retained recovery. No same-user fallback or dummy-token result closes D3. |
| Native provider credential proxy within D3 | The observed Muse CLI can accept a key on stdin and a base URL. Supplying the reusable owner key to the worker violates the selected custody rule. The relay/proxy must authorize one exact run and withdraw use on cancellation, owner death and restart. | `cmd/soda-factory-worker/src/bin/soda-factory-provider-relay.rs`, `cmd/soda-factory-worker/systemd/`, `lib/desktop-runtime/src/credentials/`, `pkg/factory/control/mcp_key.go` | A private-loopback fake provider, address-policy probe and invalid-token native owner/relay run are recorded. Owner pidfd teardown and subsequent local 503 after withdrawal were observed in disposable units. | Prove the final authenticated endpoint and network confinement, real owner-held credential use without exposing it to the worker, run-bound authorization, withdrawal/owner-crash/restart denial and an actual finite provider task. A base URL or dummy 401 alone is insufficient. |
| Authentication and admission at other native boundaries | Any new or changed broker, worker, product HTTP, IPC or grant path must be verified before activation. Normal coding agents may implement and qualify these boundaries; source validation alone is not admission proof. | `cmd/soda-factory-broker/src/main.rs`, `cmd/soda-factory-worker/src/`, `cmd/soda-desktop/src/`, `pkg/factory/runtime/native_server.go`, `pkg/factory/runtime/native_application.go` | Existing private Unix peer checks, bounded bridge contracts and some root-peer broker probes are recorded in their producer checkpoints. | For each activated path, demonstrate authenticated peer and exact instance/profile/run/resource binding, denied foreign/stale callers, cancellation/retirement, and no credential exposure through UI, logs or Go observation DTOs. Record the actual production path and its negative evidence. |
| D5 issue-only Fountain collaborator parity | Current Fountain extension admission requires code-unit read. Full issue-only visibility needs a separate Fountain capability change and authorized scope; ordinary code-reader journeys do not close it. | Fountain extension/adapters referenced by `T06-FOUNTAIN` and `T07-READ`; `pkg/factory/runtime/native_product_reads.go` for the standalone boundary | Current code-reader routes and shared UI have producer evidence. | Obtain the separate authorized Fountain capability implementation and prove issue-only reads/writes and denial outside scope on the actual host before claiming collaborator parity. |

## Current dependencies

The canonical table has T06-CONNECT Active. Its ordinary shared UI source and
component checks are recorded, but the connected acceptance still waits for the
native crash to be resolved and the actual authentication/IPC proof. In row
order, T06-GITHUB-EVIDENCE and T06-GITHUB then block T11-WRITES and the shared
cross-provider editor/PR slices. T07-READ also depends directly on T06-CONNECT.
T09-NATIVE-EXECUTOR is itself D3 work; T09-EXECUTION and its publication,
automation, packaging, installed and upgrade consumers remain pending. T10-GATEWAY
is D2 work. T12-PLACEMENT depends on T06-CONNECT and must keep automation inactive.

For D3 storage, the broker now has a source-level and direct filesystem check of
a 512 MiB aggregate allocated-block bound over retained preparation trees.
`cargo check --locked --offline -p soda-factory-broker` passes. The current host
has no installed `soda-factory-broker.socket`; no live broker/preparer activation
qualified this change. The bound does not cover coder workspace, agent output or
all tmpfs allocation. Close the storage part only after actual service-path
checks of retained multi-run capacity/refusal, cleanup/retry, coder/output bounds
and denial when `/run` has low free space, with exact process and filesystem
observations. This source checkpoint does not close D3.
The agent unit also now disables swap under its existing 1 GiB memory ceiling;
its binary built and a rendered scratch unit passed parser verification, but no
live agent service ran. Actual coder workspace/output limits, cleanup and
captured results still need service-path evidence before the D3 storage
requirement can close.

For D3 result custody, the worker now has a direct Git check of clean committed
candidate bundling and explicit dirty/unchanged states. The broker and desktop
source bind and hash up to 4 MiB of bundle bytes; the desktop fsyncs them into a
private receipt before retiring root staging. A v2 writer header collision was
corrected: `source_sha256` now records the admitted source while `bundle_sha256`
records the returned candidate bundle. A read-only native reader checks the
trusted run/profile/source/base candidate/role/tool/prompt identity, private
profile and receipt file, size and content hashes. A pending file without a
committed receipt reports uncertainty; a committed file does not prove root
retirement. The reader passed a local on-disk fixture with valid readback,
wrong source, altered output digest, interrupted pending write and older-version
refusal. This is only direct Git, source-compilation and local fixture evidence.
The actual system-service worker/broker reply, live receipt writer-to-reader
roundtrip, cross-run refusal, Go admission/checkpoint binding, dirty coder
checkpoint, candidate retrieval/publication and crash/reboot reconciliation are
unverified.
The current CLI invocation does not create a commit for dirty work. Close this
part only with an admitted real coder run that produces an exact retained
candidate, denied altered/stale bundle replies, and successful recovery after
worker or owner interruption. Do not treat the direct bundle fixture as D3.

For D3 restart reconciliation, `Children::start` in
`lib/desktop-runtime/src/supervisor.rs` calls
`Broker::reconcile_profile` before starting the Go backend. That broker client
retires unlaunched staging but refuses a launched run; it cannot compare the
new native receipt to the Go ledger because the ledger is not yet available.
The new read-only receipt API does not change this startup order. A crash with
launched root state therefore remains a startup blocker. Keep launch disabled
until a trusted Go-record read, exact receipt validation, root/process and
credential retirement, and atomic outcome reconciliation are composed and
proved across an interrupted owner and a full restart. The source inspection
establishes this ordering; no crash/restart run has qualified it.

If a later slice reveals another authentication, IPC admission, credential or
isolation requirement, add a row with its exact dependency, files, positive and
negative evidence. Route only hacking-like work to Daybreak. Do not mark a
canonical slice Complete while its required qualification is open.
