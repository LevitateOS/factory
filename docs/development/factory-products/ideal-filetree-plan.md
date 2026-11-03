# Factory products implementation plan

Status: implementation in progress, 2026-11-03. The owner selected Linux first
with Tauri. The source now lives in the public Factory repository. Shared UI,
Storybook, native lifecycle and provider/service producers are implemented;
connected execution and installed product journeys remain unfinished.

## Outcome and scope

Deliver two hosts for one Factory product: the Fountain extension inside Soda
Forge and an installable Linux desktop application, followed by macOS. Both consume the same Lit
feed, issue, PR, conversation and review components. Factory remains a Go engine;
Rust owns native execution, credentials and desktop lifecycle. Issues and PRs
remain authoritative records on Fountain or GitHub.

Desktop must run work on the host computer without a CoreOS container, Fountain
installation, Docker installation or manually administered database. Connecting
desktop to an existing Soda Forge is also supported. Storybook consumes the
production components; it is the development workbench, not the sharing mechanism.

This is a targeted product-extraction plan. Canlang, the appliance installer,
unrelated applications, the predecessor/Updates platform and a complete repository
correctness audit are outside scope. Their existing ownership remains intact.
Windows desktop distribution and multiple independent controllers for one GitHub
repository are outside the first release. Native qualification must run on the
selected platform. Later macOS qualification requires the actual supported Mac
architecture; cross-compilation is not that evidence.

## Read the plan

| Document | Owns |
| --- | --- |
| [Seams and source map](seams-and-source-map.md) | Current evidence, canonical owners, conflicts and workflow boundaries |
| [Reuse decisions](reuse.md) | Existing source donors, upstream libraries, licenses and selection spikes |
| [Target tree](proposed-tree.md) | New defining files, retained owners and cutover joins |
| [Implementation tasks](implementation-tasks.md) | Canonical slice dependency graph, sequential queue, progress and completion journeys |

## Product decisions and selected engineering approach

The owner selected separate products in the same repository, a native desktop
experience with local execution, shared changes across desktop/Forge, automatic
repository-driven work, and first-class issue/PR screens. The following are
engineering selections for this plan, rather than claims of implemented behavior:

1. Keep Go workflow/domain/storage ownership and Rust system ownership. Use Lit
   throughout the product UI. Do not port the engine to Rust or introduce React.
2. Use Tauri as the native shell with a bundled Go executable. One Rust supervisor
   owns the local database, executor and backend lifetime. Close-window keeps the
   application running; explicit Quit stops admission, retires work, then stops
   the backend and database. No launch agent or always-on daemon initially.
3. Preserve PostgreSQL for the first desktop store. Privately provision a native
   per-user cluster, with no user database setup. Prove packaging/start/recovery
   early. SQLite is a separate semantic port, not a driver substitution; do not
   combine it with this extraction without evidence that the native PG path fails.
4. Canonical repository identity is a local stable ID mapped to provider instance
   plus opaque remote repository ID. Credential connections and grants are
   separate. Reauthentication must not create another repository/work ledger.
5. Shared issue/PR views become the normal Soda product navigation in both hosts.
   Existing native provider URLs remain explicit external destinations. The
   current Fountain protocol allows a same-repository route cutover without
   replacing its native issue/PR handlers; see the source map.
6. Prefer Pierre Diffs through its vanilla API, subject to an early browser,
   WebKit, CSP and review-anchor proof. Keep one renderer after selection.
7. GitHub desktop uses authenticated current-state polling and wake reconciliation
   initially. Events prompt fresh reads; event delivery does not grant execution
   authority. Unsupported conditional operations remain visibly unavailable.
8. A desktop connected to Fountain uses an authenticated collaboration gateway
   for its private operation protocol, including candidate artifact transfer.
   Private service admissions and appliance credential files never leave it.
9. GitHub automation initially acts as the connected account. The interface names
   that account. A shared GitHub App signing key is never bundled with desktop.
   Bot installation identity is a separate deployment mode requiring trusted
   token issuance or an operator-owned app configuration.

## Separate public repository

The owner requested a public [Factory repository](https://github.com/LevitateOS/factory)
before finishing the product. The initial import commits preserve the current
product source and both complete integration working-tree patches. Original
checkouts were preserved at import. Source provenance and patch base revisions are
recorded in [`docs/source-import.json`](../../source-import.json).

The extracted Go module is `github.com/levitateos/factory`. Its public `pkg/`
owners include the engine, services, store, application, collaboration and required
identity/project definitions. The native server is the default build; its
dependency graph excludes SodaOS, Fountain and the extension SDK. Rust has its
own desktop/runtime, Unix transport, workbench and native-check workspace. Shared
UI/Storybook inputs include the actual fonts, theme, portraits and notices.

T04-REPO-GO now switches Forge to the pinned published Factory module and removes
its duplicate Go owners, preserving appliance composition and native Fountain
adapters. Forge commit `a1cd0499` passes production builds, integration compilation,
vet, dependency checks and the real issue-editor/native write/recovery journey.
The broader SQL-locality check remains red on existing Forge adapter/test usage;
no exemption or full-source pass is claimed. T04-REPO-UI now removes the old UI,
Storybook and shared-design/build copies and consumes Factory through a standard
Bun source link. Forge commit `5e695dfb` passes actual asset/physical-source graph,
single-Lit, browser/native-write and native staging checks. Local setup uses an
editable checkout; clean/CI setup fetches the published commit selected by go.mod.
The dev browser activation refused because no development deployment is active,
so deployed parity remains unqualified. T04-REPO-NATIVE now removes the remaining
Rust runtime/desktop/workbench/Unix-transport copies in Forge commit `6bb9b60d`.
Six retained native consumers use the defining Factory Git crates at the same
published source revision. Workspace/driver compilation and Cargo graph checks
pass; the rebuilt Forge driver passes the actual managed PG/Go retained-data,
restart, crash, key-rotation and ordered-shutdown journey. Generated schemas and
unrelated staged work remain preserved. This proves local native source
consumption, not connected credentials/execution or an installed package.
Installed parity remains later. Fountain's SDK distribution remains its owning integration
task; the SDK is not copied into the standalone dependency closure. Historical
coordinator tests that instantiate appliance/SDK owners are retained as source
under `integrations/soda-forge/`, while store tests remain active in `pkg/store/`.

Do not mark connected, installed, extraction-parity or product gates complete
because the new repository builds. The owner supplied the public GitHub OAuth
client ID, and T06-GITHUB-DEVICE now qualifies a real unapproved challenge plus
the private native authorization transport. T06-GITHUB-CUSTODY retains complete
encrypted grants and enforces access/refresh expiry at native use.
T06-GITHUB-ROTATION reserves one durable attempt and conditionally publishes a
complete replacement without replaying uncertain refreshes. T06-GITHUB-REFRESH
wires fixed-origin exchange and fresh account verification to that custody owner,
qualified with actual provider refusal and declared positive responses.
T06-GITHUB-SIGNIN now provides native desktop sign-in/browser handoff, cancellation
and saved-account controls, qualified with actual WebKit and an unapproved GitHub
challenge plus declared positive authorization. T06-GITHUB-LIVE now additionally
qualifies owner-approved sign-in, verified identity, encrypted grant reload, two
authenticated repository pages, private repository detail and an actual successful
provider refresh followed by another authenticated read. The isolated test account
was removed locally; remote OAuth revocation has not been exercised.
T06-GITHUB-IDENTITIES now atomically records observed account/connection bindings
and repository associations in the existing registry, preserving reconnect/rename
and refusing scope changes or unadopted retained data. Real native PostgreSQL
checks prove concurrency, rollback, restart and WAL recovery. Provider-verified
native composition and normalized collaboration reads still need to consume
these mappings. Connected feed and installed journeys remain unfinished;
they do not block independent Fountain implementation or source preservation.

Extraction checks on 2026-11-03, using the imported dependency versions:

- The native Go executable builds with no special build tag; `go vet ./...`
  and the transitive provider/appliance boundary check pass.
- The production shared UI emits 20 assets from 76 shared sources. Desktop UI,
  TypeScript, Lit analysis and the existing Go/TypeScript test-root guards pass.
- Storybook builds 195 entries, including all five issue-editor states.
- The independent Rust workspace checks, and the Linux Tauri `native-ui`
  executable builds using the existing owned GTK/WebKit sysroot. This is build
  evidence, not a new window/input or installed-app qualification.
- Factory's own PostgreSQL driver, Go backend and store integration binary pass
  the real managed native journey: retained transactions/restart, duplicate-owner
  refusal, backend/database/owner crashes, startup failure, key rotation and
  ordered shutdown. The new private profile is retained and stopped.
- Both complete recovery patches pass `git apply --cached --check` against their
  recorded base revisions using separate temporary indexes. The original
  worktrees and indexes are not modified.

Local extraction logs are under
`/home/vince/Projects/.factory-extraction-checks/`; the owned stopped native
profile is `/home/vince/Projects/.fx-pg1`. These local artifacts and private
profile are excluded from the repository. No real provider login, model run,
issue/PR mutation or deployment was performed for the extraction.

## Decisions that gate specific work

| ID | Required conclusion | Dependent work; independent work can continue |
| --- | --- | --- |
| D1 | Adopt an explicit GitHub standing automation policy over current verified facts, or retain original-author creation evidence as mandatory. Recommendation: the former, with eligible authors/forks, actor, repository, limits and accepted content revision recorded. | Automatic GitHub intake activation, not read/write UI or connector development. Until resolved, discovered work waits for explicit maintainer adoption. |
| D2 | Prove Fountain desktop enrollment/login and revocation using supported native authentication. Existing OAuth PKCE code is evidence for a spike, not proof the new gateway is admitted. | Remote Fountain operations; no spoofed browser authority or raw Unix socket exposure. |
| D3 | Qualify native same-user execution as a distinct trusted-host mode. It does not inherit appliance UID/container isolation. Determine and document which role/custody invariants the native executor actually enforces. | Selectable native execution and unattended work, not desktop browsing or Storybook. |
| D4 | Select native PG packaging and diff renderer from the early spikes. Resolve fresh official artifacts and actual target behavior. | Shipping those components, not the independent extraction packets. |
| D5 | Current repository extension admission requires code-unit read. Issue-only collaborators require a Fountain capability change for full native visibility parity. | Full collaborator parity; initial Factory/code-reader journeys can use current extension routes. External Fountain edits need their own authorized implementation scope. |

These are precise implementation gates, not requests to stop planning. The task
list includes their producers and useful work that proceeds independently.

## Inspection identity and evidence limits

The source identities and review descriptions below describe the original
planning inspection. They are historical inputs, not identities of the current
implemented working tree. Current implementation evidence is recorded here.

### Implemented and checked

- T07-ISSUE-EDITOR supplies the shared title/Markdown editor with explicit original
  content conditions, changed-field delivery and durable draft/pending custody.
  Fountain and the standalone frame mount the same element beneath current
  primary-read/account gates. Actual browser/native SQL/PostgreSQL checks prove
  stale review, a second-client race with native cancellation, lost-reply recovery,
  original retry/cancel, title-only/empty/full-size updates and readback. Browser
  writing/account/realm checks preserve newer drafts and retired replies; emitted
  standalone port checks, five built stories and both product asset builds pass.
  Cross-provider/installed qualification and PR flows remain consumers.
- T07-ISSUE-EDIT-BASE binds original external ID/title/Markdown digest to the
  existing edit journal and trusted native preparation. Stale saves obtain a
  real cancellation tombstone before dispatch; capture-to-dispatch races retain
  native CAS. Actual Fountain/private HTTP/native SQL/PostgreSQL qualification
  covers issue/PR stale conditions, changed-intent refusal, lost cancellation and
  retained reopen, with canonical contradiction fencing. The shared editor is
  the next consumer; cross-provider and installed qualification remain pending.
- T07-ISSUE-CREATE supplies the shared title/Markdown form, current capability/
  result decoding, scoped durable writing and pending-command/result custody.
  Fountain's normal new-issue route and the standalone frame consume it, with
  exact typed navigation to the returned issue. Actual Chromium/private API/
  native SQL/PostgreSQL qualification covers creation, original retry, cancellation,
  full UTF-8 byte bounds and lost-reply recovery. Browser storage/account/realm
  checks preserve newer writing and retired outcomes; Storybook and both product
  asset builds use the production component. Installed/connected qualification
  and ordinary title/body editing remain consumers.
- T07-ISSUE-CREATE-DESTINATION returns the exact canonical issue selected by the
  original authenticated creation receipt and a fresh normal issue read. Actual
  Fountain/native SQL/PostgreSQL checks preserve that destination through lost
  replies, replay and Store/API reopen; native permission withdrawal hides the
  destination without erasing a committed result, and mid-read account retirement
  suppresses the outcome. The same checks caught and corrected the frontend
  response bound for full escaped issue Markdown. The shared new-issue form is
  the next consumer; no installed UI or deployment claim is made.
- T07-ISSUE-COMMENT supplies the shared ordinary comment composer, canonical
  capability/outcome decoding, scoped draft/pending custody and optional shared
  frame ports. Fountain mounts it under the primary issue read gate. Actual
  Chromium → frontend/private API → native SDK/SQL and PostgreSQL checks prove
  three human comments, lost-reply lookup, original-command retry and cancellation
  without duplicate effects. Browser checks preserve newer writing, cancellation
  intent, account/realm isolation and retired replies; four production stories
  render the same component. No deployment or installed desktop claim is made;
  new-issue/edit forms and connected/installed journeys remain consumers.
- T07-FOUNTAIN-ISSUE-ROUTES mounts shared issue list/detail/Factory facts beneath
  Fountain's native header, with the preserved existing workflow controls gated
  by the current issue read. Manifest, native templates and request/feed/work
  links use `/feed/work/issues`; old status routes have no alias. Native
  manager/SQL/callback route admission and actual header rendering pass, as do
  browser navigation, Back, scoped heading/comment jumps and access withdrawal.
  Local source qualification is not deployment or installed desktop evidence;
  shared pull/editor and authenticated media remain unfinished consumers.
- T07-FOUNTAIN-ISSUE-HOST supplies the Fountain shared-issue context adapter:
  current canonical/native/account binding, repository-relative Markdown and
  separate cancellable native/external navigation, with a scoped read transport.
  Actual Chromium/HTTP checks render shared list/detail/conversation and reject
  changed bindings, revoked metadata and retired reads/links. Metadata inputs are
  captured native replies; issue/comment bodies are declared HTTP peers. The
  normal manifest/header/URL cutover and installed journeys remain consumers.
- T07-FOUNTAIN-NAVIGATION supplies live configured default-branch metadata through
  the existing admitted SDK/canonical identity routes and browser host decoder.
  Real native/HTTP and captured-response browser checks cover changed/unknown
  configuration, permissions, sessions and retirement. The coherent Fountain
  manifest/header/URL cutover remains a later T07-READ consumer.
- T07-ISSUE-COMPOSITION mounts the shared list/detail and optional matching
  Factory journey in the production application frame. Typed issue and
  work-detail routes stay distinct; current account/resource bindings and
  coordinated read retirement pass actual browser and emitted Storybook checks.
  Fountain manifest/header/URL cutover and connected desktop qualification remain
  T07-READ; no installed or provider-connected completion is claimed.
- T07-ISSUE-DETAIL supplies the reusable read-only description/conversation
  component, sanitized Markdown and host-owned link ports. Ten production
  stories and actual Chromium journeys cover scope, paging, large/untrusted
  writing and retired reads/navigation. Factory facts and connected host
  navigation/desktop qualification remain T07-READ; mutations remain T07-EDIT.
- T07-ISSUE-LIST supplies the reusable production issue list, canonical read
  projection and shared stylesheet, exported by the standalone browser bundle
  and consumed by eight stories. Actual Chromium checks cover filters, paging,
  exact IDs, access loss and retired reads/navigation. Detail/conversation,
  Factory facts and connected Fountain/desktop navigation remain T07-READ.
- T10-WRITES completes the local Fountain human-operation backend boundary:
  journaled issue/PR creation, comments, title/body edits, lock/state changes,
  reviews, inline comments, thread resolution, reviewer requests and allowed
  merges use the shared Product/Application and admitted native adapter.
  Real native/HTTP/recovery evidence and supported bounds are recorded in the
  canonical backlog. Shared editor navigation, desktop HTTPS enrollment,
  execution/publication and installed journeys remain their later slices.
- T06-FOUNTAIN completes human-admitted collaboration reads at the local installed
  extension/separate-Factory boundary. Actual native repository, issue, PR, diff,
  review, thread, status, protection, Actions and capability journeys pass,
  including private access/revocation and retained identity. Exact evidence and
  remaining host/UI/product gates are in the canonical implementation backlog.
- Production Fountain entries now import `frontend/factory/feed/feed.ts` and its
  extracted support modules. Catalogue, links, navigation, optional application
  inventory and draft scope come from a typed host adapter. Shared production
  modules do not import Fountain/Spaces code.
- T04-UI closes the shared component boundary after its scoped draft/action and
  composition slices. `bun run build:factory-ui` now emits the production
  application module and offline styles/assets independently of Forge/Storybook.
  The browser composition journey uses those emitted files. The existing
  Storybook index contains 160 production-source examples across eighteen groups;
  installed native lifecycle, credentials and connected journeys remain later work.
- Shared feed/conversation CSS imports canonical branding fonts, palette and the
  new `assets/branding/theme/tokens.css`. Fountain retains document theme rules.
- Storybook 10.6.5/Vite 8.3.5 builds the production component with six development
  states. `tools/factory-ui` embeds those assets using Tauri 2.12.5 on Linux. This
  is a development workbench, not the desktop application or a backend bridge.
- Passed: full TypeScript/Lit/source guards; 26 existing feed browser journeys;
  added standalone-host journey covering scoped drafts, routing and dark theme;
  three existing extension asset graph/color/token checks; Storybook static build.
- The actual Linux Tauri/WebKit rendering check passed: three production feed
  cards, light background `rgb(255, 255, 255)`, dark `rgb(16, 16, 16)`. It used a
  private Weston compositor and downloaded GTK/WebKit development sysroot under
  `/home`, without installing system packages. The headless compositor has no
  keyboard seat, so this proves rendering rather than native input interaction.
- Native UI dependencies are behind an explicit `native-ui` feature. The normal
  `soda-factory-ui` Cargo check passes without GTK/WebKit development libraries.

T00-PG is complete: the Rust native database component and real three-phase store
proof qualify PostgreSQL 18.6 on Linux with a relocated payload. See the
[runtime guide](../../../lib/desktop-runtime/README.md) for source/size/timing and
platform limits. This is the database lifecycle producer for the later supervisor.

T01-REGISTRY is complete: canonical identity records, additive PostgreSQL registry
and same-host bindings are implemented and integration-checked. Opening storage
does not choose the namespace for historic numbers. T01-ID was split before
implementation. T01-LEDGER is now also complete: explicit configured-source
adoption preserves retained IDs/history, reserves new ranges atomically, refuses
wrong/ambiguous sources and survives rollback/retry with mixed-provider data.
The actual native PostgreSQL checks passed. See the
[identity guide](../../../internal/collaboration/README.md).

T01-RECORDS is complete: neutral collaboration reads/capabilities/anchors, their
Lit mirror and production diff/anchor decoders passed compiler/source checks and
a Go-to-client JSON probe. [Contract and evidence](../../../internal/collaboration/records.md).
The former T01-PORTS packet was split before implementation into T01-RECORDS,
T01-EFFECTS and T01-RUNTIME. T01-EFFECTS is also complete: neutral graph/operation
contracts and validation preserve conditional guarantees, operation namespaces and
partial/uncertain checkpoints. SDK wire and existing-journal retention checks
passed through native PostgreSQL restart/crash recovery.
[Contract and evidence](../../../internal/collaboration/effects.md).
T01-RUNTIME is complete: SDK-free admitted-actor, standing grant/policy,
native/appliance provenance and runtime placement contracts passed scope, kernel
process and native PostgreSQL retention checks.
[Contract and evidence](../../../internal/factory/access/README.md).
T00-DIFF is complete: the shared production Lit wrapper selects Pierre 1.6.2 and
passed actual Fountain HTML/policy, stronger CSP and Linux Tauri/WebKitGTK checks,
including trusted keyboard input, immutable drafts, virtualization and disposal.
[Renderer and evidence](../../../frontend/factory/review/README.md).
T02 was split before implementation into intake, graph, execution and identity
consumer cutovers followed by complete-engine/provider qualification.
T02-INTAKE is complete: opaque provider proof and neutral semantic issue-maker
records preserve retained PostgreSQL grants/retries/slots; the factory/store
production import closures are SDK-free. Subsequent T02 slices own its consumers.
[Boundary and evidence](../../../internal/factory/issue_intake.md).
T02-GRAPH is complete: graph plans, source/creation facts, operation outcomes and
receipt consumers use neutral ports. Fountain's codec preserves approved native
packet encodings/digests after JSONB round trips. The store uses decoded facts
matched to opaque provider JSON for its delivered-history guards. Private Unix
transport and native PostgreSQL retention/adoption/repair checks pass.
T02-RUN-OBSERVATIONS has since completed. T02-RUNS is active. Its producer supplies
tagged run/custody observations, typed
absence and an appliance adapter. Actual private Unix clients and the Rust broker
over native PostgreSQL passed metadata/seal/restart checks. No agent execution
or acquisition ran. The coordinator now consumes neutral run/custody observations
and candidate export; typed errors replace message matching, and its production
dependency closure excludes the SDK and concrete host. Retained run/tool/admission
variants and the remaining command boundaries are incomplete. Canonical ID cutover
and actual Fountain qualification remain their later T02 gates.

Pending: actual admission/execution/placement consumers, full desktop supervision, portable backend services/admission, real
desktop account/executor bridge, provider identities/connectors, shared issue/PR
screens, installed journeys and distribution. The Darwin dependency probe still
fails in the external SDK's Linux peer-credential code; Linux-first work proceeds
while the planned provider/composition extraction addresses that later closure.
The required `bun run dev:ui` browser update built assets, then stopped because
`soda` has no active development deployment. No browser assets were activated;
deployed Forge parity remains unverified. No full remote deployment,
provider/model execution or repository mutation was run for this implementation.

### Original planning inspection

Checkout: `/home/vince/Projects/sodaos`. Inspected HEAD:
`c5370e1bafee95adb3274042d3c8f45d774ea365`; tree:
`bea01343bb52e2848881b1ee3908849d43a5a895`; 2,697 tracked paths.
Tracked dirty diff SHA-256 before planning:
`07a92db50c3b3890851044bf910a32e05f2a06957e591d8c978b6d648822d000`.
The 22 in-scope untracked process-split paths listed by the current Git inventory
have aggregate `sha256sum`-listing identity
`ceae93aed5dc53f03c5077b7396316ec3448cd6fb5b0b42f8aa9ab72517d25ef`.
These identify inspected source; they are not a freshly qualified release baseline.
Existing working edits and unrelated native-support/release changes are preserved.

Read-only source reviews covered the seams named in the source map, manifests,
representative transactions, runtime bindings, native routes and build joins.
Existing feed/Factory behavior and tests were inspected; no implementation test,
provider/model call, native app build, database operation or deployment was run
for this planning turn. Earlier process-split test results do not qualify these
new desktop/GitHub paths.

Three independent reviewers participated: frontend primary `frontend_plan`,
runtime primary `runtime_plan`, and donor/scope challenger `donor_review`.
Frontend and runtime also challenged each other's boundaries. Their corrections
are incorporated in the source map: stable host IDs, scoped drafts, theme assets,
Fountain route limits, managed PG ownership, publication transfer, historical
authority and unknown remote outcomes. This is source review, not native proof.

| Dimension | Status for this plan |
| --- | --- |
| Responsibility coverage | Complete for the named product seams; unchanged source families are retained. No claim of line-by-line whole-repository coverage. |
| Workflow tracing | Established source paths traced for feed, intake, creation recovery, publication and process lifetime. Desktop/GitHub workflows are specified targets, not implemented paths. |
| Independent challenge | Completed for consequential UI/runtime/reuse choices; unresolved product/protocol decisions remain D1–D5. |
| Target allocation | Defining owners and implementation packets allocated. Exact SDK/auth changes for D2/D5 and platform artifact selections for D4 require their named spikes. |

Upstream metadata was checked against official repositories/docs for candidate
libraries. No dependency or lockfile was changed. Re-resolve current upstream
inputs before implementing/building, following [freshness](../freshness.md).

## Completion standard

An installed Linux desktop can connect a real repository, render its issues/PRs inside
the app, automatically admit eligible work under an explicit policy, execute on
the host, publish a PR, receive review feedback, correct it and recover after
restart. The Fountain host performs its corresponding journey using the same
components. A subsequent shared component change appears in both builds.

Read-only browsing alone, a Tauri window around a website, an executable that
still needs the appliance, or Storybook screenshots do not meet this standard.
Unsupported provider guarantees must remain explicit rather than appear ready.

Maintain this folder in place as packets land. Replace stale target allocations
with actual ownership, move implemented contracts into their existing product,
trust and reference owners, and refresh affected source identities/evidence.
The owner has authorized a goal to implement one slice at a time. Follow the
canonical dependency table and execution rule in the backlog: preserve existing
progress, implement/verify one ready slice per goal turn, then continue at the
next slice boundary. Do not dispatch parallel slices or create a competing plan.
This authorization does not turn a partial slice into completion of the product
or grant publication, remote repository mutations or destructive deployment.
