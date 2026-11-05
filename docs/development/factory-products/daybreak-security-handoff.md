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
A production-bundle Chromium check with a local Tauri command fixture now proves
that a failed `credential_status` read removes stale saved-account UI rows and
repository options. It uses no real keyring, provider, consent or authenticated
IPC, so the connected gate remains open.

## Gate register

| Gate and dependent slices | Exact blocker and dependency | Relevant files | Proven so far | Still unverified; evidence needed to close |
| --- | --- | --- | --- | --- |
| Desktop authentication and IPC admission: T06-CONNECT, then T06-GITHUB-EVIDENCE, T06-GITHUB, T07-READ and T11-WRITES | The connected desktop journey has not completed. T06-CONNECT consumes the completed T06-CONNECT-BRIDGE, but real current-account selection, authenticated reads and per-window product IPC have not passed together. A retained pre-consent WebKit/heap crash interrupted an earlier journey. Do not repeat a challenge merely to bypass the crash. | `cmd/soda-desktop/src/{main,signin,bridge,product}.rs`, `frontend/desktop/{main,accounts,connections,transport,product}.ts`, `lib/desktop-runtime/src/{github,credentials,supervisor}.rs`, `pkg/factory/runtime/{native_application,native_product}.go` | Source/component builds, shared selector/draft checks and bridge producer checks are recorded under T06-CONNECT. A current WebKit runtime passed an unapproved challenge/cancel journey. The retained sysroot supports locked offline native-ui checking and an opt-in native-check build. A current disposable headless Tauri/WebKit offline check exited zero for PostgreSQL/Go readiness, shared UI, bounded bridge, windows and Quit. Earlier approved headless native GitHub reads and private account retention passed in the P1 producer. | Diagnose the earlier crash without treating its detection stack as root cause; then run the actual connected Tauri/WebKit account, repository, issue/detail/feed, switch/reconnect, draft and retirement journey with current IPC admission. Capture exact runtime/source identity, successful and denied caller/resource observations, and a retained result. Offline check success does not prove connected consent or authenticated IPC. |
| GitHub credential custody and revocation: T06-GITHUB and the connected GitHub consumers | The cohesive Rust connector is implemented, but the final provider gate depends on T06-CONNECT and T06-GITHUB-EVIDENCE. The connected desktop has not proved remote revocation and full private/paged/current-head observations. | `lib/desktop-runtime/src/credentials/`, `lib/desktop-runtime/src/github_provider/`, `lib/desktop-runtime/src/github.rs`, `pkg/factory/runtime/native_product_observations.go`, `pkg/factory/runtime/native_product_reads.go` | The Go native token route was retired; Rust owns OAuth, refresh, HTTPS, decoding and native credential lifetime. Approved native reads, private access, refresh and retained identity are recorded in the plan. | Prove final connected consent/identity/storage, current-account and canonical binding, private/paged observations, refresh if issued, remote revocation and local removal without credential disclosure. Retire or migrate historical Go-observer check consumers only with equivalent current evidence. |
| D1 standing GitHub automation policy: T11-INTAKE, T12-GITHUB-PRS and installed GitHub automation | T11-INTAKE depends on T09-EXECUTION, T11-WRITES and T12-PLACEMENT. Before automatic intake, select and enforce the explicit standing authorization policy or retain original-author creation evidence as mandatory; discovered work must wait for maintainer adoption until then. | `pkg/factory/runtime/placement.go`, `pkg/factory/control/dispatch_launch.go`, `pkg/factory/execution/`, `lib/desktop-runtime/src/github_provider/` | The plan identifies the policy choices and keeps automatic intake inactive. Existing canonical authorization and ledger owners remain Go. | Record the selected policy, eligible actor/fork/repository, limits and accepted revision; prove authenticated polling/cursors, wake reconciliation, refusal of unauthorized or self-triggered work, and the eligible issue-to-PR journey through the admitted isolated executor. |
| D2 Fountain desktop authentication and grant revocation: T10-GATEWAY, then T10-PUBLISH/T10-INTAKE and desktop Fountain journeys | T10-GATEWAY depends on T05-CREDENTIALS, T06-FOUNTAIN, T10-WRITES and T12-PLACEMENT. Supported native enrollment/login and revocable grants have not been proved. Browser authority must not be spoofed and the raw Unix socket must not be exposed. | `lib/desktop-runtime/src/credentials.rs`, `cmd/soda-desktop/src/{main,bridge}.rs`, `pkg/factory/runtime/placement.go`, Fountain host adapters described by T10-GATEWAY | Existing OAuth PKCE code is only spike evidence; Fountain SDK/native writes and shared UI components have separate completed producer evidence. | Prove supported actual desktop enrollment, current authenticated HTTPS reads and operations, scoped IPC admission, grant withdrawal/revocation, and centrally coordinated placement using the real host. Include denial after withdrawal and retained recovery. |
| D3 isolated native worker and execution admission: T09-NATIVE-EXECUTOR and T09-EXECUTION; then T10-PUBLISH, T11-INTAKE, packaging and installed automation | The selected worker must have a distinct host identity with no access to the desktop user's files, processes, session bus or keyring. The root broker must bind the exact admitted run, role, snapshot, worktree, process, credential and retirement. T09-EXECUTION additionally requires T06-CONNECT and T12-PLACEMENT. Product execution must remain unavailable until this gate closes. | `cmd/soda-factory-broker/`, `cmd/soda-factory-worker/`, `lib/native-process/`, `lib/desktop-runtime/src/{execution,supervisor,credentials}.rs`, `pkg/factory/execution/`, `pkg/factory/control/{dispatch_launch,mcp_key}.go`, `pkg/factory/runtime/{native_application,placement}.go` | The plan records live distinct-UID service, file/keyring denial probes, captured ELF transfer, bounded process/cgroup retirement, broker staging/preparation/relay/agent probes, exact-run attestation, owner-death teardown, and retained Go isolated-run validation. The headless process primitive is outside the production desktop dependency graph. These are component probes, some using dummy credentials or a fake provider. | Complete and qualify production Go admission and semantic Rust ports to the single coordinator; role-limited tool execution and exact worktree/candidate/artifact capture; strict workspace storage bound; cross-run denial; scoped credential delivery/withdrawal and MCP signing-key custody; durable transactional receipt/outcome reconciliation across crash/reboot; production Quit; and a real finite provider run. On the actual launch path capture worker UID, file/process/keyring/session denials, exact mounts and role capabilities, deadline/tree cleanup, credential withdrawal and retained recovery. No same-user fallback or dummy-token result closes D3. |
| D3 reviewer/coder tool admission within T09-NATIVE-EXECUTOR | Muse advertises workflow and memory functions despite reviewer CLI flags. The trusted role must come from the Go admission and broker attestation; the production credential owner must filter requests and validate model tool events before the worker can act. This depends on the live `RunOwner` path and a real finite provider task. | `cmd/soda-factory-worker/src/bin/soda-factory-agent.rs`, `lib/desktop-runtime/src/factory_provider.rs`, `lib/desktop-runtime/src/factory_provider/{owner,response}.rs`, `pkg/factory/execution/` | The current Muse ELF sent an 88,434-byte reviewer request from an isolated service with 11 functions and 128,000 requested output tokens. Production `Policy::admit` applied by a disposable fake owner reduced that exact body to two read functions and 4,096 tokens. Earlier scripted SSE checks refused unoffered functions. | Prove the production `Owner` applies that policy to the actual admitted reviewer and coder, and that complete real model/tool responses enforce their role through execution and retirement. Capture positive allowed functions and denied disallowed events without promoting a scripted response or dummy 401 into a provider run. Route a check that looks like hacking to a user-started Daybreak session. |
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
