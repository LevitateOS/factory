# Headless native process primitive

`soda-native-process` owns finite Linux processes without Tauri, Secret Service,
a Go product workflow or a browser command surface. Its caller supplies a
private directory, sealed `Executable`, explicit arguments/environment, PTY size
and hard limits. The crate executes under its caller's host UID. It is not linked
into the production desktop runtime and does not itself qualify an agent worker.

`Executable::capture` hashes native ELF bytes into a sealed memfd. Replacing the
source path cannot replace an admitted invocation. Script/provider freshness and
authorization belong to the later harness consumer.

`Executable::from_stream` bounds, hashes and seals bytes under the receiving
worker UID. Its source path is retained only as provenance; the worker never
opens that desktop path to execute. The protocol owner must bound transfer time
and validate the complete frame. `Executable::copy_to` streams the exact sealed
capture without rereading the mutable source. Both capture and import use a
512 MiB ceiling: the currently installed Muse ELF is 415 MB and exceeded the
old 128 MiB limit. On this Rocky Linux host, passing the
desktop-created sealed memfd with `SCM_RIGHTS` across the UID boundary failed:
SELinux denied the worker write access to its `user_tmp_t` labeled memfd and
`recvmsg` returned truncated control data. A same-UID transfer control passed.
The byte-stream design avoids that label crossing.

An external live check sent `/usr/bin/true` bytes captured under the desktop
home, replaced the source path, and ran the re-sealed image inside the
socket-activated UID-61739 worker with delegated CPU/memory/process cgroups.
The worker could not read the source path. Digest, ELF header, truncation and
declared-size refusals passed; the valid child exited zero and its tree retired.
This checks the import/process boundary, not a product launch or provider run.
The observed 415 MB Muse 1.4.14 ELF separately streamed and ran `--version`
under the same worker service profile; it exited zero and its tree retired.
This did not authenticate, invoke a model or qualify provider credential custody.

`Processes::launch_with_stdin` now accepts at most 1,024 native-only input
bytes. It gives the child a pipe for stdin, closes it after the bounded write,
keeps stdout/stderr on the PTY and zeroizes the owned input. A write failure
retires the child tree and refuses launch. The intended worker use is a
run-specific provider placeholder; an account token must never enter this
channel. A disposable observer in a user-delegated systemd scope read the
fixture input without echoing it to the terminal log, verified the original
all-PTY path, and observed zero child exits with retired cgroup trees. Empty
and over-1,024-byte inputs were refused. This is a process I/O check, not an
isolated agent or provider task.

The caller must supply a user-delegated cgroup-v2 root with CPU, memory and
process controllers enabled, and permission to move its children into that
subtree. The primitive never enables host-wide controllers or falls back to a
process-group-only boundary. Each invocation enters its resource scope before
executable code can fork. PTYs acquire close-on-exec descriptors atomically.

The monitor owns deadline, cancellation, bounded output and detached-descendant
retirement through `cgroup.kill`, independently of request polling. Direct child
exit, empty process tree, complete output and credential release remain distinct
facts. Owner retirement joins monitors; uncertain cleanup fences further work.
An exclusive private lock and its file identity bind the scope namespace to its
profile/storage owner. Reopen retires only retained scopes of that owner.

Launch also captures a Linux pidfd before readiness or reaping. `Process::is_live`
polls that exact kernel identity; missing or failed observation returns an error.
It does not derive liveness from cached monitor state or a current numeric PID,
and does not prove descendant retirement, output completeness or custody. The
isolated agent uses this query both for bound current-process requests and to
confirm the direct child is non-live before returning its terminal result.

The exact-run agent service now opens this process owner before it attests.
Its systemd unit delegates CPU, memory and process controllers with the main
process in an `init` subgroup; the worker validates the exact cgroup path and
UID, enables those controllers only under its own service, and fails closed
without them. A disposable activation under that unit profile attested; a
missing subgroup produced no reply and a delegation refusal. This qualifies
owner admission and cgroup location, not an agent child launch.

Concurrent instances of the same dynamic-user template received the same
host UID. The agent unit therefore gives each run a private PID namespace and
binds only its own delegated cgroup under a tmpfs overlay that hides the host
cgroup tree. A two-instance dummy probe denied cross-run runtime and cgroup
visibility while both instances remained active. The actual agent unit then
attested after opening `Processes` under that fixed private mount. A diagnostic
binary substituted only for that unit's `ExecStart` launched a captured finite
child, observed zero exit and tree retirement, then cancelled an invocation
after it spawned a background child and observed `Cancelled` with tree
retirement. The disposable unit and binary were removed. This qualifies the
primitive under the agent service profile, not the production agent's still
unavailable execution path.

The desktop user's host UID does not isolate a hostile process from that user's
files, login session or cgroup authority. D3 requires a dedicated system worker
with a distinct host UID, exact workspace access and brokered credentials before
this primitive can be used in a selectable native executor. A user namespace
mapped back to the desktop host UID is insufficient. The existing direct process
check under `.factory-rust-provider-check/process-v1` proves only this primitive's
process/resource behavior, not D3 or a real provider run.
