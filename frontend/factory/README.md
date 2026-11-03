# Shared Factory UI

The [production diff renderer](review/README.md) now supplies the shared Lit
review primitive: immutable anchors and controlled drafts, local highlighting,
virtualized split/unified patches and safe thread annotations. It is qualified in
Fountain HTML/CSP and Linux Tauri/WebKitGTK; connected PR views/writes remain T08.

`feed/feed.ts` defines the production `<soda-factory-feed>` Lit element. Fountain,
Storybook and the Linux Tauri component workbench import this same source. A
change lands in each consumer when that consumer is rebuilt; installed desktop
applications receive bundled changes through app updates.

`app/factory-app.ts` defines the production `<soda-factory-app>` frame. It mounts
the existing Feed, request, issue list/detail, work overview/detail/plan, Knowledge and Watch
controls in light DOM. `app/routes.ts` defines canonical destinations; the host
owns their URLs, history, account selection and current admission. Set `context`
to a `FactoryAppContext` with the bound transport, canonical host and actor, stable
profile/provider/account scope, display account, selected route and typed
`resolve`/`navigate` ports. Each new context retires the previous view. Returned
ports must match the selected route, transport and relevant actor/repository/
issue/run scope. A mismatched binding renders unavailable before mounting a child.

The `issues` screen receives list read ports; the frame supplies selection through
its typed `issue` destination. `issue` mounts the shared description/conversation
and an optional matching `JourneyContext`. `work-detail` is the distinct Factory
workflow control screen. Issue ports must match the frame's exact host, actor,
account scope, transport and route. An optional journey must match the same
transport/repository/issue before either child mounts.

The detail element emits `factory-issue-read` display availability on read start,
successful issue/conversation validation and retirement. The frame starts the
optional Factory journey only after a matching successful read; unavailable,
foreign or malformed child facts clear and cancel it. This coordinates visible
read state and grants no server authority. External Markdown links retain the
host's resolver/opener, with captured frame and child cancellation. Seven
`Factory/Application` stories include the issue list, composed issue view and
shared new-issue form.

`app/browser.ts` is the standalone browser entry. Run
`bun run build:factory-ui --out /path/to/assets` (default `.artifacts/factory-ui`).
`scripts/build-factory-ui.ts` emits `factory-app.js`, `factory.css`, local role
portraits and font/Lit/Markdown-library notices. Fonts are embedded; no Storybook
or Forge page, provider, backend or account is needed to build these assets. The builder refuses
host/story source imports and host branding assets. Its public runtime exports
are `SodaFactoryApp`, `SodaFactoryIssues`, `SodaFactoryIssue`,
`SodaFactoryIssueComment`, `SodaFactoryIssueCreate`, `SodaFactoryIssueEditor` and
`SodaRequestError`;
hosts constructing request errors must use that exported constructor to share
the component bundle's identity.
Types export the same application context, screen, route and transport contracts.

`issues/issues.ts` defines the shared read-only `<soda-factory-issues>` list.
Supply `IssueListContext` with the current transport, canonical host/repository/
actor, stable account scope and `openIssue(ref, signal)` navigation port. The
host owns URLs/history and must honor cancellation before delayed navigation.
The component pages the existing collaboration issue API with open/closed/all
filters and twenty rows per page. It preserves exact decimal-string IDs and
opaque cursors, displays labels/assignees/timestamps, and renders provider text
literally. Null metadata stays unavailable rather than becoming an empty fact.
`api/issue-response.ts` owns the checked list projection of the canonical record;
it does not construct mutation capabilities or parse the unused description.

Context/account/repository replacement, disconnection and observed runtime
generation changes retire reads and pending navigation. Starting another page
or filter cancels earlier reads, and failed admission/readback clears old rows.
Hosts still own fresh account admission and must replace/retire the context when
that admission changes. Import `styles/issues.css` in the host's shared envelope;
standalone `styles/base.css` already includes it. Eight `Factory/Issues` stories
and the existing browser integration root consume this same production source.
Shared issue detail, Markdown/conversation, Factory facts and host route/history
joins complete the read journey in T07-READ; this list is not an installed connected issue journey.

`issues/detail.ts` defines `<soda-factory-issue>`, the read-only description and
paged ordinary conversation. `IssueDetailContext` binds the same host/repository/
actor/account scope plus the exact issue number. Its `resolveLink(href)` port
resolves provider-specific relative and fragment references; `openLink(url,
signal)` owns actual native/browser navigation and must honor retirement.
The component rechecks issue detail before each twenty-comment page, matches
the exact external parent and clears all old facts on failed readback. Refresh
returns to the first page. It has no mutation or execution controls.

`shared/markdown.ts` uses Marked and DOMPurify to produce sanitized DOM fragments
for descriptions and comments. Code, tables, lists, quotes and disabled task
checkboxes share one render path; provider scripts, styles, forms and event
attributes never become application markup. Link targets must resolve to HTTP(S)
or mailto without embedded credentials. Markdown images are attachment links;
authenticated media rendering remains a host integration concern. No description
or comment is silently truncated inside the bounded JSON read. Ten production
stories and the existing real Chromium root cover paging, exact scope, large
writing, access changes, untrusted content and cancelled reads/navigation.
Fountain supplies real host URLs/history and the optional ordinary comment port.
Further issue editing and connected/installed qualification remain their later
slices. The work-detail element is a separate Factory workflow view.

`issues/comment.ts` defines the shared ordinary issue-comment composer. Its
context binds canonical host/actor/repository, stable account scope and the exact
issue reference, with a fresh authenticated `commandScope` port. It rechecks
issue identity and the current comment capability before submitting. The shared
screen mounts it only when the primary issue read succeeds; Fountain supplies
the same scoped transport as its issue/facts. Standalone `FactoryScreen.issue`
can supply the optional `comment` ports without importing Fountain.

`issues/comment-record.ts` owns scoped draft/pending custody over the existing
browser storage layer. Original UTF-8 Markdown is bounded to 64 KiB for delivery,
with no input truncation. Draft and exact command/body/parent are saved before
submission; failed persistence prevents dispatch. Restore performs a read-only
lookup, while retry and cancellation require explicit clicks and preserve the
original command. A saved cancellation intent cannot become a submit retry.
Missing, unavailable, unfinished or contradictory outcomes retain pending writing;
only a bound terminal result settles it. A committed result refreshes the shared
discussion through normal reads. Cancellation and receipts preserve newer drafts;
if a cancelled original and a newer draft differ, both remain copyable until the
user explicitly discards the original. Failed writes retain their persisted
baseline across remounts and never overwrite a newer document's saved copy.
Unknown stored bytes stay blocked and untouched. No credential, native callback
token, installation ID or provider receipt is stored in these records.

Four `Factory/Issue comment` stories use this production component with declared
peers. The existing browser/native roots qualify actual Fountain submission,
lost-reply lookup, cancellation, original-command retry, scoped storage failure,
account/realm changes and retired replies. This is local source qualification;
installed desktop/provider connection and remaining editors are unfinished.

`issues/editor.ts` defines the shared ordinary title/description editor. Supply
`IssueEditorContext` with the exact issue reference and current command scope;
`FactoryScreen.issue` can supply optional `editor` ports without the parent,
which the shared screen derives from the successfully read primary issue.
Fountain supplies the same scoped transport/account as its discussion controls.
Port mismatches refuse before reads, and read loss/disposal clears the editor.
A committed edit refreshes the ordinary issue rather than locally inventing
updated content. Neither component nor browser records contain native authority.

The editor retains the original title, exact Markdown and UTF-8 body digest with
its draft. It saves only changed fields with the existing API's explicit original
content condition. No-op/invalid/oversized changes cannot submit; empty bodies
can clear a description. Inputs are never truncated. A fresh read catches stale
drafts before submit; known stale drafts disable Save and show current content.
Use current issue as base is an explicit choice that preserves the user's changed
fields and takes current values for untouched fields. Further server races still
face the captured condition and native revision/title/body CAS.

`issues/editor-record.ts` owns durable original/draft/pending custody over the
existing storage primitives. A pending command retains its exact source and
condition before dispatch. Failed persistence prevents POST; reopening performs
lookup only, while retry/cancel are explicit and preserve original identity and
field presence. Missing/uncertain/malformed/unfinished results retain pending
writing. Successful receipts clear only the matching original draft; newer
writing remains. Cancellation/no-effect preserves writing, including both an
original edit and a differing newer draft until explicitly chosen. Failed writes
retain their persisted baseline; unknown bytes remain blocked and untouched.
Five `Factory/Issue editor` stories and existing browser/native integration roots
cover the production element. Actual Fountain effects, declared browser storage/
account peers and emitted standalone composition are separate evidence; connected
GitHub desktop and installed application qualification remain later consumers.

`issues/create.ts` defines the same `<soda-factory-issue-create>` form in Fountain
and the standalone frame. Supply `IssueCreateContext` with the current canonical
host/actor/repository, stable account scope, transport, fresh `commandScope` and
typed `openIssue` navigation. `FactoryScreen.issue-new` receives these ports
without `openIssue`; the frame supplies exact typed `issue` navigation. The
Fountain host mounts the form at `/feed/work/issues/new` after its existing fresh
repository binding, and its issue-list navigation links to that route.

`issues/create-record.ts` keeps title/Markdown, draft revision and pending command
durably before submission. Titles are unpadded, 1–255 UTF-8 bytes; descriptions
may be empty and allow 64 KiB. Inputs are never silently truncated. Failed storage
prevents dispatch; restoration checks the original command without automatic
POST. Explicit retries/cancellation retain the same command and original writing,
and a saved cancellation cannot become a submit retry. Unknown/unfinished results,
malformed or foreign references and unreadable committed destinations retain the
pending command. The existing creation API supplies the exact authenticated,
currently readable issue; the UI never searches by title or predicts a number.

A finished readable creation is saved with its destination before navigation.
Only the matching original draft is cleared; newer writing remains. A failed
navigation leaves an explicit Open created issue action, and restoration of a
saved result does not create another issue. Write another issue explicitly
releases that completed destination while preserving newer writing. Terminal
no-effect with different newer writing keeps both versions until the user chooses
to discard the original. Unknown stored bytes remain untouched. Context/actor/
repository/generation retirement aborts requests/navigation and rejects late
results. Five `Factory/New issue` stories plus an application story consume this
production form. Real Fountain/native database/browser checks and declared storage/
account peers qualify the local source paths; connected/installed desktop and
remaining editors retain their own later gates.

The host still supplies its bootstrap, current authenticated ports, navigation,
optional terminal renderer/styles and native lifecycle. The module starts none
of those. Source consumers can import the defining modules directly; standalone
hosts load the emitted module and stylesheet. `scripts/factory-browser-assets.ts`
owns shared portraits/notices and native CSS scheme correction for both builds.
Standalone CSS retains canonical document tokens; Forge strips its extension's
duplicate root rules so its document remains authoritative.

The frame owns its `.soda-factory-root` envelope; import `styles/base.css`.
The optional `scheme` property selects `light`/`dark`, preserving mounted view
state; its default follows the document's supported system color scheme.
Admission loading, unavailable and disconnected states show no previous account
facts. Retirement cancels admission and explicitly clears child bindings, including
a child retired before it connects to the DOM. Request API calls and scoped work/
Watch ports retain the captured frame lifetime. Opening views issues no workflow
commands; Watch attachment remains an explicit read action. Library source
navigation uses a canonical Feed destination through the host.

Six `Factory/Application` stories import this frame directly with the existing
declared component peers. Browser evidence covers all nine view kinds, navigation,
draft restoration, account isolation, rejected/delayed admission, retirement and
theme switching. The production source closure contains no host or Storybook
imports. The Linux Tauri supervisor, private bridge, history and installed product
joins consume this entry in their own slices; a frame story is not a running
native backend or connected provider.

The component renders in light DOM. Import `styles/base.css` and put standalone
content inside `.soda-factory-root`, with `data-factory-scheme="light"` or `"dark"`.
Fountain imports `styles/feed.css` through its workspace stylesheet and owns its
document theme. Fonts, palette, tokens and portraits retain their canonical owners
under `assets/branding/`; do not copy their definitions into a desktop theme.

Set the element's `context` to a `FactoryFeedContext`:

- `api/transport.ts` supplies bounded API requests, asset base and runtime generation.
- `app/navigation.ts` supplies the repository catalogue, links, navigation and an
  optional appliance application catalogue. Repository keys are canonical Factory
  ledger IDs carried as positive decimal strings throughout the host, feed,
  subscription and post APIs. The full positive signed BIGINT range stays exact;
  numeric JSON repository keys are refused. Native issue/PR numbers, sequence
  positions and revisions keep their existing contracts.
- `host.draftScope` identifies the profile/provider/account without containing
  credentials. Changing this scope retires active context and isolates drafts.

`frontend/spaces/soda-factory-feed-host.ts` adapts Fountain catalogue, repository
URLs and navigation. Its asynchronous context builder resolves native repository
locators through authenticated discovery before supplying local IDs to the feed
or project-work models. Native URLs retain the current provider owner/name.
Draft scope uses the adopted host and canonical acting human, so a display-name
change or transport generation does not change that identity. Mounts abort
pending discovery when retired and ignore late results after a context switch.
Legacy draft keys are retained without an automatic migration.

That host owner also exposes `fountainRepositoryNavigation` for current canonical
repository navigation metadata, including the native configured default branch.
It validates scope/field representation and rejects cancelled or retired-generation
reads. An empty branch stays unknown; callers must not guess a default or treat
the value as a ref-existence or write-authority proof. Host adapters still compare
the returned account/host with their captured binding before building a view.

`frontend/spaces/soda-factory-issue-host.ts` performs that join for shared issue
list/detail contexts. It compares freshly read metadata with the captured
canonical host, actor, repository, native locator and name. Contexts capture a
stable `fountain:host:actor` scope and a transport bound to the place lifetime;
a composing frame and its Factory facts must use that same transport. Retirement
cancels pending reads and navigation, and refuses further reads. The router
supplies issue destinations, same-origin navigation and a separate external
web/mail opener, all honoring cancellation. These are read/navigation ports,
not operation capabilities.

Fountain issue/comment Markdown uses the native repository-relative URL base,
as in Fountain's `modules/markup/markdown/transform_link.go` without branch
context. Explicit `src/branch/...` destinations preserve their branch; an empty
configured default branch does not cause a guessed fallback. Fragment/query
references use the host's current issue destination. Attachments stay explicit
links through the shared sanitizer. Fountain's `soda-place-issues.ts` consumes
this adapter in the normal `/feed/work/issues` tree beneath the native header.
List/detail and Factory facts use the same production components as the standalone
frame, including `issues/screen.ts`'s current-primary-read availability gate.
Acceptance, context resolution and completion controls are preserved beneath
that gate; `?tab=factory` opens and scrolls to them. The route's host navigation
handles Back/deep links and body/comment heading jumps, with body-scoped heading
IDs and stable ordinary comment anchors. Original native issue links stay explicit;
recorded native work/activity links are validated before adopting shared issue
destinations. Shared pull routes currently offer their native counterpart.
Deployment, authenticated media and connected desktop qualification remain later
journeys; local route/browser evidence does not prove an installed connection.

The feed captures `transport.generation` at context admission. Replacing or
retiring the context and disconnecting abort its page/conversation/control requests
and host catalogue/application-origin reads, stop polling and clear the view. Generation
changes retire the binding even when the transport object is reused. Shared
request guards run before dispatch and after headers/JSON bodies; peers that
ignore cancellation cannot restore old facts, start a follow-up review write,
navigate or erase another account's writing. Optional `applicationOrigins`
receives the same cancellation signal; Fountain forwards it to inventory reads.
Retiring a UI request cannot cancel an already admitted server effect. An issued
post retains its exact scoped pending payload for explicit recovery, and retired
revision replies leave the saved writing intact. Hosts still own current account
admission and must retire/replace bindings on profile/account changes.
Changing feed filters changes the visible window without retiring an issued post.
Its acknowledgement/failure follows the captured account/runtime lifetime, so
filtering cannot strand the composer or discard newer writing. Access withdrawal,
account changes and runtime retirement still prevent old responses from settling it.

Revision saves and change-review handoffs also follow the captured account/runtime
lifetime across filter changes. `feed/feed-response.ts` owns `feedRevisionSource`
and `feedRevisionReceipt`: editable reads must match the requested post and exact
canonical repository, with a safe revision and body. Each revision POST supplies
a fresh command ID, canonical repository and expected revision. Retry receipts
must match that command, `post_id`, repository, expected revision and exact UTF-8
body digest, and report the next revision. The store's unchanged, already-applied
draft exception can retain its revision only with matching submitted/source body
and a matching recorded receipt. Mismatched replies keep writing instead of inferring
success. A conflict reread cannot change repository/post identity, and withdrawn
editability disables resubmission.

Each editor opening has its own identity, so an earlier save cannot settle or
rebase a replacement editor. Review handoffs validate source revision and intent
before writing/navigating; returned intent changes refuse navigation. This
qualifies live editor/handoff behavior. `feed/revision-record.ts` retains exact
pending revisions under host-admitted profile/provider/account, canonical
repository, post and author scope before dispatch. It stores the command, prior
revision, submitted body/base and writing snapshot separately from newer draft
text. Unknown/foreign/unreadable records refuse replacement. Failed writes retain
the exact intent in this document, but no effect is sent until persistence works.

Reload/remount never retries a revision automatically. **Recover saved edit** is
available even when the current post is no longer editable. **Check saved edit**
uses the shared read-only receipt endpoint; **Retry saved edit** explicitly sends
the original saved body/CAS/command, preserving newer writing. Only a matching
recorded receipt, including current canonical author and body digest, settles its
own pending record. A newer head cannot substitute for that receipt or be rebased
backwards. Changed/unknown records and unreadable/replaced draft storage survive
late replies. A failed removal may reappear after reload and is settled by reading
its original receipt. A newly minted command's explicit CAS refusal can release
that new attempt; uncertain saved commands retain their identity. Filter changes
preserve the issued lifetime; account/generation/access retirement blocks old
settlement. Full cross-document ownership and installed native restart remain
consumer qualification.

`feed/review-record.ts` retains a change-review handoff under the current
profile/provider/account, canonical repository, source post and reviewing actor.
The exact identity is its source revision, intent and accepted predecessor; the
backend already owns their atomic, idempotent draft binding. A fresh review is
sent only after this tuple persists. Failed writes remain recoverable within the
document without submitting. Reload never opens or retries a review automatically.
**Check saved change review** reads the existing source projection and requires
its recorded draft revision, exact retained tuple and current review permission
before settling/navigating. **Retry saved change review** explicitly reuses that
source revision; it cannot retarget another intent, predecessor or source version.
Unknown, foreign, replaced, unavailable or mismatched records remain saved.
Permission loss and retired lifetimes prevent navigation or settlement. A failed
removal can be settled after reload by reading the original draft receipt, with
no second write. Conversation writing is separate from this handoff metadata.

A current feed/conversation GET refusal (401/403), or a 401 response to an owned
command, clears cached feed/thread/source/revision facts and aborts that visibility
lifetime, including child previews. It also stops polling. Scoped drafts and exact
pending posts remain saved; writing stays editable while submission is disabled.
An explicit refresh must successfully load a fresh feed window before actions and
polling resume. A failed refresh cannot unblock commands, and old replies cannot
restore facts or revoke a newer visibility lifetime. Optional owner-only allowance
403s remain separate from readable feed access. The `Factory/Feed` stories
include expired-session and access-withdrawn examples; the latter withdraws access
on the next feed read after its initial recorded window. No story connects an
account or invokes a provider/model/worker.

All fifteen feed examples use `frontend/storybook/fixtures.ts`'s single declared
peer. Paging supplies an older opaque cursor/window; replies page in canonical
order and accept explicit replies. Busy submission waits for the story's
**Release declared response** control. Retry fails once before recording;
unknown submission records locally then loses the first reply. Explicit retry
keeps the exact body/key and reads back one declared post. The stale example uses
the actual edit/retry path: edit the recorded post, encounter a newer revision,
review its current text and save again. **Unknown revision** loses an edit reply
after recording its receipt; explicit receipt recovery preserves newer writing
without another mutation. **Unknown change review** loses the source-bound draft
reply and recovers it through an explicit read, without acceptance or another
review write. New-post creation has no revision CAS.
The peer's in-memory ledger is development data, not retained server/provider
qualification. Production elements are imported directly; no example copies UI.

`feed/feed-artifact.ts` defines the production bounded media preview. Cards and
conversation replies pass `FeedArtifactContext` containing transport, canonical
repository, captured generation and the parent lifetime signal. There is no bare
request callback. The descriptor's ID and exact canonical API hint must match that
repository before any GET. The component snapshots metadata, checks liveness after
headers/chunks/text decoding, cancels retired streams and revokes cached Blob URLs
on source/context changes, retirement and disconnection. Equal metadata/context
refreshes preserve the cache; hide/show makes no additional request. Hosts must
abort the lifetime when the account/profile is retired.

Downloads/previews keep the existing 8 MiB byte bound; text displays the first
64 KiB with an explicit truncation note. Reads have a 15-second deadline and
retry explicitly after failure. Images/audio/video use the existing allowed media
types; other bytes remain downloadable. Text stays escaped and download links use
only component-owned local Blob URLs. Eight `Factory/Artifact preview` stories use
declared bytes for text/image/binary/loading/offline/denied/size-limit/truncated
states. Browser checks qualify these lifetimes and both feed compositions;
installed/native and actual provider/artifact-transfer qualification remain later
consumer work.

Pending posts retain their submitted text, repository, parent, issue and
idempotency key across retries; newer writing stays a separate draft. A valid
scoped pending copy can recover a missing separately stored nonce after browser
reload. An unrecognized, numeric-repository, foreign or conflicting pending copy
is preserved and not submitted or replaced. Historical draft/action import is
still unfinished; this refusal does not qualify that upgrade path.

Failed browser-storage writes and removals remain in the shared draft helper for
the current document, keyed by the same host/profile/account scope. This keeps
drafts and command identity through component remounts and account switches.
When storage is unavailable, that fallback cannot survive closing/reloading the
document; it is not a replacement for durable desktop profile storage.

Other repository-place consumers still require their own native/local boundary
conversion; this adapter does not change the native mount context. Shared
production code must not import `frontend/spaces`,
native mount context, host inventory or provider SDKs. Their response contracts
now have defining owners in `api/`, shared by all current Fountain callers:

| Module | Owns |
| --- | --- |
| `request-response.ts` | Request revisions, proposals, reviews and consistency reads. |
| `run-response.ts` | Recorded run/status and command response contracts. |
| `output-response.ts` | Read-only output frames, cursors and close reasons. |
| `work-response.ts` | Repository/issue work, capabilities and intervention results. |
| `journey-response.ts` | Recorded work-journey state. |
| `progress-response.ts` | Request overview, issue plans and revision history. |
| `acceptance-response.ts` | Acceptance preview and decision responses. |
| `access-response.ts` | Current access, editability and execution usage projections. |
| `activity-response.ts` | Recorded activity and revisioned display preferences. |
| `library-response.ts` | Scoped library pages, item identity and revision history. |

These modules depend only on shared model, format and allowance helpers. Moving
them preserves their existing validation and public exports; no host re-export
or second implementation remains. Host place/routing and observation adapters still
live in `spaces`; their extraction and new shared issue/PR screens remain in the
[implementation backlog](../../docs/development/factory-products/implementation-tasks.md).

`request/request.ts` now defines the production `<soda-intent>` request composer,
with `request-view.ts` and `request-walkthrough.ts` providing its review rendering.
Fountain's request page and workspace setup import this same implementation.
Its existing Shadow DOM, scoped component styles, custom-element name and events
are preserved. Shared tokens/fonts come from the standalone envelope; no Fountain
document CSS or host module is needed.

Supply the exported `IntentAPI` callback through `.api`, a canonical actor string
through `.actor`, and a profile/provider/account identity through `.draftScope`.
The callback accepts the existing `/api/factory/...` path, optional command body
and abort signal, returning parsed JSON or raising `SodaRequestError` for an HTTP
refusal. The host owns authentication, current resource admission and credentials;
API injection is not permission. Keep the same API function for an unchanged
binding; replace it when the admitted API changes.

`.active` admits the whole request workflow, including repository confirmation
while its composer is hidden. Fountain's workspace/page picker keeps that workflow
active and uses a stable host API callback across normal renders. Deactivation,
blocked-state changes, API replacement, actor/profile changes, reset and disconnect
synchronously retire action/resume/poll/history requests. A different `.request`
address retires pending work; adopting the intent just saved by the current action
keeps that action's save → plan/generate chain admitted. Identical props leave a
live operation intact. Disconnected or inactive elements cannot dispatch.

Each API call checks admission before dispatch and after the result, with a
bounded signal joined to its caller and the current component lifetime. Old
results, failures, finalizers and deferred focus cannot repaint or publish an
event for another lifetime. Retired chained actions cannot continue through a
replacement API. Timer/history reads share that lifetime too. UI retirement does
not cancel an already admitted server effect.

Repository selection, routing and native runtime preparation remain host event
handlers. `request/draft.ts` owns the one current request-draft record and the
shelf/repair persistence types. It validates the entire record before applying
scalars, decisions, delivery approach, exact shelved tasks or dependency lineage.
The record includes stable profile/account scope, actor, intent/new-request
subject, base revision and repository. Restoration also matches the loaded head
and its idea identities. An invalid, foreign or stale record is preserved in
full; it cannot partly restore choices or silently replace the current draft.
Older records lacking verifiable ownership are preserved for historical-import
qualification rather than decoded through a compatibility path.

Before saving, the draft owner compares persisted bytes with the view's observed
baseline. A changed copy is preserved. Failed writes retain their original
baseline and unsent bytes within the document, including across component
remount; a cached failure cannot hide newer persisted writing. Unknown reads
never authorize overwriting storage. Explicit retry only retries local storage.
Conflict/stale notices can show the validated saved outcome and change notes in
read-only fields. Unverified foreign contents are not exposed there. Current
unpersisted writing carries a copy-before-leaving warning: document-local recovery
does not promise survival of closing/reloading when storage fails.

The existing draft key namespace has one defining decoder with no legacy alias
or automatic import. `request/save-record.ts` owns separate exact pending saves
under the stable profile/account/intent-or-new address. The production composer
captures the immutable revision body, authenticated host and both pre-save writing
and post-save shelf/repair metadata before POST. Fresh identity must match the
current canonical actor. Durable storage and Web Locks are required for a new
save; unknown/foreign/unreadable records refuse dispatch. The lock serializes the
short pending-record write across browser documents, not server work or general
draft editing.

Reload/remount offers **Check saved result** without replaying a command. The
lookup uses fresh host/account admission and compares every authored field,
including choices, task graph and effective outcomes, with the immutable saved
receipt. Only Go's omitted empty optional values are normalized; a changed body,
owner, invalid timestamp or unknown shape cannot confirm a save. A missing receipt
keeps the identity pending and permits an explicit **Retry original save** with
the original body. No new identity, exploration, planning or generation is chained
from recovery. Current head status alone never confirms the command.

Confirmation durably archives the exact post-save dependency recovery before
removing the matching pending record. Failed archive/removal and replacement
records stay pending. Archived metadata can restore only against the identical
saved revision. Later writing or a different current head is preserved; recovery
does not replace it with historical prose or decisions. For an absent save whose
expected predecessor has already been replaced, **Release obsolete save** freshly
rechecks identity, the advanced head and receipt absence after that observation
before removing only that pending record. It sends no server mutation. A missing
receipt without that head fence remains uncertain.

`request/generation-record.ts` owns exact pending work-creation commands under
the stable profile/account/intent address. It reuses the save owner's strict
`revisionInput` validator and the draft owner's complete writing decoder. The
composer captures the selected accepted revision and writing, freshly checks
the canonical actor/host and privately reads that revision's existing operation
before durable retention. A missing operation uses the existing deterministic
generation identity; an existing ordinary or protected local operation retains
its original identity. Web Locks serialize initial record admission across
documents. Unavailable storage/coordination or unknown/foreign records send no
new POST.

**Check work creation** explicitly reads that exact acceptance's recorded outcome
through current host/account admission, even after a later head is loaded. It
checks intent/revision/operation, native actor/revision and authorization/digest
facts separately from the lenient status projection. Only `completed`/`refused`
settle a command; `pending`/`committed`, explicit `not_requested` and unknown
states preserve uncertainty. Confirmation archives the captured writing before
removing only the matching pending record. Storage failures or intervening
replacement bytes keep it pending. Historical confirmation never replaces
current prose, decisions, shelf, limits or the displayed revision, and never
publishes an acceptance event.

**Resume original work creation** is an explicit same-revision POST with current
write admission. It cannot select a newer acceptance. **Release superseded
request** reads an advanced head and then operation absence before clearing only
that local command; the server's durable-before-effect/head fence excludes a
late creation. Checking, remount/reload, status polling and release never dispatch
work. Failed/mismatched/retired replies preserve the record and later writing.
Read-only original prose/notes remain available while recovery is pending.

`request/planning-record.ts` owns the exact saved draft, captured planning
operation, host and complete writing before dispatch. Short Web Locks serialize
initial admission across documents; unavailable storage/coordination and unknown
or foreign records refuse a new command. Observed assignment/run IDs and the
POST's launch confirmation are bound before a follow-up read. If that write
fails, the current document retains those IDs through component remounts and
shows a copy-before-closing warning; it cannot promise reload durability.

**Check planning result** freshly checks host/account identity and reads the
original draft's private historical receipt. Operation and any observed
assignment/run must match; unknown states, review payloads and fabricated launch
confirmation cannot settle it. A recorded attempt is archived with its complete
writing and launch knowledge before removing only the matching pending record.
It confirms admission, not native process start. Current prose, decisions,
shelf, dirty state and displayed head stay untouched, with no acceptance/save
event or chained work. Polling and pending-state wording make no start claim.

A missing receipt remains uncertain. **Resume original planning** explicitly
submits the captured revision/operation only when the head still matches and no
run identity has been observed; changing the current operation picker does not
change this command. **Release superseded planning** rechecks the advanced head
and then absence before clearing that local record. Known run identities with
missing backend records remain pending. New planning, saves and acceptance cannot
silently bypass an unresolved command. Archive/removal failures and intervening
newer bytes preserve it.

`request/exploration-record.ts` owns exact pending exploration under stable
profile/account/intent scope. Its existing server identity is the original saved
draft revision; no new command field is invented. The composer freshly checks
host/account identity and durably retains the exact input and complete writing
before POST, using short Web Locks admission. Unknown/foreign/unreadable records
and unavailable coordination refuse a new exploration. Pending exploration also
blocks successor saves, planning and acceptance until explicitly resolved.

**Check exploration result** reads the private original-input receipt through
fresh current host/account admission. It validates intent/input/result,
predecessor, owner, repository, objective/limits and recorded advice bindings
before rendering. Unknown states, foreign owners, malformed result input and
stale content/advice cannot confirm it. A recorded result archives its captured
writing and proposal/advice before clearing only the matching pending command.
The unchanged view may adopt the proposal when the current head matches the
result; later writing, dirty decisions, shelf and a different displayed head
stay intact. Recovery publishes no save/acceptance event and chains no planning
or work. Existing draft recovery preserves later writing after reload when the
server already advanced to the proposal.

Absence remains uncertain: consultation may finish and credit may already have
been consumed. **Resume original exploration** explicitly posts only the saved
input revision, never the current edits or a replacement save. **Release
superseded exploration** reads the advanced head before rechecking absence, then
clears only its local record. The server's head CAS prevents that input from
later saving a proposal; this does not cancel consultation or refund funding.
Known validation/judgment-unavailable/funding refusals archive writing without
claiming a result. Unclassified transport failures stay pending. A failed
archive/removal keeps recovery visible; an exact writing-only archive can gain
the result of an explicitly resumed request without replacing foreign bytes.

Full
multi-document serialization across other records, historical import and installed profile
persistence keep their UI/upgrade/native owners. The sequential real-browser
checks cover two documents and an intervening newer write, including after a
failed draft write. Save, generation and planning admission additionally cover simultaneous
initial POSTs from two documents; this does not qualify other record owners.

Storybook's thirteen `Factory/Request` examples import the production element for
new/saved, loading, denied, offline, stale-save, inactive, restored local draft
older local draft, unconfirmed-save, unconfirmed-work-creation, unconfirmed-planning
and unconfirmed-exploration views. Responses and seeded local records are declared
fixtures. Unsupported actions remain unavailable; stories never connect an
account, contact a provider, run a model or start a worker.

`work/journey.ts` defines the production `<soda-factory-work-journey>` recorded
history element, with `journey-view.ts` supplying its projection and rendering.
Fountain and `Factory/Work journey` stories import this same light-DOM element.
Supply `JourneyContext` with a shared `FactoryTransport`, canonical repository
and issue strings, and optional `FactoryWorkLinks` issue/pull callbacks. The
component validates numbers before invoking these callbacks; evidence hashes
remain text. Fountain's `frontend/spaces/soda-factory-links.ts` owns its URL
construction. Desktop hosts supply their own destinations.

The transport generation identifies the admitted runtime/account binding. Hosts
must retire or replace that binding on an account/profile change. Retiring the
context or disconnecting aborts the read and clears history; late results and
changed generations cannot repopulate it. Replacing only navigation callbacks
reprojects recorded links without another read. A transient refresh failure keeps
explicitly labelled last-loaded history; a 401/403 clears it. Five declared stories
cover recorded, empty, loading, unavailable and denied reads without connecting
an account or starting a provider, model or worker.

`work/acceptance.ts` and `work/completion-retry.ts` define the production
requirements-acceptance and completion-review retry controls. Fountain's three
current composition hosts import these same light-DOM elements. Their exported
`RequirementsAcceptanceContext` and `RetryContext` accept shared transport and
canonical repository/issue strings. Acceptance optionally receives
`FactoryAcceptanceLinks` callbacks for the issue and its comments. Fountain owns
its comment fragment syntax in `soda-factory-links.ts`; other hosts supply their
own destinations. Replacing only these callbacks preserves reviewed choices and
pending decisions without another authority read.

Both controls bind reads and commands to a captured transport generation, abort
their UI lifetime on retirement/disconnection, and refuse late facts or events
from a retired binding. Hosts must retire/replace the context on an account or
profile change. Aborting a browser request does not cancel a server effect.
Acceptance and completion-review retry both persist their exact decisions before
dispatch and recover across browser remount/reload, as described below. A first
known pre-admission stale acceptance refusal still requires a fresh review.
Native restart and historical import remain unqualified.
Authority remains a server decision; controls disable unavailable actions and
do not acquire credentials or invoke a provider themselves.

Seven `Factory/Requirements acceptance` and six `Factory/Completion retry` stories
use declared peers for ready, denied, offline, loading and interactive lost-reply
states, plus stale acceptance evidence and recovered acceptance/completion retries.
They import production elements; no
backend/provider/worker/model or account connection runs.

`work/context-resolution.ts` defines the production context-choice control used
by Fountain and ten `Factory/Context choice` stories. `ResolutionContext` accepts
shared transport, canonical repository/issue strings, an optional direct run and
the host's `commandScope(signal)` callback. The callback returns a stable
profile/provider/account reference, never credentials or a transport generation.
Missing or unavailable identity leaves reads visible and actions disabled.
Fountain supplies its authenticated command scope; native composition supplies its
own profile/account binding through the same port.

Direct run props take precedence over issue lookup and do not trigger an initial
run read. Repeated bindings do not reread. `refresh()` explicitly rechecks either
the selected run or the issue's latest run, including canonical wire run ID,
repository and issue. Refreshed direct data updates the selection immediately;
unchanged selections preserve an active action, while changed selections retire
its UI. Issue refresh remains available during a pending action.

Before a new choice POST, the control fresh-reads the exact captured run,
verifies canonical scope, eligibility and the three selection digests, then
re-resolves host identity. Changed facts refuse a new choice until explicit
refresh. `work/context-record.ts` retains its exact run and three digests under
stable profile/provider/account, repository and issue scope before dispatch.
It preserves distinct same-run selections and other pending runs in a bounded
collection. Direct-run controls display their run's pending choices; issue
controls also retain earlier requests when the issue's latest run changes.

Restoration reads saved choices without dispatching or confirming anything.
An explicit saved retry fresh-reads the original run's canonical scope and
rechecks host account identity, then submits only the retained tuple. Its current
selection or eligibility may have changed: the server owns principal-bound
semantic replay and current authorization. A current `serve` projection alone
does not confirm the original choice. Only a matching POST receipt clears its
exact saved tuple; other unknown choices remain. Changed or unreadable stored
bytes are preserved and block a new write. A missing original record prevents
its delayed reply from clearing a replacement.

Storage failures before dispatch send no POST; the shared draft owner retains
failed writes within the document so a remount can retry the same tuple. Failed
removal remains visible, and reload can explicitly check the same saved request.
This browser evidence covers localStorage reload and declared response handling;
full multi-document ownership, historical record import and installed persistence
remain with the broader UI/upgrade/native consumers.

Identity, reads and action requests have bounded cancellation signals. Binding,
generation changes and disconnection abort their UI lifetime; guards refuse
late results, events and acknowledgement from peers that ignore cancellation.
Repeated identical props preserve an active action. Changed same-ID selections
retire its UI while preserving the saved request. Issue refresh still works
while a POST is pending. Hosts must retire or replace the context when an
account/profile changes. UI cancellation does not cancel a server effect.
Ten production-source stories include unavailable identity and a recovered
choice. Declared peers qualify browser behaviour only; native account connection
and runtime execution remain with their product consumers.

`work/work-view.ts` and `work/progress-view.ts` now own the shared work-detail,
intervention, overview, planning, direction, permission and revision-history
models/renderers. Fountain's existing Work elements and fourteen
`Factory/Work presentation` stories import these defining functions. Their
production closure has no transport or host import. Navigation-bearing builders
accept `FactoryWorkLinks` callbacks; positive decimal issue/PR numbers are checked
before callbacks run. A host may omit destinations, in which case links remain
text. Shared detail presentation never uses response URL hints to bypass that port.
Fountain's `soda-factory-links.ts` explicitly adopts its decoded recorded URLs
for detail views, retaining its current preference over path-derived destinations.

The workspace's `planContextFromScope`, `detailContextFromScope`,
`revisionContextFromScope` and `DetailScopeRuns` now live in
`frontend/spaces/soda-factory-work-scope.ts`: they assemble Fountain mounts and
native watch targets. Shared overview scope records/producers remain in
`work/progress-view.ts`. Presentation stories use declared facts and disabled
intervention adapters; they run no requests or commands. Runtime controls and
durable recovery are defined by the shared owners below; installed consumers
remain separate.

`work/progress.ts` now defines the production overview, plan and direction/history
elements. `WorkOverviewContext`, `WorkPlanContext` and `DirectionRevisionContext`
accept shared transport and canonical read keys; navigation comes from optional
host callbacks. `work/read.ts` owns bounded JSON reads and their error contracts,
also used by the retained Fountain detail component. Fountain workspace setup and
mount functions adapt paths through `soda-factory-work-scope.ts` instead of passing
native mount context into shared implementation code.

Changing resource/transport/generation or disconnecting aborts reads, clears the
view and retires overview child scope. Late responses cannot publish a scope or
start later plan/history reads. Hosts must retire/replace bindings when an account
or profile changes. Navigation-only replacements reproject retained records
without rereading. Authoritative request overviews must match the expected
canonical repository and never fall back to composed reads after failure.
Transient overview failures label retained work; 401/403 clears it. Plans keep
per-issue read failures distinct, while direction/history preserve missing versus
unavailable states. Nine `Factory/Work progress` stories use declared read peers.
They exercise production elements without provider/model/worker/login calls.
Runtime states and durable pending recovery are supplied by the corresponding
shared owners below; installed journeys remain separate.

`work/detail.ts` now defines the production Work detail and intervention element.
`WorkDetailContext` supplies shared transport, canonical resource/run keys, upfront
capability gates, optional issue/PR and acceptance navigation, and an optional
`detailLinks` callback. Fountain's scope adapter explicitly adopts its decoded
recorded URLs through that callback; desktop hosts supply their own routes. Link
replacement preserves the binding and pending retry without rereading. The shared
control imports no Fountain mount, path builder, terminal or Watch implementation.

Interventions and Watch stay unavailable until the current issue capabilities
load. A failed detail read clears facts/child contexts and does not initiate run
reads. Retirement/disconnection aborts reads/actions and rejects late UI results;
generation is also checked before submission and Watch events. Unknown run-retry
outcomes retain the exact command and run target, including navigation rebinding,
refresh, remount and browser reload through `work/retry-record.ts`. Request
cancellation never claims to cancel a server effect.
Watch emits the existing observe-only event for the host to handle; it performs
no work mutation. Seven `Factory/Work detail` stories use declared peers, including
a saved retry recovered without automatic submission; unsupported
actions refuse without backend/provider/model/worker/login calls. Shared
`factoryRunID` and `statusText` now own run identity validation and status copy;
native terminal ownership remains separate. Scoped pending-command recovery uses
the shared owners described here; connected/native qualification remains with
their product consumer slices.

Run retry requires `WorkDetailContext.commandScope`: a host-admitted stable
profile/provider-instance/account reference, never a credential. Fountain resolves
its canonical host and acting human through authenticated identity reads. Desktop
hosts must include their profile identity. The callback runs during recovery and
again before a retry; hosts must retire the transport binding on account/profile
changes. Runtime generations are not persistence keys. Records also bind exact
canonical repository/issue strings and the captured run/command IDs.

The command reaches browser persistent storage before its POST. Reading storage
must distinguish absent from unavailable; unrecognized, foreign or conflicting
bytes are preserved and block replacement. Failed writes retain the chosen ID in
the document, but send no retry until persistence succeeds. Recovery never posts
automatically. Replaying after a 4xx preserves uncertainty about an earlier effect;
only the matching recorded retry receipt settles the captured record. A late
receipt cannot remove a replacement command. An older saved run outside the
current list requires a fresh matching repository/issue read before replay.
Browser reload/remount and account isolation are qualified; native profile storage,
process-crash recovery, multi-document coordination and historical import remain
consumer requirements.
`shared/drafts.ts` retains its existing document fallback behavior;
`inspectStored` and the write result expose availability/persistence to run retry
without changing existing feed/request draft semantics.

Completion retry uses the same `commandScope` contract on `RetryContext`.
`work/completion-retry-record.ts` retains its exact command, completion event and
prior cycle under a separate account/repository/issue key. Persistence must
succeed before dispatch. Reload/remount offers an explicit replay of that saved
request, including when a newer gate replaces it or bounded history omits it;
the server's command journal and current authority decide replay eligibility.
Recovery never targets the newer gate or submits automatically. Malformed/foreign
records, unavailable storage, refused replays and mismatched receipts preserve
uncertainty. Only a receipt matching repository, command, event, prior cycle,
successor cycle and queued status settles the issued record. Late receipts cannot
erase a replacement or publish into a retired binding. Fountain's three hosts
share `fountainCommandScope`; credentials remain host-owned. This qualifies
declared-peer browser recovery, not native persistence or an executed server effect.

Acceptance uses that same `commandScope` contract on `RequirementsAcceptanceContext`.
`work/acceptance-record.ts` owns the exact pending decision body: predecessor,
title/body digests and versions, title occurrence, native revision, selected
requirements/resolution comments and prerequisite outcomes/accepted revisions.
It validates bounded complete records and preserves unsupported/foreign bytes.
The decision must reach persistent storage before POST; failed writes retain
the selected ID in this document without sending it. Recovery only offers an
explicit repeat and cannot approve a newer preview. Current account identity is
resolved again immediately before dispatch. Matching recorded decision ID,
valid current head and positive chain depth are required before settlement;
a newer current head is allowed by the existing server replay contract. Late
receipts cannot erase a replacement or publish into a retired context/generation.

After an unknown outcome or recovery, a refusal preserves the saved
request. For the first known attempt only, typed evidence-staleness refusals that
precede server persistence settle the exact refused request and require a fresh
review. This retains current stale-review behavior without treating a later 4xx
as proof that the earlier decision never applied. Failed storage removal leaves
the document tombstone; a reload can replay the old durable decision ID safely.
Browser evidence uses declared peers; native profile persistence, process-crash
recovery, multi-document ownership and historical import remain unqualified.

`work/verification-retry.ts` defines the production application-verification retry,
repair request and recorded repair-progress element. Fountain's Project Work and
App place both import this shared module directly; App no longer loads Project
Work to register it. `VerificationRetryContext` supplies shared transport, a canonical
repository string and up to 64 distinct recorded issue-zero run IDs. Hosts own
discovery/account/profile admission and must retire the binding on identity changes.
The seven-source production closure imports no host code or native terminal types.

Reads, actions and progress polling bind to a captured transport generation and
abort on retirement/disconnection. Late results cannot repopulate the view.
Reads require matching run/repository and absent issue scope; progress binds the
failed run, cause and cycle. Failed refreshes label last-loaded facts and disable
their actions; denied reads clear them. Retry confirmations require the exact
command/prior run and queued status. Repair acknowledgment requires its run,
valid cause/cycle and queued status; it never means the application is repaired.
Unknown replies keep the exact pending request within this live binding, including
unchanged context and refresh, and recover across remount/reload as described below.
Nonterminal repair states poll only recorded GETs;
Attention/adopted/stale states stop polling. Thirteen production-source stories
include recovered retry/repair and use declared peers that exercise no
backend/provider/model/worker or login.

Verification mutation also requires `VerificationRetryContext.commandScope`, the
same stable host-admitted profile/provider/account reference used by the other
decision controls. `work/verification-record.ts` retains exact repository, run,
action and retry command ID under that scope. Repair uses the existing run-bound
idempotent request with no invented command field. The record reaches durable
storage before POST. Failed writes keep the chosen identity in the document
without sending an effect; unavailable/malformed/foreign/conflicting records
remain preserved. Scope is resolved again before dispatch, and an account or
saved-request change requires review/refresh before a new explicit click.

Recovery never submits automatically or changes the saved run/action. A saved run
outside the current host list receives a fresh canonical repository/run/issue-zero
read before replay; unknown or foreign scope cannot authorize it. Both Fountain
hosts supply authenticated scope, and App keeps the control active with an empty
run list so saved requests can recover. Refused replays remain uncertain. Only a
matching receipt settles its exact record; late replies cannot erase replacements
or publish after retirement. Failed removal retains the document tombstone and a
reload can safely replay the original durable request. The nine-source closure
imports no host code. Native profile/process-crash recovery, cross-document
ownership, historical import and actual provider effects remain unqualified.

`work/verification-allowance.ts` defines the shared verification-limit control;
Project Work and request places import it directly. The response/command types
and decoder live in `api/verification-allowance.ts`, and exact pending increases
live in `work/allowance-record.ts`. The eight-source closure has no host imports.
`VerificationAllowanceContext` supplies shared transport, canonical repository,
intent ID and the stable admitted `commandScope` reference. Hosts retire it on
account/profile changes. Before an increase, the control resolves identity and
checks the current private request still names that intent/repository.

An increase persists exact command ID, expected revision and cumulative limits
before POST, and recovers across remount/reload without submitting automatically.
Unavailable/malformed/foreign/conflicting storage is preserved; failed writes
retain the chosen change in the document without dispatch. Unknown outcomes and
recovered refusals retain the same request. Only the first known CAS conflict
settles a refused change and requires a fresh read. A matching recorded command
receipt is required to settle a POST; its current totals may exceed the requested
totals after a later increase. A fresh read naming that exact command as current
head with matching totals can also confirm it without another POST. Late replies
cannot erase replacements or publish after retirement. Failed removal retains
the document tombstone. Reads/actions abort with binding/generation retirement;
failed reads disable increases and denied reads clear private facts. Increasing
limits preserves usage and never starts verification. Eight declared stories
include read-only, loading, offline, denied, lost reply, recovery and stale revision.
Native persistence, cross-document ownership, historical import and real effects
remain unqualified by these browser peers.

`work/repair-funding.ts` defines the shared repository repair grant/revoke and
charge/episode history control, with contracts in `api/repair-response.ts` and
exact pending storage in `work/repair-record.ts`. Both Fountain settings surfaces
pass only shared transport, canonical repository and admitted `commandScope`.
The seven-source production closure imports no host code. Grants keep finite
limits and explicit increase effects; withdrawal never refunds recorded charges.

Changes persist command, expected revision, limits, active flag and exact effect
before PUT. Account/profile/repository scope isolates recovery. Failed writes
retain the chosen request in the document without dispatch; unreadable/malformed/
foreign/conflicting records remain preserved. Reload/remount never sends a grant
or withdrawal automatically. Unknown and recovered refusals keep their command;
only a first known CAS refusal requires a fresh review instead. A matching command
receipt at the captured revision plus one settles a write, including replay after
a newer head. An exact current-head read can also confirm matching limits, active
flag and effect without another PUT. Late replies cannot erase replacements or
publish after retirement. A known write stays confirmed when its following history
read fails; refresh retrieves history without resending it. Failed removal retains
the document tombstone. Reads/actions abort on binding/generation retirement;
denied reads clear private history, and retired 401 replies emit no session-invalid
event. Nine production stories use declared peers. Browser recovery is qualified;
native persistence, cross-document ownership, historical import and real effects
remain consumer work.

`work/activity.ts` defines the shared recorded activity and display-preference
control. `ActivityContext` takes shared transport, a canonical repository string,
an explicit current `editable` capability, optional admitted `commandScope` and
optional `presentItem` navigation callback. Without that callback, facts remain
plain text. Fountain validates exact native issue/PR links in
`spaces/soda-factory-links.ts`; other hosts derive their own destinations from
the same recorded facts. Replacing navigation callbacks preserves unsaved choices
without another read. Source observation, last-confirmed receipt time, unknown
timestamps and truncated history remain distinct.

`work/activity-draft.ts` retains the exact expected revision and three display
settings under account/profile/repository scope. Choices persist before PUT;
remount/reload never writes automatically. Missing identity, unavailable storage
or current readonly capability disables submission. Malformed/foreign records
remain preserved. An unknown reply retains the submitted selection; a newer
matching current-head read can confirm the preferences without another PUT.
Revision conflicts require explicit refresh, review against current activity and
save. The API has revision CAS, not a command journal. Retirement/disconnection
aborts reads/actions, and generation guards reject late responses. Denied reads
clear private facts; unavailable reads label cached facts and disable saving.
Ten production stories use declared peers, including plain-text navigation,
recovered preferences and partial history. Native persistence, cross-document
coordination, historical import and real server effects remain unqualified.

`library/library.ts` defines the shared knowledge/decision browser. Fountain's
Knowledge place and eight `Factory/Knowledge library` stories import this same
light-DOM element and shared styles. Supply `LibraryContext` with shared transport
and a canonical repository string; `.itemID` optionally selects an item and
`.pendingSource` disables an in-flight source-post open. `library-source-open`
carries typed `LibrarySource` repository/post/item/revision facts for host
navigation. It never constructs provider routes. `library-navigate` lets a host
retire an older pending source open. Hosts retain current account admission and
retire/replace the binding on account/profile changes.

Search, item type, removed history, cursor paging, related items and revision
contents retain one implementation. Response parsing checks canonical repository
strings, requested item identity, ordered pages and descending bounded revision
history. Superseding navigation aborts the previous read; context/generation
retirement and disconnection reject late results. A queued source click from
old DOM cannot navigate under a replacement binding. Same-binding context updates
preserve filters without rereading. Denied/missing reads clear private facts;
failed detail refresh or paging labels retained facts, and explicit retry keeps
the selected item. All requests are bounded GETs. Stories use declared peers;
connected/native provider behavior remains a later consumer requirement.

`watch/watch.ts` now defines the production read-only Watch element and mount
owner, with the defining view/display/request/screen helpers in the same package.
The eleven-source production closure imports only shared Factory modules, Lit
and xterm types. Supply immutable `FactoryWatchContext` and a renderer loader.
`ports.ts` exposes only output rendering methods, never keyboard/input callbacks.
Its `source` port supplies canonical repository discovery, an admitted output
peer and the host's attachment payload. Fountain's `sodaspaces-factory.ts` owns
native repository/custody validation, vendor loading, socket construction and
resume-validation adaptation. A desktop host can supply IPC-backed ports without
Fountain paths or credentials in shared components. Injection is not authority;
the host/server must admit the current account and run.

Watching never starts/resumes a run or sends terminal input/resize frames. It
preserves cursor and renderer during bounded reconnects, retains finished-run
details, and closes only the view on stop/retirement. Hiding/suspend/resume retains
the stream without reconnecting. The configured transport generation is captured
once; a changed binding or denied read clears private output and refuses late
reads/frames, reconnects and attachment. Hosts own conceal/revalidation/retirement;
there are no page-event lifecycle guesses. Common output geometry now lives in
`styles/output.css`, imported by both Fountain terminal CSS and the standalone
envelope. Renderer hosts also supply their xterm CSS; Storybook imports the
installed package's CSS. Seven `Factory/Watch` stories use the real xterm renderer
and declared read/output peers, without a backend/provider/model/worker/login.
These checks do not qualify installed Tauri, connected accounts or native output
admission; those remain consumer requirements alongside durable pending recovery.

`api/collaboration.ts` mirrors the [canonical read records](../../internal/collaboration/records.md)
for the upcoming issue/PR views. `api/collaboration-response.ts` owns the shared
diff/anchor decoders: IDs remain strings and original immutable anchors are kept
separate from a provider's later relocation. The existing Fountain API has not
yet been cut over to these records; capability facts are never mutation authority.

```sh
bun run storybook        # browser workbench at http://127.0.0.1:6006
bun run build:storybook  # local static workbench
bun run dev:desktop-ui  # bundled stories in a Linux Tauri window
bun run check:desktop-ui # native light/dark rendering check
```

The [native workbench](../../tools/factory-ui/README.md) needs GTK3/WebKitGTK 4.1
development libraries and a graphical session. It uses development fixtures and
does not provide the standalone runtime, agent execution or account connections.
Storybook output is excluded from production appliance assets.

When building with Vite, preserve native `light-dark()` CSS as configured in
`frontend/storybook/main.ts`. Lowering it to inherited guard variables lost root
token values in the actual WebKit window. Validate the built native assets after
changing the theme or CSS compilation target.
