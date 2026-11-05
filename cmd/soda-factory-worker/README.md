# Isolated worker socket boundary

`soda-factory-worker` is an inert, one-connection-per-service Linux binary.
The installer must render `systemd/soda-factory-worker.socket.in` with the
selected desktop account, install both units and the root-owned executable, and
enable the socket. The template service starts each accepted connection under
a separate dynamic system UID with private home, IPC, devices, temporary files,
hidden other-user process metadata, no ambient capabilities and finite cgroup
resource ceilings. Each accepted connection has a systemd-owned private runtime
directory removed with its service instance. Its privileged system manager owns
the socket and service lifetime; the worker binary never runs as root.

The service accepts one 4-byte little-endian length followed by at most 256
bytes of JSON. Currently the only request is
`{"kind":"attest","version":1}`. It checks that standard input is a Unix
stream with kernel peer credentials, its own UID differs from the peer, no new
privileges are set, and desktop session variables were not inherited. It replies
with one length-prefixed JSON attestation and `execution_available:false`. It
also checks that its runtime directory is owned by the worker and mode `0700`.
Unknown versions, fields and requests fail closed. No executable, path, token,
worktree, role or provider selector is accepted.
The inert service denies all IP traffic. A future provider run must gain a
separately reviewed, run-bound network path instead of inheriting host egress.
The rendered units passed `systemd-analyze verify` after this setting was
added. A live socket activation still returned attestation from worker UID
61739 for desktop UID 1000 with execution unavailable; the temporary units,
binary and socket were removed.

This is the first D3 worker boundary, not an admitted native executor. The
production desktop does not connect to this socket, and the units are not
installed by the current development build. Before adding a launch request,
the trusted native owner must bind profile, run, role and exact workspace to a
current Go coordinator admission, broker only scoped credentials, and prove
retirement and negative file/keyring/role checks on the actual run path.

On 2026-11-04, the rendered units passed `systemd-analyze verify` and a live
socket activation on the Linux development host. The socket directory was
root-owned, its socket was mode `0600` and owned by the desktop account, and
`Accept=yes` started a worker with UID 61739 for peer UID 1000. Attestation
reported no-new-privileges and execution unavailable. A request with an extra
path field and a launch request each received no response. The temporary units,
binary and socket were removed after the check. These checks establish the
admission stub and unit wiring; they do not qualify worker file/keyring access,
credentials, roles, workspace mounts or an actual provider run.

The follow-up live activation with `RuntimeDirectory=soda-factory-worker/%i`
reported `private_runtime:true` for UID 61739; the per-instance directory was
gone after the service exited. The worker refuses admission if that directory
is absent, not worker-owned, or not mode `0700`.

The unit explicitly selects `KeyringMode=private`. A temporary fixed diagnostic
executable under the same service settings ran as UID 61739 and reported denied
reads of a nonempty mode-0600 desktop canary and the connecting desktop process
environment; the desktop session bus was hidden. A corrected `keyctl`
`get_persistent` check denied UID 1000's keyring while a same-UID control could
access the worker's own keyring. The diagnostic executable and units were
removed. This qualifies those host boundaries for this service profile, not
the later provider execution path.

The unit also blocks kernel logs, alternate syscall ABIs, personality changes
and realtime scheduling. The rendered unit verified and live attestation still
succeeded after these settings were applied. Delegated cgroups and host network
access remain necessary for the intended process owner and provider transport;
those permissions require role and credential mediation before execution.

Reviewer checkout permissions alone are insufficient: the worker owns its
private runtime directory and can change modes on files it owns. A transient
systemd comparison on 2026-11-04 confirmed that the same unprivileged UID could
write and `chmod` its own file normally, but could only read it when its parent
was supplied through `BindReadOnlyPaths`; write and `chmod` were denied. The
source and services were removed. A privileged broker must bind a root-selected
exact run snapshot read-only for reviewers and supply a separate writable
workspace for coders. This inert socket service currently does neither.

A follow-up probe showed that the bind source must be a readable child beneath
a root-owned mode-`0700` parent. Binding the private parent itself did not make
its contents readable. Binding a mode-`0755` child did: the worker read only the
mounted snapshot, could not traverse the original parent, and could not write
to the mount. The privileged broker must preserve that source layout per run.

The observed Muse `exec --api-key-stdin` input would place the provider key in
the worker and cannot satisfy Factory's credential custody rule for an account
secret. The headless process primitive now has bounded pipe stdin for the
owner-generated run placeholder; the actual worker has not used it. Its
`--base-url` setting offers the fixed private relay endpoint, but still needs
run-bound launch, network confinement and withdrawal proof on that path.

On this host, a bounded transient systemd comparison confirmed
`IPAddressDeny=any` blocked a reachable loopback endpoint, while adding
`IPAddressAllow=127.0.0.1/32` restored access to that address and still blocked
a known reachable listener on `127.0.0.2`. The listeners and temporary units
were stopped. An address allowlist alone cannot select a single service or
authenticate a run, so it is not yet a provider credential boundary.

An empty scratch Muse `exec` probe with `--api-key-stdin`, a dummy key and
`--base-url` sent model discovery and response requests to a local fake
provider. It confirmed only that the authorization header matched the dummy
key; the fake provider returned 401 and no real model ran. The listener and
scratch workspace were removed. A future proxy must replace that placeholder
with owner-held authority and enforce the admitted run, endpoint, budgets and
withdrawal.

A separate subordinate-UID namespace check mapped the desktop host UID 1000
and a worker host UID 524288. It exposed a Git checkout from a root-private
parent through a read-only bind, then ran the actual Muse 1.4.14 ELF with
reviewer flags, a dummy stdin key and private-loopback fake provider. The
worker could read the mounted checkout but could not traverse its original
parent, write or chmod through the mount, read a desktop canary or process
environment, reach the desktop session bus, or obtain the desktop keyring.
Muse sent `GET /muse-code/models` to the fake provider and exited after its
401; no real account, model, owner socket or systemd agent unit participated.
The temporary namespace and scratch source were removed. The current
`Accept=yes` worker socket remains attestation-only: its `%i` identifies a
connection, so it cannot select a run-specific checkout mount. The agent
needs a broker-started exact-run service with its own role and workspace.

`soda-factory-reviewer` is a separate, credential-free run attester behind the
root broker. Its fixed socket template listens only inside
`/run/soda-factory-broker/preparations/<run>`; the root-owned mode-`0700`
parent and mode-`0600` socket deny a direct desktop connection. The paired
service uses a distinct dynamic UID and binds only the prepared checkout at
`/run/factory-worktree` read-only. It requires systemd socket activation, a
root peer, its own private runtime, no new privileges and an exact 32-hex run.
The single bounded `attest_run` request names the expected 40-hex candidate.
The worker verifies the mount's read-only flag and Git HEAD, using Git
`safe.directory` for only that fixed mount because the preparer owns the
checkout under a different UID. It replies with its UID and
`execution_available:false`. It accepts no executable, role choice, host path
or credential.

On 2026-11-04, the broker staged and prepared a local candidate, then activated
the reviewer through its actual private socket. The first attempt failed Git's
cross-UID ownership check; after restricting `safe.directory` to the fixed
mount, attestation succeeded as UID 61729. Direct desktop connection to the
private socket was denied; changed-candidate attestation was refused. Repeated
attestation succeeded, and exact retirement stopped the socket and service and
removed the checkout and source. A post-retirement replay was refused. All
temporary units, binaries, socket state and scratch source were removed.
This qualifies the read-only run mount and service identity only. It does not
run an agent, proxy credentials, withdraw provider authority or prove a finite
provider task.

`soda-factory-provider-relay` is a credential-free, run-specific transport
primitive. Its template starts a different dynamic UID in a private network
namespace, binds only the desktop owner's run socket beneath a private
mode-`0700` directory at the fixed `/run/factory-provider.sock` path, and
listens on private loopback port 37377.
The relay checks its run ID, service identity, runtime directory, source socket
owner and mode, and the connected owner's kernel peer UID. It forwards bytes
with request, response, concurrency, idle and lifetime limits. It does not
read a provider token or decide whether a request is authorized. The desktop
owner must authenticate the run and enforce provider routes, model, spend,
deadline and withdrawal before any real credential use. This unit is a
template only; no installer enables it and no agent is launched through it.
The relay reports systemd readiness only after validating the socket and
binding its private listener; the root broker waits for this readiness before
reporting an attested relay UID.

On 2026-11-04, a temporary rendered system unit passed
`systemd-analyze verify` and started under a dynamic UID with a fake,
credential-free owner
socket. A direct desktop request to the private owner socket was refused by
the fake owner. Host loopback could not reach the relay. A separate dynamic
UID service joining only the relay's private network namespace reached the
relay; the owner observed peer UID 64727, distinct from desktop UID 1000.
The joined service could not see a usable Unix socket at the relay's bind
destination. Systemd created an inert regular-file placeholder at that path
in the host mount namespace; the proof removed it with the temporary unit,
binary, fake socket and source directory. This establishes the transport
namespace shape on this host, not admission, owner policy, agent role
confinement, credential withdrawal or a real provider run.

`soda-factory-agent@.socket` and `.service` add a broker-started exact-run
agent boundary. The socket is root-owned mode `0600`; the service
uses a distinct dynamic UID, joins that run's provider relay network namespace,
binds the broker-prepared checkout and root role declaration read-only, and
uses a private keyring, IPC and runtime with finite cgroup limits. It checks
the root broker peer, its own UID, the fixed socket, role declaration and exact
Git HEAD before accepting `attest_agent` or one `execute_agent`. The root-mounted role manifest
also binds a 32-hex profile and the staged 64-hex tool and prompt digests; the
broker request must present the same profile, and the reply echoes these values
for the desktop client to verify through the broker. Its reply says
`execution_available:false` because attestation itself does not launch a tool
or receive an account secret. The source compiles and a rendered template passed
`systemd-analyze verify` on 2026-11-04.

The broker now owns worker attestation. It opens the root-only agent socket,
checks the worker's exact run, profile, candidate, role, tool and prompt
digests, distinct UID, read-only source and root peer, then returns a bounded
attestation to its authenticated desktop client. A disposable activation of
the actual broker, preparer, relay and agent units accepted the exact reviewer
run while a direct desktop connection to the agent socket was denied. The
worker UID differed from the desktop UID. Exact retirement and removal of all
temporary units, binaries and runtime state followed. This was an attestation
check before the later one-shot launch implementation.

A disposable system installation activated the actual agent service with a
dummy Git checkout and a mock relay namespace. It returned worker UID 64596
for desktop UID 1000, the exact candidate and reviewer role, and a read-only
source with execution unavailable. The worker namespace matched the mock
relay and differed from the host. A diagnostic run under the same unit profile
could not read a desktop mode-`0600` canary or process environment, connect to
the desktop session bus, obtain the desktop persistent keyring, traverse the
original root-private checkout, or write, create or chmod on the mounted
checkout. A request with an extra path field returned no reply and failed the
service. The desktop client accepted an exact attestation when run as the
normal host user. The temporary service, mock relay, binaries, socket and
checkout were removed. The mock relay did not prove an owner-backed provider
request or credential withdrawal; the agent remains attestation-only and D3
remains open.

A separate exact-run proof then used the real broker, preparer, provider relay
and agent units. The broker armed only the staged and prepared reviewer run;
the agent attested UID 64596 and the exact candidate. A changed role was
refused without stopping the valid socket after the broker cleanup fix. Exact
retirement stopped the agent and relay and removed the mounted source; a later
agent attestation failed. The owner socket supplied no account credential, and
no agent execution or provider request was attempted. Temporary system state
was removed.

The one-shot `execute_agent` path now accepts only the broker's exact root-peer
request. The worker checks the root role declaration, admitted prompt SHA-256,
captured ELF SHA-256 and byte bounds, then launches fixed Muse CLI arguments in
its private cgroup. Reviewer disables shell and write; coder uses the private
cloned workspace. The child receives only a placeholder on stdin and reaches
the run-specific loopback relay. The worker keeps its listener and accepted
broker stream close-on-exec, polls for broker loss, retires the process tree and
returns bounded output with a digest and retirement receipt. A disposable live
check denied desktop canary access and source writes, refused altered prompt
and ELF bytes before launch, and retired a sleeping child when the caller left.
The captured Muse ELF reached a credential-free private relay, which returned
401; this did not qualify account credential custody or a real provider run.
The version-2 execution request also emits a bounded `agent_started` frame
immediately after the captured process starts. The broker and native owner
validate its exact run and process identity before exposing it to a trusted
observer, then require the terminal receipt to match. A disposable service
check received it before a three-second local child exited and stopped a
second run when the observer refused. This supplies start evidence for the
execution consumer; current liveness, Go admission and MCP activation remain
separate requirements.
For the next MCP transport consumer, the official stable channel was checked
on 2026-11-05 and selected Muse `1.4.14-R7309.1`, matching the installed ELF.
Its per-run config accepts `mcpServers.factory` with
`transport:"streamable_http"`, an HTTP URL and headers. CLI header values must
be whole environment references such as `${SODA_FACTORY_MCP_AUTH}`; literal
values refuse startup. A local echo-only check reached a synthetic MCP
server with the exact fixture headers, initialized it and listed its tools;
an absent endpoint refused required-server startup. This is a local CLI
contract check. The worker now always configures required MCP at the fixed
private `http://127.0.0.1:37377/mcp` route. Its header uses only the same
owner-generated run placeholder as its provider stdin. The owner accepts that
route only from its attested relay and forwards bytes through an exact-run
`WorkerMcp` port supplied before launch by `RunOwner::start_with_mcp`.
The port captures its destination/capability; worker headers cannot select
either. No port returns 503 without forwarding. The production Go native
listener and admitted consumer remain unimplemented; its MCP secret, IPC/root
key and provider account credential must stay outside the worker.
The native owner bounds MCP bodies at 72 KiB, successful JSON replies at 1 MiB,
calls at 256 per attempt and operation time at the earlier of 30 seconds or the
run deadline. Both relay and owner allow at most 512 connections over the run;
their existing concurrency and byte bounds remain. Credential withdrawal or
owner retirement drops and joins in-flight forwarding. Go still owns MCP
decoding, current capability/work/tool admission and transactions.
A disposable actual-service check used a tiny local ELF to verify the fixed
arguments and placeholder environment, then initialized and discovered tools
through the native owner and the real Go bound MCP handler with a synthetic
capability. A missing-port run returned 503 with no callback. Separate pending
operations were dropped on credential withdrawal and owner cancellation;
uncertain cancellation retained its root launch evidence. All exact services
retired. These checks used a private dummy keyring and no model request. The
actual Muse model request/response policy still admits only its builtin
`muse` namespace; Factory tool negotiation and execution need the next cutover
before a real finite provider task can qualify D3.
The execution receipt now carries the process owner's invocation ID, boot ID,
start tick, start time, namespace PID and exit code/signal for later native
provenance binding. These added fields have source/build validation; the prior
live launch did not exercise their new wire encoding. A subsequent disposable
activation of the actual broker, preparer, reviewer, relay and agent units ran
a tiny credential-free ELF as reviewer. The broker returned a nonzero isolated
UID, 32-hex invocation, boot ID, positive start tick, namespace PID 5, start
time, exit code 0 and complete bounded output with a retired tree. Exact
retirement removed the staged source and preparation. The first cleanup attempt
exposed a missing reviewer unit in the temporary probe installation; adding
that unit made the exact retry and a second complete launch/retirement pass.
The temporary installation and scratch artifacts were removed.

A later disposable check exercised cancellation with the actual root broker,
preparer, relay and agent units. A tiny credential-free reviewer ELF reached a
fake desktop owner through the private relay and waited. The desktop client
cancelled the broker call; the agent unit had `MainPID=0` and no control group
before exact bundle retirement, and the system journal recorded SIGKILL of the
agent main process and child. Retirement and removal of all temporary units,
binaries and run state followed. This does not establish owner-held provider
credential withdrawal or a production Go-admitted run.

A later disposable run composed the real broker, preparer, reviewer, relay and
agent units with `factory_provider::RunOwner` and a separate private GNOME
Keyring fixture. A tiny reviewer ELF used only its placeholder through the
run-specific relay. The fixed Muse catalog route returned HTTP 401 for the
dummy credential; after fixture withdrawal, the next run returned local 503.
Both exact worker units had `MainPID=0` and empty control groups after the
owner retired them. The temporary installation and fixture were removed. This
proves native custody and withdrawal at this path, not Go product admission,
real model work or production Quit.

The relay now pins the desktop socket owner's exact process with a Linux pidfd
before reporting readiness. It checks that handle every second and stops when
the owner process exits; missing pidfd support fails relay startup closed. Both
the exact agent socket and service are bound to the relay unit with systemd
`BindsTo`, so relay loss stops their admission and process service. A disposable
actual-unit check killed only a fake desktop socket owner: the relay and an
armed root-only agent socket both became inactive, and the temporary units and
socket were removed. A separate cross-UID kernel probe observed a pidfd become
readable after owner exit. A second disposable run staged a real bundle and
started a sleeping ELF in the isolated reviewer service. Killing its fake
desktop owner stopped the relay and active agent service; systemd then showed
`MainPID=0` and no control group. The broker returned a retired-process receipt
with exit signal 15, not a clean exit code, and exact bundle retirement
succeeded. Temporary units, binaries, source and child were removed. This
qualifies owner-death process teardown on this host; the fake owner was separate
from the surviving broker client, and production desktop crash/restart recovery
of retained root staging remains open.

The agent service now opens `soda-native-process::Processes` under its own
run-specific runtime and delegated cgroup before accepting an attestation.
Its unit places the main process in `init`, leaving the parent cgroup empty so
the worker can enable only CPU, memory and process controllers. The agent
validates the exact systemd unit path and dynamic UID before enabling them;
missing delegation refuses startup. A disposable system service without
`DelegateSubgroup=init` had an empty controller set. Adding the subgroup let a
distinct UID enable `cpu memory pids` beneath its own service while the
desktop user's cgroup was unwritable. The actual agent unit then returned an
exact reviewer attestation after opening the native process owner; removing
the subgroup from that disposable unit produced no reply and logged a
delegation refusal. Restoring the source unit restored attestation. Temporary
units, binary and dummy checkout were removed. No agent child or model ran in
this check.

A concurrent probe found that two instances of one `DynamicUser=yes` template
can share the same host UID (62475 here), so UID and mode `0700` alone did not
isolate their run directories or cgroups from each other. A per-run systemd
profile placed each agent in a private PID namespace, overlaid the shared
runtime parent with a root-owned mode-`0700` tmpfs, and bound only its own
runtime at `/run/factory-agent-private` during that check. It also overlaid the
host cgroup tree
and binds only its exact system unit cgroup at `/sys/fs/cgroup/factory`.
Two concurrent dummy instances with the same UID had distinct PID namespaces,
could read only their own runtime and cgroup mount, could not see the host
user cgroup tree, and enabled `cpu memory pids` only in their own bound cgroup.
The actual agent template uses the same fixed mounts; its updated binary
attested the exact dummy reviewer run after validating them and opening its
native process owner. With a private PID namespace, systemd's `LISTEN_PID`
names the host PID; the worker now validates its numeric presence and the
inherited listening FD's type, state and exact fixed socket path. The worker
also accepts a desktop peer whose PID is hidden by that namespace while still
requiring the kernel peer UID. Temporary units and dummy state were removed.
A disposable activation using the actual agent unit profile, with only
`ExecStart` replaced by a diagnostic binary, launched a captured finite child
through `soda-native-process::Processes`. It exited zero with complete output
and a retired tree. A second invocation started a background `sleep` child,
emitted a marker, and was cancelled; the monitor reported `Cancelled` and a
retired tree. The process owner retired, and the service exited successfully.
The diagnostic unit, binary and dummy preparation were removed. This verifies
child launch and descendant cancellation under the unit's private runtime,
PID namespace and delegated cgroup. At that checkpoint the production agent
remained attestation-only; concurrent execution, cross-run denial, role-limited tools,
credential withdrawal and a finite provider run remain open.

The current agent unit mounts a separate 768 MiB tmpfs for each service
instance at `/run/factory-agent-private`. The worker checks the mount type,
device, mode and capacity, then creates its own mode-`0700` `run` and `tmp`
directories as the dynamic UID. Git and the agent CLI use this bounded `tmp`
directory. The unit makes `/tmp`, `/var/tmp`, `/dev/shm` and `/run/lock`
inaccessible; the worker checks their inaccessible mounts before handling a
request. A disposable systemd service with `DynamicUser`, `PrivateTmp`,
`PrivateDevices` and `ProtectSystem=strict` confirmed the 768 MiB mount, denied
writes to those four scratch paths, and created the private directories under
the dynamic UID. A first disposable probe showed that `PrivateTmp` overrode
separate tmpfs size options on `/tmp` and `/var/tmp`, so those options were
removed. The actual worker binary passed a locked offline build and a rendered
unit passed syntax verification. A disposable unit initially failed before
worker startup because it omitted the source unit's `Slice=system.slice`, so
the requested cgroup bind source was absent. With the exact slice and cgroup
bind restored, the built worker passed service identity, storage and process
delegation and refused at the expected missing role manifest. The temporary
binary was removed. This did not activate the installed agent/broker path, a
coder, or a provider. Coder
growth/refusal, aggregate writable surfaces and multi-run isolation have not
been requalified under this profile. D3 remains open.

An attempt to extend this into reviewer attestation with a temporary socket,
service, dummy Git checkout and role manifest failed before the readiness byte:
the worker's captured Git command could not read the dummy repository. A
disposable native-process diagnostic could stat its `.git/HEAD` but got
`EACCES` opening it, both from its parent and from a captured child; a direct
command in the matching disposable service profile read that same file. Moving
the dummy checkout from the worktree to `/run` did not change the denial. The
cause is unresolved. The temporary units, binaries and `/run` fixture were
removed. This failed attempt does not qualify worker source admission, native
process reads, or the D3 gate.

A follow-up disposable check used the actual `soda-factory-preparer` binary on
a 373-byte dummy Git bundle, with the broker's root-owned source and output
mount modes. The preparer exited zero and created a dynamic-UID checkout. The
current agent service profile then returned exact reviewer and coder
attestations from that checkout: worker UID 64596, root peer, read-only source,
and a private coder workspace only for the coder role. This narrows the earlier
`EACCES` to the hand-built fixture path without establishing its cause. The
broker did not orchestrate this preparation; neither role executed a tool or
provider request. The temporary services, binaries, socket and staged run were
removed. Full service admission, storage exhaustion and D3 remain open.

A second disposable coder check used that preparer path and the current agent
service profile to execute a 16,848-byte credential-free ELF. It appended one line
to the private workspace, then exited zero. The isolated worker reported UID
64596, root peer, retired process tree, complete 24-byte output and a captured
437-byte candidate bundle. Git verified the bundle's new commit descends from
the prepared source and contains the expected edit. The check did not use the
broker's `execute_agent` orchestration, a provider credential or a real model;
it did not exercise workspace exhaustion, cross-run denial, receipt persistence
or Go admission. Exact temporary units and `/run` paths were removed. N3/D3
remain open.

A follow-up temporary installation exercised the broker's full coder route
with a separate 362-byte dummy source and 16,840-byte credential-free ELF.
The broker attested distinct relay and worker UIDs, forwarded a zero-exit
retired-tree result with complete output and a 419-byte candidate bundle, and
retired the exact run. Git verified the returned commit and one-line edit. The
temporary owner socket and all run-specific services and state were removed.
The native desktop receipt, Go coordinator, real provider and storage/cross-run
qualification remain open; D3 is not complete.

A disposable current broker/preparer/relay/agent run also exercised private
scratch growth. A 17,256-byte credential-free coder ELF attempted up to 820 MiB
of writes under the worker's `TMPDIR`, reporting progress every 64 MiB. Its
last reported progress was 704 MiB; the child then received signal 9. The
typed broker client accepted a complete worker reply with the retired tree,
`candidate_state=unchanged` and that signal. The systemd journal recorded an
OOM kill in the agent unit and a 773.9 MiB peak. The unit ended with
`MainPID=0`; exact broker retirement removed the run source, preparation,
socket and relay runtime. The temporary units and owner socket were removed.
This demonstrates bounded growth ending in a child kill and cleanup for one
dummy run. It does not prove an `ENOSPC` path, all writable surfaces, multi-run
capacity or a real provider task. A future Go consumer must retain the signal
as a failed execution observation; systemd's service `Result=success` is not
the child outcome.

The production agent's Git HEAD admission check now runs the fixed captured
`/usr/bin/git` executable inside its native process owner, with a three-second
deadline, 4 KiB output ceiling, CPU/memory/process limits, and required tree
retirement. It accepts only the exact candidate line after complete output and
zero exit. This replaces the ordinary `timeout` subprocess, whose descendants
and output were outside that owner. A disposable activation of the actual agent
socket and service attested the dummy reviewer candidate under the new check.
Changing the root role declaration to a different well-formed candidate caused
`agent source candidate invalid` and no attestation; repeated activation then
hit systemd's start limit. The socket, units, binary and dummy preparation were
removed. The service still accepts only `attest_agent` and reports execution
unavailable.

After profile binding was added to the root role manifest and direct agent
attestation, a disposable activation of the actual isolated agent socket and
service used a dummy Git checkout and mock network namespace. The matching
profile returned the exact candidate and reviewer role from dynamic UID 64596
for desktop UID 1000, with read-only source and execution unavailable. The
same socket refused a different profile without an attestation. The service
still ran its bounded Git HEAD check through the delegated process owner.
All temporary units, binary, checkout, socket and runtime state were removed.
This qualifies the revised profile wire contract on the real service profile;
it does not prove Go run admission, agent execution or provider custody.

The agent service now prepares a coder checkout beneath its worker-owned
private runtime directory from the exact read-only source. Captured Git
commands run under the delegated process owner with finite CPU, memory,
process, output and time limits. Before attestation, the service verifies the
new directory is owned by its distinct UID, mode `0700`, writable, at the
candidate HEAD and clean. A reviewer run creates no coder checkout. A
disposable activation of the actual agent unit and mock network namespace
returned `coder_workspace:false` for reviewer and `true` for coder, each from a
distinct desktop/worker UID and with `execution_available:false`. No provider
credential was supplied; temporary units, binary, source, socket and runtime
state were removed. This qualifies the workspace preparation boundary, not an
admitted agent launch, role tool execution or credential withdrawal.

The earlier root-private reviewer attester's fixed Git HEAD check now uses a
three-second `timeout` with a one-second kill grace and reads at most 42 bytes
from stdout. The exact candidate line is 41 bytes; excess output is refused
without buffering it in memory. Its systemd service also has a 15-second
lifetime and control-group kill. A disposable activation of the actual reviewer
socket and service returned the dummy candidate, root peer UID and a distinct
worker UID with read-only worktree and execution unavailable. The temporary
socket, unit, binary and checkout were removed.

The agent unit now sets `MemorySwapMax=0` beside its existing `MemoryMax=1G`.
Its private writable workspace and output live under that service lifetime;
the setting prevents swap from extending the service's memory charge, as
already done for the preparer. The agent binary built from locked offline
inputs, and a rendered scratch unit pointing at that binary passed
`systemd-analyze verify --recursive-errors=no`. The
actual unit is not installed in this workspace and was not started for this
change. The effect on a real coder/provider run, workspace growth and cleanup
still needs live qualification; this setting alone does not close D3.

The agent now observes a coder checkout after its tool tree has retired and
before the Git process owner stops. It refuses changed workspace/.git identity
or local Git configuration. An unchanged checkout reports no candidate; a clean
descendant HEAD is exported as a Git bundle of at most 4 MiB. Bounded dirty
edits are staged, written as a tree, committed with a fixed internal checkpoint
identity and moved into HEAD with an old-HEAD compare-and-swap before bundling.
The preflight allows at most 512 dirty paths, 4 KiB of status data and 32 MiB
of changed-file logical or allocated bytes. Git hooks are disabled for the
checkpoint. A failure to capture or a remaining dirty tree does not yield a
completed agent reply.
The bundle is verified against its base in the worker, hashed, and streamed with
the bounded output and exact run receipt. Reviewer runs report no candidate.
The broker and desktop client independently check the declared state, commit,
size and digest; the desktop owner fsyncs the bundle inside its private run
receipt before retiring root staging. The changed reply and durable receipt use
version 2, so retained version-1 observations cannot be read as bundles. A
direct temporary Git-repository check passed unchanged, dirty checkpoint,
detached HEAD, deletion/replacement, rename, changed-configuration refusal,
33 MiB dirty refusal and a 5 MiB incompressible bundle refusal. Locked offline
worker source checking passes. This has not run through an installed isolated
agent service or the Go coordinator. Qualification of the aggregate workspace
storage bound, artifact custody, failed-checkpoint recovery and canonical result
transaction remain N3/N4 work.
