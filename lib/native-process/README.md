# Headless native process primitive

`soda-native-process` owns finite Linux processes without Tauri, Secret Service,
a Go product workflow or a browser command surface. Its caller supplies a
private directory, sealed `Executable`, explicit arguments/environment, PTY size
and hard limits. The crate executes under its caller's host UID. It is not linked
into the production desktop runtime and does not itself qualify an agent worker.

`Executable::capture` hashes native ELF bytes into a sealed memfd. Replacing the
source path cannot replace an admitted invocation. Script/provider freshness and
authorization belong to the later harness consumer.

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

The desktop user's host UID does not isolate a hostile process from that user's
files, login session or cgroup authority. D3 requires a dedicated system worker
with a distinct host UID, exact workspace access and brokered credentials before
this primitive can be used in a selectable native executor. A user namespace
mapped back to the desktop host UID is insufficient. The existing direct process
check under `.factory-rust-provider-check/process-v1` proves only this primitive's
process/resource behavior, not D3 or a real provider run.
