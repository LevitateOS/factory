# Factory runtime boundaries

`placement.go` owns retained runtime selection. Rust owns native process/storage
lifetime; this package does not launch an OS process or grant execution authority.

`LoadNative` consumes the private inputs produced by
`NativePostgres.backend_config` in [desktop-runtime](../../../lib/desktop-runtime/README.md).
Pass the absolute `backend.json` path in the owned profile. Its bounded, single
JSON object contains only `profile_id`, matched to retained `profile.id`.
The loader derives `backend.sock` in that same profile and reads `backend.dsn`
and `backend.key`; it accepts no configured appliance URLs, operator IDs, broker
paths or arbitrary external secret paths.

On Linux, the profile must be canonical, owned and private. Inputs must be owned
private regular files, opened without following symlinks and with bounded reads.
The database input must select the profile's authenticated PostgreSQL Unix socket,
factory user, postgres database and port 5432, without TLS or fallback targets.
The IPC input is 32 random bytes encoded as 64 hex characters. Errors omit input
contents; database/key values are not exported JSON fields and normal string
formatting is redacted. `IPCKey` returns a private copy. These secrets never
belong in a webview or logs.

Loading performs no database connection, listener creation, account/provider
enrollment, namespace adoption, coordinator ownership or work dispatch. Process
composition must explicitly consume these inputs and supply current account and
resource admission to shared services. Non-Linux native file admission currently
refuses; macOS qualification remains deferred.

`RunNative(ctx, path, connector)` takes a trusted `NativeConnector` factory. After
opening/recovering the owned Store, it supplies the validated bootstrap profile
and Store to that factory once. Connector construction performs no provider I/O
or account activation. The production executable composes GitHub canonical
observation ports here; a caller cannot claim another profile in read JSON.

`RunNativeWithBinding` uses the same defining runner with the existing trusted
per-request product binding port. It creates no actor from readiness or IPC;
account authority must be established independently on every bound request.
The normal executable supplies `BindNativeProduct`. It requires an admitted
native operation and fresh Rust account verification, then transactionally maps
the account and optional selected repository in the existing Go Store. Ordinary
readiness-only calls still refuse. Native control acceptance can supply a
declared blocking binding to exercise cancellation/drain.

The runtime owns the Go network/store lifetime from those inputs. It
claims only `backend.lock` for one network process, opens the canonical store
under a bounded cancellable startup context, and serves authenticated readiness
on `backend.sock` (`0600`) with no TCP listener. Before serving, shared
`Application.Recover` traverses all pages of owned pending assessments. Only proved
released producers become unavailable, preserving their receipts without replay
or invitation; live and unowned historical claims remain untouched. Store opening
and recovery share the ten-second startup budget. A signal cancels actual schema
or recovery SQL waits, and malformed/unknown recovery errors prevent readiness
without exposing retained contents. It does not claim a coordinator ledger,
reconcile unobserved runs or start work. `store.OpenStandaloneContext` uses the
same schema owner as `Open`; cancellation also ends actual SQL admission waits.
Schema 93 records whether application hosting belongs to the database. New
standalone profiles omit that component; retained shared databases preserve its
tables, rows and required verification. An appliance opener refuses a standalone
database. The existing appliance Project/identity compatibility bridges remain;
they do not require native users to configure application hosting or SSH keys.

Private IPC requires a current-user kernel peer and the exact per-start Bearer
capability. Browser Origin/Fetch headers refuse. `GET /healthz` checks the live
store and returns only readiness plus profile and process-instance IDs, with
no-store; it makes no execution-readiness claim. The Rust bridge retains the
capability and owns account/resource admission. The native application mounts
shared product routes behind this private channel, independently of readiness.
Those product routes also require exactly one matching captured native-instance
header. Missing, stale or duplicate instances refuse before product binding.
Without a current connector binding, those routes return unavailable; IPC
capability never becomes a provider login or an empty successful feed/account.

An optional, strictly bounded `X-Soda-Native-Operation` header establishes only
the private callback context. Malformed or duplicate operation headers refuse;
missing headers confer no callback port. `NativeRequestPort(ctx)` is available
inside trusted product binding after peer/key/instance admission. Its Current,
Identity and Repository methods call the Rust semantic owner at the fixed
profile-local `host.sock`. The Go client independently checks its owned private
socket inode and same-user peer, uses no proxy/redirect/retry, and bounds request,
response, header and operation time. Rust independently checks the live operation,
captured credential revision and selected remote repository. No provider token,
SDK request, arbitrary URL, actor claim or SQL transition crosses this port.
Returned account/resource facts still require Go's canonical mapping and service
admission. Current is custody freshness only; it cannot replace Identity.
The reply includes the operation's optional admitted remote repository. It is
independent of product request selectors and remains fixed throughout the port
lifetime. Repository facts include nullable provider `push` permission; missing
permission is unknown, not an inferred write grant.

The separate fixed `/api/native/mcp-sign` and `/api/native/mcp-verify`
host-socket routes accept a capability ID and its immutable scope digest.
Rust keeps the persistent native MCP root key in the private profile. Signing
returns one derived secret for eventual delivery; verification returns only a
boolean for a presented worker secret. Both use the same peer, channel-key and
instance checks. The Go client validates capability shape and bounded replies;
the Go ledger must still establish current run admission before delivering or
accepting a worker secret. The native coordinator and MCP worker listener are
not active yet. Forge retains its separate appliance key path.

`BindNativeProduct` composes the shared product router with the same Store and
coordinator. It supplies feed/subscription, owner-private request, retained
assignment/priority and library reads. Resource callbacks resolve the exact
canonical repository on the bound host before asking Rust for fresh provider
facts; each check also applies current display metadata transactionally. A
registered association cannot expand the operation's selected scope. Go admits
bound request saves only on confirmed push permission. Confirmed denial returns
403, missing permission returns 503 and a foreign/unselected resource returns
404. Unbound drafts retain canonical owner privacy and need no repository grant.

The existing final current-account gates run before disclosure, including after
writes. A late refusal can conceal a reply to an already committed save; the
immutable record remains available to its owner on reconnect. No command is
automatically replayed. Intake/capture, planning, environment setup, execution,
artifacts retain explicit unavailable states until their dependencies are supplied.
The coordinator is never started by this binding. T06-CONNECT supplies the actual
desktop selection and shared UI.

The native_product_* read sources compose the defining shared issue, PR,
discussion, review, thread, file, check, repository and capability services.
Rust owns authenticated requests and provider decoding; Go validates the bounded
nonsecret envelope and maps participants and referenced repositories in one Store
transaction before neutral projection. Mentioned fork repositories gain canonical
identities without a connection/access association. The repository catalog lists
only the operation's selected resource; Rust's account catalog supplies selection.
Opaque cursors bind scope, filters, page size and observed diff/check commits.
Filtered empty issue pages retain their continuation. Safe provider status and
rate facts survive both HTTP and the native JSON transport.

REST review reply groups use actual root comment IDs, with unknown resolution,
review staleness and unproved diff anchors. Check runs and statuses have distinct
identities; they do not establish requiredness or branch rules.
The separate native job reader consumes typed Rust workflow/run-attempt facts,
checks the canonical PR/head/base around I/O and preserves run/attempt/job
identity in the common records. Opaque cursors bind scope, head, page sizes and
observed run count. Empty job pages can keep a continuation. Approval,
workflow-definition commit and job requiredness remain unknown, and a successful
read never becomes a passing merge gate. The branch-protection projection and
remaining continuation/partial-rule qualification stay in T06-GITHUB-EVIDENCE.
CI gates remain unknown pending the remaining T06-GITHUB-EVIDENCE work and
admitted product policy.
Human mutations remain unavailable until their admitted ports exist.

Native executable composition supplies a credential-free observation handler at
`/api/native/collaboration/observe`. The peer/key/instance and coordinator gates
admit it. Rust owns complete authenticated GitHub operations; this Go handler
accepts bounded account/repository/issue facts, maps each registry batch in one
transaction and projects neutral records through the shared issue service.
Legacy provider credential headers refuse; the old GitHub read/device/refresh
routes are absent. No provider SDK enters the engine/store closure. Registered
identity facts neither install a product resolver nor start work.

`NewNativeApplication` constructs the same `service.Application` used by the
appliance once. A trusted `NativeProductBinding` resolves current account and
service ports on each request, receiving that same application/coordinator.
The coordinator's existing HTTP admission gate encloses both binding and product
handling. `StopAdmission` refuses before account lookup, and `Close` joins admitted
bindings/handlers even if HTTP has already disconnected them. This lifetime gate
does not require or start a background runtime, confer account permission or
acquire a coordinator ledger lock.
The returned product must use the owned store. Missing/error/mismatched bindings
refuse without exposing account/provider error details. The binding must use
current credential/connection truth and give resource callbacks the same bound
account/generation; registered rows alone are not login evidence. No binding is
decoded from browser JSON. The headless product binding is now supplied by the
executable. The desktop now consumes it through prepared native selections and
the shared Lit context; installed qualification remains a later gate.

Duplicate processes refuse before store startup. Stale socket removal requires
exclusive profile use, an owned socket inode and confirmed connection refusal;
live listeners, other resources and uncertain outcomes refuse. Shutdown removes
only the inode it bound and preserves a replacement path. Parent cancellation or
listener failure stops product admission, drains/closes HTTP, then joins admitted
application work before closing the store and releasing network/profile ownership.
Forced HTTP closure cannot release Store while a handler is still settling; its
existing operation bounds remain in force.
Neither application construction nor serving reads starts its coordinator or
drives pending posts. Explicit startup recovery does not infer native execution
facts or settle unobserved runs; those records remain unchanged until an admitted
runtime supplies observation/retirement/custody ports. Rust still owns PostgreSQL and
backend-child supervision, including preparing a
new bootstrap capability before each managed start.
