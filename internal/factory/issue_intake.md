# Issue-intake persistence boundary

`issue_intake.go` owns the Factory issue-maker semantic mutation and receipt,
plus the admitted ledger scope of a provider proof. `internal/store` persists
that proof as bounded opaque JSON. `internal/forgejo/issue_intake.go` interprets
Fountain's claims, constructs its SDK request and validates/translates its receipt.
There is no SDK type alias, second operation journal or copied provider signer.

An admitted Fountain contribution still calls its existing native authorization
API. `FountainIdentities.EncodeIssueIntakeGrant` separates the returned proof from its scope before
the store binds it to the current post/revision/author/repository. The adapter
must derive that scope from the same admitted proof. The store does not verify
signatures or infer permissions from JSON. `FountainIdentities.DecodeIssueIntakeGrant` reads retained
metadata; it does not authenticate an arbitrary bearer. The native host remains
authoritative for signature, installation, expiry and issue-write admission.

The existing `issue_intake_grant` JSON shape remains unchanged, including its
exact bearer value. PostgreSQL continues to normalize JSON formatting. Existing
rows require no migration or reissue. The proof is server-side request-scoped
authority, never an AI-provider account credential, browser DTO, prompt input,
publication grant or execution/spending permission.

`IssueMakerMutation` contains only action, allocated slot/step, exact title/body
and refinement preconditions. It cannot carry a bearer. The existing role-post
lock and journal assign slots/steps before sending, replay captured requests by
operation/hash identity and refuse changed identities. Refreshed authorization
does not rewrite semantic requests. Limits remain 16 issue slots (including the
reserved request slot) and 32 operations. A full journal still admits a retained
idempotent replay. Refine requests keep their original expected title/body digest.

`FountainIssueIntake` attaches the retained grant only at dispatch, sharing the
existing private service transport. It rejects invalid metadata/mutations and
mismatched returned issue/slot/title/body-digest facts. Its neutral receipt uses
the canonical issue discriminator/ref with opaque external ID and int64 index;
large identities are not decoded through floating point. Private peer-UID and
service-admission checks still precede a credential-bearing request.

`service.IssueIntakeCapture` now owns canonical source/author/revision checks,
current resource admission, retained-proof reuse and capture into this same journal.
Known absence or locally invalid/expired metadata can request fresh issuance;
unknown retained reads or decoding errors do not authorize replacement. Issued scope and decoded proof must
match the exact source/actor/repository/digest, remain unexpired and survive a
fresh source/permission check. Store persistence also checks body digest and
deletion/revocation. Capture launches no worker, model or issue mutation.

The Fountain adapter resolves canonical actor and repository back to the adopted
host and requires live native issuance to belong to that author. It encodes the
returned native proof for the shared capture service. Proof decoding uses lookup-only external-ID mappings
on that host; it never allocates identities or assumes equal native/local IDs.
The opaque proof keeps its native metadata while scope and returned issue refs
use canonical Factory IDs. API recovery rechecks source, revision, author,
repository, content digest and deletion/revocation before reuse. Native command
principals retain their admission namespace. The remaining T02-IDENTITIES
provider boundaries still gate mixed-provider dispatch. A GitHub connection does
not acquire this Fountain grant or its atomic guarantees.

## T02-INTAKE evidence

The production import closures of `internal/factory` and `internal/store` contain
no Fountain SDK, concrete host or Fountain adapter package. T02-GRAPH has since
cut the coordinator's graph/source/receipt consumers over to neutral ports.
T02-RUNS has removed its concrete host dependency and wired neutral observations,
custody and export. Retained run/tool/admission and remaining command boundaries
are still active, before canonical ID cutover and T02-ENGINE qualification.

Extended existing PostgreSQL integration files (no new unit tests) verified a
pre-cutover JSON grant and mutation rows, refresh and reopen, wrong actor/repository/revision
refusal, exact frozen retry/precondition requests, conflicting operation hashes,
eight concurrent callers, slot/step limits and replay at capacity. Stored semantic
requests contain no bearer or grant. The private native PostgreSQL fixture ended
with clean shutdown.

Four existing HTTP/store issue-maker journeys also passed: capture before
planning with one reply per issue, new issue to verified, lost-effect-reply
recovery and deleted-source refusal. Their native peer is explicitly scripted;
these results do not establish real Fountain conditional-mutation semantics.

An extended existing Unix HTTP integration checked the real private transport
and admission, exact SDK request/precondition translation, receipt IDs above
`2^53`, corrupted-receipt refusal and wrong peer-UID refusal before dispatch.
The endpoint is scripted, not Fountain's real issuer/ledger. No real provider
operation, model call, credential enrollment or deployment was performed.

Go compilation and the source guard passed. Logs:
`/home/vince/factory-intake-cutover-build.log`,
`/home/vince/factory-intake-portable-deps.log`,
`/home/vince/factory-intake-cutover-source-guard.log`,
`/home/vince/factory-intake-cutover-pg.log`,
`/home/vince/factory-intake-cutover-bounds-pg.log` and
`/home/vince/factory-intake-cutover-unix.log`. The final retained-mutation/bounds
run is `/home/vince/factory-intake-cutover-retained-pg.log`.
T02-ENGINE retains the actual Fountain workflow and complete portable-engine
closure gates; this persistence slice does not complete them.
