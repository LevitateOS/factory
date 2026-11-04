# Privileged run bundle staging and reviewer preparation

`soda-factory-broker` is a narrow, socket-activated system service for the
first N3 workspace-transfer boundary. The installer must render the socket
template for the selected desktop account and install the root-owned binary
and units. The native desktop library has a bounded, peer-checked client, but
the production runtime does not call it and no installer enables the service.
The broker stages bytes, prepares a fixed checkout and activates
credential-free reviewer, relay and agent-boundary attestations; it cannot
launch an agent.

One connection carries a 4-byte little-endian JSON length (at most 512), then
`{"kind":"stage_bundle","version":1,"run":"<32 lowercase hex>","profile":"<32 lowercase hex>","length":N,"sha256":"<64 lowercase hex>"}`,
then exactly `N` raw bytes. `N` must be from 1 through 512 KiB, matching the
current Go reviewer source-bundle bound. The service checks the Unix stream's
kernel peer UID against the desktop-owned mode-`0600` socket, requires a
root-owned mode-`0755` socket parent, no-new-privileges and a root-owned
mode-`0700` systemd runtime directory. It accepts no host path, role, credential
or unit property. The systemd profile has no capabilities, no IP access and a
90-second lifetime to cover bounded preparation and cleanup. A JSON response
names the run, profile and digest.

An exact reviewer preparation uses
`{"kind":"prepare_reviewer","version":1,"run":"<32 lowercase hex>","profile":"<32 lowercase hex>","sha256":"<64 lowercase hex>","candidate":"<40 lowercase hex>"}`
with no payload. Under the same state lock, the broker rechecks the staged
bundle's digest, writes a root-owned mode-`0400` manifest beneath a private
per-run preparation directory, and asks systemd to start only
`soda-factory-preparer@<run>.service`. The unit binds only that run's staged
bundle read-only and its empty output writable. The unprivileged preparer
checks the exact candidate; on success the broker freezes the output directory
to mode `0555`. An identical replay returns the prepared identity. A different
candidate for the same run is refused. An interrupted or failed preparation is
stopped and cleaned with the fixed cleaner before retry. The request has no
mount path, command, unit property, role selector, or credential field. This
is checkout preparation, not Go run admission or reviewer execution.

An exact reviewer mount attestation uses
`{"kind":"attest_reviewer","version":1,"run":"<32 lowercase hex>","profile":"<32 lowercase hex>","sha256":"<64 lowercase hex>","candidate":"<40 lowercase hex>"}`
with no payload. The broker rechecks the staged source, exact manifest and
frozen output before starting only `soda-factory-reviewer@<run>.socket`.
That socket is root-owned mode `0600` inside the root-private preparation
parent; the desktop peer cannot connect directly. A distinct dynamic UID gets
the checkout at one fixed read-only bind. The reviewer attests its service
identity, root broker peer, read-only mount and exact Git HEAD; the broker
validates the bounded reply and returns `execution_available:false`. This
does not authorize a reviewer agent or deliver a provider credential.

An exact retirement uses
`{"kind":"retire_bundle","version":1,"run":"<32 lowercase hex>","profile":"<32 lowercase hex>","sha256":"<64 lowercase hex>"}`
with no payload. The broker verifies the stored bundle's owner, mode, bounded
length and digest, then renames the run to a profile- and digest-bound retiring directory
before unlinking. An exact retry can finish deletion after either the rename
or bundle unlink. An exact retry can also remove an empty tombstone after the
profile declaration was unlinked. Staging that run is refused while retirement
is pending.
Before the source rename, the broker stops the reviewer service/socket and
fixed preparer instance, then uses the exact-run cleaner to remove its checkout
and manifest. It verifies
the private preparation child is empty and removes it. A failed cleanup leaves
the staged source in place. The response reports `removed:true`, or
`removed:false` when that run is already absent. A mismatched digest is refused.
The broker cannot launch a reviewer agent; future execution code must also
stop and reconcile that agent before retiring its workspace.

Under a serialized lock, the broker checks SHA-256 and stages the bytes under
`/run/soda-factory-broker/runs/<run>/bundle`. The same child contains a
root-owned mode-`0444` `owner.profile` declaration. Every existing-run request
checks that declaration before acting; an identical run and digest under a
different profile is refused. The runtime and `runs` parents
are root-owned mode `0700`; the finished run child is mode `0555` and the bundle
mode `0444`. A worker cannot traverse the original source but a privileged
manager can bind the selected child read-only into one worker instance. Up to
64 finished run IDs are retained. An identical request is idempotent; a changed
bundle for the same run is refused. Exact retirement frees its slot. The unit's
`RuntimeDirectoryPreserve=yes` retains bytes across socket service instances;
the data is transient across host reboot. Interrupted staging directories are removed
under the lock before another run is staged. No Git command runs as root, and
the raw bundle is not treated as a verified candidate until the unprivileged
preparer checks its Git contents. A current coordinator admission is still
required before any agent run.

On 2026-11-04, the rendered units passed `systemd-analyze verify`. A temporary
root-owned installation staged a 359-byte local Git bundle; an identical replay
succeeded. Changed bytes, a wrong digest, invalid run, extra path field,
oversized declaration and truncated payload were refused. The resulting state
had the intended root ownership and modes. An unprivileged transient service
could read the bundle through a read-only bind while the original source path
was inaccessible, and it cloned that bundle into a disposable private runtime.
The temporary units, binary, state and scratch repository were removed.

The staging directory moved from `/var/lib` to `/run` after a live cleanup
probe found systemd's `sd-rmrf` was denied by this host's SELinux policy for
`var_lib_t` directories, even when `systemctl clean --what=state` returned
success. A bounded `/run` manager cleanup probe removed a separate-UID child
with only `CAP_DAC_OVERRIDE` and `CAP_FOWNER`. With the revised broker units,
live staging and replay survived separate service activations; wrong-digest and
extra-path retire requests were refused, exact retirement removed the run,
repeat retirement reported it absent, and restaging after retirement succeeded.
The temporary installation and its runtime were removed.

A second live activation simulated interruption immediately after the atomic
retirement rename. Staging the same run and retiring with a wrong digest were
refused; the exact retry removed the tombstone. Repeating with the bundle
already unlinked also completed on the exact retry. Root inspection found no
staged or retiring run afterward. The temporary units and runtime were removed.

A follow-up transient proof used a separate unprivileged Git preparation
service to clone a mounted bundle into a writable child under a root-private
parent. After freezing that child, a dynamic reviewer UID could read only the
finished checkout through a read-only bind and could not change or add files.
The proof used `nobody` for preparation; an installed preparer needs an identity
isolated from unrelated processes and other concurrent preparations. The temporary
checkout and source were removed. At that checkpoint neither preparation nor
reviewer launch was wired to this broker.

`soda-factory-preparer` is an unprivileged executable for that fixed preparation
step. It accepts only a run ID, candidate commit and bundle digest; the manager
must choose the two fixed mounts and those exact arguments from admitted state.
It requires a non-root UID, no new privileges, a root-owned read-only source
directory and bundle, and an empty root-owned output directory. It hashes the
bounded bundle before invoking Git with a cleared environment, disabled system
and global configuration, and no network in the service profile. It checks the
cloned HEAD against the candidate, rejects a dirty tracked tree and bounds the
checkout's entries, depth and bytes. It rejects absolute and relative symlink
targets that lexically escape the checkout, then resolves each complete chain
and rejects targets outside the checkout or dangling links. Directory symlinks
are refused because later `..` components can traverse outside a lexical
workspace boundary. A failed preparation can leave a partial
checkout, which must be removed by the manager before that run ID is reused.
The checkout size check runs after clone, so the installed service also needs
CPU, memory, runtime and writable-space limits during extraction.

On 2026-11-04, a `DynamicUser=yes` transient preparer with private IPC and
keyring, no network or capabilities, and a read-only bundle bind prepared a
local candidate. A second dynamic UID read the frozen checkout through a
read-only bind, while its original root-private path, file writes, new files
and chmod were denied. Wrong-digest preparation failed before clone; a wrong
candidate failed after clone. A separate restricted root manager probe launched
a fixed cleanup service with only `CAP_DAC_OVERRIDE` and `CAP_FOWNER` and
removed that partial checkout; inspection confirmed its absence. At that
checkpoint these services were not yet wired to the broker. The
temporary binaries, mounts, service instances and scratch repository were removed.
An additional isolated bundle probe rejected an escaping symlink after clone
and accepted a contained relative symlink. Both temporary checkouts and their
source bundles were removed.

The dormant `soda-factory-preparer@.service` template fixes the unprivileged
identity, source and output bind destinations, private network/keyring/IPC and
CPU, memory, task and runtime limits. For instance `<run>`, the broker creates
`/run/soda-factory-broker/preparations/<run>` mode `0700` and
root-owned, with `manifest.env` mode `0400` containing only the validated
`FACTORY_CANDIDATE` and `FACTORY_BUNDLE_SHA256` hex values, and an empty
root-owned mode-`0777` `output` child. The manager reads the manifest before
starting the dynamic UID; the worker sees only the fixed source and output
binds. The broker stops or reconciles that unit, freezes a successful output,
and cleans partial output before retiring or reusing the run.

`systemd-analyze verify` accepted a temporary rendering of the template. A
live instance read a local root-owned manifest and staged bundle, prepared the
exact candidate as a dynamic UID, then exited successfully with the checkout
under the expected private run path. The temporary unit, binary, source,
checkout and scratch repository were removed.

The dormant `soda-factory-preparation-cleaner@.service` runs a fixed Rust
cleaner as root with only `CAP_DAC_OVERRIDE` and `CAP_FOWNER`, no network, and
write access limited to the selected root-private preparation child. It accepts
only a 32-character lowercase hex run ID, validates the root-owned path and
manifest, and removes the stopped preparer's output and manifest. It leaves
the empty root-owned run directory for the broker to remove under its state
lock. Missing contents are safe to retry; unexpected entries cause refusal.
The broker stops and reconciles the preparer, then invokes the cleaner and
verifies its final result before retiring the run. A future reviewer service
needs the same stop and reconciliation gate.

A live fixed-unit proof prepared a checkout owned by a dynamic UID, froze its
output, then removed it and the manifest with the limited-capability cleaner.
The staged source and empty per-run directory remained. Repeating cleanup
succeeded. A malformed instance failed. Both units use `Type=oneshot` with
`TimeoutStartSec`, so `systemctl start` reports the process outcome rather
than only successful exec. The temporary units, binaries, state and scratch
repository were removed.
A separate exact-run probe inserted an unexpected root-owned entry. The
cleaner refused before changing the output or manifest; after that entry was
removed, it cleaned the expected contents. The proof state was removed.

A transient check also confirmed that a root service with no capabilities, no
new privileges and Unix-only IPC can ask systemd to start a fixed unprivileged
service. The broker now uses a fixed manager request for preparation; future
agent launch code must derive every manager property from validated run state,
not desktop input.

A temporary installation of the actual socket, broker, preparer and cleaner
passed a stage → prepare → replay → retire journey on 2026-11-04. Wrong source
digest made no preparation state. A wrong candidate failed after clone and the
broker removed the partial checkout. The exact candidate prepared as a separate
dynamic UID, froze its output, and replayed across broker activations; changing
the candidate was refused. Wrong-digest retirement left source and checkout;
exact retirement removed both, and its retry reported the run absent. A planted
incomplete manifest was reconciled on retry. An unexpected preparation entry
made retirement refuse while preserving source and checkout; after removing
that entry, exact retirement completed. Root inspection confirmed no run or
tombstone remained. The temporary socket, units, binaries, state and scratch
repository were removed.

A further temporary installation verified the run-specific reviewer path.
The first activation exposed Git's cross-UID `safe.directory` check; the worker
now trusts only its fixed read-only mount for Git's HEAD inspection. A repeat
activation returned worker UID 61729, the exact candidate and a read-only
worktree with execution unavailable. The desktop UID could not connect to the
root-private reviewer socket, and a changed candidate was refused. Exact
retirement stopped the reviewer socket and service and removed source and
checkout; a later attestation could not revive the run. Root inspection found
no run child afterward. The temporary socket, units, binaries, state and
scratch repository were removed.

These are staging, preparation and read-only reviewer attestation primitives,
not the D3 broker closure. The broker does not check a Go-admitted
profile/role/preparation, launch an agent, mediate provider credentials, retire
active agent runs or perform a real provider task. It must remain uninstalled
until those launch and retirement controls are wired and qualified.

The broker now also accepts `attest_provider_relay` for an exact staged digest
and frozen reviewer candidate. It starts only the fixed run-specific provider
relay unit, then reports the relay runtime directory's distinct dynamic UID
with `execution_available:false`. The capless broker cannot traverse the
desktop user's mode-`0700` socket directory; the relay verifies its fixed
bind-mounted socket and desktop owner peer itself. The unit uses
`Type=notify`, so a successful manager start means those checks and the
private loopback listener completed. Exact bundle retirement stops the relay
before removing the prepared checkout and source. No request can select a
provider URL, key, executable, unit property, role or host path.

A temporary installation of the actual broker, preparer and relay units used
a scratch Git bundle and credential-free fake owner socket. Wrong digest and
candidate attestation were refused before relay start. A mode-`0600` owner
socket made the relay refuse readiness; restoring mode `0666` allowed an exact
attestation of dynamic UID 64727, distinct from desktop UID 1000. A wrong
retirement digest preserved the active relay. Exact retirement stopped it,
removed the run source and checkout, and later attestation replay failed.
Systemd unit verification passed; temporary units, binaries, source socket,
scratch runtime state and bind placeholder were removed. This does not admit
an agent or provider credential and does not satisfy D3.

An actual preparer-unit check exercised chained symlinks. A fixture with
`d/alias -> ..` and `d/escape -> alias/../..` passes the older lexical test but
resolves outside the checkout; the unit refused it with `reviewer symlink
resolves outside checkout`. At that checkpoint, a chain resolving inside the
checkout prepared successfully. The temporary unit, checkout and source were
removed.

A contained directory link is also refused: following that link and then `..`
can leave the checkout without an escaping link target. A link to a regular
file inside the checkout remains allowed. This conservative rule stays in
place until every agent file operation is constrained beneath the workspace
at the kernel path-resolution boundary. The actual preparer unit refused a
directory link with `reviewer directory symlink is not admitted` and prepared
a contained regular-file link. Temporary units and sources were removed.

The broker also accepts `arm_agent` with only the exact run, staged bundle
digest, prepared candidate and fixed `reviewer` or `coder` role. It requires an
active run-specific provider relay, writes a root-owned role declaration and
starts only `soda-factory-agent@<run>.socket`. A different role on replay is
refused. The declaration stays discoverable for retirement even if unit stop
fails; a mode-`0400` interrupted write is stopped, removed and recreated on
exact retry. Retirement stops the agent service/socket before the relay and
checkout. The paired worker currently attests only its identity, role and
read-only candidate mount, then reports `execution_available:false`.
`Broker::arm_agent` and `Broker::attest_agent` use the existing public provider
`Role` type, but no production admission calls them. On 2026-11-04, a
disposable system installation of the agent service and a mock private relay
activated the exact-run socket. The desktop client accepted its exact worker
attestation, and an unknown path field produced no reply. The mock relay did
not exercise the broker's `arm_agent` request or full retirement chain.

A subsequent disposable installation used the real broker, preparer, provider
relay and agent units with a 365-byte dummy Git bundle and credential-free
owner socket. The typed desktop client staged the bundle, prepared its exact
candidate, attested relay UID 64727, armed the reviewer role and attested agent
UID 64596. A role-change replay was refused. The live check found that the
refusal also stopped the existing socket; the broker now stops a socket after
an arm failure only if it attempted to start that socket. Repeating the role
replay preserved the active socket and an exact attestation still succeeded.
A wrong retirement digest left the run active. Exact retirement stopped the
agent socket and relay, removed its checkout and staged bundle, and made later
agent attestation fail; repeating retirement reported the run absent. Root
inspection found no source, preparation, socket or run runtime afterward. All
temporary units, binaries, dummy sockets and state were removed. Go admission,
coder workspace, role tool policy, credential withdrawal and a real finite
provider task are still required before D3 closes.

The fixed preparer Git calls now run through a 20-second `timeout` with a
one-second kill grace, inside the unit's 30-second start deadline and
control-group kill. Its HEAD check reads at most 42 stdout bytes and refuses
anything beyond the exact 41-byte candidate line; clone and cleanliness
checks still discard stdout. A disposable activation of the actual preparer
unit cloned a 289-byte local bundle, produced the expected candidate as a
distinct worker UID and exited successfully. The unit, binary, bundle and
checkout were removed. This bounds subprocess output during preparation; it
does not authorize agent execution.

The preparer now clones without checkout, verifies the exact HEAD, and reads a
bounded NUL-delimited Git tree listing before `reset --hard` materializes files.
It refuses unsupported entries, malformed paths, more than 20,000 entries,
more than 8 MiB of listing output and more than 64 MiB of declared blob bytes.
Under the actual unit, a normal local bundle prepared successfully. An
81,810-byte bundle containing an 80 MiB zero-filled file was refused with
`reviewer tree byte bound exceeded` before that file appeared; the partial
no-checkout repository used 176 KiB. Both disposable runs were removed.
The preflight now asks Git for the effective `ident`, `eol`, legacy `crlf` and
`working-tree-encoding` attributes of every path in `HEAD`, with bounded
input and output. It budgets conservative expansion factors before checkout:
16 for `ident`, 2 for CRLF and 4 plus a BOM for admitted UTF-16/UTF-32
encodings; other declared worktree encodings are refused. The service also
fixes Git's default line ending and global/system attribute sources. Under
the actual unit, a 1 MiB `ident` blob checked out to 10,066,321 bytes and
remained clean. An 8 MiB `ident` blob was refused with `reviewer expanded tree
byte bound exceeded` before its worktree file appeared; its partial repository
used 112 KiB. The temporary units, bundles and checkouts were removed.
A UTF-16LE-BOM plus CRLF fixture also prepared cleanly under the actual unit,
producing its expected 16-byte worktree file. Its disposable state was removed.
This is a bound on admitted logical file content, not a strict filesystem
quota: block allocation and repository metadata still consume storage.

The tree preflight also counts distinct parent directories from every admitted
path and refuses when files plus directories exceed the existing 20,000-entry
checkout ceiling. A 3,318-byte local bundle described 600 files beneath about
24,000 parent directories; the actual preparer unit refused it with `reviewer
tree namespace bound exceeded` before any branch directory appeared. Its
partial no-checkout repository used 100 KiB. The disposable unit, binary,
bundle and checkout were removed. The strict total-storage quota remains open.

`/run` is tmpfs on this host, and the host has swap enabled. A disposable
systemd service with `MemoryMax=32M` and `MemorySwapMax=0` was OOM-killed at a
32 MiB peak after writing 32,210,944 bytes while attempting 128 MiB under
`/run`. The preparer unit now also sets `MemorySwapMax=0` alongside its
existing 128 MiB memory ceiling, so output pages written by that activation
cannot escape its cgroup limit through swap. The rendered actual unit reported
both limits and prepared a tiny exact candidate successfully; its temporary
unit, binary, bundle and checkout were removed. This covers one preparation
activation. The broker still needs a bound on aggregate retained checkouts
across runs and the free capacity of `/run` before D3 closure.

Under its serialized run lock, the root broker now requires `/run` to be
tmpfs and checks free blocks before each new preparation. It keeps the larger
of one quarter of that filesystem or 256 MiB free, plus the preparer's 128 MiB
per-activation ceiling, and rechecks the reserve before retaining a completed
checkout. The preparer separately verifies its bound output mount is tmpfs.
A disposable installation of the actual broker socket and preparer accepted a
tiny local bundle, prepared the exact candidate and was removed afterward.
This verifies the normal path through the checks. A low-free-space refusal
under a constrained `/run` mount has not been exercised; concurrent non-Factory
writers can also consume free space after admission.

The staged run now carries a root-owned profile declaration. The broker checks
it before preparation, reviewer and relay attestation, agent arming, and
retirement, and echoes it in replies. The agent role manifest and direct agent
attestation also carry the profile. The desktop provider policy compares the
relay profile before activating its credential owner. A disposable activation
of the actual broker socket accepted staging and exact replay, refused the
same run and digest under another profile for replay, preparation, both
attestations, agent arming and retirement, then accepted exact retirement.
This is profile consistency after staging; the broker still lacks an
authenticated Go coordinator decision proving which profile may stage the
run. The agent service profile and credential path were not activated in that
probe, and D3 remains open.
After the profile was added to the retirement tombstone name, a second
activation simulated interruption after both bundle and profile declaration
were unlinked. A wrong-profile retry was refused, while the exact-profile
retry removed the empty tombstone; restaging and retiring the same run then
succeeded. All disposable units and runtime state were removed.
