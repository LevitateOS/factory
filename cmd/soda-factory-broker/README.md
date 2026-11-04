# Privileged run bundle staging

`soda-factory-broker` is a narrow, socket-activated system service for the
first N3 workspace-transfer boundary. The installer must render the socket
template for the selected desktop account and install the root-owned binary
and units. The current desktop runtime does not connect to it and no installer
enables it. This command only stages bytes; it cannot admit or launch an agent.

One connection carries a 4-byte little-endian JSON length (at most 256), then
`{"kind":"stage_bundle","version":1,"run":"<32 lowercase hex>","length":N,"sha256":"<64 lowercase hex>"}`,
then exactly `N` raw bytes. `N` must be from 1 through 512 KiB, matching the
current Go reviewer source-bundle bound. The service checks the Unix stream's
kernel peer UID against the desktop-owned mode-`0600` socket, requires a
root-owned mode-`0755` socket parent, no-new-privileges and a root-owned
mode-`0700` systemd state directory. It accepts no host path, role, credential
or unit property. The systemd profile has no capabilities, no IP access and a
15-second lifetime. A JSON response names only the run and digest.

Under a serialized lock, the broker checks SHA-256 and stages the bytes under
`/var/lib/soda-factory-broker/runs/<run>/bundle`. The state and `runs` parents
are root-owned mode `0700`; the finished run child is mode `0555` and the bundle
mode `0444`. A worker cannot traverse the original source but a privileged
manager can bind the selected child read-only into one worker instance. Up to
64 finished run IDs are retained. An identical request is idempotent; a changed
bundle for the same run is refused. Interrupted staging directories are removed
under the lock before another run is staged. No Git command runs as root, and
the raw bundle is not treated as a verified candidate until an unprivileged
consumer checks its Git contents against a current coordinator admission.

On 2026-11-04, the rendered units passed `systemd-analyze verify`. A temporary
root-owned installation staged a 359-byte local Git bundle; an identical replay
succeeded. Changed bytes, a wrong digest, invalid run, extra path field,
oversized declaration and truncated payload were refused. The resulting state
had the intended root ownership and modes. An unprivileged transient service
could read the bundle through a read-only bind while the original source path
was inaccessible, and it cloned that bundle into a disposable private runtime.
The temporary units, binary, state and scratch repository were removed.

A follow-up transient proof used a separate unprivileged Git preparation
service to clone a mounted bundle into a writable child under a root-private
parent. After freezing that child, a dynamic reviewer UID could read only the
finished checkout through a read-only bind and could not change or add files.
The proof used `nobody` for preparation; an installed preparer needs a dedicated
static UID so unrelated processes cannot share that identity. The temporary
checkout and source were removed. No preparation service or reviewer launch is
currently wired to this broker.

This is a staging primitive, not the D3 broker closure. It does not check a
Go-admitted profile/role/preparation, prepare an immutable reviewer checkout,
select a worker unit, mediate provider credentials, retire active runs or
perform a real provider task. It must remain uninstalled until those launch and
retirement controls are wired and qualified.
