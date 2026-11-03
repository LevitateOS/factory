# Target defining tree and cutovers

The product was extracted to `LevitateOS/factory` on 2026-11-03. Current defining
Go owners are `factory/pkg/{factory,store,application,collaboration,github,identity,project,strictjson,filelock}`;
the frontend and native Rust owners keep their relative paths in Factory. The
diagram below retains the original in-repository allocation and unfinished
consumer targets. Appliance/Fountain files stay in their owning repositories.
The initial extraction snapshots remain in Git history; recovery patches and
inactive archived coordinator tests are retired from the working tree.
See [source provenance](../../source-import.json) and the
[current extraction boundary](ideal-filetree-plan.md#separate-public-repository).

This is the target delta for the [product extraction](ideal-filetree-plan.md),
not a replacement listing of all 2,697 repository files. Existing unlisted leaves
retain their current owner. The [source map](seams-and-source-map.md) allocates
mixed-file duties; tasks name the full import-closure handoff before a move.
No directory below implies another daemon, database schema or public SDK.

```text
sodaos/
├── assets/
│   └── branding/theme/tokens.css
├── cmd/
│   ├── soda-factory-server/
│   │   ├── main.go
│   │   ├── compose_linux.go
│   │   ├── compose_darwin.go
│   │   └── README.md
│   └── soda-desktop/
│       ├── Cargo.toml
│       ├── build.rs
│       ├── tauri.conf.json
│       ├── capabilities/
│       │   └── main.json
│       └── src/
│           ├── main.rs
│           └── commands.rs
├── internal/
│   ├── collaboration/
│   │   ├── connection.go
│   │   ├── identity.go
│   │   ├── repository.go
│   │   ├── issue.go
│   │   ├── pull_request.go
│   │   ├── review.go
│   │   ├── checks.go
│   │   ├── capabilities.go
│   │   ├── observations.go
│   │   └── operations.go
│   ├── factory/
│   │   ├── access/
│   │   │   ├── actor.go
│   │   │   └── grants.go
│   │   ├── execution/
│   │   │   ├── provenance.go
│   │   │   └── client.go
│   │   ├── service/
│   │   │   ├── feed.go
│   │   │   ├── posts.go
│   │   │   ├── requests.go
│   │   │   ├── operations.go
│   │   │   ├── repositories.go
│   │   │   ├── issues.go
│   │   │   ├── pulls.go
│   │   │   ├── intake.go
│   │   │   └── placement.go
│   │   ├── httpapi/
│   │   │   ├── handler.go
│   │   │   ├── admission.go
│   │   │   └── streams.go
│   │   └── runtime/
│   │       ├── native_config.go
│   │       ├── native_config_linux.go
│   │       ├── native_config_other.go
│   │       ├── server.go
│   │       └── shutdown.go
│   ├── forgejo/
│   │   ├── collaboration.go
│   │   ├── collaboration_issues.go
│   │   ├── collaboration_pulls.go
│   │   └── collaboration_operations.go
│   ├── github/
│   │   ├── client.go
│   │   ├── identity.go
│   │   ├── issues.go
│   │   ├── pulls.go
│   │   ├── reviews.go
│   │   ├── checks.go
│   │   ├── mutations.go
│   │   ├── polling.go
│   │   └── capabilities.go
│   ├── fountainclient/
│   │   ├── client.go
│   │   ├── connection.go
│   │   ├── artifacts.go
│   │   └── operations.go
│   ├── store/
│   │   ├── collaboration_hosts.go
│   │   ├── collaboration_connections.go
│   │   ├── collaboration_identities.go
│   │   ├── collaboration_operations.go
│   │   ├── factory_placement.go
│   │   └── factory_polling.go
│   └── web/
│       └── collaboration/
│           ├── handler.go
│           ├── admission.go
│           ├── enrollment.go
│           ├── artifacts.go
│           └── operations.go
├── lib/
│   ├── factory-host/
│   │   ├── Cargo.toml
│   │   └── src/
│   │       ├── lib.rs
│   │       ├── protocol.rs
│   │       ├── workspace.rs
│   │       ├── process.rs
│   │       ├── output.rs
│   │       ├── artifacts.rs
│   │       └── receipts.rs
│   └── desktop-runtime/
│       ├── Cargo.toml
│       └── src/
│           ├── lib.rs
│           ├── supervisor.rs
│           ├── paths.rs
│           ├── database.rs
│           ├── bootstrap.rs
│           ├── credentials.rs
│           ├── connections.rs
│           └── bridge.rs
├── frontend/
│   ├── factory/
│   │   ├── api/
│   │   │   ├── client.ts
│   │   │   ├── transport.ts
│   │   │   ├── library-response.ts
│   │   │   └── models.ts
│   │   ├── app/
│   │   │   ├── browser.ts
│   │   │   ├── factory-app.ts
│   │   │   ├── factory-app.stories.ts
│   │   │   ├── routes.ts
│   │   │   ├── navigation.ts
│   │   │   └── capabilities.ts
│   │   ├── feed/
│   │   │   ├── feed.ts
│   │   │   ├── feed-view.ts
│   │   │   ├── feed-response.ts
│   │   │   ├── revision-record.ts
│   │   │   ├── review-record.ts
│   │   │   ├── feed-thread.ts
│   │   │   ├── feed-communication.ts
│   │   │   ├── feed-actions.ts
│   │   │   ├── feed-artifact.ts
│   │   │   ├── feed-artifact.stories.ts
│   │   │   ├── feed-graph.ts
│   │   │   ├── feed-portraits.ts
│   │   │   ├── feed-record-actions.ts
│   │   │   ├── allowance.ts
│   │   │   ├── consultation.ts
│   │   │   └── feed.stories.ts
│   │   ├── library/
│   │   │   ├── library.ts
│   │   │   └── library.stories.ts
│   │   ├── work/
│   │   │   ├── work.ts
│   │   │   ├── work-view.ts
│   │   │   └── work-journey.ts
│   │   ├── requests/
│   │   │   ├── intent.ts
│   │   │   └── intent-view.ts
│   │   ├── issues/
│   │   │   ├── issue-list.ts
│   │   │   ├── issue-detail.ts
│   │   │   ├── issue-editor.ts
│   │   │   └── issue-detail.stories.ts
│   │   ├── pulls/
│   │   │   ├── pull-list.ts
│   │   │   ├── pull-detail.ts
│   │   │   ├── pull-files.ts
│   │   │   ├── diff-view.ts
│   │   │   ├── review-thread.ts
│   │   │   ├── review-draft.ts
│   │   │   ├── merge-status.ts
│   │   │   └── pull-detail.stories.ts
│   │   ├── shared/
│   │   │   ├── markdown-view.ts
│   │   │   ├── comment-editor.ts
│   │   │   ├── format.ts
│   │   │   ├── jev-answer.ts
│   │   │   └── drafts.ts
│   │   └── styles/
│   │       ├── base.css
│   │       ├── feed.css
│   │       ├── conversation.css
│   │       ├── issues.css
│   │       └── pulls.css
│   ├── desktop/
│   │   ├── index.html
│   │   ├── main.ts
│   │   ├── desktop-host.ts
│   │   └── desktop.css
│   └── storybook/
│       ├── main.ts
│       ├── preview.ts
│       └── fixtures.ts
├── docs/
│   ├── README.md
│   └── development/
│       └── factory-products/
│           ├── ideal-filetree-plan.md
│           ├── seams-and-source-map.md
│           ├── reuse.md
│           ├── proposed-tree.md
│           └── implementation-tasks.md
└── tools/
    ├── factory-ui/
    │   ├── Cargo.toml
    │   ├── build.rs
    │   ├── tauri.conf.json
    │   ├── capabilities/main.json
    │   └── src/main.rs
    └── release-assets/
        └── src/
            ├── desktop.rs
            └── bin/
                └── soda-desktop-build.rs
```

The tree names defining entry files, not a mandate to collapse complete extracted
helpers into those files. Existing work/request/feed support leaves move with their
verified import closure under the named owner; T04 records each actual source →
destination before changing callers. Do not create empty wrappers for every name.
Prefer same-source imports first; a separate Bun workspace/package manifest is
introduced only if its enforced public entry points add value. No public npm
publication or separate repository is required.

Implemented: the feed subtree, `api/models.ts` and `transport.ts`, host navigation
contracts, shared format/draft/answer helpers, shared styles, Storybook and the
opt-in Linux `tools/factory-ui` development workbench. Canonical tokens live under
branding alongside palette/fonts. The workbench has no backend or credentials;
`cmd/soda-desktop` and its runtime remain targets. Work/request extraction and new
issue/PR screens remain pending. Actual integration is documented in the
[shared UI guide](../../../frontend/factory/README.md).

T00-PG adds `lib/desktop-runtime/{Cargo.toml,build.rs,src/lib.rs,src/database.rs}`
and the existing PG fixture's native check binary. T03-BOOTSTRAP adds
`src/bootstrap.rs` to that Rust owner and the `native_config*.go` loader under
`internal/factory/runtime/`; these own private native inputs without importing
the appliance configuration. Process/IPC composition remains its consumer.

T07-ISSUE-CREATE defines `frontend/factory/issues/create.ts`, `create-record.ts`
and `create.stories.ts`, with the public result/capability decoder in
`frontend/factory/api/issue-create-response.ts`. The shared frame owns the typed
`issue-new` destination; `frontend/spaces/soda-place-issues.ts` supplies Fountain's
existing admitted transport/account/navigation ports. Story peers stay under
`frontend/storybook/issue-create-fixtures.ts`; neither product imports them.
The defining issue list/detail/comment owners remain `issues.ts`, `detail.ts`,
`screen.ts`, `comment.ts` and `comment-record.ts` in the same directory. The
speculative `issue-editor.ts` name above does not introduce a duplicate form.

T07-ISSUE-EDIT-BASE extends the existing `internal/factory/human_edit.go` command/
capture and `human_edit_outcomes.go`, with orchestration in
`service/human_edits.go`, strict HTTP decoding in `httpapi/human_edits.go` and
native snapshot binding in `internal/forgejo/human_edits.go`. It creates no
parallel journal or alternate edit handler.

T07-ISSUE-EDITOR defines `frontend/factory/issues/editor.ts`, `editor-record.ts`
and `editor.stories.ts`, with original-content/hash/capability projections in
`frontend/factory/api/issue-edit-response.ts`. The existing `issues/screen.ts`
owns primary-read gating, resource binding and ordinary readback after commit.
Fountain and the standalone frame supply matching optional editor ports; their
production builds consume the same element. Declared stories stay in
`frontend/storybook/issue-editor-fixtures.ts`. The speculative `issue-editor.ts`
above is not a second implementation.

T01-REGISTRY adds
`internal/collaboration/identity.go` and the defining
`internal/store/collaboration_{schema,identities}.go`; do not split these into the
speculative per-table filenames above without a concrete responsibility need.

T03-NATIVE-PROCESS adds `cmd/soda-factory-server/host_{appliance,native}.go`;
`factory_native` selects the SDK-free native command, with the default appliance
build preserved. `internal/factory/runtime/native_{server,listener_linux,listener_other}.go`
owns its authenticated Unix readiness/process lifetime. Product-route and
reconciliation composition remain consumers; Rust retains child/database ownership.

T03-PRODUCT-HTTP adds `internal/factory/httpapi/product.go` to compose existing
route owners around one bound identity/store and
`internal/factory/runtime/native_application.go` to consume the canonical
application with a trusted current-account binding. Provider/account admission
is supplied by connector consumers; no native session is fabricated.
T03-APPLICATION-LIFETIME supplies shared application startup/admission-stop/drain
over the existing coordinator. Native binding/product handlers use that same gate;
both hosts drain admitted application work before closing Store. Native background
startup and Rust/Tauri child supervision remain consumers.
T03-PROOF-CAPTURE adds `internal/factory/service/issue_intake_capture.go` for
canonical source/author/revision and current resource admission, proof reuse and
trusted issuer capture into the existing intake journal. Fountain's HTTP adapter
retains only its native issuance/encoding; desktop connectors supply their own.
T03-APP finishes Fountain product composition in
`internal/web/api/factory_product.go`. All seven native route adapters bind the
same `httpapi.Product` used by the native application, with one current canonical
account/store and lazily selected service ports. Native reconciliation/startup
remains T03-SERVER; connector login and native execution remain later consumers.
T03-CONTRIBUTION-RECOVERY adds
`internal/store/factory_contribution_owner.go`: producer-session ownership and
bounded recovery over existing assessment rows. The shared continuation and
current store callers use that owner through claim/settlement. Native startup
wiring is consumed by T03-SERVER; no timestamp implies producer death or permits replay.
T03-SERVER wires `service.Application.Recover` into the actual native executable
before readiness, sharing the bounded startup context with Store opening. The
appliance calls the same recovery before coordinator startup. Unknown execution
facts remain retained until admitted native runtime ports can observe/retire them;
startup grants no model, workflow or execution authority.
T05-SUPERVISOR defines `lib/desktop-runtime/src/supervisor.rs` for one owned
profile and dedicated PG/Go owner thread, exact private readiness, ordered stop,
child failure and parent-death retirement. `database.rs` supplies owned-child
observation and Linux parent-death setup; `soda-unix-http` supplies bounded
Bearer-authenticated framing without exposing the key. The existing native PG
acceptance driver exercises the actual supervisor/Go/PostgreSQL and retained
transaction through crashes and startup failures. T05-LIFECYCLE consumes this
producer for the Tauri bridge, window and activation semantics; no UI, connection
or executor authority is provided by this runtime owner.
T04-UI closes the shared source/build boundary after its listed recovery and
composition prerequisites. `frontend/factory/app/browser.ts` exposes the defining
application and its request-error constructor, plus typed host contracts.
`scripts/build-factory-ui.ts` builds that module and standalone CSS independently
of Forge/Storybook, with a product-source boundary check, embedded fonts, local
portraits and notices. `scripts/factory-browser-assets.ts` owns the common native
scheme correction and asset copying consumed by both builders. The existing
browser composition journey loads the emitted production module/styles; Forge
continues importing the same defining component sources. T05-LIFECYCLE owns the
native bootstrap/private bridge/window/managed-runtime join; no product HTML,
connection, credential or execution is provided by this asset build.
T04-RESPONSES moves the eight defining request/run/output/work/journey/progress/
acceptance/access response modules from Spaces into `frontend/factory/api/`.
Their complete source closure is these modules plus shared model/format/allowance
helpers. Production and fixture callers import those defining modules directly;
the old host paths have no re-export aliases. Work/request element and transport
extraction remains T04-UI.
T04-REQUEST moves the defining composer/review/walkthrough modules into
`frontend/factory/request/`. Its complete production source closure uses only
these modules, shared API models/request responses/drafts and Lit. Fountain's
request page/workspace setup import it directly. `request.stories.ts` consumes
the same element with declared fixtures in `frontend/storybook/request-fixtures.ts`.
Host routing/repository selection, work/progress components and remaining lifetime/
transport extraction stay in their explicit T04-UI consumers.
T04-REQUEST-LIFETIME keeps request admission in that same defining composer.
Synchronous reactive accessors retire the current request lifetime on API,
actor/profile, availability and requested-intent changes. All API dispatches,
background reads, chained effects and deferred UI completions stay bound to that
lifetime. An action may adopt its own committed intent route without being
retired. The Fountain workspace supplies a stable existing API callback; its
repository picker and request-page picker keep the entire workflow active while
hiding the composer. Seven production-source stories include inactive admission.
T04-REQUEST-DRAFTS consumes this lifetime before strict draft ownership;
T03-REQUEST-SAVE-RECEIPT adds exact owner-bound immutable revision lookup to the
existing store, request service, shared HTTP and Fountain private proxy owners.
It reads a historical save independently of the head and bounded history, using
existing rows and authentication. T04-REQUEST-COMMANDS consumes this producer,
drafts and the existing request/planning HTTP owners before exact pending-action
recovery. Larger multi-document and installed
qualification requirements remain in their UI/upgrade/native consumers.
T04-REQUEST-DRAFTS defines `frontend/factory/request/draft.ts` as the sole owner
of scoped request-draft decoding, persistence and shelf/repair types. The composer
adopts it for whole-record restoration and explicit persistence feedback. Shared
`drafts.ts` supplies an uncached persisted-byte read; other consumers retain
existing semantics. The request owner preserves a failed write's original
baseline and local copy, refuses changed/unknown persisted bytes and keeps
unreviewed copies intact. Nine production-source stories include restored/stale
local drafts. Exact action journals, atomic multi-document ownership, historical
import and installed persistence remain with their pending consumers.
T04-REQUEST-SAVE-RECOVERY defines `frontend/factory/request/save-record.ts` for
strict exact save inputs, scope/host binding, short Web Locks admission, immutable
receipt matching and revision-bound dependency recovery archives. The defining
composer captures the shelf transition before POST, refuses undurable dispatch
and offers explicit lookup/same-body retry without a chained action. Confirmation
preserves later writing and advanced heads; obsolete release requires fresh head
and receipt-absence evidence. It reuses the request draft decoder and existing
shared HTTP receipt producer. Ten production-source stories include an unconfirmed
save. The remaining effects consume this seam in T04-REQUEST-COMMANDS.
T03-GENERATION-REVISION binds the existing generation HTTP/service/coordinator
entries to the caller's selected acceptance. The shared composer sends that
revision; record-plan selection, chosen-plan adoption and graph resume preserve
it across reloads. Completion/repair/background recovery pass captured revisions
too. This changes current owners and callers together without a new journal,
schema or compatibility route. T04-REQUEST-COMMANDS consumes this prerequisite
before retaining generation commands.
T04-REQUEST-GENERATION-RECOVERY defines
`frontend/factory/request/generation-record.ts` and adopts it in the existing
shared composer. Exact accepted revision, original operation and complete writing
reach durable storage before dispatch; short Web Locks protect initial admission.
The save owner exports its existing strict authored-revision validator for this
consumer. `Requests.SavedGeneration` and the shared HTTP/native/private-proxy
routes read the original immutable acceptance's existing operation after head
changes, with explicit absence and no effects. The UI checks/archives/resumes
only the original identity, preserving later writing and refusing stale or
foreign receipts. Eleven production-source request stories include unconfirmed
work creation. Planning and exploration journals are defined below.
T03-PLANNING-RECEIPT adds `Requests.SavedPlanning`/`PlanningReceipt` to the existing
service progress owner and a revision-specific GET to shared HTTP, native product
routing and the Fountain private proxy. It validates the original draft/assignment
binding and returns captured operation/run/outcome facts after head changes,
without review replay, funding or native-start claims. T04-REQUEST-PLANNING-RECOVERY
defines `frontend/factory/request/planning-record.ts` and its production composer
consumer. It retains exact operation/revision/writing before POST, binds observed
run identities with a document-local fallback on storage failure, and explicitly
checks the historical receipt through fresh host/account admission. Confirmation
archives before clearing, preserves later writing/head, and makes no native-start
claim. Resume uses only the captured command; release requires an advanced head
followed by absence. Unknown records and known-but-missing run identities remain
pending. Twelve production-source request stories include unconfirmed planning.
Exploration is defined below; installed persistence/upgrade remain
with their existing consumers. The backend adds no schema or replacement status API.
T03-EXPLORATION-RECEIPT adds `Store.IntentExplorationReview` to the existing
atomic review owner, `Requests.SavedExploration`/`ExplorationReceipt` to
`request_exploration.go`, and the original-input read to shared HTTP/native product
routing/Fountain's private proxy. It resolves the server-minted result ID from
existing revision/review rows, validates original scope and bindings, and returns
recorded proposal/advice or scoped absence after later head changes. Ordinary
saves do not confirm exploration; absence is not proof of no consultation.
No schema, funding, chooser or dispatch is added. T04-REQUEST-COMMANDS consumes
this producer through `frontend/factory/request/exploration-record.ts` and the
production composer. It retains the original saved draft and complete writing
before dispatch under fresh host/account admission and short Web Locks, checks
private historical transition/advice identity explicitly, and archives before
clearing. Unchanged writing can adopt the current result; later edits/head and
stale drafts remain preserved. Resume posts only the captured input; release
requires an advanced head then absence. Known refusals remain distinct from
uncertain results; recovery chains no planning/work. Thirteen production-source
request stories include unconfirmed exploration. Installed persistence/upgrade
remain later consumers.
T04-JOURNEY moves the defining recorded-history element and projection into
`frontend/factory/work/{journey,journey-view}.ts`. Its independent production
closure contains only shared API models/journey responses/transport, navigation
types and Lit. `FactoryWorkLinks` supplies issue/PR destinations; Fountain owns
URL construction in `frontend/spaces/soda-factory-links.ts`. Work, project-work
and place-status hosts import the shared element directly. Five production-source
stories use declared peers in `frontend/storybook/journey-fixtures.ts`. Main work/
progress controls and remaining transport/lifetime extraction stay
with T04-UI.
T04-DECISIONS moves requirements acceptance and completion retry into
`frontend/factory/work/{acceptance,completion-retry}.ts`. Their production closure
uses only shared API/navigation/format/allowance modules and Lit. Typed contexts
bind shared transport and canonical resource IDs; `FactoryAcceptanceLinks` keeps
issue/comment routing in the host. Fountain's three composition hosts import the
shared controls directly. Six acceptance and five retry stories use declared
peers in `frontend/storybook/decision-fixtures.ts`. Generation/retirement guards
prevent late UI results or events; existing live-binding command identity remains
unchanged on uncertain replies. Durable remount/restart recovery remains with its
unfinished UI/upgrade consumers.
T04-CONTEXT moves the context-choice element into
`frontend/factory/work/context-resolution.ts`. Its independent production closure
is that element plus shared models, run responses and transport, with Lit as the
external UI dependency. Fountain's three composition hosts import it directly.
`ResolutionContext` admits direct run data or canonical issue lookup inputs;
binding/selection generations retire stale reads/actions without replay. Eight
production-source stories use declared peers in
`frontend/storybook/context-fixtures.ts`. Main work/progress and the remaining
host/lifetime/draft extraction stay with T04-UI.
T04-CONTEXT-ADMISSION adds host-supplied command custody to that same context
control. `ResolutionContext.commandScope` supplies stable profile/provider/account
metadata; shared detail forwards it and Fountain's composition hosts use their
existing authenticated scope adapter. The shared control owns fresh exact run/
repository/issue and selection admission, cancellation and explicit refresh;
the server owns authorization and semantic replay. Nine production-source stories
include unavailable identity. No host import or storage owner moves into the
control. T04-CONTEXT-RECOVERY consumes this admission before persisting/restoring
exact scoped choices; T04-UI remains the larger unfinished composition/recovery
consumer.
T04-CONTEXT-RECOVERY defines the scoped context-choice journal in
`frontend/factory/work/context-record.ts`, using the existing shared draft-storage
owner. Each retained entry is the backend's run/fingerprint/scope/required-digest
replay identity. The existing context control owns explicit recovery UI and
matching POST acknowledgement; neither current projection nor mount/reload
confirms or submits a choice. Different selections and runs retain separate
entries; canonical run/account checks precede replay and the backend retains
authorization. Ten stories import that same defining control. Multi-document
storage ownership, historical import and installed persistence keep their
unfinished UI/upgrade/native consumers.
T04-PRESENTATION moves the defining work/progress models and renderers into
`frontend/factory/work/{work-view,progress-view}.ts`. Their nine-source production
closure contains only shared API/navigation/format/allowance modules and Lit.
Navigation-bearing builders consume host issue/PR callbacks; response URL hints
never override that port. Fountain's detail adapter explicitly adopts its decoded
recorded URLs. Mount-dependent scope builders and `DetailScopeRuns` move into
`frontend/spaces/soda-factory-work-scope.ts`, with workspace callers cut over.
Shared overview scope records/producers stay in `progress-view.ts`. Fourteen
production-source stories use declared facts in
`frontend/storybook/work-presentation-fixtures.ts`. Runtime Work element transport/
lifetime extraction remains with T04-UI.
T04-PROGRESS moves read-only overview, plan and direction/history elements into
`frontend/factory/work/progress.ts`, with bounded JSON/error handling in `read.ts`.
The twelve-source production closure has no Spaces import. Typed contexts consume
shared transport/canonical read keys and optional navigation; generation/retirement
guards also prevent later plan/history reads after a binding is retired. Fountain
native path and mount conversion stays in `soda-factory-work-scope.ts` and its
existing mount composition, with explicitly named Fountain context types. Nine
production-source stories use declared peers in `frontend/storybook/progress-fixtures.ts`.
Work detail/intervention and Watch extraction remain with T04-UI.
T04-DETAIL moves the defining detail/intervention element into
`frontend/factory/work/detail.ts`, with a twenty-source independent production
closure that contains no Spaces imports. Typed transport/navigation/capability
inputs replace Fountain mounts/paths. `FountainWorkDetailContext` and its conversion
stay in `soda-factory-work-scope.ts`; the former Work module now owns only Fountain
mount admission/composition. Shared `factoryRunID` and `statusText` supply run
validation/status presentation; native terminal ownership and Watch hosting stay
separate. Six production-source stories use `frontend/storybook/detail-fixtures.ts`.
Current-capability gates, bounded retirement and exact live-binding unknown retry
identity are qualified; durable recovery and Watch extraction remain unfinished.
T04-WATCH moves the defining observation element and
view/display/request/screen helpers into `frontend/factory/watch/`, with output-only
renderer, discovery/attachment/peer and resume ports in `ports.ts`. The eleven-source
production closure has no Spaces imports. Fountain's old Watch module now owns
only native context admission, renderer/socket loading, discovery and lifecycle
adaptation. Common terminal/output geometry lives in `frontend/factory/styles/output.css`,
shared by Fountain and standalone hosts. Seven production-source stories use real
xterm plus declared peers in `frontend/storybook/watch-fixtures.ts`. Native terminal
ownership, installed/connected observation and durable recovery remain distinct.
T04-RUN-RETRY-RECOVERY adds
`frontend/factory/work/retry-record.ts` beside the detail consumer. The twenty-two
source production closure remains host-independent. A host-admitted command-scope
callback plus canonical repository/issue keys owns browser pending identity;
Fountain's adapter resolves authenticated host/human scope. The draft storage
owner exposes read availability and write persistence while retaining existing
fallback behavior. Exact run retry persists before dispatch and recovers on
remount/reload without automatic POST; unsupported/conflicting records remain
preserved, and settlement checks the exact issued record. Native profile/crash,
other pending commands and historical recovery remain unfinished consumers.
T04-COMPLETION-RETRY-RECOVERY adds
`frontend/factory/work/completion-retry-record.ts` beside the shared completion
control. Its eight-source production closure imports no host code. Account/work
scoped command/event/prior-cycle records persist before POST and recover without
automatic mutation, even after the current gate advances. Exact recorded receipts
settle only their issued record; unknown/refused results remain saved. Fountain's
three composition hosts supply the same authenticated `fountainCommandScope`
callback; native hosts supply their own profile/account reference. Six completion
stories include a recovered request. Acceptance/native recovery remain later work.
T04-HOST-REFRESH corrects the remaining Fountain Project Work host's inventory
handoff in `frontend/spaces/sodaspaces-project-work.ts`. Same-binding verification
run updates preserve admitted canonical IDs and reviewed issue state; pending
identity discovery adopts the latest inventory snapshot. Existing browser roots
cover newly discovered/removed runs, unchanged pending commands, canonical IDs
above the safe integer range and account/project retirement. This does not move
the host into shared presentation or qualify native discovery.
T04-ACCEPTANCE-RECOVERY adds `frontend/factory/work/acceptance-record.ts`, owning
the complete reviewed decision shape and bounded account/work-scoped pending
storage beside the shared control. Its twelve-source closure has no host imports.
Fountain's existing three hosts supply the same admitted command-scope port.
Exact decisions persist before POST, recover without automatic approval and
settle against recorded receipts; recovered/unknown refusals stay pending.
First known typed stale refusals still require a fresh review. Seven production
acceptance stories include recovered decisions. Native/historical recovery remain
later consumers.
T04-VERIFICATION moves the defining application-verification retry, repair request
and recorded progress control out of Fountain Project Work into
`frontend/factory/work/verification-retry.ts`. Both Project Work and App place use
that one shared element; App no longer imports Project Work for registration.
Its seven-source production closure has no host/native-terminal imports. The
typed context carries canonical repository/run IDs; the host retains discovery.
Lifetime/generation guards retire reads, actions and polling; scoped reads and
matching receipts gate facts/confirmations. Eleven production stories use declared
peers. Persistent pending recovery and installed/native execution remain consumers.
T04-VERIFICATION-RECOVERY adds `frontend/factory/work/verification-record.ts`
beside the shared control. The nine-source closure remains host-independent.
Admitted profile/account plus canonical repository scope retains the exact run,
action and retry command before POST; repair keeps its existing run-bound body.
Remount/reload recovery never posts automatically, freshly checks saved runs even
outside the current list and settles only its exact recorded receipt. Both
Fountain hosts supply the existing authenticated scope callback. Thirteen stories
include recovered retry/repair; native/historical recovery remain consumers.
T04-ALLOWANCE moves the verification-limit control to
`frontend/factory/work/verification-allowance.ts`, response/command definitions to
`frontend/factory/api/verification-allowance.ts`, and exact pending storage to
`frontend/factory/work/allowance-record.ts`. Both Fountain callers use that one
control, with admitted identity and canonical intent/repository scope. The
eight-source closure has no host imports. Exact cumulative limit increases persist
before POST and recover without automatic submission; recorded command receipts
or an exact current-head read settle them. Eight declared stories cover permissions,
unknown outcomes, recovery and stale revisions. Native/historical recovery remains
a consumer requirement.
T04-REPAIR moves repository repair funding and charge/episode history into
`frontend/factory/work/repair-funding.ts`, with response/command definitions in
`api/repair-response.ts` and exact scoped pending requests in `work/repair-record.ts`.
Fountain settings supplies transport, canonical repository and admitted account
scope; the seven-source closure imports no host code. Grants and withdrawals
persist before PUT, recover without automatic mutation and settle only against
matching journal receipts or exact current-head facts. Nine production stories
use declared peers. Native/historical recovery and real effects remain consumers.
T04-ACTIVITY moves recorded activity and display preferences into
`frontend/factory/work/activity.ts`, contracts into `api/activity-response.ts`
and scoped preference recovery into `work/activity-draft.ts`. Fountain settings
supplies current edit capability and admitted identity; exact native links stay
in `spaces/soda-factory-links.ts`. Without a host navigation callback, recorded
facts remain text. The seven-source closure imports no host code. Preference
changes retain their expected revision before PUT, recover without automatic
submission and require explicit review after conflicts. Ten declared stories
cover standalone routes, plain text, recovery and partial history. Native/server
effects and historical recovery remain consumers.
T04-LIBRARY moves knowledge/decision search, paging and item/revision presentation
to `frontend/factory/library/library.ts`, with scoped contracts in
`api/library-response.ts`. Fountain Knowledge supplies canonical transport/scope
and owns source-post navigation from typed events. The eight-source closure
imports no host code. Superseded reads abort, late results and queued old source
clicks cannot cross binding retirement, and denied reads remove private facts.
Eight production stories use declared read peers. Connected/native provider
qualification remains a consumer requirement.
T04-FEED-LIFETIME binds `frontend/factory/feed/feed.ts` page/conversation/control
requests to a cancellable context with a captured transport generation. Both
response headers and JSON completion are checked; retirement clears private view
state, stops polling and preserves issued pending commands. The optional
`app/navigation.ts` application-origin port now receives a cancellation signal,
forwarded by Fountain's existing feed host. The eighteen-source production closure
imports no host code. Browser checks qualify retired reads/writes and exact scoped
post recovery. T04-ARTIFACT-LIFETIME supplies the child preview port and byte
retirement; connected/native execution remains a consumer requirement.
T04-ARTIFACT-LIFETIME replaces bare request injection in cards and replies with
`FeedArtifactContext` in the existing `feed/feed-artifact.ts`. It carries canonical
repository, transport, captured generation and parent lifetime. Source/context
changes cancel streams and revoke cached Blob URLs; late headers/chunks/decoded
text cannot publish. Exact API hints and retained byte/text bounds gate previews.
The four-source component closure imports no host code. Eight production stories
use declared byte peers; standalone/Fountain browsers qualify both compositions,
actual timeout, image decode, recovery and retirement. Native/provider transfer
remains a consumer requirement.
T04-FEED-ACCESS keeps access-loss ownership in `feed/feed.ts`: current feed/
conversation denial clears private view facts, aborts the old visibility lifetime
and stops polling without erasing scoped writing/pending posts. Commands stay
blocked until an explicit fresh window succeeds. Optional owner-only allowance
403s do not revoke readable feed access; retired replies cannot re-admit or deny a
newer visibility lifetime. The eighteen-source closure remains host-independent.
Production feed stories include expiry/withdrawal. Browser evidence covers
standalone and Fountain composition; installed/provider qualification remains a
consumer requirement.
T04-FEED-EXAMPLES completes the thirteen feed states through the single declared
`frontend/storybook/fixtures.ts` peer: paging, replies, busy/retry/unknown outcomes
and actual revision CAS. It has no backend/provider/worker/model effects. All
callers use the new peer directly; the old context helper has no alias. A browser
example exposed issued posts stranded by filter changes; `feed/feed.ts` now
settles those against captured account/runtime lifetime while retaining access/
generation retirement. Standalone/Fountain browsers verify the interactions and
exact retry readback. The production eighteen-source closure remains host-free.
T04-FEED-EDIT-LIFETIME keeps revision/review ownership in `feed/feed.ts` and adds
strict editable-source/receipt decoders to the existing `feed/feed-response.ts`.
The view passes the canonical repository with edit actions. Captured account/
runtime lifetime survives filter changes; source/receipt scope and editor identity
gate settlement. Review intent changes cannot redirect navigation. The actual
`post_id` receipt and unchanged-applied-draft branch are preserved. The nineteen-
source closure imports no host code. Browser checks qualify live behavior;
durable pending revision-effect and installed/native recovery remain consumers.
T03-POST-RETRY-RECEIPTS adds `internal/factory/feed_post_retry.go` for exact typed
command/receipt scope and `internal/store/factory_feed_post_retry.go` for their
atomic persistence in the existing `factory_commands` ledger. The existing retry
transition has one transaction-level owner in `factory_feed_post.go`; no schema
or parallel workflow is introduced. T03-POST-RETRY-HTTP owns the service/current
caller cutover and capture/replay semantics before T04-FEED-REVISION-RECOVERY can
consume durable receipts in the shared UI. Existing integration roots qualify
unchanged-revision retries, concurrent admission, rollback and native retained
restart/WAL recovery; this producer does not complete its consumer cutovers.
T03-POST-RETRY-HTTP cuts `service/post_commands.go`, `httpapi/feed_posts.go`, the
Fountain private route/query mounts and current caller over to those commands and
receipts. Its read-only receipt path checks current author/visibility; retained
receipt decoding stays in the domain owner. `store.NotePostContextWait` guards
late capture failure by revision. The unused unjournaled Issue Maker wrapper is
removed; the transaction-level transition remains shared. The production feed
validates issued command/body/repository identity in its existing response owner,
while T04-FEED-REVISION-RECOVERY owns durable pending command persistence and
reload recovery. Real HTTP/PG lost-reply/reopen, private Fountain proxy and browser
evidence qualify this cutover without claiming installed native lifecycle.
T04-FEED-REVISION-RECOVERY adds the defining `feed/revision-record.ts` owner and
consumes it directly in `feed.ts`. Profile/provider/account, canonical repository,
post and author bind the exact command/CAS/body and writing snapshot. Shared draft
storage retains failed writes/removals in the current document. Dispatch requires
durable persistence; reload/remount performs no mutation. Explicit receipt checks
and exact saved retries preserve newer draft text, replaced/unknown records and
newer head revisions. Pending recovery has an entry point even when current edit
capability is false. Fourteen feed stories include unknown revision receipts;
standalone and Fountain browsers use the same production control. The twenty-
source production closure imports no host code. Installed lifecycle/upgrade,
cross-document ownership and review-handoff recovery remain consumers.
T04-APP-COMPOSITION adds `app/factory-app.ts` and `app/routes.ts` as the defining
production frame and canonical destination owner. Typed host admission resolves
ports for the existing Feed, request, work overview/detail/plan, Knowledge and
Watch elements; the frame owns their DOM rather than asking hosts to create a
second UI. Current transport/account and exact route scope gate mounting. Context
retirement explicitly clears bindings even before a child connects; late admission
cannot restore a retired view. Host navigation owns URLs/history and connection
selection. The frame's own envelope and scheme use the existing product tokens.
Four application stories and an existing browser integration root qualify the
seven compositions and account/lifetime/navigation joins. Their declared peers
reuse existing fixtures with selectable canonical repositories; production imports
no stories or hosts. The independently checked source closure contains 52 sources.
T05 supplies the native process/bridge/bootstrap consumer; T04-UI retains its
remaining ownership/recovery joins and is not complete merely because a frame
can render every extracted control.
T04-FEED-REVIEW-RECOVERY adds `feed/review-record.ts` for exact review handoffs and
the strict `feedReviewSource` decoder in the existing response owner. `feed.ts`
persists the source revision/intent/accepted predecessor under current profile/
account/repository/post/reviewing-actor scope before calling the existing review
endpoint. It consumes the store's source-bound `draft_revision` receipt; no new
nonce protocol, schema or competing workflow is introduced. Recovery is an
explicit read with current permission, while retry retains the original source.
Late, foreign, replaced or unavailable records cannot settle or redirect a handoff.
The twenty-one-source production feed closure remains host-free. Fifteen feed
stories include unknown review results, qualified through standalone and Fountain
composition. Full multi-document ownership and installed/native recovery remain
consumer requirements.
T01-LEDGER adds `collaboration_adoption.go` and `collaboration_retained.go` under
that store owner for the explicit import and its read-only reference inventory.
T01-RECORDS adds `internal/collaboration/records.go`, the Lit API mirror and its
diff/anchor decoders. T01-EFFECTS adds `internal/collaboration/effects.go`,
`effect_outcomes.go`, `graph.go` and `graph_receipt.go`: server-side intents,
conditional guarantees, receipt checks and durable-outcome shapes. T01-RUNTIME
adds `internal/factory/access/{actor,grants,policy}.go`,
`internal/factory/execution/provenance.go` and
`internal/factory/runtime/placement.go`. These are the defining neutral contracts;
actual adapter/consumer cutovers remain pending. The defining records stay
cohesive instead of empty per-record wrappers.

T00-DIFF adds the production `frontend/factory/review/diff.ts`, its stories,
`frontend/storybook/diff-fixtures.ts` and the actual native webview integration
check. The existing `tools/factory-ui` workbench runs that check with trusted
compositor keyboard input. Provider/normal-navigation review cutover remains T08.

T02-INTAKE adds `internal/factory/issue_intake.go` and cuts the existing store,
Fountain adapter and native HTTP admission/issue-maker callers over together.
Signed provider proof retains its old JSON owner/shape; semantic mutations have
one defining domain owner. Remaining T02 graph/runtime/identity cutovers precede
the final complete-engine/Fountain qualification.

T02-GRAPH adds `internal/factory/{graph_observation,graph_outcome,graph_codec}.go`
and matching Fountain translators. Generated reads, retained original-creation
recovery and graph outcomes use those contracts. Generation/reassessment plans
use `collaboration.GraphIntent`; Fountain owns packet inspection/encoding and
receipt decoding. Approved native packet/receipt shapes and digests survive
PostgreSQL JSONB round trips. Store delivered-history guards consume decoded
facts matched to opaque provider evidence. Canonical identity translation and
general host-scoped operation composition follow their existing dependency
gates; the narrow outcome does not invent a HostID. T02-RUNS is next.

T02-RUN-OBSERVATIONS adds `internal/factory/execution/observation.go` and
`internal/host/factory_observation.go`. The neutral port carries tagged runtime
facts without fabricated placement/image/native metadata. The appliance adapter
separates process retirement, broker sealing and credential return. Actual Unix
clients and the Rust broker/native PostgreSQL passed observation/tombstone/restart
checks. T02-RUNS now wires coordinator/dispatch observation and custody consumers.
`execution/candidate.go` and `host/factory_export_observation.go` move candidate
decoding and errors behind the adapter. The coordinator's production closure is
SDK/host-free. Retained run/tool/admission variants and remaining command/profile
boundaries are still active T02-RUNS work; the full server composition is later.

## Existing owners retained

`internal/factory/control`, canonical Factory domain files and `internal/store`
schema files remain in place. Existing `internal/forgejo` private transports and
publisher remain appliance-only; new translation files extend that owner.
`internal/host`, `lib/host`, `cmd/soda-identity`, appliance Projects/terminals,
Tailnet, application administration and their tests retain ownership.
`frontend/spaces` retains the native mount contract and all Project/terminal/host
settings not actually consumed by the shared Factory product. Canonical palette,
fonts, portraits and licenses retain their asset owners and are bundled by both
consumers rather than copied into a second asset source.

Go `factory/access` owns admitted actors/grants; collaboration owns remote
identities. TypeScript models mirror canonical API records through one parser
owner. Rust `factory-host/protocol.rs` mirrors the bounded execution wire contract;
it does not define a second workflow or policy model. `desktop-runtime` owns
native storage/supervision, while `soda-desktop` is a thin app entry and commands.
No new native network server is introduced in Rust.

`service/operations.go` is the shared application entry to existing durable
workflow journals. `store/collaboration_operations.go` owns provider-effect
metadata/checkpoints and their bindings to that history; it must not create a
second competing operation or workflow state machine.

## Retirement and defining cutovers

| Predecessor duty | Retire only after |
| --- | --- |
| Feed implementation under `frontend/spaces` | Every production entry/story imports the shared component and existing browser journey passes; remove old defining files, no re-export compatibility layer |
| Native issue/PR URL builders in moved product code | All typed navigation targets are resolved by the host, including thread/file/check links |
| Factory-status-only repository issue view | Shared issue detail contains its Factory facts and manifest/header links are changed together |
| Factory business orchestration inside `web/api` | Both host adapters/background progress invoke the shared application service; retain HTTP-only parsing/admission there |
| SDK-dependent domain/store records | Fountain translator and retained data decoding preserve all required evidence/outcome semantics |
| Factory command's dependency on shared Forge composition | Portable runtime owns its lifecycle and Darwin import/build proof excludes the whole Linux-only closure |
| Duplicate template implementations of the primary Soda issue/PR journey | Shared screens are the normal Soda navigation; native provider pages remain provider-owned, not deleted or claimed dead |

## Integration writer checklist

One coordinator edits root `Cargo.toml`/`Cargo.lock`, `package.json`/`bun.lock`,
`go.mod`/`go.sum`, TS configs, Lit/source-guard globs, command discovery, extension
manifest, shared schema and release inventories. Packet writers submit exact
imports/dependency selectors/paths. Update existing `tools/unitban` allowlists only
for relocated existing integration files; the plan adds no unit-test exception.

Maintain separate browser outputs for Fountain and desktop, importing the same
product sources. Bundle grammar/theme/font assets locally. Storybook assets never
enter appliance/desktop production payloads. Desktop packaging includes native
Go/PG binaries for the selected architecture with their notices; the appliance
continues shipping its Factory executable/unit using existing release tools.

Fountain `extension.json` routes, `frontend/spaces/soda-places.ts`, repository
entry, native header/places templates and route integration fixtures have one
cutover writer. Core native `/issues` and `/pulls` handlers stay in Fountain.
The gateway's HTTPS path/admission/Caddy rule is a separate explicit server join;
it must not reuse a browser extension socket as a public API.
