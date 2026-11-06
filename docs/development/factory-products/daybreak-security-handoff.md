# Open security gates and Daybreak handoff

Updated 2026-11-06. This is an evidence register, not a completion ledger. The
canonical order and statuses remain in
[implementation-tasks.md](implementation-tasks.md); the selected ownership remains
in [language-allocation.md](language-allocation.md). All gates below are open.
Normal coding agents can implement and verify routine product security work,
including these gates and ordinary checks. Use a user-started Daybreak session
only when work or a check looks like hacking; Daybreak has the anti-hacking audit
for that work. Do not infer a passed gate from a proposed design, a source build, a
fixture, or an earlier component probe. Do not place credentials or private
profile contents in this file.

Untangling update (2026-11-06): nonsecret execution DTOs/bounds now belong to
`lib/execution-protocol`; private filesystem and parent-death helpers belong to
`lib/desktop-runtime/src/{private_fs,child_process}.rs`. Independent boundary
checks remain in the worker, broker and desktop. Structural reviews/builds and
dummy-provider edit/inline journeys do not qualify installed isolation, admission,
credential custody or any open gate below. Blockers and closure evidence remain
as recorded here; refactor scope/results live in [the decision log](../untangle-codebase.md).

Canonical decision scope: D1 gates standing GitHub automation, D2 supported
Fountain desktop enrollment/revocation, D3 isolated native worker execution,
D4 packaging/diff selection and D5 Fountain issue-only collaborator admission.
See the decision table in [ideal-filetree-plan.md](ideal-filetree-plan.md).
GitHub mutation target/head/body/concurrent-edit limits belong to their concrete
T11 operations. Earlier checkpoint labels that called those limits D5 have been
corrected; they never established a new canonical decision or a general stop on
independent reads, writing UI or connector development. Preserve truthful API
limits and explicit unavailable cases without claiming stronger atomicity.

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

Refresh-observer migration finding (2026-11-05, verification pending):
`github_provider/transport.rs::Client::oauth` previously returned bodies for all
HTTP 200–499 statuses to the grant decoder. Redirect following was disabled, but
a complete grant-shaped redirect/error body could still enter identity
verification. The current change permits success bodies only on 200; HTTP
400–499 must contain a bounded JSON OAuth error, and redirects refuse. Relevant
files are `lib/desktop-runtime/src/github_provider/{transport,oauth}.rs` and the
existing `tools/native-checks/src/native_github_transport.rs` refresh integration.
The declared loopback-only fixture under
`/home/vince/tmp/factory-github-oauth-declared-1105/` now supplies full dummy grant
bodies on redirect/403 plus a documented OAuth error on 400. Required closure
evidence: production Rust refuses both grant-shaped non-success bodies before
identity I/O, retains the pending fence, makes only one exchange and refuses
replay; known structured HTTP error still classifies correctly. No real grant
is used. This proposed fix is not yet recorded as verified; broad custody,
installed and other product gates remain open.

OAuth status finding closure evidence (2026-11-05, bounded source check passed):
The production Rust refresh integration exited 0 under
`/home/vince/tmp/factory-github-oauth-declared-1105/attempt3/`. Complete dummy grant
bodies on redirect and HTTP 403 refused before identity I/O, retained pending
custody, made one exchange and blocked replay; a structured `bad_refresh_token`
error on HTTP 400 still produced the known refusal. The fixture observed no
unexpected request and no followed redirect. This verifies the preceding
proposed status-handling fix for these paths. Unknown/malformed exchange
outcomes remain fenced. Successful JSON rotation/reload, caller cancellation,
withdrawal/replacement and joined retirement also passed in the same real
private keyring/runtime journey, without any real grant.

Safe result SHA-256
`602c090fa360b339ef9d4af50b8093beebb2ffeae3f0791710389a011c0c13f4`,
log `b22340bd5e9d0068b348d5a66ccd57fdfdb085016cd3b53f1948498f5a04ec86`,
fixture source `7024f2813079a31badd0436aa6f42a477afea21ae409a3876a7ec879a6be93b0`.
Earlier fixture attempts/source are preserved. The historical Go refresh
observer is retired in favor of `--github-refresh-refusal` (actual invalid
noncredential) and `--github-refresh-declared` (owned dummy HTTPS fixture).
These bounded current-source results do not qualify device/verifier/transport
or declared WebKit obligations not yet migrated. T06-GITHUB stays Active for
those exact remaining consumers in the canonical inventory. D1/D2/D3/D5 and
installed native admission/custody/isolation/executor evidence remain open;
the register's dependencies and required installed journeys still apply.

Device/grant migration evidence (2026-11-05): current production Rust declared
device → fresh identity → complete encrypted grant reload/withdrawal passed in
`/home/vince/tmp/factory-github-oauth-declared-1105/grant-attempt1/` (result
`5dd2f8b9fa3474adb018b84c4bd01eba373844f9f748d1369f659797b1061911`).
The dummy-only HTTPS device suite passed under `device-attempt2/` in that root
(result `486b753623c1b846006ace99b33011f787bb77b807c31595472e42813a6ee2fd`):
expiring/long-lived grants, pending/slow-down, refusal/expiry/deadline, malformed
token/URI/size/redirect, upstream start/wait cancellation and retired
authorization with no network or stored account. Three polls took 14012 ms.
The earlier failed fixture interval expectation is preserved and explained in
the canonical plan; no production polling change was needed.

Independent actual GitHub unapproved-device pending/cancellation/retired-start
check passed with empty custody under
`/home/vince/tmp/factory-github-device-pending-1105/` (result
`4863f547ff528bea0c56b669d6117904f4745187703da26a63d1d399062be5f4`).
These replace the old Go grant/device observers; they do not constitute fresh
owner consent or installed qualification. Relevant existing check files remain
`tools/native-checks/src/{native_github_transport.rs,bin/factory-credential-operation-check.rs}`.
Exact remaining T06-GITHUB blocker: migrate verifier/transport and declared
WebKit obligations from the canonical inventory before closing that provider
gate. The broader installed admission/custody/isolation, D1/D2/D3/D5 and executor
gates retain their separate required journeys and dependencies.

Verifier/transport migration follow-up (2026-11-05): all headless historical
Go-observer consumers are now retired with current Rust replacements and real
private keyring/Go/PG evidence. The actual generated-invalid-token verifier and
transport modes each received actual GitHub 401, kept Factory unbound, refused
retired work, and left custody empty/withdrew the owned fixture respectively.
The independent dummy-only HTTPS modes proved refusal before custody, restart
profile/instance behavior, invalid/foreign-provider scope refusal before network,
independent keyring replacement/withdrawal suppressing pending results, caller
cancellation and joined retirement/cleanup. They do not prove a late response
was delivered or an installed hostile-context boundary. Source stays in the
existing `native_github_transport.rs` and CLI; no new test root or Go connector.

Private safe-result hashes:
- `factory-github-oauth-declared-1105/verify-attempt1/`: `265c9c0d2fe09a01904c5410d87feaa597bd67fbde554e0ecd5414eef78b8248`
- `factory-github-oauth-declared-1105/transport-attempt1/`: `7240139376c8f3ed6969436496c94fbccee6ac2d62d35953a06c7da721b6838e`
- `factory-github-verify-refusal-1105/`: `4587282f65dd69f35da9fe1c4fe1292bf67aad67fc2803e9d32390ab4ae40fc3`
- `factory-github-transport-refusal-1105/`: `23c99b790fd1b8d25fa3b54af5d5a8cfc4e43ad099881e445821ceba945e2b6d`

All roots are beneath `/home/vince/tmp/`; exact log hashes are in the canonical
plan. All four exited 0. Exact remaining T06-GITHUB blocker is now the declared
WebKit migration in `cmd/soda-desktop/src/check_credentials.rs`: preserve UI
refusal/cancellation plus canonical mapping, large IDs, nullable records/paging,
restart and withdrawal obligations with the current Rust HTTPS producer before
retiring its old `io/mode` Go observer assumptions. D1/D2/D3/D5 and installed
isolation/admission/custody/executor gates retain their distinct dependencies
and closure evidence; these bounded probes do not close them.

Declared WebKit migration and provider-slice closure (2026-11-05): the remaining
historical observer now uses the current production Rust HTTPS producer and
actual isolated Tauri/WebKit/keyring/Go/PG. It exited 0 under
`/home/vince/tmp/factory-gh-ui-1105/a2/`, including UI refusal/cancellation,
foreign profile/repository refusal, large canonical IDs, nullable/filtered issue
pages, stable rename/restart mappings, withdrawal and retired sign-in. A
declared xdg-open receiver observed only the fixed native verification URL;
this run claims no real browser consent. Both provider-refusal groups now
explicitly record `actual_github_refusals:false`. Actual consent and remote
revocation remain the distinct owner-approved connected proof above.

Safe log `225df68c6ccf81f892c037d0464b336af9d1e1ad68b7e8b1f30cbf5daf9fafd7`,
binding result `d70d6b94cca156ed648ea0be619687d1c1a600f93f07b9a40b881e31103b177e`,
issue result `52e00b207fe354667e83b873ebb52d25d7279906738f5d494e34d33aeceda088`.
Retained-mapping and fixture source hashes are in the canonical plan. Attempt1's
fixture schema-opening race is preserved; waiting for native readiness fixed
the harness without a production schema change. Fixture foreign-repository
input uses existing Go Store transactions only in the owned private database.

T06-GITHUB is Complete for its connected source finish condition; all historical
Go-observer consumer migrations and actual remote-revocation obligations are
satisfied. This supersedes the preceding T06-GITHUB blockers, not any broader
gate. Exact remaining security/resource gates are the installed native
peer/window/instance/account/resource admission and the registered D1/D2/D3/D5
and executor/custody/isolation journeys. Their dependency rows and evidence
requirements earlier in this handoff still apply. This is not installed or
fresh-release qualification, worker isolation, secret-delivery qualification,
credential-root absence, finite paid-provider work or durable execution
settlement. These tasks remain normal engineering; only particular work that
looks like hacking needs the owner-started Daybreak session. Next product row
is T07-READ; the overall product goal remains active and incomplete.

T07-READ desktop navigation checkpoint (2026-11-05): typed issue fragments now
stay in the selected canonical repository's shared issue view. Fragment-only
history preserves the mounted issue and conversation page; host scrolling looks
only inside that current issue DOM and its observer is retired on selection
change/disconnect. Other-repository/provider destinations retain the existing
native external-link admission. Files: `frontend/{desktop/product.ts,
desktop/connections.ts,factory/app/routes.ts}` and the opt-in existing native
check `--check-signin --check-issue-read` in `cmd/soda-desktop/src/`.

The actual native Tauri/WebKit/private-keyring/Rust/Go/PG journey passed under
`/home/vince/tmp/factory-gh-read-1105/a2/` with a dummy-only, no-forwarding HTTPS
producer. It checked sanitized Markdown DOM, explicit attachments/default-branch
links, ordinary conversation and paging, scoped heading/comment/issue routing,
view retention, Back/Forward/deep links and Back from Factory work. Primary read
denial removed the issue and optional Factory facts, and disconnect cancelled
pending Rust comment I/O and removed the shared screen. This is a bounded
connected source result, not a hostile installed-context or full admission gate.
Actual GitHub consent/revocation remains the separately recorded real journey.

T07-READ remains Active for actual Fountain shell composition and its complete
supported-host read finish condition. Installed peer/window/instance/account/
resource admission, D1/D2/D3/D5 and executor/custody/isolation journeys still
require the exact evidence registered above; this navigation check closes none
of those broader gates. Attempt1's missing issue-10 fixture detail is preserved;
the provider fixture was corrected without a product fallback. No new authority
or security bypass was introduced.

T07-READ actual Fountain browser checkpoint (2026-11-05): opt-in
`factory-fountain-host-check --issues-browser` now joins the real native host,
installed local package, appliance-composed Factory and private PostgreSQL to
an actual Chromium reader session. Native login and real provider reads render
the shared list/detail/conversation/Markdown/Factory section; actual owner REST
collaborator deletion makes refresh remove issue content and optional Factory
facts/controls. No synthetic API or admission header, retained account or remote
repository is used. Files: existing native host CLI plus
`tools/native-checks/src/fountain_issue_browser.{rs,mjs}` in that same integration
root. This is routine owned product checking, not Daybreak work.

Passed host root `/home/vince/tmp/ff-read-host-1105-a3/`; host result hash
`c97aab8a43ed678ef9420b22dc17cc57f2870a5a494743f1241d8c864666ce0e`,
browser result `b1eb0c75989a2571e08c2944bee1e7bb0280012fd6dcf7799cc81892efe2aff7`.
The existing native HTTP read/private-reader/rename/revocation checks and native
generation/logout refusal also passed; service children and owned PG stopped.
Full source/binary/log/image evidence is recorded in the canonical plan.
Attempts1/2 preserve a fixture expectation error about repository-relative
Markdown links; product behavior was not relaxed for the passing assertion.

This does not close D5 issue-only collaborator admission: this reader held the
ordinary full repository read grant. Installed peer/window/instance/account/
resource admission, credential delivery/root absence, D1/D2/D3/D5 and executor/
isolation/settlement journeys retain their registered blockers and evidence
requirements. T07-READ still needs normal Feed → Issues navigation through the
shipped native presentation/header, beyond the passed stock-host extension
rendering. No fresh or installed release qualification is claimed.

T07-READ normal native-header closure (2026-11-05): attempt5 of the actual
Fountain browser/native check passed with the complete shipped presentation
generated by the existing production stage/locale/assets owners. The fresh
native host's custom-root contains those exact files, and the actual template
engine renders the Issues header. Feed → shared Issues, ordinary detail/
conversation, Markdown, fragment/history/Factory-section/reload and the loaded
empty recorded-facts state passed. Real local collaborator revocation removed
content and optional Factory facts/controls; generation/logout refusal and
service/PG shutdown passed. No header stub, synthetic provider/admission context
or retained account is used.

Host root `/home/vince/tmp/ff-read-host-1105-a5/`; result
`950edd6347403fb03a0dcd7cf99a58ee7acb99acd15f72d6054e2a8861d322e6`,
browser result `25d0e110375477633d8215b22d2a2b9053890fd98b68e13be945534cd7bb077e`,
log `3e1741abe5c4d974a4e2859cad760c68b07efdc059e82c1025b093e0a959dd49`.
Production presentation inventory hash
`103cce2b186a95bf2b9606b2756f28586edf5825e745153a73eaeb6a9e1b92f3`.
Source inputs, image and earlier attempt evidence are in the canonical plan.
Files remain the existing native host check and its `fountain_issue_browser.mjs`.

T07-READ's connected source finish condition is Complete, superseding its
preceding native-header blocker. This reader held full repository read; D5
issue-only collaborator admission is still unverified. Installed peer/window/
instance/account/resource admission, credential root absence/scoped delivery,
D1/D2/D3/D5, native executor isolation/retirement/settlement and real finite
provider execution retain their registered dependencies and required evidence.
Desktop Fountain and installed release/freshness/upgrade/parity remain pending.
The next product slice is T11-WRITES; none of these broader security gates is
closed by the read journey. The overall goal remains active and incomplete.

### T11-WRITES local dispatch fence checkpoint (2026-11-05)

T11-WRITES is Active. Ordinary product engineering continues under the corrected
Daybreak boundary. No hacking-like check was performed or delegated here.

Relevant files: `pkg/factory/command_dispatch.go`,
`pkg/store/factory_human_comment_dispatch.go`, `pkg/store/schema_command_dispatch.go`,
`pkg/store/schema.go`; upcoming consumers remain `pkg/factory/service/`,
`pkg/factory/runtime/`, the semantic native bridge and
`lib/desktop-runtime/src/github_provider/`.

Proved: the existing Go journal now durably reserves one comment dispatch for
an exact winning capture after a validated saved absence observation. Exactly
one of 32 concurrent callers across two Store owners won. Reservations cannot
reset after unknown/absence observations, restart or WAL recovery. Terminal
commands cannot acquire a reservation. A real schema-80 Store archive produced
the retained captures; schema 81 conservatively fences them `legacy_unknown`
and preserves their complete UTF-8 bodies, preparation and outcomes byte for
byte. Fresh-schema and upgrade checks both exited 0 through the existing native
PostgreSQL driver, with clean owned-child shutdown. Fixture source/executable
and log hashes are recorded in the canonical T11 checkpoint under
`/home/vince/tmp/factory-comment-dispatch-1105/`.

| Remaining task | Exact blocker/dependency | Unverified and evidence needed to close |
| --- | --- | --- |
| Current-account human comment admission and native effect authority | The reservation has no Rust provider consumer yet. Compose T11-WRITES with the existing native account/credential lifetime and Go current caller/canonical registry admission before enabling the action. | Prove the exact selected account, host, actor, repository and issue/pull binding; actual comment permission and lock/read-only refusal; account withdrawal, permission revocation, stale IPC/runtime generation and resource mismatch must refuse or retire the effect. Repository push permission alone is not comment admission evidence. |
| No duplicate or falsely attributed comment after timeout/cancellation/restart | The local reservation only prevents a second claim. Rust bounded dispatch, causal response capture and Go outcome reconciliation are not implemented by this checkpoint. | Prove a single authenticated POST without automatic retry; causally bound successful response/readback; uncertain transport or lost reply remains indeterminate and fenced; pre-dispatch cancellation differs from cancellation after a reserved/in-flight request; no absence/body search or user-writing marker is promoted to a receipt. |
| Retained/installed schema upgrade and release admission | Only fresh owned PostgreSQL and actual schema-80 fixture upgrade ran with captured source inputs. | Current release selection, full retained installed profile compatibility, failure/recovery and the installed product journeys remain their existing packaging/upgrade dependencies. This bounded schema probe does not close that gate. |

No real GitHub comment was posted, no credential was requested or exposed, and no
native mutation capability was enabled. The earlier owner-revoked test grant
remains removed and must not be replayed. Actual connected writes will require
a fresh authorized grant and a concretely scoped owned fixture. D1/D2/D3/D5,
credential root absence/scoped delivery, installed admission, executor isolation,
retirement/settlement and finite provider execution retain their previously
recorded blockers and evidence requirements. No broad gate or full product is
complete from this producer.

T11-WRITES Rust comment producer checkpoint (2026-11-05):
`lib/desktop-runtime/src/github_provider/comments.rs` and the fixed transport
operation now implement bounded no-retry issue/PR ordinary comment creation.
The existing native integration command's `--github-comment-declared` attempt2
passed 26 named cases, eight invalid descriptors, cloned-client serialization/
pacing and blocked-POST owner retirement. Evidence is under
`/home/vince/tmp/factory-github-comment-1105/`; result hash
`a67ff9de782b9a626e619899c6ec482e7ca4a49c3c9000f0aee0dce753ad2ca2`,
log `7c18b2d3058d0945f11eafd3a912fefd64b4fb0458a982d59a26ec8fb54bf63f`.
Attempt1's observer runtime-start failure remains preserved. The canonical plan
records the fixture/source/executable identities and exact scope.

What this proves: only dummy local HTTPS was used; current identity/immutable
subject, known unlocked and non-read-only checks refuse their declared conflicts
before a POST. The single fixed POST preserves writing. Redirects, refusal,
lost/malformed/misbound replies, size limits and timeouts do not become safe
retries or invented success. The operation serializes across native owner clones
and observes retirement with an eight-second overall bound. No repository push
fact is promoted to comment permission. Checks before POST are not atomic remote
revision or permission guards.

Remaining exact blocker: the Rust producer and Go durable reservation are not
yet composed through the admitted native credential/IPC operation and current
human-comment service. No product write capability is enabled. The preceding
table's current account/caller/repository/resource admission, withdrawal/stale
generation, causal receipt persistence, lost-reply/restart and cancellation
obligations remain unverified end to end. Positive fixture replies are declared
data; they are not actual GitHub writes or permission proof. Close the bounded
consumer only after its real native/Go/PG check and a freshly authorized, scoped
actual GitHub write/readback. All registered D1/D2/D3/D5, installed custody/
admission, native isolation/retirement/settlement, finite provider execution and
fresh release/upgrade gates retain their existing dependencies and evidence
requirements. No Daybreak invocation or broad completion is inferred here.

T11-WRITES atomic cancellation checkpoint (2026-11-05):
`pkg/store/factory_human_comment_dispatch.go` now shares reservation's row lock
with a local cancellation decision, and `factory_human_comment.go` retains one
observation-save owner. `pkg/factory/human_comment_outcomes.go` preserves a newly
proved complete creation receipt when a prior local cancellation remains
indeterminate, without mutating the caller's evidence or inventing cancellation
acknowledgment. Reserved and legacy-unknown commands cannot become pre-dispatch
no-effect tombstones through this helper. It applies only to adapters using the
one-shot protocol for every dispatch; ordinary current caller/account/namespace
admission is still required before calling it.

Fresh and actual schema-80 upgrade checks in
`/home/vince/tmp/factory-comment-cancel-1105/` exited 0 through restart, WAL recovery
and clean owned-child shutdown. Both sets of 32 concurrent two-owner races
exercised reservation and cancellation winners. Exact-capture conflict refusal,
terminal and prior-provider-record preservation, legacy uncertainty, idempotence
and late causal receipt retention passed with declared provider records. Log
hashes: upgrade `4af1ca2ffdb33595461b70ecf977b57dabb4b5921bfe47707e4d8b25be66ae9b`,
fresh `ba845f763c3c5a5a2b698f85f221b02b48da70ed8fd0ede7af2648dee5a2bcbc`.
The canonical plan retains fixture/source/executable hashes. This does not prove
native caller admission, actual wire cancellation or a real provider mutation.

Additional consumer blocker discovered: a full 64 KiB comment copied into its
success receipt can exceed the existing 64 KiB journal outcome/receipt limits.
Relevant files are `pkg/factory/types.go`, `pkg/collaboration/effect_outcomes.go`,
`pkg/store/schema.go`, `pkg/store/factory_human_comment.go` and the Rust comment
producer/upcoming native bridge. Before enabling the action, select bounded causal
receipt evidence or explicitly evolve its bounded retention contract, then prove
maximum-length Unicode/control-escaped writing retains both its exact capture
and attributable success through the real native/Go/PG path and restart. A body
hash or readback alone must not be promoted to causal provider write evidence.
No proposed solution is verified here. Credential/IPC/current-authority composition,
real scoped provider write/readback and all broader registered gates stay open.

T11-WRITES bounded causal receipt checkpoint (2026-11-05): the preceding
receipt-size blocker is resolved for the defining Rust producer and Go storage
path. `github_provider/{comments,transport}.rs` emit at most 4 KiB of provider
metadata/URLs, causal HTTP status, optional bounded unique request ID and actual
response/writing digests only after the received POST reply is validated against
the exact captured writing and scope. The original writing stays in the immutable
capture. A digest or readback alone is not accepted as a causal write receipt.
Post-dispatch malformed/oversized evidence remains indeterminate.

The extended existing native HTTPS check passed 31 named cases, including exact
65,536-byte Unicode and control-escaped bodies and malformed receipt/request-ID
boundaries. A Go acceptance command consumed its actual dummy-only HTTPS receipt
outputs and independently checked the fixture's original response-byte digest,
writing, scope and status before real Store persistence. Both maximum writing
captures and 1,565-byte complete outcomes, exact receipts and dispatch fences
survived retained PostgreSQL restart and WAL recovery. Clean shutdown passed.
Evidence roots: `/home/vince/tmp/factory-github-comment-receipt-1105/attempt1/`
and `/home/vince/tmp/factory-comment-receipt-store-1105/`. Result hash
`4fdb2822de8ed0d3c43768f4ce61c4ee53a7018316252f7aac4269ca9b3bdbc8`,
Store log `10d039c921c0331ea3db93f3e23124c69fae4b00a9271986acdb7840bf9f4308`.
The canonical plan records source/executable/receipt hashes. Prior evidence is
preserved unchanged; no actual provider request was forwarded by the fixture.

Exact remaining blocker: these producer/storage checks consume artifact receipts,
not the production admitted native IPC/service path. The current private request
bound is 8 KiB (`lib/desktop-runtime/src/collaboration.rs`,
`pkg/factory/runtime/native_collaboration.go`), so explicitly evolve that bounded
semantic channel to carry maximum human writing before enabling the action.
Compose current credential/account/repository/generation and Go caller/canonical
admission, exact capture-to-effect binding, reservation, causal receipt persistence
and concurrent cancel/lost-reply/restart handling in the real native/Go/PG path.
Only then qualify a freshly authorized and concretely scoped actual GitHub
write/readback. Native mutation capabilities remain unavailable. All broader
registered security, executor, installed release/freshness and upgrade gates
retain their dependencies and evidence requirements; none is complete here.

T11-WRITES outer comment envelope checkpoint (2026-11-05): additional fixed
64 KiB limits existed in the desktop decoder, joined native product I/O and Go
native server. The defining decoder now lives once in the headless runtime's
`product_request.rs`, re-exported by the desktop bridge. Exact issue/PR comment
submission routes accept at most a 512 KiB JSON envelope while the Go handler
retains its 64 KiB writing limit. Other routes and ambient native requests retain
their existing smaller limits. This is request-shape admission, not provider or
product authority.

The existing native check's `--github-comment-envelope-declared` attempt1 passed
through the shared desktop decoder, actual native supervisor/credential/product
owner, current Go backend and PostgreSQL with a dummy-only owned HTTPS provider.
Maximum Unicode/control writing reached the unavailable Go writer intact for
both issue and PR routes. Excess writing, excess envelope, unrelated/cancel/query
paths and ambient large requests refused; no provider POST occurred. The owned
grant was withdrawn and native/Go/PG resources stopped. The actual desktop
source build passed after the move. Evidence root
`/home/vince/tmp/factory-github-comment-envelope-1105/`; result
`c7375120c3ac3721ef2678f530f25aba12f3067013f8972b354de10e716aadb2`,
log `aa652f1ca72c795d5e71517494b3fa6c0d311f0c94e9c5614e9dbf7775d6fe72`.
Source/executable identities are in the canonical plan. No actual WebKit write,
real GitHub mutation or installed admission gate is qualified here.

The outer envelope blocker is resolved; the private Go-to-Rust callback's 8 KiB
bound and missing semantic writer remain the exact next composition blocker
(`collaboration.rs`, `native_collaboration.go`, `native_product.go` and the
upcoming human-comment writer). Native mutation capabilities stay unavailable.
Current account/caller/resource/generation/withdrawal admission, exact durable
intent-to-effect binding, outcome/cancellation/uncertainty/restart handling and
actual authorized provider write/readback still require the composed consumer
evidence. All previously registered broader security and installed/executor/
freshness/upgrade gates remain open.

T11-WRITES composition in progress (2026-11-05, not yet a qualified checkpoint):
the Go journal/service writer and scoped native callback are being connected in
`pkg/factory/runtime/native_product_comments.go`, `native_collaboration.go`,
`native_product.go`, `lib/desktop-runtime/src/collaboration.rs` and the Rust
comment producer. A first owned dummy-only composed REST attempt passed exact
writing/receipt, duplicate/lost-reply, concurrent cancellation, withdrawal/
replacement, another-account refusal and retained restart. It is preserved in
`/home/vince/tmp/factory-github-comment-product-1105/attempt1/`, result hash
`02209d00e1d826f36e9dd2c5c7cff9268c39d97960598b4dc0b13dc1cfafe136`.
This attempt does not qualify immutable mutation target admission or real GitHub.

An additional target-binding issue was found before committing the composition:
the REST POST addresses an owner/name after preflight, allowing a name change/
reuse to alter the addressed resource between the read and write. The producer
is being changed to GitHub's documented `addComment` mutation using the opaque
Node ID from the same checked immutable REST subject. That proposed cutover is
not yet verified. The native mutation capability projection remains unavailable.
Relevant files include `github_provider/{mod,request,comments,transport}.rs` and
the composed real native check `tools/native-checks/src/native_github_comments.rs`.

Before closing this bounded target obligation, prove the exact subject/actor node
bindings, no owner/name-addressed write, name-slot reuse, errors/partial GraphQL
replies, large database IDs, causal receipt persistence and the composed native/
Go/PG cancellation/lost-reply/restart path. Then obtain actual authorized provider
schema/write/readback evidence. Reference: GitHub's current
[addComment and AddCommentInput documentation](https://docs.github.com/en/graphql/reference/issues#addcomment).
Client mutation correlation is not a documented deduplication guarantee.

Separate remaining T11 mutation-policy question: neither the inspected REST comment API nor
`AddCommentInput` exposes an expected repository/lock revision precondition. A
Node ID pins the logical subject; it must not be described as an atomic guarantee
that a transferable subject remains in its original repository or unlocked.
The exact blocker is the required selected-resource invariant versus provider
mutation semantics, including issue transfer during dispatch. Define the supported
policy and prove its permitted/refused transfer/lock behavior in an authorized
fixture before advertising any stronger atomic repository/lock guarantee. Do not
infer such a guarantee from preflight reads or a proposed Node-ID fix. No real
transfer/name-reuse experiment or broader D5 gate qualification has run here.

T11-WRITES current composed Node-ID checkpoint (2026-11-05): the preceding
composition and proposed cutover now pass their bounded declared source checks.
This supersedes the unverified cutover status above, not its open T11 mutation-policy
question or actual-provider/installed qualification requirements.

The Go native human-comment writer now uses the existing service and sole
PostgreSQL command journal, exact capture/digest and durable reservation. Rust's
private typed callback checks current native custody/repository/operation scope,
admits only one write per operation and retains bounded deadlines/retirement.
Only the comment semantic request gets a 512 KiB envelope; reads/MCP remain
8 KiB. Profile/account command authority survives same-account reconsent while
current credential generations are checked independently. Captured deadlines
are retained rather than refreshed on replay. No provider credential crosses Go.

The fixed GraphQL mutation receives the opaque Node ID from the same checked
REST subject. Its actual reply must bind that subject and the current actor's
Node IDs, unchanged writing and provider-returned comment identity. Full/partial
GraphQL errors or uncertain/misbound evidence never become success or safe retry.
The bounded receipt retains protocol/status, original provider metadata/Node IDs,
request ID and actual response/writing digests; no search/marker/readback proves
an attributable write. Other provider operations and the shared write capability
projection remain unfinished/unavailable.

Evidence under `/home/vince/tmp/factory-github-comment-graphql-1105/`:
`producer-a1` passed 39 named cases, eight invalid descriptors, serialization/
pacing and retirement, including large IDs, name-slot reuse and partial GraphQL
errors. `product-a1` passed the actual native credential/product/callback, current
Go service/registry/journal and PostgreSQL path: maximum writing, normal readback,
simultaneous replay, wrong repository, lost reply without fabricated attribution,
cancel racing both success and a no-POST refusal, withdrawal/replacement, retained
restart and another account's refusal. Nine intended dummy fixture mutations ran;
the owned accounts and child resources cleaned up. Neither fixture forwards.
Producer result `47a8f8009cd6f66cfe282ba6d1c2c9415944018f26cec256118b6bc8c7ba5ef6`;
composed result `cd912b1077358cfb2fe24b360fe6497ff0e06dc1b536e182271a9328cc4d6a5b`.
Source/executable/log hashes are in the canonical plan. The earlier REST attempt
is preserved as historical evidence. The envelope-only observer explicitly
refuses with replacement guidance; it does not silently start posting comments.

Still unverified: real provider schema/grant/mutation/readback, actual shared
WebKit writing, delayed captured-deadline expiry boundaries and all broader
installed credential/caller/window/instance/resource admission. The first live
write check needs fresh owner authorization and a specifically scoped owned
repository/subject; the earlier revoked account is absent. No app secret or old
refresh pair should be requested/replayed to work around this dependency.

The T11 comment transfer/lock question remains a policy/provider dependency:
preflight checks plus a logical Node ID are not an atomic guarantee that a
movable subject stays in its original repository or unlocked during mutation.
Define the supported invariant and qualify its permitted/refused behavior in an
authorized fixture before advertising a stronger guarantee. Node-addressed
name-slot refusal proof does not close that obligation. All registered D1/D2/D3/
D5, worker isolation, root absence/scoped delivery, installed admission, native
execution/retirement/settlement, finite provider execution and release/freshness/
upgrade gates remain open. No Daybreak access/session or broad completion is
inferred from these ordinary owned product checks.

T11-WRITES issue-creation journal producer (2026-11-05): schema 82 and the shared
Store row-lock implementation now support typed human issue creation as well as
comments. Files: `pkg/store/factory_command_dispatch.go`, the two typed dispatch
wrappers, `factory_human_issue_create.go`, `schema{,_command_dispatch}.go` and the
typed human creation outcome reducers. An actual schema-81 archive produced
retained captures. The upgrade fences old issue history unknown while preserving
all command/writing/outcome bytes and existing comment state, with triggers
active. Replayed stale upgrade DDL does not fence newly captured schema-82 work.

Owned fresh and upgraded PostgreSQL acceptance passed exact-capture conflicts,
32 concurrent claims with one winner, 32 dispatch/cancel races per fixture, late
committed/no-effect evidence, concurrent process opens, restart and WAL recovery.
The first upgrade fixture exceeded its connection budget; it is preserved and
the corrected fixture closes its SQL inspector before stress without changing
production limits. Evidence root `/home/vince/tmp/factory-issue-dispatch-1105/`;
fresh log `0a634e833e7710be84abee336f61ad14b3f5d8a448a636e8d04d07156fbde5ec`,
upgrade log `72acf78ddbafcd709ef6556a85726c390783d937883acc5c3378e37f55cdc207`.
The canonical plan records the exact fixture variants/source/executable hashes.
The existing composed native comment journey also passed with schema 82 under
`/home/vince/tmp/factory-comment-schema82-1105/attempt1/`, preserving its prior
result scope; no broader security gate is newly qualified.

Exact next issue-creation dependencies remain the complete Rust fixed provider
effect and its admitted native/Go writer. Before enabling creation, prove current
account/repository/credential/generation admission, immutable repository targeting,
bounded unchanged title/body and causal receipt, one-shot reservation, lost reply/
cancel/restart and readable destination behavior in that composed path, followed
by a freshly authorized real provider fixture. Store-only declared records are
not provider permission or successful creation evidence. Installed retained
upgrade/freshness, the comment transfer/lock policy, D1/D2/D3/D5, root absence/
scoped delivery, installed IPC/window admission, worker isolation/execution and
retirement/settlement retain their recorded dependencies and required evidence.

T11-WRITES Rust issue-creation producer (2026-11-05):
`github_provider/issue_create.rs` adds a fixed repository-Node-ID mutation,
serialized with other native creations. `writes.rs` supplies one common outcome;
`transport.rs` keeps only private fixed mutation builders and shared bounded
authentication/response decoding. The effect checks current account, selected
immutable repository, known issues-enabled/non-read-only state and exact title/
body, and validates causal reply Node identities. Errors after dispatch retain
uncertainty; no remote deduplication, marker/search attribution, provider grant
or product SQL authority is inferred. Bounded receipts preserve writing digests
while the immutable Go capture retains the complete title/body.

The existing native integration tool's `--github-issue-declared` passed 37 named
cases, eight invalid descriptors, serialization/pacing and in-flight retirement
against dummy-only local HTTPS under `/home/vince/tmp/factory-issue-provider-1105/`.
The existing 39-case comment producer also passed after transport sharing. Issue
result `bf9f2cf6c8eb5cf7a2e3787b9ddd9e10c5263eb12f31bb6c51f9811d2ad5e0dd`;
comment result `47a8f8009cd6f66cfe282ba6d1c2c9415944018f26cec256118b6bc8c7ba5ef6`.
Source/executable/log identities are in the canonical plan. The fixture never
forwards and no actual provider account/grant was used.

At this producer checkpoint the issue-create blocker was the admitted native
callback and Go writer composition with the new journal protocol. The composed
checkpoint below supersedes that source blocker. A fresh,
specifically authorized real provider fixture must qualify actual schema/grants/
permission/write/readback afterward. The effect's source checks do not close
D1/D2/D3/D5, repository/visibility policy, installed IPC/window/custody, worker
isolation/execution/retirement, root absence/scoped delivery or release/upgrade/
freshness gates. Those retain their previously recorded evidence requirements.

T11-WRITES composed native issue creation (2026-11-05):
`pkg/factory/runtime/native_product_issue_creates.go` now owns the Go service
adapter; `native_product_creations.go` shares receipt metadata/account authority
with comments. `lib/desktop-runtime/src/collaboration.rs` admits the semantic
issue callback, retaining native credential/generation/repository checks. The
exact public issue-create route and private creation callback allow bounded
maximum writing. No product SQL or authenticated provider owner moved languages.

The actual native credential/Go/PostgreSQL composition passed 13 declared cases
and ten dummy mutations under `/home/vince/tmp/factory-issue-product-1105/attempt1/`:
exact/maximum writing, causal receipt and normal immutable destination readback,
hidden/changed destination without losing the committed primary effect, concurrent
one-shot commands, wrong repository, ambiguous reply without search attribution,
both cancellation races, withdrawal/replacement, same-account retained restart and
other-account refusal. The composed comment regression also passed. The fixture
is local, dummy-only and does not forward; all owned accounts/resources cleaned
up. Result `c1a48784d4a2f87e9a6caa25f5feabcf6972c4cd274b73b32b1cbe8b87d0f4f6`;
comment result `cd912b1077358cfb2fe24b360fe6497ff0e06dc1b536e182271a9328cc4d6a5b`.
The canonical plan records source, executable and log identities. These checks
prove the described source composition with captured inputs, not a real provider
grant/permission, installed caller/window boundary or broad custody gate.

Remaining blockers/evidence: the shared write capability remains unavailable;
before exposing it, qualify actual GitHub schema, fresh granted permissions, an
explicitly scoped owned mutation and normal readback, shared WebKit writing and
captured-deadline expiry. No retained live grant remains, so this needs fresh
owner-authorized consent and a concrete owned repository/subject. Repository/
visibility and comment transfer/lock policy remain independent provider/policy
dependencies. Installed IPC/window/instance/custody, D1/D2/D3/D5, worker isolation/
execution/retirement/settlement, root absence/scoped delivery and release/upgrade/
freshness gates retain their exact earlier blockers and evidence requirements.
No broad gate or Daybreak work is marked complete by this checkpoint.

T11-WRITES direct close/reopen producer (2026-11-05):
`lib/desktop-runtime/src/github_provider/states.rs` supplies four fixed issue/PR
Node-ID mutations through the existing authenticated transport. It checks current
account, immutable subject/repository binding and known read-only/merged state,
keeps all post-dispatch failures uncertain, and validates the bounded causal
state receipt. This does not attribute a timeline event, grant retry authority,
merge/synchronize a PR or establish an atomic revision/transfer condition.

The existing `--github-state-declared` native check passed 43 named cases, seven
invalid descriptors, held-response clone serialization/pacing and retirement in
`/home/vince/tmp/factory-state-provider-1105/attempt3/`. Earlier fixture failures
are preserved and explained in the canonical plan. The existing issue-creation
producer also passed after the transport extension. State result
`c0db2e4c1e3dc58b849dbc1c30a45e5b560a2dd97ee545d67ea680bc6887ae0a`;
source/executable/log hashes are in the plan. This was dummy-only loopback HTTPS
with no forwarding or actual GitHub grant. It proves only the declared producer.

Exact remaining state-operation dependencies: extend the single Go command
journal's one-shot reservation and atomic cancellation to the existing typed
state capture, then compose current native callback/account/repository/generation
admission and prove causal persistence, replay, cancellation races, uncertainty,
withdrawal and restart. Before enabling the shared action, qualify freshly
authorized actual provider permission/mutation/readback and shared WebKit behavior.
The movable-issue repository/visibility invariant needs the same explicit policy
and permitted/refused evidence as comments; reply validation cannot retroactively
prevent an effect after a transfer race. Installed caller/window/custody, D1/D2/
D3/D5, worker isolation/execution/retirement, root absence/scoped delivery and
release/upgrade/freshness gates retain all earlier blockers. No broad gate closes.

T11-WRITES state journal producer (2026-11-05): schema 83 extends the same shared
Store reservation/cancel protocol to `HumanStateCapture`. Relevant files:
`pkg/store/{factory_command_dispatch,factory_human_state_dispatch,factory_human_state,schema,schema_command_dispatch}.go`
and `pkg/factory/human_state_outcomes.go`. The actual 82→83 migration fences old
unknown state dispatch without rewriting captures/outcomes or releasing existing
comment/issue claims. It keeps triggers active and protects new captures against
stale concurrent migration replay.

Fresh and actual schema-82 upgraded PostgreSQL fixtures passed exact capture and
lookup refusals, one of 32 claims, both cancellation race orders, late known
primary results, immutable SQL/terminal outcomes, concurrent openers, retained
restart and owned-child WAL recovery. Evidence root
`/home/vince/tmp/factory-state-dispatch-1105/`; fresh log
`9fa320111f394d7d362fb4da188cc73f1cfd579dd3866e6520d6ecd10de05f39`, upgrade
log `5e67e217e3be4adf6c9b61029763f3019393862d4cfde0f2eb0c56776d69daae`.
Exact source/executable hashes and the historical console-label discrepancy are
in the canonical plan. These are declared Store observations, not actual GitHub
state changes or installed retained-upgrade qualification.

The composed issue-creation regression also passed with schema 83 in
`issue-product-regression-a1`, result
`c1a48784d4a2f87e9a6caa25f5feabcf6972c4cd274b73b32b1cbe8b87d0f4f6`.
It retained its existing 13-case/ten-dummy-mutation scope and cleaned up its owned
accounts/resources; no actual GitHub or broader admission gate was qualified.

Remaining state blocker: admit the native callback and Go writer against these
typed fences, then prove current scope, exact causal persistence, replay/cancel/
uncertainty/withdrawal/restart through the composed native/Go/PostgreSQL path.
Fresh actual provider permission/mutation/readback, shared UI and the repository/
visibility/transfer policy remain unverified prerequisites to exposing the action.
All broader custody, root absence/scoped delivery, installed IPC/window admission,
D1/D2/D3/D5, isolation/executor/retirement and release/freshness/upgrade dependencies
remain as recorded; this producer closes none of those gates.

T11-WRITES composed native close/reopen (2026-11-05):
`pkg/factory/runtime/native_product_states.go` supplies the Go service adapter;
`native_product.go` binds it and `lib/desktop-runtime/src/collaboration.rs` admits
the bounded semantic state callback. The one journal/reservation, original
deadline, current account/repository/generation checks and one native effect per
operation are retained. State/read/MCP envelopes remain small. No credential
crosses Go, and the native provider has no product SQL authority.

The actual native custody/callback/Go/PostgreSQL path passed 12 declared cases and
nine intended dummy mutations in `/home/vince/tmp/factory-state-product-1105/attempt1/`:
all four close/reopen operations, normal state readback, exact command replay/
lookup/cancel/conflict, concurrent one-shot dispatch, wrong repository, lost reply
without current-state attribution, both cancellation races, withdrawal/replacement,
another account and retained restart. Owned resources cleaned up. State result
`5acf055a67953ae22175079ce0c363a5df1d2345ac8cbd4142f3e005b4fc2da0`.
The existing composed issue-creation regression also passed, result
`c1a48784d4a2f87e9a6caa25f5feabcf6972c4cd274b73b32b1cbe8b87d0f4f6`.
Exact fixture/source/executable/log identities are in the canonical plan. The
local provider never forwards and uses only literal dummy grants.

This resolves the source composition blocker for states. Remaining action gates:
fresh specifically authorized actual GitHub schema/grants/permissions/mutation
and normal readback, shared WebKit actions and delayed captured-deadline expiry.
Shared write capability projection remains unavailable. Repository/visibility/
transfer policy is still independent: a Node ID and checked causal reply cannot
prevent a movable issue's effect after a transfer race. Define the supported
invariant and qualify permitted/refused behavior before stronger claims. Installed
credential/root absence/scoped delivery, peer/window/instance admission, D1/D2/D3/D5,
worker isolation/execution/retirement/settlement and release/upgrade/freshness gates
retain their earlier exact dependencies and required evidence. No broad gate or
Daybreak work is marked complete by these ordinary declared product checks.

T11-WRITES issue/PR edit producer (2026-11-05):
`lib/desktop-runtime/src/github_provider/edits.rs` adds fixed partial title/body
updates. `transport.rs` constructs only the requested fields and immutable Node
selector; `writes.rs` shares the update outcome with states. It verifies account,
immutable subject/repository scope and original title/body digest in preflight,
then requires an exact bounded causal reply including unchanged unselected content.
Failures after dispatch remain uncertain and cannot authorize a second send.

The existing `--github-edit-declared` native observer passed 50 named cases and
13 invalid descriptors in `/home/vince/tmp/factory-edit-provider-1105/attempt2/`.
These include both partial field shapes, empty/maximum writing, changed original
content refusal, changed unselected reply fields, deadlines, uncertainty,
serialization and retirement. Attempt1's passing 48-case source/executable is
preserved. The defining state regression also passed. Final edit result
`2565abb72384fd3263e3590b11d99cc908a52f24f5de9fd07b53fb789f51973a`;
source/executable/log hashes are in the canonical plan. All traffic used a literal
dummy token and no-forwarding local peer, not actual GitHub or installed admission.

Exact remaining source dependency: compose typed Go edit reservation/cancellation,
captured expected/observed original content and the admitted native callback; prove
current profile/account/repository/generation, one-shot replay, stale-base refusal,
causal persistence, cancellation, withdrawal and retained restart through actual
native/Go/PostgreSQL. Fresh specifically authorized provider permission/schema/
normalization/mutation/readback and shared WebKit editing remain unverified.

New explicit policy/evidence obligation: the selected GitHub update mutation inputs
carry no original-content revision condition. The current original-content check
is a preflight; it cannot prevent a different edit or issue transfer racing POST.
Relevant files: `github_provider/edits.rs`, the future `native_product_edits.go`,
`pkg/factory/{human_edit,human_edit_outcomes}.go`, the shared editor and capability
projection. Establish the supported concurrent-edit and selected-resource invariant
before exposing stronger guarantees. Closing an atomic-protection requirement
would need a documented provider mechanism and authorized real race evidence;
the declared stale-base cases alone cannot close it. Repository/visibility/D5 and
all broader custody/root absence/scoped delivery, installed IPC/window/instance,
D1/D2/D3, isolation/execution/retirement and release/upgrade/freshness gates retain
their earlier blockers. No shared write capability or broad security gate closes.

T11-WRITES edit journal producer (2026-11-05): schema 84 now supports typed edit
captures in the shared reservation/cancel protocol. Files:
`pkg/store/{factory_command_dispatch,factory_human_edit_dispatch,factory_human_edit,schema,schema_command_dispatch}.go`
and `pkg/factory/human_edit_outcomes.go`. The defining captured-content comparison
also gates reservation after exact stored-identity validation. It cannot establish
a current remote revision or atomic provider condition. Late known results retain
cancellation uncertainty; original input evidence is not mutated.

Fresh and actual schema-83-upgraded PostgreSQL checks passed ten exact-capture
conflicts, missing/unprepared/unknown/unavailable refusal, already-stale dispatch
refusal, one of 32 claims, both cancellation race orders, late causal/no-effect
evidence, unchanged terminal/SQL records, concurrent process opens, stale migration
replay, retained restart and owned-child WAL recovery. Evidence root
`/home/vince/tmp/factory-edit-dispatch-1105/`; fresh log
`bfb8769baec86f9cff21839c8d3a917899390c9997474dd28ffdadbcce261c24`, upgrade
log `687aa7027fac62e15e1272fe76dc5f0b9aec79a1e1082fe2d7ba88f245648f63`.
Exact writing, field presence and expected/observed facts were retained. Fixture
source/executable identities are in the canonical plan. Declared Store receipts
are not actual provider or installed upgrade qualification.

The existing composed state regression also passed unchanged with schema 84 in
`state-regression-a1`, result
`5acf055a67953ae22175079ce0c363a5df1d2345ac8cbd4142f3e005b4fc2da0`.
Its 12 cases/nine dummy effects retain their prior declared scope, with owned
resources cleaned up. No actual provider or broader security gate is inferred.

The remaining edit source blocker is its admitted native callback and Go consumer
using these fences, followed by actual composed current scope, stale original
content, partial fields, causal persistence, replay/cancel/withdrawal/restart
evidence. Actual provider permission/schema/normalization/write/readback, shared
WebKit editing and the concurrent-content/repository-transfer invariant remain
unverified. All broader custody/root absence/scoped delivery, peer/window/instance,
D1/D2/D3/D5, isolated worker/executor/retirement and release/upgrade/freshness gates
retain their previously recorded blockers and evidence requirements.

T11-WRITES composed native issue/PR editing (2026-11-05):
`pkg/factory/runtime/native_product_edits.go` now supplies the Go service adapter;
`native_product.go` binds it and `lib/desktop-runtime/src/collaboration.rs` admits
the typed native edit callback. Public/private edit envelopes retain maximum
writing while other operation bounds stay unchanged. Exact original/expected/
observed content, optional field presence, original deadline, current account/
repository/generation and one-shot journal/native effect fences are retained.
No credential crosses Go or product SQL moves to Rust.

The actual native custody/callback/Go/PostgreSQL path passed 16 declared cases and
11 dummy mutations in `/home/vince/tmp/factory-edit-product-1105/attempt1/`:
full/partial/empty/maximum writing and normal readback, exact replay/cancel/conflict,
stale client base, provider base change after preparation, concurrent claims,
wrong repository, lost reply without matching-writing attribution, both cancellation
races, withdrawal/replacement, another account and retained restart. Owned accounts/
keyring/children cleaned up. Edit result
`8a9939e204e4d4ca3dc7f6413a2574d35f997a69d60b209d4e0cc94fefb596ba`.
The existing composed state regression also passed unchanged, result
`5acf055a67953ae22175079ce0c363a5df1d2345ac8cbd4142f3e005b4fc2da0`.
The canonical plan records source/executable/log identities. These are dummy-only,
no-forwarding HTTPS fixtures, not actual GitHub or installed admission evidence.

The source composition blocker for edits is resolved. Remaining action dependencies:
fresh specifically authorized actual GitHub permissions/schema/content normalization/
mutation and normal readback, shared WebKit editing, delayed captured-deadline
expiry and explicit concurrent-content/selected-resource policy. The preflight
does not establish atomic content or repository-transfer protection; the stronger
invariant still needs its documented provider mechanism and authorized evidence.
Shared write capabilities remain unavailable. Installed custody/root absence/
scoped delivery, peer/window/instance admission, D1/D2/D3/D5, worker isolation/
execution/retirement/settlement and release/upgrade/freshness retain all earlier
blockers. No broad security gate or Daybreak work closes from this checkpoint.

T11-WRITES direct lock/unlock producer (2026-11-05):
`lib/desktop-runtime/src/github_provider/locks.rs` and its private transport variant
implement one fixed issue/PR effect. Rust maps only exact documented reason values,
rejects unsupported text without provider I/O, verifies current account/subject/
repository and causal actor/state/reason evidence, and keeps post-dispatch failures
uncertain. Missing nullable reason is not proof of a cleared reason. There is no
implicit unlock/relock sequence, atomic transfer guard or remote retry grant.

The existing `--github-lock-declared` observer passed 52 cases, eight invalid
descriptors, four unsupported reasons, serialization/pacing and retirement under
`/home/vince/tmp/factory-lock-provider-1105/attempt1/`. Result
`78c75b5c2756c8376831164c00784404ba003cd07962ed5fc11e1d3a184c41bd`;
source/executable/log identities are in the canonical plan. This is direct native
producer evidence against a dummy-only no-forwarding local peer, not actual
provider permissions or composed/installed admission.

Exact remaining lock dependencies: extend the existing typed Go lock journal
reservation/cancellation, then connect the admitted native callback/Go writer.
Relevant consumers are `pkg/store/factory_human_lock.go`,
`pkg/factory/{human_lock,human_lock_outcomes}.go`, `pkg/factory/service/human_locks.go`
and the future `native_product_locks.go`. Prove supported/unsupported reason outcomes,
current caller/account/repository/generation, causal persistence, one-shot replay,
cancel races, withdrawal and restart through native/Go/PostgreSQL before claiming
that composition. Actual freshly authorized provider schema/permissions/reason
behavior/normal readback and shared UI remain unverified. The same selected-resource/
visibility/transfer policy and all broader D1/D2/D3/D5, custody/root absence/scoped
delivery, installed IPC/window/instance, worker isolation/execution/retirement and
release/upgrade/freshness dependencies remain open. No broad gate closes here.

T11-WRITES lock journal producer (2026-11-05): schema 85 extends the existing
shared reservation/cancel implementation to `HumanLockCapture`. Files:
`pkg/store/{factory_command_dispatch,factory_human_lock_dispatch,factory_human_lock,schema,schema_command_dispatch}.go`
and `pkg/factory/human_lock_outcomes.go`. The actual 84→85 migration retains exact
reasons/preparations/outcomes and fences old unknown dispatch history, with triggers
active and new captures protected from stale migration replay. Other command
claims are preserved. Late known results retain cancellation uncertainty.

Fresh and actual schema-84-upgraded PostgreSQL checks passed exact-capture/lookup
refusals, one of 32 claims, both cancellation race orders, late known results,
immutable SQL/terminal records, concurrent openers, stale migration replay,
retained restart and owned-child WAL recovery. Root
`/home/vince/tmp/factory-lock-dispatch-1105/`; fresh log
`16fb5237a086357b7d4b9192dbe280d44d7ef318b78810d7b423376b87d24b52`, upgrade
log `39bad270ce29d76c2b39d344e60c8e30ce2b2011eef39187b3d9522e8cc861e2`.
The initial fixture used an invalid unlock-with-reason conflict request; failed
profiles/source/executable/logs are preserved. Correcting that input required no
production validation change. The canonical plan records exact artifact identities.
The composed edit regression also passed unchanged with schema 85, result
`8a9939e204e4d4ca3dc7f6413a2574d35f997a69d60b209d4e0cc94fefb596ba`.
These are declared Store/provider observations, not actual GitHub/installed proof.

At this journal checkpoint the lock source blocker was the native callback and Go writer using
these fences, followed by composed current admission, supported/unsupported reason
outcomes, causal receipts, replay/cancel/withdrawal/restart. Actual fresh authorized
provider permissions/schema/reason behavior/normal readback and shared UI remain
unverified. Selected-resource/visibility/transfer policy, D1/D2/D3/D5, custody/root
absence/scoped delivery, installed peer/window/instance, isolation/execution/
retirement and release/upgrade/freshness retain their earlier dependencies and
required evidence. No broad security gate or Daybreak work is closed here.

T11-WRITES composed native lock/unlock (2026-11-05):
`pkg/factory/runtime/native_product_locks.go` supplies the admitted Go service
adapter; `native_product.go` binds it, and `lib/desktop-runtime/src/collaboration.rs`
admits the typed callback. Current account/repository/generation, exact reason,
original deadline and one-shot journal/native fences are retained. Rust owns reason
mapping and authenticated effects. Maximum escaped unsupported reasons reach a
known no-send outcome without provider-policy duplication in Go.

The first composed check found that normal PR readback dropped the provider's
lock boolean. `pkg/collaboration/records.go`, the shared TypeScript shape and
`native_product_reads.go` now retain nullable PR lock observations, with contract
notes in `records.md`. Unknown is not unlocked or permission. Failed attempt1's
profile/log/executables remain preserved; it applied three dummy mutations before
the missing readback field failed and is not passing evidence.

The actual native custody/callback/Go/PostgreSQL path passed 23 declared cases and
15 dummy POSTs under `/home/vince/tmp/factory-lock-product-1105/attempt2/`: both
issue/PR actions, all four/blank reasons, exact replay/cancel/conflict, explicit
unknown PR lock, unsupported/maximum reasons without POST, concurrent claims,
wrong repository, lost reply without current-state attribution, both cancellation
races, withdrawal/replacement, another account and retained restart. Successful
owned accounts/keyring/children cleaned up. Result
`3421e7dc98aafc96f8aa87194740e6fafd631d92b7d6cddd9e19bc793531f695`.
The final composed edit regression also passed, result
`8a9939e204e4d4ca3dc7f6413a2574d35f997a69d60b209d4e0cc94fefb596ba`.
Exact source/executable/log identities are in the canonical plan. These local
dummy-only HTTPS fixtures never forward and do not qualify actual provider or
installed admission. Other/older hosts' missing lock metadata remains unknown.

The lock source composition blocker is resolved. Remaining action dependencies:
fresh specifically authorized actual GitHub permission/schema/reason behavior/
mutation and normal readback, shared WebKit actions, delayed captured deadlines
and the selected-resource/visibility/transfer invariant. A Node ID and causal
reply are not an atomic repository condition. Shared write capabilities remain
unavailable. D1/D2/D3/D5, custody/root absence/scoped delivery, installed peer/window/
instance admission, worker isolation/execution/retirement/settlement and release/
upgrade/freshness retain earlier exact blockers and evidence requirements. No
broad security gate or Daybreak work is closed by this ordinary composed check.

T11-WRITES thread-identity dependency (2026-11-05):
`lib/desktop-runtime/src/github_provider/review_threads.rs` now produces genuine
GraphQL thread IDs and bounded scope-checked catalogue/detail observations. The
historical `github_provider/threads.rs` still supplies REST root-comment groups to
`Read::PullThreads`; a numeric root-comment ID is not a review-thread Node ID.
The exact source blocker is canonical read cutover in `request.rs`, `reads.rs`,
`pkg/factory/runtime/native_product_discussions.go` and cursor identity in
`native_product_observations.go`, followed by retiring the old grouping owner.
Old numeric selectors/cursors must refuse without heuristic remapping. Preserve
null author/review links and original coordinates separately from proven anchors.
Only after that dependency can journaled resolve/unresolve native effects and
canonical human selection be composed. Viewer capability booleans are provider
observations, not product authority.

The native reader passed 36 declared cases, eight invalid descriptors and owner
retirement through the actual native transport using isolated dummy-only HTTPS;
result `/home/vince/tmp/factory-review-threads-1105/attempt2/thread-producer-result.json`,
SHA-256 `5947fca7163a6f76457678a1f8c378caae5394346a3b7c49b3805a0a2b1cfed5`.
The initial oversized-response fixture expectation failed (expected 502, observed
correct 413) and is retained. Existing lock mutation transport regression passed.
Exact artifacts and limitations are recorded in the canonical plan. These prove
local decoding/bounds/refusal behavior, not actual GitHub schema, permission,
pagination consistency, canonical admission or installed UI qualification.

Closing this dependency requires actual canonical read mapping and retained
cursor refusal evidence, then exact account/repository/PR/thread selection,
original full/unknown anchors, one-shot reserve/replay/cancel/restart, current
custody and causal resolve/unresolve evidence. Fresh specifically authorized
GitHub and normal shared readback remain required; no real grant is retained.
Transfer/visibility policy, delayed deadlines, D1/D2/D3/D5, credential root absence/
scoped delivery, installed IPC/window/instance, isolation/execution/retirement/
settlement and release/upgrade/freshness retain their earlier blockers. No broad
gate was closed, and no hacking-like work or Daybreak session was performed.

T11-WRITES canonical thread read cutover (2026-11-05):
The preceding read-consumer blocker is now resolved in source.
`github_provider/reads.rs` uses `review_threads.rs`; the REST grouping owner was
removed. `request.rs` and `native_product_discussions.go` reject decimal root IDs.
`native_product_observations.go` now binds cursors to `pull_review_threads_v1`,
refusing the old kind and another thread's comment cursor. Go/shared records and
`collaboration_threads.go` preserve a nullable review link; no ID is invented.
Viewer booleans do not activate shared capabilities. Unknown original/current
anchors remain null, separately from recorded root coordinates.

Actual native custody/callback, Go canonical mapping/service/HTTP and owned
PostgreSQL passed 17 declared cases including retained restart, old-cursor
refusal, scope/count/partial-error refusals, null links, paging and disabled write
capabilities. Successful owned grant and children were cleaned up. Result
`/home/vince/tmp/factory-review-thread-product-1105/attempt3/thread-product-result.json`,
SHA-256 `27c0b8bb00e5c37c8699b6a0b542f1bd12ae6c0f0d7a5031efbc386ad158dc7f`.
Failed attempt1's strict rejection of an unused producer field and attempt2's
incorrect fixture expectation for the existing 502 error mapping remain preserved.
The canonical plan records artifact identities. These are local dummy-only,
no-forwarding observations; actual GitHub/installed/other-host full journeys remain
unverified.

Exact next blocker: Go `pull_request.review.thread.resolve` one-shot dispatch fencing in
`pkg/store/factory_command_dispatch.go` and migration/native checks, then a fixed
Rust resolve/unresolve effect in `github_provider` and its admitted callback in
`collaboration.rs`, the Go writer/selection in `pkg/factory/runtime` and shared
human action evidence. Closing requires full/unknown original-anchor bindings,
exact account/repository/PR/genuine-thread identity, reserve/replay/cancel/races/
restart and withdrawal, causal receipt/readback, plus fresh specifically scoped
owner-authorized actual provider checks. A matching resolution observation cannot
close lost-reply uncertainty. No real grant is retained. Delayed deadlines,
visibility/transfer policy, D1/D2/D3/D5, custody/root absence/scoped delivery,
installed IPC/window/instance admission, worker isolation/execution/retirement/
settlement and release/upgrade/freshness retain their earlier exact dependencies
and evidence requirements. No broad gate or Daybreak work is closed here.

T11-WRITES thread one-shot Store prerequisite (2026-11-05):
`pkg/store/factory_human_thread_dispatch.go` now delegates exact capture state,
reservation and cancellation to the existing shared row-lock protocol in
`factory_command_dispatch.go`. `factory_human_thread.go` shares observation saving
inside cancellation transactions. Schema 86 (`schema.go`, `schema_command_dispatch.go`)
fences prior schema-85 thread captures as legacy-unknown without rewriting their
intent, full/unknown anchors or outcomes; older comment/lock claims are preserved.
`pkg/factory/human_thread_outcomes.go` now retains late known primary results and
uncertain cancellation using the existing non-mutating helper. A reserved row is
never permission to recover lost dispatch ownership.

The first actual PostgreSQL runs exposed the missing schema-85 allow-list entry
and thread reducer's loss of late known results. Both were corrected; failed
profiles/logs/executable/source remain preserved. Final fresh and actual-85-upgrade
checks passed 19 capture conflicts, full/unknown anchor binding, unsuitable lookup
refusal, one of 32 concurrent claims, 32 cancellation races in each run, late
causal/no-effect results, immutable SQL guards, concurrent upgrade/stale DDL,
retained restart and owned-child WAL recovery/clean shutdown. Evidence root
`/home/vince/tmp/factory-thread-dispatch-1105/`, fresh log SHA-256
`5dddc12eb1815a3037f6d4d5926516456a2fa1cece43f08b7bcd5e54e48d7435`, upgrade
log `43046cf1b80025f5482221e9bbfb13ed01ca3e41ddac3e45b29937380598a878`.
The canonical plan records driver/baseline/executable identities. These are local
journal observations; no actual provider or installed gate is closed.

The Store source blocker from the preceding entry is resolved. Exact remaining
thread dependency: fixed Rust authenticated resolve/unresolve effects and causal
receipts in `github_provider`, admitted native callbacks in `collaboration.rs`,
then the Go writer/selection in `pkg/factory/runtime` using the one-shot fence.
Required closure evidence remains exact current account/repository/PR/genuine
thread scope and full/unknown original context, current custody, provider permission
and refusal, causal reply versus lost reply without current-state attribution,
replay/cancel/races/restart/withdrawal, and shared UI normal readback. A fresh
specifically scoped owner-authorized actual GitHub check is still required; no
real grant is retained. Delayed captured deadlines and visibility/transfer policy,
D1/D2/D3/D5, custody/root absence/scoped delivery, installed IPC/window/instance,
worker isolation/execution/retirement/settlement and release/upgrade/freshness retain
their earlier exact blockers and evidence requirements. No broad security gate or
Daybreak work was completed here.

T11-WRITES bounded native thread resolution (2026-11-05):
`lib/desktop-runtime/src/github_provider/thread_resolution.rs` and its typed
transport variant now perform a fixed resolve/unresolve against a genuine thread
Node ID, current account and immutable repository/PR membership. Preflight checks
reuse `review_threads.rs`; the obsolete REST-grouping header there was corrected.
The mutation owner serializes/paces clones, bounds the entire operation to eight
seconds, disables redirect/retry and joins retirement. Causal receipts are bounded
and scope/state checked; resolve verifies the returned resolver, and unresolve
requires known null rather than inventing an actor. An already desired state is
explicitly NotSent without a receipt. It cannot attribute a lost prior write.

New exact original-context blocker: current GitHub observations contain no proved
original two-commit pair. A supplied non-null original anchor refuses before any
provider I/O as `github_original_anchor_unobserved`; it is not silently discarded
or reconstructed from current refs. Supporting known full anchors requires a real
authenticated original-context producer/retained identity join plus matching
preparation/effect evidence; `review_threads.rs`, the new effect and the future Go
writer/selection are the relevant files. No full-anchor write support is claimed.
GitHub's selected input also has no conversation-revision or repository condition.
Membership/action/lock preflights and post-reply validation therefore do not prove
atomic protection against intervening conversation, visibility, lock or transfer
changes. Those stronger T11 selected-resource claims require an adopted supported
policy/invariant and actual permitted/refused evidence before closure.

The existing native observer passed 52 declared cases, eight invalid descriptors,
unproved-anchor refusal, pacing/serialization and both retirement boundaries using
an owned dummy-only, no-forwarding TLS peer. Evidence root
`/home/vince/tmp/factory-thread-resolution-1105/attempt1/`, result SHA-256
`96b2811d574332de71b768b5520cabc29ad63951575a59dac285b981d816bc07`.
The canonical plan records source/executable/log hashes and exact coverage. This
proves local decoder/transport behavior, not real provider permissions/schema,
credential withdrawal through the product callback, installed admission or UI.

The bounded provider source prerequisite is resolved for explicit unknown original
context. Remaining consumer blocker: typed `collaboration.rs` callback, current
Go thread writer/selection in `pkg/factory/runtime`, shared schema-86 reserve fence
and exact captured deadline/custody/receipt binding. Closure still requires actual
composed replay/lookup/cancel/races, lost reply without state attribution, withdrawal/
replacement/another-account refusal and retained restart, followed by a fresh,
specifically scoped owner-authorized real GitHub journey and normal shared UI
readback. No real grant is retained. Delayed captured deadlines, visibility/transfer
policy and the known-original-context dependency above remain open. D1/D2/D3/D5,
custody/root absence/scoped delivery, installed IPC/window/instance, isolation/
execution/retirement/settlement and release/upgrade/freshness retain earlier exact
blockers and evidence requirements. No broad gate or Daybreak work was closed.

T11-WRITES composed human thread resolution (2026-11-05):
`pkg/factory/runtime/native_product_threads.go` now binds the current admitted
writer/selection to the schema-86 fence; `native_product.go` supplies the existing
Application/HTTP service. `lib/desktop-runtime/src/collaboration.rs` supplies the
typed callback with selected repository/account, exact command/digest, captured
deadline, one native write per operation, current token lifetime and final custody.
Ordinary 8 KiB thread effect limits remain. Canonical selection returns observed
state and explicit unknown original context; both shared action capabilities remain
unavailable. Native causal receipts and exact saved preparation remain internal.

Actual native custody/callback, Go canonical service/HTTP/journal and owned
PostgreSQL passed 13 declared cases with seven dummy mutations. Safe selection,
resolve/unresolve/readback, exact replay/lookup/cancel, changed thread/state/context
refusal, explicit no-send no-op, supplied full-anchor refusal, concurrent claims,
other repository refusal, lost reply without state attribution, both late-result
cancellation races, withdrawal/replacement, other account and retained restart
passed. The peer retains its dummy thread across restart and never forwards.
Successful owned accounts/children were cleaned up. Evidence root
`/home/vince/tmp/factory-thread-resolution-product-1105/attempt1/`, result SHA-256
`11cb1977b2e23d3a22e642b0934139d16e2e820970737a38d42d5b4c77679548`.
The canonical plan records exact artifacts and coverage. The separate prior SQL
check covers cancellation winning before reservation. None of this proves actual
provider or installed admission/custody, shared WebKit or full-original-context
support, and no real grant is retained.

The callback/Go writer/selection composition blocker is resolved for explicitly
unknown original context. Remaining exact thread blockers: an authenticated
original two-commit-pair producer/retained identity join before accepting any full
anchor; actual specifically owner-authorized permission/schema/no-op/resolve/
unresolve and normal readback; original captured-deadline expiration evidence;
shared UI/installed peer/window/instance and custody; and an adopted supported
policy/invariant with evidence for intervening conversation, lock, visibility or
PR transfer changes. Relevant files remain `github_provider/review_threads.rs`,
`thread_resolution.rs`, `collaboration.rs` and `native_product_threads.go`; native
input has no expected conversation revision/repository condition, and a matching
resolution cannot attribute a lost prior write. Stronger protection is unverified.

Other T11 operations still require their typed canonical/native composition and
actual evidence. D1/D2/D3/D5, credential root absence/scoped delivery, installed
IPC/window/instance, worker isolation/execution/retirement/settlement and release/
upgrade/freshness retain their earlier dependencies and closure evidence. No broad
security gate or Daybreak work was completed; routine independent work continues.

T11-WRITES reviewer-request one-shot Store prerequisite (2026-11-05):
`pkg/store/factory_human_review_request_dispatch.go` now shares exact capture
state/reservation/cancellation with `factory_command_dispatch.go`.
`factory_human_review_request.go` shares observation persistence, and
`pkg/factory/human_review_request_outcomes.go` retains late known primary results
and uncertain cancellation without mutating the caller. Schema 87
(`schema.go`, `schema_command_dispatch.go`) fences actual schema-86 reviewer
captures as legacy-unknown while preserving intent, preparation and outcomes.
No claim can be recovered from reserved state; current authority must still be
admitted by the future native/Go product consumer.

Fresh and actual-86-upgraded PostgreSQL checks passed 12 capture conflicts,
separate user/team identity despite equal numeric IDs, request/removal intent,
four invalid unions, four missing/foreign target refusals without a command,
one of 32 claims, 32 cancellation races per run, late known/no-effect results,
immutable SQL guards and comment/lock/thread regressions. Concurrent upgrade,
stale DDL, retained restart, owned-child WAL recovery and clean shutdown passed.
Evidence `/home/vince/tmp/factory-reviewer-dispatch-1105/`, fresh log SHA-256
`1a9d1ea746aa360e11ca143a0870a38c33d954a0f68fc42b51fa259c1bf0d093`, upgraded
log `4bf798545126c91b3a64681fdb2053ccdb24b2d7bf4af5abf0ed2c9cf95220f1`.
The canonical plan records source/baseline/executable identities. This proves local
journal and namespace behavior, not remote target membership/permission or any
provider/installed gate.

Exact remaining reviewer blocker: complete Rust authenticated target reads and
request/removal effects under `github_provider`, then typed `collaboration.rs`
callback and Go canonical writer using this fence and current custody/deadline.
Canonical registry existence or a name must not stand in for current provider
permission or selected immutable user/team identity. The documented REST removal
body uses user logins and team slugs; binding an immutable selected target across
name changes remains unverified. Preserving all unrelated review requests while
changing one selected target is also unproved. Relevant defining files include
`pkg/factory/human_review_request.go`, the Store files above, native provider
`reads.rs`/`records.rs`/`transport.rs`, `collaboration.rs` and the future admitted
runtime writer. Required closure evidence is exact current account/repository/PR/
user-or-team scope, permitted/refused target and name-change behavior, preservation
of unrelated requests, no-op versus causal reply, replay/cancel/races/withdrawal/
replacement/restart and fresh scoped owner-authorized actual provider/normal shared
readback. A proposed preflight or SQL capture does not close these requirements.

Original thread context, selected-resource/conversation/visibility/transfer policy,
delayed captured expiration, D1/D2/D3/D5, custody/root absence/scoped delivery,
installed IPC/window/instance, worker isolation/execution/retirement/settlement and
release/upgrade/freshness retain their earlier dependencies and evidence. No broad
security gate or Daybreak work was closed; independent routine work continues.

T11-WRITES reviewer-observation prerequisite (2026-11-05):
`github_provider/review_requests.rs` now supplies fixed account-bound, bounded
review-request traversal and current user/team target lookup. `records.rs` owns
the shared GraphQL continuation decoder; `review_threads.rs` uses that same decoder.
`transport.rs` admits fixed read query variants only. User targets use durable
numeric IDs; team targets use opaque GraphQL Node identity, without a deprecated
numeric-team endpoint or inferred Node mapping. Repository/PR/owner and target
kind/identity mismatches refuse, including a team outside the repository owner.
This is not member eligibility, product permission or a mutation grant.

The reader retains code-owner flags, bots/other reviewer types and null reviewer
slots. An unseen target with hidden slots stays unknown; unknown numeric metadata
is not replaced with zero, labels or a guessed ID. Canonical Go user/team consumers
and retained mappings still need to preserve these distinctions. Complete bounded
page traversal and final identity rechecks do not prove an atomic membership
snapshot during concurrent changes.

Actual native reader/transport passed 56 declared cases, eight invalid descriptors
and retirement using dummy-only, no-forwarding TLS. Evidence
`/home/vince/tmp/factory-reviewer-read-1105/attempt1/reviewer-read-result.json`,
SHA-256 `9d0e3b160bf0140987145f94d5ba6aa6741a7f08718d20694c8cba2acbe52482`.
The existing thread-reader regression passed 36 cases, eight invalid descriptors
and retirement after the shared decoder move; result
`5947fca7163a6f76457678a1f8c378caae5394346a3b7c49b3805a0a2b1cfed5` under
`/home/vince/tmp/factory-review-threads-1105/reviewer-helper-regression/`.
Exact artifacts are in the plan. There were no mutations, real grants, canonical
consumer, shared UI or installed admission checks.

The bounded target/request reader source blocker is resolved. Exact remaining
reviewer dependency: native fixed effects preserving all unrelated reviewer
requests, then `collaboration.rs` callback and Go canonical mapping/writer/readback
using schema-87 one-shot intent. GraphQL permits immutable user/team union addition;
REST removal uses mutable login/slug selectors. Immutable selected-target binding
across renames/reuse, preservation during concurrent requests and the supported
policy for those API limits remain unverified. Relevant files are the new reader,
`transport.rs`, the future native effect, `collaboration.rs`, canonical registry
and runtime consumers. Close only with actual permitted/refused scope/name-change
and unrelated-target evidence, causal/no-op/lost-reply distinctions, replay/cancel/
races/withdrawal/replacement/restart, captured expiry and fresh scoped owner-
authorized provider/normal shared readback. No proposed preflight is verified
atomic protection. A partial/hidden list must never become permission to replace
a complete reviewer set. D1/D2/D3/D5, original thread context, selected-resource/
visibility/transfer policy, custody/root absence/scoped delivery, installed IPC/
window/instance, isolation/execution/retirement/settlement and release/upgrade/
freshness retain earlier blockers. No broad security gate or Daybreak work closed.

T11-WRITES bounded reviewer effect (2026-11-05):
`github_provider/review_request_effect.rs` and `transport.rs` now implement fixed
single-target GraphQL union addition by user/team Node ID and REST removal by
current user login/team slug. Account/repository/PR/target identity, lock/read-only
facts and known requested state precede dispatch. A matching state has no causal
receipt. Only a valid causal reply followed by complete bounded readback can
return applied; unrelated request IDs, known target identities and code-owner
flags must remain present. REST supplies no mutation actor. Failed readback,
observable selector rename, missing reply or post-send deadline remains uncertain;
matching later state cannot attribute a missing reply or authorize another send.
Native global serialization, pacing, eight-second whole deadline and joined
retirement cover this operation. No product callback/canonical writer/capability
was enabled.

The production native effect passed 61 declared cases, eight invalid descriptors,
clone serialization/pacing and pre/post-send retirement against an owned dummy-only
TLS peer that never forwards. Evidence root
`/home/vince/tmp/factory-reviewer-write-1105/attempt1/`, result SHA-256
`9f2b09bb6e3a4845361ca0635849576a558f75eaaa3dd0ce1e3f6de8c574dcaf`.
The plan records exact peer/binary/runner/result identities and public provider
contract capture. This proves bounded local component behavior, not actual GitHub
permissions, live compatibility, shared UI or installed custody/admission.

Exact remaining dependency: admitted `collaboration.rs` callback, canonical Go
user/team mapping and writer/readback using the schema-87 reservation, then fresh
scoped owner-authorized actual provider and shared/installed evidence. Removal's
name selector lacks an atomic immutable-target precondition. Preflight/readback
detect observable changes but do not exclude unseen rename/reuse; no stronger
binding is proved. Likewise bounded traversal and preservation checks do not
establish an atomic snapshot or concurrent conversation/repository condition.
The supported product policy/invariant for these API limits remains unresolved
under D5 before claiming its stronger guarantees. Relevant files are the effect,
`review_requests.rs`, `transport.rs`, `collaboration.rs`, canonical registry and
future runtime writer. Closure still needs actual permitted/refused user/team
scope, mutable-name/concurrent/unrelated-target behavior, causal/no-op/lost-reply
distinctions, replay/cancel/races/withdrawal/replacement/restart, captured expiry
and normal shared readback. No whole-set replacement is justified. Original thread
context, other T11 operations, D1/D2/D3/D5, custody/root absence/scoped delivery,
installed IPC/window/instance, isolation/execution/retirement/settlement and release/
upgrade/freshness retain earlier blockers. No broad gate or Daybreak work closed.

T11-WRITES reviewer callback/Go writer composition (2026-11-05):
`pkg/factory/runtime/native_product_review_requests.go`, `native_product.go` and
`lib/desktop-runtime/src/collaboration.rs` now bind the saved schema-87 intent to
one native reviewer effect. Go resolves the existing host-scoped canonical actor
or team, pins the observed immutable PR/account/target and original five-minute
deadline, and reserves one send only after SQL commit. Numeric historical team
external IDs refuse without guessing/rekeying. Native current custody/generation,
repository scope, ordinary 8 KiB envelope, exact command/digest and whole deadline
remain enforced. Go revalidates receipt scope/target/state/protocol, actual
addition actor or explicit null removal actor and bounded hashes/request ID.
Lost replies remain uncertain without searching matching state or resending.
Late primary results retain cancellation uncertainty.

Production native custody/callback, Go HTTP service and PostgreSQL passed 13
declared cases and nine dummy mutations covering both target kinds/directions,
exact replay/changed-intent conflicts, no-op, concurrency, scope refusal, lost
reply, both cancellation races, withdrawal/replacement, another account and
retained restart. Evidence
`/home/vince/tmp/factory-reviewer-product-1105/attempt1/reviewer-product-result.json`,
SHA-256 `2fe41e18fb99f9fe9473e27d5d3659d285530fc50998666218e472b7773c173a`.
The plan records exact source/binary/runner hashes. The local peer never forwards;
successful owned dummy accounts and children were cleaned up. Canonical targets
were seeded through the actual Store; no ordinary target catalogue/mapping,
normal shared readback, real GitHub write or installed admission was proved.

Callback/writer/existing canonical binding are now composed. Exact remaining
reviewer dependencies: authenticated target observation-to-registry mapping and
shared selection/readback; actual current eligibility/permission; supported T11 mutation
policy and evidence for mutable-name removal, unseen rename/reuse and concurrent
membership; original captured-deadline expiration; fresh scoped owner-authorized
provider/shared WebKit/installed checks. Source files for those consumers include
the writer above, `native_product_observations.go`, canonical identity registry,
`github_provider/review_requests.rs`, `review_request_effect.rs`, shared contract/
service/HTTP/UI selection. Shared write capabilities remain unavailable. Original
thread context, other T11 effects, D1/D2/D3/D5, custody/root absence/scoped delivery,
IPC/window/instance, isolation/execution/retirement/settlement and release/upgrade/
freshness retain their recorded blockers and required evidence. No broad gate or
Daybreak work was closed.

T11-WRITES atomic canonical team observation prerequisite (2026-11-05):
`pkg/collaboration/observations.go`, `pkg/store/collaboration_observations.go` and
`collaboration_identities.go` now share bounded complete team mapping in the
existing account/repository/actor/reference transaction. A late invalid fact rolls
back the whole batch; teams retain their separate host-scoped canonical identity.
No membership, permission, action or credential authority follows from this map.
Provider-neutral external IDs remain opaque; the native consumer must validate
the actual GitHub team Node ID without guessing or rekeying historical mappings.

Actual Store/native PostgreSQL evidence passed rollback before/after team writes,
rename/replay identity, equal numeric actor/team and equal external-string namespace
separation, 16 concurrent mappings, host/repository refusal, cancellation and
1,000/1,001 bounds. Retained owned/foreign team identities survived restart and
owned-child WAL recovery with clean shutdown. Evidence
`/home/vince/tmp/factory-team-observation-batch-1105/result.log`, SHA-256
`70dcbaf8f5c668b9167b483d949fb7faea0a68d894b9d9e8d6564ddff113437b`;
observer source/binary hashes are in the plan. No provider/installed check ran.

The atomic mapping prerequisite is resolved. Exact next dependencies remain the
authenticated reviewer read consumer, canonical projection and ordinary shared
selection/readback, then actual current target eligibility/permission, supported
mutable-name/concurrency policy, captured expiry and fresh owner-authorized
provider/WebKit/installed evidence. Shared writes remain unavailable. Other T11
effects, original thread context, D1/D2/D3/D5, custody/root absence/scoped delivery,
IPC/window/instance, isolation/execution/retirement/settlement and release/upgrade/
freshness retain their recorded blockers and closure evidence. No broad gate or
Daybreak work closed.

T11-WRITES authenticated reviewer catalogue and normal API readback (2026-11-05):
`github_provider/request.rs`, `reads.rs` and `review_requests.rs` now share one
semantic read; the callback independently retains native scope/custody checks.
`native_product_review_request_reads.go` validates the bounded nonsecret snapshot
and maps known actors/teams in one canonical observation transaction. The shared
service/HTTP endpoint, product composition and Go/TypeScript records preserve
reviewer kind, request/code-owner facts, nullable external/Node identity and
separate canonical actor/team refs. Unknown numeric users stay unmapped; unknown
other-type Node IDs and null reviewer slots stay unknown/visible. No membership,
permission, action or spending authority follows from catalogue/registry facts.

The actual custody/provider/callback/Go HTTP/PostgreSQL observer passed 27 declared
cases and nine dummy mutations without seeded canonical targets, including
ordinary readback after all four write directions and matching readback without
lost-reply attribution. Unknowns, bots, provider-type and user/team namespaces,
renames, BigInt identity, refusal/partial/count/bounds, 1,000 compact records,
replay/cancel/races/withdrawal/replacement/other account and restart passed.
Evidence `/home/vince/tmp/factory-reviewer-read-product-1105/attempt4/`, result
SHA-256 `7e891aaccc8b512d5484b8ffadb0559671d255380a94f3cb492625acc014af03`.
The existing 56-case/eight-invalid/retirement reader passed through the refactored
port. Artifacts/hashes and failed attempts are recorded in the plan. Earlier
attempts exposed an omitted Go expected-kind dispatch entry; an external fixed-
marker diagnostic overlay located it. Only the final production build passed;
no diagnostic code was added to repository source. Failed private fixture roots
are retained. Successful owned dummy accounts/children were cleaned up. No real
grant/write, shared browser or installed check ran.

Authenticated mapping and normal API readback dependencies are resolved in source
with declared-peer evidence. Remaining reviewer blockers: shared selection/action
UI, actual target eligibility/permission and fresh scoped owner-authorized provider
evidence; supported T11 mutation policy and evidence for unseen mutable-name rename/reuse,
concurrent requests and absent atomic conversation/repository conditions; captured
expiration; shared WebKit/installed lifetime/custody/admission. The private channel
still caps replies at 2 MiB; maximum metadata across all product/browser envelopes
is not qualified. Shared writes remain unavailable. Relevant files include the
native/read consumer above, shared reviewer records/service/HTTP and future shared
selection UI. Other T11 effects, original thread context, D1/D2/D3/D5, custody/root
absence/scoped delivery, IPC/window/instance, isolation/execution/retirement/
settlement and release/upgrade/freshness retain recorded blockers and closure
evidence. No broad gate or Daybreak work was closed.

T11-WRITES PR-creation one-shot journal prerequisite (2026-11-05):
`pkg/store/factory_human_pull_create_dispatch.go`, `factory_command_dispatch.go`,
`factory_human_pull_create.go` and `pkg/factory/human_pull_create_outcomes.go` now
share exact capture, committed reservation, cancellation and late-primary result
retention. Only receiving true after SQL commit permits one send; reserved or
legacy-unknown history cannot release or regain ownership. Schema 88
(`schema.go`, `schema_command_dispatch.go`) fences actual schema-87 PR captures
without rewriting original base/head repositories/branches, prose, maintainer
intent or outcomes. This establishes no current provider/fork/branch authority.

The actual Store/native PostgreSQL observer passed 15 capture conflicts, six
invalid commands, six missing/foreign head/base/actor refusals with no command,
same/separate repository intents, maximum writing/preparation, one of 32 claims,
32 cancellation races per run, late causal/no-effect receipts without caller
mutation and immutable SQL/previous-operation regressions. Actual schema-87
captures (including a maximum-size capture) survived concurrent upgrade/stale DDL;
unsupported older/future markers refused without rewrite. Restart, owned-child
WAL recovery and clean shutdown passed. Evidence root
`/home/vince/tmp/factory-pull-create-dispatch-1105/`, fresh final log SHA-256
`53c37f42020747a1cfe29b88c377d47795c890accc6c76b7252c8ebd773a8ae2`, upgraded
final log `f47ea48731380f56cad1675106c1ef6fef4b60d32306218fb5e33618192c3db9`.
The plan records exact baseline/source/binary identities and the preserved failed
initial upgrade that caught the omitted version admission. This is local journal
evidence; no provider effect, shared action or installed gate was exercised.

Exact remaining PR-creation blockers: authenticated current base/head repository
and branch producers, selected secondary head/fork scope and immutable identity,
original tip/preparation conditions, fixed Rust creation effect, admitted typed
callback and Go canonical writer/destination/readback using this fence. Relevant
files are `pkg/factory/human_pull_create.go`, native provider `request.rs`/
`reads.rs`/`transport.rs`, future creation producer, `collaboration.rs`, runtime
product adapters, native/shared request envelopes and existing PR HTTP services.
Current permission/fork use, maintainer-edit support, provider conditional API
semantics and intervening tip/repository/visibility changes are unverified; a
proposed preflight or SQL identity cannot prove atomic protection. Close only
with supported documented policy/invariant and actual scope/permission/refusal,
causal/lost-reply/replay/cancel/races/withdrawal/replacement/restart, captured expiry,
normal destination/readback and fresh specifically owner-authorized provider/
shared WebKit/installed evidence. Shared writes stay unavailable. Reviewer/thread
limitations, other T11 effects, D1/D2/D3/D5, custody/root absence/scoped delivery,
IPC/window/instance, isolation/execution/retirement/settlement and release/upgrade/
freshness retain recorded blockers. No broad gate or Daybreak work was closed.

T11-WRITES current PR base/head branch observation prerequisite (2026-11-05):
`github_provider/pull_branches.rs`, `request.rs`, `reads.rs` and `transport.rs`
now share fixed authenticated branch-pair observations with bounded responses,
whole deadline and joined retirement. Numeric repository identity binds to its
same-object Node ID; repository/name/owner/ref/commit scope, exact branch prefix
and pair rechecks refuse contradictions or observed changes. Unknown permission,
read-only and fork/source facts stay unknown. Related repository facts cannot
contradict primary numeric/Node identities. Neither data nor a matching fork label
admits creation, maintainer modification, spending or execution.

The actual native reader passed 60 declared cases, 12 invalid descriptors before
provider I/O and retirement against the owned dummy-only, no-forwarding TLS peer.
Evidence `/home/vince/tmp/factory-pull-branches-1105/attempt2/pull-branches-result.json`,
SHA-256 `e3e674ef45a3707a598fba6cee77142efe2bd0522cb5cf054e7ae982be6e2b37`.
The plan records exact peer/binary/runner identities and preserved earlier passing
artifacts. There were zero mutations, real grants, canonical consumer, browser or
installed checks. Current branch tips are not a proved historical thread anchor.

The current branch producer dependency is resolved in source. Exact remaining
dependencies: fixed Rust PR creation over saved preparation, admitted typed
callback and explicit secondary-head/fork scope, Go canonical preparation/writer/
destination/readback using schema 88, and exact-route maximum-writing envelopes.
Current native PR POST still has the ordinary 64 KiB request limit. Relevant
files include the new producer, `collaboration.rs`, runtime product adapters,
`product_request.rs`, native collaboration envelopes and shared PR services/UI.
The official creation input lists no expected-tip condition; preflight/recheck is
not an atomic write condition. Supported T11 mutation policy and actual tip/repository/
visibility/race evidence remain open, alongside current fork/branch/maintainer
permission, captured expiry, causal/lost reply/replay/cancel/withdrawal/replacement/
restart and fresh owner-authorized provider/shared WebKit/installed evidence.
Shared writes remain unavailable. Reviewer/thread constraints, other T11 effects,
D1/D2/D3/D5, custody/root absence/scoped delivery, IPC/window/instance, isolation/
execution/retirement/settlement and release/upgrade/freshness retain recorded
blockers and closure evidence. No broad gate or Daybreak work was closed.

PR maintainer-edit delegation dependency found during creation work (2026-11-05):
The [official fork guidance](https://docs.github.com/en/pull-requests/how-tos/work-with-forks/allowing-changes-to-a-pull-request-branch-created-from-a-fork)
describes this option for personal-account forks; permitting edits can also permit
workflow changes and access to secrets. The captured `AllowMaintainerEdit` boolean
is intent, not proof of eligible ownership or an informed grant. No live delegation
or secret-access check has run, and no such access is needed to prove the option's
ordinary product behavior. The native creation effect and shared PR action UI are
not yet implemented.

Exact blockers: current selected head/fork identity, personal-fork ownership and
permission; explicit truthful shared UI scope for the option; exact propagation
and provider observation of true/false without silently dropping the choice; and
fresh specifically owner-authorized live fixture evidence of accepted/refused
grant scope. Relevant files are `pkg/factory/human_pull_create.go`, the future
native creation effect, `pull_branches.rs`, native callback/runtime preparation
and shared PR form. Closure needs bounded actual provider permission/flag evidence
and account/scope withdrawal/race behavior, with the workflow-access implication
represented in the user decision. Do not infer permission from a username, registry
row, dummy response or a proposed restriction, and do not promote this research
to verified protection. D5 and the existing creation/custody/admission gates remain
open.

T11-WRITES bounded native PR-creation effect (2026-11-05):
`github_provider/pull_create.rs`, `mod.rs` and `transport.rs` now implement a fixed
single attempt over saved repository/owner/ref/tip pins and exact prose/options.
Fresh branch observations must match; known head write access and a known matching
cross-repository network are required. Unknown/moved inputs refuse. True maintainer
delegation refuses unless the selected head is the current account's personal
fork; no choice is silently discarded. This restriction has declared-peer evidence
only. It does not close the informed UI/workflow-access or actual permission gate
above. Literal ref qualification that cannot be interpreted safely refuses.

The causal reply must match current author, scope, refs/tips, writing, state and
maintainer choice. Bounded receipts explicitly describe preflight/causal-reply
checks, not CAS. Every failure after attempted send remains uncertain without
resend or matching-state attribution. Native serialization/pacing and joined
retirement enforce local lifetime only: a missing reply, timeout or local stop
does not prove a remote effect stopped or rolled back. The Go owner must win the
durable reservation before invoking this component.

Actual native provider/transport passed 77 declared cases, 18 invalid descriptors,
clone serialization/pacing and pre/post-send retirement; the stopped owner sent
no further requests. Evidence
`/home/vince/tmp/factory-pull-create-1105/attempt2/pull-create-result.json`, SHA-256
`3371e4ab15854dbc72abf6134eac4fbbda6ed025e232982d1eab72ec0bc49cd7`.
There were 55 dummy mutation attempts against an owned peer that never forwards.
The plan records exact artifacts and the preserved initial fixture-accounting
failure; scenario-local accounting does not infer remote cancellation. No real
PR/grant, Go consumer, shared UI or installed gate was exercised.

The fixed native effect source dependency is resolved. Exact next blockers:
`collaboration.rs` typed creation callback and selected secondary-head admission,
Go canonical preparation/writer/destination/readback using schema 88, precise
large-writing route/envelope bounds and actual captured-expiry/replay/cancel/
races/withdrawal/replacement/restart evidence. Relevant files include the effect,
branch reader, native collaboration/product envelopes, runtime PR adapters and
shared PR services/UI. Actual provider permission/fork/maintainer behavior, UI
grant scope, supported non-atomic tip/repository/visibility policy and fresh owner-
authorized provider/WebKit/installed evidence remain unverified. Shared writes
remain unavailable. Reviewer/thread limits, other T11 effects, D1/D2/D3/D5, custody/
root absence/scoped delivery, IPC/window/instance, isolation/execution/retirement/
settlement and release/upgrade/freshness retain recorded blockers. No broad gate
or Daybreak work was closed.

T11-WRITES journaled native PR-creation composition (2026-11-05):
The earlier source blockers for the typed callback, explicit secondary head, Go
preparation/writer, canonical destination and maximum-writing envelopes are now
resolved in source. `native_product_pull_creates.go`,
`native_product_pull_branches.go`, `native_product_observations.go`,
`native_collaboration.go`, `native_server.go`, `collaboration.rs`,
`product_request.rs` and shared `human_pull_creates.go` services/HTTP compose the
schema-88 fence and fixed native creator. Canonical head references do not grant
access; Rust freshly observes the explicit numeric head, saved pins, permission
and network. Go preserves the original preparation/deadline and requires a
committed one-shot reservation before calling Rust. Only a matching causal receipt
plus a fresh exact normal PR read can project a destination. Lost replies and
replacement generations remain unresolved without another attempt; hidden or
changed destinations cannot erase a committed effect.

Actual native custody/Go HTTP/registry/journal/PostgreSQL composition passed 22
owned declared cases with 14 dummy mutations, maximum Unicode/control writing,
selected branch/delegation variants, pre-send permission/network refusals,
replay/concurrency/input conflicts, lost-reply normal readback, cancellation,
withdrawal/replacement, retained restart and other-account refusal. The canonical
head came from an authenticated normal PR read without a registry seed. The final
owned dummy accounts were removed and the supervisor stopped. Evidence:
`/home/vince/tmp/factory-pull-create-product-1105/attempt6/pull-product-result.json`,
SHA-256 `df0441081d91b914f9d9e0fdcfb756b0ed16f52cb119c01e5db9aeb2d92de855`.
The canonical plan records exact binaries/peer/runner hashes and all retained
failures, including the fixed Go large-submission whitelist omission. This is a
no-forwarding TLS fixture; no real GitHub mutation, permission or delegation was
proved. Source/desktop/ownership builds are not installed qualification.

Exact remaining creation blockers/dependencies: a genuinely unclaimed original
capture surviving its five-minute expiry without reprepare/resend (whole-call
8-second timeout and reserved/terminal replay do not prove it); fresh specifically
owner-authorized GitHub schema/grant and current fork/branch/write/maintainer
acceptance/refusal; truthful shared UI for the delegation option, including its
workflow/secrets implications; supported T11 mutation policy and actual tip/repository/
visibility races despite the absent expected-tip creation condition; and fresh
provider/shared WebKit/installed evidence. The private creator's personal-fork
restriction and dummy option propagation are not proof of informed live grant
scope. Closure requires the actual admitted request, captured intent and causal
provider receipt/normal readback under current identity, plus no duplicate after
loss/cancel/withdrawal/replacement/restart and truthful visibility retirement.
Shared writes remain unavailable. Remaining T11 review/branch/merge operations,
reviewer mutable-name and historical-thread-anchor constraints, D1/D2/D3/D5,
custody/root absence/scoped delivery, IPC/window/instance, isolation/execution/
retirement/settlement and release/upgrade/freshness retain recorded requirements.
No broad gate or Daybreak work was closed; the product remains unfinished.

T11-WRITES human review local dispatch dependency (2026-11-05):
`factory_human_review_dispatch.go`, `factory_command_dispatch.go`,
`factory_human_review.go`, `human_review_outcomes.go` and schema 89 now fence
review submission attempts with the existing immutable Go ledger and row-lock
protocol. Actual fresh and 88→89 PostgreSQL evidence preserves old exact captures
and outcomes, protects new rows against stale upgrades, permits one of 32 claims,
serializes cancellation races and retains late causal/no-effect evidence without
mutating the caller. Restart, real child crash/WAL recovery, unknown-schema
refusal and other existing operation fence regressions passed. Evidence:
`/home/vince/tmp/factory-review-dispatch-1105/fresh.log` SHA-256
`3b306a5ac4de2ffa7ad9c535c0f2d6612e3ef1c5e526d2d9f29629ac175f754f` and
`upgraded.log` SHA-256
`f2238ee6e7e1510b31904eba9a7f0983dd1dad5653bf2fcddd1acda599936ade`.
The canonical plan records source/binary hashes and the real schema-88 baseline.
This is local journal coordination; no provider/security gate was tested or closed.

Exact remaining review dependencies: complete current-account pending-review and
selected PR/head facts; fixed native draft-versus-submitted effects that preserve
existing pending review identity, full writing and notification semantics; typed
callback/deadlines and exact-route envelopes; Go canonical capture/writer and
normal review readback; then fresh scoped owner-authorized provider/shared UI/
installed evidence. A retained capture, not-observed row, current matching review
or SQL reservation cannot prove current permission, remote absence, attribution,
head atomicity or retry safety. Closure needs actual current identity/scope/head
and existing pending review binding, causal bounded receipt, exact event/writing,
no duplicate after lost reply/cancel/withdrawal/replacement/restart, original
unclaimed expiry and truthful refusal/visibility retirement. Existing review
comments/threads, reviewer mutable-name, PR delegation and T11 tip-condition limits
retain their separate requirements. Relevant consumers are the future
`github_provider` review effect/observation, `collaboration.rs`, native product
adapters and shared review UI; Go registry/transactions stay in Go. Shared writes
remain unavailable. No broad gate or Daybreak work is complete.

T11-WRITES current-account pending-review observation (2026-11-05):
The native observation prerequisite now exists in
`github_provider/review_preparation.rs`, `request.rs`, `reads.rs`, `transport.rs`
and the existing native observer. It binds numeric/Node account, selected
repository/PR and head, traverses complete bounded visible pending identities
without a mutable login filter, selects only the exact current author Node and
fetches that review's full writing. Unknown numeric review IDs/commits/flags remain
unknown; historical pending commits remain distinct from the current head.
Observed contradictions/changes, hidden authors, multiple current pending reviews
and invalid counts/pages/details refuse. No write permission or atomic snapshot
is inferred. The source dependency for this producer is resolved; Go consumption
and fixed authenticated review effects remain pending.

Production native reader/transport passed 58 owned declared cases, two invalid
selectors and joined owner retirement with zero mutations. Evidence:
`/home/vince/tmp/factory-review-preparation-1105/attempt2/review-preparation-result.json`,
SHA-256 `47f6060c2a9e0f8e189ea148914d758e0d7f6cdf8fc481e1d72819ed50300831`.
The plan records the exact peer/binary/runner hashes, first expected-status
correction and preserved artifacts. This is dummy-only/no-forwarding TLS, not
live GitHub schema/grant/visibility/permission, all combined maximum-body/page
bounds, shared UI, installed admission or an atomic mutation condition. Shared writes remain
unavailable.

New exact review lifecycle blocker identified in the
[official review input schema](https://docs.github.com/en/graphql/reference/pulls#updatepullrequestreviewinput):
`updatePullRequestReview` addresses a review and body without an expected-state or
body condition. A separate pending preflight followed by that update could race
another client's submission; it cannot prove the updated writing stayed private
in a draft. Creation and existing-review submission also have different semantics
and must preserve the actual pending identity and comments rather than create a
replacement review or silently publish a draft. Relevant files: the new producer,
future native review effect, `collaboration.rs`, native Go human-review adapter,
`human_review.go` capture and shared review editor.

What is proved: bounded current identity/pending/head observations and the schema-89
local one-shot fence, separately. What remains unverified: a supported draft/body/
head condition or explicit safe product policy for concurrent submission/editing;
exact existing review/comment preservation and notification behavior; stale/dropped
reply/cancel/custody/restart attribution; original unclaimed expiry; actual provider
and shared UI behavior. Closure requires evidence from the actual admitted review
operation and causal receipt under current account/head, concurrent external
submission/body-change cases, truthful partial/indeterminate outcomes and no
unrequested publication or duplicate. No proposed restriction or preflight is a
verified fix. This routine product work remains in the normal queue; use Daybreak
only for a user-started check that looks like hacking. D5 and the previous review,
PR delegation, thread/reviewer, custody/admission/isolation/execution/distribution
gates remain open.

T11-WRITES bounded new-review effect (2026-11-05):
`github_provider/review_create.rs`, `mod.rs` and `transport.rs` now provide one
fixed creation over saved repository/PR Node/numeric identity and reviewed head,
exact event and writing. Current account/pending/head/state preflight, causal
identity/body/commit/submission validation, bounded receipt/transport/deadline,
serialized/paced attempts and retirement have source implementation. Existing
pending reviews refuse before a new attempt and retain their separate owner;
this does not implement existing-review submission or draft-body editing.

Production native provider/transport passed 55 declared cases, nine invalid
descriptors, clone serialization/pacing and retirement with 42 dummy mutation
attempts. Evidence:
`/home/vince/tmp/factory-review-create-1105/attempt1/review-create-result.json`,
SHA-256 `2ac1d407ade5f24c0512f87c193369a391db648e2f535e72766edc8ec647ae79`.
The plan records exact peer/binary/runner identities. This is dummy-only,
no-forwarding TLS; no actual GitHub review, grant, notification, current permission,
atomic condition, shared UI or installed admission was qualified. Longest combined
identity receipt bounds also lack direct case evidence. No broad gate closed.

Exact new-review consumer dependencies: schema-89 committed reservation before
every native attempt; typed callback/current custody/original deadline and exact-
route writing envelope; canonical Go capture/receipt/normal readback; then actual
replay/loss/cancel/withdrawal/replacement/restart/maximum writing and genuinely
unclaimed original-expiry evidence. Go registry/transactions stay in Go. Existing
pending submission additionally needs actual pending identity/commit/full writing
and comment preservation. The producer currently records pending comment count,
not their complete identities; unchanged count cannot prove unchanged comments.
Add the bounded defining observation/condition needed by that consumer before
claiming full preservation. Existing-draft body edits retain the separately
recorded race with another client's submission; this new-review refusal is not a
verified fix for that operation.

T11 current-head/body/visibility policy, actual owner-authorized provider
permission/event/notification behavior and shared UI/installed checks remain
required. Preflight plus a matching causal reply is not an expected-head
condition or remote deduplication. Shared write capabilities remain unavailable.
Remaining review/branch/merge effects, thread/reviewer/PR-delegation constraints
and earlier D1/D2/D3/D5, custody/admission/isolation/execution/distribution gates
retain their recorded blockers and closure evidence. No Daybreak work ran or was
marked complete.

T11-WRITES journaled native new-review composition (2026-11-05):
The source dependencies for new-review callback/Go writer/destination/large-writing
composition are now resolved in `native_product_review_creates.go`,
`native_product_observations.go`, `native_collaboration.go`, `native_product.go`,
`native_server.go`, `collaboration.rs`, `product_request.rs`, shared human-review
services/HTTP and the Go/TypeScript `ReviewRef`. Go retains canonical IDs, original
preparation/deadline and the schema-89 one-shot reservation; Rust retains the
complete authenticated operation. Existing pending reviews refuse during
preparation. Matching current state cannot attribute a lost creation reply, and
replacement generations cannot regain permission to send. A causal saved receipt
plus exact normal review/parent/author read is required to project a destination;
visibility failure cannot erase the committed effect.

The actual native credential/callback/Go HTTP/registry/journal/PostgreSQL path
passed 14 declared cases and 10 dummy mutations, all four new-review events,
maximum Unicode/control writing, replay/concurrency/conflicts, existing-pending
refusal, destination disappearance/replacement/restoration, lost reply, cancellation,
withdrawal/replacement, restart and another account's refusal. Final owned dummy
accounts were removed and the supervisor stopped. Evidence:
`/home/vince/tmp/factory-review-create-product-1105/attempt1/review-product-result.json`,
SHA-256 `4caa9a912759a0ff017a0a374e4a3e1ec84ac779af4923cf4497559445aab144`.
The canonical plan records exact binaries/peer/runners. Event scenarios use
independently selected declared provider states; they do not qualify a continuous
pending-review lifecycle. No live GitHub permission, actual notification, shared
review UI, installed admission or broad gate was qualified. Builds remain captured
source/locked-input evidence. Shared write capabilities remain unavailable.

Exact remaining dependencies: genuinely unclaimed original-capture expiry;
complete pending comment identities/full writing/commit and a dedicated native
existing-review submission owner; a supported existing-draft body/submission race
condition or explicit safe product policy; supported T11 mutation head/body/visibility policy
and actual races; fresh scoped owner-authorized current provider grant/permission/
event/notification and shared UI/installed evidence. Closure needs the actual
admitted operation, immutable saved scope/intent, causal receipt and normal readback,
exact pending/comment preservation, truthful draft-versus-submitted behavior and
no duplicate/unrequested publication after loss/cancel/custody replacement/restart.
No pending edit/submission protection is inferred from this new-review refusal.
Other review/branch/merge effects, reviewer/thread/PR-delegation constraints and
D1/D2/D3/D5, custody/admission/isolation/execution/distribution requirements remain
open. No Daybreak work ran or was marked complete.

T11-WRITES original new-review capture expiry proved locally (2026-11-05):
The new-review consumer's genuinely unclaimed original-expiry dependency is now
resolved by the actual native credential/callback/Go HTTP/service/journal/
PostgreSQL path. The private fixture helper delayed only the first post-capture
outcome transaction, verified rollback and an exact committed unfinished unclaimed
capture, then removed its trigger/function. No command/preparation/deadline was
inserted or rewritten by the helper. After waiting 287 real seconds for the
original five-minute deadline, the same request returned 403 with original
capture/intent/deadline bytes unchanged, no reservation, no further preparation
query and zero provider mutations. Local cancellation then completed, the owned
dummy account was withdrawn and the supervisor stopped cleanly.

Evidence: `/home/vince/tmp/factory-review-expiry-1105/attempt1/review-expiry-result.json`,
SHA-256 `6681e919731b1f23e80c276b94e0b3863ef91973b77830f8ee2761838751f447`.
The plan records exact helper/peer/binary/runner hashes and the method. Private
owned database input was read locally without printing it; all state/evidence
remain outside the repository. This is the production consumer with a disposable
SQL timing fixture and dummy-only/no-forwarding provider. It is not a fake clock,
seeded expired capture, reserved replay, live provider or broad gate qualification.

Remaining expiry obligations apply to other native writer consumers and future
existing-pending review paths, not this proved new-review branch. Existing review
submission/draft-edit semantics, exact pending/comment preservation, concrete
provider head/body/visibility limits, actual permission/event/notification and
shared UI/installed evidence still require their recorded work. D5 is specifically
Fountain issue-only admission; it is not a generic GitHub mutation gate. Normal
routine engineering and independent product work continue. Shared writes remain
unavailable, all actual D1/D2/D3/D5 and custody/admission/isolation/execution/
distribution gates remain open, and no Daybreak work was marked complete.

T11-WRITES complete bounded pending comment observation (2026-11-05):
The count-only observation dependency is now resolved in source by
`github_provider/review_preparation.rs`, `request.rs` and `reads.rs` through
`PullPendingReviewSnapshot`. It hashes complete bounded comment identities/full
writing/nullable author and commit metadata with deterministic Node ordering,
checks exact review/PR/repository/account/head and rechecks pending preparation.
Two comments per response, 1,000 total, 8-MiB aggregate and existing whole deadlines
bound the work. Unknown IDs/authors/commits remain unknown. No full comment array
crosses Go's private callback and no mutation or consumer was enabled.

Production reader/transport passed 48 declared cases, two invalid selectors,
complete independently checked writing/identity digests, 1,000 comments, maximum
escaped bodies, order stability, nullable/historical metadata, scope/count/page/
body/aggregate/change refusals and joined owner retirement, with zero mutations.
Evidence:
`/home/vince/tmp/factory-pending-review-snapshot-1105/attempt3/pending-review-snapshot-result.json`,
SHA-256 `a167ad14a6da107db5eec69274caf6de96ce8da4c46a9e91ec41d5024d9299f2`.
The plan records exact peer/binary/runners and both retained pre-check setup
failures. Required isolated-keyring setup was restored; its guard was unchanged.
This is a dummy-only/no-forwarding native reader component, not custody, current
real GitHub schema/pending privacy, write permission or publication qualification.

Exact remaining dependencies: dedicated exact pending-review submission and Go
capture/writer/readback; preservation of selected pending identity, original body/
comments/commit and explicit event through actual provider operation/receipt;
loss/replay/cancel/custody replacement/restart/expiry; actual permission/publication
and shared UI/installed checks. This observed-set digest is not an expected-state,
head or comment-set revision token; provider races still require truthful supported
semantics and actual evidence. Existing-draft body editing retains the recorded
race with concurrent submission. Complete observed facts alone do not prove
atomic preservation or a passed security gate. These are concrete T11 constraints,
not D5's Fountain issue-only admission decision. Independent work continues;
shared writes and all actual broad gate claims remain unavailable/open. No
Daybreak work ran or was marked complete.

T11-WRITES known primary versus unfinished completion (2026-11-05):
`human_command_outcomes.go::retainHumanCancellationUncertainty` now retains a
proved terminal primary after local cancellation even when separate completion is
unfinished, and carries that uncertainty into a later completion reply without
mutating the caller. Exact receipt/effect/terminal guards still reject conflicting
claims. This resolves a local reducer dependency found before pending submission;
no provider authority or publication condition is added.

Actual fresh and old-88→89 native PostgreSQL checks proved late committed/unfinished
primary retention, repeat cancellation, later completion, caller immutability,
contradictory receipt rollback, restart and real child crash/WAL recovery, with the
existing dispatch/upgrade/conflict/race regressions. Evidence:
`/home/vince/tmp/factory-review-completion-1105/fresh.log`, SHA-256
`492b6112f00f8abc9ac459a41b9734edb3a2502fab0dfebf24c302c7cdd00c09`, and
`upgraded.log`, SHA-256
`509b881c8f6ade32a4fb25b161e7671624f776374267d72f057d7a29f5bf3821`.
The plan records the exact driver/helper/baseline hashes. These are typed declared
observations in real SQL, not actual GitHub receipts or a provider/security gate.

Remaining dependencies stay the dedicated pending-review effect and Go consumer,
separate current publication/comment preservation verification when needed,
loss/replay/cancel/custody/restart/expiry and actual provider/shared UI/installed
behavior. A known causal primary must survive later verification failure; do not
replace it with no-effect, discard it, mutate its receipt or resend to finish
completion. Concrete provider-condition limits and all actual canonical/broad
gates remain open. No Daybreak work ran or was marked complete.

T11-WRITES current publication verification observation (2026-11-05):
`github_provider/review_preparation.rs`, `request.rs` and `reads.rs` now expose
`PullReviewPublication` for an exact numeric/Node review under the current account,
PR and repository. One defining bounded decoder handles pending and submitted
comment facts. It returns both full-fact and identity/exact-writing digests;
publication timestamp changes are separate from writing, and historical review
commits stay distinct from a later current head. Pending snapshots also supply a
native-produced preparation digest. None is a provider revision token or proof
attributing a prior command. No write or product consumer was enabled.

Production reader/transport passed 41 declared publication cases, three invalid
selectors and retirement with zero mutations. The shared decoder then passed the
48-case pending regression, two invalid selectors and retirement with independent
pending-record and writing/identity digest assertions. Evidence:
`/home/vince/tmp/factory-review-publication-1105/attempt1/review-publication-result.json`,
SHA-256 `9b89b21ee4b16922552f1599a968b27b8a7a20da76e225f2895bf8d03ec14871`,
and `pending-regression/pending-review-snapshot-result.json`, SHA-256
`a167ad14a6da107db5eec69274caf6de96ce8da4c46a9e91ec41d5024d9299f2`.
The canonical plan identifies exact peer/runners and the two observer binaries.
This is dummy-only/no-forwarding native component evidence, not live provider
permission/privacy/publication, custody, shared UI or installed qualification.

Exact remaining dependencies: dedicated fixed pending submission, causal receipt
persistence in the schema-89 Go journal, then separately admitted current
publication verification/completion. A verifier may be used for completion only
after authenticating the immutable saved causal receipt and exact command/scope;
it must not adopt a matching current review after a lost reply. Known primary
commitment must survive verification failure with unfinished completion and no
resend. Actual provider identity/writing/comment preservation and event semantics,
loss/cancel/custody replacement/restart/expiry and shared UI/installed cases remain
unverified. Existing-draft update/concurrent-submission and other concrete T11
condition limits remain recorded; no atomic publication guarantee follows from
these reads. All actual canonical/broad gates remain open, shared writes remain
unavailable, and no Daybreak work ran or was marked complete.

T11-WRITES fixed exact pending-review submission producer (2026-11-05):
`github_provider/review_submit.rs`, `mod.rs` and `transport.rs` now submit only the
saved current account's exact pending Node identity, actual reviewed commit,
explicit final event and optional summary, after current saved pending/comment
hash, scope/state/deadline checks. An omitted summary preserves writing; a supplied
summary is exact. No replacement review or comment mutation is sent. A valid causal
reply returns a bounded known-primary receipt with original pending/comment pins
and actual counts, explicitly requiring separate publication verification. It is
not full completion, atomic publication or remote deduplication.

Production provider/transport passed 58 declared cases, 15 invalid descriptors,
serialized/paced clones and retirement with 41 dummy mutation attempts. Evidence:
`/home/vince/tmp/factory-pending-review-submit-1105/attempt1/pending-review-submit-result.json`,
SHA-256 `1452a23f8527c94a05c37466cb50599efb9abcd59349cb57e86cdce13bf28348`.
The plan records exact peer/binaries/runners. Preserve/replace/clear/max writing,
nullable IDs/old commit, causal count difference, separate publication/comment
readback, stale/refused preflight, all post-send uncertainty and lost reply without
attribution passed. This is dummy-only/no-forwarding component evidence. Real
provider scope/privacy/permission/notification and longest combined identity receipt
bounds remain unqualified; no Go consumer or shared capability was activated.

Exact remaining dependencies: admitted typed callback, versioned canonical Go
preparation and schema-89 committed reservation, durable immutable causal primary
receipt, then separately admitted publication completion. Verification failure or
count/writing/identity mismatch must retain known commitment with unfinished
completion, without changing its receipt or resending. A later current review alone
cannot settle a lost causal reply. Real native/Go/PostgreSQL replay/cancel/loss/
custody replacement/restart/original expiry and full writing, then actual owner-
authorized provider/shared UI/installed cases remain needed. Existing-draft body
editing/concurrent submission, concrete head/body/comment-set limits and other
review/branch/merge work remain normal T11 tasks, not D5's Fountain admission gate.
Shared writes and all actual canonical/broad gate claims remain unavailable/open.
No Daybreak work ran or was marked complete.

T11-WRITES journaled pending-review composition (2026-11-05):
`pkg/factory/runtime/native_product_{reviews,pending_reviews,review_creates,observations}.go`,
`native_collaboration.go` and Rust `collaboration.rs` now bind the exact pending
submission to the canonical schema-89 one-shot journal. Saved version-1 creation
captures retain their implementation and deadline; new version-2 captures retain
native pending/comment pins and exact account/parent/review/commit identity.
Empty canonical summary preserves saved writing; nonempty replaces it exactly.
Explicit clear and existing-draft-only editing remain unsupported product intents.

A valid causal receipt is durably committed with unfinished completion before a
separately admitted lookup verifies current published summary/comment identity
and exact writing. Verification failure/mismatch retains the immutable known
primary without resend. A causal count discrepancy stays unfinished even after
later count restoration. A lost receipt remains indeterminate despite readable
matching current writing. Cancellation uncertainty survives late primary and
later completion; account replacement or supervisor restart cannot recover send
ownership from a reserved dispatch.

Actual Rust custody/native supervisor, Go HTTP and PostgreSQL passed 23 declared
cases with 16 dummy attempts. Evidence:
`/home/vince/tmp/factory-pending-review-product-1105/attempt3/pending-review-product-result.json`,
SHA-256 `19ca099b76d34e4073fbd512b99123c8c01c0e04dfbf50881f5aecde2a832bd3`;
read-only journal assertions/log SHA-256
`41a0963eac03ef4e34781e5484a2eeeb592e83a154afd5ff5b0ce7a7d76fb2d6`.
Maximum writing, nullable/64-bit identities, historical commit/head advance/later
dismissal, failed/changed publication verification, count mismatch, concurrency,
lost reply, late cancellation/primary/completion, custody replacement, restart
and other-account refusal were observed. The version-1 regression passed 14
cases/ten dummy attempts in `rg2/`; its result SHA-256 is
`4caa9a912759a0ff017a0a374e4a3e1ec84ac779af4923cf4497559445aab144`.
The plan records exact retained artifacts, earlier fixture/observer/path failures
and the regression observer artifact retention limit. These independent declared
provider states are not a continuous live GitHub lifecycle or a security gate.

Exact remaining evidence includes original pending-capture expiry (separate
record below), longest combined identity receipt bounds, fresh actual GitHub
schema/grant/permission/pending privacy, summary/comment preservation and final
event/notification semantics, then shared UI and installed journeys. No atomic
expected-head/body/comment-set publication follows from separate bounded reads.
Actual provider cases require fresh specifically scoped owner authorization;
historical sign-ins or dummy request approvals do not authorize real writes.
Existing-draft/concurrent-submission and inline/branch/merge work remain concrete
normal T11 tasks. Shared write capabilities, D1/D2/D3/D4/D5 and broad qualification
claims remain unavailable/open. No Daybreak work ran or was marked complete.

T11-WRITES original pending-review capture expiry (2026-11-05): the existing
native expiry observer now selects version 2. In a fresh disposable native/Go/
PostgreSQL profile, a first-outcome SQL delay left the actual product capture
unclaimed without rewriting its command, preparation or five-minute deadline.
After 287 real seconds to the saved original deadline plus one, exact POST
returned 403, SQL capture bytes/deadline remained identical, no new preparation
query or mutation occurred, lookup invented no effect, and local cancellation
completed as no-effect/cancelled. The fixture trigger/function were removed;
owned account withdrawal and clean stop passed. Evidence:
`/home/vince/tmp/factory-pending-review-product-1105/ex1/review-expiry-result.json`,
SHA-256 `98ffb60e830cd1828aa10915b75603fb0deee1c58c752beacc93ebb10dc4346d`;
log SHA-256 `23381cd3848e7eee4ff8c513f6a7fad290d9c1305eb520995f1a743dbcdf844b`.
The plan records exact helper/observer/runners and production artifacts. This
closes original-expiry evidence for this consumer only. It is a dummy-only
declared check, not actual provider, installed, freshness or security-gate
qualification. All other concrete tasks, dependencies and evidence requirements
in the preceding checkpoint remain open; no Daybreak work was completed.

T11-WRITES bounded native branch preparation/creation (2026-11-06):
`github_provider/{branch_create,request,reads,transport,mod}.rs` now define one
current-account numeric/Node repository, literal branch and exact commit owner.
Preparation rechecks repository/commit/branch facts; creation refuses an existing
branch and requires known writable metadata before one fixed `createRef` with
repository Node ID, fully qualified heads name and commit OID. The causal reply
binds exact new ref/repository/commit identity. Force/update/delete, arbitrary
namespaces, replacement and compensation are absent. A lost reply stays
indeterminate despite a matching current branch; no Go consumer is enabled.

Production provider/transport passed 44 declared cases, 16 invalid descriptors,
serialized/paced clones and joined retirement with 24 dummy mutations. Evidence:
`/home/vince/tmp/factory-branch-create-1106/attempt2/branch-create-result.json`,
SHA-256 `4c7704ad4cd1ba24906d1fb53d7381cd7fb0401b5751cf6fb4bf5ebb6f4e988f`;
log SHA-256 `51a68e346ac4fe477cf8c799bb0336a34c6a8d78b3db4edbacb954ce0eeb91c6`.
The plan records exact artifacts, official API references, maximum/literal-name/
commit/identity/refusal/loss cases and the initial missing-metadata fixture
failure. This is dummy-only/no-forwarding component evidence, not a security gate.

Exact dependency before product use: canonical Go intent/capture, committed
one-shot reservation, typed current-account callback and durable immutable causal
receipt/recovery. Then prove replay/cancel/loss/original expiry/custody replacement/
restart with the actual native/Go/PostgreSQL path. Longest combined receipt bounds
remain unqualified. Actual provider object/repository association, current grant,
push permission, protected/restricted branch rule enforcement and name collision
behavior remain unverified; close them with a fresh specifically authorized
owned-repository provider journey and exact branch/commit readback, followed by
shared UI/installed evidence. Do not attempt rules bypass or infer real-write
authorization from historical consent or dummy approvals. No source check,
fixture, documentation selection or proposed fix closes these actual gates.
Inline/draft/merge and all canonical/broad gates remain open, shared writes remain
unavailable, and no Daybreak work ran or was marked complete.

T11-WRITES canonical branch journal/fence (2026-11-06):
`collaboration/effects.go`, `factory/human_branch_create{,_outcomes}.go`,
`factory/types.go`, shared branch validation in `human_pull_create.go`, and
`store/{factory_human_branch_create,factory_human_branch_create_dispatch,factory_command_dispatch,schema,schema_command_dispatch}.go`
now retain exact canonical new-branch/commit intent and bounded trusted native
preparation in the existing journal. Schema 90 requires the shared committed
one-shot reservation and conservatively fences earlier branch-shaped history.
No captured preparation/deadline is refreshed, and no provider/API was activated.

Real native PostgreSQL fresh and genuine committed 89→90 upgrade checks passed,
including concurrent openers, stale DDL, legacy uncertainty without send ownership,
one-of-32 claims, 32 reserve/cancel races per run, unbound/invalid intent,
immutable receipt/identity, late primary/completion with retained cancellation,
byte limits and future-schema refusal. After actual restart and child crash/WAL
recovery, each final observer compared 40 complete saved command/outcome records
byte-for-byte and rechecked captures/fences. Evidence:
`/home/vince/tmp/factory-branch-dispatch-1106/fresh3.log`, SHA-256
`d2817e3255a3db28acedcca0dc22b1a500f6603fac2e7cc6cdb6ba7683e09cd5`,
and `upgraded3.log`, SHA-256
`4cbe7500db2792d566cd7129e84622356ec8975886a2a475b7b406e7e56fea77`.
The plan records the exact baseline, observer/helper/driver artifacts, original
proof retention and earlier observer setup/recovery-evidence gap. These are
declared local receipts, not live provider evidence or a passed security gate.

Exact remaining dependency is admitted service/API/native callback composition
over the schema-90 fence and the Rust branch producer. Close with the actual
native custody/Go HTTP/PG journey, normal exact ref/commit readback, replay/loss/
cancellation/original expiry/withdrawal/replacement/restart and combined receipt
bounds; then fresh specifically authorized actual provider grant/permission/rule/
collision and shared UI/installed evidence. No source check or SQL fixture
qualifies real GitHub permission, a fresh/installed upgrade, candidate execution,
D1/D2/D3/D4/D5 or the complete product. All other concrete tasks and gates remain
open. No Daybreak work ran or was marked complete.

T11-WRITES composed native branch product path (2026-11-06):
`factory/service/human_branch_creates.go`, `httpapi/{human_branch_creates,product}.go`,
`runtime/{native_product_branch_creates,native_product,native_product_observations}.go`
and Rust `collaboration.rs` now compose admitted current-account branch intent,
original five-minute preparation, schema-90 committed reservation and the fixed
native provider effect. HTTP and native descriptors stay within ordinary 8 KiB
bounds. Valid causal identity/commit evidence is durably retained; no reserved
dispatch can recover ownership. Current matching branches cannot attribute a
lost reply. Only a validated committed receipt plus a fresh same-identity read
projects a public current destination; changed visibility/ref identity hides
that reference while retaining primary commitment. Current tip and historical
creation commit remain separate facts, with immutable command/receipt bytes.

Actual Rust custody/supervisor, Go HTTP and native PostgreSQL passed 14 cases with
ten dummy attempts under
`/home/vince/tmp/factory-branch-product-1106/attempt1/branch-product-result.json`,
SHA-256 `05018a0685d757ae7476c80baf40088a3b508a069a8d02a892cf0cd24b49c665`;
log SHA-256 `92de610661b8b6c85e9ba5ae3854d505325f7f73754ba221e138b7caf68cfeb5`.
Exact/max/Unicode/escaped names, immutable commits, replay/concurrency/conflict,
input/scope refusal, hidden/replaced/advanced/restored destinations, readback
after lost reply without attribution, late committed/no-send evidence after
cancellation, withdrawal/replacement, stop/restart and other-account refusal
passed. A read-only helper compared complete saved command/outcome bytes. The
canonical plan records exact retained artifacts and the separate expiry check.
The dummy peer never forwards, and no actual provider request or security gate
was qualified.

Remaining evidence: genuine original branch-capture expiry (separate record
below), longest combined identity/receipt/descriptor bounds, fresh actual GitHub
object/repository association, current grant/push permission/rule enforcement/
name collision, followed by shared UI and installed readback/recovery. Actual
provider checks need fresh specifically scoped owner authorization; historical
sign-ins/dummy approvals do not authorize real branch writes. Inline/draft/merge
work and all canonical/broad gates remain open. The shared write capability is
unavailable; no Daybreak work ran or was marked complete.

T11-WRITES original branch capture expiry (2026-11-06): the existing native
observer's separate PostgreSQL first-outcome delay left a real product capture
unclaimed without changing its command, version-1 preparation or original
five-minute deadline. After **286 real seconds** to that saved deadline plus one,
exact replay returned 403. Actual SQL intent/deadline bytes stayed identical,
there was no new preparation query or mutation, lookup invented no effect, and
local unclaimed cancellation completed as known no-effect/cancelled. The owned
delay trigger/function were removed; account withdrawal and clean stop passed.
Evidence: `/home/vince/tmp/factory-branch-product-1106/ex1/branch-expiry-result.json`,
SHA-256 `f7ca6282961146c97fee2f4bfd05c9e9e6187b116422c75f01cb269a27816afb`;
log SHA-256 `fc29f6e77f7bd8d64144622336dcc63eaee181a2503971ec3a957513f3e90192`.
The plan records the exact separate observer/peer/helper/runners and unchanged
production server. This closes original-expiry evidence for this branch consumer
only. Actual GitHub permission/rules, combined bounds, shared UI, installed/
freshness and all other concrete gates/dependencies above remain open. No
Daybreak work was completed.

T11-WRITES inline one-shot journal prerequisite (2026-11-06):
`store/{factory_human_inline_dispatch,factory_command_dispatch,factory_human_inline,schema,schema_command_dispatch}.go`
and `factory/human_inline_outcomes.go` now bind the existing exact inline capture
to the shared committed reservation. Schema 91 preserves old writing/selection/
preparation/outcomes while fencing unknown earlier local dispatch history. The
inline reducer now retains cancellation uncertainty with late known primary and
later completion, without changing caller objects or immutable receipts. No
native inline effect or product writer was activated.

Actual Go Store/native PostgreSQL fresh and genuine committed 90→91 checks
passed concurrent upgrades/stale DDL, legacy uncertainty without send ownership,
one-of-32 claims, 32 reserve/cancel races per run, conflicting full selection and
writing, invalid/unbound scope/observations, maximum writing/preparation, retained
branch/review reservations, late primary/completion and receipt conflict rollback,
byte limits and future-schema refusal. Forty complete command/outcome records
were compared after actual restart and child crash/WAL recovery. Evidence:
`/home/vince/tmp/factory-inline-dispatch-1106/fresh.log`, SHA-256
`005a45c04267eceb1b8df1ff27350f6caf1e7a5dbe3de699299f082c78eb1f55`,
and `upgraded.log`, SHA-256
`937d3a2a1deb1b2504b64da1c108caca5bb5c95903058ccee08dc77213eddff6`.
The plan records exact baseline/observer/driver artifacts and original-90 proof
retention. These declared local receipts do not prove any GitHub inline effect.

Remaining dependency: defining authenticated Rust inline preparation/effect,
then current-account Go/native product composition over this fence. Qualify
supported exact selections, full writing, causal identity and pending-review
ownership/draft privacy, loss/replay/cancel/original expiry/custody/restart and
current readback. Do not fabricate a complete original base/head anchor from
today's branches or substitute another pending review. Unknown original provider
anchors must remain unknown. Fresh specifically authorized actual provider
permission/privacy/publication/notification, shared UI and installed behavior
remain unverified. No SQL fixture or source fix closes those actual gates. All
other canonical/broad gates remain open, shared writes remain unavailable, and
no Daybreak work ran or was marked complete.

T11-WRITES exact current native inline preparation (2026-11-06):
`github_provider/{inline_preparation,request,reads,mod}.rs` now join the complete
current account's pending-review/comment snapshot to a bounded complete PR file
catalogue and an exact immutable diff selection. Native digests bind observed
file and side/range/content facts. Nullable metadata and binary/file selections
do not become line or permission claims. Current merge base and branch tip remain
separate; unknown historical provider anchors are not inferred. Pending writing,
child writing and diff scope are rechecked after traversal. No effect or product
consumer was enabled.

Production provider/transport passed 40 declared cases, 18 invalid selectors,
zero mutations and joined retirement. Evidence:
`/home/vince/tmp/factory-inline-preparation-1106/attempt1/inline-preparation-result.json`,
SHA-256 `842931b3ab09c6b2e55a24631c0f02f837c9855351a73fe4bb59c336a60f440d`;
log SHA-256 `7292cf32832affb87114a6f8275cc783a6e93b65591e19a88c3f164b8f5a5af1`.
The plan records exact peer/binaries/runners, independent position/content digest
assertions and catalogue/patch/change/bounds cases. This proves a dummy-only
native read, not any actual provider, privacy, IPC or security gate.

Exact next dependencies: supported fixed native inline effects, explicit own
new/existing pending identity, schema-91 causal-primary persistence, separately
admitted preservation/completion when needed, and Go/native product composition.
The largest escaped 4,096-byte selector was tested directly at the provider client;
its serialized request exceeds ordinary 8 KiB RPC. Qualify a narrowly typed
bounded read envelope plus full writing/selection envelopes through actual native
custody/Go HTTP/PG before advertising that input. Arbitrary request admission must
stay bounded. Qualify supported exact positions, existing summary/comment writing
and draft privacy, loss/replay/cancel/original expiry/replacement/restart and normal
readback. A real grant/permission/privacy/publication/notification and shared UI/
installed journey remain unverified and need fresh specifically scoped owner
authorization. Complete historical two-commit anchors and full source/newline
provenance must remain unknown where the provider facts cannot establish them.
No documentation or source/fixture check closes those gates. All other concrete
and canonical/broad gates remain open; shared writes remain unavailable. No
Daybreak work ran or was marked complete.

T11-WRITES admitted inline preparation envelope (2026-11-06):
`runtime/{native_inline_preparation,native_collaboration,native_product_observations}.go`
and `desktop-runtime/src/collaboration.rs` now admit only the exact typed inline
preparation read at 64 KiB. Ordinary small descriptors remain 8 KiB. The
canonical selector/account/repository and single-field observation checks remain;
private native facts are not permission, receipt or browser projection. No inline
writer or public product route was activated.

The existing native executable passed 20 declared cases through the production
Go port and native callback with actual isolated keyring custody and PostgreSQL
startup/stop. The largest escaped 4,096-byte selector crossed 8 KiB and survived
exactly; ordinary oversized, over-64-KiB inline and extra-field envelopes refused
before provider I/O. Invalid/outside selectors, stale head and changed pending/
child writing refused. These are owned dummy observations, including deliberately
absent-operation malformed descriptors, not hostile caller or security-gate
qualification. Dummy custody was withdrawn and joined stop passed with zero
mutations. Evidence:
`/home/vince/tmp/factory-inline-port-1106/attempt1/inline-preparation-port-result.json`,
SHA-256 `49bce137fb632a98b29cbc7bf56b23d4e9c96c9c7940073bb895dd5001c6ad41`;
log SHA-256 `f81c6b737ebebdd44167ac5e731e1dae2673017208b6bc5158548e47d9434f5d`.
The plan records exact observer/fixture/binaries/runners and the unavailable
HTTP response; there is no enabled product writer or durable inline command
journey. This closes only the preceding maximal escaped preparation-read callback
gap. The initial check cleanup API typo was corrected before running; production
guards were unchanged.

Next dependencies remain the supported fixed native effect and Go/native inline
consumer, full effect/HTTP writing envelopes, schema-91 causal-primary persistence,
new/existing pending identity, preserved summary/comments and draft privacy,
loss/replay/cancel/original expiry/replacement/restart and normal readback. Fresh
specifically authorized actual grant/permission/privacy/publication/notification,
shared UI/installed and all other concrete/canonical/broad gates remain unverified.
Do not infer original historical anchors or full source provenance from current
diff fragments. Shared writes remain unavailable; no source or dummy observation
closes the actual gates. No Daybreak work ran or was marked complete.

T11-WRITES exact own pending inline draft producer (2026-11-06):
`github_provider/{inline_draft,inline_preparation,transport,mod}.rs` adds one fixed
new-thread mutation on an explicit existing own pending review at the selected
head. Complete native preparation and selector digests bind the repeat preflight;
causal identity/writing/position/commit facts bind the created member. This is
not an atomic expected-head/body/comment-set guard. New pending creation,
immediate publication, older pending commits and out-of-GraphQL-range indices
remain separate unsupported intent/semantics dependencies.

A valid causal receipt explicitly requires separate preservation/privacy
verification and retains actual versus expected summary/count/state/publication.
Known creation survives concurrent summary/count/publication discrepancies;
post-send loss/misbind remains indeterminate, with no retry/current-content
attribution. No native callback effect or Go inline writer was enabled. Original
provider base/head anchors and full source provenance are not inferred.

The existing native executable passed 38 declared cases, 12 invalid descriptors,
cloned-handle pacing and checked joined retirement with 37 fixed dummy mutations.
Maximum escaped path and 64 KiB original control writing were combined. Fifteen
retained receipts also passed independent standard-crypto exact writing/selector,
summary/count/nullable-identity assertions. Evidence:
`/home/vince/tmp/factory-inline-draft-1106/attempt2/inline-draft-result.json`,
SHA-256 `a596a9821c1cad64fcd552ba0a47ed0f7a5a2f6f9bcc66e6f12cd39e553271af`;
log SHA-256 `a7956214999395476d1e691ddd5d43da3a83d1ea8351d1179bb597c5e3115d77`;
independent receipt result SHA-256
`35520c975dab4e69aee8dbf32a93a41592be88411e76894669e7a2c31061f64f`.
The plan records exact fixture/binaries/runners/observer and retained first-attempt
limitations. The TLS fixture never forwards; this explicit-token native component
run does not qualify custody callback, product journal, live GitHub, privacy,
permission, notification, IPC, security or installed gates.

Exact next dependency: separately admitted native current observation of the
known causal member plus the old pending summary/comment-writing set, excluding
that known new ID when comparing old members. Require continued draft state and
same review identity; current reads must never attribute a lost reply. Then add
the fixed callback and Go/schema-91 consumer, persist known causal primary before
completion reads, and prove full effect/HTTP envelopes, loss/replay/cancel/original
expiry/custody/replacement/restart and normal readback. Actual schema/permissions/
rename/original positions/draft privacy/publication/notification require fresh
specifically scoped owner authorization and real evidence. Shared UI/installed,
combined longest bounds and all other concrete/canonical/broad gates remain open.
Shared writes remain unavailable; no Daybreak work ran or was marked complete.

T11-WRITES current pending inline preservation reader (2026-11-06):
`github_provider/{review_preparation,request,reads}.rs` now exposes fixed current
facts for an exact own pending review/comment Node. The defining complete walker
computes the old identity/writing set with that selected Node excluded and reports
whether the selected member is present, with its sanitized writing/identity/
author/original facts. Pending state, root reread, complete count/pagination,
aggregate bounds and current-account checks remain. Missing/nullable facts stay
unknown; no matching current content attributes a lost mutation reply.

The existing native executable passed 37 declared cases, five invalid selectors,
a complete 1,000-member walk, remaining-set digest assertions and joined retirement
with zero mutations. Its existing shared pending-snapshot regression also passed
48 cases, two invalid selectors and retirement, retaining original full-set and
writing digest semantics. Evidence:
`/home/vince/tmp/factory-inline-preservation-1106/attempt1/inline-preservation-result.json`,
SHA-256 `0d93128cdc9a49e24bfe7f0a814ddca372388ca4e3a14d16634252b212f92af2`;
log SHA-256 `f5cc21539532f12d6cc1d55c7156778545ea755b48a099dff22e5ba8def96c4c`;
regression result SHA-256
`a167ad14a6da107db5eec69274caf6de96ce8da4c46a9e91ec41d5024d9299f2`.
The plan records exact retained peer/binary/runners. These explicit-token dummy
component observations are not custody callback, product journal, live provider,
privacy/permission/notification, IPC/security gate or installed qualification.

Next: authenticate the saved causal primary before this separate completion read
in the admitted Go/schema-91 consumer; preserve original old summary/members,
known new identity/writing and continued draft state. A causal summary/count/
publication discrepancy must remain unfinished even if later state looks restored.
Full effect/HTTP envelopes, loss/replay/cancel/original expiry/custody/replacement/
restart/readback remain unverified. Exact capacity blocker: an existing 1,000-member
review leaves no room for one added member within this 1,000-member complete walk.
A before-send supported-capacity admission (or a newly qualified expanded bound)
is still required and unverified; no proposed fix closes it. New-pending/published
modes and old-commit/integer/original-anchor limitations remain as recorded above.
Actual provider schema/permissions/rename/original positions/draft privacy/
notification need fresh specifically scoped authorization and real evidence.
Shared UI/installed, combined bounds and all other concrete/canonical/broad gates
remain open; shared writes remain unavailable. No Daybreak work ran or completed.

T11-WRITES durable pending inline consumer (2026-11-06):
`runtime/native_product_inlines.go` now composes schema-91 capture/reservation,
fixed native `append_pending_thread` callback and a separate authenticated
preservation read. Relevant native files are `collaboration.rs`,
`product_request.rs` and `github_provider/inline_draft.rs`; Go callback/read/HTTP
admission lives in `native_collaboration.go`, `native_product_observations.go`
and `native_server.go`. Saved causal primary precedes completion; lost replies
never gain attribution from current matching writing. Causal summary/count/
publication discrepancies stay unfinished. Before-send native/product capacity
refusal now leaves room within the complete 1,000-member observation bound,
closing that specific component dependency from the prior checkpoint.

Actual production Go HTTP/store, Rust custody/callback and private PostgreSQL
passed the dummy composed matrix (31 named cases, 24 dummy mutations), including
loss/replay, two actual reservation/cancel races, concurrency, preservation,
withdrawal/reconsent/restart and cross-account refusal. Separate genuine original
expiry waited 287 seconds, then refused without mutation/re-preparation and
completed local cancellation. Existing native producer capacity matrix also
passed. Evidence under `/home/vince/tmp/factory-inline-product-1106/`:
`attempt4/inline-product-result.json` SHA-256
`e8316ca916bccc5d38e75826b2b54e20ec48b5dfd38ba36a02a322618556fa82`,
`ex1/inline-expiry-result.json`
`8c8ba2089690ebffa8cd2cddb3c8595c0e84066ad2b3a34f5cb3ab6881239745`,
`native-capacity/inline-draft-result.json`
`2055ec912375f654f1418cc592615ff0c13724710d65bce715dbcdb80c53fce9`.
The canonical plan records retained inputs and earlier fixture/observer failures.
No-forwarding owned peers used only dummy tokens; owned accounts were withdrawn.

Exact remaining T11 dependencies: new-pending/immediate-publication/summary/merge
modes, supported provider condition policy and actual provider qualification.
Full original two-commit identity and atomic expected head/base/body/comment-set
conditions are not proved by preflight/causal positions. Existing fixtures do not
prove actual draft privacy, notification behavior, permissions or live schema.
Closing those claims requires the corresponding supported API condition and a
specifically scoped connected journey with causal receipts, separate normal
readback and appropriate privacy/notification/permission observations; source or
fixture checks alone cannot close them. Shared public thread/UI readback,
installed consumers and combined longest identity/receipt bounds remain
unverified. Shared writes remain unavailable. These concrete T11 limits do not
rename canonical D5. D1/D2/D3 and all previously open broad security gates remain
open. Routine implementation remains in the normal queue; no Daybreak work ran
or was marked complete.

GitHub merge update (2026-11-06): `github_provider/merges.rs` and
`runtime/native_product_merges.go` now compose ordinary merge, squash and
message-free rebase with schema-92 one-shot dispatch. Expected head is a provider
condition; base identity is bounded preflight only. No merge queue, admin bypass,
manual marking or unsupported method is synthesized. A lost public reply retains
the journal's causal result; current merged state never attributes an unknown send.
Real authorized disposable-repository merges, normal readback and restart passed
using a gh-sourced grant in native custody. Real PG migration/concurrent fence/
cancellation/restart/WAL checks passed. Evidence:
`/home/vince/tmp/fgw-1106.cqnVjg/{live2/connected-merge-result.json,pg-merge.log}`.
This closes the merge implementation dependency, with the stated supported API
conditions. Fresh Factory OAuth scope, remaining review/inline behavior,
capability/UI/installed qualification and all previously open broad gates remain
unverified. No Daybreak work or security gate completion is claimed.
