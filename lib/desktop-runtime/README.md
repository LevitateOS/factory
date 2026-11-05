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

This crate does not depend on `soda-native-process` or expose a product launch
API. Its native-only broker client can request one isolated worker launch, but
the Go product and desktop lifecycle do not call it. The separate process
primitive retains same-user process and cgroup checks;
it does not isolate a hostile worker from the desktop user's files or keyring.
Native workspace/Git, a distinct worker host identity, brokered credential
delivery and invocation receipts now have native primitives and disposable
system-unit checks. Go admission, durable product consumption of the receipt,
production lifecycle wiring and a finite real provider task remain N3/N4
consumers. Product execution stays unavailable until D3 proves those boundaries
on the actual product path.

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

After taking the exclusive native profile lock and reading its retained ID,
supervisor startup now reconciles exact root broker staging before launching Go.
The broker returns one root-verified selector and its consumed-launch flag per
call. Rust retires unlaunched staging and refuses startup when a launch marker
survives or cleanup is uncertain. If a broker unit is installed but
its socket is absent, startup fails closed and stops PostgreSQL. An uninstalled
broker is skipped because this product still cannot admit native execution.
A disposable real PostgreSQL/Go supervisor check retired one staged profile
while preserving another and verified stopped-socket refusal. Go's retained
execution outcome still needs durable recovery before D3 closes. The marker
fence alone cannot recover an outcome after retirement or host reboot.

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
reduces each request to at most 4,096 output tokens, pins the provider cache
key to the admitted run, disables parallel tool calls, removes unneeded response
includes and filters the nested Muse function namespace by role. Reviewers
retain only `read_file` and `search`; coders may
also retain `write_file`, `edit_file`, `bash` and `bash_input`. Unknown top-level
request fields, built-in tool namespaces and duplicate tool names fail closed.
For successful Responses replies, the owner holds the bounded SSE body until
every event is parsed. It permits at most one function call, whose name must
appear in that request's filtered tool list. It refuses built-in tool events,
malformed or incomplete streams, and a disallowed call even after allowed
text. Nothing from a refused stream reaches the CLI. Holding the complete
response delays CLI streaming until the provider finishes; a finite real
provider run must qualify that behavior before execution is enabled.
`factory_provider::BoundOwner` now binds a per-run Unix socket under the
desktop user's `0700` runtime directory. Before binding, it requires the policy
profile to match its credential owner and the policy connection to match the
supplied Muse capability. It does not serve until `activate` consumes an exact
`RelayAttestation` from the native broker client. That client checks the fixed
broker socket in a root-owned directory, its root peer, framed replies and
profile/run/role/digest/candidate identity. Before serving, the owner compares the
attested profile, run, role, digest and candidate to its captured policy. It checks
the attested relay UID with `SO_PEERCRED`,
requires a random non-account placeholder bearer, applies the run policy,
renews the same OS credential revision and borrows the token only inside a
revocable native streaming operation. It sends the bearer to fixed Meta HTTPS
routes with no proxy, redirect or retry. Requests, concurrent work, response
wire bytes and lifetime are bounded; provider errors return generic local
bodies. `Owner::retire` cancels active work, removes the exact socket, then
asks the broker to retire the exact bundle and relay; Drop attempts the same
sequence. `RunOwner` composes the native-only path: bind the inert owner first,
stage an exact source bundle, prepare and attest the reviewer checkout and
relay, arm and attest the worker, then activate token use. It permits one
brokered execution attempt and releases its result only after owner and exact
broker retirement. Setup errors try exact retirement; an unconfirmed cleanup
is an error. Activation refuses a policy that expired during staging. Its
cancellation token is available to the future desktop Quit owner. No Go
admission or desktop lifecycle currently constructs this owner.
`RunOwner::start` now also requires the existing private profile directory and
checks its retained `profile.id` before staging. It creates and syncs the private
receipt directory before staging; the receipt
writer can also create it on first use after validating the exact observation.
An existing directory must pass the same ownership and mode checks.
On a completed broker reply,
it writes a framed v2 receipt to `native-run-receipts/<run>.receipt` under that
profile: a bounded JSON header with exact run, profile, role, admitted source,
tool, prompt, process, output and candidate-bundle digest facts, followed by at
most 16 MiB of output and 4 MiB of candidate bundle bytes.
It syncs the file and directory before broker retirement. A failed execution
or receipt write stops local token use and asks the broker to quiesce the exact
run without erasing root staging. An incomplete `.pending` receipt is retained
as uncertainty. `factory_provider::read_retained` checks the private file and
exact trusted run scope before returning the retained observation. Its caller
must obtain that scope from the Go ledger. The native receipt still needs an
admitted Go consumer, exact retained-run transaction and restart reconciliation;
its presence alone is not a product outcome, proof of root retirement, or
permission to reactivate an agent.
The writer and reader now share one `ReceiptScope` and v2 header validator;
`RunOwner::execute` calls `factory_provider::write_retained` before retirement.
In a disposable current-unit check, the typed Rust broker client received a
credential-free coder result from the real broker, preparer, relay and worker.
The production writer synced its 31-byte output and 419-byte candidate bundle
to a private receipt. A separate process reopened it after the writer exited,
recovered the same process/output/candidate facts and refused a wrong source
scope. Git verified the recovered bundle's descendant commit and one-line edit.
Exact broker retirement and removal of the temporary units and owner socket
passed. This did not exercise `RunOwner` with an actual provider credential,
Go ledger settlement, a crash/reboot or an installed desktop.
A later disposable current-unit check did compose production `RunOwner` with a
fixture Muse token in a separate private Secret Service profile. It completed
owner bind/activation, broker staging/attestation, isolated coder execution,
production receipt write and exact retirement. A separate process reopened
the retained 31-byte output and 419-byte candidate bundle after the private
D-Bus session exited; Git verified the expected descendant commit and edit.
The fixture ELF never called the provider, so token use, real Muse behavior,
Go ledger settlement, crash/reboot recovery and installed qualification remain
open.
After the broker returns, `RunOwner` retains the external cancellation signal
through owner and broker retirement and refuses a result if it was set during
that cleanup. The native caller still must join execution and
handle an unconfirmed retirement before reporting a terminal product outcome.

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

A later dummy-key loopback probe returned scripted Muse SSE. Under the reviewer
flags, Muse rejected `write_file` as unavailable but attempted the still
registered `read_memory` tool; that tool failed on the scratch-only arguments.
This confirms that removing a tool from the upstream request alone cannot be
the role boundary. A native check against the production SSE parser accepted
text and offered `read_file`, and refused late `read_memory`, `write_file`,
built-in tool events and incomplete streams. The probe used an isolated scratch
HOME and no saved credential; its listener and files were removed.

The current 396 MiB Muse 1.4.14 ELF also ran inside a disposable brokered
reviewer service with a dummy key and local fake owner. The service-generated
Responses body advertised 11 functions and requested 128,000 output tokens.
The production `Policy::admit` implementation, applied to that exact body by
the disposable owner fixture, retained only `read_file` and `search`, capped
output at 4,096 tokens and disabled parallel calls. The fake owner then
returned 401, and the worker returned a complete nonzero-exit observation
with exact retirement. The production credential-bearing `Owner` did not
serve this request, and no real model/tool response was exercised.

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

The broker client's cancellable execution call now watches a trusted native
owner's cancellation signal during upload and reply wait. Cancellation shuts
down its authenticated broker stream and refuses a late receipt. A disposable
root-peer Unix socket probe accepted the actual client request, then observed
EOF after cancellation; its socket and root directory were removed. This
proves client-side cancellation transport, not worker retirement under the
actual systemd broker or credential withdrawal on Quit.

A subsequent disposable activation used the actual root broker, preparer,
relay and isolated agent units. A tiny credential-free reviewer ELF reached a
fake owner socket through the run's private relay, then waited. Cancelling the
desktop broker call returned no receipt; before the exact retirement RPC, the
agent unit had `MainPID=0` and no control group. The system journal recorded
SIGKILL of both the agent main process and its child. Exact retirement then
removed the staged run. The temporary units, binaries and run state were
removed. This proves cancellation reaches an active isolated tree; provider
credential withdrawal and production Quit remain unqualified.

A disposable private GNOME Keyring and D-Bus fixture then exercised `RunOwner`
with the actual root broker, preparer, reviewer, relay and isolated agent units.
A tiny reviewer ELF sent the owner-generated placeholder through the private
relay. With a dummy Muse credential, the fixed model-catalog request received
Meta HTTP 401. After withdrawal of that fixture connection, an otherwise
identical second run received local 503 without credential reuse. Both worker
units ended with `MainPID=0` and empty control groups. Exact retirement was
idempotent; temporary root units, binaries, run state and fixture data were
removed. This qualifies the native owner-to-worker custody path for a dummy
token, not real account enrollment, a model response, Go admission or Quit.

The isolated relay now pins the bound desktop owner process with a pidfd before
readiness, and its systemd dependency closes the exact agent socket and service
on owner death. A disposable real-unit probe observed relay and agent-socket
shutdown after killing the fake owner. A second actual-unit probe launched a
sleeping child in the isolated reviewer service, then killed the fake owner.
The relay and agent service stopped, with `MainPID=0` and no control group;
the broker returned a signal-15 retired-process receipt and exact cleanup
succeeded. The native `RunOwner` still needs production lifecycle registration
and retained-run restart recovery before Quit/restart can report settled work.

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

`factory_provider::RunOwner::spawn` transfers one already staged native attempt
to a headless `RunExecution` handle. The lifecycle owner retains the handle;
requests may inspect it or wait with a timeout without cancelling the attempt.
The handle records the exact process-start event, joins the complete result on
`finish`, and cancels and joins on drop. `Executing` means no terminal result
has arrived; it does not assert fresh process liveness. `Retired` requires the
existing durable receipt and local/broker retirement path. Cancellation or an
unconfirmed result remains `Uncertain`, with root launch evidence preserved
even when the credential owner is dropped. Go still owns admission, ledger
settlement and recovery decisions. This handle is not yet composed into the
production supervisor or coordinator.

`RunOwner::observer` and `RunExecution::inspect` now provide an exact-process
query over the version-3 execution stream. The worker samples its captured
Linux pidfd for each sequenced request. Observation is single-flight and bounded
to five seconds; timing out leaves the attempt owned and running, and an old
reply cannot satisfy a later query. A validated complete terminal result reports
non-live thereafter; uncertain connection loss refuses. This supplies native
facts only: `Executing` stays cached phase, current Go admission remains a
consumer, and process exit does not imply owner/custody retirement.

`Credentials::while_current` guards the complete provider-owner lifetime without
passing a token to the callback. It shares revision validation, change signals,
deadlines and final checks with streaming requests. Initialization runs before
periodic checks; a callback continues to progress while a check is pending.
Credential loss or autonomous owner termination cancels the exact native attempt,
including an idle process which makes no provider/MCP call. Its lifecycle owner
still joins and quiesces, preserving uncertain launch evidence. Disposable real
services/keyring checks cover local withdrawal/replacement, independent-process
replacement and owner retirement. They do not qualify real Muse/Meta cancellation,
canonical custody/Go settlement or crash/restart recovery.

`WorkerMcp` also captures the defining Go tool-name catalog before launch.
Go's `mcpserver.BoundToolNames` exposes that catalog using the same role and
recorded task-controller selection as its SDK server, without activating a
capability. The native launch consumer still needs to join these ports.
The native model policy accepts its exact `mcp__factory` aliases alongside
filtered builtin `muse` tools, and validates complete SSE replies against the
qualified namespace/name plus stable item and execution call IDs. Workers
cannot replace that catalog through model schemas. This boundary adaptation
keeps Go's role/tool decisions in Go. The installed CLI's qualified Factory
call was exercised with a local scripted provider; actual Meta output and the
fully admitted native/Go execution path remain unqualified.
