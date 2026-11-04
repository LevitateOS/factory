# Factory products implementation plan

Status: implementation in progress, 2026-11-03. The owner selected Linux first
with Tauri. The source now lives in the public Factory repository. Shared UI,
Storybook, native lifecycle and provider/service producers are implemented;
connected execution and installed product journeys remain unfinished.

## Outcome and scope

Deliver two hosts for one Factory product: the Fountain extension inside Soda
Forge and an installable Linux desktop application, followed by macOS. Both consume the same Lit
feed, issue, PR, conversation and review components. Factory remains a Go engine;
Rust owns native execution, credentials, the complete authenticated desktop
GitHub connector and desktop lifecycle. Issues and PRs
remain authoritative records on Fountain or GitHub.

Desktop must run work on the host computer without a CoreOS container, Fountain
installation, Docker installation or manually administered database. Connecting
desktop to an existing Soda Forge is also supported. Storybook consumes the
production components; it is the development workbench, not the sharing mechanism.

This is a targeted product-extraction plan. Canlang, the appliance installer,
unrelated applications, the predecessor/Updates platform and a complete repository
correctness audit are outside scope. Their existing ownership remains intact.
Windows desktop distribution and multiple independent controllers for one GitHub
repository are outside the first release. Native qualification must run on the
selected platform. Later macOS qualification requires the actual supported Mac
architecture; cross-compilation is not that evidence.

## Read the plan

| Document | Owns |
| --- | --- |
| [Seams and source map](seams-and-source-map.md) | Current evidence, canonical owners, conflicts and workflow boundaries |
| [Reuse decisions](reuse.md) | Existing source donors, upstream libraries, licenses and selection spikes |
| [Selected Go and Rust allocation](language-allocation.md) | Adopted responsibility owners, audit evidence, rejected alternatives and required cutovers |
| [Target tree](proposed-tree.md) | New defining files, retained owners and cutover joins |
| [Implementation tasks](implementation-tasks.md) | Canonical slice dependency graph, sequential queue, progress and completion journeys |

## Product decisions and selected engineering approach

The owner selected separate products in the same repository, a native desktop
experience with local execution, shared changes across desktop/Forge, automatic
repository-driven work, and first-class issue/PR screens. The following are
engineering selections for this plan, rather than claims of implemented behavior:

1. Follow the [selected language allocation](language-allocation.md): Go owns
   durable product workflows, domain state and product SQL transactions; Rust
   owns native execution, resources, credentials and complete authenticated
   desktop provider connectors. Use Lit throughout the product UI. Keep one
   defining owner per responsibility.
2. Use Tauri as the native shell with a bundled Go executable. One Rust supervisor
   owns the local database, executor and backend lifetime. Close-window keeps the
   application running; explicit Quit stops admission, retires work, then stops
   the backend and database. No launch agent or always-on daemon initially.
3. Preserve PostgreSQL for the first desktop store. Privately provision a native
   per-user cluster, with no user database setup. Prove packaging/start/recovery
   early. SQLite is a separate semantic port, not a driver substitution; do not
   combine it with this extraction without evidence that the native PG path fails.
4. Canonical repository identity is a local stable ID mapped to provider instance
   plus opaque remote repository ID. Credential connections and grants are
   separate. Reauthentication must not create another repository/work ledger.
5. Shared issue/PR views become the normal Soda product navigation in both hosts.
   Existing native provider URLs remain explicit external destinations. The
   current Fountain protocol allows a same-repository route cutover without
   replacing its native issue/PR handlers; see the source map.
6. Prefer Pierre Diffs through its vanilla API, subject to an early browser,
   WebKit, CSP and review-anchor proof. Keep one renderer after selection.
7. GitHub desktop uses authenticated current-state polling and wake reconciliation
   initially. Events prompt fresh reads; event delivery does not grant execution
   authority. Unsupported conditional operations remain visibly unavailable.
8. A desktop connected to Fountain uses an authenticated collaboration gateway
   for its private operation protocol, including candidate artifact transfer.
   Private service admissions and appliance credential files never leave it.
9. GitHub automation initially acts as the connected account. The interface names
   that account. A shared GitHub App signing key is never bundled with desktop.
   Bot installation identity is a separate deployment mode requiring trusted
   token issuance or an operator-owned app configuration.

## Separate public repository

The owner requested a public [Factory repository](https://github.com/LevitateOS/factory)
before finishing the product. The initial import commits preserve the current
product source and both complete integration working-tree patches in Git history.
Original checkouts were preserved at import. Recovery patches and inactive
archived coordinator tests are now retired from the working tree. Source
provenance and the historical snapshot commit are recorded in
[`docs/source-import.json`](../../source-import.json).

The extracted Go module is `github.com/levitateos/factory`. Its public `pkg/`
owners include the engine, services, store, application, collaboration and required
identity/project definitions. The native server is the default build; its
dependency graph excludes SodaOS, Fountain and the extension SDK. Rust has its
own desktop/runtime, Unix transport, workbench and native-check workspace. Shared
UI/Storybook inputs include the actual fonts, theme, portraits and notices.

T04-REPO-GO now switches Forge to the pinned published Factory module and removes
its duplicate Go owners, preserving appliance composition and native Fountain
adapters. Forge commit `a1cd0499` passes production builds, integration compilation,
vet, dependency checks and the real issue-editor/native write/recovery journey.
The broader SQL-locality check remains red on existing Forge adapter/test usage;
no exemption or full-source pass is claimed. T04-REPO-UI now removes the old UI,
Storybook and shared-design/build copies and consumes Factory through a standard
Bun source link. Forge commit `5e695dfb` passes actual asset/physical-source graph,
single-Lit, browser/native-write and native staging checks. Local setup uses an
editable checkout; clean/CI setup fetches the published commit selected by go.mod.
The dev browser activation refused because no development deployment is active,
so deployed parity remains unqualified. T04-REPO-NATIVE now removes the remaining
Rust runtime/desktop/workbench/Unix-transport copies in Forge commit `6bb9b60d`.
Six retained native consumers use the defining Factory Git crates at the same
published source revision. Workspace/driver compilation and Cargo graph checks
pass; the rebuilt Forge driver passes the actual managed PG/Go retained-data,
restart, crash, key-rotation and ordered-shutdown journey. Generated schemas and
unrelated staged work remain preserved. This proves local native source
consumption, not connected credentials/execution or an installed package.
Installed parity remains later. Fountain's SDK distribution remains its owning integration
task; the SDK is not copied into the standalone dependency closure. The inactive
historical coordinator-test archive is retired and remains recoverable in Git
history. Existing store checks remain in `pkg/store/`.

Do not mark connected, installed, extraction-parity or product gates complete
because the new repository builds. The owner supplied the public GitHub OAuth
client ID, and T06-GITHUB-DEVICE now qualifies a real unapproved challenge plus
the private native authorization transport. T06-GITHUB-CUSTODY retains complete
encrypted grants and enforces access/refresh expiry at native use.
T06-GITHUB-ROTATION reserves one durable attempt and conditionally publishes a
complete replacement without replaying uncertain refreshes. T06-GITHUB-REFRESH
wires fixed-origin exchange and fresh account verification to that custody owner,
qualified with actual provider refusal and declared positive responses.
T06-GITHUB-SIGNIN now provides native desktop sign-in/browser handoff, cancellation
and saved-account controls, qualified with actual WebKit and an unapproved GitHub
challenge plus declared positive authorization. T06-GITHUB-LIVE now additionally
qualifies owner-approved sign-in, verified identity, encrypted grant reload, two
authenticated repository pages, private repository detail and an actual successful
provider refresh followed by another authenticated read. The isolated test account
was removed locally; remote OAuth revocation has not been exercised.
T06-GITHUB-IDENTITIES now atomically records observed account/connection bindings
and repository associations in the existing registry, preserving reconnect/rename
and refusing scope changes or unadopted retained data. Real native PostgreSQL
checks prove concurrency, rollback, restart and WAL recovery. T06-GITHUB-BINDING now consumes
these mappings through runtime-owned profile/store composition and current native
credential/account verification. Typed canonical account and neutral repository
pages preserve scope and reject cross-profile use. Actual native acceptance
uses declared positive provider facts plus real GitHub 401s; successful live
canonical collaboration remains a consumer. T06-GITHUB-CANONICAL-ISSUES now
provides host-scoped neutral issue lists/detail and canonical participants,
preserving full Markdown, nullable metadata and empty filtered continuations.
Actual native acceptance uses declared positive issues plus real GitHub refusal,
with cross-provider/profile checks, rename and restart. Discussion, PR/review/check
normalization, successful live canonical issues and connected UI remain consumers.
Connected feed and installed journeys remain unfinished;
they do not block independent Fountain implementation or source preservation.

The selected language allocation now requires the complete authenticated desktop
connector in Rust. The queue maps N1 to T05-CONTROL, P1/C1 to T06-GITHUB-RUST,
N2 to T06-CONNECT-PORTS, T06-CONNECT-PRODUCT, T06-CONNECT-READS,
T06-CONNECT-BRIDGE and their desktop consumer
T06-CONNECT, N3 to T09-NATIVE-PROCESS and T09-NATIVE-EXECUTOR, and N4 to
T09-EXECUTION. The N2 port
producer gives Go services fresh semantic callbacks into the joined headless
Rust owner, scoped to one finite native account/repository operation. It retains
Go's canonical identity, service admission and transactional product owner;
the product binder now maps fresh account and optional selected repository facts
transactionally and serves shared core Factory APIs with current per-resource
admission. Shared canonical issue/PR/discussion/review/reply-group/file/check
readers now consume Rust observations, with Go atomic participant/fork identity
mapping and neutral projection. Fork references grant no connection access.
Scoped cursors preserve pagination, pins and nullable evidence. REST root groups
leave resolution/anchors unknown; jobs and branch rules are the explicit required
T06-GITHUB-EVIDENCE producer after CONNECT and before the final provider gate.
The native per-window bridge now admits account/repository connections, obtains
canonical Go binding, and supplies cancellable bounded product IPC. Its actual
approved Linux Tauri/WebKit journey verifies discovery, selected canonical scope,
feed/issue/PR reads, cancellation, disconnect and reconnect; private access and
refresh also pass. The shared application's selectors/context and installed
qualification remain consumers.
Direct production-server evidence distinguishes declared semantic facts from
actual approved Rust connector reads; neither claims connected UI qualification.
T05-CONTROL is complete: bounded product I/O has a separate joined native owner,
desktop calls release the lifecycle mutex before awaiting, cancellation retires
polling I/O, and native product routes enforce captured instance scope. Actual
Linux acceptance holds 16 requests while exercising health, cancellation,
ordered stop, child failure and Quit. Existing sign-in/canonical-read acceptance
also passes after transient upstream transport failures were recorded. The Rust connector is now the defining native owner: OAuth/refresh/identity/HTTPS
and all current provider-local readers moved together, and the Go SDK/token routes
are removed. Go receives bounded nonsecret facts, maps each registry batch in one
transaction and retains all product authority/SQL. Actual approved native reads,
private access, refresh and retained canonical identity pass; Linux Tauri challenge
and cancellation also pass. Remote revocation, connected UI, native execution,
automation and installed journeys remain separate gates.

Extraction checks on 2026-11-03, using the imported dependency versions:

- The native Go executable builds with no special build tag; `go vet ./...`
  and the transitive provider/appliance boundary check pass.
- The production shared UI emits 20 assets from 76 shared sources. Desktop UI,
  TypeScript, Lit analysis and the existing Go/TypeScript test-root guards pass.
- Storybook builds 195 entries, including all five issue-editor states.
- The independent Rust workspace checks, and the Linux Tauri `native-ui`
  executable builds using the existing owned GTK/WebKit sysroot. This is build
  evidence, not a new window/input or installed-app qualification.
- Factory's own PostgreSQL driver, Go backend and store integration binary pass
  the real managed native journey: retained transactions/restart, duplicate-owner
  refusal, backend/database/owner crashes, startup failure, key rotation and
  ordered shutdown. The new private profile is retained and stopped.
- Both complete recovery patches pass `git apply --cached --check` against their
  recorded base revisions using separate temporary indexes. The original
  worktrees and indexes are not modified.

Local extraction logs are under
`/home/vince/Projects/.factory-extraction-checks/`; the owned stopped native
profile is `/home/vince/Projects/.fx-pg1`. These local artifacts and private
profile are excluded from the repository. No real provider login, model run,
issue/PR mutation or deployment was performed for the extraction.

## Decisions that gate specific work

| ID | Required conclusion | Dependent work; independent work can continue |
| --- | --- | --- |
| D1 | Adopt an explicit GitHub standing automation policy over current verified facts, or retain original-author creation evidence as mandatory. Recommendation: the former, with eligible authors/forks, actor, repository, limits and accepted content revision recorded. | Automatic GitHub intake activation, not read/write UI or connector development. Until resolved, discovered work waits for explicit maintainer adoption. |
| D2 | Prove Fountain desktop enrollment/login and revocation using supported native authentication. Existing OAuth PKCE code is evidence for a spike, not proof the new gateway is admitted. | Remote Fountain operations; no spoofed browser authority or raw Unix socket exposure. |
| D3 | Require a native worker identity isolated from the desktop user's files and keyring. Keep the headless process primitive outside the desktop dependency graph and unavailable to product execution. Qualify worker UID, filesystem and process visibility, session/keyring access, exact worktree mounts, credential handoff/withdrawal, role capabilities and retirement on the actual launch path. A namespace mapped to the desktop user's host UID alone does not satisfy this gate. | Selectable native execution and unattended work, not desktop browsing or Storybook. |
| D4 | Select native PG packaging and diff renderer from the early spikes. Resolve fresh official artifacts and actual target behavior. | Shipping those components, not the independent extraction packets. |
| D5 | Current repository extension admission requires code-unit read. Issue-only collaborators require a Fountain capability change for full native visibility parity. | Full collaborator parity; initial Factory/code-reader journeys can use current extension routes. External Fountain edits need their own authorized implementation scope. |

These are precise implementation gates, not requests to stop planning. The task
list includes their producers and useful work that proceeds independently.

### D3 native-agent isolation decision

The user selected isolation from the desktop user's files and keyring. The
current Rust process primitive is a verified resource/retirement probe, not an
admitted agent executor: it launches under its caller's host UID. It has moved
to `lib/native-process`, outside the production desktop runtime dependency
graph. The production Go native server starts no execution coordinator and
continues to report its missing execution ports as unavailable.

The selected installation model permits a privileged setup step for a dedicated
system worker. The executor should run as a separate host UID under a root-owned
system service, with its own private state and no desktop login session. A narrow
broker must authenticate the desktop peer and bind each launch to the admitted
profile, role, run and worktree; the desktop process must not be able to choose
arbitrary unit properties, host paths or credentials. The worker must receive
only the approved run workspace and scoped capabilities, never the desktop home
or Secret Service socket. A systemd unit or socket file alone is not this broker;
its protocol and the live negative probes below remain required.

Before a native coding agent becomes selectable, the N3/N4 path must prove:

- The worker has a distinct host identity and cannot read the desktop user's
  private files, Secret Service session bus or kernel keyring, inspect its
  processes, or modify the desktop user's cgroup scopes. A user namespace that
  maps back to the desktop UID is insufficient. Grant only the exact run
  worktree and required system/runtime paths; verify this with actual negative
  reads and an unrelated private-file canary.
- The native owner keeps provider account secrets, Git credentials, collaboration
  tokens and the MCP signing key outside the worker. It brokers only scoped,
  revocable capabilities needed by the admitted run. Withdrawal, owner crash and
  restart must deny new use and retire or reconcile old capabilities without
  claiming that direct-child exit proves credential return.
- The actual worker path enforces the admitted role's tool set, resource ceilings,
  deadline, process-tree retirement and exact candidate capture. A real finite
  provider run, plus denied role/file/keyring probes, is required for D3 closure.

No current N3/N4 consumer satisfies these checks; D3 remains open and native
agent execution remains unavailable to the product. The earlier same-user acceptance evidence
continues to cover only process and cgroup behavior.

On 2026-11-04, a transient system service with `DynamicUser=yes`,
`ProtectHome=yes`, `PrivateTmp=yes`, `ProtectProc=invisible`, private IPC/devices,
strict system-file protection, no new privileges and no ambient capabilities
ran under UID 61609. It could not read an existing `0600` desktop canary under
`/home/vince`, see `/run/user/1000/bus`, read the desktop process environment,
or inherit session-bus environment variables. A separate worker probe showed
UID 65518 in the initial user namespace, distinct from the desktop process's
namespace and UID. The extracted native process acceptance binary then ran in
the same transient service profile with delegated cgroup controllers and passed
all retained finite process, PTY, deadline, cancellation, descendant, output,
owner and uncertain-retirement checks. The probe binary was removed from `/run`
afterward. These checks prove the selected system-worker profile is feasible on
this host; no production broker, worker admission, worktree transfer, scoped
credential delivery, role enforcement or provider run was exercised.

An inert `cmd/soda-factory-worker` boundary is now in the Rust workspace, still
outside the desktop runtime dependency graph. Its systemd templates admit one
connection per dynamic UID; the binary requires peer credentials, a different
non-root UID, no-new-privileges and a private mode-0700 runtime directory. The
live rendered socket had a root-owned parent and desktop-owned mode-0600 entry;
an attestation ran as UID 61739 for a UID-1000 peer. Extra-path and launch
requests were refused, and the temporary installation was removed. No execution
request exists. D3 remains open until the actual N3/N4 workspace, role,
credential and retirement path passes its required negative and provider checks.

The worker unit now explicitly uses a private kernel session keyring. A fixed
diagnostic executable under that same socket-activated service profile ran as
UID 61739: the nonempty mode-0600 desktop file and connecting desktop process
environment were denied, `/run/user/1000/bus` was hidden, and access to UID
1000's persistent keyring was denied. The same-UID keyring control succeeded,
so the denial was not a broken `keyctl` invocation. The temporary diagnostic
installation was removed. The actual agent launch path still needs this check
alongside role, workspace, credential and retirement evidence.

The cross-UID executable transfer also has a concrete host constraint: SELinux
denied the system worker receipt of a desktop-created sealed memfd labeled
`user_tmp_t`; `SCM_RIGHTS` control data was truncated. A same-UID control
passed. `lib/native-process` now supports a bounded byte stream that the worker
hashes and seals into its own memfd. Under the actual socket service profile,
an externally captured ELF whose desktop source was replaced ran as UID 61739;
the worker could not read the original desktop path, and its cgroup tree
retired. Wrong digest, non-ELF header, truncated payload and oversized declared
length were refused. This establishes a viable executable import primitive;
scripts/provider bundles, admitted run binding, workspace, role, credentials
and a real provider run remain unqualified.

The observed installed Muse 1.4.14 binary is a 415 MB static ELF, above the
old 128 MiB process-capture ceiling. Capture and worker import now share an
explicit 512 MiB ceiling, and the desktop side can stream only the sealed
captured bytes. The actual Muse ELF crossed the isolated socket, ran its finite
`--version` command as the worker UID, exited zero and retired its process tree.
That version check used no provider account, model or real task and does not
close the remaining credential, role, workspace or provider-run gates.

The headless process owner now supports a bounded, zeroized stdin pipe while
keeping output on the PTY. A disposable user-delegated systemd scope verified
the finite pipe read, absence of input in the PTY log, unchanged all-PTY path,
zero child exits and retired cgroup trees. This permits a run placeholder to
reach Muse's `--api-key-stdin` without putting the account token in the worker.
The worker launch, actual systemd UID, mounted workspace, role policy and
provider task remain unqualified.

A subordinate-UID namespace check then exercised the actual Muse 1.4.14
`exec` binary under host UID 524288, distinct from desktop UID 1000. A
root-private fixture Git checkout was bind-mounted read-only. The worker
read the mount but could not reach the original source, write or chmod it,
read a desktop canary or process environment, see the desktop session bus,
or obtain the desktop keyring. With reviewer flags and a dummy stdin key,
Muse requested `/muse-code/models` from a private-loopback fake provider and
stopped on its 401. This is an actual CLI/filesystem/UID probe, not an
installed systemd worker, owner-keyring/relay composition, admitted role or
real provider task. The existing `Accept=yes` worker instance names a socket
connection rather than a run and cannot carry an exact `%i` checkout mount;
the production agent needs a separately broker-started run service.

The broker now has an exact `arm_agent` declaration tied to its staged bundle
and captured tool and admitted prompt digests, prepared candidate, active relay
and one fixed role.
It arms only a run-specific
root-owned socket. A separate dynamic-UID service checks the root role declaration and
read-only Git mount, then responds only to an attestation request; execution
remains unavailable. The broker stops that socket/service before relay and
workspace retirement and recovers a partially written declaration. The typed
desktop client checks the broker's bounded reply and distinct worker UID. Source checks
and rendered unit syntax passed on 2026-11-04. A disposable system activation
then returned an exact reviewer attestation from a distinct host UID. Under
the same unit profile, desktop file, process environment, session bus,
persistent keyring and original checkout reads were denied, as were writes and
chmod on the read-only mount. Its network namespace matched a mock relay and
differed from the host; an unknown path field failed without a reply. The
temporary units and dummy workspace were removed. The actual broker arm and
retirement chain was then exercised with the real broker, preparer, relay and
agent units, a dummy Git bundle and a credential-free owner socket. Wrong-role
replay initially stopped the valid socket; the broker cleanup path was fixed
and a repeat refusal left the socket active and attestable. Wrong-digest
retirement preserved the run. Exact retirement stopped both units and removed
the staged bundle and prepared checkout; replayed retirement found the run
absent, and agent attestation could not revive it. Temporary system state was
removed. Admitted role-limited tool execution, coder workspace use, credential
withdrawal, cgroup boundary probe and a finite provider task remain D3
requirements. The agent now also opens the headless Rust process owner under
its exact run cgroup before attesting. A disposable `DynamicUser` service with
`DelegateSubgroup=init` enabled only `cpu memory pids` in its own parent cgroup;
the desktop user's cgroup was unwritable to that UID. The actual agent unit
attested after acquiring its process owner. Removing that subgroup from the
disposable unit produced no reply and a delegation refusal; restoring it
restored attestation. This is a process-scope prerequisite, not a child launch
or proof of process-tree retirement on an agent task.

The actual isolated agent service now makes a separate coder checkout beneath
its private worker-owned runtime from the exact read-only prepared source. It
uses captured, bounded Git processes and checks worker ownership, mode `0700`,
writability, exact candidate HEAD and clean status before attesting. A live
disposable unit activation returned `coder_workspace:true` for coder and
`false` for reviewer; each run had a worker UID distinct from the desktop UID
and still reported execution unavailable. The mock namespace, units, source
and runtime state were removed. The production Go admission, role-scoped agent
tool execution, credential custody and finite provider task remain D3 work.

A simultaneous two-instance check exposed that systemd reused one dynamic
host UID across instances of the same template. The agent service now overlays
the shared runtime parent and host cgroup tree, binds only its own private
runtime and delegated unit cgroup, and uses a private PID namespace. Two
concurrent dummy instances sharing UID 62475 could see only their own runtime
and cgroup mount, had distinct PID namespaces, and enabled their own
`cpu memory pids` controllers while the host user cgroup tree was hidden.
The updated actual agent unit attested its dummy reviewer source after opening
`Processes` with those fixed mounts; a failed first activation showed
`LISTEN_PID` had to be interpreted in the host PID namespace, and the worker
now checks the inherited listener itself. All disposable units and source
were removed. A later disposable activation used the actual agent unit
profile with only `ExecStart` replaced by a diagnostic binary. It launched a
captured finite child through `Processes`, observed zero exit, complete output
and a retired tree, then cancelled a second invocation after it spawned a
background child and observed `Cancelled` with tree retirement. Its process
owner retired and the service exited successfully. The diagnostic binary,
unit and dummy source were removed. Actual concurrent execution, cross-run
denial, role-limited tools, credential withdrawal and a finite provider run
on the production launch path remain D3 requirements.

The production agent's fixed Git HEAD admission check now uses its native
process owner instead of an ordinary `timeout` subprocess. The captured Git
binary runs with a three-second deadline, 4 KiB output limit, resource limits
and required process-tree retirement. A disposable exact-run socket activation
attested the dummy reviewer candidate; changing the root role declaration to
a different valid candidate failed with `agent source candidate invalid` and
no attestation. Repeated refusal hit systemd's start limit. The temporary
socket, units, binary and source were removed. This closes the unbounded
admission subprocess gap; agent execution and D3 remain open.

The earlier root-private reviewer attester's Git HEAD check now caps stdout at
42 bytes and runs through a three-second `timeout` with a one-second kill grace,
within its 15-second service lifetime and control-group kill. A disposable
activation of its actual socket and service attested the dummy candidate as a
separate UID and root peer; the temporary socket, unit, binary and checkout
were removed. The reviewer remains credential-free and attestation-only.

The fixed checkout preparer Git calls now run through a 20-second `timeout`
with a one-second kill grace inside its 30-second systemd start deadline. Its
HEAD check reads at most 42 bytes, refusing output beyond the exact candidate
line. A disposable activation of the actual preparer unit cloned a 289-byte
local bundle to the expected candidate as a distinct worker UID. The unit,
binary, bundle and checkout were removed. This bounds preparation subprocess
output; D3 agent execution remains unavailable.

Checkout preparation now clones without materializing files, verifies HEAD,
and bounds a NUL-delimited `ls-tree --long` listing before `reset --hard`.
The actual unit prepared a normal bundle and refused an 81,810-byte bundle
containing an 80 MiB zero-filled file before that file appeared; the partial
repository used 176 KiB. This is a raw blob-size preflight, not an exact disk
quota. A separate local `.gitattributes` `ident` probe expanded a 1 MiB blob
to 10,066,321 worktree bytes. The preparer now queries effective attributes
from `HEAD` for every path before checkout, budgets conservative `ident`, CRLF
and supported UTF-16/UTF-32 expansion, and refuses other worktree encodings.
Under the actual unit, the 1 MiB `ident` fixture prepared cleanly, while an
8 MiB `ident` blob was refused before materialization; the partial repository
used 112 KiB. A UTF-16LE-BOM plus CRLF fixture prepared cleanly under the
actual unit. The temporary service state was removed. Filesystem allocation
and repository metadata still require a strict storage quota or a separately
proved upper bound for D3.

The preflight now also counts distinct parent directories before checkout.
A 3,318-byte bundle declared 600 files beneath about 24,000 parent directories;
the actual preparer unit refused it with `reviewer tree namespace bound
exceeded` before creating any branch directory. The partial repository used
100 KiB; the disposable state was removed. This bounds namespace expansion,
while a strict total-storage quota or equivalent upper-bound proof remains
open for D3.

The host `/run` is tmpfs with swap enabled. A disposable 32 MiB systemd
memory-cgroup probe with `MemorySwapMax=0` OOM-killed a 128 MiB writer at
32 MiB peak and 32,210,944 bytes written. The actual preparer unit now sets
`MemorySwapMax=0` alongside `MemoryMax=128M`; a rendered unit check reported
both values and prepared a tiny exact candidate successfully. The temporary
state was removed. This bounds output charged to one preparation activation;
aggregate retained checkouts across runs and available `/run` capacity remain
security gates.

The broker now checks `/run` is tmpfs and, under its run lock, reserves the
larger of 25% of total space or 256 MiB plus 128 MiB for each new preparer
activation. It rechecks the reserve before retaining the checkout; the
preparer verifies its bound output mount is tmpfs. A disposable actual broker
socket staged a tiny local bundle and prepared the exact candidate through
these checks, then its units and run state were removed. Low-free-space
refusal under a constrained mount and races with unrelated `/run` writers
remain unverified; this is an admission reserve, not a global hard quota.

The reviewer workspace needs a manager-owned mount boundary: the dynamic worker
UID owns its private runtime directory, so file modes on a checkout it owns do
not keep that checkout read-only. A transient system service on 2026-11-04 first
ran as `nobody` against a file owned by `nobody`; it could both write the file
and change its mode. The same identity, with the same file and no capability
bounding set, then ran with `BindReadOnlyPaths` on its containing directory. It
could read the file, while the write failed with `Read-only file system` and
`chmod` failed. The temporary source and both service instances were removed.
This proves the mount mechanism on this host, not reviewer admission. The
privileged broker must select a root-controlled exact run snapshot and install
the read-only bind for a reviewer instance; the desktop peer must not supply a
mount source, destination, or unit property. The coder needs a separate writable
run workspace. Both roles remain unavailable until that broker and the actual
role/file probes are implemented.

The source also needs a private parent with a readable child. A follow-up
transient unit bound the mode-`0700` root-owned parent itself and the worker
could not read the mounted file: bind mounting does not override the source
directory's mode. Binding its mode-`0755` child into the service instead let the
unprivileged worker read the mounted file, while traversal through the original
root-private parent and writes through the mount were denied. That layout keeps
the original run source inaccessible to other worker instances while exposing
only the selected child at the per-run mount target. The temporary source and
transient units were removed.

The observed Muse 1.4.14 `exec` interface can read an API key from stdin and
can override its provider base URL. Supplying the owner's reusable key over
stdin would put that secret inside the worker and fail the selected custody
rule. A brokered provider endpoint is a possible route, but its protocol,
network confinement, per-run authorization, withdrawal and actual model call
remain unproven. The base-URL option alone is not a credential boundary.

A transient network comparison used two known reachable loopback listeners.
Without an IP policy, an unprivileged system service reached both. With
`IPAddressDeny=any`, it could not connect; with only
`IPAddressAllow=127.0.0.1/32`, it reached the listener on `127.0.0.1` but not
the one on `127.0.0.2`. Each connection was bounded by `nc -w 2` and a
four-second process timeout; a preceding unbounded TCP-open probe had to be
stopped and is not evidence of a successful policy check. The listeners and
transient units were stopped. This proves an address allowlist is enforced on
this host, but allowing `127.0.0.1` permits other services on that address; it
does not yet confine an agent to one authenticated broker endpoint or prove
provider credential withdrawal.
The inert worker service now sets `IPAddressDeny=any` so it has no inherited IP
egress while it only serves attestation. Its rendered units passed verification,
and a live Unix socket activation still attested worker UID 61739 for desktop
UID 1000 with execution unavailable. The temporary installation was removed.

An empty-workspace, `--no-session-log` Muse `exec` probe used a dummy stdin key
and a local fake provider that always returned HTTP 401. With `META_API_KEY`
removed from that process, `--base-url` routed `GET /muse-code/models` and
`POST /responses` to the fake endpoint; the endpoint checked that each
authorization header matched the dummy key without logging its value. This
confirms the observed CLI can route these requests through a broker while
carrying an unprivileged placeholder credential. The fake endpoint, scratch
workspace and listener were removed. It does not establish all provider
routes, proxy token replacement, live account authority or a finite model run.

A later dummy-key Muse 1.4.14 check disabled reminders, user input, shell,
writes, web tools and foreign personal context. It observed one
`GET /muse-code/models` followed by `POST /v1/responses` with `stream:true`
and a 107,066-byte request body. The exact path depends on the configured
base URL. The fake endpoint returned 401, so no model ran. This confirms a
whole-response eight-second credential callback is insufficient as the sole
provider boundary. The probe listener and scratch files were removed.

The native credential owner now has a finite streaming callback. It borrows
the token only inside Rust, renews and rechecks the same OS keyring revision
every two seconds, and drops the callback on local withdrawal, replacement,
owner retirement or deadline. An independently reopened keyring owner is
detected at the next recheck. A fresh isolated GNOME Keyring run proved those
refusals, invalid duration bounds, callback drop, and a current stream
continuing past its original 30-second admission lease while the old short
operation was refused. The isolated keyring processes and fixture state were
removed. This is a credential-custody prerequisite, not a worker proxy or a
real provider run; D3 remains open.

The native owner now has a bounded provider-request policy primitive. A
dummy-key Muse 1.4.14 probe showed its one-step coder request advertises
workflow, memory and monitor functions and asks for 128,000 output tokens;
the reviewer launch flags also left several of those schemas advertised.
The policy filters the actual request's nested Muse tools by reviewer/coder
role, pins the selected model and fixed routes, requires streaming and no
provider storage, caps each response to 4,096 output tokens, and consumes a
finite per-run byte/token/call budget. The actual 117,775-byte coder request
passed after filtering; wrong model, stored response, built-in tools, extra
routes, retired run and exhausted budget failed. The local fake provider
returned 401 and no real credential was used. This policy has no socket or
credential-bearing caller yet, so it does not qualify the owner proxy or D3.

The first privileged workspace-transfer component is now
`cmd/soda-factory-broker`. It is a root-owned, socket-activated staging service
with no launch request: it admits only a desktop-UID peer and a bounded Git
bundle byte stream keyed by a 32-hex run ID and SHA-256 digest. It has no host
path, role, credential or unit-property input. It stores up to 64 exact bundles
under a root-private state parent, with each finished child readable only via a
manager-selected bind mount. Its systemd profile has no capabilities or IP
access and a 15-second lifetime. The rendered units verified, and a transient
installation staged and identically replayed a tiny local bundle while refusing
changed bytes, wrong digest, invalid run, extra path, oversize and truncation.
An unprivileged service could clone that bundle from the selected read-only
mount but could not traverse the original source. The temporary installation
and state were removed. The broker does not parse Git as root or authorize a
role, snapshot, worker launch, credential, active-run retirement or provider run. D3
remains open; this staging endpoint is not installed by the current desktop.

A separate transient two-identity reviewer checkout probe then mounted a small
Git bundle read-only into a preparation service running as `nobody`. That
service cloned into a designated writable child beneath a root-private parent;
Git itself did not run as root. After the root-owned child was frozen, a second
service with `DynamicUser=yes` ran as UID 61755 with only the finished checkout
bound read-only. It read the candidate file, could not traverse the original
root-private path, and could neither change the file nor create another. The
temporary source and checkout were removed. This proves a possible preparation
layout, not current run admission. Production needs a preparation UID isolated
from unrelated processes and concurrent preparations, a root-controlled fixed unit and
mount selection, exact Git HEAD/commit validation, and retirement of the
staging/worker lifetimes before this role boundary can be used.

`cmd/soda-factory-broker` now also builds `soda-factory-preparer`, a fixed-path
unprivileged checkout preparer. Its manager-selected source bundle and empty
output directory are the only filesystem inputs; arguments carry only the run
ID, expected candidate commit and source digest. It rejects invalid identity,
privilege state, ownership, modes, bundle size/digest, candidate HEAD, dirty
tracked files, special files and oversized or deep checkouts. Git runs with a
cleared environment and no inherited configuration. At that checkpoint the
production broker did not invoke it. A live `DynamicUser=yes` preparer cloned
a small test bundle from a read-only bind with an exact expected digest and
candidate. A separate dynamic reviewer UID read the frozen checkout from a
read-only bind but could not traverse the original root-private path or write,
create or chmod in its checkout. Wrong-digest and
wrong-candidate runs were refused. The latter left a partial clone; a restricted
root manager service launched a fixed cleanup service with only
`CAP_DAC_OVERRIDE` and `CAP_FOWNER` and removed it. Inspection confirmed the
partial clone was gone. These are mount and preparation proofs, not Go run
admission or an installed active-run cleanup path. The temporary binaries,
mounts and scratch repository were removed.

The size check runs after Git extraction; production must also limit the
preparer's CPU, memory, runtime and writable filesystem space before accepting
an untrusted compressed bundle.
The preparer now rejects checkout symlinks whose absolute or relative targets
escape the checkout lexically. Under the same transient isolated service
profile, an escaping-symlink bundle was refused and a contained relative
symlink bundle was accepted. The partial and successful checkouts, mounts,
service instances and scratch source were removed.

The dormant `soda-factory-preparer@.service` template now fixes the selected
source/output binds and an unprivileged dynamic identity with private network,
keyring and IPC, plus CPU, memory, task and runtime ceilings. Its proposed
broker contract required a root-private per-run manifest and empty output,
validated instance selection, frozen successful output and cleanup of failed
or retired instances. At that checkpoint the broker had no start request.
`systemd-analyze verify` accepted a temporary rendering; a live instance
prepared an exact candidate from its root-owned manifest and staged bundle as
a separate UID, then exited successfully. The unit and proof state were removed.

The dormant `soda-factory-preparation-cleaner@.service` now has a fixed Rust
cleaner that validates one 32-hex run and the root-owned preparation layout,
then removes only the stopped preparer's output and manifest. Its system unit
grants only `CAP_DAC_OVERRIDE` and `CAP_FOWNER` and write access to that run's
preparation child. It leaves the empty run directory for the broker's locked
state transition and refuses unexpected entries. A live fixed-unit check
prepared a checkout as one dynamic UID, froze it, then removed it with this
cleaner. Retry succeeded, malformed instance failed, and the source bundle
remained. `Type=oneshot` with `TimeoutStartSec` made the manager's start result
reflect the actual process exit; `Type=exec` had returned success before a
malformed instance failed. The temporary units and proof state were removed.

An unexpected-entry probe then refused cleanup before changing the checkout
or manifest, and cleanup succeeded once that entry was removed. Its temporary
state was removed.

At that checkpoint the broker had no request or lifecycle code to stop active
instances, invoke the cleaner, verify the final state or remove the empty run
directory.

The broker now accepts a bounded `prepare_reviewer` request with only run ID,
staged bundle digest and candidate commit. It verifies the root-owned bundle,
creates an exact root-private manifest/output child, starts only the fixed
unprivileged preparer unit, and freezes its successful checkout. An identical
request replays; a changed candidate conflicts. Interrupted or failed
preparation is stopped and cleaned before retry. `retire_bundle` verifies the
digest, stops and cleans that preparation, removes its empty metadata child,
then performs its existing tombstone-first source retirement. A cleaner refusal
leaves both source and checkout available for reconciliation. The broker unit's
runtime ceiling rose to 90 seconds to encompass the fixed 30-second preparer
and 15-second cleaner. No desktop-supplied mount, command, unit property or
credential enters the manager call.

The actual socket and fixed system units passed a live stage, wrong-digest
refusal, wrong-candidate failure with partial-clone cleanup, exact preparation,
idempotent replay, changed-candidate refusal, wrong-digest retirement refusal,
exact retirement and absent retry. A manually planted incomplete manifest was
reconciled and prepared successfully. An unexpected root-owned preparation
entry made retirement refuse while preserving both source and frozen checkout;
after removing it, retirement completed. Root inspection found no run or
tombstone. The temporary installation, state and scratch source were removed.
At that checkpoint this was credential-free reviewer checkout preparation only.
There was still no Go-admitted role/profile/run binding, reviewer agent launch,
provider credential proxy, withdrawal or finite provider run.

The broker now also accepts a bounded `attest_reviewer` for the exact staged
digest, frozen manifest and candidate. It starts only the run-specific
root-private `soda-factory-reviewer@<run>.socket`, then connects as root to an
unprivileged dynamic-UID reviewer service. The service receives the checkout
through a fixed read-only bind and verifies its mount flag and Git HEAD. Git's
normal cross-UID ownership check initially refused the checkout; the worker
now grants `safe.directory` only for that fixed mount. The broker validates
the worker UID differs from the desktop UID and returns
`execution_available:false`. Retirement stops the reviewer service and socket
before checkout cleanup. A live actual-socket run returned UID 61729 and the
exact candidate/read-only mount; the desktop UID was denied a direct connection
to the private reviewer socket. A changed candidate was refused, repeated
attestation succeeded, and exact retirement left both units inactive and no
source or checkout. Post-retirement replay failed. The temporary installation
and scratch state were removed. This proves a run-specific read-only reviewer
mount and private socket, not Go admission, an agent run, keyring/file probes on
that agent, credential withdrawal or a real provider task. D3 remains open.

The preparer's lexical symlink check had a chain escape: `d/alias -> ..` and
`d/escape -> alias/../..` appear contained one link at a time but resolve
outside the prepared checkout. It now resolves the full chain under its
private checkout and refuses outside or dangling targets. The actual
preparer system unit refused that escape with the intended error, while a
contained chain prepared successfully. Temporary units and source were
removed. This closes that checkout-input escape, not D3's admission,
credential or provider-run gates.

The subsequent review found that even a directory symlink resolving within
the checkout could be followed by `..` during an agent file operation and
leave a lexical workspace boundary. The preparer now refuses directory
symlinks and accepts only links resolving to regular files inside the
checkout. Kernel-enforced beneath-root resolution remains necessary for
actual agent file tools. The actual preparer unit refused a directory link
with the intended error and prepared a contained file link; temporary proof
state was removed. The native execution gate stays closed.

The credential-free provider relay template now provides a fixed Unix-socket
to private-loopback bridge for one run. A live temporary systemd check used a
fake owner with no credential: host loopback could not reach the relay, and a
different dynamic UID joined its private network namespace and reached the
fake owner as UID 64727. That joined service had no usable owner Unix socket
mount; systemd's regular-file bind placeholder was removed afterward. The
relay checks the desktop owner's socket and peer UID and bounds traffic and
lifetime. This qualifies only the transport shape. The trusted desktop owner
still needs a run-bound provider policy and revocable token use; the broker
must construct and retire exact agent unit instances, and an actual confined agent
must pass role, file, keyring and finite provider-run checks. D3 remains open.

The actual broker now starts that relay only for an exact staged digest and
prepared candidate, and reports its distinct dynamic UID while still marking
execution unavailable. A live fixed-unit check refused wrong digest,
candidate and owner-socket permissions; systemd waited for relay readiness
after its own socket checks. Wrong-digest retirement preserved the service;
exact retirement stopped it, removed the run and denied replay. Temporary
units, sockets and state were removed. Go run/role admission, desktop owner
integration, agent launch and the finite provider task remain unqualified, so
D3 remains open.

The desktop-owned provider socket now gates requests by the relay's exact UID,
a run-specific placeholder, native Muse request policy and a fresh OS keyring
revision. It sends only to fixed Meta HTTPS routes and bounds requests, wire
responses, concurrent work and time. An isolated GNOME Keyring and subordinate
host-UID check confirmed direct desktop connections were reset; the admitted
UID saw 400 without the placeholder and 403 for an out-of-policy Responses
body. An invalid fixture token reached Meta's model catalog and yielded a
sanitized 401. Local withdrawal made the same request return 503; stopping
removed the socket. The check did not exercise the actual systemd relay because
the system manager required interactive administrator authentication. The
desktop broker client now parses only root-peer, exact framed run replies; a
private mapped-UID service proved its stage/prepare/attest/retire protocol and
mismatched-candidate refusal. The production desktop caller, verified Muse
account enrollment, confined agent and finite provider task remain to be
connected and qualified before D3 closes.

A dummy-key loopback Muse probe then showed an inbound role risk: reviewer
flags rejected a scripted `write_file` call, but the CLI still attempted its
registered `read_memory` tool when the fake provider returned that function.
Factory's upstream filter would remove `read_memory` from the offered tool
list, so the desktop owner now validates every event of a complete, bounded
Responses SSE body against the exact filtered names before forwarding any
bytes to the worker. A native check accepted text and offered `read_file` and
rejected late `read_memory`, `write_file`, built-in tool items and incomplete
streams. The scratch provider and HOME were removed. Real provider streaming
behavior and the admitted worker launch still need qualification for D3.

A bounded transient manager check showed a root service with
`NoNewPrivileges=yes`, an empty capability bounding set, no IP access and only
Unix address families could ask systemd to run a fixed `User=nobody` service;
both services exited successfully. This confirms the intended manager call is
available from the restricted broker profile. It does not authorize a desktop
run or prove a safe unit-property whitelist. The broker must construct every
unit property from validated run state and must never forward request-supplied
properties or paths to systemd.

A cleanup probe changed the broker staging location. A dynamic preparer could
write a private `StateDirectory` under `/var/lib/private`, and a reviewer could
read a child only through a read-only bind. After the service stopped,
`systemctl clean --what=state` returned success but left the paths intact:
systemd's `sd-rmrf` received SELinux denials on `var_lib_t` unlink/rmdir. A
flat-name state directory had the same result. An exact root-private `/run`
cleanup probe, launched by systemd with only `CAP_DAC_OVERRIDE` and
`CAP_FOWNER`, removed a child owned by another UID. The broker now stages under
a preserved root-owned `/run/soda-factory-broker` runtime, which is ephemeral
across reboot. Its actual socket service passed staged bundle replay across
activations, rejected wrong-digest and extra-path retirement, removed an exact
run, reported an absent run on retry, and accepted restaging. Root inspection
confirmed the run child was gone; the temporary units and runtime were removed.
At that earlier checkpoint retirement removed only staged bytes because no
worker launch existed. A later checkout cleaner needed to stop the worker
first and use a fixed manager-owned action; a zero cleanup-command exit code
alone was insufficient.
The broker then made staged retirement retryable across process interruption:
it renames a verified run to a digest-bound tombstone before unlinking. Live
socket checks simulated a stop after the rename and after bundle unlink. In
both cases the exact digest retry removed the tombstone, while restaging the
pending run and a wrong-digest cleanup were refused. Root inspection found no
run or tombstone afterward. This does not yet prove active worker retirement.

The broker and dynamic-UID agent now have a one-shot launch protocol for the
exact staged run, role, candidate, captured ELF digest and prompt digest. The
root broker consumes the agent socket and writes a root-owned replay marker;
the worker imports and hashes the streamed ELF and prompt before spawning
fixed Muse CLI arguments under its private cgroup. A live disposable check
denied desktop canary access, read-only source writes and privileged listener
inheritance from the child. Altered ELF and prompt bytes were refused before
launch; caller disconnection stopped the child and agent service. The captured
415 MB Muse 1.4.14 ELF reached the private credential-free relay once and
exited after a planned 401. The worker reported a retired tree and bounded
output. Temporary system units and runtime were removed. The production Go
coordinator still does not admit this launch; credential custody and
withdrawal, coder result capture, complete role checks and a finite real
provider task remain D3 gates.

The worker execution reply now retains process invocation and start identity,
namespace PID, boot ID, start time and exit code/signal through the root broker
and typed desktop client. This is needed to bind later Go native provenance to
the process actually launched; a namespace PID is not a host PID. The fields
passed a disposable live broker-to-worker wire probe with a tiny credential-free
reviewer child, distinct dynamic UID, positive namespace PID, exit code 0,
complete bounded output and exact retirement. The first cleanup attempt lacked
the reviewer unit in the temporary installation; after adding it, exact retry
and a second full launch/retirement passed. All temporary units and state were
removed. This does not change the D3 conclusion because Go admission and real
credential custody/provider work are still unconnected.

Go's retained native evidence now distinguishes historical
`same_user_trusted_host` from `isolated_system_worker`. An isolated prelaunch
record captures the exact broker run ID; a started record also needs distinct
desktop and worker UIDs. The immutable record comparison and native checkpoint
anchor retain that broker run and UID pair, and a captured isolated launch
refuses a same-user observation or a different broker run. This is a typed
admission/evidence prerequisite. The existing checkpoint retention integration
case exercised the isolated anchor and forged broker/UID refusal through a
disposable native PostgreSQL profile, including reopen. No production Go
execution port yet consumes the broker receipt, so D3 remains open.

The active Go launch path now refuses historical same-user native admissions at
both `Run.LaunchRequest` and `LaunchRequest.Validate`; retained same-user records
remain readable. The existing native PostgreSQL launch-binding case passed with
an isolated captured broker run after reopen and refused a legacy launch. This
prevents a same-user fallback when a native Host port is later connected; it
does not itself qualify the worker or provider custody.

## Inspection identity and evidence limits

The source identities and review descriptions below describe the original
planning inspection. They are historical inputs, not identities of the current
implemented working tree. Current implementation evidence is recorded here.

### Implemented and checked

- T07-ISSUE-EDITOR supplies the shared title/Markdown editor with explicit original
  content conditions, changed-field delivery and durable draft/pending custody.
  Fountain and the standalone frame mount the same element beneath current
  primary-read/account gates. Actual browser/native SQL/PostgreSQL checks prove
  stale review, a second-client race with native cancellation, lost-reply recovery,
  original retry/cancel, title-only/empty/full-size updates and readback. Browser
  writing/account/realm checks preserve newer drafts and retired replies; emitted
  standalone port checks, five built stories and both product asset builds pass.
  Cross-provider/installed qualification and PR flows remain consumers.
- T07-ISSUE-EDIT-BASE binds original external ID/title/Markdown digest to the
  existing edit journal and trusted native preparation. Stale saves obtain a
  real cancellation tombstone before dispatch; capture-to-dispatch races retain
  native CAS. Actual Fountain/private HTTP/native SQL/PostgreSQL qualification
  covers issue/PR stale conditions, changed-intent refusal, lost cancellation and
  retained reopen, with canonical contradiction fencing. The shared editor is
  the next consumer; cross-provider and installed qualification remain pending.
- T07-ISSUE-CREATE supplies the shared title/Markdown form, current capability/
  result decoding, scoped durable writing and pending-command/result custody.
  Fountain's normal new-issue route and the standalone frame consume it, with
  exact typed navigation to the returned issue. Actual Chromium/private API/
  native SQL/PostgreSQL qualification covers creation, original retry, cancellation,
  full UTF-8 byte bounds and lost-reply recovery. Browser storage/account/realm
  checks preserve newer writing and retired outcomes; Storybook and both product
  asset builds use the production component. Installed/connected qualification
  and ordinary title/body editing remain consumers.
- T07-ISSUE-CREATE-DESTINATION returns the exact canonical issue selected by the
  original authenticated creation receipt and a fresh normal issue read. Actual
  Fountain/native SQL/PostgreSQL checks preserve that destination through lost
  replies, replay and Store/API reopen; native permission withdrawal hides the
  destination without erasing a committed result, and mid-read account retirement
  suppresses the outcome. The same checks caught and corrected the frontend
  response bound for full escaped issue Markdown. The shared new-issue form is
  the next consumer; no installed UI or deployment claim is made.
- T07-ISSUE-COMMENT supplies the shared ordinary comment composer, canonical
  capability/outcome decoding, scoped draft/pending custody and optional shared
  frame ports. Fountain mounts it under the primary issue read gate. Actual
  Chromium → frontend/private API → native SDK/SQL and PostgreSQL checks prove
  three human comments, lost-reply lookup, original-command retry and cancellation
  without duplicate effects. Browser checks preserve newer writing, cancellation
  intent, account/realm isolation and retired replies; four production stories
  render the same component. No deployment or installed desktop claim is made;
  new-issue/edit forms and connected/installed journeys remain consumers.
- T07-FOUNTAIN-ISSUE-ROUTES mounts shared issue list/detail/Factory facts beneath
  Fountain's native header, with the preserved existing workflow controls gated
  by the current issue read. Manifest, native templates and request/feed/work
  links use `/feed/work/issues`; old status routes have no alias. Native
  manager/SQL/callback route admission and actual header rendering pass, as do
  browser navigation, Back, scoped heading/comment jumps and access withdrawal.
  Local source qualification is not deployment or installed desktop evidence;
  shared pull/editor and authenticated media remain unfinished consumers.
- T07-FOUNTAIN-ISSUE-HOST supplies the Fountain shared-issue context adapter:
  current canonical/native/account binding, repository-relative Markdown and
  separate cancellable native/external navigation, with a scoped read transport.
  Actual Chromium/HTTP checks render shared list/detail/conversation and reject
  changed bindings, revoked metadata and retired reads/links. Metadata inputs are
  captured native replies; issue/comment bodies are declared HTTP peers. The
  normal manifest/header/URL cutover and installed journeys remain consumers.
- T07-FOUNTAIN-NAVIGATION supplies live configured default-branch metadata through
  the existing admitted SDK/canonical identity routes and browser host decoder.
  Real native/HTTP and captured-response browser checks cover changed/unknown
  configuration, permissions, sessions and retirement. The coherent Fountain
  manifest/header/URL cutover remains a later T07-READ consumer.
- T07-ISSUE-COMPOSITION mounts the shared list/detail and optional matching
  Factory journey in the production application frame. Typed issue and
  work-detail routes stay distinct; current account/resource bindings and
  coordinated read retirement pass actual browser and emitted Storybook checks.
  Fountain manifest/header/URL cutover and connected desktop qualification remain
  T07-READ; no installed or provider-connected completion is claimed.
- T07-ISSUE-DETAIL supplies the reusable read-only description/conversation
  component, sanitized Markdown and host-owned link ports. Ten production
  stories and actual Chromium journeys cover scope, paging, large/untrusted
  writing and retired reads/navigation. Factory facts and connected host
  navigation/desktop qualification remain T07-READ; mutations remain T07-EDIT.
- T07-ISSUE-LIST supplies the reusable production issue list, canonical read
  projection and shared stylesheet, exported by the standalone browser bundle
  and consumed by eight stories. Actual Chromium checks cover filters, paging,
  exact IDs, access loss and retired reads/navigation. Detail/conversation,
  Factory facts and connected Fountain/desktop navigation remain T07-READ.
- T10-WRITES completes the local Fountain human-operation backend boundary:
  journaled issue/PR creation, comments, title/body edits, lock/state changes,
  reviews, inline comments, thread resolution, reviewer requests and allowed
  merges use the shared Product/Application and admitted native adapter.
  Real native/HTTP/recovery evidence and supported bounds are recorded in the
  canonical backlog. Shared editor navigation, desktop HTTPS enrollment,
  execution/publication and installed journeys remain their later slices.
- T06-FOUNTAIN completes human-admitted collaboration reads at the local installed
  extension/separate-Factory boundary. Actual native repository, issue, PR, diff,
  review, thread, status, protection, Actions and capability journeys pass,
  including private access/revocation and retained identity. Exact evidence and
  remaining host/UI/product gates are in the canonical implementation backlog.
- Production Fountain entries now import `frontend/factory/feed/feed.ts` and its
  extracted support modules. Catalogue, links, navigation, optional application
  inventory and draft scope come from a typed host adapter. Shared production
  modules do not import Fountain/Spaces code.
- T04-UI closes the shared component boundary after its scoped draft/action and
  composition slices. `bun run build:factory-ui` now emits the production
  application module and offline styles/assets independently of Forge/Storybook.
  The browser composition journey uses those emitted files. The existing
  Storybook index contains 160 production-source examples across eighteen groups;
  installed native lifecycle, credentials and connected journeys remain later work.
- Shared feed/conversation CSS imports canonical branding fonts, palette and the
  new `assets/branding/theme/tokens.css`. Fountain retains document theme rules.
- Storybook 10.6.5/Vite 8.3.5 builds the production component with six development
  states. `tools/factory-ui` embeds those assets using Tauri 2.12.5 on Linux. This
  is a development workbench, not the desktop application or a backend bridge.
- Passed: full TypeScript/Lit/source guards; 26 existing feed browser journeys;
  added standalone-host journey covering scoped drafts, routing and dark theme;
  three existing extension asset graph/color/token checks; Storybook static build.
- The actual Linux Tauri/WebKit rendering check passed: three production feed
  cards, light background `rgb(255, 255, 255)`, dark `rgb(16, 16, 16)`. It used a
  private Weston compositor and downloaded GTK/WebKit development sysroot under
  `/home`, without installing system packages. The headless compositor has no
  keyboard seat, so this proves rendering rather than native input interaction.
- Native UI dependencies are behind an explicit `native-ui` feature. The normal
  `soda-factory-ui` Cargo check passes without GTK/WebKit development libraries.

T00-PG is complete: the Rust native database component and real three-phase store
proof qualify PostgreSQL 18.6 on Linux with a relocated payload. See the
[runtime guide](../../../lib/desktop-runtime/README.md) for source/size/timing and
platform limits. This is the database lifecycle producer for the later supervisor.

T01-REGISTRY is complete: canonical identity records, additive PostgreSQL registry
and same-host bindings are implemented and integration-checked. Opening storage
does not choose the namespace for historic numbers. T01-ID was split before
implementation. T01-LEDGER is now also complete: explicit configured-source
adoption preserves retained IDs/history, reserves new ranges atomically, refuses
wrong/ambiguous sources and survives rollback/retry with mixed-provider data.
The actual native PostgreSQL checks passed. See the
[identity guide](../../../internal/collaboration/README.md).

T01-RECORDS is complete: neutral collaboration reads/capabilities/anchors, their
Lit mirror and production diff/anchor decoders passed compiler/source checks and
a Go-to-client JSON probe. [Contract and evidence](../../../internal/collaboration/records.md).
The former T01-PORTS packet was split before implementation into T01-RECORDS,
T01-EFFECTS and T01-RUNTIME. T01-EFFECTS is also complete: neutral graph/operation
contracts and validation preserve conditional guarantees, operation namespaces and
partial/uncertain checkpoints. SDK wire and existing-journal retention checks
passed through native PostgreSQL restart/crash recovery.
[Contract and evidence](../../../internal/collaboration/effects.md).
T01-RUNTIME is complete: SDK-free admitted-actor, standing grant/policy,
native/appliance provenance and runtime placement contracts passed scope, kernel
process and native PostgreSQL retention checks.
[Contract and evidence](../../../internal/factory/access/README.md).
T00-DIFF is complete: the shared production Lit wrapper selects Pierre 1.6.2 and
passed actual Fountain HTML/policy, stronger CSP and Linux Tauri/WebKitGTK checks,
including trusted keyboard input, immutable drafts, virtualization and disposal.
[Renderer and evidence](../../../frontend/factory/review/README.md).
T02 was split before implementation into intake, graph, execution and identity
consumer cutovers followed by complete-engine/provider qualification.
T02-INTAKE is complete: opaque provider proof and neutral semantic issue-maker
records preserve retained PostgreSQL grants/retries/slots; the factory/store
production import closures are SDK-free. Subsequent T02 slices own its consumers.
[Boundary and evidence](../../../internal/factory/issue_intake.md).
T02-GRAPH is complete: graph plans, source/creation facts, operation outcomes and
receipt consumers use neutral ports. Fountain's codec preserves approved native
packet encodings/digests after JSONB round trips. The store uses decoded facts
matched to opaque provider JSON for its delivered-history guards. Private Unix
transport and native PostgreSQL retention/adoption/repair checks pass.
T02-RUN-OBSERVATIONS has since completed. T02-RUNS is active. Its producer supplies
tagged run/custody observations, typed
absence and an appliance adapter. Actual private Unix clients and the Rust broker
over native PostgreSQL passed metadata/seal/restart checks. No agent execution
or acquisition ran. The coordinator now consumes neutral run/custody observations
and candidate export; typed errors replace message matching, and its production
dependency closure excludes the SDK and concrete host. Retained run/tool/admission
variants and the remaining command boundaries are incomplete. Canonical ID cutover
and actual Fountain qualification remain their later T02 gates.

Pending: actual admission/execution/placement consumers, full desktop supervision, portable backend services/admission, real
desktop account/executor bridge, provider identities/connectors, shared issue/PR
screens, installed journeys and distribution. The Darwin dependency probe still
fails in the external SDK's Linux peer-credential code; Linux-first work proceeds
while the planned provider/composition extraction addresses that later closure.
The required `bun run dev:ui` browser update built assets, then stopped because
`soda` has no active development deployment. No browser assets were activated;
deployed Forge parity remains unverified. No full remote deployment,
provider/model execution or repository mutation was run for this implementation.

### Original planning inspection

Checkout: `/home/vince/Projects/sodaos`. Inspected HEAD:
`c5370e1bafee95adb3274042d3c8f45d774ea365`; tree:
`bea01343bb52e2848881b1ee3908849d43a5a895`; 2,697 tracked paths.
Tracked dirty diff SHA-256 before planning:
`07a92db50c3b3890851044bf910a32e05f2a06957e591d8c978b6d648822d000`.
The 22 in-scope untracked process-split paths listed by the current Git inventory
have aggregate `sha256sum`-listing identity
`ceae93aed5dc53f03c5077b7396316ec3448cd6fb5b0b42f8aa9ab72517d25ef`.
These identify inspected source; they are not a freshly qualified release baseline.
Existing working edits and unrelated native-support/release changes are preserved.

Read-only source reviews covered the seams named in the source map, manifests,
representative transactions, runtime bindings, native routes and build joins.
Existing feed/Factory behavior and tests were inspected; no implementation test,
provider/model call, native app build, database operation or deployment was run
for this planning turn. Earlier process-split test results do not qualify these
new desktop/GitHub paths.

Three independent reviewers participated: frontend primary `frontend_plan`,
runtime primary `runtime_plan`, and donor/scope challenger `donor_review`.
Frontend and runtime also challenged each other's boundaries. Their corrections
are incorporated in the source map: stable host IDs, scoped drafts, theme assets,
Fountain route limits, managed PG ownership, publication transfer, historical
authority and unknown remote outcomes. This is source review, not native proof.

| Dimension | Status for this plan |
| --- | --- |
| Responsibility coverage | Complete for the named product seams; unchanged source families are retained. No claim of line-by-line whole-repository coverage. |
| Workflow tracing | Established source paths traced for feed, intake, creation recovery, publication and process lifetime. Desktop/GitHub workflows are specified targets, not implemented paths. |
| Independent challenge | Completed for consequential UI/runtime/reuse choices; unresolved product/protocol decisions remain D1–D5. |
| Target allocation | Defining owners and implementation packets allocated. Exact SDK/auth changes for D2/D5 and platform artifact selections for D4 require their named spikes. |

Upstream metadata was checked against official repositories/docs for candidate
libraries. No dependency or lockfile was changed. Re-resolve current upstream
inputs before implementing/building, following [freshness](../freshness.md).

## Completion standard

An installed Linux desktop can connect a real repository, render its issues/PRs inside
the app, automatically admit eligible work under an explicit policy, execute on
the host, publish a PR, receive review feedback, correct it and recover after
restart. The Fountain host performs its corresponding journey using the same
components. A subsequent shared component change appears in both builds.

Read-only browsing alone, a Tauri window around a website, an executable that
still needs the appliance, or Storybook screenshots do not meet this standard.
Unsupported provider guarantees must remain explicit rather than appear ready.

Maintain this folder in place as packets land. Replace stale target allocations
with actual ownership, move implemented contracts into their existing product,
trust and reference owners, and refresh affected source identities/evidence.
The owner has authorized a goal to implement one slice at a time. Follow the
canonical dependency table and execution rule in the backlog: preserve existing
progress, implement/verify one ready slice per goal turn, then continue at the
next slice boundary. Do not dispatch parallel slices or create a competing plan.
This authorization does not turn a partial slice into completion of the product
or grant publication, remote repository mutations or destructive deployment.
