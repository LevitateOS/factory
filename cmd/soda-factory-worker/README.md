# Isolated worker socket boundary

`soda-factory-worker` is an inert, one-connection-per-service Linux binary.
The installer must render `systemd/soda-factory-worker.socket.in` with the
selected desktop account, install both units and the root-owned executable, and
enable the socket. The template service starts each accepted connection under
a separate dynamic system UID with private home, IPC, devices, temporary files,
hidden other-user process metadata, no ambient capabilities and finite cgroup
resource ceilings. Its privileged system manager owns the socket and service
lifetime; the worker binary never runs as root.

The service accepts one 4-byte little-endian length followed by at most 256
bytes of JSON. Currently the only request is
`{"kind":"attest","version":1}`. It checks that standard input is a Unix
stream with kernel peer credentials, its own UID differs from the peer, no new
privileges are set, and desktop session variables were not inherited. It replies
with one length-prefixed JSON attestation and `execution_available:false`.
Unknown versions, fields and requests fail closed. No executable, path, token,
worktree, role or provider selector is accepted.

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
