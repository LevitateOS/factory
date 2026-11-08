# Factory untangling decisions

## Current redesign — 2026-11-08

Status: D1–D8 implemented and verified within their recorded local scopes.
Product qualification and all broad security gates remain open.
The decisions below came from step 3 of
`untangle-codebase`, based on the [structural audit](factory-products/untangle-audit-2026-11-08.md)
at Factory `502147c`, consolidated in canonical main through `42f2684`.
The old product implementation goal remains cleared. Optional comment/test
cleanup is outside this design pass.

### Ownership and compatibility decisions

| Owner | Defining responsibility | Dependencies |
| --- | --- | --- |
| Go application/service | Product admission, workflow and host composition | Stable product contracts, Store, admitted semantic ports |
| Go Store | Product transactions, command journals and retained schema | Product contracts and PostgreSQL; no native resource custody |
| Rust runtime | Authenticated provider sessions, native effects, cancellation and retirement | Native capability/resource owners; no product SQL writes |
| Lit work-detail screen | Current read generation and presentation state | Existing typed API observations and explicit child events |
| Fountain Go host | Its native ownership, operations and gateway | Its ledger and dependency-neutral execution contracts |

Calls remain UI -> Go product service -> admitted native operations -> Rust.
Go records intent/outcome through Store. Fountain host work stays in Fountain.
Public JSON, command identities/digests, persisted receipts, actor attribution,
unknown outcomes and withdrawal/retirement guarantees remain compatible.

### D1. Compose a complete native work feature in one binding

**Design:** `runtime/native_product.go` owns a fixed work-feature binding using
the existing `service.Issues`, `RunStatus`, `Interventions` and Coordinator.
Each request supplies one current bound account; it supplies all required ports
or returns an explicit unavailable result for the affected feature. Existing
read-only browsing remains available without admitted execution.

Bind issue Status to Coordinator acceptance evidence; Authority to current
local-resource plus provider admission; Membership to the enrolled native
workspace/profile. Bind run repository identity through canonical Store records,
Inspect through admitted native run observation, Availability/Writer to the same
Coordinator, and RecoveryTarget to an actually admitted recovery destination.
A stored project ID or login alone never supplies these facts. Required-port
validation belongs in this feature binding, not in a generic service registry.

**Problems solved:** audit 1: Issues/RunStatus constructed with mandatory ports
unset, and run Commands not constructed at all.
**Removes:** duplicate assembly conventions and partial service literals.
**Retains:** canonical services, one coordinator/ledger, per-request account
revalidation and explicit unavailable execution/recovery. Missing adapters are
unfinished integration work; completing them is not a line-reduction claim.
**Proof:** existing composed native HTTP journey must read an actual recorded
issue/run and perform an already-supported intervention with its exact receipt;
missing authority/inspector/recovery ports must hold that action. Fixture scope
remains distinct from connected and installed qualification.

Completion (2026-11-08): one account-bound work binding supplies complete
issue/run/intervention services from the same Store/Coordinator. Canonical run
scope, enrolled native membership and Rust-admitted recovery destinations replace
inference from project/login IDs; missing adapters hold affected controls.
Production Go, Rust runtime/checker builds and dependency boundaries passed.
The existing native issue product check (`SODA_NATIVE_CHECK_WORK=1`, existing Go
helper) passed recorded issue/run reads, exact pause receipt and refreshed controls,
read-only/withdrawn account refusal and missing inspector/authority/recovery holds.
Result: `/home/vince/Projects/d12-native-verified/work-detail-result.json`;
reproduction: `bash /home/vince/Projects/work-native-session.sh NEW_SCRATCH_ROOT`.
Provider/history are declared; no worker, connected or installed qualification.

### D2. Make the work-detail screen own its read generation

**Design:** `work/detail.ts` loads issue detail/journey and repository access once
per refresh and publishes one locally scoped, typed snapshot. Reuse the existing
decoders. The snapshot is bound to transport generation, repository, issue and
refresh generation; it does not claim an atomic provider snapshot across HTTP
reads. A newer refresh or context change cancels/discards older responses.

`journey.ts` renders the supplied journey. Acceptance and completion-retry
children receive the same observation/access generation and retain their existing
write transports and pending-command records. Child refresh/action-success events
ask the parent to reload. Story hosts supply snapshots directly; they do not
require another runtime loader. Failed current access disables controls while
retained history is explicitly stale. Server mutation admission stays authoritative.

**Problems solved:** audit 4: duplicate reads and parent refresh leaving sibling
views on older snapshots; reinforces D1's usable inspection/intervention journey.
**Removes:** child issue/access GET orchestration and independent read lifetimes.
**Retains:** local editing/pending writes, stable draft scope, control-specific
write behavior, navigation ports and backend checks.
**Proof:** existing browser journey must show parent/children advancing together
after refresh and an action, bounded issue/access reads, and old replies ignored
after account/repository changes. Retained commands must survive refresh unchanged.

Completion (2026-11-08): the screen publishes one scoped observation per refresh;
children consume it and request shared refreshes. New refresh/context generations
abort/discard older reads. Failed current reads retain labeled stale history with
controls disabled; local editing and pending write identities survive refresh.
Typecheck/Lit and Chromium passed exact acceptance, lost completion-retry replay,
unchanged retained bytes, one issue/access GET pair per refresh, failed access and
old replies delivered after newer refresh/account/repository observations:
`TMPDIR=/home/vince/Projects bun /home/vince/Projects/d12-browser.ts`.
The existing requirements/code-prerequisite browser journey also passed from
`/home/vince/Projects/d12-work-controls.test.ts` (scratch copy corrects obsolete
host-link expectations only; retained original unchanged). Declared browser scope;
no connected/installed claim. The old product queue remains cleared; D3–D8 stay open.

### D3. Separate historical migration input from mutable fresh-schema text

**Design:** `store/factory_delivery_repair_schema.go` owns explicit known prior
and current constraint expressions. The current expression may also be used by
fresh-schema construction; the prior expression is an intentionally frozen
historical contract. Remove extraction by column delimiter and final-branch text.
Keep PostgreSQL normalization and exact retained-constraint comparison.

The existing version-96 label alone does not prove this initializer's constraint
transition ran. Keep the bounded idempotent comparison until the shared migration
owner explicitly versions that transition. Any later version advance must commit
with its constraint change, and must admit both already-current and known-prior
profiles. There is no additional migration registry or version ledger.

**Problems solved:** audit 2: formatting/branch ordering in fresh DDL controlling
old-profile startup.
**Removes:** CREATE TABLE scans, delimiter cuts and suffix-derived old definitions.
**Retains:** transaction/row locking, exact known-form admission and refusal of
unknown constraints. No normalization of unknown schemas into accepted state.
**Proof:** existing PostgreSQL upgrade/reopen cases for known-prior, already-current
and unknown constraints, plus preservation of stored rows/receipts. Reformatting
fresh-schema construction must not change an old profile's upgrade input.

Completion (2026-11-08): prerequisite review confirmed D1's complete native
bindings and retained HTTP/PG receipts/refusals; the retained D2 Chromium journey
was rerun and passed shared refresh, exact replay and stale-response checks.
D3 now owns frozen prior/current checks; fresh DDL uses the current definitions
with different formatting. PostgreSQL normalization, exact comparison, transaction
and schema-row locks, unknown-state refusal and version 96 remain unchanged.
The existing `TestOpenCreatesOnlyTheCurrentSchema` passed through
`soda-native-pg-check --once` on disposable native PostgreSQL: known-prior/current
reopen, exact fresh-versus-upgraded definitions, unknown refusal and unchanged
ledger rows/command receipts/version marker. Output:
`/home/vince/Projects/d3-pg-verified.log`. No new registry or version inference.
D4–D8 remain pending; the old product queue remains cleared.

### D4. Make coordinator serialization explicit and cancellation-aware

**Design:** replace the private ordinary progress mutex admission with one
context-aware gate. Acquisition returns context, release and error; cancellation
before or immediately after acquisition returns without starting work. Update all
existing progress-pass callers together. Only an internally owned pass context
permits nested reuse. Retain one Coordinator and its existing durable journals.

Separate observation I/O from protected transitions where existing journals and
revision guards already permit it: read immutable observations outside the gate,
then acquire and revalidate captured state/authority before recording or using
them. An external effect leaves the gate only after its existing durable intent
has claimed ownership; its existing outcome path reconciles the reply. Without
that ownership or revalidation, retain bounded serialization. Do not infer
per-repository independence or introduce new queues, leases or worker schedulers.

**Problems solved:** audit 3: expired interactive callers waiting on a mutex and
unrelated observation I/O extending the shared critical section.
**Removes:** uninterruptible admission and unnecessary ownership around pure I/O.
**Retains:** dispatch/publication ordering, reentrant internal passes, existing
revision/admission transactions and uncertain-effect handling. Global conflicting
transitions remain serialized.
**Proof:** existing coordinator/native checks must hold one slow observation while
an expired waiter exits without effects, then show a current request succeeds;
changed revisions must invalidate observations fetched outside the gate.

Completion (2026-11-08): prerequisite code, D1 native receipts, D2 browser
checks and D3 PostgreSQL upgrade/reopen evidence were reviewed; no gap remained.
One context-aware gate replaces the progress mutex at all ten acquisition sites,
including zero-value Coordinators. Cancellation is checked before/after acquisition;
only a live private pass owner permits nested reuse. Initial graph ownership I/O
runs before admission; the accepted head/digest and actor are revalidated under it,
and existing native checks still precede generation admission/submission. Check,
settlement, dispatch, publication/merge and completion sequences retain bounded
serialization where their coupled transitions lack a safe independent handoff.
Production Go/helper builds and dependency boundaries passed. Existing native
PG/helper `progress-gate` check passed: slow check observation, deadline waiter
with no command/assessment effects, all public callers' pre-cancelled refusals,
live nested reuse, retired-context refusal, later assessment revision 2, independent
admission during graph observation and changed-head refusal before journal/effects.
Result: `/home/vince/Projects/d4-progress-verified.log` via
`soda-native-pg-check --once … -- .artifacts/bin/native-mcp-helper progress-gate`.
Observations are declared; no connected/installed qualification. D5–D8 remain
pending, and the old product queue remains cleared.

### D5. Give the native callback boundary one internal operation representation

**Design:** preserve strict existing wire DTOs and convert them once into a private
read or mutation representation. A mutation contains one descriptor
(command ID, digest, deadline) and a typed operation. Descriptor validation,
repository binding and claim handling operate on this representation once.
Background conversion has an explicit allowlist checked before credentials or
write claims; adding a foreground operation never enables it in background.

`collaboration.rs` owns admitted callback/request lifetime. Existing
`factory_provider::Executions`, preparations, work and judgment owners receive
their typed operations and retain their resource lifetimes. Move semantic callback
handling into those existing owners rather than wrapping each match in another
forwarding module. Keep their references in the owning runtime and preserve joined
shutdown. Existing Go semantic operations and Rust receipts remain the contract.

**Problems solved:** audit 6: repeated mutation metadata and parallel dispatcher
lists mixed with execution/preparation internals.
**Removes:** repeated internal metadata extraction/validation and callback bodies
that belong to existing native owners. Legacy wire DTO repetition may remain
where required for strict decoding and compatible bytes.
**Retains:** wire formats/errors, narrow background admission, credential revision
leases, one-shot claims, deadlines and uncertain replies. No new RPC version,
generic operation registry, provider abstraction or background actor.
**Proof:** existing affected native operation checks must retain exact requests,
receipts, foreground/background refusals and cancellation/Quit retirement.

Completion (2026-11-08): verified D1–D4 code and retained checks against
`f2db7cf`, `a8bd728` and `f3e1a4c`; no prerequisite gap found. Strict existing
wire DTOs now convert once to private observations or commands with one
descriptor and typed write. The connector owns payload validation/dispatch;
existing execution and preparation owners handle semantic callbacks while the
collaboration runtime retains admission, credential leases, claims, cancellation
and joined shutdown. Background conversion keeps its separate explicit allowlist
before custody. Wire errors and causal/uncertain receipts remain unchanged.
Rust runtime/native checker builds and `bun run check:boundaries` passed.
The extended existing `--github-issue-product-declared` journey passed exact
writing/receipts, replay, loss/cancel races, withdrawal and restart, plus raw
foreground/background decoding, refusal-before-custody, owner errors and admitted
background current/read/write. Reproduction: `bash
/home/vince/Projects/d5-operation-session.sh NEW_SCRATCH_ROOT`; results:
`/home/vince/Projects/d5-operation-verified/{run.log,issue-product-result.json}`.
Existing managed MCP/recovery checks also passed both startup/restart phases,
peer/channel refusals and joined retirement:
`/home/vince/Projects/d5-mcp-verified/run.log` (`factory-native-mcp-signing-check
SCRATCH PG_PAYLOAD SERVER NATIVE_HELPER`). Provider facts are declared;
no connected/installed or worker execution qualification. Seven pre-existing
modified files preserved. D6–D8 remain pending; old product queue stays cleared.

### D6. Put Fountain ordinary ownership below issue/pull orchestration

**Design:** one small Fountain `services/nativeownership` leaf owns ordinary
claim/capability-file/release lifetime. It depends on `models/nativeoperation`
and dependency-neutral `modules/nativeoperation`, not issue, pull or the
orchestrating nativeoperation service. All three callers use it. Pure ordinary
resource construction/parsing lives in `modules/nativeoperation` so recovery and
writers share it. That module retains its prohibition on importing models/services.

Pass the caller's already-validated scope encoding through the shared primitive;
retain current codecs and bytes. Operation-specific permission/effect/recovery
rules stay in the orchestrating service. Global reservation/revision semantics
stay unchanged; finer locking is not established by the audit.

**Problems solved:** audit 5: import cycles forcing mirrored ownership protocols
and resource-label builders whose drift strands recovery.
**Removes:** duplicated claim/release mechanics and mirrored resource encodings.
**Retains:** nested ownership, cancellation-independent release, capability-file
custody, crash barriers, global fencing and operation-specific recovery.
**Proof:** existing ordinary/conditional ownership and recovery checks must cover
nested calls, cancelled callers, failed release and unchanged retained formats.
Fountain changes remain separately owned and do not close D2 or another gate.

Completion (2026-11-08): Factory D1–D5 code and recorded native/browser/PG
checks were verified; no prerequisite gap remained. Canonical Fountain
`/home/vince/Projects/forgejo-ext` commit
`e88b50dadb91b5ea4bb39dcf80307939d3270fed` implements D6 atop retained gateway
head `1b523c6e3d7370b4abfd3894f7c6cf9bf1853c03`. Its working tree was clean
before and after the change. One `services/nativeownership` leaf now owns the
three callers' claim/capability/release protocol. Callers retain their exact
scope encodings; shared collaboration/ref/owner codecs live in dependency-neutral
`modules/nativeoperation`. Permission/effect/recovery policy, crash barriers,
reservation transactions and bounded waits retain their existing owners.
Pinned Go 1.26.10, existing SQLite ownership/recovery checks passed ordinary and
conditional nesting, pre/mid-call cancellation, failed release with capability
cleanup and replacement fence intact, exact retained formats, admission refusals
and known/unknown recovery. Command: `go test -mod=readonly -tags 'sqlite
sqlite_unlock_notify' -count=1 -run 'Test(OrdinaryOwnership|CollabOwnership|RefWrite|MergeRetry|NestedRef|ParseCollabResource|ClassifyOrdinaryOwner|PreparedAdmission|Recover|SplitOrdinaryResource|RepositoryResourceFormats)'
./services/nativeoperation ./services/issue ./services/pull`; output:
`/home/vince/Projects/d6-verified.log`. Full Fountain build passed with the same
toolchain/tags (`/home/vince/Projects/d6-fountain`); direct import check:
`/home/vince/Projects/d6-imports.log`. Factory's seven pre-existing modified
files and all retained evidence are preserved. No global-lock redesign or new
security/installed qualification. D7–D8 remain pending; old product queue stays cleared.

### D7. Put native-check configuration in its existing feature-gated owner

**Design:** `cmd/soda-desktop/src/check.rs` parses scenario options into one typed
configuration and owns scenario timeout/result selection. `main.rs` retains
production path/options parsing and a narrow feature-gated call into that owner.
The same production startup/sign-in/Quit functions run under either configuration.
Remove repeated process-argument reads after configuration has been resolved.

**Problems solved:** audit 7: scenario booleans/combinations and checks mixed into
ordinary desktop startup; repeated argument interpretation in check callbacks.
**Removes:** the scenario flag ladder in main and duplicated interpretation.
**Retains:** current flags, refusal of invalid combinations, private fixture
boundaries and actual application checks. Large check files are not deleted
merely for their size; shared setup is consolidated only where duplication is
proved. There is no replacement test framework or new acceptance hierarchy.
**Proof:** build ordinary/native-check configurations and run the existing
lifecycle scenario affected by the extraction. Required live cases remain open.

Completion (2026-11-08): D1–D6 code/evidence verified, including canonical
Fountain `e88b50dadb91b5ea4bb39dcf80307939d3270fed` and its preserved gateway
parent. `check.rs` now owns one typed scenario, flag parsing/validation,
timeout precedence and scenario result selection. Callbacks use that resolved
configuration; only main's production option iterator reads process arguments.
Production startup/sign-in/Quit functions remain shared and unchanged.
Both `cargo build --locked -p soda-desktop --features native-ui` and
`--features native-check` passed with the retained GTK/WebKit sysroot:
`/home/vince/Projects/d7-{regular,native-check}-build.log`.
Existing `--check` passed real Tauri/WebKit, native PostgreSQL/Go readiness,
shared UI/themes, bridge refusals, stream/window closure, second-launch
activation and production Quit. Its stale status-key assertion was updated for
the already-present `execution_error` field. Reproduction: `bash
/home/vince/Projects/d7-native-session.sh FRESH_PREPARED_SCRATCH_ROOT`; evidence:
`/home/vince/Projects/d7-lifecycle-verified/run.log`. Built-binary CLI probes
passed all 15 supported flags, modifier refusals, last sign-in mode precedence,
path-value consumption and regular-build refusal (`d7-cli-check.sh` and
`d7-cli-check.log` under `/home/vince/Projects`). Seven original edits,
including the overlapping credential-check block, are preserved outside this
commit. Other scenario journeys, including live consent/provider writes,
revocation and OS sleep/wake, remain unperformed/pending; no installed or new
security qualification. D8 remains pending; old product queue stays cleared.

### D8. Keep current decisions short and historical evidence immutable

D8 prerequisite review (2026-11-08): the existing D1–D7 implementations and
recorded checks were inspected. One D2 gap was found: parent retry-identity and
saved-run reads only checked account/repository lifetime, so an older refresh
could overwrite a newer refresh's retry state. They now use the same refresh
guard and abort signal as issue/access/run reads; retry state is published after
validation. User-triggered writes retain their separate context lifetime.
TypeScript passed. The retained D12 Chromium journey, extended in
`/home/vince/Projects/d8-browser.ts`, passed acceptance, exact completion replay,
shared sibling refresh, access refusal, superseded/account/repository replies
and late retry-scope/saved-run success/failure with retained command bytes
unchanged (`d8-browser-verified.log` in that directory). The same race case fails
against exact prior source `fe40c5e` (`d8-browser-prior.log`: older scope-0
overwrites newer scope-1). Fix committed in `63a6155`. This is declared browser
evidence, not connected/installed qualification.

**Design:** keep `implementation-tasks.md` as the single current queue with stable
IDs, dependency-ready outcomes and current blockers. Freeze its long historical
version at a recorded Git commit and link to that exact version for prior
requirements/evidence. Apply the same current-versus-history rule to the handoff;
each open gate retains its blocker, files, proved scope and closure evidence once.
The assumptions audit remains a findings/owner-decision register.

Preserve human edits verbatim and identify status conflicts explicitly; the
uncommitted Active edit must be reconciled with the owner before it is replaced.
Existing evidence locations stay valid. Factory and Fountain work is consolidated
in each canonical repository; replace the redundant Fountain review patches with
direct source/commit references after its canonical source is preserved.

**Problems solved:** audit 8 and the observed duplicate patch package: several
maintained narratives and review copies for one underlying fact.
**Removes:** repeated active prose and unnecessary review copies, with historical
content retained in Git. This is documentation reduction, counted separately
from production-code changes. No 15,000-line deletion target is assumed.
**Proof:** current entries resolve to their exact historical requirements/evidence;
all open gates and original human edits remain accounted for. No artifact/profile
or ignored evidence is deleted as an incidental refactor step.

Completion (2026-11-08): D1–D7 sources and actual retained check outputs were
reviewed, including Fountain's D6 commit and both gateway commits. The D2 race
above was fixed first. No other concrete regression was found in the changed
binding, schema, gate, dispatch, ownership or lifecycle paths. Earlier successful
native/PG/operation/ownership/lifecycle checks were reused; only the new refresh
race and the combined gateway source required new execution.

| Redesign | Completed behavior | Verified implementation reference |
| --- | --- | --- |
| D1 | Complete account-bound native issue/run/intervention binding; explicit unavailable adapters | Factory `f2db7cf`; composed native work journey recorded above |
| D2 | One parent observation generation shared by children, together refresh and stale reply refusal | Factory `f2db7cf`, corrected `63a6155`; full retained Chromium journey plus late scope/saved-run cases |
| D3 | Frozen prior/current constraints, exact PostgreSQL normalization, locking/unknown refusal and data preservation | Factory `a8bd728`; existing upgrade/reopen checks |
| D4 | Context-aware progress acquisition at all callers; bounded serialization and revision revalidation | Factory `f3e1a4c`; expired waiter/no effects then valid request, nested/retired context and stale-head checks |
| D5 | One private request conversion, consolidated metadata/dispatch under semantic owners, early background allowlist | Factory `3f530ce`; existing operation and managed MCP lifecycle journeys |
| D6 | One small ordinary ownership leaf and dependency-neutral resource codecs | Fountain `e88b50dadb91b5ea4bb39dcf80307939d3270fed`; existing nesting/cancellation/failed-release/recovery checks |
| D7 | Typed scenario parsing and timeout/result choice in check owner, narrow main gate | Factory `fe40c5ef51d1f6be0b541efa1a9d8f723d7092da`; regular/check builds, CLI cases and production lifecycle |
| D8 | Current queue/handoff reduced, exact historical Git references and canonical gateway source | This completion entry; Fountain merge `2e57559c0e4844baba861a86632d083192537ff3` |

The full prior plan and handoff are frozen at Factory
`fe40c5ef51d1f6be0b541efa1a9d8f723d7092da` with their exact paths/locators in
the reduced documents. All 15 Pending rows, the pre-existing human CONNECT
Active row and its note are preserved verbatim. The committed Complete status,
human Active edit and cleared product goal remain explicitly unresolved.
The six other pre-existing code edits and pending reviewer evidence are
preserved; no permission-check completion is inferred.

Canonical Fountain main now merges gateway originals
`778e08aa98329848ddca1c07295120cf5fd082a8` and
`1a29a5e86e06e370486afeee689eaaf54ea04b7b` atop D6. All ten gateway paths
match the original head exactly; D6 remains intact. Existing PostgreSQL
`TestNativeDesktopEnrollment` passed on the combined source (S256 exchange,
enrollment/replay/read, withdrawal/inactive/403 and bound-proof refusals):
`/home/vince/Projects/d8-gateway-verified/run.log`; reproduction:
`bash /home/vince/Projects/d8-gateway-check.sh`. Pinned Go 1.26.10 integration
build passed (`d8-gateway-build.log` in that directory's parent). Native TLS/
keyring/profile reopen proof is reused, not requalified. Only then were the two
tracked Factory review patches replaced by direct source/commit references.
Original worktree/branch, ignored artifacts, profiles, keyrings and evidence
remain retained. Documentation reduction is separate from the production fix.
Exact preservation/history/ancestry and all changed local links/evidence paths
passed (`/home/vince/Projects/d8-doc-verified.log`); existing incoming milestone
anchors remain valid. Only the two tracked review patches are deleted.

No redesign prerequisite remains blocked. Product blockers remain: the human
CONNECT conflict/pre-consent crash, Muse typed-result encoding, unperformed
read-only reviewer qualification, real Fountain login/placement/operations and
installed full-pipeline recovery. All 13 handoff gates, including historical
security D1/D2/D3/D5, remain open. Original uncertain commands remain fenced.
This completes the redesign cuts only; it neither resumes the old queue nor
claims Factory, installed isolation/custody/IPC or provider parity finished.

### Q01 completion: current native session (2026-11-08)

The newly authorized Q01–Q30 goal is active; Q01's source/native scope is complete,
and Q02 is next. D1–D8 code/commits/retained checks, including canonical Fountain
D6/gateway references above, were verified and reused. Current
pre-consent Tauri/WebKit/Go/PostgreSQL startup and production Quit passed in
`/home/vince/Projects/q01-preconsent-verified/run.log`. The owner accepts current
diagnostics and the complete connected lifecycle while retaining the lost
historical crash cause as unknown. This does not waive current checks.

The approved isolated connected check found that the coordinator's empty
priority scan prevented first account binding. Under the existing ledger/table
locks, only scans with no subjects and zero cursor now count as identity-neutral;
candidate rows and every identity-bearing scan still require adoption. No rows
are deleted or rewritten. Existing identity/connection and retained-adoption
checks passed, including reopen and canonical scope:
`/home/vince/Projects/q01-ledger-check.log` (`go test -mod=readonly ./pkg/store
-run '^TestCollaborationRegistry(IdentityAndConnections|RetainedDataWaitsForAdoption)$'
-count=1 -v` with the private PostgreSQL fixture). The old fixture's later native
foreign keys and missing run record were repaired in that existing check.
The repair is Factory `d5b4506`; `67b6b16` retires unobserved account bindings and
pending reads while preserving choices/drafts, disables stale account actions,
and restores an iconified GTK window through the shared production opener.
Actual Tauri/WebKit/Go/PostgreSQL, approved GitHub identity/private reads, real
refresh, account/repository controls, cancellation, reconnect, native minimize/
restore, hide/restore, two owned Secret Service outages/recoveries, retained
drafts, explicit Disconnect and production Quit passed:
`/home/vince/Projects/q01-connected-verified/{run.log,session-lifecycle-result.json}`.
The owned local grant was removed after success; profile/keyring and all failed
attempts remain retained. Reproduce with the isolated external driver
`/home/vince/Projects/q01-native-session.sh ROOT consent`; a new grant needs human
consent. No provider writes, inference or remote revocation were claimed.

Execution-unavailable UI and production Retry execution passed with a held then
released coordinator lock, fresh runtime generation, successful coordinator start
and Quit: `/home/vince/Projects/q01-execution-retry-confirmed/run.log`.
Missing worker isolation remained explicitly unavailable. Existing
`--check-startup-error` owns this opt-in fixture; its driver contract is in the
desktop README. Typecheck/UI build and regular/native-check builds passed
(`q01-{typecheck,ui-build,regular-build,native-build}.log` under
`/home/vince/Projects`). Retained GTK/WebKit/PG inputs qualify these source checks,
not a fresh release baseline; Q02 owns current-input work.

The current canonical protection snapshot correctly refused with `check_changed`;
`canonical-workflow-evidence.json` records `actual_canonical_protection_read=false`.
The original aggregate `consent-result.json` incorrectly hardcoded that flag true;
it is preserved and does not qualify that read. Factory `5544082` now derives the
aggregate from the actual canonical evidence; native-check build passed
(`/home/vince/Projects/q01-evidence-build.log`). No extra live run was needed for
that reporting-only correction. Historical crash cause remains unknown under
the owner's clarification; the human status row/note and six pre-existing edits
remain verbatim. Installed startup/restart/sleep-wake, remote revocation and all
13 security gates stay open. Q01 does not complete Factory.

### Q02 active: tool selection races (2026-11-08)

D1–D8 and Q01 prerequisites are complete within their recorded scopes. Q02 is
incomplete. The native tool owner now publishes selection failures while holding
its existing selection lock, so an older failure cannot overwrite a newer ready
status. It rechecks the official channel after acquisition and refuses changed
or withdrawn releases without deleting the verified prior bytes. Its existing
explicit refresh entry is available to native hosts; no browser/provider URL or
new registry was added.

The existing `factory-native-mcp-signing-check ROOT tool-races` passed interrupted
download cancellation/staging cleanup, changed-release refusal, status recovery,
an active process retaining its actual ELF and a new process capturing the next
verified ELF. Declared no-forwarding HTTPS fixture/evidence:
`/home/vince/Projects/q02-tool-{fixture.go,races-verified/result.log}`.
The existing composed Go/native `tool-discovery` case passed against real official
metadata and the installed current Muse `1.4.14-R7309.1`, SHA-256
`0809aac7cb58d18f5022b6d05e02aa1974683c0ec430e5f04d25b60ed17de2da`:
`/home/vince/Projects/q02-tool-discovery-verified/result.log`.
No model call, login, personal launcher change or installed qualification ran.
Required interface admission now uses the fixed native helper's actual argument
builder and the existing terminal decoder: offline `--help` validates the Meta
flags, then the CLI's echo provider proves the shared terminal envelope with no
model/authentication. Child output/deadlines are bounded; cancellation kills and
joins its owned process group and removes only its new probe directory. Missing
helpers or incompatible flags/JSON leave the source unavailable. Public current
ELF/Go/native proof passed in
`/home/vince/Projects/q02-tool-protocol-verified/result.log`; normal helper
finish/cancellation/service retirement passed in
`/home/vince/Projects/q02-muse-connector-verified/result.log`.
The existing race check additionally passed incompatible flags/JSON, cancelled
in-flight helper retirement, late-status refusal and later success:
`/home/vince/Projects/q02-protocol-cancel-verified/result.log`.
The real, unshortened five-minute joined refresh detected a declared changed
release without foreground inspection and passed Go/native readback:
`/home/vince/Projects/q02-tool-cadence-verified/result.log`. Reproduction driver:
`/home/vince/Projects/q02-native-release-check.sh ROOT {races|cadence}`.

Remaining Q02: reachable initial sign-in when needed, automatic dependency
freshness coverage and affected current-input checks. The current CLI login
handoff was observed then cancelled in a new private child home; no grant was
obtained and the personal login was untouched. The supported no-model
`muse usage --json` returned `server_sent_no_usage`; that is not missing login,
verified billing identity or authority to choose a different source. Q02 stays
incomplete and Q03–Q30 remain pending; no security/installed gate closes.

### Implementation order and completion boundary

1. D1 + D2: one native work inspection/intervention journey across Go and Lit.
   Preserve unavailable recovery/execution until its real ports are supplied.
2. D3: independently stabilize old-profile startup without changing stored records.
3. D4: cancellation-aware admission first; move only I/O covered by existing
   ownership/revalidation. D1's interaction supplies the observable consumer.
4. D5 and D6: separate native and Fountain ownership cuts, preserving each wire
   boundary and using their existing checks. Neither depends on new provider breadth.
5. D7: encapsulate existing check support without changing the product lifecycle.
6. D8: reduce current documentation against the resulting sources and retained
   evidence; preserve unresolved human status choices until answered.

Each cut ends after its defining behavior and smallest affected existing proof.
Reuse earlier audit evidence and current check owners. No additional audit suite,
parallel state machine, speculative case matrix or recurring report is required.
Any further structural finding needs a concrete link to the cut being implemented.

The redesign cuts above are implemented with their recorded evidence; none
qualifies the full provider pipeline, real Fountain desktop login/placement, installed startup,
worker isolation, custody/IPC or D1/D2/D3/D5. Muse payload encoding, connected
permission checks and the pending reviewer-permission work retain their existing
blockers. These are product/qualification dependencies, not permission to weaken
admission or fabricate completion.

## Earlier implementation record — 2026-11-06

The following record is preserved historical evidence. Its completed sections
do not mark the current redesign implemented or the product finished.

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

### 4. Rust GitHub connector and admitted effects — complete

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

Implemented one mutation runner/admission permit for all twelve producers and one
receipt-bound policy. Operations own deferred request builders beside their
queries/decoders; transport owns bounded HTTP lifetime and fixed origins. Builders
still execute on the joined HTTP owner after queue admission. Callback outcomes
normalize once. Independent review preserved early validation, absolute deadlines,
current-account/pacing/one-claim order, send classification and receipt predicates.
Offline native builds and existing composed edit (16 cases) and inline (31 cases)
journeys passed: `/home/vince/tmp/fun.GVtPh3/` and `/home/vince/tmp/fui.BYYuZW/`.
These use actual Go/PG/native custody with no-forwarding dummy providers; they do
not qualify GitHub or stronger provider atomicity. Retain active native-check
canonical-read APIs and explicit flat callback variants: removing them requires
caller migration or decoding machinery that would obscure strict admission.

### 5. Native protocol, resources and desktop lifetime — complete

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

Implemented narrow nonsecret execution-protocol ownership for shared started,
execution and observation DTOs, bounds and pure candidate-result validation.
Borrowed/owned serialization preserves field order; observation encoding retains
its previous sorted-map bytes. Every boundary still owns peer/process/scope and
authority checks. Filesystem and parent-death helpers moved unchanged to private
resource modules. `RunStart` is the defining preparation input; five existing
public signatures remain compatibility projections. Feature-only desktop control
and observation state now lives in its check bridge. Product jobs keep their own
account/selection lifetime. Workspace pins preserve resolved versions/features.

Independent source reviews, broker/worker builds, normal desktop compile and actual
check-desktop staging passed. Existing composed edit/inline journeys exercised
custody/filesystem consumers; explicit edit journal passed real PG write, restart
and WAL recovery (`/home/vince/tmp/fup.1ntt2U/result.log`). These do not execute the
changed installed worker/broker admission path or qualify D3. Keep its gate open.

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

### 7. Browser workflow/lifecycle owners — complete

Replace callback bags with semantic lifecycle owners; compose visible snapshots
in Lit elements. Extract workflows with their retained state, not loose methods.

- Watch display/screen/request expose dozens of private getters/setters/callbacks
  assembled by Watch. Reconnect bounds are actual behavior, not arbitrary waits.
- Request is 3,806 lines and passes 50-plus fields/methods to its view; feed owns
  timeline and several writing/review transactions together.
- Request view's planningUnavailable is always false: remove speculative surface.
- API IntentGeneration is decoded inside a rendering/model module.
- Role labels/portrait keys are a smaller defining-data consolidation candidate.

Implemented Watch attachment ownership in the element and renderer/fit/observer
lifetime in `WatchScreen`, removing both getter/setter callback bags. Strict
generation decoding now lives in API ownership; one role definition supplies
labels/aliases/portraits; the unused planning-availability field is gone. Existing
Chromium Watch lifecycle and portrait cases, typecheck and Lit passed; independent
source review preserved reconnect, cursor, disposal and stale-response behavior.

`TaskShelf` now owns declined tasks, dependency lineage and proposal/save-preview
transitions. Request no longer temporarily mutates/rolls back four fields to prepare
persisted after-state. Independent source review and ten existing UI/history/repair
browser journeys passed, plus typecheck/Lit. The historical test that accesses the
removed private fields no longer applies; actual zero/unique/ambiguous successor
and persisted-repair journeys cover that behavior. Feed extraction follows below.

Feed revision writing/command settlement now belongs to `FeedRevisionEditor`, and
the source-bound change-review transaction to `reviewHandoff`. Timeline reads,
current HTTP/account admission and navigation remain in Feed. Independent review
and the existing Chromium revision/review recovery journey passed, with typecheck
and Lit. Final UI review retained Request's value/action view snapshot and shared
head/lifetime: planning/generation have distinct archives and receipts, so further
splitting would introduce controller coordination without a confirmed benefit.
Removed implementation-narration comments and the one-choice setter parameter;
preserved lineage, host-lifetime and recovery constraints. No further worthwhile
UI structural finding remained in the final sweep.

### 8. Integration checks and test cleanup — complete

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

Implemented one native fixture helper owner and finite flag/arity dispatch,
preserving all 69 CLI flags, aliases, errors and distinct scenarios. The shared
uncertainty assertion now checks all optional causal destination references instead
of only issue references. Go resolves one explicit case/phase with bounded legacy
compatibility; ambiguous selection and invalid phase refuse before DB open. The
existing driver accepts explicit case/input. One JSON contract supplies all 14
existing browser/native diff assertions, including previously omitted unknown-thread
retention. Test cleanup retained all tests: none was proved worthless. Historical
external tests bound to removed private fields/obsolete host URLs were preserved;
actual UI behavior replacements passed as recorded above. Redundant comments were
removed while external and retained-format constraints stayed documented.

Existing checker build, one final composed edit journey through the new dispatch
and helpers (`/home/vince/tmp/fuh.bMAxvr/`), explicit three-lifetime PG check,
admission refusals, Storybook and native workbench build passed. Independent review
confirmed all 14 labels and preserved trusted-input assertions. An optional fresh
WebKit diff run stopped before page launch: first the cached libc loader, then a
missing matching private DBus ABI in retained runtime libraries. Diagnostics:
`.artifacts/untangle-diff/` and `/home/vince/tmp/fud.rTufdi/`. No UI failure or new
native diff qualification is inferred from these setup failures. Chromium behavior
checks and the earlier native evidence retain only their actual stated scopes.

### 9. Build, assets and active documentation — complete

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

Implemented Cargo-reported executable staging and symmetric, root-correct payload
overlap refusal. Production UI copies the canonical diff-library license. Current
testing/ownership guides describe this checkout; original predecessor text is
retained verbatim under `history/`. Lit commands, empty current unitban allowlist
and redundant formatting/lint settings are corrected. Historical product-plan
evidence remains intact; use its opening status and the handoff for open gates.
Independent script review, script TypeScript, unitban, actual desktop stage with
nondefault `CARGO_TARGET_DIR`, byte comparison of staged binary/license and early
root-overlap refusal passed. The first plain-host stage lacked WebKit development
libraries; the retained sysroot resolved it. Build log: `.artifacts/untangle-desktop-build.log`.
This is a cached development build, not fresh or installed qualification.

## Verification and completion

Use existing affected checks: production Go build and dependency boundaries,
actual disposable PostgreSQL/native product checks, affected Cargo/native paths,
TypeScript/Lit/browser journeys and build staging as appropriate. Source checks
support their actual scope only. The final independent pass must revisit every
section and resolve worthwhile remaining findings. Completion requires preserved
intended behavior; missing features and original provider/installed/security
qualification remain explicitly unfinished in their owning product plan.
