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

`RunNative(ctx, path, connector)` owns the Go network/store lifetime from those inputs. It
claims only `backend.lock` for one network process, opens the canonical store
under a bounded cancellable startup context, and serves authenticated readiness
on `backend.sock` (`0600`) with no TCP listener. Before serving, shared
`Application.Recover` traverses all pages of owned pending assessments. Only proved
released producers become unavailable, preserving their receipts without replay
or invitation; live and unowned historical claims remain untouched. Store opening
and recovery share the ten-second startup budget. A signal cancels actual schema
or recovery SQL waits, and malformed/unknown recovery errors prevent readiness
without exposing retained contents. It does not claim a coordinator ledger,
reconcile unobserved runs or start work. `store.OpenContext` uses the same
schema owner as `Open`; cancellation also ends actual SQL admission waits.

Private IPC requires a current-user kernel peer and the exact per-start Bearer
capability. Browser Origin/Fetch headers refuse. `GET /healthz` checks the live
store and returns only readiness plus profile and process-instance IDs, with
no-store; it makes no execution-readiness claim. The Rust bridge retains the
capability and owns account/resource admission. The native application mounts
shared product routes behind this private channel, independently of readiness.
Without a current connector binding, those routes return unavailable; IPC
capability never becomes a provider login or an empty successful feed/account.

Native executable composition can supply a provider-owned handler for the fixed
`/api/native/github/read` RPC. The existing peer/key gate additionally checks its
captured process-instance header. The same coordinator admission/drain gate owns
that request lifetime. Provider HTTP and token handling stay outside this runtime
package; no provider SDK enters the engine/store closure. This read RPC does not
install a product account resolver or turn process readiness into account access.

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
decoded from browser JSON. Connector/account storage and actual login remain
their later consumers; the executable currently has no configured resolver.

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
