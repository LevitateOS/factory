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

The latest Go producer, `pkg/factory/mcpserver/catalog.go::BoundToolNames`, shares
role and recorded task-controller selection with the bound SDK server in
`bound.go`. It supplies the canonical names Rust's
`lib/desktop-runtime/src/factory_provider/mcp.rs::WorkerMcp` requires before
launch, without preparing/activating a capability or asserting process liveness.
The existing disposable PostgreSQL/Go/Rust native MCP check compared all eight
role catalogs against authenticated SDK discovery across supervisor restart;
absent task ports/sources, missing runs and unplaced native metadata omitted task
tools. The projection did not call preparation or observation. Evidence is in
`/home/vince/tmp/factory-native-mcp-catalog-20261105/`; a first overlong Unix socket
path failure is retained separately. The final result SHA-256 is
`fc28e27d413f7c65ed388dc14ccaa1f4fccc12c69cfcbcb03f8ab27b75c693a7`.
This is synthetic catalog transport evidence
using retained inputs. It does not prove supported native task discovery,
canonical SQL admission or worker delivery. The exact remaining dependency is
the native launch consumer joining this Go projection to `WorkerMcp`, current
canonical work/process admission and the Rust signer before enabling launch.
Close that dependency only with the actual finite isolated Muse/Go path, matching
catalog/tool call, outcome settlement, withdrawal and restart evidence. D3 remains
open; normal composition/check work does not require Daybreak.

The current-process producer now exists in version 3 of the execution protocol.
`lib/native-process/src/{lib,process}.rs` captures a pidfd before launch readiness;
`Process::is_live` queries that exact kernel identity. The worker's
`agent_observation.rs` answers bounded sequential queries on the existing broker
connection, and requires the direct child non-live before its terminal result.
The broker's matching module validates the pending sequence before forwarding a
reply. The admission socket stays consumed. `factory_provider/observation.rs`
binds the native observer to the validated start identity; `RunOwner::observer`
and `RunExecution::inspect` expose it with a five-second maximum wait. A timed-out
query does not cancel execution or let its late reply satisfy a later query.
Confirmed terminal process exit reports non-live; uncertain loss refuses. Cached
`Process::observe` and `RunExecution::observe` remain separate facts.

A disposable real-unit/private-keyring check passed fresh live queries with the
exact distinct-UID process, timeout/requery, finite completion with the worker's
kernel non-live check, explicit cancellation and handle drop. Uncertain cases
kept launched root evidence, had no invented receipt and refused further process
observation. Agent/relay services had `MainPID=0`; explicit fixture disposal
removed the remaining roots and temporary units. Final evidence and the driver
are under `/home/vince/tmp/factory-native-observe-v2-20261105/`; result SHA-256 is
`69103512f66986cc56746633978b6b7a99cb2630b2abf55472470955579fe45a`.
This is a tiny local ELF with dummy-only credentials and retained build inputs.
It does not prove actual Muse liveness, canonical Go capability activation,
current work/placement joins, crash/reboot recovery or D3. The exact remaining
dependency is composition of the native registry/Go current-run adapter joining
these process facts to the captured run and custody, then complete admitted
worker/tool/settlement qualification. Current positive process facts alone grant
no product authority.

Idle-worker withdrawal now has a native lifetime implementation and component
evidence. `credentials.rs::while_current` uses the same revision guard as
`with_token_stream`, supplies no token to its callback, and keeps initialization
separate from periodic rechecks. The whole `owner.rs` listener runs inside that
guard; loss of credential validity or autonomous owner exit requests the exact
run's existing cancellation handle. `run.rs` then joins/quiesces the worker and
retains uncertain root evidence. Initialization, rechecks and callback progress
remain cancellable. Three initial readiness failures are retained externally;
the new guard had allowed a revision recheck to wait on a credential gate held by
its suspended initialization. Restructuring initialization and polling callback
progress during later checks removed that self-blocking path. Safe readiness
errors now retain the native reason without credential contents.

The existing real-unit/private-keyring handle check ran an idle 15-second local
ELF with no provider/MCP traffic. Local withdrawal, replacement and owner
retirement cancelled/joined it in 180, 170 and 166 ms respectively; a separate
native process replacing its disposable credential was detected in 1,452 ms.
Each uncertain case refused further process observation, retained its launched
root with no receipt and had stopped agent/relay services before explicit fixture
disposal. The unchanged finite-success, explicit-cancel and handle-drop cases
also passed. Evidence is under
`/home/vince/tmp/factory-native-idle-custody-v4-20261105/`; result SHA-256 is
`ca116ad028d39138711a9b286281c843b009720063b54074235ca03819016ea9`.
The existing credential-operation check also passed actual keyring withdrawal,
replacement, cancellation/deadlines and streaming beyond its initial 30-second
lease (`/home/vince/tmp/fcs-1105/result.log`, SHA-256
`77ea5b0e16e31e355420f4db153c3c02273be103b534c4c15a5b2427a5acf898`).
The existing pending-MCP check passed withdrawal/cancellation dropping the native
future without a late tool result, now requiring an uncertain attempt and exact
root disposal (`/home/vince/tmp/factory-native-mcp-life-v2-20261105/`, result SHA-256
`6c324f6eb5a712691e555540e378e742952e257e0b7d07f4fecb3095db1f6a5e`).

These checks used retained inputs and private dummy credentials. They do not
prove actual Muse/Meta cancellation, an installed connected account, canonical
Go custody/admission facts, interrupted owner/revision updates, crash/reboot or
ledger settlement. The guard retains the existing conservative profile-wide
local-change notification behavior; multi-account product behavior was not
qualified here. The remaining dependency is the native registry's production
composition and the current Go observer, followed by complete finite-provider,
withdrawal, Quit and restart reconciliation evidence. D3 remains open.

Native custody now has a separate producer in
`lib/desktop-runtime/src/factory_provider/custody.rs`, joined by `run.rs`,
`attempt.rs`, `owner.rs` and the exact scope fingerprint in `receipt.rs`.
An immutable private acquisition record is written before broker staging. Its
random ID, provider connection and generation 1 belong to this one-shot native
acquisition; they are not a credential revision, process incarnation or Go
placement/capability generation. A retained directory or older receipt/pending
receipt fences reacquisition. The live owner can report Held, independently of
process liveness and current-account/product admission. A cold read without
release is Uncertain. Local thread join, exact socket removal and placeholder
zeroization precede the durable seal; broker quiescence/retirement acknowledgement
precedes release. Strict scope/marker checks and file/directory sync apply to
readers as well as writers. Release can be proved while an outcome stays
uncertain with no result receipt; it does not settle Go work or erase the user's
global login. Missing or partial evidence is never proof of no acquisition.

The existing disposable real-unit/private-keyring handle mode passed all seven
normal/cancel/drop/credential-change cases with unique stable acquisitions.
Separate native readers observed active journals as uncertain without activating
them, then exact sealed/released identity after cleanup. An acquisition-only
record blocked reuse even without a result receipt; a copied pre-journal receipt
also blocked reuse and stayed byte-for-byte unchanged. Changed connection and
prompt scope refused. Uncertain worker outcomes retained their launched roots
and no result receipt while separately confirming custody release. Agent/relay
services had `MainPID=0`; exact fixture disposal removed roots and temporary
units. Readback also passed in a new native process after the private credential
session ended. Evidence is under
`/home/vince/tmp/factory-native-custody-v3-20261105/`; result SHA-256 is
`2d84c79a430d53c52ab2c12c904bbe805b19dd328b761d85b3eff8c135044177`, and
post-session read SHA-256 is
`6b397387fe929dd8e1546c397ff379903178d71abd1775668172ad58eeea1c6c`.

The exact remaining dependency is composition of the native registry and the
Go `execution.CustodyObserver` / current-run adapter, comparing these facts to the
captured canonical run/placement/acquisition before any capability activation or
settlement. Relevant Go files are `pkg/factory/execution/{provenance,observation,
native_record}.go`, `pkg/factory/service/worker_admission.go` and
`pkg/factory/control/settle.go`. Still unverified: interrupted acquisition,
seal/release writes and lost acknowledgements; recovery when the old native
owner is interrupted; root/process/credential retirement across a full restart
and reboot; actual finite Muse/provider work; production Quit and atomic Go
outcome reconciliation. The normal readback checks did not inject a crash or
power loss. Historical missing custody must stay unknown; do not synthesize an
acquisition or authorize replay from an old receipt. These are ordinary
implementation/qualification tasks; route hacking-like checks to user-started
Daybreak. D3 remains open.

The native lifecycle registry producer now exists in
`factory_provider/executions.rs`, using the same `RunExecution` owner and new
read-only `RunView` in `attempt.rs`. `ExecutionBinding` captures a native
profile/instance, exact receipt-scope digest and provider connection. Exact
replay does not invoke its factory or extend a deadline; changed scope/lifetime
refuses. Capacity counts all retained entries, including unacknowledged results
and failed/unconfirmed preparation. Only a complete result with matching
acquisition and sealed/released custody can acknowledge its memory slot.
Evidence is never deleted by acknowledgement. Unobserved factory errors remain
uncertain, retain capacity and cannot fabricate process/custody or rerun work.
Tickets check lifetime before/after kernel I/O without holding the registry;
retirement fences launches/queries, cancels all owned handles before joining
any, and reports independent run and custody results. These facts do not grant
product admission or establish a canonical Go/native identity mapping.

The existing disposable native fixture passed request-drop ownership, exact
process replay, changed/stale scope refusal, retained finite output, capacity
including completed entries, wrong/correct acquisition acknowledgement, two-run
joined cancellation and registry drop. A query remained pending against a
paused disposable agent; retirement still joined all workers and released no
retired observation. Cancelled outcomes stayed uncertain despite proven custody
release, and could not acknowledge away their slots. A declared factory error
remained unobserved/uncertain without retry. Exact fixture root disposal and
temporary service removal completed. Final result/driver are under
`/home/vince/tmp/factory-native-registry-v2-20261105/`; result SHA-256 is
`447524942d2c870e6c4aca51e7ef8ea1c50e8243e04a088763475109ee4a7d48`.
This used local ELFs, dummy keyring credentials, retained inputs and declared
profile/instance metadata; no actual Muse/Go admission or installed Quit ran.

Preparation is now lifecycle-owned in `executions.rs`: `ensure` returns a
pending ticket while retaining its preparation thread. The factory receives the
same cancellation signal checked before model spawn and supplied to
`run.rs::RunOwner::start_cancellable` (or its MCP counterpart). Capture the
policy/deadline at admission and make any additional factory work cancellable.
`broker.rs` watches startup RPCs and checks cancellation during nonblocking
connection attempts. The root broker keeps preparation request reads open,
observes caller loss during fixed service startup and attestation reads, stops
the exact startup job and joins its systemctl child on failure. Cleanup RPCs
continue after cancellation. No source check alone proves a bounded shutdown.

The existing real-unit fixture passed again with delayed checkout preparation
and a second active worker: pending ensure returned immediately, replay did not
restage, and the active process remained queryable. Retirement joined both in
250 ms. A separate delayed agent-attestation case initially failed its five-second
retirement check: its namespace-init startup process did not handle SIGTERM and
systemd waited for its twenty-second fixture delay. That failure and service log
are retained under
`/home/vince/tmp/factory-native-registry-v4-20261105/`.
`cmd/soda-factory-worker/systemd/soda-factory-agent@.service.in` now explicitly
sets `TimeoutStopSec=2s`; the rerun joined interrupted attestation in 2,361 ms.
Neither interrupted case activated its agent executable or produced a run
receipt. Cold acquisition journals stayed unsealed/Uncertain despite exact
unlaunched root cleanup. Finite success, retained/replayed observations,
capacity/acknowledgement, pending-query cancellation and owner drop also passed.
All fixture agent/relay MainPIDs were zero, roots absent and temporary units
removed. Final driver/result are under
`/home/vince/tmp/factory-native-registry-v5-20261105/`; result SHA-256 is
`b8888352c7df4a6a39a49c21e5858acf2852494fd1940c0b822c8de18b7aebf2`.
These are retained-input, dummy-only local-ELF component checks. They do not prove
installed Quit, canonical admission, full runtime retirement, arbitrary stalled
kernel/manager I/O or a full activation backlog. Those remain unverified.

Remaining blockers are concrete. An uncertain factory has no usable execution
handle after its preparation thread is joined; recovery must compare captured
intent, root and custody evidence before claiming no effects or freeing its slot.
`supervisor.rs` does not yet retain this registry. Its startup recovery now follows
Go Store readiness, as recorded below. Compose lifecycle retirement and the Go
`RunLauncher`/`RunObserver`/`CustodyObserver` ports without activating intake
before dependencies are admitted. Compare canonical Go addresses and placement
against native profile/instance/broker selectors explicitly. Prove interrupted
staging, lost replies, supervisor/worker death, actual finite provider handling,
Quit, full restart/reboot and atomic ledger settlement. The normal registry
check and one paused-agent query do not close those gates. D3 remains open;
these ordinary implementation/check tasks do not require Daybreak unless a
particular check looks like hacking.

The Go ledger-read producer for that dependency now exists in
`pkg/store/factory_native_recovery.go::NativeFactoryRun` and
`pkg/factory/runtime/native_execution_recovery.go`, mounted by
`native_server.go`. The private GET is profile-bound by validated bootstrap and
requires the current native instance in addition to the existing peer/key
channel. It maps a broker selector to one validated canonical run, includes
settled history and rejects missing, ambiguous or damaged matches. Canonical
run/workspace IDs, admitted source, retained native record and optional captured
launch placement remain distinct. It exposes no prompt/output/summary, performs
no recovery write and grants no current launch/settlement authority.

The existing Go/Rust native MCP integration check now also exercises this
actual PostgreSQL/backend read. A private marker checked that the same canonical
mapping survived supervisor restart. Unplaced metadata stayed unplaced; settled
history remained readable; another profile did not change the selected mapping.
Missing/stale/noncanonical/browser requests, duplicate selector mappings, a
mismatched stored row ID and invalid retained prompt digest all refused. GET
left the original run unchanged. Evidence/driver are under
`/home/vince/tmp/fnr3-1105/`; result SHA-256 is
`7ceb5705fc6e32b461e75e9d2b74be9f3810d287d1bf3db87fdc155414ca6fc2`.
This used declared native records and retained local build/database inputs,
without a broker root, actual launch/placement admission, process kill, provider
credential or real model task. Captured placement projection is not yet exercised
through a production dispatch packet. A Rust reader and exact native scope/process-anchor/custody comparison now
exist as described below. Durable intent binding and post-ledger resource startup
now have component evidence below; atomic Go outcome recovery remains to implement
and qualify. Missing or conflicting reads must leave native
recovery pending. No gate closes from this producer; D3 remains open.

The read consumer now exists in
`lib/desktop-runtime/src/factory_provider/recovery.rs`, with the native-only
`supervisor.rs::NativeSupervisor::native_recovery` entry point. It uses the
bounded joined `product.rs` request owner; generic product calls still refuse
the private recovery route. Strict response decoding binds profile, current
channel instance and broker selector. It retains Go's original instance,
worktree, canonical IDs and optional placement without inventing any of them.
Local reads use only the captured profile path and exact receipt scope; any
recorded process anchor is compared, including kernel start identity, timestamp,
namespace PID, executable digest and isolated UID. Known acquisition ID,
connection, generation, seal and release facts are compared with the custody
journal. Missing evidence remains unknown, and no captured connection means no
custody observation. Retirement fences subsequent evidence reads. The reader
performs no root retirement, credential acquisition, launch or SQL outcome write.
Go retains canonical validation and terminal/outcome advancement.

The existing real supervisor/PostgreSQL/backend integration check passed the
Rust read across restart, unknown local evidence, invalid/missing selectors,
generic-route refusal and evidence fencing after retirement. Evidence is under
`/home/vince/tmp/fnr-rust1-1105/`; result SHA-256 is
`e8d8619319db05d0fee01b6edf3192496527dce54a0bf0643ac6bc650ab5a557`.
The existing real-unit/private-dummy-keyring fixture also ran a finite isolated
local ELF, seeded a declared Go record from its actual receipt/custody and read
both through the production Rust consumer. Changed process, tool, prompt,
source, candidate, role and custody evidence copies refused; each original
receipt/journal inode and bytes were restored. After dummy credential withdrawal,
normal supervisor restart preserved the original process/acquisition and
canonical mapping under a new current instance; the old snapshot refused reads.
Agent/relay MainPIDs were zero, exact roots absent and temporary units removed.
Final evidence/driver: `/home/vince/tmp/fnr-live2-1105/`; result SHA-256:
`5629cfc41bc8bc65ed1400b21ce49391efc9384fc4bbb74910326d3da0baec3e`.
An initial fixture tried changing the stored process anchor; PostgreSQL's
immutability trigger refused it. That failure/driver are retained under
`/home/vince/tmp/fnr-live1-1105/`; no trigger was disabled. The final check used
disposable local evidence replacements and restored the originals instead.

These checks used retained build inputs and declared Go records, with no
production dispatch/placement admission, provider inference, process kill or
reboot. They prove neither a fresh release nor complete D3 recovery. The
original Go instance/worktree and canonical address lacked an independent
native intent binding at that checkpoint: receipt/custody scope contains broker
selector, profile, source/base/role/tool/prompt but not that full mapping. The
separate intent producer/read comparison now exists as described below; its
production admission/recovery dependencies remain in the register. At that
checkpoint startup refused a launched root before Go; the later post-ledger
resource recovery below supersedes that ordering. Compose Go transactional
settlement, then qualify interruption, lost replies, actual provider execution,
Quit and restart/reboot before enabling intake. D3 remains open.

The durable intent producer is now defined by
`pkg/factory/execution/native_intent.go`. `EncodeNativeIntent` copies only
nonsecret scope from the validated semantic launch: canonical run/workspace,
role, source, connection and deadline plus captured native workspace, tool,
runtime/placement and broker selectors. `NativeRecord.Intent` stores the exact
JSON string rather than a JSONB object whose key order could change. Go validates
it against the retained run/native record and any captured admission; Store's
advance check and `factory_execution_schema.go` forbid replacing those bytes.
Shape validation alone is not current authorization. An unplaced retained record
with an intent remains unplaced; the intent does not create an admission.

`factory_provider/intent.rs` retains exact bytes before custody/root staging via
`RunOwner::start_with_intent`. The native scope and credential connection must
match policy, and the policy deadline cannot exceed the captured deadline.
Publication is pending-file/write/fsync/exclusive-link/directory-sync; pending
files remain uncertain. Existing receipts/acquisitions refuse adoption, and a
legacy start cannot sidestep a retained intent-only interrupted start.
`ExecutionBinding::from_intent` binds the raw-byte digest to registry replay and
factory matching. `recovery.rs` compares local and Go bytes plus the captured
canonical/native/placement binding before reporting the intent observation.
Historical absence remains unknown and is not filled. The producer check caught
that canonical Go roles are `soda-coder`/`soda-reviewer`; native decoding now
translates those explicitly to worker roles while preserving historical plain
role reads. The pre-staging failed role fixture is retained under
`/home/vince/tmp/fnr-live3-1105/`.

The existing real-unit/private-keyring native fixture passed a Go-recorded intent
before registry launch, observed the exact file before the live child, completed
the finite local ELF, and persisted the observed process/released custody through
Go while preserving the intent. Store and raw SQL refused intent replacement.
Recovery refused seven changed receipt/custody copies, eight changed intent
bindings and changed encoding; originals were restored. A pending publication
refused, and a separate intent-only legacy start refused before custody with no
root stage/preparation. Normal supervisor restart after dummy credential
withdrawal retained exact intent/process/acquisition/canonical mapping and fenced
the old snapshot. Agent/relay MainPIDs were zero, exact roots absent, and temporary
units removed. Evidence/driver:
`/home/vince/tmp/fnr-live5-1105/`; result SHA-256:
`e532ef87bcc296d99ac4dc881fd6ecdebf320e4b07748ccab5993c4b16b7c140`.
The existing supervisor/Go/Rust check also passed legacy metadata reads across
restart without a synthesized intent under `/home/vince/tmp/fnr-legacy1-1105/`
(result SHA-256 `e8d8619319db05d0fee01b6edf3192496527dce54a0bf0643ac6bc650ab5a557`).

These are retained-input, dummy-only local-ELF checks with a declared unplaced
Go record and synthetic native runtime/placement generation. They do not prove
an actual dispatch packet, admitted placement, provider inference, interrupted
writes during a real owner kill, reboot, installed Quit or Go outcome settlement.
Remaining dependencies: record this intent in the atomic admitted Go dispatch
packet before the external call, require it on the production native launch
ports, compose current run/custody/MCP/credential admission and reconcile outcomes
transactionally. Startup now follows ledger availability, with the resource-only
evidence below. Missing
history must stay fenced instead of being backfilled. Prove real interrupted
publication/launch/replies, worker/owner death, root/process/credential retirement,
normal restart/reboot, actual finite provider handling and installed Quit. The
unproved launched roots remain fenced; N3/N4/D3 remain open.

Native recovery fact recording now has a production path without starting the
coordinator. `factory_provider/recovery.rs::NativeRecovery::record_evidence`
uses the joined private `product.rs` owner and the fixed Go observation route in
`pkg/factory/runtime/native_execution_observation.go`. The handler requires the
current instance and the existing private peer/key channel, refuses browser and
noncanonical requests, and decodes a bounded facts-only body. The Go endpoint
derives the desktop UID from its owned process, not a request field.
`pkg/store/factory_native_observation.go::ObserveNativeRecovery` locks the current
canonical row, validates exact stored intent/profile and run state, and uses
`execution.ObserveNativeReceipt` / the existing `NativeRecord.Advances` rules.
It updates only native facts. A confirmed child exit is at most Exited, not
root-retirement proof; absent exit status stays uncertain. Reported NotAcquired
custody and caller-invented root/outcome/reconciled fields refuse. Outcome,
admission, active/settled projections and spending/accounting remain untouched.

The reader can now use a connection captured in a verified intent to inspect the
first acquisition not yet in the Go snapshot. Existing nonempty acquisition
identity/generation remain immutable. Missing intent/connection/evidence is not
filled from an unrelated journal, and known release/seal cannot regress.
This covers the gap where native receipt/custody exist but Go has only its
pre-launch intent. Fresh Go transaction validation remains mandatory even when a
Rust snapshot is older. The native fact path does not grant positive liveness.

The existing real registry/isolated local-ELF/private-keyring/PostgreSQL/backend
fixture passed that first-observation path before any fixture observation write.
The production Rust consumer recorded the actual process/acquisition, concurrent
replays succeeded, and Go verified exact native facts with active=true,
settled=false, no outcome and no admission. Refusals covered stale instance,
browser origin, wrong bearer, noncanonical query, changed intent/process/custody,
invented retirement/outcome/reconciled fields, and fabricated NotAcquired custody.
Replays/refusals left the retained row unchanged. Existing evidence mismatch,
pending-intent and legacy-start fences also passed. After dummy credential
withdrawal, normal supervisor restart preserved the original facts and replayed
the production observation path under the new current instance. Exact roots were
absent, agent/relay MainPIDs zero and temporary units removed. Evidence/driver:
`/home/vince/tmp/fnr-observe1-1105/`; result SHA-256:
`07eeb2023b6e9801729438be5e1f6ac9f6c01a6e5fd0d0fcbe2eca5eca83c968`.

This used retained inputs and declared unplaced Go metadata with a synthetic
runtime/placement generation, not admitted dispatch or actual provider inference.
The observation transaction proves neither root retirement nor ledger outcome,
reservation/accounting settlement, interrupted-owner recovery or installed Quit.
Production dispatch still explicitly rejects native packets pending its owning
placement/execution gates. Keep that fence while composing output/candidate
handling and the existing Go settlement
owner. Prove actual interrupted launch/write/commit replies, worker/owner death,
restart/reboot, admitted finite provider work and installed retirement. No N3/N4
or D3 gate closes here; facts-only recording must not become an intake bypass.

### Current post-ledger resource recovery evidence

`supervisor.rs` now starts the private Go Store before native root recovery,
retaining the exclusive profile lock and withholding product/provider readiness.
`factory_provider/recovery.rs::recover_profile` retires confirmed unlaunched
staging through the launch-fenced broker call. A launched or partially retired
root requires the unique Go binding, exact local intent, complete receipt and
already durable custody seal. Coder work additionally requires captured or
unchanged candidate evidence. Missing proof refuses startup and preserves the
root. `run.rs` now seals confirmed local thread/socket/placeholder cleanup before
the broker effect. `custody.rs::release_recovered` never creates a missing seal;
it records release only after the exact broker retirement acknowledgement.

`pkg/store/factory_native_retirement.go` owns the subsequent PostgreSQL
transaction. It locks the canonical run, compares immutable intent and retained
process/acquisition plus sealed/released custody, and advances only native
retirement. The fixed private routes in
`pkg/factory/runtime/native_execution_retirement.go` retain current peer/key and
instance admission. A bounded paged inventory finds fact-recorded pending rows
after root removal, allowing a lost acknowledgement to be retried. Neither
inventory nor retirement settles outcomes, spending, reservations, active/settled
flags or placement. Unobserved intent-only history is not adopted.

The existing real isolated finite-ELF/private-keyring/PostgreSQL/Go integration
fixture passed two actual SIGKILL owner interruptions: after durable receipt and
seal before root retirement, and after root removal/release while the next Go
write waited behind a fixture row lock. Owned Go/PostgreSQL exits were confirmed
with pidfds. Removing the exact seal refused startup with the root preserved;
restoring its original inode permitted recovery. The final startup discovered
the pending Go row with no root and retained the original process, intent,
acquisition and instance while leaving the run active, unsettled and unplaced.
Retirement replay was stable. Stale/browser/wrong-key requests, altered
intent/process/acquisition, invented outcome fields and invalid inventory
cursors refused. Agent/relay MainPIDs were zero, run/preparation roots absent,
and disposable units removed. Evidence and exact drivers:
`/home/vince/tmp/fnr-start3-1105/`; result SHA-256:
`f47d4385b2779ca9af5d4ae923baf7dc888bc0263ec3e8928d35df43d34a34f2`.

Earlier failed checks are retained under `fnr-start1-1105` (seal ordering) and
`fnr-start2-1105` (fixture pidfd polling bug, then successful resumed recovery).
The first disposable root was explicitly cleaned up from its exact receipt;
neither a missing seal nor Go retirement was fabricated. The final fresh check
above uses the corrected production ordering and monitor. Locked offline runtime
and driver builds, affected Go builds, formatting and whitespace checks passed.

This remains retained-input/dummy-only evidence with declared unplaced Go
metadata and synthetic runtime/placement. Exact blockers: production atomic
admitted dispatch/ports still depend on N3, T06-CONNECT and T12-PLACEMENT; no
coordinator/native intake starts. Missing/torn intent, receipt, seal or release
proof remains unresolved and must not be reconstructed from owner absence.
Interrupted launch/write/commit boundaries beyond those checked, worker death,
reboot/power loss, complete output/candidate adoption and Go transactional
outcome/accounting recovery still require implementation or evidence. Real
finite provider work and installed Quit remain unverified. Close these only
with actual admitted effects, exact resource/custody evidence and durable
settlement across those failures. N3/N4/D3 remain open. Routine implementation
and checks stay in the normal queue; use user-started Daybreak if a particular
check looks like hacking.

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
next four to six hours at that checkpoint. The later connected result below
supersedes that temporary consent blocker.
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

### Current connected desktop evidence

T06-CONNECT is now Complete. On 2026-11-05 the user approved a fresh GitHub
device sign-in after an actual dummy store/reopen/remove check in a fresh private
default Secret Service collection. Native OAuth verified and stored the encrypted
grant. The first connected attempt passed direct account/repository/product IPC
but timed out waiting for the shared feed: repository selection had preserved the
account-wide New request route. `frontend/desktop/connections.ts` now opens a
new repository's feed while preserving the route for a same-repository reconnect.
The first failed log and exact source diff remain retained. The retry reopened
the same encrypted grant without another challenge; it exited zero.

Actual production Rust connector, Go canonical services and real Tauri/WebKit
shared Lit views passed identity, repository paging and private detail, direct
feed/issue/PR reads, missing-job unavailability, stale selection/disconnect
refusal, request cancellation, canonical reconnect, account/repository controls,
feed/issues/detail, repository-specific issue drafts and restored request drafts.
The native owner exercised real refresh and refused the prior local revision,
then removed the owned local account and refused its capability. Quit completed
and PostgreSQL logged shutdown. The safe result records two repository pages,
private read and refresh as true; remote revocation is explicitly false.

Evidence/scripts/source identity are under
`/home/vince/tmp/factory-consent-keyring-1105/`. Final result log SHA-256:
`4f660c774e29cad8889804379353bd166e5d64cc8b6bfa3f41de448af13ac6ce`;
safe `consent-result.json` SHA-256:
`74d46b64895abd2a429ec900e855d9b1406063478fe1db5499e323d4e2769e28`.
The scratch desktop ELF SHA-256 is
`6d764aed3bf801cc10cb1425a377de5a91d5ff7297ae3e4794168213f8b6ccd8`;
the retained WebKit library hash matches the earlier 2.54.2 runtime. Official
[stable metadata](https://webkitgtk.org/releases/) was rechecked on 2026-11-05 and
still selects 2.54.2. Type checking, affected lint, shared desktop bundle and
locked offline native-check build passed. This is an actual connected source
journey using the retained runtime, not a fresh whole-distribution or installed
qualification, diagnosis of the unavailable historical heap crash, Fountain
desktop result or executor admission.

Remaining dependencies: T06-GITHUB-EVIDENCE must supply actual workflow-job and
branch-protection facts without filling unknown requirements; T06-GITHUB must
finish full provider normalization/current-head and remote-revocation evidence
and retire historical Go-observer consumers. Installed authentication/IPC and
multi-host parity remain their packaging/journey gates. Relevant files remain
`cmd/soda-desktop/src/{check_credentials,check,product,bridge,signin}.rs`,
`frontend/desktop/{connections,product,transport}.ts`,
`lib/desktop-runtime/src/{credentials,github_provider}/` and the Go canonical
projection/services. Close each remaining gate with actual scoped positive and
refused operations on its target path, exact retained source/runtime identity,
revocation/retirement and appropriate installed evidence. T06-CONNECT success
does not close those gates or D1/D2/D3/D5.
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
| Workflow-job and branch-protection producer: T06-GITHUB-EVIDENCE Complete; final T06-GITHUB gate open | Native authenticated jobs/protection and canonical Go projections passed connected source reads and declared consistency/refusal cases. This producer no longer blocks T06-GITHUB; remote revocation, historical observer-consumer migration and installed admission remain separate. | `lib/desktop-runtime/src/github_provider/{request,reads,records,evidence,protection}.rs`, `pkg/factory/runtime/{native_product_jobs,native_product_protection,native_product_evidence}.go`, `tools/native-checks/src/native_github_transport.rs`, `cmd/soda-desktop/src/check_credentials.rs` | Actual approved connected reads prove canonical jobs, progressing continuation, current-head/protection scope, private access, refresh and shared UI. Declared native/Go cases prove positive classic/app-qualified rules, empty/partial observations, policy/run/attempt/count/base/branch/cursor/refusal behavior and pending-read withdrawal. No merge authority is inferred. Evidence and exact scope are recorded below. | T06-GITHUB still needs real provider-side revocation followed by refusal and migration of retained Go-observer consumers. Installed admission and D1/D2/D3/D5/native execution retain their own finish conditions. Classic positive policy mutations were declared; the live public classic read remains unavailable. No complete atomic provider history or passing merge gate is claimed. |
| Worker MCP transport and Factory tool policy within D3/N4 | The private native producer and namespaced model policy exist, but the production Go native listener/current-run observer and its exact backend/instance/capability binding are missing. Go must supply the captured canonical tool catalog from the same server definition before launch. N4 also depends on T06-CONNECT and T12-PLACEMENT. | `lib/desktop-runtime/src/factory_provider/{mcp,owner,run,response}.rs`, `lib/desktop-runtime/src/factory_provider.rs`, `cmd/soda-factory-worker/src/bin/{soda-factory-agent,soda-factory-provider-relay}.rs`, `pkg/factory/{runtime/native_server,mcpserver/bound}.go` | Current local Muse echo completed MCP startup/discovery; a tiny isolated ELF separately verified fixed configuration and run-placeholder delivery through the real relay/native owner/Go bound handler with a synthetic active capability. Missing binding made no callback. Credential withdrawal and owner cancellation each dropped a pending native MCP future; uncertain cancellation retained root evidence. A local scripted provider then made the actual CLI execute a qualified Factory call and return its fixture result. The native component check accepted those captured requests/replies and Go's 24-tool catalog, and refused catalog, namespace and execution-call identity changes. | Compose the trusted catalog/port with the current Go backend and Rust signer under the admitted canonical capability. Prove actual provider Factory tool handling through the isolated native owner, current Go work/run/role checks, expiry/revocation, exact backend/instance binding and no root/IPC/provider secret in the worker. Then qualify a real finite task, transaction settlement and crash/reboot recovery. The checks used synthetic capabilities and locally scripted replies, with no canonical Store row or real inference, and do not close this gate. |
| Desktop authentication and IPC admission: connected source evidence complete; final provider and installed consumers open | T06-CONNECT passed the real connected source journey. T06-GITHUB-EVIDENCE now qualifies its source producer. T06-GITHUB and packaging/installed parity retain their separate requirements. No installed authentication/IPC or full remote-revocation result is inferred. | `cmd/soda-desktop/src/{main,signin,bridge,product,check_credentials}.rs`, `frontend/desktop/{connections,product,transport}.ts`, `lib/desktop-runtime/src/{github_provider,credentials,supervisor}.rs`, `pkg/factory/runtime/{native_application,native_product}.go` | The current connected section records user-approved OAuth, verified encrypted grant reload, actual canonical account/repository/product IPC, shared feed/issues/drafts, switches/reconnects, local withdrawal, real refresh and Quit. The first route failure and passing retry are retained. Historical missing crash artifacts remain undiagnosed. | Complete workflow/protection observations and provider revocation on the actual connector, then demonstrate authenticated peer/window and exact instance/account/resource admission, stale/foreign refusal and retirement in the installed package. Keep missing execution unavailable. The source connected result does not qualify an installer or Fountain desktop. |
| GitHub credential custody and revocation: T06-GITHUB and the connected GitHub consumers | T06-CONNECT and T06-GITHUB-EVIDENCE source producers are complete. The connected desktop proves private/paged reads, refresh and scoped workflow/protection observations. Final remote revocation and retained historical observer-consumer migration remain open. | `lib/desktop-runtime/src/credentials/`, `lib/desktop-runtime/src/github_provider/`, `lib/desktop-runtime/src/github.rs`, `pkg/factory/runtime/native_product_observations.go`, `pkg/factory/runtime/native_product_reads.go` | The Go native token route was retired; Rust owns OAuth, refresh, HTTPS, decoding and native credential lifetime. Approved native reads, private access, refresh and retained identity are recorded in the plan. | Connected consent/identity/storage, canonical binding, private/paged reads, actual refresh and local removal now have evidence above. Complete provider-side revocation and subsequent refusal without credential disclosure. Audit and retire or migrate historical Go-observer check consumers with equivalent current evidence; do not treat the completed source producer as full provider or installed qualification. |
| D1 standing GitHub automation policy: T11-INTAKE, T12-GITHUB-PRS and installed GitHub automation | T11-INTAKE depends on T09-EXECUTION, T11-WRITES and T12-PLACEMENT. Before automatic intake, select and enforce the explicit standing authorization policy or retain original-author creation evidence as mandatory; discovered work must wait for maintainer adoption until then. | `pkg/factory/runtime/placement.go`, `pkg/factory/control/dispatch_launch.go`, `pkg/factory/execution/`, `lib/desktop-runtime/src/github_provider/` | The plan identifies the policy choices and keeps automatic intake inactive. Existing canonical authorization and ledger owners remain Go. | Record the selected policy, eligible actor/fork/repository, limits and accepted revision; prove authenticated polling/cursors, wake reconciliation, refusal of unauthorized or self-triggered work, and the eligible issue-to-PR journey through the admitted isolated executor. |
| D2 Fountain desktop authentication and grant revocation: T10-GATEWAY, then T10-PUBLISH/T10-INTAKE and desktop Fountain journeys | T10-GATEWAY depends on T05-CREDENTIALS, T06-FOUNTAIN, T10-WRITES and T12-PLACEMENT. Supported native enrollment/login and revocable grants have not been proved. Browser authority must not be spoofed and the raw Unix socket must not be exposed. | `lib/desktop-runtime/src/credentials.rs`, `cmd/soda-desktop/src/{main,bridge}.rs`, `pkg/factory/runtime/placement.go`, Fountain host adapters described by T10-GATEWAY | Existing OAuth PKCE code is only spike evidence; Fountain SDK/native writes and shared UI components have separate completed producer evidence. | Prove supported actual desktop enrollment, current authenticated HTTPS reads and operations, scoped IPC admission, grant withdrawal/revocation, and centrally coordinated placement using the real host. Include denial after withdrawal and retained recovery. |
| Native recovery intent binding within N3/N4/D3 | A Go-defined immutable intent and native pre-staging copy/read comparison now exist. A facts-only Go observation transaction now records first process/acquisition evidence and replays without settling work. Production dispatch/native ports do not yet compose the admitted binding. Startup now compares exact sealed complete evidence after ledger readiness; missing proof still fences recovery. Receipt/custody alone and historical missing intents grant no recovery authority. | `pkg/factory/execution/{native_intent,launch,native_record}.go`, `pkg/factory/run.go`, `pkg/store/{factory_execution_schema,factory_native_recovery,factory_native_observation,factory_native_retirement}.go`, `pkg/factory/runtime/{native_execution_observation,native_execution_retirement}.go`, `pkg/factory/execution/native_observation.go`, `lib/desktop-runtime/src/factory_provider/{intent,executions,receipt,custody,recovery,run}.rs`, `lib/desktop-runtime/src/supervisor.rs` | The real registry/local-ELF/private-keyring/Go fixture retained an exact intent before the observed child, preserved it through process/custody recording and normal restart, refused byte replacement in Store/SQL and changed/partial native evidence, and fenced an intent-only legacy start. The later fresh fixture passed actual owner death before root retirement and after root removal/lost Go acknowledgement, plus missing-seal startup refusal and retained original facts. The record/runtime/placement were declared fixture metadata; no production admission was granted. | Wire exact intent bytes through the atomic admitted dispatch and native effect ports, retain uncertainty across remaining interrupted writes/launches/lost replies, and compose atomic output/candidate/outcome recovery with fresh Go transaction validation. Prove root/process/credential retirement and atomic outcomes across worker/owner death, restart/reboot, actual provider handling and installed Quit. Never synthesize an intent for historical acquisition or treat an unplaced intent as admitted placement. |
| D3 isolated native worker and execution admission: T09-NATIVE-EXECUTOR and T09-EXECUTION; then T10-PUBLISH, T11-INTAKE, packaging and installed automation | The selected worker must have a distinct host identity with no access to the desktop user's files, processes, session bus or keyring. The root broker must bind the exact admitted run, role, snapshot, worktree, process, credential and retirement. T09-EXECUTION additionally requires T06-CONNECT and T12-PLACEMENT. Product execution must remain unavailable until this gate closes. | `cmd/soda-factory-broker/src/main.rs`, `lib/desktop-runtime/src/factory_provider/{broker,run}.rs`, `cmd/soda-factory-worker/`, `lib/native-process/`, `lib/desktop-runtime/src/{execution,supervisor,credentials}.rs`, `pkg/factory/execution/`, `pkg/factory/control/{dispatch_launch,mcp_key}.go`, `pkg/factory/runtime/{native_application,native_execution_recovery,native_server,placement}.go`, `pkg/store/factory_native_recovery.go` | The plan records live distinct-UID service, file/keyring denial probes, captured ELF transfer, bounded process/cgroup retirement, broker staging/preparation/relay/agent probes, exact-run attestation, owner-death teardown, and retained Go isolated-run validation. A disposable current broker/relay/agent run also proved exact setup retirement of an unlaunched stage, refusal after dummy launch, retained launch observation and explicit final retirement. The headless process primitive is outside the production desktop dependency graph. These are component probes, some using dummy credentials or a fake provider. | Complete and qualify production Go admission and semantic Rust ports to the single coordinator; role-limited tool execution and exact worktree/candidate/artifact capture; strict workspace storage bound; cross-run denial; scoped credential delivery/withdrawal and MCP signing-key custody; durable transactional receipt/outcome reconciliation across crash/reboot; production Quit; and a real finite provider run. Specifically prove the lifecycle fence across a real interrupted launch and restart with ledger outcome reconciliation; the disposable normal-path check does not cover a torn file update. On the actual launch path capture worker UID, file/process/keyring/session denials, exact mounts and role capabilities, deadline/tree cleanup, credential withdrawal and retained recovery. No same-user fallback or dummy-token result closes D3. |
| D3 reviewer/coder tool admission within T09-NATIVE-EXECUTOR | Muse advertises workflow and memory functions despite reviewer CLI flags. The trusted role must come from the Go admission and broker attestation; the production credential owner must filter requests and validate model tool events before the worker can act. This depends on the live `RunOwner` path and a real finite provider task. | `cmd/soda-factory-worker/src/bin/soda-factory-agent.rs`, `lib/desktop-runtime/src/factory_provider.rs`, `lib/desktop-runtime/src/factory_provider/{owner,response}.rs`, `pkg/factory/execution/` | The current Muse ELF sent an 88,434-byte reviewer request from an isolated service with 11 functions and 128,000 requested output tokens. Production `Policy::admit` applied by a disposable fake owner reduced that exact body to two read functions and 4,096 tokens. Earlier scripted SSE checks refused unoffered functions. | Prove the production `Owner` applies that policy to the actual admitted reviewer and coder, and that complete real model/tool responses enforce their role through execution and retirement. Capture positive allowed functions and denied disallowed events without promoting a scripted response or dummy 401 into a provider run. Route a check that looks like hacking to a user-started Daybreak session. |
| Native provider credential proxy within D3 | The observed Muse CLI can accept a key on stdin and a base URL. Supplying the reusable owner key to the worker violates the selected custody rule. The relay/proxy must authorize one exact run and withdraw use on cancellation, owner death and restart. The user authorized one dummy-token/prompt request after automatic approval review stopped an earlier attempt. This approval boundary is separate from Daybreak's hacking-like-work boundary. | `cmd/soda-factory-worker/src/bin/soda-factory-provider-relay.rs`, `cmd/soda-factory-worker/systemd/`, `lib/desktop-runtime/src/factory_provider/{run,owner,response}.rs`, `lib/desktop-runtime/src/credentials/`, `pkg/factory/control/mcp_key.go` | A private-loopback fake provider, address-policy probe and invalid-token native owner/relay run are recorded. Owner pidfd teardown and subsequent local 503 after withdrawal were observed in disposable units. Production `RunOwner` completed a credential-free coder run and durable receipt. With explicit user authorization, production `RunOwner` also completed one isolated Muse reviewer attempt using a private in-memory dummy credential. Muse reported the dummy key rejected, exited 1, and the 191-byte complete output was retained; the worker/relay retired. The CLI error is consistent with remote authentication rejection, but the exact upstream HTTP status was not captured. | Prove final authenticated endpoint and network confinement, usable owner-held credential use without exposing it to the worker, run-bound authorization, withdrawal/owner-crash/restart denial, complete model/tool response admission and an actual successful finite provider task. Then prove Go transaction settlement and recovery. A dummy-key rejection does not close D3. |
| Native MCP signing-key custody within D3/N4 | The dormant Go `LoadFactoryMCPKey` creates and returns a raw persistent root key; its separate appliance path remains in Go. The selected native allocation requires Rust to own secret derivation and lifetime before activating the Go native execution consumer. | `pkg/factory/control/{mcp_key,coordinator,dispatch,dispatch_launch}.go`, `pkg/factory/mcp_submission.go`, `pkg/factory/{httpapi/worker,mcpserver/{bound,catalog}}.go`, `pkg/factory/runtime/{native_mcp_signing,native_server}.go`, `lib/desktop-runtime/src/{mcp_signing,collaboration,supervisor}.rs`, `tools/native-checks/{native_mcp_signing_check.go,src/bin/factory-native-mcp-signing-check.rs}` | Go dispatch and worker HTTP accept a scoped derivation function while preserving the separate appliance key handler. The Rust signer creates and reopens a private profile key and derives the fixed HMAC. Its supervisor-owned host socket has a bounded fixed signing route; the native Go server binds its client to the current instance. A real disposable PostgreSQL/Go/Rust supervisor check passed direct host-socket derivation, wrong-key and wrong-instance refusal, invalid-scope refusal, ordered socket retirement and stable key reopening across a restart. The synthetic Go client also called that Rust route successfully on both incarnations, matched the Go capability HMAC and refused after retirement. The bound Go adapter now refuses missing current-work preparation and missing/empty live observation; the bound catalog requires exact observation at each tool call. A synthetic bound HTTP initialization passed exact observation and refused those missing inputs on both ordered supervisor incarnations. Existing MCP HTTP integration and locked offline Rust checks passed. The fixture read its own synthetic key for comparison; it did not use a product capability row, a native MCP listener, worker delivery or a real credential. | Wire the Go signing client through an admitted capability on the real native coordinator and listener, then prove exact-run/current-state admission, expiry/revocation refusal, key rotation/restart behavior, worker delivery and root-key absence from Go/UI/logs on that production path. The current desktop starts no native coordinator or MCP listener. The callback currently authenticates a same-UID peer with the shared IPC key and instance; demonstrate that the isolated worker cannot acquire that authority. Synthetic callback evidence does not close custody. Route adversarial checks that look like hacking to a user-started Daybreak session. |
| Authentication and admission at other native boundaries | Any new or changed broker, worker, product HTTP, IPC or grant path must be verified before activation. Normal coding agents may implement and qualify these boundaries; source validation alone is not admission proof. | `cmd/soda-factory-broker/src/main.rs`, `cmd/soda-factory-worker/src/`, `cmd/soda-desktop/src/`, `pkg/factory/runtime/native_server.go`, `pkg/factory/runtime/native_application.go` | Existing private Unix peer checks, bounded bridge contracts and some root-peer broker probes are recorded in their producer checkpoints. | For each activated path, demonstrate authenticated peer and exact instance/profile/run/resource binding, denied foreign/stale callers, cancellation/retirement, and no credential exposure through UI, logs or Go observation DTOs. Record the actual production path and its negative evidence. |
| D5 issue-only Fountain collaborator parity | Current Fountain extension admission requires code-unit read. Full issue-only visibility needs a separate Fountain capability change and authorized scope; ordinary code-reader journeys do not close it. | Fountain extension/adapters referenced by `T06-FOUNTAIN` and `T07-READ`; `pkg/factory/runtime/native_product_reads.go` for the standalone boundary | Current code-reader routes and shared UI have producer evidence. | Obtain the separate authorized Fountain capability implementation and prove issue-only reads/writes and denial outside scope on the actual host before claiming collaborator parity. |

## Current dependencies

The canonical table now has T06-CONNECT Complete after the actual connected
journey above. T06-GITHUB-EVIDENCE is the next row in order; it and T06-GITHUB
block T11-WRITES and the shared cross-provider editor/PR consumers. T07-READ
and T12-PLACEMENT now have their connected desktop prerequisite satisfied.
T09-NATIVE-EXECUTOR is itself D3 work; T09-EXECUTION and its publication,
automation, packaging, installed and upgrade consumers remain pending. T10-GATEWAY
is D2 work. T12-PLACEMENT must keep automation inactive until placement and execution admission are proved.

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

Historical D3 restart checkpoint (superseded by the current post-ledger resource
recovery section above): `Children::start` in
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
credential. At that checkpoint the helper was not composed into desktop startup, so the
recorded ordering blocked launched roots. The later post-ledger resource recovery
above supersedes that ordering. Keep launch disabled
until a trusted Go-record read, exact receipt validation, root/process and
credential retirement, and atomic outcome reconciliation are composed and
proved across an interrupted owner and a full restart. That source inspection established the historical ordering. The later actual
owner-kill resource checks do not prove full admitted outcome/crash/reboot recovery.

If a later slice reveals another authentication, IPC admission, credential or
isolation requirement, add a row with its exact dependency, files, positive and
negative evidence. Route only hacking-like work to Daybreak. Do not mark a
canonical slice Complete while its required qualification is open.

T06-GITHUB-EVIDENCE native producer checkpoint (2026-11-05):
`github_provider/evidence.rs` supplies bounded workflow-run lists, exact
run-attempt job pages and PR-bound classic protection/active-ruleset observations.
It retains repository/run/job/head bindings, nullable provider fields and
original state/conclusion. Attempt-specific job URLs are accepted only for the
same numeric run and attempt; they are never followed. The first live check
exposed that representation difference and refused it. Three failed logs remain
retained; the subsequent retry reopened the same approved encrypted grant.

The final real Tauri/WebKit/native Rust/PostgreSQL/Go check exited zero. It
observed one actual public job on the first requested workflow page, checked
run/attempt/head, refused an altered head, read PR head/base/branch with active
rules, and refused an altered protection head. Classic protection was not
visible and remained null. The existing connected shared UI journey, two
repository pages, private read, real refresh, local withdrawal and Quit also
passed. Evidence/scripts/source diff and safe results are under
`/home/vince/tmp/factory-github-evidence-1105/`; final log SHA-256:
`471b3a59ec93557ff3fb971b9f932231d4fa037f919049f2e9289b6efd1c43c3`;
native evidence result SHA-256:
`0fa93a0753f5cb55eb65740dafa916f97ea624cca56ce4fe874981fb28a13be8`.
Locked offline runtime/native-check builds, touched formatting and whitespace
checks passed. This uses retained runtime inputs and proves the stated native
reads, not a newly fresh installed baseline or complete provider admission.

At that native-producer checkpoint the parent remained Active and Go job reads
were deliberately unavailable; the later canonical consumer below supersedes
that job-read fence. Protection/requiredness/status gates remain unknown. Its consumer must join
these native facts to current canonical PR/head/base and scoped continuations
without moving provider decoding into Go. Job pagination, hidden classic rules,
app-qualified requirements, rule mutation/partial-data handling and revoked or
changed scope need their actual consumer evidence. Primary contracts were
checked against GitHub's current API version 2026-03-10:
[workflow runs](https://docs.github.com/en/rest/actions/workflow-runs?apiVersion=2026-03-10),
[attempt jobs](https://docs.github.com/en/rest/actions/workflow-jobs?apiVersion=2026-03-10),
[active branch rules](https://docs.github.com/en/rest/repos/rules?apiVersion=2026-03-10).
No provider, D1/D2/D3/D5 or installed gate is closed by this producer.

T06-GITHUB-EVIDENCE canonical job consumer checkpoint (2026-11-05):
`pkg/factory/runtime/native_product_jobs.go` now reads the defining native facts
through the current scoped callback, binds them to canonical repository/PR/head/
base before and after I/O, and projects run/attempt/job identities. Cursors retain
that scope, page size, run/job pages, known run/attempt and observed run count.
The native filtered-workflow limit refuses a total beyond GitHub's 1,000-result
cap. An empty job page may keep a continuation rather than being reported as the
end. Workflow-definition commit, separate trigger attribution, approval,
requiredness and passing merge status stay unknown. Go still owns projection
and canonical product decisions; provider decoding remains in Rust.

The existing actual connected Tauri/WebKit/native Rust/Go/PostgreSQL/GitHub check
exited zero after real canonical HTTP job reads and changed-head 409 / malformed
cursor 400 refusals. The full shared UI/private read/refresh journey also passed.
Its `--check-retain-account` mode preserves the approved encrypted fixture account
for follow-up; the safe result records local removal false. Earlier default-mode
local-withdrawal evidence is unchanged. The first failed assertion used the wrong
JS type for canonical PR numbers. A subsequent run returned 503; that failed log
is preserved, not treated as success. The instrumented retry passed; safe backend
failure diagnostics contain fixed stages, bounded code/status and elapsed time,
without resource IDs, URLs, provider bodies, credentials or SQL errors.

Source/scripts/failed attempts/final safe results:
`/home/vince/tmp/factory-github-jobs-1105/`; final result log SHA-256:
`b1b2522cf7dca2421bd043b10129359de7d9f3d96e963387b477ec0aa9ca29a9`;
safe result SHA-256:
`4c9f1ca021734b362884f4ed7f5cabf37094a1c124b5d777b50e06fd5cabb4b0`.
Affected Go builds/vet, locked offline native-check build and whitespace checks
passed. These retained-runtime source checks do not qualify an installed build.

Exact remaining blocker: the Go branch-protection product projection and its
classic/ruleset/app-qualified requirement semantics are not finished. The job
check follows a continuation conditionally, but the retained result does not
independently establish that this branch executed; collect explicit pagination
evidence before closing the parent. Prove empty/partial data, changing rules,
cursor or scope changes and withdrawal on the completed consumer. Keep hidden
classic protection unknown. Complete remote revocation and installed admission
in their owning slices. T06-GITHUB-EVIDENCE remains Active; no full provider or
D1/D2/D3/D5 gate is closed.

T06-GITHUB-EVIDENCE protection/continuation checkpoint (2026-11-05):

The canonical Go protection consumer is composed with the defining Rust provider
constraint decoder. Relevant additions are
`lib/desktop-runtime/src/github_provider/protection.rs`,
`pkg/factory/runtime/native_product_protection.go`, typed requirements in
`pkg/collaboration/records.go`, and the existing connected acceptance driver in
`cmd/soda-desktop/src/check_credentials.rs`. Source visibility, app-bound check
requirements and unmapped rule kinds survive projection; hidden classic input
stays unknown. Go retains canonical identity and product policy. Rust rechecks
PR/branch and projected policy sources before returning. No per-check
requiredness, bypass actor authority or passing merge gate is asserted.

The first check reached the unchanged 8,000 ms job deadline at PR revalidation.
The provider now composes workflow/job/PR observations in one scoped native
operation, avoiding repeated identity/repository reads while retaining the
before/after checks. The failed attempt remains in
`/home/vince/tmp/factory-github-jobs-1105/protection-attempt1/`.
The previous successful job-only evidence is preserved in
`/home/vince/tmp/factory-github-jobs-1105/job-consumer-checkpoint/`.

The rebuilt actual connected check exited zero and explicitly proved a
progressing canonical job continuation, canonical protection read, changed-head
409s and malformed job-cursor 400. Classic protection was unavailable and active
rules visible; missing classic input did not become complete requirements or
zero approvals. Private access, actual refresh and the shared UI journey passed.
The approved encrypted fixture account is retained for follow-up; this result
records local removal false and remote revocation false. Evidence root:
`/home/vince/tmp/factory-github-jobs-1105/`; result log SHA-256
`23577ad70263196dadf4d1acf4810ec9af693e37656794771d68e13076207cdd`,
safe result `bf7d3f18307d75fb9ef8969519cdd6204056651ed7ec79d81b4b6096c49f8007`,
explicit canonical evidence
`295e7dba0e461997c28d0ea784a3fd1cfe657be7f3387c267ddf5fa407477f85`.
Go build/vet, locked offline native-check build, shared TypeScript checks and the
engine/provider dependency-boundary check passed. Retained runtime inputs are
not an installed or fresh distribution qualification.

Exact remaining blocker/dependency: T06-GITHUB-EVIDENCE needs positive classic
and app-qualified requirements and partial/empty/mutating rule observations,
plus run/attempt/cursor/scope changes and withdrawal on the completed consumers.
The new change detection and positive classic decoder are source implementation,
not verified mutation/positive-classic evidence. Use actual permitted provider
reads or the existing real integration harness with explicitly declared provider
responses; label declared evidence separately. Never mutate an unrelated remote
repository to manufacture evidence. T06-GITHUB remote revocation still requires
provider-side proof and subsequent refusal; retained local capability rejection
cannot replace it. Installed peer/window/instance/account/resource admission and
D1/D2/D3/D5/native executor gates remain pending in their owning slices. These
ordinary implementation/qualification tasks do not require Daybreak unless a
particular action looks like hacking; no gate is closed by this checkpoint.

T06-GITHUB-EVIDENCE declared policy and scope qualification (2026-11-05):

Relevant files: the existing native check in
`tools/native-checks/src/{native_github_transport.rs,bin/factory-credential-operation-check.rs}`,
`lib/desktop-runtime/src/github_provider/{protection,reads,evidence}.rs`, and
`pkg/factory/runtime/{native_product_evidence,native_product_observations,native_product_protection}.go`.
The declared HTTPS integration mode uses a fresh private bus/keyring/profile,
dummy token and process-local CA. Its loopback fixture has no forwarding or
outbound provider operation. Production TLS verification and selectors remain
active. Retained real accounts are separate and preserved.

Attempt 5 exited zero after 35 native/canonical cases plus local withdrawal.
Declared positive classic and rules preserve app-qualified requirements and
combined constraints; empty, hidden, legacy unknown and unrepresented policies
remain distinct. Actual native decoding and Go HTTP projection refuse declared
policy/branch/head/base/identity/repository changes, denied provider access,
rate limits and oversized rules. Status cursors also refuse changed base/branch;
withdrawal refuses a previously admitted local request. These are declared
provider responses through real native/keyring/Go/PostgreSQL components, not
actual provider revocation or live positive classic-protection evidence.

The integration check exposed an unknown-parameter completeness defect and a
base-change gap in the combined status/protection read. Both are fixed and the
corresponding declared refusals passed. Native status snapshots now capture
head/base/branch; Go compares the pins between reads, across empty check-run
fallback and during continuation. Old cursors lacking the new pin are rejected.
Unrepresented parameters cannot establish complete representation. Merge gate,
per-check requiredness and actor/bypass authority remain unknown.

Evidence and source/input hashes:
`/home/vince/tmp/factory-github-declared-1105/attempt5/`;
safe result SHA-256
`5b0e8cc3d263b55a660a9e7fadbaed98064528c5e533ca03f50be8042b9437df`,
log `8647d1a2330c0a002e5e285093fb1d067509687a682a55e50ecd7d85172014a0`.
Failed attempts 1–4 remain in that evidence root. No existing credential/profile
or historical proof was removed. Go build/vet, native integration/native-check
builds, dependency-boundary and whitespace checks passed with retained inputs.

Exact remaining dependency: complete workflow-job run/attempt/count/cursor and
empty-page cases before closing T06-GITHUB-EVIDENCE. T06-GITHUB still needs real
provider-side revocation followed by refusal, plus historical Go-observer
consumer migration. Installed peer/window/instance/account/resource admission,
native executor and D1/D2/D3/D5 finish conditions are unchanged. Declared refusal
and local withdrawal cannot close those gates. Routine product qualification
continues normally; a check that looks like hacking requires a user-started
Daybreak session.

The follow-up rebuilt live source journey passed the updated native/Go protocol:
canonical protection and job continuation, changed-head/cursor refusals, private
access, real refresh and shared UI. Live root:
`/home/vince/tmp/factory-github-jobs-1105/`; previous protection evidence is now
preserved in `protection-projection-checkpoint/`. Current live log SHA-256
`7b72a2b70483b91b2b84e59a7e4c3975acea4b4396264395a06b0ff573bb042d`,
safe result `bf7d3f18307d75fb9ef8969519cdd6204056651ed7ec79d81b4b6096c49f8007`,
canonical evidence
`295e7dba0e461997c28d0ea784a3fd1cfe657be7f3387c267ddf5fa407477f85`.
The real encrypted account is retained for remaining evidence work; local removal
and remote revocation are false in this result. Classic visibility in that public
read remains unavailable. No installed or full-provider gate is closed.

## Workflow/protection source producer completed — 2026-11-05

T06-GITHUB-EVIDENCE is Complete after its requirement audit in the canonical
plan. This closes that bounded source producer only. Relevant final changes are
`pkg/factory/runtime/native_product_jobs.go` (captured base/branch, job count,
previous page IDs and previous completed run in the cursor),
`lib/desktop-runtime/src/github_provider/evidence.rs` (exact counted pages), and
`tools/native-checks/src/native_github_transport.rs` (the existing real
integration check extended with declared jobs and pending-read withdrawal).
Go retains canonical identity and product decisions; Rust retains authenticated
provider operations/decoding and native credential lifetime.

The declared native/keyring/PostgreSQL/Go HTTPS integration check passed 62 case
entries across jobs/protection. Normal and empty/multi-run pages remain distinct;
run/attempt/count/base/branch/cursor changes, overlapping jobs, repeated completed
run, malformed bindings and truncated/inconsistent pages refuse. A pending job
read cannot publish after withdrawal, and a previously admitted old local
capability cannot start a job read. Attempt 8 records the five missed refusals
that prompted the cursor fix; attempt 9 records no missed refusals. Input source,
fixture, scripts, binary hashes and results are private under
`/home/vince/tmp/factory-github-declared-1105/attempt9/`. Safe result SHA-256
`cf1a9efcdceaaf1f79018f9d59d30175dd7fe458b89a36c1db44ee0ca6198ea7`;
empty missed-refusal inventory
`4b1956ba5ce910f65e2dd9a7563d99651cda7dcd8893dde419524462686d44ee`.
Declared positive classic and policy mutations are not live GitHub mutation
qualification, provider-side revocation or installed proof.

The rebuilt real connected source journey exited zero with canonical jobs and
progressing continuation, canonical protection, head/cursor refusals, private
access, real refresh and shared UI. Live root:
`/home/vince/tmp/factory-github-jobs-1105/`; previous live snapshot proof is
preserved in `protection-snapshot-checkpoint/`. Current log SHA-256
`3fa077b6ccf79ff2e225a411dacd7f49db220e8781bbaeb23ff35c57421d3b03`,
safe result `bf7d3f18307d75fb9ef8969519cdd6204056651ed7ec79d81b4b6096c49f8007`,
explicit canonical evidence
`295e7dba0e461997c28d0ea784a3fd1cfe657be7f3387c267ddf5fa407477f85`.
The real account remains encrypted and retained for follow-up. This result
records local removal false and remote revocation false. Live public classic
protection remains unavailable; requiredness, approval, workflow-definition
commit, actor/bypass authority and merge status stay unknown. These are bounded
page observations, not a complete atomic historical provider snapshot.

Exact remaining security/product gates: T06-GITHUB requires actual provider-side
revocation followed by refusal and auditing/migrating retained historical
Go-observer consumers. Installed peer/window/instance/account/resource admission
still needs its package journey. D1/D2/D3/D5 and native executor gates retain the
blockers/evidence in their register rows, including actual admitted isolation,
credential delivery/custody, listener/tool admission, finite provider work,
retirement/recovery and durable settlement. No dummy-token or local-withdrawal
result closes those gates. Routine implementation continues normally; only a
particular check that looks like hacking needs a user-started Daybreak session.

T06-GITHUB actual revocation attempt (2026-11-05, incomplete):
The new opt-in `--check-provider-revocation` consent observer in
`cmd/soda-desktop/src/{main,check_credentials}.rs` passed its full positive
connected journey and wrote a positive native identity/canonical repository
ready record. The owner then confirmed revocation of the exact client-specific
GitHub authorization. The observer passed actual native identity 401, canonical
identity/catalogue/job refusal and actual WebKit reconnect refusal. It failed at
its expected revoked-refresh classification before final proof/local cleanup.
This attempt is failed; do not promote it to a full provider gate result.

The retained profile has a durable `refreshing-credentials` marker. Do not replay
that refresh or clear its uncertainty fence to recover a token. No final safe
revocation result or local-withdrawal result was produced. Relevant private root:
`/home/vince/tmp/factory-github-jobs-1105/` with the current failed `result.log`,
`provider-revocation-ready.json` and `p/provider-revocation-ui-refused` barrier.
The preceding completed producer evidence is preserved in
`workflow-evidence-complete/`. Grants, profiles and prior failures remain intact.

Exact blocker: the failed check did not record which safe refresh failure class
occurred; expected `bad_refresh_token` was not established. Diagnose the provider
contract using noncredential fixtures and safe bounded failure classification;
retain pending custody. A full refresh-after-revocation proof needs a confirmed
provider outcome on a correctly instrumented, separately authorized fresh grant,
not replay of this uncertain pair. Historical observer check migration remains
independent work. No installed, D3, credential-custody or full-provider gate is
closed, and the manually confirmed app action alone is not refresh proof.

Revocation follow-up (2026-11-05): the dedicated native-check-only
`--check-provider-revocation-cleanup` invocation verified the sole fixture was
`RefreshPending`, refused access, withdrew it through the existing custody owner,
and refused its captured capability. It exited 0 in the real isolated
Tauri/WebKit/keyring/runtime journey. No refresh was replayed and no uncertainty
marker was cleared to recover a token. The failed attempt is preserved under
`/home/vince/tmp/factory-github-jobs-1105/revocation-attempt1/`; the safe
`provider-revocation-cleanup.json` in its parent has SHA-256
`dacc7ca5aa3156be0d86a28d27124c78c88834ddb4c6bc57985dca9ee00f8ffa`.
The owned revoked fixture is now removed; other profiles and historical evidence
remain intact. This supersedes the preceding retained-account cleanup blocker,
not the unverified refresh outcome.

GitHub's documented `incorrect_client_credentials` OAuth error now has a bounded
typed native refresh outcome, distinct from an unconfirmed exchange and from
`bad_refresh_token`. No upstream description, raw response or secret is retained.
The existing native operation check gained `--github-refresh-refusal` to run its
actual-provider negative phase independently of the obsolete Go observer. Its
real Rust/Go/PostgreSQL/private-keyring run exited 0 under
`/home/vince/tmp/factory-github-refresh-refusal-1105/`: generated invalid fixture,
client rejection, durable pending custody, second exchange refused before
network, then normal withdrawal. No real grant was used and this does not
identify the previous failed exchange. Unknown/provider/network outcomes keep
their uncertainty behavior.

The corrected revocation observer writes access/WebKit evidence and a bounded
refresh outcome before asserting. A fresh isolated connected check is awaiting
owner consent under `/home/vince/tmp/factory-github-revocation-v2-1105/`.
Closing the remaining remote qualification requires its actual owner-approved
grant revocation, recorded provider refusal and local cleanup. A client rejection
must remain labelled as client rejection; it does not establish a
`bad_refresh_token` response. T06-GITHUB also still requires migration/retirement
of the historical Go-observer check consumers. Installed and D1/D2/D3/D5 gates
remain open with their existing dependencies.

Corrected actual revocation check (2026-11-05, passed for this obligation):
`/home/vince/tmp/factory-github-revocation-v2-1105/` exited 0 after fresh owner
consent, positive native/canonical/shared-UI/private/paging/refresh checks and the
owner's second explicit app-grant revocation. Actual identity returned 401;
canonical identity/catalogue/jobs and WebKit reconnect refused. The subsequent
refresh returned the typed `client_credentials_rejected` outcome. It did not
return `bad_refresh_token`, and the safe evidence explicitly records that false.
The failed rotation remained durably pending until normal withdrawal removed
this owned local account; no refresh was replayed. Owned PostgreSQL shut down.

Safe evidence hashes in that root:
- `result.log`: `265846cde278dc715464115e4848ffb6789272b93ac3ea163dc58dc35a12db74`
- `provider-revocation-access-result.json`: `d54f016e022b35880889ac7e2c4ad96f8d6f60eaf495444012f290a9e897ee44`
- `provider-revocation-refresh-result.json`: `5811af33a692bbf8d89627f0e3919448683482a4c17f3b6eaf869a64bdf8c830`
- `provider-revocation-result.json`: `93b8c3ee4768f20757c4550221c2540c87ae7b0f1df24a96c58b430b72589aa6`
- `consent-result.json`: `2465bdf381315763a2c9dd94224bd50461631058823d2c000cf3a5b9b4a6a80b`

This supersedes the pending fresh remote-revocation obligation above. The first
attempt remains failed and its original refresh class remains unknown. It is
not retroactively repaired by this result. T06-GITHUB remains Active for the
historical observer migration inventory in the canonical plan. In particular,
current Rust OAuth malformed-response, cancellation/rotation/withdrawal and
retirement obligations still need equivalent declared HTTPS evidence before
retiring the Go-observer consumers in
`tools/native-checks/src/native_github_transport.rs` and
`cmd/soda-desktop/src/check_credentials.rs`. No installed, D1/D2/D3/D5 or executor
gate is closed by this connected provider check.
