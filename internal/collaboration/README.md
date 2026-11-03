# Collaboration identities

This package defines the provider-neutral identity records used by the Factory
application, provider adapters and their store. It does not import a provider SDK,
HTTP session or native credential implementation.

| Record | Identity and ownership |
| --- | --- |
| `Host` | A configured provider instance has a stable random `HostID`. Its provider kind is immutable. The normalized API locator can change without changing that identity. |
| `RepositoryRef` | Local positive numeric ID mapped uniquely to `(HostID, opaque ExternalID)`. Names/owner paths are mutable display metadata. |
| `ActorRef` | Separate local positive numeric ID mapped uniquely to `(HostID, opaque ExternalID)`. Logins are mutable metadata. |
| `TeamRef` | Separate local positive `TeamID` mapped uniquely to `(HostID, opaque ExternalID)`. Names are mutable metadata; teams are never encoded as actors. |
| `Profile` | Stable local profile identity, independent of remote account or hostname. |
| `ConnectionRef` | Stable account binding to one profile, host and remote actor. Contains no credential. Refresh retains the ID; another account needs another connection. |
| `RuntimeRef` | Stable runtime identity bound to a profile and native/appliance kind. A display-name change does not create another runtime. |

Local host/profile/connection/runtime IDs are 32 lowercase hexadecimal digits.
Remote IDs remain opaque, bounded strings, including IDs that are not numbers.
Repository/actor/team surrogates use PostgreSQL BIGINT and Go int64; JSON encodes them
as strings. A credential connection or a repository URL is never the repository
identity. Separate provider instances with the same remote numeric IDs stay
separate, even if their repositories/accounts have the same names.

The canonical persistence owner is `internal/store/collaboration_*`. Observing a
repository, actor or team atomically updates its current display metadata while
retaining its ID. Database guards prevent identity rebinding/deletion. Composite
foreign keys prevent a connection's actor or repository association from crossing
hosts. Multiple connections, accounts and profiles may refer to the same remote
repository; a connection association is not an access or spending grant.
Team observation likewise grants neither membership nor repository permission.
An empty team name preserves the known name, or uses its external ID on first
observation. Names and organization slugs never select an identity.

Store resolution is lookup-only. `CollaborationRepositoryOnHost` and
`CollaborationActorOnHost` and `CollaborationTeamOnHost` require the expected host
with the local ID; `CollaborationRepositoryByExternalID`,
`CollaborationActorByExternalID` and `CollaborationTeamByExternalID` take
that host with its exact opaque remote ID. Missing or foreign mappings return
`store.ErrNotFound`, without allocating, guessing a provider or checking access.
Names, logins, URLs and credential connections do not substitute for these keys.

`NativeProjectForCollaborationRepository` and
`CollaborationRepositoryForNativeProject` join through the explicitly adopted
Fountain host in one SQL read. Native Project repository/owner IDs stay native,
including when their canonical collaboration surrogates differ. An unadopted
ledger, another host or a missing mapping cannot select a native Project merely
through equal numbers or display names. `Store.ProjectByRepository` still takes
an appliance-native ID. Factory control and transactional authority/completion/
priority/run-feed consumers now use the scoped joins; native lifecycle commands
resolve the canonical repository before changing its Factory gate. Repository-place
selection/settings, API actors and Fountain read/effect/receipt consumers now use
these boundaries. Engine/provider qualification and explicit runtime placement
still gate new dispatch capabilities; these lookups grant neither placement nor
provider permission.

Fountain's `FountainIdentities` adapter requires the explicitly adopted host and
has no numeric passthrough. Outbound repository/actor/team resolution is lookup-only;
authenticated incoming observations may register unseen subjects. Empty display
metadata supplies an external-ID placeholder for a new subject and preserves
existing labels/logins/names atomically. Known subjects resolve without reallocating.
Signed issue/comment/repository events use this boundary only after signature
verification. The unattended acceptance/dispatch reader resolves canonical
repositories back to native request IDs and maps provenance posters to local
actors while preserving the original snapshot and native issue identities.
Missing/foreign mappings refuse before provider reads. An event observation or
new mapping does not grant automation authority.

Factory API repository paths, filters and model scopes now take local IDs. Its
permission helpers resolve the adopted host before calling native authority;
foreign/missing mappings cannot reach a native repository check. A Fountain host
discovers the adopted host and current local actor through authenticated
`GET /api/factory/identity`, retaining the native session actor separately. It
discovers the local repository and current actor through authenticated
`POST /api/factory/native-repositories/resolve` with a native repository string.
The response includes the host, local repository/actor IDs, the native repository ID and
current display name. Native sessions, Project membership and broker ownership
keep their native IDs. Policy/operator/capacity/sponsorship human attribution uses
local actors. Sponsorship records carry canonical execution actors; broker user
and grant identities remain native at the credential boundary.
Configured native actor bindings are projected into local actor references for
status/settings. Policy actor references must resolve on that same Fountain host;
their opaque native token references remain unchanged.

The Fountain feed catalogue/selection and project-work adapter now await this
discovery before supplying canonical repository/account scopes to Factory models.
Catalogue discovery retains native provider locators and URLs, resolves each
repository separately, and refuses mismatched hosts/actors rather than guessing
from numbers or labels. Drafts use the adopted host and local actor. Mounts abort
pending discovery and ignore superseded results.

The shared Fountain settings component also resolves its native Project binding
before using Factory status, permissions, policy/grants, sponsorship, activity or
repair. Its native environment/preparation APIs retain native IDs. Reads capture
the resolved context and abort on replacement/hiding; resuming resolves again.
Repository and policy actor IDs remain strings throughout these controls.

Knowledge, Factory status and App verification also resolve the native locator
before mounting Factory components. Native inventory filtering and application
Project associations retain native IDs; Knowledge source events and issue work
use the resolved local repository. Run views must return that exact local ID,
including BIGINT string values; the decoder refuses a native/foreign scope rather
than replacing it with the caller's repository.

Request places use the resolved local actor and repository. Their native picker
resolves the selected row before accepting it, and verifies the same host/actor.
`GET /api/factory/repositories/{repositoryID}/identity` resolves local IDs back to
native navigation metadata only on the adopted host and after current visibility.
Requests in another repository use this boundary rather than passing local IDs
to native inventory APIs. Foreign/missing metadata supplies no native link.

The request composer's draft key includes an explicit scope and local actor;
both Fountain request places and workspace setup supply their adopted host as
scope. Workspace account discovery must echo the native inventory actor before
the composer mounts. Native picker selections resolve to local repositories for
request writes. Committed requests/overview retain a separate local reference;
preparation and watch navigation resolve it back to a visible native Project.
Missing or mismatched scopes cannot trigger request writes or native setup.
Native Project/account/terminal custody is retained. Old draft keys remain
untouched and are not automatically migrated; T14-UPGRADE still requires explicit
retained-client recovery with a verified profile/account binding. Unproven pending
commands cannot acquire authority through recovery. Empty draft scope is now
limited to standalone component fixtures.

Fountain workspace watches also resolve the native Project repository before
decoding the canonical run view. The watch context names `nativeRepositoryId`
explicitly: the native output handshake still binds native repository, run and
session generation, as enforced by the host's output route. It never receives the
canonical repository used for HTTP run metadata. Refused discovery, a mismatched
run view or retirement during discovery cannot open an output stream. This is the
Fountain host adapter; portable/native desktop output remains a later product gate.

Run-status HTTP reads join a recorded canonical run-view repository through
`CollaborationRepositoryForNativeProject` before selecting verification, policy
or repair records. An unresolved/foreign/native-number view reports a conflict
before host inspection; an unbound run stays unbound. Permission checks, Project
ownership and host inspection retain their native keys. Issue-zero verification
classification and repair-owner reads use the canonical assignment/actor scope;
reading status alone never grants Retry or repair authority.

Feed repository keys now travel as positive decimal strings through the shared
host catalogue, subscriptions, feed entries, human/worker posts and contributions.
HTTP projection leaves retained subscription/contribution integers intact. Native
discovery, current actor identity and reverse repository metadata pass through
the Fountain frontend proxy with their exact methods and no query overrides.
Current scoped drafts and pending commands retain their identity through remount,
retry and storage denial; durable pending commands also recover after reload.
This is not an installed-client parity or historical unscoped-draft import claim.
Existing retained equal-number namespaces keep their identities.

Request/departments API ownership is canonical: native sessions resolve through
the adopted host before saved requests, history/review, planning/generation,
overview/revisions and verification limits are read or changed. Human idea
provenance must match the canonical owner; caller-supplied owner fields cannot
replace the authenticated actor. Planning attempt and repair visibility compare
the same owner. Native permission/lifecycle/broker calls retain their native
session. Foreign/missing requests remain private 404s.

Feed subscriptions, human posts/replies, retries, private authoring links and
product-change review/read ownership use the same canonical actor. Native issue
context reads reverse the canonical repository through the adopted host before
calling Fountain under the live human admission. Display names and agent/role
post authors retain their recorded meanings. Activity attribution and repair
sponsors also use canonical humans, while repair command principals retain the
native admission identity.

Delivery-repair requests pass the authenticated canonical actor separately from
that command principal. The controller never parses a native/OS principal to
select a request owner: the canonical actor must own the exact accepted
revision/repository before an absent origin limit can be created. Retained
command identities and principal attribution stay intact. The browser action
body cannot choose actor, owner or principal; current native permission/session,
settled verification evidence and funding gates still apply.

Verification-allowance increases also take canonical ownership and command
attribution separately. The Fountain adapter supplies the native session
principal; the store authorizes the current canonical intent owner under its
existing transaction lock. New commands no longer label a canonical owner key as
a native actor. Replay keeps the captured command and its original principal,
returns current limits/usage and cannot reset a later increase. Body fields
cannot override the admitted actor, owner or principal.

Opaque native intake proofs retain their provider JSON. Capture reverses the
canonical actor/repository and checks the admitted native author. Lookup-only
proof decoding maps native subjects on the adopted host; the shared private
transport adapter returns canonical receipt scope. Missing mappings cannot be
allocated by decoding a proof. API recovery rechecks exact source/revision,
author/repository, content digest and deletion/revocation before reuse. Differing
and retained equal-ID namespaces are supported by this intake boundary. The
provider read/effect/recovery/receipt/publishing consumers use the same scoped
mapping; T02-ENGINE qualifies their combined portable closure and Fountain
behavior before application-service extraction.

Creation recovery now resolves its canonical repository through the adopted host
before using the native service credential. Native recovery pages are validated
before observed original creators are mapped to Factory actors. Returned batch
and event repositories are canonical, including after store reopen. Native issue
IDs/indexes, recovery positions and delivery identities, original content facts,
creator-write facts and explicit incomplete provenance/retention coverage retain
their recorded meaning. Foreign/unmapped repositories and an unbound observer
refuse before credential-bearing source reads.

Graph authorization, generated-issue reads, submission/lookup and feedback now
resolve canonical repository, human owner and graph-actor keys through the same
adopted host. The coordinator's graph actor is canonical while its configured
credential owner stays native. Credential ownership and stable permission
brackets remain authoritative. Generated creators are canonical; native issue
identities, revision/occurrence/dependency facts and redactions retain their
recorded meaning. Context-aware receipt decoding uses existing mappings only,
returns canonical actor/repository facts and retains the original raw receipt.
Outcome records keep unknown/partial/indeterminate states and opaque evidence;
unsupported receipts remain non-adoptable.

Completion enumeration and current-tip capture also require the adopted host
binding. Native page/body/ref reads resolve the canonical repository; local
authority capture and sealed material keep its canonical key. A page must echo
the requested native repository, and each body must match its enumerated native
issue ID as well as its index. Foreign/unmapped scopes refuse before source
reads or local authority capture. Existing revision brackets, digest checks,
pagination and native-read budgets remain in place.

Publication resolves canonical repository, publishing actor and correction author
before native remote/credential access or operation construction. PR creation's
nested head repository is native too. Operation outcome metadata returns canonical
repository/actor keys through existing host-scoped mappings without allocation;
raw receipts and native PR/issue identities remain intact. Context-aware adoption
matches the raw native tuple to canonical recorded work and returns a canonical
PR author. Missing observations and receipt-less cancellation tombstones retain
zero scope. Lookup/cancel remain available after credential withdrawal through
owning-installation admission.

Review observations, packets and receipt adoption now resolve canonical
repository, reviewer and PR-author keys before native access. Native pending
review authors and PR authors are compared in the native namespace; returned
observation/adoption actors are canonical. Raw receipts and review/comment IDs,
inline finding keys and head/base/merge-base facts retain their meaning. Thread
resolution also resolves packet/readback scope, rejects a foreign canonical
outcome before reading, and confirms the native resolver behind the exact head
and resolved conversation digest. Publication/review/resolution share lookup-only
operation metadata projection; credential withdrawal does not erase outcomes or
permit a new identity allocation.

Check observations resolve canonical repository and assessor keys before native
reads, retaining exact-head/latest-status evidence. Merge observations resolve
repository, merger, PR author and approval reviewer before access; native approval
and merger facts are compared to native subjects. Returned observations and
adopted/completed actor identities are canonical. Raw operation receipts,
PR/issue/review IDs, native commit/ancestry and original issue-closing facts remain
provider evidence. Receipt adoption receives context for identity lookup; lookup
and cancellation continue after credential withdrawal without allocating from
opaque outcomes. These consumer cutovers do not qualify new provider capabilities,
an installed native client or the complete portable engine dependency closure.

Delivery-judgment discussion uses the same boundary: its canonical merge work
resolves before credentials or native reads; exact native target/body/revision
checks precede participant observation. Returned comment posters, review actors
and thread resolvers are canonical, so blocking-review and resolution-reuse
comparisons use the controller's namespace. Raw native record IDs, body/version
evidence and conversation digests remain provider facts. Complete authenticated
discussion may register a new participant without granting policy or execution
authority; absent optional attribution stays absent.

Store opening adds the registry transactionally and verifies its columns/guards,
without changing the core schema marker or renumbering workflow rows. As with the
existing library/artifact tables, the Go store owns this additive initialization.
It never selects a provider namespace as a side effect of opening storage.

New composition calls `InitializeEmptyCollaborationLedger` before admitting work
or allocating numeric mappings. That step refuses a ledger with retained data
(the seeded global readiness counter does not carry provider identity). Existing
Fountain data instead uses `AdoptFountainLedger(ctx, hostID, sourceAPIBase)`.
Until explicit initialization/adoption, mapping attempts return `ErrUnboundLedger`;
host/profile metadata can be prepared without claiming old repository numbers.

The appliance Factory constructor calls `BindConfiguredFountainLedger` with its
trusted `Config.ForgejoURL` before constructing a coordinator. Initial host
registration and retained adoption share one transaction. Repeated or concurrent
construction keeps the same host and does not rescan mixed-provider history.
Another configured source or an unbound registry that already has numeric
mappings refuses construction; a failed import leaves no newly registered host.
Forge composition does not perform this binding. Hostless native composition
uses the separate empty-ledger initialization above.

Appliance composition translates the captured canonical execution actor only in
the outgoing host launch. The native broker receives its Fountain user ID while
the assignment, prompt/MCP inputs and retained canonical launch remain unchanged.
Missing or foreign mappings refuse before native credential acquisition.

Closing/reopening the store and reconstructing Factory retains the adopted host,
canonical grants and native Project custody. Command replay preserves captured
attribution and current limits/usage without rewriting the command journal or
reinterpreting other providers' local IDs. T03 extracts application admission
services; it does not restore raw provider-numbered ledger writers.

## Retained Fountain adoption

Before admitting work, the owning appliance composition supplies the registered
Fountain `HostID` and the already verified source locator from its configuration.
The store requires the normalized locator to match and the provider to be
Fountain. This is an internal composition API, not a browser-selected host or
remote enrollment proof. Authentication and actual provider verification remain
with the connector/admission owners.

Adoption holds the namespace gate and captures a stable inventory under table
locks. It reads identity-bearing columns, structured JSON/JSONB, subscription
arrays, and the retained command target/principal/JSON payload and outcome forms.
JSON numbers are decoded without floating-point conversion. Positive numeric
references are conservatively reserved in the declared Fountain namespace; labels
come from existing users/Projects when available, otherwise explicit placeholders.
The import does not assert remote existence or grant permission.

Each retained number becomes the same local number and its decimal remote ID.
No workflow row, decision, captured input, user, key or credential is rewritten.
The identity allocators restart beyond the imported ranges. Binding, mappings and
allocator restarts commit together; [PostgreSQL's transactional sequence restart](https://www.postgresql.org/docs/current/sql-altersequence.html)
allows them to roll back together on failure. Store observers take the namespace
gate before evaluating sequence defaults, preserving the adoption lock order.

An existing numeric registry without a retained-source binding is ambiguous and
is refused. Once bound, a different Fountain instance is refused. A same-source
retry is a no-op: it does not rescan later GitHub rows or reset allocators.
The registered instance's locator may change, but its stable HostID remains bound.

Factory collaboration key columns use BIGINT on fresh and retained stores.
Native Project/user columns keep native provider IDs. The T02 identity consumer
cutover qualifies the scoped translations and cross-provider refusals separately
from this import. Neither result deploys a running appliance, activates another
provider or completes the remaining engine and installed-product gates.

The [read contract](records.md) now defines neutral issue/PR/comment/review/check
records, capabilities and immutable diff anchors, with a mirrored Lit client
contract and production anchor/diff decoders. The [effect contract](effects.md)
defines server-side operation/graph intents, conditional guarantees, provider
evidence and partial/uncertain outcomes for the existing journals. The registry
itself does not grant permissions or implement dispatch, placement leases,
credential storage or execution. Those consumers belong to the later slices in the
[implementation backlog](../../docs/development/factory-products/implementation-tasks.md).

The [Factory authority/runtime contracts](../factory/access/README.md) now define
admitted actors, independent dispatch identities, native/appliance provenance,
placement generations and explicit automation proposals. Account observation
does not activate them. Production admission and executor cutovers remain later
consumers; the identity registry does not become a permission ledger.

## Integration evidence

`internal/store/schema_test.go` extends the existing PostgreSQL integration
checks for provider/instance collisions, concurrent observation, renames,
reconnection through a configured locator, several credentials/accounts sharing
a repository, immutable bindings and cross-host FK refusal. It also reopens
retained Project/feed/intake data and proves it is unchanged while new allocations
and empty-ledger initialization are refused.

The checks ran on the private native PostgreSQL fixture. The existing current
schema, retained-68/feed migration and unsupported-version refusal checks also
passed. Logs: `/home/vince/factory-identity-registry-final-identities.log` and
`/home/vince/factory-identity-registry-final.log`. An additional actual BIGINT
allocation/connection-FK case passed above the 32-bit range; log:
`/home/vince/factory-identity-registry-bigint.log`. Actual provider permissions and
connected journeys are later verification, not established by registry tests.

T01-LEDGER adds three real PostgreSQL adoption checks in the same existing file:
exact retained-row fingerprints, numeric precision above `2^53`, column/JSON/
subscription/command references, wrong/ambiguous source refusal, new-provider
allocation beyond the reserved ranges, reopen/retry with mixed-provider history,
injected import failure and transactional sequence rollback, and eight concurrent
adopters. The three existing registry checks also passed after observation's
gate/sequence lock order changed. Log:
`/home/vince/factory-fountain-adoption-pg.log`; source guard:
`/home/vince/factory-fountain-adoption-source-guard.log`. Tests used disposable
databases on native PostgreSQL and ended with clean shutdown. No live appliance
data, external repository, provider account or deployment was changed.
