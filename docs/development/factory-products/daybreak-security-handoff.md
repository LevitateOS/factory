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
could not be re-inspected here. The user confirmed on 2026-11-05 that those
artifacts are no longer available. Their exact failure cannot be reconstructed
from the remaining description. Capture fresh pre-consent diagnostics and
runtime/source identity before another connected challenge; do not claim the
historical crash diagnosed or resolved from an offline pass.
A prior worktree checkpoint passed normal and opt-in native desktop source
checks after the generated UI was built; those checks did not diagnose the
crash or qualify connected authentication and IPC.
On 2026-11-05 an unconfigured repeat `cargo check` for the native desktop
stopped at missing pkg-config entries for GDK Pixbuf, Pango, libsoup 3 and
JavaScriptCoreGTK 4.1. The retained
`/home/vince/.cache/soda-desktop/sysroot` does contain those entries. With
`PKG_CONFIG_SYSROOT_DIR` and `PKG_CONFIG_LIBDIR` set to that prefix, locked
offline `native-ui` checking passed, and the opt-in `native-check` desktop,
current Go backend and retained PostgreSQL payload built into a scratch stage.
The first headless Cage trial failed before desktop launch because the sandbox
presented `/tmp/.X11-unix` with an owner Cage rejected and did not permit its
Wayland socket bind. A disposable user namespace with the retained matching
runtime loader and WebKit helpers then ran the opt-in offline Tauri/WebKit
`--check` journey to exit zero: private PostgreSQL/Go readiness, shared UI,
bounded bridge, multiple windows, second-launch activation and Quit. The check
used a scratch-patched ELF interpreter so second launch addressed the app, not
the loader; it was neither the normal installed executable nor a connected
account session. The current system coredump index and user journal did not
contain the historical WebKit crash artifact. The actual connected journey and
crash diagnosis remain open.
A fresh opt-in `--check-preconsent` run on 2026-11-05 reached the real
headless Tauri/WebKit shared UI and native PostgreSQL/Go runtime, kept two
windows and backend health available for 20 seconds, then exited zero through
Quit without initiating GitHub OAuth. Its log is retained outside tracked
source at `/home/vince/tmp/factory-evidence-20261105/preconsent-20261105.log` (SHA-256
`86f290d6d1747962baeca7c6033f8bc491ac41542a64d3bb54064f74dff705d4`).
The source base was `c3ce1ab0c6d453d4321b89cc3793df66d5e06491` plus
the opt-in pre-consent check change; the scratch desktop ELF was SHA-256
`a17a11606896751d252e1c996eb9d7b17f39750f888c1023807f1739e4f35f0c`.
The retained WebKit 4.1 library was SHA-256
`3b303f80773d04c3ca3478eb297675e45c432e72682513ab8f29e0bcff6f4737`.
This controlled pass permits a separately observed current challenge attempt;
it neither reproduces nor explains the historical crash and proves no
consent, authenticated read or connected product IPC. Retain logs and runtime
identity for that next attempt; a missing historical core cannot be recovered.
Four disposable connected attempts followed. The first opt-in observer
incorrectly waited for a declared-browser marker after opening the real
GitHub device page, and stopped before publishing its code. The second
published a code and reported only a generic sign-in failure after the user
approved it. The fourth retained a safe reason: the user approved the new code,
the native client reached account storage, and the isolated keyring refused
the save; the run exited 1. Its log is
`/home/vince/tmp/factory-evidence-20261105/consent-20261105-d.log` (SHA-256
`f8b4965a9221b3f5825924154cae0a66768e681d4f91d72e87fb73209b22f111`).
The intermediate run ended without a result or exit marker; its last log lines
are Xwayland window errors, and neither the host coredump index nor user
journal held a fresh crash record. Do not infer its failure cause or a saved
account. The approved run's isolated data root had no default `login.keyring`.
A separate fresh, dummy-only Secret Service probe created a default keyring and
stored a dummy item after explicit `gnome-keyring-daemon --unlock`; it proves
that initialization works in this host, not that the OAuth grant saves. Before
another real challenge, initialize and verify a fresh private keyring for the
headless fixture, then retain the actual native save/reload and connected
read/IPC result. The user is unavailable for another consent step for the
next four to six hours. No connected gate is closed.
The opt-in consent observer now checks for an unlocked default collection
before starting a new challenge. Locked offline native-check compilation
passed. A fresh uninitialized private-keyring Tauri/WebKit run reached the
shared UI and native backend, reported the expected refusal, exited 1 and
created no user-code file. Its external log is
`/home/vince/tmp/factory-evidence-20261105/consent-preflight-20261105.log`
(SHA-256 `2a0a0b9232178859c32f075a7b3aef3d6c52af42161965908dc6d96cb35641b7`).
This proves only the negative preflight; it does not prove a write or restore
the absent collection. Initialize and verify the disposable keyring before
another approval request, then retain the actual grant save/reload and
connected result.
A subsequent disposable PostgreSQL/Go/Rust supervisor check exercised a
synthetic bound MCP initialization through the real Rust secret verifier.
Exact current-work preparation and run observation returned 200; missing
preparation, missing observation and empty observation returned 403. The Go
bound catalog now also requires the exact observation on each tool call.
This is a transport/component check, not a canonical capability transaction,
production native listener, worker delivery or D3 qualification.
A later disposable service check exercised the new version-2 native execution
protocol. A credential-free three-second reviewer process emitted its exact
start observation before exit; the broker and Rust client matched that identity
to its terminal result. Observer refusal stopped a second run. Exact retirement
left both agent/relay units inactive with `MainPID=0` and removed source and
preparation paths. The result and disposable driver are retained under
`/home/vince/tmp/factory-native-start-20261105/`. `RunOwner::execute_observed`
now exposes this producer event, but it is not a current-liveness observer or
Go admission. The native consumer must still prove those facts, MCP activation
and delivery, custody and transactional crash/reboot recovery on the admitted
path before D3 closes.
A new headless attempt-handle check exposed a destructor defect: an uncertain
cancelled `RunOwner` stopped local use, but its nested credential `Owner` then
retired the root staging on drop. The source now detaches that automatic root
retirement on the uncertain path. A fresh real-service/private-dummy-keyring
check passed finite completion and verified that explicit cancellation and
handle drop both joined the owner, stopped agent/relay services, created no
receipt and preserved the exact launched root before explicit fixture disposal.
Failed and successful evidence are retained respectively under
`/home/vince/tmp/factory-native-handle-20261105/` and
`/home/vince/tmp/factory-native-handle-fixed-20261105/`. Relevant files are
`lib/desktop-runtime/src/factory_provider/{attempt,run,owner}.rs`. The handle
still needs supervisor ownership and Go consumer composition; its `Executing`
state is not a fresh process-liveness observation. No actual owner crash,
reboot, provider task or atomic ledger settlement was proved. Those remain
required to close D3 and the N4 recovery gate.
For the missing MCP delivery path, a current official-stable Muse
`1.4.14-R7309.1` echo-only check completed a local synthetic streamable-HTTP
MCP handshake and tool discovery with exact fixture headers. Literal CLI
header values refused; whole environment references were accepted. Evidence
is retained under `/home/vince/tmp/factory-native-mcp-config-20261105/`.
At that checkpoint the worker still lacked its exact-run transport; the
following producer now supplies it. The production Go listener/current-run
observer, signing-key join and actual model/tool admission remain missing.
The worker's fixed required MCP route now authenticates with its run placeholder
through the existing isolated relay. Rust forwards only through a trusted
exact-run `WorkerMcp` port; the worker receives no Go MCP secret or endpoint
selector. A real-service/private-dummy-keyring check crossed the Rust owner
and Go's bound MCP handler for initialization and tool discovery with a
synthetic capability; missing binding returned 503 without a callback.
Separate pending calls were dropped on credential withdrawal and owner
cancellation, with uncertain launch evidence preserved. Exact services and
fixture daemons were stopped. Evidence is retained under
`/home/vince/tmp/factory-native-mcp-owner-20261105/` and
`/home/vince/tmp/factory-native-mcp-lifetime-20261105/`. The isolated executable
was a tiny fixture, not Muse. A later local scripted provider/MCP check with
the actual CLI established request namespace `mcp__factory`, canonical-name
aliases and qualified response dispatch. The unqualified fixture returned an
unknown-tool result despite exit zero; the qualified fixture under the worker's
existing headless approval mode made a local `tools/call` and returned its real
fixture result. The native model policy now captures the defining Go catalog
from the trusted port, validates namespace/name pairs and stable SSE item plus
execution call IDs, and refuses missing/changed catalogs or unoffered calls.
Its component check accepted the actual CLI requests, Go's 24-tool role catalog
and the executed scripted replies. Evidence is retained under
`/home/vince/tmp/factory-native-mcp-model-20261105/` and
`/home/vince/tmp/factory-native-mcp-scripted-v4-20261105/`. This used retained
nonsecret catalog metadata and no external provider inference. Canonical SQL
capability admission, current backend/instance binding, Rust signer composition
and the complete native owner/model task remain unverified and required before
D3 closes.
A production-bundle Chromium check with a local Tauri command fixture now proves
that a failed `credential_status` read removes stale saved-account UI rows and
repository options. It uses no real keyring, provider, consent or authenticated
IPC, so the connected gate remains open.

## Gate register

| Gate and dependent slices | Exact blocker and dependency | Relevant files | Proven so far | Still unverified; evidence needed to close |
| --- | --- | --- | --- | --- |
| Worker MCP transport and Factory tool policy within D3/N4 | The private native producer and namespaced model policy exist, but the production Go native listener/current-run observer and its exact backend/instance/capability binding are missing. Go must supply the captured canonical tool catalog from the same server definition before launch. N4 also depends on T06-CONNECT and T12-PLACEMENT. | `lib/desktop-runtime/src/factory_provider/{mcp,owner,run,response}.rs`, `lib/desktop-runtime/src/factory_provider.rs`, `cmd/soda-factory-worker/src/bin/{soda-factory-agent,soda-factory-provider-relay}.rs`, `pkg/factory/{runtime/native_server,mcpserver/bound}.go` | Current local Muse echo completed MCP startup/discovery; a tiny isolated ELF separately verified fixed configuration and run-placeholder delivery through the real relay/native owner/Go bound handler with a synthetic active capability. Missing binding made no callback. Credential withdrawal and owner cancellation each dropped a pending native MCP future; uncertain cancellation retained root evidence. A local scripted provider then made the actual CLI execute a qualified Factory call and return its fixture result. The native component check accepted those captured requests/replies and Go's 24-tool catalog, and refused catalog, namespace and execution-call identity changes. | Compose the trusted catalog/port with the current Go backend and Rust signer under the admitted canonical capability. Prove actual provider Factory tool handling through the isolated native owner, current Go work/run/role checks, expiry/revocation, exact backend/instance binding and no root/IPC/provider secret in the worker. Then qualify a real finite task, transaction settlement and crash/reboot recovery. The checks used synthetic capabilities and locally scripted replies, with no canonical Store row or real inference, and do not close this gate. |
| Desktop authentication and IPC admission: T06-CONNECT, then T06-GITHUB-EVIDENCE, T06-GITHUB, T07-READ and T11-WRITES | The connected desktop journey has not completed. T06-CONNECT consumes the completed T06-CONNECT-BRIDGE, but real current-account selection, authenticated reads and per-window product IPC have not passed together. A pre-consent WebKit/heap crash interrupted an earlier journey; its artifacts are unavailable. Do not repeat a challenge merely to bypass the crash. | `cmd/soda-desktop/src/{main,signin,bridge,product,check_credentials}.rs`, `frontend/desktop/{main,accounts,connections,transport,product}.ts`, `lib/desktop-runtime/src/{github,credentials,supervisor}.rs`, `pkg/factory/runtime/{native_application,native_product}.go` | Source/component builds, shared selector/draft checks and bridge producer checks are recorded under T06-CONNECT. A current WebKit runtime passed an unapproved challenge/cancel journey. The retained sysroot supports locked offline native-ui checking and an opt-in native-check build. A current disposable headless Tauri/WebKit offline check exited zero for PostgreSQL/Go readiness, shared UI, bounded bridge, windows and Quit. Earlier approved headless native GitHub reads and private account retention passed in the P1 producer. | The current connected check reached credential storage but its isolated default keyring was absent; initialize and verify that collection before rerunning the connected Tauri/WebKit account, repository, issue/detail/feed, switch/reconnect, draft and retirement journey with current IPC admission. Capture exact runtime/source identity, successful and denied caller/resource observations, and a retained result. Offline check success does not prove connected consent or authenticated IPC. |
| GitHub credential custody and revocation: T06-GITHUB and the connected GitHub consumers | The cohesive Rust connector is implemented, but the final provider gate depends on T06-CONNECT and T06-GITHUB-EVIDENCE. The connected desktop has not proved remote revocation and full private/paged/current-head observations. | `lib/desktop-runtime/src/credentials/`, `lib/desktop-runtime/src/github_provider/`, `lib/desktop-runtime/src/github.rs`, `pkg/factory/runtime/native_product_observations.go`, `pkg/factory/runtime/native_product_reads.go` | The Go native token route was retired; Rust owns OAuth, refresh, HTTPS, decoding and native credential lifetime. Approved native reads, private access, refresh and retained identity are recorded in the plan. | Prove final connected consent/identity/storage, current-account and canonical binding, private/paged observations, refresh if issued, remote revocation and local removal without credential disclosure. Retire or migrate historical Go-observer check consumers only with equivalent current evidence. |
| D1 standing GitHub automation policy: T11-INTAKE, T12-GITHUB-PRS and installed GitHub automation | T11-INTAKE depends on T09-EXECUTION, T11-WRITES and T12-PLACEMENT. Before automatic intake, select and enforce the explicit standing authorization policy or retain original-author creation evidence as mandatory; discovered work must wait for maintainer adoption until then. | `pkg/factory/runtime/placement.go`, `pkg/factory/control/dispatch_launch.go`, `pkg/factory/execution/`, `lib/desktop-runtime/src/github_provider/` | The plan identifies the policy choices and keeps automatic intake inactive. Existing canonical authorization and ledger owners remain Go. | Record the selected policy, eligible actor/fork/repository, limits and accepted revision; prove authenticated polling/cursors, wake reconciliation, refusal of unauthorized or self-triggered work, and the eligible issue-to-PR journey through the admitted isolated executor. |
| D2 Fountain desktop authentication and grant revocation: T10-GATEWAY, then T10-PUBLISH/T10-INTAKE and desktop Fountain journeys | T10-GATEWAY depends on T05-CREDENTIALS, T06-FOUNTAIN, T10-WRITES and T12-PLACEMENT. Supported native enrollment/login and revocable grants have not been proved. Browser authority must not be spoofed and the raw Unix socket must not be exposed. | `lib/desktop-runtime/src/credentials.rs`, `cmd/soda-desktop/src/{main,bridge}.rs`, `pkg/factory/runtime/placement.go`, Fountain host adapters described by T10-GATEWAY | Existing OAuth PKCE code is only spike evidence; Fountain SDK/native writes and shared UI components have separate completed producer evidence. | Prove supported actual desktop enrollment, current authenticated HTTPS reads and operations, scoped IPC admission, grant withdrawal/revocation, and centrally coordinated placement using the real host. Include denial after withdrawal and retained recovery. |
| D3 isolated native worker and execution admission: T09-NATIVE-EXECUTOR and T09-EXECUTION; then T10-PUBLISH, T11-INTAKE, packaging and installed automation | The selected worker must have a distinct host identity with no access to the desktop user's files, processes, session bus or keyring. The root broker must bind the exact admitted run, role, snapshot, worktree, process, credential and retirement. T09-EXECUTION additionally requires T06-CONNECT and T12-PLACEMENT. Product execution must remain unavailable until this gate closes. | `cmd/soda-factory-broker/src/main.rs`, `lib/desktop-runtime/src/factory_provider/{broker,run}.rs`, `cmd/soda-factory-worker/`, `lib/native-process/`, `lib/desktop-runtime/src/{execution,supervisor,credentials}.rs`, `pkg/factory/execution/`, `pkg/factory/control/{dispatch_launch,mcp_key}.go`, `pkg/factory/runtime/{native_application,placement}.go` | The plan records live distinct-UID service, file/keyring denial probes, captured ELF transfer, bounded process/cgroup retirement, broker staging/preparation/relay/agent probes, exact-run attestation, owner-death teardown, and retained Go isolated-run validation. A disposable current broker/relay/agent run also proved exact setup retirement of an unlaunched stage, refusal after dummy launch, retained launch observation and explicit final retirement. The headless process primitive is outside the production desktop dependency graph. These are component probes, some using dummy credentials or a fake provider. | Complete and qualify production Go admission and semantic Rust ports to the single coordinator; role-limited tool execution and exact worktree/candidate/artifact capture; strict workspace storage bound; cross-run denial; scoped credential delivery/withdrawal and MCP signing-key custody; durable transactional receipt/outcome reconciliation across crash/reboot; production Quit; and a real finite provider run. Specifically prove the lifecycle fence across a real interrupted launch and restart with ledger outcome reconciliation; the disposable normal-path check does not cover a torn file update. On the actual launch path capture worker UID, file/process/keyring/session denials, exact mounts and role capabilities, deadline/tree cleanup, credential withdrawal and retained recovery. No same-user fallback or dummy-token result closes D3. |
| D3 reviewer/coder tool admission within T09-NATIVE-EXECUTOR | Muse advertises workflow and memory functions despite reviewer CLI flags. The trusted role must come from the Go admission and broker attestation; the production credential owner must filter requests and validate model tool events before the worker can act. This depends on the live `RunOwner` path and a real finite provider task. | `cmd/soda-factory-worker/src/bin/soda-factory-agent.rs`, `lib/desktop-runtime/src/factory_provider.rs`, `lib/desktop-runtime/src/factory_provider/{owner,response}.rs`, `pkg/factory/execution/` | The current Muse ELF sent an 88,434-byte reviewer request from an isolated service with 11 functions and 128,000 requested output tokens. Production `Policy::admit` applied by a disposable fake owner reduced that exact body to two read functions and 4,096 tokens. Earlier scripted SSE checks refused unoffered functions. | Prove the production `Owner` applies that policy to the actual admitted reviewer and coder, and that complete real model/tool responses enforce their role through execution and retirement. Capture positive allowed functions and denied disallowed events without promoting a scripted response or dummy 401 into a provider run. Route a check that looks like hacking to a user-started Daybreak session. |
| Native provider credential proxy within D3 | The observed Muse CLI can accept a key on stdin and a base URL. Supplying the reusable owner key to the worker violates the selected custody rule. The relay/proxy must authorize one exact run and withdraw use on cancellation, owner death and restart. The user authorized one dummy-token/prompt request after automatic approval review stopped an earlier attempt. This approval boundary is separate from Daybreak's hacking-like-work boundary. | `cmd/soda-factory-worker/src/bin/soda-factory-provider-relay.rs`, `cmd/soda-factory-worker/systemd/`, `lib/desktop-runtime/src/factory_provider/{run,owner,response}.rs`, `lib/desktop-runtime/src/credentials/`, `pkg/factory/control/mcp_key.go` | A private-loopback fake provider, address-policy probe and invalid-token native owner/relay run are recorded. Owner pidfd teardown and subsequent local 503 after withdrawal were observed in disposable units. Production `RunOwner` completed a credential-free coder run and durable receipt. With explicit user authorization, production `RunOwner` also completed one isolated Muse reviewer attempt using a private in-memory dummy credential. Muse reported the dummy key rejected, exited 1, and the 191-byte complete output was retained; the worker/relay retired. The CLI error is consistent with remote authentication rejection, but the exact upstream HTTP status was not captured. | Prove final authenticated endpoint and network confinement, usable owner-held credential use without exposing it to the worker, run-bound authorization, withdrawal/owner-crash/restart denial, complete model/tool response admission and an actual successful finite provider task. Then prove Go transaction settlement and recovery. A dummy-key rejection does not close D3. |
| Native MCP signing-key custody within D3/N4 | The dormant Go `LoadFactoryMCPKey` creates and returns a raw persistent root key; its separate appliance path remains in Go. The selected native allocation requires Rust to own secret derivation and lifetime before activating the Go native execution consumer. | `pkg/factory/control/{mcp_key,coordinator,dispatch,dispatch_launch}.go`, `pkg/factory/mcp_submission.go`, `pkg/factory/{httpapi/worker,mcpserver/{bound,catalog}}.go`, `pkg/factory/runtime/{native_mcp_signing,native_server}.go`, `lib/desktop-runtime/src/{mcp_signing,collaboration,supervisor}.rs`, `tools/native-checks/{native_mcp_signing_check.go,src/bin/factory-native-mcp-signing-check.rs}` | Go dispatch and worker HTTP accept a scoped derivation function while preserving the separate appliance key handler. The Rust signer creates and reopens a private profile key and derives the fixed HMAC. Its supervisor-owned host socket has a bounded fixed signing route; the native Go server binds its client to the current instance. A real disposable PostgreSQL/Go/Rust supervisor check passed direct host-socket derivation, wrong-key and wrong-instance refusal, invalid-scope refusal, ordered socket retirement and stable key reopening across a restart. The synthetic Go client also called that Rust route successfully on both incarnations, matched the Go capability HMAC and refused after retirement. The bound Go adapter now refuses missing current-work preparation and missing/empty live observation; the bound catalog requires exact observation at each tool call. A synthetic bound HTTP initialization passed exact observation and refused those missing inputs on both ordered supervisor incarnations. Existing MCP HTTP integration and locked offline Rust checks passed. The fixture read its own synthetic key for comparison; it did not use a product capability row, a native MCP listener, worker delivery or a real credential. | Wire the Go signing client through an admitted capability on the real native coordinator and listener, then prove exact-run/current-state admission, expiry/revocation refusal, key rotation/restart behavior, worker delivery and root-key absence from Go/UI/logs on that production path. The current desktop starts no native coordinator or MCP listener. The callback currently authenticates a same-UID peer with the shared IPC key and instance; demonstrate that the isolated worker cannot acquire that authority. Synthetic callback evidence does not close custody. Route adversarial checks that look like hacking to a user-started Daybreak session. |
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

The native MCP signer now commits first-use key material from a synced private
pending file with a no-replace rename and directory sync. An interrupted
pre-commit pending file can be discarded under the supervisor's exclusive
profile lock; an invalid committed key still refuses. The disposable native
fixture passed simulated interrupted creation and retained-key checks, and the
headless PostgreSQL/Go/Rust callback passed across an ordered restart. Actual
process-kill/power-loss recovery, rotation after admitted work, canonical
capability admission and isolated worker access remain unverified. This does
not close the custody row above.

A synced private identity marker now binds a committed MCP root key across
restarts. A disposable pre-marker profile kept its exact key and gained that
marker; changed and missing retained keys refused. An interrupted marker write
is reconstructed only from the already committed key. This covers local
retained-state behavior, not a real interrupted process/power-loss event or an
authorized key rotation with admitted capabilities. Those gates remain open.

The native host now also verifies a presented MCP secret inside Rust and
returns only true or false. The disposable Go/Rust supervisor check passed
matching and wrong-secret results, retirement refusal and a synthetic worker
HTTP listener's active, wrong-secret, revoked and expired responses. This
proves the callback and Go transport order with fixture capabilities. The
canonical Store row, admitted native listener, broker delivery and actual
isolated worker still need their production checks; no D3 gate is complete.
The worker HTTP boundary now refuses duplicate bearer/capability headers,
an Origin header even with an empty value, and query-bearing `/mcp` targets.
The same disposable PostgreSQL/Go/Rust supervisor check passed these denials
across two ordered backend incarnations. It did not exercise a production
native MCP listener, canonical capability row or isolated worker, so current
run/peer admission and worker delivery remain unverified.

For D3 storage, the broker has a source-level and direct filesystem check of
a 512 MiB aggregate allocated-block bound over retained preparation trees.
`cargo check --locked --offline -p soda-factory-broker` passes. The initial
source check had no live broker/preparer activation. The worker unit now gives
each agent service a separate 768 MiB tmpfs for workspace, output, Git state and its preferred temporary
directory; the worker checks mount type, device, mode and capacity and refuses
other common writable scratch mounts. A disposable dynamic-UID systemd service
confirmed the mount size, mode-`0700` private directory creation and denied
writes to `/tmp`, `/var/tmp`, `/dev/shm` and `/run/lock` under the relevant unit
properties. Its first trial showed that `PrivateTmp` overrode separately sized
tmpfs mounts on `/tmp` and `/var/tmp`; the unit now marks those paths
inaccessible. Locked offline worker compilation and rendered unit syntax
verification pass. At that checkpoint, no broker-orchestrated agent service, coder
growth, multi-run capacity or cross-run isolation check used this storage profile.
The built worker also ran in a disposable systemd unit. A first attempt omitted
`Slice=system.slice` and stopped in systemd before worker startup because the
cgroup bind source was absent. With that source-unit property and the cgroup
bind restored, the worker passed service identity, storage and process
delegation, then refused the expected missing role manifest. The probes were
collected and the temporary binary was removed. This is not a worker/broker
execution or storage exhaustion check.
An additional actual-worker reviewer attestation attempt under a temporary
socket and service failed before readiness. The worker's captured Git command
returned a repository-read failure on a dummy checkout. A disposable
`soda-native-process` diagnostic showed `EACCES` opening the dummy `.git/HEAD`
in both the native-process parent and its captured child, although stat worked
and a direct command in the matching disposable service profile could read it.
Copying the fixture from the protected worktree to `/run` did not change the
denial. The cause is unverified. The temporary socket, units, binaries and
`/run` fixture were removed. A follow-up disposable check used the actual
`soda-factory-preparer` binary on a 373-byte dummy Git bundle in the broker's
root-owned source/output layout. It exited zero and created a dynamic-UID
checkout. With that checkout bound read-only, the current agent profile
returned exact reviewer and coder attestations: worker UID 64596, root peer,
read-only source and a private workspace only for coder. This proves the
preparer-to-worker component path for those dummy inputs, not why the
hand-built fixture was denied. The broker did not orchestrate the run, and no
tool/provider, candidate result or credential path was exercised. Before N3/D3
closure, verify the broker-orchestrated production source and explain any
remaining file-access denial without bypassing an enforced boundary. Relevant code:
`cmd/soda-factory-worker/src/bin/soda-factory-agent.rs`,
`lib/native-process/src/`, `cmd/soda-factory-broker/src/main.rs`,
`cmd/soda-factory-broker/src/preparation_quota.rs` and the agent service/socket
templates. No fix is claimed here.
The worker mount does not prove that every writable surface is bounded or that
failed checkpoints remain recoverable. Close the storage part only after actual
service-path checks of retained multi-run capacity/refusal, cleanup/retry,
coder/output bounds, writable mounts, and denial when `/run` has low free
space, with exact process and filesystem observations. These component checks
do not close D3.
The agent unit also disables swap under its existing 1 GiB memory ceiling.
Two later concurrent credential-free broker clients used the same profile with
different exact run IDs. The first attempt lacked dummy owner sockets, so both
relay units failed at mount setup before readiness; both broker retirements
succeeded. With live owner sockets, both runs completed through broker,
preparer, relay and agent, returned zero child exit, retired tree, complete
31-byte output and Git-verified 419-byte candidate bundles. Their agent
service lifetimes overlapped in the systemd journal. Both reported worker UID
64596 and relay UID 64727, so the check does not prove per-run UID separation.
Both exact run paths retired, the services had `MainPID=0`, and temporary units,
binaries and owner sockets were removed. No cross-run access denial, storage
pressure/refusal, credential use, Go transaction or real provider was tested.
Before D3 closure, demonstrate run-specific filesystem/process/resource denial
on the actual service path despite any shared dynamic UID; route a check that
looks like hacking to a user-started Daybreak session. Relevant files are the
agent/relay systemd templates, `cmd/soda-factory-broker/src/main.rs` and
`cmd/soda-factory-worker/src/bin/soda-factory-agent.rs`.
An earlier disposable service attested workspace creation and separately ran a
small coder edit. A separate broker/preparer/relay/agent run drove a
17,256-byte credential-free ELF to write under the worker's bounded `TMPDIR`.
It reported progress through 704 MiB, then exited by signal 9. The systemd
journal recorded an OOM kill in the agent unit and a 773.9 MiB memory peak;
the typed client accepted a complete, retired-tree reply with no candidate.
The service ended with `MainPID=0`, and exact broker retirement removed all
run-specific source, preparation, socket and relay paths. This proves one
bounded growth and cleanup observation, not the tmpfs `ENOSPC` path, all
writable surfaces, multi-run capacity or denial with low `/run` free space.
The service reported `Result=success` even though the child was killed, so the
future Go adapter and transaction must preserve the exit signal as a failed
execution observation. Prove that consumer behavior before D3 closure.

For output bounds, a disposable full broker/preparer/relay/agent path ran a
15,240-byte credential-free ELF attempting 24 MiB of stdout writes. The typed
Rust client returned `output_limit`, signal 9, exactly 16,777,216 captured
bytes, `output_complete=false`, unchanged candidate and exact run retirement.
The agent and relay units ended with `MainPID=0`, and the agent journal showed
a 33.9 MiB memory peak. Temporary units, binaries, owner socket and run paths
were removed. This is one dummy-input cap check. The Go consumer has not
processed an incomplete native receipt; prove it cannot promote such a result
to successful completion on the actual ledger path. Relevant files include
`lib/native-process/src/process.rs`,
`cmd/soda-factory-worker/src/bin/soda-factory-agent.rs`,
`lib/desktop-runtime/src/factory_provider/{broker,receipt}.rs` and
`pkg/factory/control/settle.go`.

The Go source contract now has `RunObservation.OutputComplete`; isolated
`output_limit` maps to failure, and an isolated completed phase needs an
ordinary child exit, zero exit code and complete output before successful
settlement. The changed Go packages compiled and passed `go vet`, but no actual
Rust receipt crossed the
Go port or product transaction. Prove that mapping and its retained outcome
with a real admitted run; the service probe and source check do not close D3.

The Go coordinator now has a source-level settlement guard for that observation:
`pkg/factory/control/settle.go` maps a retained native child signal or nonzero
exit code to a failed outcome and failure summary even if the adapter reports
`completed`. An isolated `completed` run without an observed zero child exit
stays unsettled. The control package compiled but has no test files. No actual
Go/Rust port or retained Go transaction consumed the signal-9 service receipt;
prove that exact result path and its durable failure outcome before closing D3.

For D3 result custody, the worker now has a direct Git check of clean committed
candidate bundling and bounded dirty-edit checkpointing. After the agent tree
retires, the worker checks the captured repository identity, bounds the dirty
inventory to 512 paths, 4 KiB of status and 32 MiB of changed-file bytes,
then stages and commits an internal checkpoint with an old-HEAD compare-and-swap.
It refuses a completed reply if capture fails or the tree remains dirty. The
broker and desktop
source bind and hash up to 4 MiB of bundle bytes; the desktop fsyncs them into a
private receipt before retiring root staging. A v2 writer header collision was
corrected: `source_sha256` now records the admitted source while `bundle_sha256`
records the returned candidate bundle. A read-only native reader checks the
trusted run/profile/source/base candidate/role/tool/prompt identity, private
profile and receipt file, size and content hashes. A pending file without a
committed receipt reports uncertainty; a committed file does not prove root
retirement. The reader passed a local on-disk fixture with valid readback,
wrong source, altered output digest, interrupted pending write and older-version
refusal. The direct Git fixture also passed dirty checkpoint, detached HEAD,
deletion/replacement, rename, changed-configuration refusal, a 33 MiB dirty
refusal and an oversized bundle refusal. A follow-up disposable actual agent
service ran a 16,848-byte credential-free coder ELF against real preparer output.
It returned worker UID 64596, root peer, zero exit, retired tree, complete
24-byte output and a 437-byte candidate bundle. Git verified the returned
commit descends from the prepared source and contains the single expected edit.
The temporary unit and exact `/run` paths were removed. This proves one
service-path dirty coder checkpoint with dummy inputs, not durable result
custody. A subsequent disposable installation used the current broker,
preparer, relay and agent units for the entire credential-free coder path.
The broker staged a 362-byte dummy bundle, attested relay UID 64727 and worker
UID 64596, forwarded a 16,840-byte ELF and prompt, and returned zero exit,
retired tree, complete output and a 419-byte candidate bundle. Git verified
the returned commit's parent and expected edit. Exact retirement removed the
run source, preparation, agent socket and relay runtime; temporary units,
binaries and owner socket were removed. This proves the broker-to-worker
result path for one dummy run. The production receipt writer and reader now
share one exact scope and header validator. A further disposable current-unit
check used the typed Rust broker client and this writer on a dummy coder run.
It synced the 31-byte output and 419-byte candidate bundle to a private
receipt; a separate process reopened the same receipt after the writer exited,
recovered the matching result and refused a wrong source scope. Git verified
the recovered candidate bundle, and exact broker retirement removed the run.
The live `RunOwner` credential-bearing receipt path, cross-run refusal, Go
admission/checkpoint binding, candidate retrieval/publication and crash/reboot
reconciliation are unverified. The
aggregate worker workspace and ignored-file storage bound is still
unqualified on the actual worker service; the changed-file preflight is not
that bound. A failed checkpoint currently yields uncertainty, but no recovered
worker workspace has been proved after the service exits. Close this
part only with an admitted real coder run that produces an exact retained
candidate, denied altered/stale bundle replies, and successful recovery after
worker or owner interruption. Do not treat the disposable fixture as D3.

The first-use native receipt directory is now created and synced by
`lib/desktop-runtime/src/factory_provider/receipt.rs`, and
`RunOwner::start` ensures it exists after checking `profile.id` and before
broker staging. The existing receipt fixture removed its empty directory,
then verified production writer creation with the current UID and mode 0700,
followed by retained readback. That first-use fixture did not run the
credential-bearing owner or prove ledger/restart reconciliation.

A later disposable check did run production `RunOwner::start` and `execute`
with a fixture Muse token held in a separate private XDG/Secret Service profile
under `/home`. It exercised finite Rust capability lookup, owner socket bind,
broker source staging, preparer/relay/worker attestation, local owner activation,
one isolated coder execution, production receipt write and exact owner/broker
retirement. The result reported worker UID 64596, zero exit, complete 31-byte
output and a 419-byte candidate bundle. A separate process reopened the receipt
after the private D-Bus session ended; Git verified the bundle's commit parent
and expected one-line edit. The owner socket and run-specific broker paths were
gone, and the worker/relay units had `MainPID=0`; temporary units, binaries and
`/run` paths were removed. The dummy keyring/profile remains private under
`/home`. The fixture executable made no provider request, so it did not prove
credential delivery or withdrawal, actual Muse behavior, current Go admission,
transactional settlement or crash/reboot recovery. Those remain required D3
evidence. Relevant files are `lib/desktop-runtime/src/factory_provider/{run,owner,receipt}.rs`,
`lib/desktop-runtime/src/credentials.rs`, `cmd/soda-factory-broker/src/main.rs`,
the agent/relay worker units and `pkg/factory/control/settle.go`.

On 2026-11-05 a follow-up local fixture for the same production `RunOwner`
and current Muse ELF compiled with the retained locked offline dependencies.
The intended run used only a private dummy token and a one-sentence prompt
about the fixture README. Automatic approval review rejected its launch because
the owner would send the token and local prompt/context to Meta without
explicit authorization for that data egress. No Muse owner run or outbound
request occurred. The disposable broker socket and units installed in
preparation were stopped and removed, and the remaining empty `/run` directory
was removed. This leaves production request filtering, credential delivery,
withdrawal and provider response handling unverified at that checkpoint. The
subsequent explicit authorization and bounded retry are recorded below.

The user subsequently authorized that exact dummy-token/prompt outbound check.
The first attempts stopped locally: one retained dummy keyring had invalid
metadata and a fresh keyring lacked a default collection. No worker or network
request started in those attempts. The successful attempt used a separate
private D-Bus session whose in-memory Secret Service collection was set as the
default. Production `RunOwner::start` activated the owner and isolated reviewer;
`execute` ran the current 396 MiB Muse 1.4.14 ELF against the fixture README
prompt. The worker UID was 64596. Muse exited normally with code 1 and a
complete 191-byte output saying the dummy API key was rejected. The production
receipt was retained (1,139 bytes), with reviewer candidate state
`not_applicable`; no dummy token string appeared in that receipt. The worker
and relay units were inactive with `MainPID=0`, the run-specific broker paths
were absent, and temporary units, binaries and empty `/run` paths were removed.
The result is consistent with a rejected provider request, but no independent
upstream HTTP status/body was captured. It does not prove a usable credential,
complete response/tool policy, cancellation withdrawal, Go ledger settlement,
crash/reboot recovery or a successful finite model task. Those remain required
D3 evidence. The user authorization covered this one dummy request only; do
not infer permission for a different credential or prompt.

A separate process, started after that private D-Bus session ended and without
the broker or worker units installed, reopened the production receipt through
`read_retained`. It recomputed the source bundle, current Muse executable and
prompt digests, matched the exact run/profile/candidate/reviewer scope, and
recovered worker UID 64596, ordinary exit code 1, complete 191-byte output
and no candidate. This proves readback of the failed dummy attempt across a
credential-session boundary. It does not prove a Go ledger record, atomic
settlement, broker recovery after a crash or a successful provider response.

A subsequent disposable credential-free live agent slept until the desktop
client cancelled its broker call. The client returned no result;
`Broker::quiesce_run` preserved the launched selector, and explicit retirement
then removed it. The agent logged a broken pipe as it stopped and had
`MainPID=0`; the relay was inactive with `MainPID=0`. This checks one ordinary
component cancellation path. It does not prove production owner credential
withdrawal, a retained Go outcome or cancellation of a real provider request.

For D3 restart reconciliation, `Children::start` in
`lib/desktop-runtime/src/supervisor.rs` calls
`Broker::reconcile_profile` before starting the Go backend. That broker client
retires unlaunched staging but refuses a launched run; it cannot compare the
new native receipt to the Go ledger because the ledger is not yet available.
Its unlaunched cleanup now uses the broker's atomic launch-fenced request,
rather than trusting a prior inspection before unrestricted retirement. A
disposable broker/relay/agent fixture retired exactly one unlaunched startup
stage after the earlier launched dummy run had been explicitly retired. The
broker now distinguishes an exact `.retiring` selector in a versioned inspection
reply and exposes its original launch state conservatively to general callers.
A separate disposable fixture staged one unlaunched run, simulated the broker's
post-rename partial state, observed that unlaunched cleanup refused it, and
recovered it through an exact `finish_retiring` request; no selector remained.
This proves handling of one simulated partial state, not an actual crash,
reboot, launched-run receipt or Go ledger reconciliation.
The Rust provider now also has `retire_recovered_receipt`, which validates an
exact complete receipt before retiring its broker root. A disposable isolated
coder left a durable receipt and launched root across a fixture process exit;
a separate process retired that root from the receipt and reopened the same
receipt. A cancelled launched run with no receipt was not retired by this API.
The broker now requires launched state for this receipt recovery request: a
stale complete receipt was refused against a newly staged unlaunched run with
the same selector, while a fresh separate-process launched receipt retired its
exact root.
Those checks had no Go ledger, actual process kill, reboot or real provider
credential. The helper is not composed into desktop startup, so the startup
order above still blocks a launched root. Keep launch disabled
until a trusted Go-record read, exact receipt validation, root/process and
credential retirement, and atomic outcome reconciliation are composed and
proved across an interrupted owner and a full restart. The source inspection
establishes this ordering; no crash/restart run has qualified it.

If a later slice reveals another authentication, IPC admission, credential or
isolation requirement, add a row with its exact dependency, files, positive and
negative evidence. Route only hacking-like work to Daybreak. Do not mark a
canonical slice Complete while its required qualification is open.
