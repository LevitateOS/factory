# Factory native runtime

Rust owns the desktop's native process/storage lifetime. Go retains the Factory
workflow, admission and canonical PostgreSQL schema. This crate currently
implements the Linux PostgreSQL lifecycle, private backend inputs, managed
PG/Go supervisor and OS account credential custody. The production Tauri host
is [`cmd/soda-desktop`](../../cmd/soda-desktop/README.md); native executor work
remains a later slice.

`database::NativePostgres::start(payload, profile)` creates or reopens an owned
private profile, holds its database lock, initializes a fresh cluster, and owns a
foreground server child. It uses a SCRAM-authenticated Unix socket with TCP
disabled. The profile/socket directories are `0700`; its generated database
password and log are `0600`. Connection URIs are secret backend inputs, never
frontend state or log output. Runtime dynamic shared memory uses files in the
cluster. Durability remains enabled.

Explicit stop sends the owned child a fast shutdown and waits for its checkpoint
and exit. Drop also retires the child. Timed-out owned commands are killed and
reported as failures. Existing clusters with another major version, unsafe paths,
or missing credentials are refused; their data is not replaced or upgraded.
Socket paths must fit the native Unix socket limit. The desktop host selects
profile paths; T14 implements supported distribution/upgrades.

## Managed native children

`supervisor::NativeSupervisor::start(payload, backend, profile)` owns one private
`supervisor.lock`, starts the existing native PostgreSQL owner, prepares a fresh
backend capability, then starts the production Go executable with only its
private configuration path. A dedicated owner thread stays alive for both
children. Linux parent-death signals retire them after abrupt supervisor death;
normal stop waits for Go admission/drain and exit before stopping PostgreSQL.
Timeouts force retirement and report failure, never a clean stop.

`health()` freshly checks owned child exits and authenticated bounded Unix HTTP
readiness. The response must bind the retained profile and the same captured
process instance. Readiness is store/process availability, not an account login,
execution admission or permission to resume work. No key, DSN, socket selector or
provider credential is returned. Native process IDs in the Rust observation are
for owner diagnostics; they are not webview process-control capabilities.

The owner detects a child exit and retires its remaining child; it does not
silently restart work. `stop()` joins retirement and releases ownership. Drop also
retires; a window-close handler must retain the supervisor, while explicit Quit
consumes it. UI activation, single-instance window messaging, suspend/resume and
the bounded Tauri product bridge belong to the desktop host.
Existing incompatible clusters and private credentials are retained on failure.

Direct `NativePostgres` consumers must keep their spawning thread alive until
stop: Linux parent-death semantics are tied to that thread. Applications should
use `NativeSupervisor`, whose dedicated owner supplies that lifetime even when
the handle moves between threads. The direct native acceptance driver stays on
its main thread.

## Native account credentials

`credentials::Credentials::open(profile_directory, profile_id)` scopes OS
items to this application's retained native profile. It uses the Linux
[Secret Service API](https://specifications.freedesktop.org/secret-service/latest-single/)
through the current `secret-service` crate with encrypted D-Bus sessions and
Rust crypto. An existing desktop service such as GNOME Keyring supplies storage;
this crate does not create a password database, use a plaintext fallback or
automatically unlock a locked collection. Missing/locked services return bounded,
sanitized errors. The supported installed desktop prerequisites remain T14.

Only native connectors construct an `Account` from a remotely verified provider,
HTTPS instance and opaque account ID. `store(account, Token)` writes the token
to the OS service; reauthentication retains a current connection's ID and rotates
its revision. Stored metadata does not establish provider authentication or
execution admission. Browser/device login and provider verification belong to T06
and T10. `Token` has no Debug/Serialize implementation and clears its owned bytes
on drop. Token bodies are limited to 8 KiB of visible ASCII.

`status()` returns only connection/provider/instance/account metadata and
stored/locked/withdrawn state. `capability(connection)` creates a Rust-only,
owner/connection/revision-scoped capability lasting 30 seconds. `with_token`
fetches fresh OS state, rejects withdrawn/replaced/expired capabilities and lends
the token to trusted native code for a single operation. That consumer must bound
its operation duration and must not copy the token into browser state, a Go
configuration file or logs. Keyring operations and queued access have a five
second deadline. Ambiguous or excessive results fail visibly.
Capabilities also capture non-secret provider/origin/account/connection metadata;
fresh OS metadata must still match it on use. The captured metadata is not a
provider login assertion.

`with_token_operation(capability, async_callback)` supplies the awaited native
read boundary. Its total budget, including the initial OS lookup and final
validation, is eight seconds or the remaining captured lease, whichever ends
first. It releases the keyring gate while the callback waits, so withdrawal and
reauthentication can proceed. Before returning a result it freshly checks the
same connection/revision/owner against OS state. Withdrawn, replaced, expired or
retired credentials refuse the result. Native read I/O must honor cancellation
and must not detach work or retain credential copies. Dropping/timing out the
future drops its callback and clears its owned zeroizing token. This primitive
does not itself implement GitHub HTTP, account binding or provider login.

`withdraw(connection)` first records an empty denial marker in the private
profile's `withdrawn-credentials/` directory, then attempts OS deletion. A locked
or unavailable service reports failure, while the denial persists across reopen;
explicit native reauthentication can clear it after storing a new revision.
The marker contains no token. Directories are `0700`, files are `0600`, and the
write and directory entry are synced before deletion. A marker persistence error
is reported as failure; it is never represented as completed withdrawal.

The host retires credential capabilities on runtime replacement/loss and Quit.
Closing a window retains custody for native work. Tauri exposes only
`credential_status` and `credential_withdraw`; neither token retrieval nor token
storage is a webview command. Withdrawal also retires the browser binding and
observations. Provider request/refresh consumers must use the native capability
boundary; connected provider work is not implemented by credential storage alone.

## Native GitHub read transport

`NativeSupervisor.github_read(credentials, capability, github::Read)` accepts
typed identity, account repository-page, stable repository-ID, issue/PR and
ordinary conversation reads. It uses the owner thread's fresh private
channel/instance and the awaited credential boundary above. Only GitHub-scoped
credentials can reach it. A future does not
borrow the supervisor, so runtime retirement can proceed while it is pending;
retired runtime or credential authority prevents a result from being returned.

`Read::Issues` selects a stable remote repository, page/limit and open/closed/all
state (empty defaults to open). `Read::Issue` selects any-state detail by display
number. Native validation rejects invalid limits, state, repository IDs and zero
numbers before GitHub I/O. Go still verifies the current account first. Successful
replies must match the selected kind, account, connection and issue repository.
The returned issue facts contain remote identities; canonical registry mapping
and the production UI connection remain consumers.

`Read::Pulls` selects open (default), closed or all PR pages; `Read::Pull`
selects any-state detail by number. Both use stable repository IDs and require
the same descriptor/account/reply-scope checks. Missing draft/mergeability facts
and deleted fork repositories remain null. Branch commits are PR-payload
observations; file/diff and current merge eligibility are later readers.

`Read::PullDiff` discovers a PR merge-base/head pair with two empty commit
selectors or verifies an explicit full pair. Native validation rejects malformed
and half-pairs. Go captures the actual PR base/head, resolves the merge base and
rechecks current PR/repository facts; changed pairs refuse. Rust checks returned
scope, parent and commits before releasing a result. The captured PR base remains
separate from the diff merge base and current target-tip/protection consumers.
Real public discovery/pinned-pair/mismatch checks and production-native full-pair
authentication refusal pass. Authenticated/private scope and canonical/UI
integration remain consumers. No file catalogue is returned by this operation.

`Read::IssueComments` and `Read::PullComments` select an explicit parent number,
stable repository ID and bounded page. Go verifies the matching parent before
and after the ordinary comment read, including the actual pull ID for a PR, and
rechecks repository visibility. Rust checks the reply's parent kind/number and
repository alongside the native account scope. Review submissions/inline threads
remain separate resources. Native selectors expose no token or execution grant.

The token travels only as a sensitive per-request header on authenticated Unix
IPC to the trusted Go child. It is not browser state, a bootstrap configuration
field, environment variable or stored Go token source. The native Go composition
registers a fixed private read RPC behind peer/key/instance and application
lifetime admission. Its GitHub adapter verifies `/user` against the captured
account on each call before any repository read, then uses the existing bounded
Go SDK client. It neither binds a Factory account nor starts automation.
Dropping the Rust future closes IPC and cancels the Go request context.

The read port consumes an already stored connection. For initial login,
`NativeSupervisor.github_verify(Token)` consumes a new, unpersisted native token
and requests only actual GitHub `/user`. Its eight-second total deadline includes
channel acquisition and I/O. On success, it returns a remotely observed `Account`
and the original opaque `Token` to native login code for `Credentials::store`;
neither type implements Serialize or Debug. It never stores a placeholder account
or writes to OS custody. Provider refusals expose only HTTP status. Cancellation,
failure or retired runtime drops the token and cannot return an account.

The new-token request uses the same peer/key/instance/admission gates, sensitive
header and cancellable Go SDK path as stored reads. It accepts no claimed account,
connection or repository selector; stored reads still require their captured
account/connection. Live OAuth consent and successful verify/store, canonical
account binding and desktop UI selection remain with the login/connection
consumer. Generated invalid credentials qualify refusal only, not successful
login or private repository access.

## Private backend inputs

Before starting the Go child, call `NativePostgres::backend_config(&mut self)`
while owning the profile/database. It returns the absolute `backend.json` path.
The supervisor must stop the previous backend before preparing another start;
the bootstrap writer does not itself manage that child. NativeSupervisor enforces
this ordering and never rotates a live backend’s credentials.

The method creates or validates retained `profile.id`, then writes `backend.dsn`,
a fresh per-start `backend.key`, and finally the small `backend.json` manifest.
The profile ID stays fixed across native database restarts; IPC keys rotate.
Writes use owned `0600` temporary files, sync and atomic rename within the
`0700` profile. Existing unsafe inputs and malformed retained IDs refuse.
The database password, accounts and retained store are not reset. The future
backend socket is `backend.sock` in that same profile.

The Go [runtime loader](../../internal/factory/runtime/README.md) validates
this exact private layout and owned PostgreSQL socket without appliance config.
Configuration and secret inputs stay out of the webview. They establish no
provider login, canonical ledger adoption, runtime placement or work authority.

## Native payload

Use one native PostgreSQL build with this relative layout:

```text
bin/{postgres,initdb,pg_isready,pg_controldata}
lib/libpq.so*
lib/postgresql/*.so
share/                     # PostgreSQL catalogs, locale, timezone and SQL assets
POSTGRESQL-LICENSE          # upstream COPYRIGHT
```

`postgres`, `initdb` and `pg_isready` serve the lifecycle; `pg_controldata` allows
offline diagnostics. Retain the standard runtime modules and data from that same
build; developer headers, static libraries, PGXS, server administration tools and
documentation are excluded. Children load the payload's own `lib` after
relocation. Current Linux system dependencies are glibc/libm and zlib.

Resolve [official current versions](https://www.postgresql.org/versions.json)
and the selected [source/checksum](https://www.postgresql.org/ftp/source/) before
each new build. Use the latest stable release rather than a release candidate.
Follow upstream's [native build instructions](https://www.postgresql.org/docs/current/install-make.html),
with an owned staging prefix and these selected options:

```sh
./configure --prefix=ABSOLUTE_STAGING_DIRECTORY \
  --without-icu --without-readline --without-zstd --without-lz4
make -j16
make install
```

Copy the runtime subset above into a separate payload. Keep its PostgreSQL
license. These options use PostgreSQL's built-in `C.UTF-8` locale and private
socket transport, avoiding unrelated ICU, interactive readline, TLS and optional
compression dependencies. This does not disable HTTPS in the Go provider clients.
Building requires C/Make, Bison/Flex, Perl's build modules and zlib development
headers; these are developer requirements, not desktop installation requirements.

## Actual native integration check

From the repository root, with an already built payload and a **new** profile:

```sh
cargo run --locked -p soda-pg-fixture --bin soda-native-pg-check -- \
  /absolute/native-postgres /home/USER/new-native-pg-profile -- \
  go test -count=1 -v -run '^TestNativePostgresRetainedTransaction$' ./internal/store
```

The driver prepares backend inputs before each selected Go command and supplies
their path as `SODA_NATIVE_FACTORY_CONFIG`, alongside its private database input.
The driver invokes the existing Go store integration file across three server
lifetimes. It verifies a committed transaction updating both a feed post and its
intake, retained idempotency and stale-edit refusal, then commits another revision
before deliberately killing its own server process group. WAL recovery must
retain that revision. All phases check durable settings and reject a wrong
password. The final shutdown must remove the server PID/socket; no container,
system service or manually prepared database participates. Profiles/logs remain
for inspection; the check refuses to remove an existing profile.

For a relocation proof, compile the Go test executable first, then run the same
driver with that executable while the staging prefix is inaccessible. The
executable accepts `-test.v -test.count=1
-test.run=^TestNativePostgresRetainedTransaction$`. Merely moving a payload while
its original libraries remain accessible is insufficient.

## T00-PG evidence, 2026-10-31

Official metadata selected PostgreSQL **18.6**; 19 was still RC1. Source SHA-256:
`555610c24d53e4316da5b7d3fc25c279d96856d5e0e23ee308c328c5fa881d9f`.
The upstream SHA-256 matched before building. This records the observed selection,
not a pin for future builds.

The Linux x86_64 subset was **17,020,535 bytes**, **6,377,639 bytes** as gzip tar.
On this Rocky Linux 10.2 host, the relocated payload initialized/started in
996 ms, restarted in 102 ms, and recovered after abrupt termination in 783 ms.
All three real store phases passed with the build prefix hidden in a private
mount namespace. `pg_controldata` reported `shut down` after the final exit;
profile/socket permissions were `0700` and the password `0600`.

Run log: `/home/vince/factory-native-pg-relocation-final.log`. Retained profile:
`/home/vince/fpg-relocation-final-20261031`. Payload/source/builds live under
`/home/vince/.cache/soda-native-pg/`. No system packages were installed; missing
Perl build modules were extracted into the existing owned development sysroot.

The built binaries require **GLIBC_2.38** or newer. This proves this native host
and payload, not every Linux distribution, an installed app or other CPU
architectures. T14 selects/qualifies the distribution baseline. The new crate
reuses current getrandom/url; the freshness check selected libc 0.2.191, requiring
the workspace's exact libc selectors and lock to advance together.
