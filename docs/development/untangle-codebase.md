# Factory untangling decisions

Goal started 2026-11-06 at `6df3784`. This is the current structural decision log;
product requirements and open qualification gates remain in
[the implementation plan](factory-products/implementation-tasks.md). The previous
product-completion goal was cleared. This goal preserves intended behavior while
rebuilding responsibilities one section at a time. Both optional comment and
test cleanup are selected. Preserve credentials, retained state, historical
records, public JSON/command bytes, and selected Go/Rust/Lit ownership.

Initial audit: root inspected human-command/domain/store/native adapters; three
read-only agents inventoried Go, native Rust, and UI/tooling/docs. Findings below
are concrete source findings, not exhaustive statement-by-statement coverage or
runtime qualification. Each section needs implementation, affected behavior
checks, and independent review before it can be called complete. Newly discovered
findings stay with their defining section. No structural change closes a product
security or installed gate.

## Sections and decisions

### 1. Human-command journal — complete

Define capture transactions, immutable observation persistence and atomic outcome
validation once. Keep operation-specific request shapes, matching, provider
conditions and receipt authentication explicit. Preserve typed public APIs for
existing hosts; use private shared mechanisms rather than a configurable CRUD
framework.

- Hidden links: `pkg/store/factory_human_{branch_create,inline,edit,...}.go`
  repeat capture and row-locked outcome transactions. Branch and inline files are
  identical after operation naming substitutions. Twelve journal families share
  the same store protocol; PR head-repository and reviewer/team references differ.
- Hidden links: `pkg/factory/human_*_outcomes.go` repeat atomic binding/receipt
  validation and reducer entrypoints; `service/human_*.go` repeat reconciliation,
  capture-race recovery and submit/lookup/cancel sequencing.
- Special cases: edit expected-current readiness, request matching with optional
  fields, merge's existing cancellation rules, causal primary before separate
  completion, uncertain dispatch/cancellation and historical unknown fences.
  Preserve these semantics rather than homogenizing them accidentally.
- Native product adapters repeat lookup/fence/reply decoding; provider-specific
  preparation and preservation rules remain with the defining operation.
- Service redesign: one load-or-prepare helper owns missing-only preparation and
  race recovery after preparation errors. One bound effect owner owns provider
  lookup and store observations; actual submit/cancel actions stay typed.
  Successful mismatched preparation still conflicts immediately. Typed scope
  recovery, Created* destination reads and thread Selection remain explicit.
- Verification: existing native product journeys with disposable PostgreSQL and
  owned no-forwarding peers; no live provider action needed for shared mechanics.

Implemented: one atomic outcome validator, one store journal transaction owner
for all twelve families, and shared service preparation/reconciliation owners.
Typed request matching, canonical reference checks, provider interfaces, scope
recovery, destination reads, schema and persisted bytes remain unchanged. Source
review independently compared all twelve families and retained the edit/merge
differences. Native adapters now use the store's defining local dispatch lookup,
one native reply-envelope check and one failure classifier. Typed preparation,
causal receipt authentication and separate completion reads remain explicit.
Newly synthesized found lookups use their record's unresolved/legacy reason;
saved observations stay unchanged and public projections expose neither reason.

Verification checkpoint: cached/offline Go builds and dependency boundaries;
existing composed inline journey (31 cases) and edit journey (16 cases); existing
edit, PR creation, reviewer/team, merge, state, thread, inline and review storage
cases through actual native PostgreSQL restart/WAL recovery all passed. Retained
results: `/home/vince/tmp/factory-untangle-journal-1106.wbojXR/` (inline and edit2
result JSON; eight named PG logs). Fixture repairs restore the actual pre-81
column/guard before historical upgrades and replace five unrelated external
SDK-file dependencies with exact-maximum opaque JSON. Existing assertions and
native codec checks remain. Earlier setup failures are retained in that root.
This proves the changed journal/service behavior, not live providers, freshness,
installed products or a product security gate. No new tests or test roots added.
The final adapter extraction also passed independent review, the existing
16-case composed edit journey and dependency boundaries; retained result:
`/home/vince/tmp/fua-1106.xbznkH/e/edit-product-result.json`. The dispatch read
retains its existing two-read ordering; no atomic snapshot claim is added.

### 2. Go contracts and service composition — complete

Put stable tool/planning/capability contracts below HTTP and coordinator owners;
bind dependencies explicitly and define one list/detail read contract.

- `mcpwire/role_schema.go` imports `control` for planning guidance;
  `mcpserver/catalog.go` uses HTTP's capability interface. Tool schema/mode/profile
  rules recur in wire contract, bound server and catalog.
- Bound and fixture behavior fork inside serving paths; make construction explicit.
- `service/worker_context.go:117` silently obtains Priority from a Terminal type
  assertion; `control/dispatch_launch.go:728` obtains context judgment from the
  priority chooser. These are hidden second dependencies.
- `service/application.go` constructs then mutates/back-binds its service graph.
- Issue/PR query parsing and page envelopes repeat across service and HTTP files;
  merged PR state and item identity rules are genuine differences.
- `runtime/native_application.go:47` rebuilds the router per request;
  `httpapi/product.go:54` manually propagates account/store fields across routes.
  Separate immutable routing from fresh admission without caching authority.

Implemented: stable planning/capability contracts no longer depend on control/HTTP;
one tool definition table and payload-schema policy preserve discovery and profiles.
Production bound construction is separate from the existing all-tools fixture.
MCP handlers consume explicit typed ports, with legacy resolution once at public
composition. Context selection is explicit through application/coordinator/dispatch;
legacy public dispatch inputs resolve at admission. Shared issue/PR query and page
validation retains distinct item/state rules. Native root routes register once;
each request still binds fresh account authority inside coordinator admission.
Independent reviews preserved all 76 root registrations and guard ordering.

Reassessed application wiring: coordinator-owned background callbacks intentionally
use that same coordinator as admitted transport. Moving assignments into another
constructor would relocate this required cycle without simplifying ownership.
Keep it. Verification: existing MCP SDK/HTTP suite, cached Go production build,
dependency boundaries and the existing 16-case composed native/PG edit journey
passed (`/home/vince/tmp/fuc.2cFOMm/edit-product-result.json`). The first longer
scratch path failed local socket setup before the journey; it was rerun with a
short path. No connected, installed or security qualification is claimed.

### 3. Go coordinator and provider judgments — complete

Give progress one explicit pass owner and repair adoption one workflow with
cause-specific admission/persistence. Give System One one transport owner.

- Startup/progress/dispatch repeat ordered recovery and discard stage errors;
  context-value sentinels implicitly confer reentrant progress/dispatch ownership.
- `control/{repair_adoption,delivery_repair_adoption}.go` duplicate recovery,
  freshness, staging, paid-call prefix and activation logic; cause is inferred by
  querying one storage family before another; an ignored boolean survives.
- `priority_typesafe.go` and `systemone_typesafe.go` repeat auth/model/HTTP setup;
  delivery adapter knows private coordinator material. Preserve distinct bounds.
- Dormant exported MCP-key/provider/identity helpers need caller/compatibility
  evidence before removal. No internal caller alone proves an export obsolete.

Implemented one repair-adoption procedure and generation validator with closed
stall/delivery causes. Sponsorship, progress generations, journal checks and atomic
activation/save remain cause-specific. Only generic recovery resolves the historical
cause-less `RepairCycle`; changing retained formats is unnecessary for this cut.
One System One request/default-model owner preserves separate priority/generic
decoders, bounds and error mapping. Delivery material validates beside its own shape.
Independent review preserved ordering, wire envelopes and material fingerprints;
cached production Go build passed. No existing local integration exercises repair
driver lost replies/freshness/activation or the TypeSafe transport directly, so
runtime equivalence of those paths is not newly qualified by this checkpoint.

Reassessed progress: startup recovers without dispatching new work; dispatch returns
its report and advances publication; ticks settle, repair, dispatch then detect stalls.
Reentrant progress locking, dispatch-recursion suppression and readiness budget
ownership represent separate requirements. Independent review found no worthwhile
safe sequence consolidation. Keep them explicit. Inspected SodaOS production
callers still use `LoadFactoryMCPKey` and `TypeSafeKeyReadable`; retain these APIs.

### 4. Rust GitHub connector and admitted effects — pending

One private mutation lifetime owns pacing, account checks, absolute deadline,
attempt classification and receipt bounds. Each operation owns its request,
preconditions and decoder. Keep the admitted public surface semantic and fixed.

- Operation modules repeat the same lifecycle; `transport.rs` enumerates each
  operation, wrapper and HTTP/GraphQL shape again.
- `collaboration.rs` repeats command metadata extraction, body limits, write
  classification, payload validation and outcome normalization across matches.
- Special cases retained: provider pacing, original deadlines, one claim, current
  custody, uncertainty after attempted send, causal versus completion evidence.
- Legacy canonical reads in `github.rs`/`request.rs` appear check-only; migrate
  useful checks to real product paths and verify external callers before removal.

### 5. Native protocol, resources and desktop lifetime — pending

Define nonsecret broker/worker DTOs, bounds and pure validation once. Keep every
boundary's peer/process/authority checks independent. Give private filesystem
primitives their own low-level owner and RunOwner one explicit start input.

- Broker and desktop duplicate AgentStarted/AgentExecution and candidate validation;
  worker/receipt repeat fields, bounds and observation framing rules.
- Credentials, signing, run, bootstrap and supervisor import private filesystem
  helpers from `database.rs`; they do not depend on PostgreSQL provisioning.
- `factory_provider/run.rs:72` has five mechanical start variants before start_inner.
- Desktop main/bridge retain feature-only observation state and duplicate job
  bookkeeping alongside actual product jobs; readiness and admitted jobs differ.
- Repeated dependency pins belong in workspace dependencies, preserving features.
- Keep Linux/SELinux workarounds, retirement uncertainty, independent resource
  facts and actual worker isolation. No cosmetic dissolution of native owners.

### 6. Browser retained records and API contracts — complete

One small retained-record owner handles exact-byte storage/conflicts; workflows
own schemas and settlement. Put shared command outcomes and reads in API owners.

- Issue comment/editor/create records repeat unsaved/baseline/persisted-byte
  transactions and layer caches above `shared/drafts.ts`'s cache.
- Work retry/completion records duplicate scoped command retention and settlement.
- Create/edit responses and components import general outcomes from comment code;
  issue components import generic reads from `work/read.ts`.
- Preserve edit original-writing conditions, cancellation uncertainty, creation
  navigation, scoped collections, reload recovery and generation retirement.

Implemented shared writing and single-pending-record owners, retaining each
workflow's codec, key, byte order, bounds, matcher and reload/settlement rules.
Writing owners use direct persisted writes instead of stacking a second cache.
General command outcomes, capability reads and bounded API/error handling now
live in API owners; old exported names remain aliases. Request Web Locks/archive,
protected drafts, activity writing and context-choice collections retain their
distinct protocols. Independent source review found no behavior change.

Typecheck and Lit passed. Existing Chromium issue editor/create/comment recovery
cases and feed revision/review recovery passed. The existing unconfirmed acceptance
case also ran completion-decision, allowance, repair and activity recovery helpers.
Commands used the retained `factory-fountain-read-1105/soda/tests/frontend/`
drivers with `bun test --timeout 120000 --test-name-pattern ...`. Two broader work
cases stopped on stale host-link expectations before run-retry/verification helper
chains; those full journeys remain unverified here. No assertions were weakened
and no test source added. Their unchanged codecs use the exercised shared owner.

### 7. Browser workflow/lifecycle owners — active

Replace callback bags with semantic lifecycle owners; compose visible snapshots
in Lit elements. Extract workflows with their retained state, not loose methods.

- Watch display/screen/request expose dozens of private getters/setters/callbacks
  assembled by Watch. Reconnect bounds are actual behavior, not arbitrary waits.
- Request is 3,806 lines and passes 50-plus fields/methods to its view; feed owns
  timeline and several writing/review transactions together.
- Request view's planningUnavailable is always false: remove speculative surface.
- API IntentGeneration is decoded inside a rendering/model module.
- Role labels/portrait keys are a smaller defining-data consolidation candidate.

### 8. Integration checks and test cleanup — pending, cleanup last

Define shared fixture sessions and explicit case/phase protocols in existing
check owners. Keep every distinct behavior assertion; remove only proved useless
or implementation-bound checks after structural replacements work.

- Native GitHub scenarios repeat admitted calls, fixture files/barriers/counters;
  edits/locks/states obtain generic helpers from issue scenarios. A nominally
  generic uncertain assertion checks an issue-specific result.
- Native check CLI hand-maintains a large scenario argument ladder.
- `store/postgres_fixture_test.go:85` chooses >20 cases by ordered env-var returns;
  setting two silently selects one. Use one explicit case/phase contract.
- Browser diff assertions and Rust required-name lists diverge (missing
  unknown-thread-retained in host list). Put the scenario contract in one owner.
- No initial audit proved any existing test worthless. Do not delete coverage
  from line count alone. Retain real compositor input and connected boundaries.

### 9. Build, assets and active documentation — active

Stage reported build artifacts and define UI assets/notices together. Make active
guidance describe this repository; preserve historical evidence without copying
it into current operating instructions.

- `scripts/build-desktop.ts:28` inherits CARGO_TARGET_DIR then copies target/debug;
  output/payload overlap checks cover only one direction.
- `factory-browser-assets.ts:56` omits the existing @pierre/diffs notice while
  production diff imports it. Keep the external Bun/WebKit color workaround.
- testing/ownership/assets guidance describes absent predecessor paths, commands
  and workflows; Lit README conflates compiler checks and Lit fixtures.
- Unitban allowlist and formatting/lint config retain absent predecessor paths
  and redundant settings. Preserve the unit-test prohibition itself.
- Current plan/handoff mix active scope with very large historical evidence;
  keep open gates discoverable and history intact without another verbose ledger.

## Verification and completion

Use existing affected checks: production Go build and dependency boundaries,
actual disposable PostgreSQL/native product checks, affected Cargo/native paths,
TypeScript/Lit/browser journeys and build staging as appropriate. Source checks
support their actual scope only. The final independent pass must revisit every
section and resolve worthwhile remaining findings. Completion requires preserved
intended behavior; missing features and original provider/installed/security
qualification remain explicitly unfinished in their owning product plan.
