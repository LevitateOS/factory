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
attestation boundary. The socket is desktop-owned mode `0600`; the service
uses a distinct dynamic UID, joins that run's provider relay network namespace,
binds the broker-prepared checkout and root role declaration read-only, and
uses a private keyring, IPC and runtime with finite cgroup limits. It checks
the desktop peer, its own UID, the fixed socket, role declaration and exact
Git HEAD before accepting only `attest_agent`. The root-mounted role manifest
also binds a 32-hex profile; the request must present the same profile, and the
reply echoes it for the desktop client to verify. Its reply says
`execution_available:false`; neither role can launch a tool or receive an
account secret. The source compiles and a rendered template passed
`systemd-analyze verify` on 2026-11-04.

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
profile now places each agent in a private PID namespace, overlays the shared
runtime parent with a root-owned mode-`0700` tmpfs, and binds only its own
runtime at `/run/factory-agent-private`. It also overlays the host cgroup tree
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
PID namespace and delegated cgroup. The production agent remains
attestation-only; concurrent execution, cross-run denial, role-limited tools,
credential withdrawal and a finite provider run remain open.

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

The earlier root-private reviewer attester's fixed Git HEAD check now uses a
three-second `timeout` with a one-second kill grace and reads at most 42 bytes
from stdout. The exact candidate line is 41 bytes; excess output is refused
without buffering it in memory. Its systemd service also has a 15-second
lifetime and control-group kill. A disposable activation of the actual reviewer
socket and service returned the dummy candidate, root peer UID and a distinct
worker UID with read-only worktree and execution unavailable. The temporary
socket, unit, binary and checkout were removed.
