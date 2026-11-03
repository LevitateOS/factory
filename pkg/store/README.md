# Appliance PostgreSQL store

`store` owns Soda's PostgreSQL schema and typed row operations. The dashboard
and factory coordinator consume this internal Go package to retain users,
development keys, Projects, membership, preparation decisions and factory
ledgers. Operators configure the database through appliance setup; end users
use product APIs and controls rather than direct SQL.

The preparation journal also retains native workspace evidence. Its generated
`appliance_project_id` reference enforces the Project foreign key only for
appliance preparations; a native preparation carries its captured workspace
owner instead of fabricating an appliance Project. Existing immutable-kind/
owner/retirement guards still apply. Opening retained databases upgrades the
reference under the existing schema lock without rewriting preparation records.
This reference establishes storage ownership, not runtime readiness or dispatch
authority; current capability/preparation/placement gates remain independent.

Application administration uses canonical `application.Record` references.
`CreateApplication` persists an operator-admitted administrator grant and an
unknown initial native state. `ReplaceApplication` serializes revision CAS and
retains immutable prior specs for recovery. `ObserveApplication` requires both
the exact current references and the expected prior observation; a nil result
records uncertainty. The store reserves distinct app hostnames, loopback ports
and private data identities. Canlang keeps its descriptor, application users,
credentials and binding configuration; none are copied into these records.

## Open the production store

The dashboard loads the DSN from its restricted `database_dsn_file` and uses:

```go
db, err := store.Open(dsn)
```

Handle the error before using `db` and call `db.Close()` when its owner shuts
down. Opening connects through the PostgreSQL driver, initializes an empty
database, or validates an existing current schema. It refuses an unversioned
nonempty database or an incompatible schema; it does not silently migrate those
states. The identity broker exclusively owns the separately provisioned
encryption key and encrypted credentials.

The database role needs access to the selected database and permission to create
the schema on first initialization. `OpenEncrypted` is reserved for isolated Go
encrypted credential fixtures; the production identity broker uses its separate
Rust store. `SchemaVersion()` reports the source's current schema format without
connecting.

## Consume typed records

| API area | Examples |
| --- | --- |
| Users and saved keys | `UpsertUser`, `User`, key inventory and confirmed removal. |
| Projects and members | `CreateProject`, `Project`, `ProjectByRepository`, `Join`. |
| Preparation and grants | Recorded input decisions, approvals and revision-checked authority. |
| Factory ledger | `FactoryRun`, `FactoryRuns`, `FactoryCommand`, assignments, reservations, usage and publication/merge records. |
| Collaboration registry | `PutCollaborationHost`, `ObserveCollaborationRepository`, `ObserveCollaborationActor`, `ObserveCollaborationTeam`, account/profile/runtime bindings; canonical records in `internal/collaboration`. |

The [collaboration registry](../collaboration/README.md) is added transactionally
without rewriting workflow history or changing its core schema version. Storage
opening does not initialize a provider namespace. New native composition explicitly
initializes an empty ledger before numeric mapping; retained Fountain ledgers use
`AdoptFountainLedger` with their configured source, preserving IDs/history and
reserving the new allocator ranges atomically. The running appliance's neutral
identity/admission cutover remains T02/T03 work.
Registry records and repository/connection associations do not grant authority.
Team identities occupy their own host-scoped namespace, with opaque external IDs
and mutable names. Opening an existing registry adds its team table and identity/
allocation guards under the existing schema-marker lock; workflow format and
retained identities/commands stay intact. An existing table with missing or
disabled guards refuses opening. Team observation requires ledger adoption;
host-scoped lookups never allocate or infer membership/permission.

For example, an integration reads a recorded run with
`run, err := db.FactoryRun(ctx, runID)`. Use `errors.Is(err, store.ErrNotFound)`
for an absent row. `ErrCommandConflict` signals a reused command identity with
different content; `ErrGrantKey` signals missing or incorrect encryption input.
`ErrLastKeyConfirmationRequired` preserves the explicit final-key removal gate.

## Persistence boundaries

`OwnContributions` reserves a dedicated PostgreSQL session and advisory lock for
a contribution producer. Its `ContributionOwner` claims and settles on that same
session, retaining a private owner ID beside the existing assessment. Always close
the owner before closing Store; closing discards its session rather than returning
a held session lock to the pool. Session loss fences late settlement.
`RecoverContributionReviews(ctx, after)` traverses at most 32 owned pending rows
per page, returning `Next`, `Complete` and a recovered count. Acquiring the released
producer's lock permits only an unavailable assessment, preserving its receipt
without invitation or replay. Live owners and historical claims without ownership
evidence remain unchanged. This requires no schema migration or second journal;
server startup/progress composition owns when recovery runs.

Typed methods enforce record validity, transactions and their stated revision
conditions. Store writes do not provision accounts, start containers, retire
native processes or prove Forgejo effects. The coordinator and host boundaries
perform those operations and persist their evidence here. Keep product SQL in
this package; consumers use its exported records and methods.

`CaptureHumanIssueCreate` saves plain human issue intent and its exact provider
preparation in `factory_commands` before dispatch. It requires an adopted ledger
and canonical repository/actor mappings from the captured host. Concurrent equal
requests return the winning original capture; callers dispatch that returned
capture, retaining its namespace, operation and preparation rather than refreshing
them on retry. `HumanIssueCreateCapture` recovers by the exact admitted request;
changed title/body/scope or a reused command of another kind conflicts. These
capture methods perform no provider I/O and record no outcome. Current human
admission and receipt interpretation belong to the adapter/shared application.
`ObserveHumanIssueCreate` locks the existing command row, checks exact capture
binding and advances validated observations through the shared atomic-command
outcome rules. Stale absence cannot erase terminal evidence; a changed receipt
or contradictory terminal primary refuses. Only a known primary with complete
bookkeeping finishes; committed completion needing intervention stays open.
`CaptureHumanLock` and `HumanLockCapture` retain the exact issue/PR target,
requested lock state and reason with the original opaque provider preparation.
They use the same adopted-ledger, same-host repository/actor binding and immutable
command journal. Identical requests converge on the winning capture; changed
state/reason/scope or another command kind conflicts. Capture performs no provider
I/O and records no outcome. `ObserveHumanLock` now saves validated adapter
observations under the same command row lock, checking complete capture binding
and reusing the shared monotonic outcome rules. Stale absence cannot erase
terminal facts; contradictory primary evidence or changed receipt refuses.
Only a proved completed primary/no-effect finishes the command. Live admission
and provider source authentication remain the adapter/application boundary.
Schema 74 transactionally extends the existing 2 MiB capture allowance to
`discussion.lock.apply`; comment/edit/issue-creation bounds, ordinary
8192-character command bounds, immutable command guards and retained records are
preserved. Known upgrades chain through 73-to-74 under the existing open
transaction. The development-copy
SQL emitter consumes the same owning DDL without connecting to a database.

`CaptureHumanState` and `HumanStateCapture` retain the admitted issue/PR target
and desired closed state with byte-exact opaque preparation, authority namespace
and operation ID. They require an adopted ledger and same-host repository/actor
mapping. Concurrent identical requests return the original winning capture;
changed intent/scope and command-kind collisions conflict. They perform no
provider I/O and record no outcome. State capture uses the existing ordinary
8192-byte command bound and needs no additional schema allowance. Native PostgreSQL checks cover
full 4096-byte preparation, the SDK-produced maximal request shape, retained prior
command kinds, clean restart and actual process-death/WAL recovery.
`ObserveHumanState` now saves validated adapter observations under the existing
command row lock, checking exact capture binding and shared monotonic outcome
rules. Stale unknown/absence cannot erase terminal facts; contradictory primary
evidence or replaced receipt refuses. A committed primary needing completion
intervention stays unfinished, including after Store reopen. Live authority and
provider evidence authentication remain with the Application and adapter.

`CaptureHumanReview` and `HumanReviewCapture` retain canonical PR draft/final
intent with exact prose and opaque provider preparation. They require the adopted
same-host ledger and reuse the existing immutable journal; identical concurrent
requests return the original winning capture, while changed event/prose/scope or
command kind conflicts. Capture performs no provider I/O or outcome inference.
Schema 75 transactionally adds `pull_request.review.submit` to the existing 2 MiB
capture allowance, with 64 KiB canonical prose and 512 KiB provider preparation.
Older command limits and guards stay intact. Native PostgreSQL checks qualify
schema-74 upgrade, exact large captures, retained previous commands, restart and
actual process-death/WAL recovery. The development-copy SQL consumes the same DDL
and qualifies real upgrade/replay plus refusal to run on the native database.

`ObserveHumanReview` now saves validated adapter observations under the existing
command row lock, checks exact capture equality and advances shared monotonic
outcomes. Stale unknown/absence/pending cannot erase terminal evidence; changed
receipt or contradictory terminal effect refuses. Only a proved completed primary
or no-effect finishes the command; native completion needing intervention stays
open across Store reopen. Current admission and provider authentication remain
the Application and adapter responsibility.

`CaptureHumanInline` and `HumanInlineCapture` retain draft/immediate PR inline
intent, the complete immutable base/head/path/side/scope/range anchor, exact prose
and opaque provider preparation. They require the adopted same-host ledger and
reuse the immutable command journal. Concurrent identical requests return the
original winning capture; changed intent or anchors and command-kind collisions
refuse. Capture performs no provider I/O and infers no outcome. Dispatch must use
the returned capture; current admission remains the caller's responsibility.
Schema 76 transactionally adds `pull_request.review.comment.create` to the
explicit 2 MiB capture allowance, with 64 KiB canonical prose and 512 KiB provider
preparation. Prior command limits and retained captures remain intact. Real native
PostgreSQL checks cover upgrade from 75, concurrent winners, exact replay,
restart and process-death/WAL recovery. The development-copy emitter uses the same
DDL; its generated SQL upgrades/replays only on `soda_dev` and refuses the native
database.

`CaptureHumanThread` and `HumanThreadCapture` retain human resolve/unresolve
intent, an opaque thread ID and the full original anchor or explicit unknown
context. The adopted same-host ledger and immutable command journal enforce
canonical identity binding. Concurrent identical requests return the original
winning preparation; changed thread/state/context/scope or command kinds refuse.
Capture performs no provider I/O and infers no outcome. Schema 77 transactionally
adds a 128 KiB allowance only for `pull_request.review.thread.resolve`, with
64 KiB opaque preparation; previous large/small command limits remain intact.
Native PostgreSQL checks qualify schema-76 upgrade, exact replay/concurrent
winners, restart and WAL recovery. The development-copy emitter uses the same
DDL and qualifies upgrade/replay on its isolated `soda_dev` database while
refusing the native database.

`ObserveHumanThread` now persists validated adapter observations under the
existing command row lock, checks the exact immutable capture and advances shared
monotonic outcomes. Stale unknown/absence/pending cannot erase terminal evidence;
changed receipt bytes, binding or contradictory terminal effects refuse. Only
proved complete primary/no-effect results finish a command. A committed native
primary awaiting bookkeeping retains its exact receipt across Store reopen and
finishes only after admitted completion read-back. No network runs inside SQL.

`CaptureHumanReviewRequest` and `HumanReviewRequestCapture` retain user/team
reviewer desired state, exact opaque preparation, authority namespace and operation
ID. The ledger must be adopted; operator, repository and selected user/team must
resolve on the same host. This proves identity binding, never membership or access.
Concurrent identical requests return the original winning capture. Changed scope,
reviewer kind/identity or desired state conflicts; no provider I/O or outcome is
inferred. Schema 78 extends the 128 KiB allowance to `pull_request.review.request`
while retaining all earlier command limits and immutable update guards. The
transactional schema-77 upgrade and development-copy emitter share the defining
DDL. Native PostgreSQL checks cover exact bytes, maximal SDK evidence and 64 KiB
preparation, concurrent winners, wide user/team IDs, retained commands, restart
and WAL recovery. The admitted Fountain adapter consumes this capture.

`CaptureHumanMerge` and `HumanMergeCapture` retain canonical merge/manual-mark
method, message/selected commit and exact opaque preparation in the same command
ledger. Adoption and same-host repository/operator bindings are required. A retry
returns the winning authority/operation/preparation without refreshing it;
changed human intent or command-kind reuse conflicts. No provider I/O or outcome
is inferred. Schema 79 extends the 2 MiB command allowance to `pull_request.merge`
for the 64 KiB message plus 1 MiB opaque preparation, preserving prior bounds and
immutable guards. The schema-78 upgrade and development-copy SQL share one DDL
owner. Actual PostgreSQL checks cover wide string IDs, exact bytes, concurrency,
rollback, retained commands, restart and owned-server WAL recovery.

`CaptureHumanPullCreate` and `HumanPullCreateCapture` retain exact human PR
creation intent and opaque provider preparation in the same command ledger.
Adoption and same-host base/source repository and actor bindings are required.
Repeated/concurrent requests recover the original winning authority, operation
and preparation; changed intent or command kind conflicts. Capture performs no
provider I/O and infers no outcome. Schema 80 extends the existing 2 MiB payload
allowance to `pull_request.create`, preserving prior bounds and immutable guards.
The schema-79 upgrade and development-copy emitter share one DDL owner. Actual
PostgreSQL checks cover wide IDs, exact writing, concurrency, rollback, retained
merge captures, restart, WAL recovery and development-copy replay/refusal.
`ObserveHumanPullCreate` now validates the exact capture under the command-row
lock and saves bounded monotonic results with the existing atomic human-command
ordering. Confirmed effects and receipt bytes survive stale/overlapping replies;
contradictory effects or changed bindings/receipts refuse. Only known effects with
complete native bookkeeping finish the command. Actual admitted SDK results,
including pending/intervention and early cancellation, survive PostgreSQL restart
and WAL recovery. The admitted Fountain adapter now authenticates provider
preparation/receipts before projection and persistence. Application now composes
the admitted writer with capture-before-dispatch and this outcome owner, recovering
original source/writing for scoped cleanup without another preparation. Shared
HTTP now consumes this Application path; installed UI follows. No network runs
in SQL.

`ObserveHumanMerge` validates the exact immutable capture and saves canonical
merge/manual outcomes under the existing command-row lock, using the shared
atomic human-command ordering. Stale absence/unknown/pending replies cannot erase
known effects or receipts; changed bindings, receipt bytes and contradictory
terminal effects refuse. Outcomes remain bounded to 64 KiB. Only known effects
with complete native bookkeeping finish the command; pending completion and
intervention remain open. Actual admitted native SDK results and receipts survive
PostgreSQL restart and owned-server WAL recovery. The admitted Fountain adapter
now authenticates provider evidence before projection. Application composes that
admitted writer with capture-before-dispatch and monotonic observation persistence.
Shared HTTP now consumes this Application path; installed UI remains subsequent
work. No network runs in SQL.

`ObserveHumanReviewRequest` now saves validated adapter observations under the
existing command-row lock, requiring the exact immutable capture. It retains
terminal evidence against stale absence/unknown/pending replies, rejects changed
receipt bytes/bindings and contradictory terminal effects, and bounds the saved
outcome to 64 KiB. A committed effect awaiting native completion or intervention
remains unfinished; only proved complete primary/no-effect results finish the
command. Real native user/team/no-op/remove outcomes, complete 1,000-record
receipts, overlapping replies and exact retained captures/outcomes survive native
PostgreSQL restart and WAL recovery. The store validates journal binding; admitted
provider evidence authentication and current human access remain adapter/Application
responsibilities. No network runs inside SQL. `Application.HumanReviewRequests`
now consumes capture/observation persistence, reconciling saved commands under live
admission and recovering original selection for scoped cleanup. Shared HTTP/Product
and Fountain composition now consume this service; installed UI follows.

`ObserveHumanInline` saves validated adapter observations under the existing
command row lock, requiring the complete capture and canonical intent binding.
It reuses the shared human-command ordering: stale unknown/absence/pending cannot
erase terminal facts, changed receipt or contradictory terminal effect refuses,
and completion needing intervention remains unfinished. Only proved complete
primary/no-effect outcomes finish a command. Provider authentication and current
admission remain with the adapter and Application.

`SavedIntentRevision` reads the existing immutable revision row through its
current request owner and exact intent/revision identity. It returns the captured
record independently of head position and bounded history without changing rows
or introducing another journal. Service/HTTP callers own current admission and
comparison with their retained save input; absence cannot exclude an in-flight
save.

`OpenEphemeral` creates a disposable PostgreSQL database for integration fixtures
and returns its cleanup function. It requires a suitable fixture superuser DSN;
it is separate from opening the appliance store.

See [service configuration](../../docs/reference/configuration.md),
[operator setup](../../docs/guides/operator-setup.md),
[database backup/restore](../../cmd/soda-pg-maintenance/README.md), and
[Go persistence ownership](../../docs/development/go.md#hard-invariants-persistence).

Issue-intake grants are bounded opaque provider JSON bound to an admitted source
scope. The store owns exact semantic issue-maker retries/slots, not provider
claim parsing or bearer attachment. Its production dependency closure is now
SDK-free. See the [intake boundary and evidence](../factory/issue_intake.md).

`RetryIssueMakerFeedPostCommand` atomically records the exact retry identity and
scoped receipt in `factory_commands` with the existing post/intake transition.
Its replay flag distinguishes a historical receipt from a newly committed retry;
replay never resets later attempts or replaces newer writing. Changed command
reuse conflicts, and refused transitions leave no new command row. This includes
the applied-draft retry that deliberately retains its post revision. Serving
callers must check current visibility and authorship before either first dispatch
or replay. The receipt covers only storage, not proof capture or external work.
The shared service/HTTP consumes this journal and serves read-only scoped receipt
lookup. `NotePostContextWait` binds capture-failure metadata to its exact current
revision and refuses when context already exists. Shared UI pending-command
recovery follows in the
[implementation queue](../../docs/development/factory-products/implementation-tasks.md).
