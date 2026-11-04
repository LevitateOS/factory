# Factory native runtime

Rust owns the desktop's native process/storage lifetime. Go retains the Factory
workflow, admission and canonical PostgreSQL schema. This crate currently
implements the Linux PostgreSQL lifecycle, private backend inputs, managed
PG/Go supervisor, OS account credential custody and the complete authenticated
GitHub connector. The separate headless process primitive lives in
[`lib/native-process`](../native-process/README.md). The
production Tauri host is [`cmd/soda-desktop`](../../cmd/soda-desktop/README.md).
Workspace/Git/custody composition and product execution activation remain later
slices; no coordinator or selectable native worker is enabled.

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

## Native agent execution gate

This crate does not depend on `soda-native-process` or expose a worker launch
API. The separate process primitive retains same-user process and cgroup checks;
it does not isolate a hostile worker from the desktop user's files or keyring.
Native workspace/Git, a distinct worker host identity, brokered credential
delivery, durable invocation/restart receipts and real provider integration
remain the N3/N4 consumers. Product execution stays unavailable until D3 proves
those boundaries on the actual worker path.

## Managed native children

`supervisor::NativeSupervisor::start(payload, backend, profile)` owns one private
`supervisor.lock`, starts the existing native PostgreSQL owner, prepares a fresh
backend capability, then starts the production Go executable with only its
private configuration path. A dedicated owner thread stays alive for both
children. Linux parent-death signals retire them after abrupt supervisor death;
normal stop waits for Go admission/drain and exit before stopping PostgreSQL.
Timeouts force retirement and report failure, never a clean stop.

Product I/O has a separate joined current-thread runtime. `product_request()`
owns its inputs and can be awaited without borrowing the supervisor; dropping
even an unpolled future cancels its request. The blocking `product()` adapter
uses the same admission and I/O owner. Sixteen permits bound queued/in-flight
requests and unread native completions. Bodies remain limited to 64 KiB in and
2 MiB out, with a 15-second wire deadline and 20-second caller wait limit.
Admitted requests also support subscription PUTs and the shared repository
product routes. Ordinary readiness-only product calls retain their narrower
GET/POST Factory route surface.

Product requests carry the captured process instance as well as the private key.
Retirement closes admission, cancels and joins request tasks, and drops their
runtime/HTTP drivers before stopping Go and PostgreSQL. Caller-side lifetime
checks refuse buffered late replies. Child monitoring does not wait for product
I/O; the existing bounded health probe retains its separate readiness timeout.

`admit_product(credentials, capability, remote_repository)` creates a finite,
native-only `collaboration::NativeRequest`. Consuming it with `request()` uses
the same product I/O owner and adds a private operation ID. It cannot be cloned
or serialized. Up to sixteen operations may exist, including unpolled ones;
each expires after twenty seconds or earlier custody retirement. A missing
repository scope allows identity/custody checks only. A selected remote ID is
a native resource limit, not a canonical product identity or permission.

A separate joined collaboration runtime serves semantic callbacks on the owned
`host.sock` (`0600`). Go can freshly request custody, account or selected-resource
observations while its shared services run. Rust uses the existing complete
GitHub connector and its defining `Read` selectors; it accepts no provider URL,
HTTP signing request, canonical selector or credential from Go. The Go client
exposes Current, Identity, Repository and finite semantic read observations for
the Go shared collaboration services. Current proves custody only,
while Identity performs the actual provider account check. Replies include the
operation's admitted optional remote repository, never a product/browser-selected
scope. The provider decodes nullable push permission; Go decides whether those
facts admit a bound product request save.

Callbacks require the current-user peer, private key, captured Go instance and
a live admitted operation. Browser Origin/Fetch headers refuse. Requests are
bounded to 8 KiB, headers to 16 KiB and observations to 2 MiB; calls have an
eight-second limit and connections a nine-second limit. Provider errors retain
only safe status/code/rate facts. Drop cancels product and callback work; stale
operation replay refuses. Final custody validation runs on this same joined
owner. Retirement cancels and joins callbacks and finalizations before retiring
the provider/product owners and stopping the children. The supervisor lock
protects stale owned-socket recovery; cleanup preserves any replacement inode.

The production Go executable now binds its shared core product services only for
these admitted operations, using fresh verified facts and one canonical mapping
transaction. Ordinary unscoped calls still refuse. Shared collaboration read
sources and the shared desktop UI now consume these ports. Native connection
selection and its cancellable Tauri transport stay in the desktop host.
This runtime does not start
the product coordinator or grant planning/execution authority.

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

## Factory provider request policy

`factory_provider::Policy` is a native-only request gate for one exact profile,
run, staged bundle digest, candidate and provider connection. Its trusted caller
supplies a reviewer/coder role, finite deadline and bounded call, input-byte
and output-token budget. It admits only the Muse
model catalog and streaming Responses routes at fixed Meta HTTPS destinations.
For Responses it pins `muse-spark-1.3-contributor`, requires `store:false`,
reduces each request to at most 4,096 output tokens, disables parallel tool
calls, removes unneeded response includes and filters the nested Muse function
namespace by role. Reviewers retain only `read_file` and `search`; coders may
also retain `write_file`, `edit_file`, `bash` and `bash_input`. Unknown top-level
request fields, built-in tool namespaces and duplicate tool names fail closed.
`factory_provider::BoundOwner` now binds a per-run Unix socket under the
desktop user's `0700` runtime directory. Before binding, it requires the policy
profile to match its credential owner and the policy connection to match the
supplied Muse capability. It does not serve until `activate` consumes an exact
`RelayAttestation` from the native broker client. That client checks the fixed
broker socket in a root-owned directory, its root peer, framed replies and
profile/run/digest/candidate identity. Before serving, the owner compares the
attested profile, run, digest and candidate to its captured policy. It checks
the attested relay UID with `SO_PEERCRED`,
requires a random non-account placeholder bearer, applies the run policy,
renews the same OS credential revision and borrows the token only inside a
revocable native streaming operation. It sends the bearer to fixed Meta HTTPS
routes with no proxy, redirect or retry. Requests, concurrent work, response
wire bytes and lifetime are bounded; provider errors return generic local
bodies. `Owner::retire` cancels active work, removes the exact socket, then
asks the broker to retire the exact bundle and relay; Drop attempts the same
sequence. The future desktop caller must bind before broker relay attestation,
activate with the returned identity, and handle retirement failures. Neither
that caller nor an agent launch is connected yet.

A dummy-key Muse 1.4.14 request to a local fake provider showed that the CLI
advertised `workflow`, memory, monitoring and reminder functions even with
shell, write, web, reminder and user-input flags disabled. It requested
128,000 output tokens for one model step. The actual 117,775-byte coder body
passed the native policy checker after filtering: 4,096 output tokens and the
six named coder functions remained. The same body under reviewer policy kept
only two read functions. Wrong model, stored response, built-in tool namespace,
extra routes, retirement and exhausted budget were refused. The fake provider
returned 401; no saved credential or real model was used. The temporary
listener and scratch source were removed.

A separate isolated GNOME Keyring check used an invalid fixture token and a
subordinate host UID in a private mount namespace. The desktop UID was reset
at the owner socket; the distinct UID reached it through a socket bind and
received 400 without the placeholder and 403 for an out-of-policy Responses
body. An admitted model-catalog request reached Meta and returned a sanitized
401. After keyring withdrawal, the same request returned 503; stopping removed
the socket and its directory. This proves the owner/keyring/UID path for that
fixture. It did not use the broker's actual systemd relay or a real Muse grant.

The native broker client was exercised against a disposable framed service in
a private user and mount namespace. Distinct mapped UIDs represented the root
service and desktop client; exact staging, preparation, relay attestation and
retirement succeeded, while a mismatched candidate reply was refused. This
checked the client protocol and peer check, not the installed system broker.

## Native GitHub connector

The headless `github_provider` owner implements fixed-origin authenticated HTTPS,
device authorization, refresh, current-account verification and provider-local
read decoding. It has no Tauri dependency. Its dedicated runtime admits at most
16 outstanding requests, including unread completions; requests have eight-second
bounds, 16-KiB headers/OAuth bodies and 8-MiB upstream API bodies. Native read replies
remain bounded to 2 MiB. Redirects, cookies and automatic retries are disabled.
Host proxy settings and platform TLS trust remain native inputs. Only numeric
bounded pagination is retained: GitHub may change a Link path from an owner/name
locator to a stable repository ID; Link URLs are never followed.

`NativeSupervisor.github_read(credentials, capability, github::Read)` freshly
admits native custody and verifies the actual account through /user. It accepts
semantic selectors for repositories (including bounded owner/name lookup), issues,
PRs, ordinary discussions, reviews,
inline comments, REST review reply groups, check runs, statuses, immutable diffs
and file pages. Reply groups collect a complete bounded forest (1,000 comments,
20 upstream pages, 8 MiB aggregate) under the existing operation deadline; cycles,
dangling replies and duplicate identities refuse. Catalog previews contain ten
comments and retain continuation; detail pages use actual root comment IDs.
These observations do not claim GraphQL resolution or immutable/current anchors.
Nullable private/push facts remain nullable. Workflow jobs and branch-rule reads
remain a required later producer. One Rust
selector defines request bounds. Stable repository/parent/current-head checks
bracket the relevant operations. Unknown/null facts remain unknown; read access
and CI observations never create product mutation or execution authority.

Native OAuth produces an opaque grant; account verification precedes encrypted
OS storage. Refresh uses the existing durable reservation and conditional custody
publication, with no old-pair reuse on uncertain outcomes. An explicitly rotated
complete pair and freshly verified account are required. A retired owner closes
admission, cancels and joins HTTP/DNS/TLS work, refuses stale replies and cancels
operations waiting for native mapping/custody. No slow provider call holds the
child-control thread or desktop lifecycle mutex.

Canonical operations send bounded nonsecret observations to Go at
`/api/native/collaboration/observe`, behind peer/key/instance/coordinator admission.
Go resolves host-scoped local repository IDs and atomically maps each observed
account/repository/participant batch. Rust validates the returned canonical scope;
Go owns neutral issue projection and all SQL. The old Go provider implementation,
SDK and credential-carrying native routes are removed. Canonical mappings remain
observations, never cached provider permission or an active product binding.

Real native refusal, Secret Service, PostgreSQL, removed-route, nonsecret mapping
and late-rollback checks pass. The corrected approved Rust journey also passes:
two repository pages, private access, canonical issues, PR/diff/file and evidence
reads, actual refresh, credential reload and stable canonical IDs after restart.
The later shared-reader journey also passes through production Go services backed
by Rust: live paged issues, PR discussion/reviews/files/checks, an actual REST reply
group and stable canonical account after restart. Declared semantic-boundary
checks separately cover nullable facts, scoped cursors, reply continuation and
atomic fork-reference isolation. Neither claims connected or installed UI.
The locally stored test grants were withdrawn and owned PostgreSQL stopped. The
initial pagination-alias failure remains retained evidence. Actual Linux Tauri
controls render a real device challenge, hand off the fixed browser URL and cancel
without storing a credential. The opener receiver is a declared fixture.
Remote grant revocation, connected shared UI, native execution and installed
product qualification remain downstream; historical Go evidence is not reused
as proof of the new owner.

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

The Go [runtime loader](../../pkg/factory/runtime/README.md) validates
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
compression dependencies. This does not disable HTTPS in native provider clients.
Building requires C/Make, Bison/Flex, Perl's build modules and zlib development
headers; these are developer requirements, not desktop installation requirements.

## Actual native integration check

From the repository root, with an already built payload and a **new** profile:

```sh
cargo run --locked -p soda-pg-fixture --bin soda-native-pg-check -- \
  /absolute/native-postgres /home/USER/new-native-pg-profile -- \
  go test -count=1 -v -run '^TestNativePostgresRetainedTransaction$' ./pkg/store
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

Canonical `Binding`, `CanonicalRepositories`, `CanonicalIssues` and `CanonicalIssue`
keep local-ID semantics separate from raw provider reads. Go resolves a local
repository on the selected host before Rust reads its remote ID. Rust verifies
the current provider account and repository evidence, then Go transactionally
maps the resulting facts and projects neutral pages. Empty filtered issue pages
can retain a continuation; PR wrappers cannot become ordinary issue detail.
Capabilities remain unknown. Product selection and connected UI remain consumers.
