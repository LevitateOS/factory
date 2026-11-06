# Factory HTTP boundaries

`Product.Handler` composes the existing feed, request, work, repository, run,
record and acceptance route owners. One host-bound canonical actor/host,
principal, display author and mandatory current-admission callback replace
group-level identity fields. Present concrete service stores must match the
selected canonical store; interface source ports retain their service's scope
checks. Hosts resolve a fresh product binding per request behind their admitted
channel. Resource ports must independently revalidate the same account/generation
and current permission at their service mutation/evidence boundaries.

The product identity route returns only canonical host/actor, without native
aliases. Missing account admission/store refuses. Group handlers retain their
parsing, JSON projection, limits, journals and sanitized errors. Output upgrade/
peer ownership and worker MCP keep their separate host channels. Composition
starts no listener, account enrollment, provider/model call or background work.

Fountain's product route adapters and the native application both consume
this composition. Fountain binds its current session and lazily selected service
ports; native hosts bind their real connector account. Additional Fountain-native
identity/navigation fields stay in its host adapter. Neither private IPC nor
shared routing fabricates provider authentication or execution authority.

`CollaborationIssues.Handler` owns read-only normalized issue endpoints:

```text
GET /api/factory/collaboration/repositories/{repository}/issues?state=open&cursor=...&limit=20
GET /api/factory/collaboration/repositories/{repository}/issues/{number}
```

List traversal supports open (default), closed and all non-PR issues; detail
reads include closed issues. Repository IDs are canonical and numbers are repository-local locators.
Repeated/unknown query fields and detail queries refuse. The shared service
checks the returned scope, observation, stable remote IDs and page facts; an
unconfigured source is unavailable. Current account admission is rechecked before
publishing a response. Provider SDK/session custody stays in its adapter. These
routes do not replace the separate Factory work/acceptance issue routes.

`CollaborationDiscussions.Handler` owns ordinary discussion comments:

```text
GET /api/factory/collaboration/repositories/{repository}/issues/{number}/comments?cursor=...&limit=20
GET /api/factory/collaboration/repositories/{repository}/pulls/{number}/comments?cursor=...&limit=20
```

Issue and PR responses retain their distinct typed parent references. The
provider reads full bodies and current participant/time facts under the correct
issue or PR unit permission, with exclusive bounded cursors. Lifecycle events,
reviews and inline code comments are separate resources. Unknown/repeated window
fields refuse; current account admission is rechecked before publishing data.

`HumanComments` owns admitted human issue/PR comment commands under
`/api/factory/collaboration/repositories/{repository}/{issues|pulls}/{number}/comments`:

- `POST` accepts only `command_id` (32 lowercase hex characters) and `body`.
  The original UTF-8 Markdown limit is 64 KiB; the JSON envelope allows 512 KiB
  for escaping. Fountain's frontend bridge and private body wrappers make the
  same narrow exception on these submission paths; cancellation keeps its
  ordinary 64 KiB envelope.
- `GET /commands/{command}` reconciles the original saved command within the
  current host/actor/repository/parent. No body or provider preparation is sent
  back to the client. `POST /commands/{command}/cancel` accepts `{}` and cancels
  that saved command; terminal effects retain their original receipt.

The service commits capture before dispatch and keeps the same identity on
retry. A failed/lost reply requires lookup with that original command ID;
it does not authorize generating a replacement command. Lookup of a missing or
invisible local command is not a provider no-effect proof. Known outcomes return
200; unresolved write outcomes return 202. Delivery errors return bounded fixed
messages, while the saved capture remains available for reconciliation.

Responses contain only command identity, observation/effect/cancellation/
completion and finished status. Provider receipts, installation/operation IDs,
digests, native identity aliases, admission and retained writing stay server-side.
Refresh the normal discussion read after commit to obtain visible comments.
Current host admission is checked before and after each operation. Fountain also
retains its native method/contribution/session-generation/origin checks and the
adapter's live human/permission/installation checks. These shared routes grant
no execution, spending or background enrollment.

`HumanEdits` owns human title/description commands under
`/api/factory/collaboration/repositories/{repository}/{issues|pulls}/{number}/edits`:

- `POST` accepts `command_id`, at least one of `title` and `body`, and optional
  `expected` original content.
  Supplied fields must be strings; omitted fields stay unchanged, an empty body
  clears the description, and null/unknown/duplicate fields refuse. Original
  UTF-8 limits are 255 bytes for title and 64 KiB for body, with a 512 KiB escaped
  JSON envelope admitted only on the two exact edit submission paths.
- When supplied, `expected` is exactly `{external_id,title,body_digest}` with
  three non-null string fields. The ID is the normal issue/PR reference's opaque
  external ID, title is its exact original title, and digest is lowercase SHA-256
  of its exact original UTF-8 Markdown. The original title is bounded to 64 KiB;
  this does not raise the 255-byte new-title limit. Null/missing/non-string,
  unknown/case-alias/duplicate fields and invalid digests refuse. Omission uses
  the current native preparation; a draft editor must supply its original base.
- `GET /commands/{command}` reconciles original retained writing in the current
  host/actor/repository/parent scope. `POST /commands/{command}/cancel` accepts
  `{}` and uses that same retained capture. A changed request under an existing
  command ID conflicts; lookup/cancel never accept replacement writing.
  Cancellation keeps its ordinary 64 KiB envelope through both the frontend
  bridge and shared handler; only exact edit submission gets the larger limit.

Factory saves both the requested condition and the adapter's projection of the
exact native preparation before dispatch. A mismatch takes the existing native
cancellation path and returns its real terminal no-effect result; it never
applies the draft. A lost cancellation reply leaves the same command unfinished
for scoped lookup, rather than treating an HTTP refusal/absence as proof. Changing
or dropping the condition under that ID conflicts. Freshly reviewed content
requires an explicitly new command. Changes after capture still face the native
revision/title/body CAS. Lookup/cancel use the original retained condition and
preparation, even after content changes. Conditions and captured original facts
are not returned in the safe result and grant no authority.

The same safe command-result fields described above are returned, with no
provider preparation, receipts, namespace, digests, native IDs or writing.
Committed native completion needing intervention remains unfinished: submission
returns 202 with its actual completion state. A lost native or HTTP reply must
be reconciled under the original command ID. Refresh the ordinary issue/PR read
after a committed edit to obtain visible content.

Fountain composes the same Application service and Product routes behind its
existing method/contribution/session-generation/origin and current-human checks.
Its package declares `native.repository.discussions.edit`. Current admission is
rechecked after delivery before any HTTP result is released. This local HTTP
boundary does not qualify installed editor navigation or desktop HTTPS access.

`HumanPullCreates` owns human PR creation from explicit existing branches:

| Method | Path below `/api/factory/collaboration/repositories/{repository}` | Request |
| --- | --- | --- |
| POST | `/pulls` | `command_id`, canonical string `head_repository`, `base_branch`, `head_branch`, `title`; optional string `body` and boolean `allow_maintainer_edit` default to empty/false. |
| GET | `/pull-creation/commands/{command}` | No query or replacement creation fields. |
| POST | `/pull-creation/commands/{command}/cancel` | Strict `{}`; recovers the original saved source, writing and delegation. |

Only exact lowercase fields are accepted. Missing required fields, null/wrong
types, numeric/noncanonical repository IDs, unknown/duplicate fields, invalid
branch syntax, UTF-8/NUL and oversized writing refuse. Host/actor come from
current admission; clients cannot supply tips, a predicted PR number, provider
preparation or receipts. Title/body limits are 255 bytes/64 KiB. Only the exact
POST pull-collection path admits a 512 KiB escaped JSON envelope through the
frontend bridge and both private host wrappers. Cancellation keeps the ordinary
64 KiB limit; oversized mutation bodies refuse before proxy dispatch.

Product uses the same Application Store and rechecks current account before
releasing results. Responses contain only the safe command-result fields above;
native IDs, receipt, preparation, namespace, digest and writing stay server-side.
Refresh normal PR reads under current visibility after commit. Known unfinished
completion returns 202 for mutation and 200 for lookup with `finished:false`.
Lost native or outer HTTP replies reconcile the original capture after reopen.
Fountain declares `native.repository.pulls.create`; installed controls, capability
presentation and desktop HTTPS remain separate qualification.

`HumanIssueCreates` owns plain human issue creation:

| Method | Path below `/api/factory/collaboration/repositories/{repository}` | Request |
| --- | --- | --- |
| POST | `/issues` | `command_id`, nonblank unpadded `title`, optional `body` (empty when omitted). |
| GET | `/issue-creation/commands/{command}` | No query or replacement writing. |
| POST | `/issue-creation/commands/{command}/cancel` | `{}`; uses the original retained capture. |

Only exact lowercase field names are accepted. Fields must be strings; null,
unknown/duplicate fields, invalid UTF-8, NUL and oversized writing refuse. Limits
are 255 title bytes, 64 KiB body bytes and a 512 KiB JSON envelope; the larger
envelope is admitted only on the exact POST issue-collection path through the
frontend bridge and both Fountain body wrappers. Lookup/cancel command routes
are explicitly admitted by that bridge. Cancellation keeps the ordinary 64 KiB
bound in the bridge, host wrappers and shared handler; oversized mutation bodies
refuse before forwarding. Creation command paths live beside the issue collection,
avoiding the existing issue-number/comment route namespace.

The Product binds current host/actor and the same application store. Responses
use the safe command-result fields above plus `issue`, which is either null or
the canonical `{kind:"issue",repository,external_id,number}` reference. Repository
and number retain exact decimal-string encoding. The adapter derives this
destination from the authenticated original creation receipt, then confirms its
exact external ID and index through a fresh normal issue read. It never searches
by title/body or assumes the newest issue belongs to this command. Submit, lookup
and cancellation after commit can return the same reference. Provider preparation,
raw receipts, namespace, digests and writing remain private.

An unreadable destination returns `issue:null` while preserving the command's
actual effect and finished state; null is not proof that nothing was created.
Current account admission is checked again after the destination read, and
withdrawal suppresses the entire outcome. Normal issue collection/detail GET
responses have a scoped 2 MiB frontend envelope for full escaped Markdown and
metadata. Creation-command, capability and other responses retain their existing
bounds. Incomplete native bookkeeping returns 202 with the committed effect and
`finished:false`; it may already have a readable issue. Lookup/cancel preserves it.
Lost native/HTTP replies reconcile under the original command ID. Fountain
declares `native.repository.issues.create` and rechecks native session/admission
after delivery before returning results. Installed editor and desktop gateway
journeys remain separate qualification.

`HumanLocks` owns issue/PR comment-lock commands under
`/api/factory/collaboration/repositories/{repository}/{issues|pulls}/{number}/locks`:

| Method | Path suffix | Request |
| --- | --- | --- |
| POST | Base path above | Required `command_id` and boolean `lock`; optional string `reason` defaults to empty. |
| GET | `/commands/{command}` | No query or replacement mutation fields. |
| POST | `/commands/{command}/cancel` | `{}`; uses the original retained capture. |

Exact lowercase fields are required. Missing/null/string/number lock values,
null reasons, unknown/duplicate fields, invalid UTF-8, NUL and oversized reasons
refuse. Lock preserves up to 64 KiB of original reason, including blank text;
unlock requires an empty reason. Current configured-reason and lock-state rules
remain native dispatch checks. These commands change comment-lock state.

Only the two submit routes admit a 512 KiB escaped JSON envelope through the
frontend bridge and both private host body wrappers. Cancellation keeps the
ordinary 64 KiB bound in the shared handler, bridge and host wrappers. The bridge reads bounded lock mutation bodies
before forwarding and returns 413 on overflow, so no partial request reaches
provider dispatch. Explicit method/path/positive target/command-ID admission
remains in place. Factory ownership routes these requests to its private socket.

Responses expose only `command_id`, observation, effect, cancellation, completion
and finished state; original reason, provider intent, receipt, namespace, digests
and native IDs remain server-side. The same Product/Application store and current
host/actor bind all routes. Scoped lookup/cancel recovers original retained
intent; changed request conflicts. Lost private or outer HTTP replies reconcile
under the original command. Fountain declares
`native.repository.discussions.lock` and rechecks current session/admission after
delivery before returning a result. Actual HTTP/Unix bridge qualification does
not qualify installed UI navigation or desktop HTTPS access.

`HumanStates` owns direct issue/PR close/reopen commands under
`/api/factory/collaboration/repositories/{repository}/{issues|pulls}/{number}/state`:

| Method | Path suffix | Request |
| --- | --- | --- |
| POST | Base path above | Required `command_id` and boolean `close`. |
| GET | `/commands/{command}` | No query or replacement mutation fields. |
| POST | `/commands/{command}/cancel` | `{}`; resolves the original saved capture. |

Only exact lowercase fields are accepted. Missing/null/string/number booleans,
unknown/duplicate fields, invalid UTF-8 and invalid command/target identities
refuse. These routes use the ordinary 64 KiB request bound through the frontend
and both private host wrappers. The bridge reads bounded mutation bodies before
forwarding, returning 413 on overflow before capture or provider dispatch.
No prose-size exception was added.

Responses use the same safe human-command projection: command identity,
observation, effect, cancellation, completion and finished state. Native IDs,
receipts, preparation, namespace, digests and admission stay server-side.
Completed commands return 200; unfinished mutations return 202, including a
committed state change whose native completion needs intervention. Lookup returns
200 with the unfinished projection. Original preparation/outcomes survive reply
loss and Store reopen; replay does not repeat uncertain completion. Current
account/native session is checked before work and before releasing results.
Fountain now declares `native.repository.discussions.state` and binds the same
Product/Application store. Installed UI and desktop HTTPS remain separate work.
Direct close/reopen does not combine comment posting or PR ref synchronization.

`HumanReviews` owns human PR draft/final commands under
`/api/factory/collaboration/repositories/{repository}/pulls/{number}/reviews`:

| Method | Path suffix | Request |
| --- | --- | --- |
| POST | Base path above | Required `command_id`, `event` and string `body`; optional full commit `expected_head`. |
| GET | `/commands/{command}` | No query or replacement intent. |
| POST | `/commands/{command}/cancel` | `{}`; resolves the original saved capture. |

Events are `draft`, `comment`, `approve` and `request_changes`. Body is required
even when blank; current native policy decides whether blank draft/comment prose
can use an existing draft. Request-changes needs nonblank prose. Original UTF-8
prose is bounded to 64 KiB, with a 512 KiB escaped JSON envelope admitted only on
the exact POST collection through the frontend and both private host wrappers.
Cancellation retains its ordinary 64 KiB bound. Null/missing/non-string,
unknown/duplicate fields, invalid UTF-8/NUL, invalid events and oversized prose
refuse. Existing review-list/detail reads keep their read owner.

For review and merge submissions, `expected_head` retains the caller's selected
commit in the original command. Native preparation refuses a different current
head before capture; replay cannot change the selection. Lookup and cancellation
recover the saved selection without accepting replacement intent. New shared UI
commands send this field. Older saved commands retain their original request
shape and are not silently upgraded. This does not add an atomic provider head
condition to review APIs; merge retains GitHub's existing `expectedHeadOid`.

Responses use only the safe command-result fields above. Preparation, receipts,
namespace, digests, native aliases and admissions stay server-side. The same
Product/Application Store and current host/actor bind every operation; native
method/contribution/generation/origin/session checks remain in force. Lost
private or outer HTTP replies reconcile under the original command after Store
reopen. Committed incomplete completion returns 202 with `finished:false` and
stays unfinished across lookup/replay; uncertain completion is never repeated.
Session loss after primary withholds the public outcome while retaining it for
the original human. Fountain declares `native.repository.reviews.write`.
Actual HTTP/Unix/native checks qualify these commands; installed review UI and
desktop HTTPS access remain subsequent work. New inline creation is separate.

`HumanMerges` owns ordinary merges and manual marking under
`/api/factory/collaboration/repositories/{repository}/pulls/{number}/merges`:

| Method | Path suffix | Request |
| --- | --- | --- |
| POST | Base path above | Required `command_id`, `method`, and either `message` for ordinary methods or `commit_oid` for `manual`; optional full commit `expected_head`. |
| GET | `/commands/{command}` | No query or replacement intent. |
| POST | `/commands/{command}/cancel` | `{}`; recovers original method/message or selected commit. |

Ordinary methods are `merge`, `rebase`, `rebase-merge`, `squash` and
`fast-forward-only`; only `manual` may select an existing full commit OID.
Mixed selectors refuse even when empty/null. Required fields reject omission,
null, aliases, duplicates and unknown fields. Original messages allow 64 KiB;
the 512 KiB submit envelope admits JSON escaping through shared Product and
Fountain frontend/private wrappers. The native SDK callback also permits a
512 KiB merge-submit envelope; native lookup/cancel and other small callbacks
retain their existing 64 KiB limit. Cancel retains the ordinary 64 KiB HTTP limit.
Only PR routes are admitted. Host/operator identity comes from current admission;
provider preparation and native identity are never client inputs.

Responses contain only command ID, observation, effect, cancellation, completion
and finished state. Native receipts, digests, installation and saved preparation
stay server-side. Incomplete completion returns `202` on submit/cancel and `200`
on lookup, with `finished:false`; known primary effects never resubmit. Product
requires one Store, and final current-session checks withhold withdrawn results.
Fountain declares `native.repository.merges.write`, separately from human reviews.
Installed controls and desktop Fountain HTTPS access remain subsequent work.

`HumanReviewRequests` owns user/team desired reviewer state under
`/api/factory/collaboration/repositories/{repository}/pulls/{number}/review-requests`:

| Method | Path suffix | Request |
| --- | --- | --- |
| POST | Base path above | Required `command_id`, `reviewer_kind` (`user` or `team`), string canonical `reviewer_id`, and boolean `requested`. |
| GET | `/commands/{command}` | No query or replacement intent. |
| POST | `/commands/{command}/cancel` | `{}`; recovers the original saved reviewer/state. |

User and team IDs occupy separate canonical namespaces; native IDs are never
accepted as substitutes. Required fields reject omission/null, aliases, duplicate
or unknown fields and numeric JSON IDs. The ordinary 64 KiB envelope applies
through the shared Product and Fountain frontend/private wrappers. Only PR routes
are admitted. The existing human-review capability and live request admission
remain required. Product composition enforces its single Store.

Responses expose only command ID, observation, effect, cancellation, completion
and finished state. Provider preparation, native receipt/IDs, authority namespace
and digests stay server-side. Pending/intervention completion returns `202` on
submit and `200` on lookup with `finished:false`; known effects are not submitted
again. Both lost-reply paths retain the original capture across Store reopen.
Final session checks withhold a result when the human changes after native commit.
Shared installed reviewer controls and desktop HTTPS remain subsequent work.

`HumanThreads` owns resolve/unresolve commands under
`/api/factory/collaboration/repositories/{repository}/pulls/{number}/thread-resolutions`:

| Method | Path suffix | Request |
| --- | --- | --- |
| POST | Base path above | Required `command_id`, string `thread_id`, boolean `resolved` and `anchor`. |
| GET | `/commands/{command}` | No query or replacement intent. |
| POST | `/commands/{command}/cancel` | `{}`; recovers the original saved thread/state/context. |

`anchor` is explicitly `null` for unknown original context, or the same complete
seven-field original selection described for inline commands below. It is
checked against live native thread preparation, not inferred from today's Git
placement. Omission, wrong types, duplicate/unknown fields, property aliases,
invalid UTF-8 and invalid selections refuse. The ordinary 64 KiB envelope applies
through the frontend and private wrappers; thread commands contain no prose.
Only PR routes are admitted. The existing human-review capability covers them.

Responses expose only command ID, observation, effect, cancellation, completion
and finished status; provider proof, receipts, identities, namespace and digests
remain private. Both reply losses recover with the original capture after Store
reopen. A known committed primary with pending completion returns `202` on submit
and `200` on lookup, with `finished:false`; reconciliation never resubmits it.
Post-commit session loss withholds the response. Actual frontend/HTTP/Unix/native/
PostgreSQL checks qualify these paths. Proven Git placement remains distinct from
stored original selection: a retained path missing from its trees is unplaced in
ordinary thread reads. `GET /api/factory/collaboration/repositories/{repository}/pulls/{number}/threads/{thread}/selection`
returns canonical host/repository/actor/PR/thread scope, `original_anchor` (the
complete stored seven-field anchor or explicit `null`), `resolved`, and separate
`resolve`/`unresolve` capability facts. This retained context is for command
construction, including when original paths or trees are unavailable; it does
not assert Git placement. The read allocates no command or native operation and
exposes no provider preparation. It requires current read admission and rechecks
the human after I/O. Allowed actions are presentation facts; submit observes
current policy/context again. Installed UI and the desktop gateway remain
subsequent work.

`HumanInlines` owns draft/immediate PR inline commands under
`/api/factory/collaboration/repositories/{repository}/pulls/{number}/inline-comments`:

| Method | Path suffix | Request |
| --- | --- | --- |
| POST | Base path above | Required `command_id`, boolean `draft`, string `body` and `anchor`. |
| GET | `/commands/{command}` | No query or replacement intent. |
| POST | `/commands/{command}/cancel` | `{}`; resolves the original saved capture. |

The anchor requires exactly `base`, `head`, `path`, `side`, `scope`, `start_line`
and `end_line`. Commits are full lowercase Git object IDs; path is literal and
relative; side is `old` or `new`. File scope requires explicit zero line bounds;
line scope requires positive inclusive ordered bounds within JavaScript's safe
integer range. The provider proves that selection against the current PR diff.
Missing/null/wrong-type fields, unknown/duplicate fields at any depth, property
aliases, invalid UTF-8/NUL and blank prose refuse. Body is bounded to 64 KiB; only
the exact submit collection receives the 512 KiB escaped JSON envelope through
the frontend and private host wrappers. Cancellation keeps its 64 KiB bound.

Responses expose the same safe command-result fields as review commands. Native
proofs, receipt bytes, identities, namespace and digests stay private. The same
Product/Application Store and current host/actor bind delivery and cleanup.
Both private and public reply loss recover under the original capture after Store
reopen. Incomplete completion returns 202 with `finished:false`; replay cannot
repeat uncertain native completion. Session loss after primary withholds results.
The existing `native.repository.reviews.write` capability covers these operations.
Normal review/thread reads retain their owners. Admitted thread GET responses
have a bounded 2 MiB bridge allowance for full Markdown and canonical anchor/
observation metadata; command responses keep the ordinary limit. Real HTTP/
Unix/native/PostgreSQL checks qualify delivery and full-anchor read-back.
Installed shared inline UI and desktop HTTPS remain subsequent consumers.

`Feed.Handler` owns repository catalogue, combined feed, subscription and post
submit/detail/replies/retry routes over shared services. Hosts supply an authenticated canonical actor/
host, current repository/post ports and a mandatory `Current` callback for their
browser or private IPC admission. Request fields never select the caller.
`Current` also supplies that host's refusal response, preserving native session
errors without importing its session/SDK. The host owns channel/origin admission;
the shared handler is not an anonymous public server.

The handler rechecks current admission before returning reads and before saving
preferences. It retains existing JSON projections, exact decimal identities,
catalogue/query bounds, subscription narrowing and CAS conflicts. Subscription
writes accept one UTF-8 JSON object within 64 KiB, rejecting duplicate/trailing
JSON; a supplied subscriber never overrides the bound actor. Responses are
encoded before headers and use `no-store`. Missing admission refuses; unknown
routes and methods cannot reach service calls. Construction starts no listener,
native session, provider client, model call or work driver. Fountain now consumes
this transport; standalone hosts can consume it without separate handlers.

Post commands use `Commands` and the host-bound `AuthorName`, never body-supplied
attribution. Reads use `Posts`; both must reference the same store as `Service`.
Existing services retain source/parent resolution, exact submission lookup,
immutable author, revision/refusal rules and provider-owned capture. Submit and
retry check host admission again before writing and before returning a response.
Their JSON bodies are bounded to 64 KiB; retries retain strict typed fields and
oversized/invalid JSON errors. Missing or uncertain capture remains the recorded
context wait, without an implicit model call, native effect or workflow drive.
Retry POST requires a client `command_id`, canonical decimal-string `repository`,
explicit `expected_revision` and `body`. Its response is the ledger's immutable
`FeedPostRetryReceipt`, rather than a current pipeline projection. Current author
and visibility are checked for first dispatch and replay; only a newly committed
transition owns best-effort proof/context capture. Capture failures cannot mark a
newer revision as waiting. Pipeline execution remains background-owned.
`GET /api/factory/feed/posts/{id}/retry/{command}?repository=N` is read-only
recovery through the same current scope checks. Missing, unfinished and foreign
command identities do not trigger a retry or capture. The Fountain private proxy
permits this bounded route and its repository query; both hosts use this handler.
`Requests.Handler` owns private department and request catalogue/revision/history/
saved-review/overview/scope-comparison routes, opening an exact post product-change draft and the
planning/generation/exploration/status/walkthrough-review routes.
It consumes `service.Requests` and `service.Posts` with the same bound host/actor
and mandatory current-admission contract. Inputs cannot choose owner/time; the
existing store and service retain immutable revision replay, head CAS, private
ownership, current administration and exact source review. Reads recheck admission
before response and writes recheck it after decoding, before persistence. Post
review requires the same canonical store and starts no planning or generation.
Authority/visibility error presenters retain host-specific sanitized refusals;
missing presenters return unavailable, never success. Bodies use the shared
strict 64 KiB decoder. `CommandError` and `RequestReadError` own canonical command
status and private-not-found rendering for shared and remaining native handlers.
Construction starts no listener, session, model call or work driver.

`GET /api/factory/intents/{intentID}/revisions/{revisionID}` reads one exact saved
revision through current owner/account admission, independent of the current head
or history-page bound. It accepts no query parameters and returns private 404 for
unknown or foreign identities. The shared handler and Fountain private proxy
serve the same immutable record; lookup neither replays a save nor starts work.

Planning and exploration commands receive explicit `Planning` and `Exploration`
service ports with the same canonical store. Missing/mismatched command ports
refuse, and the existing services keep current write authority, exact revision,
reviewed-plan, funding and provider/runtime admission. An admitted planning POST
retains the existing detached operation context bounded to three minutes; its
recorded attempt remains discoverable if the browser disconnects. Other commands
retain request cancellation. Status creates a read-only exploration service
without a chooser or write callback, restoring only recorded advice. Generation
readback stays distinct from explicit accepted-direction generation. Missing
preparation or authority stays a wait/refusal; no read launches, funds or consults.

Generation POST uses the strict body `{ "revision": "d…" }` and accepts no query
parameters. Missing/invalid revision input returns 400; a different current head
returns 412 `stale_revision`. Admission is checked after decoding, and the exact
selected acceptance travels through service, recorded-plan selection and graph
entry. A delayed request cannot generate another acceptance. Owner privacy,
current write authority and the existing generation operation/receipt still
govern replay. The composer sends its captured accepted revision through this
same handler in both hosts. GET generation status remains read-only.

`GET /api/factory/intents/{intentID}/revisions/{revisionID}/generation` uses
`Requests.SavedGeneration` to recover the selected acceptance's durable operation
after later head changes. Current private ownership and admission still apply.
No row returns `{intent_id, revision_id, state: "not_requested"}`; unknown/foreign
or nonaccepted revisions return 404. It accepts no query, rechecks admission
before disclosure and starts no graph/model/work. Fountain's protected native
route and private proxy expose the same read without additional authority.

`GET /api/factory/intents/{intentID}/revisions/{revisionID}/plan` reads
`Requests.SavedPlanning` under current private owner/account admission, even after
acceptance or later head changes. It accepts no query, checks admission before
dispatch and disclosure, and returns no-store JSON. The captured operation,
assignment/run and reported outcome are recorded facts; no `launch_confirmed`
field infers process start. Missing attempts return scoped `none`, while
unknown/foreign/non-draft or misbound inputs return private 404. The handler never
consults, reviews, funds or launches. Native product routing and Fountain's private
proxy expose the same read. The shared composer consumes it through its scoped
pending-planning journal.

`GET /api/factory/intents/{intentID}/revisions/{revisionID}/exploration` uses
`Requests.SavedExploration` to read the atomic saved proposal/review from the
original input draft, even after head changes. It validates the stored transition,
owner and review bindings and returns `{intent,basis_revision,state:"recorded",result}`.
An ordinary save without a review is not a result. Missing transitions return
scoped `none`; this does not prove that consultation stopped or credit was not
consumed. Unknown/foreign/non-draft input returns private 404; malformed or
ambiguous records refuse. Queries are rejected, admission is rechecked before
no-store disclosure, and the read wires no chooser, funding or work. Native
product routes and the Fountain private proxy expose the same read. The shared
UI exploration journal remains its explicit consumer.

`Work.Handler` owns recorded issue/journey, assignment and priority-comparison
reads over `service.Issues` and `service.WorkReads`. Hosts bind canonical actor/
host and current admission, and supply resource visibility, acceptance status,
runtime membership and navigation through the existing service ports. Error
presenters retain sanitized provider failures without importing a session/SDK.
Mixed store ports refuse. Canonical numeric path IDs, bounded comparison cursors
and private scope-history query limits keep their existing contracts. Reads
recheck host admission before response, preserve incomplete/missing facts, and
create no runtime inventory, model consultation, authority, task or workflow.
The private request scope-comparison endpoint belongs to `Requests.Handler`
and retains owner admission and recorded mapping semantics after store reopen.

`Repositories.Handler` owns repository status/access, activity, policy, runtime
capacity/operator/environment grants, sponsorship and repair-allowance transport.
Hosts bind the canonical actor/host and a separate nonsecret command principal;
request bodies cannot choose attribution. Mutations require that principal and
current admission before persistence. Existing services retain current
administration, operator, environment and sponsorship custody checks, revision
CAS and durable repair-command replay. Status/access reads recheck admission
before response and grant no work. Concrete repository service ports must use
the selected canonical store; policy/runtime writers remain explicit host ports.
Visibility/authority and host-specific error presenters preserve sanitized
refusals without a native session dependency. Missing ports refuse. Commands use
the shared strict 64 KiB JSON decoder and responses use no-store. Fountain now
supplies its current ports through this owner; construction starts no listener,
runtime, model call or background work.

`Runs.Handler` owns recorded run-status and explicit repository/run intervention
routes. Hosts bind actor/host and a separate command principal, current account/
channel admission and canonical services over the selected store. Repository
commands require fresh visibility; services retain current execution authority,
exact run/repository scope and takeover destination admission. Reads grant no
execution. Commands recheck host admission after strict bounded JSON decoding,
and successful reads/commands recheck it before disclosure. A changed account
cannot select another actor or principal through the request. Missing admission,
principal or mismatched service stores refuse. Status rejects query parameters.
Pending/unconfirmed effects, revision/command conflicts and durable retry/context
outcomes retain their canonical responses; native errors use host presenters.
The coordinator remains the single workflow/journal owner. Fountain supplies its
current native ports through the same transport. Construction launches no work,
listener, process, recovery or model call.

`Output.Serve` owns the read-only WebSocket protocol over an already admitted
`service.OutputAttachment`. Hosts retain origin/private-channel validation,
upgrade and peer registration/draining. The attachment binds canonical actor,
repository, run and runtime incarnation; its immutable `RunID` also constrains
the handshake before any output read. Hosts must supply current account admission
and a handshake validator for their repository label and account generation.
The existing `repository_id`/`session_generation` wire fields are host labels,
not authority or an implicit canonical mapping; a native host binds its own
profile generation without fabricating a Fountain session.

The shared protocol retains a five-second/4 KiB strict JSON handshake, bounded
cursor, text/base64 frames, finite read-failure budget, status suppression,
heartbeat and terminal-byte drain before EOF. Missing attachment/admission/
handshake ports refuse. Current admission is checked before and after each
output read, and the service independently checks current resource access and
incarnation. Further viewer input closes with policy violation and never reaches
the source. Host cancellation closes the connection and joins its input reader;
construction/attachment never launches or resumes an agent or changes its run.
Fountain consumes this same protocol with native channel and generation ports.

`Records.Handler` owns library search/item/history and verified artifact downloads.
Hosts bind actor/host and mandatory current account/channel admission; the
existing `Library` and `Artifacts` services receive explicit source and current
resource-visibility ports. Library reads retain current/history filters, bounded
pages, exact revision projections and string repository IDs. Downloads retain
complete-upload, metadata/size/digest checks and owned verified bytes before
attachment, sandbox/nosniff/private-no-store and HTTP range handling. The handler
rechecks host admission before JSON or download headers/bytes. Missing admission
or service ports refuse. Known hidden/missing records stay private; unknown
visibility/storage and invalid retained content return sanitized unavailable
responses. Fountain supplies its native visibility ports through the same owner.
These reads start no work, upload completion, model call or process; worker MCP
record contracts remain with their existing owners.

`Acceptances.Handler` owns bounded acceptance previews and explicit admit/
withdraw commands over the existing service and coordinator journal. Hosts bind
actor/host, current channel/account admission and visibility/execution ports;
those authority ports must recheck the bound account as well as repository
permission after evidence reads and before persistence. Commands use the shared
strict 64 KiB decoder, and bodies cannot choose the approver. Preview queries
retain the 8 KiB/50 distinct canonical comment-ID bounds. Reads and commands
recheck host admission before successful responses. Exact decision replay,
predecessor conflicts, withdrawal latches and incomplete/unavailable evidence
retain their canonical outcomes. `AcceptanceDecisionError` also serves work
readback; there is one refusal renderer, with sanitized host authority errors.
No preview or construction dispatches work or replaces the journal.

Verification-allowance GET/POST belongs to `Requests.Handler`. Its existing
service retains private owner checks, current administration, exact repository
admission, command replay, increasing finite limits, usage and revision CAS.
POST requires a separate host-supplied `Principal`; bodies cannot select it.
Queries refuse, commands use bounded strict JSON, and host admission is checked
after decoding and before successful disclosure. Replays return current limits
and usage while keeping captured command attribution. These operations launch
no checks, runtime, work driver or model call.

`Workers.Handler` owns the fixed `/mcp` route, POST-only/browser-Origin rules,
capability-header and Bearer/HMAC authentication, admitted scope/state/expiry
checks, generic refusals and bounded request-body handoff. It retains a private
copy of the persistent key. Missing capability, admission or protocol ports
refuse; it never treats an active row alone as current runtime admission.

Hosts supply `CapabilitySource`, current `Admit` and `Protocol` functions. Native
composition can supply `service.WorkerAdmission.Prepare` directly, without an
appliance API/session, Project inventory or native callback headers. The existing
Forge adapter composes its current worker/controller admission and role-filtered
MCP SDK server through those same ports. Per-tool authorization, schemas, rate
limits, durable work and execution remain with their existing owners.

Protocol construction follows authentication and admission; its capability is
server-supplied and must retain the authenticated immutable scope. Errors expose
no source/configuration/credential details. Body limiting bounds what the protocol
can read; that protocol owns decoding and the response to an oversized body.
Listener permissions, private socket ownership, credential delivery/rotation,
process lifetime and installed native qualification belong to host composition.

Worker definitions and payload contracts live in
[mcpwire](../mcpwire/README.md); the shared role-filtered SDK server and tool
routing live in [mcpserver](../mcpserver/README.md). Hosts compose those owners
with their current runtime/service ports.
