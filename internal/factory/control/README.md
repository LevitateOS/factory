# Factory coordinator

`control` coordinates Soda's recorded factory work inside the standalone Factory service.
It combines durable records, fresh native observations, host execution and broker
settlement. Backend integrators consume this internal Go package; operators use
Soda's admitted factory controls and the `soda-factory` CLI.

## Construct and start

`web.NewFactory` is the construction owner; `web/serve` owns its process lifecycle. Its core construction
is:

```go
coordinator := control.NewCoordinator(db, hostClient, brokerClient)
```

The dependencies are a `*store.Store`, `HostFactory` and `BrokerExecution`.
The host owns privileged Project execution; the broker owns credential custody.
The coordinator receives execution metadata rather than credential bytes.

Run launch results, inspection/retirement, custody and candidate exports use
`factory/execution` ports. Factory composition supplies the appliance adapters;
this package no longer imports a concrete host or Fountain SDK. Process exit,
retirement, sealing and credential return remain separate. The broker binding's
retained `project` field is a container identity, checked against host facts;
broker reads alone do not attest a workspace. Retained run/tool/admission variants
and the remaining command/profile boundaries are unfinished T02-RUNS work.

Call `Start(ctx, lockPath)` before serving. It takes an exclusive file lock,
reconciles recorded runs and dispatch recovery, and advances configured
publication/check/merge recovery. All backends serving the same ledger must use
the same protected, writable lock path. A second coordinator refuses ownership.
On shutdown, `StopAdmission` rejects new work and `Close` waits for admitted HTTP
handlers before releasing ownership.

## Integration surfaces

| Surface | Use |
| --- | --- |
| `Status`, `Stop`, `Reconcile` | Read recorded runs and retire/settle admitted executions. |
| `OperatorHandler`, `WithOperatorPrincipal` | Private `POST /operator/factory` with the listener's authenticated OS principal. |
| Acceptance, policy and grant methods | Record authorized issue decisions and revision-checked settings. |
| `ObserveIssueEvent`, `Dispatch` | Assess native issue evidence and launch bounded recorded assignments. |
| `PublishPass`, `CheckPass`, `MergePass` | Advance persisted publication, review/check and conditional merge work. |
| Repository pause/resume, run retry/takeover and Project stop methods | Apply explicit lifecycle decisions while preserving unresolved effects. |

The web facade wires observation and operation adapters through `AcceptanceReads`,
`Readiness`, `DispatchReads`, `Publication`, `Reviews`, `Checks` and `Merges`.
Missing dependencies yield unavailable/waiting outcomes rather than guessed
evidence. These methods can change database records and invoke host, broker or
native Forgejo operations; they are not status-only helpers.

Generation creation, chosen-plan adoption and existing-operation resume require
the selected accepted revision alongside its intent and owner. Both the
recorded-plan connector and graph entry recheck that exact revision before
planning/native effects; a head change during service authority checking cannot
select a replacement acceptance. Completion, repair and background recovery pass
their captured revision too. Superseded commands refuse while preserving their
existing operation/receipt. Current authority and the original plan/graph
currency checks still apply to exact replay.

`Graph` additionally supplies the `factory.GraphCodec` boundary. The
coordinator plans with `collaboration.GraphIntent`; the Fountain adapter owns
native encoding and receipt decoding. Receipt projection accepts the operation
context for lookup-only canonical identity resolution; it grants no provider
authority. Existing journal packets retain their
native shape and approval digest, including after JSONB normalization. Decoded
facts do not grant acceptance: current plan/owner/revision/deadline and exact
issue/edge adoption checks still apply. `Creations` supplies retained original
creation facts with separate history-coverage and provenance completeness.

The completion source accepts canonical repository scope while its Fountain
page/body/ref reads resolve native scope. Local captures and sealed material keep
the canonical key. Repository echoes and exact enumerated issue identity are
checked alongside revision/content facts; mapping lookups do not count as native
RPCs or change the existing per-pass read budget.

Publication work and returned operation metadata use canonical actor/repository
keys. The Fountain publisher resolves native packet, remote and correction-author
scope and retains raw receipts; receipt adoption receives the controller context
for identity lookups. Original operation identity, uncertain effects and
withdrawal/reconciliation semantics remain owned by the existing journals.

Review and resolution work also keep canonical repository, reviewer and PR-author
scope. Native adapters resolve those fields for credential checks, observation
and packets, then return canonical outcome metadata. Review receipt adoption uses
the controller context, preserves original native review/comment IDs and inline
key mappings, and returns a canonical reviewer. Resolution confirmation compares
the native resolver after checking the recorded canonical outcome scope.

Checks and merge use the same canonical repository/actor boundary. The adapter
preserves native check reduction and independent-approval facts, then returns
canonical approval and merger identities. Merge receipt adoption uses the
controller context. Committed effects remain distinct from confirmed bookkeeping:
completion still requires the exact base/ancestry and original issue closure.

`RequestDeliveryRepair` takes an admitted canonical actor independently of the
caller-owned command principal. Request/revision/limit ownership uses that actor;
the principal remains the original audit/idempotency attribution. The controller
does not infer a canonical owner by parsing native principal numbers. The
authenticated caller must supply the actor after its own permission/session
admission; the HTTP adapter derives it from the current session, never the body.
Existing settled-report, native receipt, funding and replay gates remain.

## Operator behavior

`Status` returns bounded recorded metadata with credential-adjacent paths
redacted; it does not query the host or broker. The private operator endpoint
accepts only status, stop and reconcile. Its listener supplies the principal;
request bodies cannot supply human identity or policy authority.

Stop and Reconcile use current run state directly. Queued owner actions and
settings commands retain their command identities and recorded results.
Unknown termination, credential return or native effects remain fenced. A failed
observation does not authorize a new execution or erase earlier effects.

Follow [the operator contract](../../../docs/reference/factory.md),
[factory interfaces](../../../docs/architecture/factory-interfaces.md), and
[trust boundaries](../../../docs/architecture/trust.md). Actual dependency wiring
is in [the dashboard facade](../../web/server.go).
