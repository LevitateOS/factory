# Factory records and validation

`factory` defines Soda's canonical factory records and their pure validation.
It is an internal Go package consumed by the dashboard, coordinator, store,
host client and Forgejo adapters. Operators encounter these records through
factory status, policy, issue acceptance and run controls.

Use the [factory operator interface](../../docs/reference/factory.md) for
existing CLI operations and the [product overview](../../docs/product/overview.md)
for the intended workflow. This package has no executable or persistent state.

## Consumer entrypoints

Import `github.com/levitateos/factory/pkg/factory` from Soda code. Important
record families include:

| Records | Purpose |
| --- | --- |
| `Run`, `RunAdmission`, `Outcome` | Execution identity, captured authority/profile, provenance and settlement. |
| `Command` | Queued owner actions such as Retry and finite funding. |
| `RepositoryPolicy`, `Capacity`, `OperatorGrant`, `Sponsorship` | Explicit operating authority and resource limits. |
| `Acceptance`, `IssueControl`, readiness records | Accepted native issue inputs and eligibility observations. |
| `Assignment`, result and allowance records | Bounded dispatch, attempts and reported work. |
| `Publication`, review, check and merge records | Persisted native-operation intent and outcome evidence. |

Construct records using admitted inputs and call their `Validate` methods before
crossing persistence or execution boundaries. For example, validating a Retry
command does not queue work:

```go
target, payload := "7/9", `{"reason":"retry"}`
cmd := factory.Command{ID: factory.NewID(), Type: factory.CommandRetry,
    Target: target, Principal: principal, Payload: payload,
    Digest: factory.SettingsDigest(factory.CommandRetry, target, payload)}
err := cmd.Validate()
```

Here `principal` comes from the admitting service.
`NewID` creates a random 32-character lowercase hexadecimal identity. Reuse the
recorded command ID for an identical replay; changed content requires a new ID.
Standing settings use their own expected revisions. Status, Stop and Reconcile
use run state directly and carry no durable command.

`FeedPostRetryCommand` binds one admitted author's retry to its post, canonical
repository, prior revision and exact body digest. `FeedPostRetryReceipt` records
the atomic post/intake transition; an unchanged applied draft can retain its
revision. The shared command ledger stores that result so recovery does not infer
an effect from the current head. Neither record grants access or claims proof
capture, planning or external publication. The shared service/HTTP consumes this
journal with current visibility/authorship for dispatch, replay and read-only
receipt recovery; only newly committed retries perform best-effort capture.

`HumanIssueCreateCommand` records an admitted human's exact plain title/body and
canonical host/repository/actor. Its target is the issue collection; the provider
allocates the issue ID/index. `HumanIssueCreateCapture` binds credential-free
provider preparation, installation/account authority and operation ID into the
existing command digest before dispatch. Titles are nonblank and unpadded, up
to 255 bytes; bodies retain exact UTF-8/CRLF up to 64 KiB, including empty text.
Decoding verifies the original journal binding. Neither a capture nor its digest
grants permission or proves that the provider created an issue.
Typed creation observation validation binds admitted adapter evidence to that
capture. It uses the shared atomic-command outcome rules, retaining exact
terminal receipts and tombstones. Unknown/pending effects and completion needing
intervention remain unfinished; adapter source authentication remains separate.

`HumanLockCommand` records the admitted issue/PR target, desired comment-lock
state and exact reason. Lock permits blank text and bounded UTF-8 up to 64 KiB;
unlock requires an empty reason. Native configured-reason rules remain the
provider's dispatch responsibility. `HumanLockCapture` pins the credential-free
provider preparation, namespace and operation ID before dispatch. Its exact
command binding and request equality preserve the original preparation on retry;
they grant no authority and establish no native outcome.
Typed lock observation validation now binds admitted adapter evidence to that
capture and reuses the shared atomic-command ordering rules. Exact terminal
receipts/tombstones survive stale observations; unresolved effects remain open.
Provider source authentication remains the adapter’s responsibility.

`HumanStateCommand` records direct issue/PR close/reopen independently of comment
posting or PR ref synchronization. `HumanStateCapture` pins exact opaque provider
JSON, namespace and operation ID before dispatch, using the ordinary 8192-byte
command bound and at most 4096 provider-preparation bytes. Request equality and
complete envelope verification preserve the winning preparation on retry. Capture
grants no human authority and establishes no provider outcome.
Typed state observation validation binds admitted adapter evidence to that
capture and reuses the shared atomic-command outcome rules. Terminal receipts
and tombstones remain immutable; committed completion needing intervention stays
open. Provider evidence authentication remains the adapter's responsibility.

`HumanReviewCommand` records canonical PR draft/comment/approve/request-changes
intent and exact UTF-8 prose independently of provider wire events. Prose is
bounded to 64 KiB; request-changes requires nonblank text. Current provider policy
still decides whether empty draft/comment prose can consume an existing draft.
`HumanReviewCapture` pins the namespace, operation ID and byte-exact opaque JSON
before dispatch, with 512 KiB preparation and a 2 MiB complete command limit.
Request equality and complete envelope verification preserve original preparation
on retry. Capture grants no authority and establishes no provider outcome.
Typed review observation validation binds admitted adapter evidence to that
capture and reuses shared atomic-command ordering. Terminal receipts and
cancelled-before-submit tombstones remain immutable; unknown effects and native
completion needing intervention remain unfinished. Provider authentication stays
with the admitted adapter.

`HumanThreadCommand` records resolve/unresolve intent for a provider-observed
opaque thread ID within a canonical host/repository/PR. It retains the complete
original anchor or explicit unknown context. `SameRequest` compares anchor values,
so decoded or independently allocated anchors preserve request identity.
`HumanThreadCapture` pins exact credential-free provider JSON, authority namespace
and operation ID before dispatch. Preparation is bounded to 64 KiB, with a
128 KiB complete command allowance covering base64 bytes and escaped context.
Replay retains the original deadline/revision/proof; capture grants no authority
and establishes no provider outcome. Typed thread observation validation now
binds admitted adapter evidence to the saved capture and reuses shared atomic
command ordering. Exact receipts and early cancellation remain immutable;
unresolved effects/completion stay unfinished. Provider authentication remains
with the admitted adapter; Application composition owns persistence.

`HumanReviewRequestCommand` records one canonical user or team and its desired
requested state on a PR. Exactly one positive `Reviewer` or `Team` selects the
target; their separate types/namespaces remain distinct even for equal numeric
values. `HumanReviewRequestCapture` preserves byte-exact credential-free provider
JSON, authority namespace and operation ID before dispatch. Preparation is bounded
to 64 KiB, within a 128 KiB complete command allowance. Request equality and full
envelope verification retain the winning preparation on retry. Capture grants no
authority and establishes no provider outcome. The neutral
`service.HumanReviewRequestWriter` now defines prepare/submit/lookup/cancel and
typed query/admission/conflict/namespace/unavailable/bounds errors. Its Fountain
adapter owns native mapping and receipt authentication. Typed reviewer observation
validation binds that admitted evidence to the exact capture and shares the atomic
human-command outcome ordering. Stale replies cannot erase terminal facts, changed
receipt bytes or contradictory terminal effects refuse, and pending/intervention
completion remains unfinished. Store persists these outcomes.
`Application.HumanReviewRequests` now commits preparation before dispatch, reconciles
saved captures under live admission and recovers original selection/state for scoped
lookup/cancel. Known effects are not resubmitted, and uncertainty never refreshes
preparation. Shared HTTP/Product now exposes strict canonical reviewer commands
through admitted Fountain composition; private preparation and receipt evidence
stay server-side. Installed UI consumers remain subsequent work.

`HumanMergeCommand` now records canonical human PR merge intent: host/repository/
actor/number, ordinary method and exact message, or manual marking with a full
selected commit OID. Ordinary commands cannot select a computed result; manual
commands cannot add a new message. `HumanMergeCapture` binds that request to an
opaque, byte-exact provider preparation and authority/operation namespace before
dispatch. Messages are bounded to 64 KiB, preparation to 1 MiB and the escaped/
base64 command envelope to 2 MiB. The existing PostgreSQL journal retains the
winning preparation on concurrent/repeated capture. Capture alone proves intent.
Typed merge observation validation binds admitted results to that exact capture
and shares the atomic human-command outcome ordering. Terminal facts and exact
receipts survive stale replies; conflicting evidence refuses. Pending native
completion and intervention keep the command open. Store persists these outcomes.
`service.HumanMergeWriter` defines admitted prepare/submit/lookup/cancel; its
Fountain adapter authenticates ordinary/manual native intent and receipt evidence.
`Application.HumanMerges` now composes that writer with the single Store, committing
winning preparation before dispatch and reconciling the original capture under
current admission. Known effects never resubmit. Scoped cleanup loads the original
method/message or selected commit from the journal. Shared HTTP/Product now exposes
strict ordinary/manual submit and scoped lookup/cancel through admitted Fountain
composition. Private evidence stays server-side; installed shared UI follows.

`HumanPullCreateCommand` records canonical human PR intent with explicit base and
source repositories, selected branches, exact title/body and maintainer-edit
choice. `HumanPullCreateCapture` binds that intent to opaque, byte-exact provider
preparation and its authority/operation namespace before dispatch. The base,
source and actor must belong to one adopted host. Concurrent capture returns the
original winning preparation; it never predicts a PR number or proves creation.
Title/body/preparation/envelope limits are 255 bytes, 64 KiB, 1 MiB and 2 MiB.
Typed observation validation binds results to that exact capture and scoped
receipt, using the shared atomic human-command ordering. Store persists outcomes
without allowing stale replies to erase confirmed effects or change receipt bytes.
Pending completion and intervention remain unfinished. The service writer port
now has an admitted Fountain adapter that authenticates original base/source
preparation and receipt evidence. `Application.HumanPullCreates` now composes
that writer with capture-before-dispatch, current admitted reconciliation and
monotonic outcome persistence. Scoped lookup/cancel recover source, branches,
writing and delegation from the journal; clients cannot replace them. Known
effects never resubmit, and unfinished completion stays open. Shared HTTP/Product
now exposes strict creation and scoped lookup/cancel through admitted Fountain
composition. Provider evidence stays server-side; installed UI follows.

## Constraints

Validation checks identifiers, stage transitions, evidence bindings and bounded
payloads. A run includes a pinned image, input commit, deadline, harness/model
provenance and broker lease/binding facts. Validating those values does not grant
authority or prove that a native process exists.

Project and broker identities remain owned by `project` and `identity`; consumers
reference those types directly. Runtime decisions belong to
[factory/control](control/README.md), and SQL belongs to
[store](../store/README.md).

See [factory interfaces](../../docs/architecture/factory-interfaces.md) and the
[trust model](../../docs/architecture/trust.md) for authority boundaries.
