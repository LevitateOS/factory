# Seams, source allocation and workflow evidence

Scope and source identity belong to the [plan](ideal-filetree-plan.md). This is
the canonical finding/source map for this targeted extraction. A family below
means its existing files remain with that owner unless a named duty is extracted;
it is not permission to rewrite an entire directory.

## Canonical boundaries

| Slice | State/authority owner | Selected boundary |
| --- | --- | --- |
| S1 Shared presentation | Lit component state, typed views, drafts; no credentials or permission authority | `frontend/factory/`; Fountain and Tauri are consumers |
| S2 Collaboration | Remote provider owns issues, PRs, comments, review/check/protection facts | Soda-owned observations/intents in `internal/collaboration`; provider adapters translate |
| S3 Factory application | Accepted inputs, grants, assignments, spending, dispatch, operation history | Existing domain/control plus extracted application services; one workflow implementation |
| S4 Local runtime | Worktrees, processes, PTYs, artifacts, identity/custody and cancellation | Rust native executor; appliance host remains its own implementation |
| S5 Installation lifecycle | One desktop instance/data root, PG, Go backend, native bridge, app windows | Rust desktop supervisor; explicit close/Quit/restart semantics |
| S6 Delivery and parity | Builds, routes, fonts/styles, manifests, licenses, installed journeys | Existing build owners plus narrow desktop/Storybook additions |

Language choices follow [ownership](../ownership.md): Go network services and
workflow integration, Rust system processes and credential storage, TypeScript/Lit
browser UI, SQL in the canonical Go store. This is a policy-constrained selection,
not an unrestricted proof that another language could never fit. Rewriting the
existing Go coordinator in Rust would discard useful behavior without solving its
Fountain or PostgreSQL coupling. Rust remains appropriate for native lifetime
and Tauri integration; Lit preserves the existing frontend investment.

## Source-to-target allocation

This table preserves the source allocation from planning. The feed and its
support modules, parser/helpers and shared styles have now moved; their current
integration is in the [shared UI guide](../../../frontend/factory/README.md).
Fountain resolves feed links/catalogue through `soda-factory-feed-host.ts`.
Work/request and new issue/PR screens remain pending. Shared tokens have their
canonical owner at `assets/branding/theme/tokens.css`.

| Existing defining source/duty | Action and selected owner | Cutover joins |
| --- | --- | --- |
| `frontend/spaces/sodaspaces-factory-feed.ts`: `SodaFactoryFeed`, paging/threads/composer and retry state | Extract production behavior to `frontend/factory/feed/feed.ts`; no redesign during move | All entries, custom-element registration, client, styles, browser fixtures |
| `FactoryFeedContext`, `readJSON`, `resolveLabels`, `readAppOrigins` in that file | Replace mount/Forge inventory dependencies with typed client, repository catalogue and optional host capabilities | Account/session transitions; unavailable versus unsupported inventory |
| Feed `*-response.ts`, `*-view.ts`, `*-thread.ts`, `*-communication.ts`, `*-actions.ts`, `*-artifact.ts`, `*-graph.ts`, `*-portraits.ts` | Extract feed-specific model/render/asset duties into corresponding `frontend/factory/feed/` files | Keep one implementation; update every importing renderer and asset builder |
| Feed localStorage/pending payload/idempotency logic | `frontend/factory/shared/drafts.ts`; bind text, pending command and idempotency identity as one profile/repository/actor-scoped record | Preserve pending operations on remount; do not replay across actors/connections |
| `sodaspaces-factory-work-view.ts`: `nativeIssueLink`/`nativePullLink`; `soda-places.ts` URL synthesis | Product links become typed `NavigationTarget`; shell resolves URLs | Every feed/work/request/comment/diff link; no silent provider-page fallback |
| `sodaspaces-factory-work*.ts`, intent/acceptance/status helpers used by moved feed | Move the actual product import closure into `frontend/factory/work/` and `requests/` as each caller is cut over | Fountain-specific action admission stays in adapter; no shared-to-`spaces` imports |
| `soda-extension.ts`: `prepareExtensionMount`, canonical context validation/lifetime | Retain Fountain adapter under `frontend/spaces`; implement Factory-owned client interface | Retire/suspend/resume/generation and native request/stream/navigation remain host-owned |
| `soda-feed-entry.ts`, `soda-repository-entry.ts`, `soda-place-feed.ts`, `soda-place-issues.ts` | Thin Fountain hosts; shared issue detail includes Factory facts and existing controls | Manifest subroutes, header/templates, all normal Soda links |
| `assets/branding/theme/palette.css`, `assets/branding/fonts/`; portable parts of `assets/branding/forgejo/components.css` | Retain canonical palette/fonts; shared product variables/component styles in `frontend/factory/styles/` | Both products and Storybook bundle local fonts/assets; preserve notices |
| Native document/navbar selectors in branding CSS; `frontend/forgejo/templates/` | Retain native ownership and GPL attribution; reuse behavior/design references | No desktop dependency on Fountain global DOM/CSS; ordinary native pages continue working |
| `repo/issue/{list,new,view}.tmpl`, native issue content templates; `repo/pulls/{files,status,commits}.tmpl` | Implement shared Lit issue/PR presentation; native provider remains record owner | Explicit comment/review/merge actions, permission-aware visibility, legacy native links remain provider destinations |
| `internal/factory/` domain and `control/` dispatch, budgets, reconciliation | Retain authoritative workflow behavior; replace leaked provider data only | Keep journal transitions and unknown outcomes; no parallel desktop state machine |
| `control/intent_generation.go`, `intent_decomposition.go`, `creation_recovery.go`, adoption/reassessment helpers | Soda-owned graph, snapshot/version, creation-evidence and effect records in `internal/collaboration/` | Translate in Fountain adapter; update SDK-based tests/fixtures with real provider boundary coverage |
| `internal/store/factory_issue_maker.go` provider SDK import | Store neutral accepted-input/operation records | Removing SDK only from coordinator is insufficient for portable dependency closure |
| `internal/forgejo/{background,observe,publish,review,merge}*`, `publish/` | Retain Fountain connector and strong private protocol, add translation files | No appliance service admission or token-file selector accepted from desktop |
| `factory/issue_intake.go`, `store/factory_issue_maker.go`, `forgejo/issue_intake.go` | T02-INTAKE complete: neutral semantic requests and opaque retained provider proof; SDK interpretation/attachment stays in Fountain | Store/domain production closure is SDK-free; graph/runtime coordinator cutovers and canonical ID translation remain later T02 slices |
| `factory/{graph_observation,graph_outcome,graph_codec}.go`, matching `forgejo/` translators | T02-GRAPH complete: neutral graph plans, source facts, creation recovery, receipt consumers and outcomes; store guards use decoded facts matched to opaque provider JSON | Native packet encodings/digests retained through JSONB; real Unix transport and native PG recovery/adoption/repair checked. Concrete host closure remains T02-RUNS; actual Fountain qualification remains T02-ENGINE |
| `internal/web/api/factory_*`: feed/post/request orchestration and background `DrivePendingPosts` | Extract application use cases into `internal/factory/service/`; HTTP handlers delegate | Background progress no longer depends on a Fountain HTTP API object |
| `internal/web/auth/extension.go`, `store.Session`, API `nativeProductActor`-style gates | Keep Fountain validation in adapter; neutral admitted actor/grants in `internal/factory/access/` | Desktop session is distinct; actual remote actor/permission checked through connector |
| `internal/web/server.go`, `products.go`, `serve/`; command `main.go` files | Portable Factory runtime/HTTP composition; OS-specific appliance composition at command boundary | Darwin dependency closure excludes SDK, `internal/web`, concrete `internal/host` and other Linux-only transitive imports |
| `control.HostFactory`/`BrokerExecution`, `internal/project` execution wire records | Consumer-owned execution port; tagged native/appliance provenance in `internal/factory/execution/` | No fake image/container/UID/broker-return values for native execution |
| `execution/{observation,candidate}.go`, matching `host/` adapters | T02-RUNS active: coordinator/dispatch use tagged observation/custody facts and bounded candidate bytes with typed errors; production core closure excludes host/SDK | Actual Unix clients, Rust broker/native PG seal/restart and affected PG supervision/recovery checked. Retained run/tool/admission variants and remaining command/profile boundaries are incomplete. Broker binding `project` is a container boundary; it does not attest workspace |
| `internal/host/`, including SDK-dependent Tailnet files | Retain appliance client; use a distinct portable execution client | Importing unused methods still imports a Go package's platform dependencies |
| `lib/host/src/factory/`, terminal/factory, checkpoint, candidate, output/receipt logic | Reuse portable validation and Git/artifact duties in `lib/factory-host`; retain Linux launch/cgroup/Podman code | Actual native process identity, descendants, PTY, deadline and candidate export proof |
| `cmd/soda-identity/`, `lib/host` credential/lease duties | Retain appliance broker; native custody and supported provider sessions in `lib/desktop-runtime/credentials.rs` | Git/API/provider accounts remain distinct; no frontend generic keychain or shell API |
| `lib/provider-tools/` | Reuse release discovery, verified selection and active-run reference semantics | Qualify native archives/executables; do not assume Linux bundles run on macOS |
| `internal/store/schema*.go`, `factory_*.go`, readiness/operation transactions | Keep one schema owner; add host/repository/actor/connection mappings and runtime provenance | Retain data, FKs and transactions; minimal required migration, no duplicate desktop schema |
| `cmd/soda-factory-server`, `cmd/soda-dashboard`, `cmd/soda-factory` | Keep installed identities: engine server, Forge server, Rust operator CLI | Separate composition and config; CLI remains administration, not desktop runtime |
| `system/containers/{dashboard,extension}`, `system/host/services/`, Caddy, tmpfiles, SELinux | Retain appliance process split; add only required gateway/enrollment/placement wiring | Browser/control socket separation and scoped mounts remain effective |
| `scripts/build-soda-extension.ts`, `build-forgejo.ts`, `tsconfig*`, `tools/lit-check/`, source guards | Teach nested shared source imports/assets and Storybook files; keep one browser build source | No copied production UI in stories; package selection and source bans continue working |
| Existing `tests/frontend/`, `tests/forgejo/`, integration files under `internal/` | Extend real integration journeys in existing files | No new unit tests or new `*.test.ts`/`*_test.go` files; native harness scripts may target real installed subjects |
| Rust release/image/install inventories and `tests/build/` | Preserve installed Factory binary/unit; add desktop distribution in its own tooling | Never put a desktop app in the CoreOS payload or rebuild the OS for a UI-only desktop change |

Oversized mixed files were inspected by duty rather than treated as mechanical
rename candidates: feed state/transport/drafts; coordinator provider intent and
receipt handling; web composition; schema and multi-row operations; native host
launch/provenance; global CSS and native templates. Large licenses, fonts and
provider fixtures retain their existing artifact ownership. Unnamed helpers stay
with their current package until their actual import closure is extracted; no
retirement is inferred from a shallow caller search.

## Required records and interfaces

`internal/collaboration` defines `HostID`, `RepositoryRef`, `ActorRef`,
`ConnectionRef`, opaque remote locators, capabilities, versioned observations,
operation intents/outcomes, issue/PR/review/check records. Provider instances and
credential connections are different: repository uniqueness is
`(HostID, ExternalRepositoryID)`, actors likewise. Use local numeric surrogates
where existing ledger rows require them. Renames, transfers within one instance,
token refresh and choosing another account must not silently fork identities.

Implemented by T01-REGISTRY: canonical identity records in
`internal/collaboration/identity.go` and additive storage/guards in
`internal/store/collaboration_*`. Empty-ledger initialization is explicit;
retained Fountain allocation stays blocked until explicit configured-source
adoption. T01-LEDGER now supplies that atomic import/range reservation and its
native PostgreSQL proof. Capability/observation/read records are now defined by
T01-RECORDS in `records.go` and the Lit mirror.
T01-EFFECTS defines operation/graph contracts and outcome validation in
`internal/collaboration/{effects,effect_outcomes,graph,graph_receipt}.go`.
T01-RUNTIME defines admitted actors/grant/policy references in
`internal/factory/access`, the native/appliance union in `execution/provenance.go`
and runtime generations in `runtime/placement.go` under that Factory owner.
T02/T03 still own actual consumer cutover; D1/D3 qualification remains open.

Keep PR discussion comments, inline review comments, submitted reviews, checks,
thread resolution and issue comments distinct. A review anchor includes immutable
base/head commit, file path, old/new side, line range and outdated/original state.
Truncated/binary/renamed patches have explicit representations. Credentials and
provider SDK objects are absent from browser DTOs.

Application services consume admitted human/runtime actors, current permissions,
standing repository/capacity/sponsorship grants and the existing operation journal.
HTTP/Fountain/local adapters establish admission; a frontend capability controls
presentation but never substitutes for server authorization. Keep narrow ports
with their consuming service/control package; do not add a second generic ports
framework duplicating the existing interfaces.

Remote operations retain pending, committed, not-committed and indeterminate
outcomes. Persist intent before submit. After timeout, reconcile by identity and
exact provider observations before another write. An operation marker is a lookup
aid, not a transactional receipt. GitHub graph generation is a sequence of effects;
persist each mapping/checkpoint and show partial progress. Never advertise atomic
graph publication or guarded merge without evidence the connector enforces it.

## Source workflow traces and consequential findings

| Finding | Observed source chain | Required correction and evidence limit |
| --- | --- | --- |
| F1 Host-coupled feed | Fountain entry → prepared mount → feed → Forge `spaces`/repository/app inventory + native link helpers | Extract catalogue, navigation, optional host capabilities, scoped drafts and full CSS/asset closure. A generic `fetch` wrapper alone does not solve it. |
| F2 Host-coupled engine | `NewFactory` → SDK `ServiceBackground` → coordinator graph/snapshot/adoption and store SDK records | Translate at connector boundary; native dependency check covers whole Go import closure. Current macOS SDK peer-credential compile failure is documented, not repaired by UI work. |
| F3 Different authority evidence | HMAC issue hint → authoritative readiness → retained original-creation provenance → acceptance → dispatch | Current-state GitHub polling cannot recreate historical human permission. D1 must define its authorization model explicitly. Signed webhook origin alone is insufficient. |
| F4 Missing external PR intake | `parseIntakeHint` accepts `issues`/`issue_comment`, rejects PR comments and unrelated events | Add a distinct external PR subject/adoption/review path. Existing Factory-published PR progression is not proof this exists. |
| F5 Publication locality | Native executor exports candidate → `Publisher` validates local bundle → registered Git push with private `PublishPushEnv` → retained lookup | Fountain gateway needs bounded candidate transfer/staging plus operation receipt lookup; forwarding JSON or a Mac pathname cannot work. |
| F6 Multiple controllers | Appliance ledger lock protects one ledger; independent desktop would have another | Fountain placement record binds repository/runtime/generation. Retire previous work before switching. GitHub first release supports one configured controller; local locks cannot prove global exclusion. |
| F7 Appliance execution evidence | Project/container/UID/image/credential-return records → Linux host launch/inspect/export | Tagged native provenance and real process/credential proof; no fabricated appliance fields or claims of equivalent isolation. |
| F8 Database and lifetime | Shared PostgreSQL schema → JSONB/locking/transaction operations; coordinator shuts down before DB close | Native supervisor owns private cluster and stops PG last. No silent cluster replacement, Docker fallback, or presumed SQLite equivalence. |
| F9 Native UI is separate | Forgejo issue template → native content/form/review/diff modules | Shared issue/PR UI is new Lit work. Original template behavior/design is reference, not a portable component library. |

These are observed dependencies and target gaps, not newly reproduced production
incidents. Frontend/runtime independent challenge confirmed F1–F9 and added the
identity, draft, CSS, artifact-transfer and route corrections below.

## Fountain route and permission cutover

`system/containers/extension/extension.json` has seven repository feed subroute
patterns. Fountain's current SDK permits repository `feed` and at most eight
subroutes, each at most three segments. The two shared work patterns, implemented
by T07-FOUNTAIN-ISSUE-ROUTES in place of the old Factory-status tree, are:

```text
work/{kind}
work/{kind}/{item}
```

Validate `kind = issues|pulls`, and `item = new|supported item number`. Product
URLs become `/OWNER/REPO/feed/work/issues/42` and
`/OWNER/REPO/feed/work/pulls/42?tab=files`; query tabs cover conversation/files/checks
without adding manifest patterns. Factory status is folded into shared issue
detail. `soda-places.ts`, repository dispatch, header/places templates and shared
links use this same tree. The pull/editor placeholders expose native originals
until their shared screens are implemented. Desktop resolves the same typed
targets in its own router; installed/connected parity remains to be qualified.

This achieves shared screens inside the current repository/SDK constraints.
Keeping native `/issues/N` and `/pulls/N` addresses as the new mount would require
external Fountain router/template capability work. Current extension permission
`read` admits code readers, not every issue-only collaborator. D5 gates broader
visibility; each action still checks the actual issue/PR permission.

## Native lifetime and remote connection

One Rust supervisor locks the profile data root, inspects/starts its private PG,
reconciles retained executor receipts, starts Go and then admits windows. A second
launch activates that instance. Window disposal releases UI resources; it does not
stop work. Quit stops admission, cancels/drains execution, records outcomes or
uncertainty, stops Go, then stops PG. Crash recovery verifies process identity and
ownership, not PID existence alone. Sleep/wake and credential expiry trigger fresh
remote reconciliation; absent polling history remains absent.

Fountain desktop login/enrollment is a new API boundary. Prefer a supported public
client authorization flow with PKCE; inspect loopback redirect/client registration,
scope, token refresh and revocation in the D2 spike. Source currently contains PKCE
support in external `routers/web/auth/oauth.go` and `models/auth/oauth2.go`, but
does not implement our connection grant. The gateway binds the authenticated human
to explicit repositories, permitted actors/operations and runtime placement. It
uses existing private adapters server-side; it exposes neither arbitrary SDK RPC
nor credential-file paths. Candidate upload admission is separate from publication.

For GitHub, use public-client/device authorization supported by the chosen GitHub
app mode. No client secret or shared app private key ships in desktop. Local native
session admission and remote repository authority remain separate. Capability
failures are visible product states; they do not become fallback credentials.
