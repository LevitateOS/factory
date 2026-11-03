# Operation and graph contracts

`effects.go`, `effect_outcomes.go`, `graph.go` and `graph_receipt.go` define the
server-side effect handoff. They import no provider SDK, host implementation,
HTTP session or credential package. They create no journal and expose no provider
receipts to the Lit client. Existing Factory journals retain persistence ownership.

## Intent and conditional writes

`OperationIntent` captures the host, adapter operation namespace, operation ID,
local repository/actor, semantic kind, required atomicity, conditions and bounded
semantic JSON. Persist the exact intent before dispatch. Credentials and connection
selection stay outside it. Reconnection cannot rewrite its actor or scope.

`Authority` retains the operation namespace alongside HostID: Fountain's admitted
installation identity, or the local effect journal for a provider without remote
operation receipts. It remains on lookups and pre-submit cancellation tombstones.
A replacement installation cannot answer an older operation merely because its
configured host locator is the same. A local namespace implies no remote guarantee.

Conditions retain the admitted authorization revision, optional repository
revision, exact resource revision/existence/head/base requirements and deadline.
They are required guarantees. Refuse a guard the adapter cannot enforce: a read
followed by an unguarded write does not implement compare-and-swap. Authorization
and deadline require fresh dispatch checks. These records grant no execution,
spending or automation. Common validation checks shape, not permission or freshness;
kind-specific validation/admission remains mandatory. Graph payloads additionally
require `GraphIntent.Validate`.

`atomic` requires all changes to commit together. `ordered` permits individual
effects and partial results. An ordered result cannot satisfy an atomic intent.
Fountain's existing atomic graph must retain its guarantee. GitHub's separate
writes cannot impersonate it.

## Outcomes and recovery

Effect, cancellation and completion remain separate. A committed effect may still
await application adoption. Cancellation does not reverse a committed write.
An unbound pre-submit tombstone has no invented actor/repository/kind/digest and
cannot pass submitted-intent binding validation.

| Effect | Meaning |
| --- | --- |
| `pending` | No settled outcome yet; processing may still be in progress. |
| `not_committed` | An attributable outcome establishes that this effect did not commit. |
| `committed` | An attributable outcome establishes that this effect committed. |
| `indeterminate` | The effect may have committed; its outcome is unresolved. |
| `partial` | Some ordered steps committed; another step has a different outcome. |

Assign stable checkpoint IDs before sending and save each result before advancing.
A lost response stays indeterminate. Aggregate validation checks all steps:
committed plus pending/refused/uncertain steps is partial. Pending, partial and
indeterminate effects cannot report completed work. Atomic results cannot be partial.

Lookup distinguishes found, not observed, unavailable and unknown, binding host,
authority and operation ID. Absence does not prove an earlier request cannot arrive
or that a non-idempotent write is safe to resubmit. Unavailability must not become
an invented committed record. No helper here authorizes retries.

`ProviderReceipt` retains the provider namespace, reference, translated-envelope
digest and credential-free receipt JSON. Search, markers and ordinary readback are
observations, not durable receipts of an attributable write. Do not fabricate one
for GitHub. A local outcome can have no remote receipt; its owning adapter/journal
must establish the classification with actual evidence. Validation checks
consistency, not authenticity or journal transitions.

Keep three digest roles separate:

- Existing `factory.IntentGeneration.IntentDigest` binds the accepted product
  revision through `IntentRevision.Digest`; its store guard is unchanged.
- `OperationBinding.IntentDigest` binds captured application operation inputs.
  The owner supplies the expected digest to `ValidateAgainst`.
- `ProviderReceipt.IntentDigest` retains the provider's translated-envelope digest.

The journal still checks immutable bindings and its existing transaction/CAS
guards before saving an observation. These definitions implement no dispatcher,
checkpoint transition policy or replay controller.

## Graphs and receipts

Creates use request-local keys. Existing issues use neutral `IssueRef` records
with opaque external IDs, repository, issue discriminator and display number.
Edits retain exact expected title/body/content revision. Fountain's content version
zero becomes explicit opaque revision `"0"`.

Dependencies retain direction and exact add/remove preconditions. A ref selects
one existing issue or one new key. Validation rejects ambiguous/cross-repository/
inconsistent refs, duplicate edits/edges, self edges and removals involving new
keys. Bounds retain the current Factory's 64 changes, 255-byte titles, 64-KiB
bodies and 48-KiB payload. This is not a cycle check against the remote graph.

`GraphReceipt.ValidateAgainst` binds repository/actor, every created key and edited
issue, explicit content revisions and every changed edge to the saved graph. It
rejects missing/duplicate results, wrong identities and new IDs that alias existing
refs. Untouched/delivered history has no receipt entry. The engine still checks
current accepted scope, delivered-binding provenance and fresh issue observations
before adoption.

## Evidence and handoff

T01-EFFECTS passed Go compilation and the source guard. An external diagnostic
used the actual checked-out Fountain SDK validator and JSON types, retaining exact
edit text and zero content version and translating a graph receipt into neutral
records. It exercised receipt binding, ambiguous refs, atomic/ordered separation,
partial/uncertain effects, cancellation tombstones, lookup scope and string IDs
above `2^53`.

The same diagnostic used existing `Store.SaveIntentRevision`,
`BeginIntentGeneration`, `RecordIntentGeneration` and `IntentGeneration` APIs on
private native PostgreSQL. It retained a neutral envelope/checkpoints in their
JSON fields, kept the legacy generation pending for partial effects, rejected a
changed immutable actor and reopened the store. Clean native restart, checkpoint
update, forced-crash/WAL recovery and final shutdown passed. The first attempt
correctly hit the accepted-product-digest guard; the diagnostic was fixed to supply
that digest separately. No production guard was weakened.

Logs: `/home/vince/factory-effect-contract-go.log`,
`/home/vince/factory-effect-contract-source-guard.log` and
`/home/vince/factory-effect-native-journal-final.log`. Diagnostic:
`/home/vince/factory-effect-probe/`. Retained disposable profile:
`/home/vince/fpg-effects-final-20261031`. No unit tests were added.

This proves contract/SDK serialization and existing-store retention, not provider
writes, engine replay or connected journeys. The running coordinator still uses
its current SDK contracts. T02 owns actual Fountain translation and engine/store
cutover, native/local ID mapping and preservation of admission/adoption/state
guards. T10/T11 own real mutations, per-step persistence/reconciliation and supported
retry semantics. The [T01-RUNTIME producer](../factory/access/README.md) now
defines admission, native/appliance provenance, placement and policy contracts;
their production consumers still require the named later slices.
