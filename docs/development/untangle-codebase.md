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

### Q02 verified increment: initial Muse login (2026-11-08)

The production desktop now exposes the official CLI's initial login through its
existing native tool owner. Only a validated Meta device URL and its matching
public confirmation code reach the UI; the CLI retains its own credentials.
The fixed helper verifies the captured executable and required `login --help`
interface. Missing/stale adapters explicitly disable sign-in. Window ownership,
runtime generation, cancellation and joined shutdown retire pending login work;
source/model/spending choices and the personal launcher/login are preserved.

Actual native WebKit acquisition/sign-in/browser handoff, owner-close cancellation,
other-window refusal, stale cancel/open refusal, approved completion and production
Quit passed in `/home/vince/Projects/q02-login-preconsent-verified/run.log` and
`muse-login-result.json`. Its expired first challenge remains in `expired-attempt/`.
Restarted isolated Secret Service/home loaded the saved CLI login through the
no-model `usage --json` command: `/home/vince/Projects/q02-muse-reopen.log` reports
`server_sent_no_usage`, not verified billing identity. Private login retained.
Regular/native-check builds and TypeScript passed in
`/home/vince/Projects/q02-login-final-{regular,native}-build.log` and
`q02-freshness-typecheck.log`. Normal fixed-helper finish/cancel and joined service
retirement passed in `/home/vince/Projects/q02-muse-connector-final-verified.log`.
Q02 remains incomplete for automatic dependency freshness/current-input work;
Q03–Q30 and all installed/security gates remain pending.

### Q02 completion: current inputs and owning update paths (2026-11-08)

Q02 is complete within its source/component scope. Implementation commits:
`cf44374` (release/status races), `699106a` (required interface and joined probes),
`031b03d` (initial native sign-in/reopen) and `5fa71ea` (current inputs/update paths).
D1–D8 and Q01 retain their previously verified scoped prerequisites, including
the canonical Fountain references. Q03 is next; no product/security gate closes.

Official npm/crates/module/toolchain metadata selected the refreshed manifests
and locks, Bun 1.4.4, Go 1.27.3 and Rust 1.99.1. The existing analyzer now uses
classic TS6.0.3 while product compilation stays TS7.0.2. The changed diff-parser
API retains strict refusal; all 15 existing native diff checks passed with trusted
keyboard input (`/home/vince/Projects/q02-fresh-native-diff-verified/native.log`).
TypeScript, Lit fixtures/product analysis, UI/Storybook, Rust workspace, both native
configurations, actual desktop staging and pre-consent startup/Quit passed in
`/home/vince/Projects/q02-fresh-*.log` and `q02-fresh-native-startup/run.log`.

Current PostgreSQL fixture acquisition captures its immutable image ID; native
packaging checks official stable 18.6 and refuses stale supplied payloads before
staging (`q02-pg-{freshness-verified,payload-refusal}/` and
`q02-fresh-stage/factory-runtime/postgresql-selection.json`, same scratch parent).
The broad existing PostgreSQL run exposed stale fixtures. Corrected migration
shape/reader locks, seed expectations, account binding, paused-gate refusal,
pre-existing feed counts and committed-versus-completed graph assertions. Every
failed case subsequently passed in the targeted `q02-current-pg-{rechecks,final}`,
`q02-native-admission-verified` and `q02-pause-verified` logs; the broad suite was
not repeated. Ordinary Go/MCP cases passed in `q02-final-go-source-check.log`.
Updated-library tool races and helper finish/cancel passed in
`q02-fresh-tool-races/result.log` and `q02-fresh-helper/result.log`; the completed
unshortened five-minute proof is reused. Original six edit sets remain unchanged.

Hourly dependency/toolchain discovery and existing-check validation are configured
without cooldown or major exclusions. Remote scheduled jobs remain unperformed
until default-branch publication; Q30 must verify their actual activation/results.
Q25/Q28 still own fresh distribution and installed update activation. No fresh
installed baseline, model result, billing identity or security qualification is
claimed. Q03–Q30 remain unfinished; personal login/launcher and all private
profiles/keyrings, failed attempts and historical evidence are preserved.

### Q03 completion: owner configuration and explicit funding (2026-11-08)

Factory `d4b05f0` completes Q03's configuration/pre-execution admission scope.
D1–D8, canonical Fountain references and Q01–Q02 retain their verified scopes.
Go projects allowance availability through the same read-only workspace/placement
check as sponsorship admission. Shared settings explicitly disable unavailable
funding, retain the chosen source, require observed ownership for withdrawal and
permit withdrawing an owned recorded native allowance without a usable source.
Judgment availability now checks the chosen model and active role allowance;
advertised models are shown without changing the user's choice.

The existing native check owner gained `--check-owner-policy`. The complete actual
WebKit/Go/PostgreSQL journey passed in
`/home/vince/Projects/q03-owner-policy-verified/{run.log,owner-policy-result.json}`:
disabled first policy, explicit run/controller/workspace consent, separate agent
and judgment choices, missing-grant/model/actor refusal, source/policy conflicts,
concurrent-policy preservation, withdrawal after workspace permission removal,
retired-context refusal, owned-account cleanup and production Quit. Reproduction:
`bash /home/vince/Projects/q03-native-session.sh NEW_PRIVATE_ROOT`. It reuses the
approved isolated Muse home and existing declared read-only GitHub fixture;
source inspection is unavailable in that fixture and is not qualified here.
No model call, copied login, billing identity or installed/provider-permission
qualification is claimed. TypeScript, required Lit analysis, regular/native-check
builds and ordinary Go cases passed in `/home/vince/Projects/q03-*.log`.
All six original edit sets, private profiles/keyrings and failed attempts remain.
Q04 is next. Actual execution/charges remain Q08/Q09/Q12/Q13; all 13 security gates
and later integrated/installed work remain open.

### Q04 completion: canonical local placement (2026-11-08)

Factory `23508db` completes Q04's local placement scope after verification of
D1–D8, Q01–Q03 and the retained canonical Fountain references. Shared policy
settings now expose explicit enrollment and disable through the existing command
owner; disabling remains available after funding withdrawal and retains observed
choices. Workspace permission is current only for the active controller's exact
runtime and generation, fixing stale consent presentation after reselection.

The complete existing native WebKit/Go/PostgreSQL journey passed in
`/home/vince/Projects/q04-owner-placement-verified/{run.log,owner-policy-result.json}`:
retirement/new generation, stale placement refusal, explicit workspace reenrollment,
disabled/unfunded enrollment refusal, enrollment/disable, missing execution,
conflicts, withdrawal, actual repository/account replacement and production Quit.
Reproduce with `bash /home/vince/Projects/q04-native-session.sh NEW_PRIVATE_ROOT`.
The earlier failed attempt exposed a check navigation assumption, corrected before
the successful full run. Both owned fixture accounts were removed; profiles,
keyrings and failed evidence are retained. No model call or real repository intake
ran. Existing `TestNativeCheckpointRetentionPG` and
`TestRecordDispatchPacketRequiresEffectiveAuthority` passed against isolated
ephemeral native databases (`/home/vince/Projects/q04-placement-authority.log`),
including competing controllers, stale generations, appliance admission and
disabled/withdrawn-funding refusal. TypeScript, required Lit, UI and both native
build configurations passed (`/home/vince/Projects/q04-*.log`). All six original
edit sets remain intact. Q05 is next; remote placement/T12 completion remains Q19,
and all 13 security gates and later integrated/installed work remain open.

### Q05 completion: ordinary native boundary admission (2026-11-08)

D1–D8/Q01–Q04 and canonical Fountain references remain verified within their
recorded scopes. Current canonical source at `6c4dc40` already contains the
required worker/native/MCP owners; no alternate executor or new product code was
needed. Current worker/broker/check binaries and Go callback built successfully
(`/home/vince/Projects/q05-{native,callback}-build.log`).

The existing actual supervisor check passed isolated reviewer finite execution,
64 KiB prompt/replay, cancellation and production-owner Quit with exact terminal
process/custody receipts and joined dependency shutdown
(`/home/vince/Projects/q05-native-boundary-finite/result.log`). The existing
public-Go-admitted coder check passed actual isolated worker MCP activation,
artifact upload, explicit incomplete result and canonical settlement after a new
supervisor instance without relaunch
(`/home/vince/Projects/q05-admit/result.log`). Reproduce with
`bash /home/vince/Projects/q05-native-execution.sh ROOT [admitted]`, supplying the
retained source bundle/commit as in those roots. Both use temporary existing
service templates, private profiles/keyrings and local fixed executables, without
model calls or personal credentials. Service/owned-root retirement passed.
Actual receipt headers record desktop UID 1000 versus worker UID 64596 for all
four runs (`/home/vince/Projects/q05-boundary-receipts.log`). Runtime admission
enforces exact source/role/worktree mounts, bounded private storage/cgroups and
private network/IPC before execution; the real admitted call uses the scoped
relay. Existing D5 current-work/all-eight-role-catalog, backend/instance,
expiry/revocation, signer-custody and restart checks remain applicable and were
reused from `/home/vince/Projects/d5-mcp-verified/run.log` rather than repeated.
Go/UI continue to receive scoped authority, never the native root key.

The first admitted attempt combined the separate slow-start budget fixture with
the normal 30-second admission check and timed out. Preserve run
`75f0c7ea6d2cb7a200a0ce930bf50fa7` under
`/home/vince/Projects/q05-native-boundary-admitted/` as unresolved; no terminal
outcome or reconciliation is inferred. Its startup/outcome recovery remains Q09.
Other failed fixture roots are also retained. Q06 is next. This completes only
Q05's ordinary boundary scope, not a successful provider task, complete crash/
outcome recovery, installed activation or any of the 13 open security gates.
All six original edit sets, existing services/evidence and canonical repositories
remain preserved.

### Q06 increment: larger workload and preparation admission (2026-11-08)

Factory `82e68b3` is verified partial Q06 work; D1–D8/Q01–Q05 retain their scopes.
The actual larger workload exposed SELinux filename transitions labeling copied
`system/licenses/` files as systemd units, denying Git mmap. Snapshot transfer now
inherits its already-owned private destination's label for the synchronous copy,
restores the thread's creation context and fails closed on restoration failure;
host policy is unchanged. [Thread-specific creation context contract](https://man7.org/linux/man-pages/man3/getfscreatecon.3.html).
Bounded Git diagnostics retain the actual failure. Setup CONNECT now reports
policy denial separately from DNS/connect failure. The existing approved artifact
read gains bounded transient retries; TLS, exact hash and total setup deadlines
remain required. Original 403 details are not retrospectively inferred.

The existing real native checks passed:

- `/home/vince/Projects/q06-ui6/result.log`: verified official Bun, cold public
  dependencies, actual 103-source shared UI/21-asset lifecycle build, identical
  offline rebuild in the copied worker snapshot, real admitted MCP artifact/result,
  exact retirement and settlement after restart without relaunch. Input is the
  retained source snapshot of Factory `72c0f81`; no model call ran.
- `/home/vince/Projects/q06-net/result.log`: public HTTPS, DNS versus policy refusal,
  loopback/plain-HTTP denial, absent-approval offline refusal and exact retirement.
- `/home/vince/Projects/q06-wait/result.log`: overlapping preparers, cancelled
  same-run waiter without effects, independent work and valid replacement.
- `/home/vince/Projects/q06-full/` and `q06-space3/`: actual active-reservation
  saturation and free-space refusal before another preparer starts, with exact
  retirement and broker diagnostics. Low-space uses only a private 64 MiB `/run`
  view for the temporary broker, preserving its real state/socket bindings; the
  host filesystem was not filled or remounted.

Current native/check/Go callback builds passed. Small cold Node/Bun/Go/Rust checks
were reused. Ceilings remain 64 MiB source, 1 GiB per prepared tree/100,000 entries,
1 GiB preparer memory/active reservation, 4 GiB retained quota, independent `/run`
reserve, 3 GiB worker tmpfs/4 GiB service memory and approved 1–1800-second entry
deadlines. The shared UI used reviewed 120/30-second setup/check limits and normal
ignored build outputs; private registries remain unavailable.

Additional verified increment `cb841bd` counts pipelined CONNECT data against the
same 512 MiB aggregate encrypted transfer ceiling and retains its exact native
failure instead of discarding connection-job errors. The existing actual
preparation check crossed the ceiling using bounded, hash-verified public artifact
reads, refused readiness with `setup HTTPS transfer budget exceeded` and confirmed
exact retirement (`/home/vince/Projects/q06-budget/{result.log,preparation-network-limit/native-preparations/}`).
Current Rust and Go callback builds passed; no model or personal credentials ran.

At that checkpoint Q06 was incomplete: interrupted network handling, overlap across
restart and snapshot-copy cancellation still need their defining checks. Preserve
all earlier attempts; notably `b8affa6b0bdfe1dc88bf55af010a4c7d` under
`/home/vince/Projects/q06-ui4/` has an unconfirmed candidate capture, not a terminal
receipt. Q09 must retain/reconcile that uncertainty without inference replay.
No failed historical run is settled by the new pass. All six original edit sets,
profiles/keyrings and evidence remain. Q07–Q30 and all 13 security gates stay open.

### Q06 completion: ordinary preparation and resource recovery (2026-11-08)

Factory `82e68b3`, `cb841bd` and `def29b7` complete Q06's ordinary preparation
scope; D1–D8/Q01–Q05 and canonical Fountain references retain their verified
scopes. The larger workload, bounds, actual failure fixes and earlier defining
checks are recorded immediately above and are not repeated here.

The remaining existing native checks passed:

- `/home/vince/Projects/q06-interrupt2/result.log`: both real admitted role
  preparations had partial public-download bytes; production-owner Quit cancelled
  and joined them. A new supervisor retained identical request/retirement bytes
  and created no replayed setup roots. The initial failed check wrongly expected
  live output from a retained-receipt API; the final barrier observes only actual
  owned file sizes, not a fabricated observation or completed result.
- `/home/vince/Projects/q06-snap/result.log`: cancellation during the first part
  of an actual 512 MiB copy joined exact retirement within six seconds, preserved
  the frozen source receipt, and allowed a later snapshot/check without rerunning
  the one-time setup. All exact roots retired. This reuses the existing cancellable
  broker controller, with its fields/wire framing private; no duplicate protocol
  client or new execution owner was introduced.

Reproduce using the retained `q06-native-preparation.sh` and
`q06-native-parallel.sh` drivers with their recorded source/observer inputs.
Current native/check and Go callback builds passed. Earlier successful small
toolchain and current-input checks were reused. No model call, user login change,
private registry admission or installed workload qualification ran. Historical
403 details and failed run outcomes are not retrospectively inferred; their
retained uncertainty still belongs to Q09. All six original edit sets, private
profiles/keyrings and evidence remain. Q07 is next; installed workload checks
remain Q26–Q28 and all 13 security gates stay open.

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

## Q07 connected setup increment (2026-11-08)

`41a1a17c9bab8615071fd92c5c8c90024286b991` invalidates setup consent after
source-path edits or a failed selection read. Existing gated native checks now
exercise the production WebKit/IPC/Go/PostgreSQL/native setup path without seeded
approval rows. `/home/vince/Projects/q07-ui7/run.log` and
`github-ui-write-result.json` passed: real owned GitHub repository/target,
disabled policy, explicit placement/workspace consent, two reviewed planning
files, actual successful setup/check exits for both roles, source-context readback,
`Reviewed=true`, `Verified=false`, no invented readiness digest and production
Quit. Both native request/completion/retirement journals are retained there.
The new disposable source branch is `factory-checks/q07-onboarding-20261108` in
`LevitateOS/factory-team-public-check-20261106`, commit
`9527f96dae9c71ebf27942c918624042df1e011f`; existing branches/text were preserved.

The same connected selection assertions passed in `q07-ui3/run.log` before its
preparation step failed: >64 paths, unsafe/missing paths, >32 KiB per file,
>128 KiB aggregate refused; exactly four 32 KiB files accepted; editing a loaded
selection disabled approval and reset consent. The failed preparation was traced
in `q07-ui5/run.log` to the display fixture's user namespace hiding root ownership
of the broker directory. The successful fixture preserves host ownership and
drops to UID/GID 1000 with all capability sets removed before app startup; broker
ownership checks remain intact. Bounds were reused, not rerun, in `q07-ui7`.
TypeScript, Lit, desktop UI, native-check and regular native-ui builds passed
(`q07-{typecheck,lit,ui-build,native-build,regular-build}.log`); the existing Go
inspection helper built and passed against the actual preparations.

**INCOMPLETE:** Q07 still needs accepted planning inputs with nonempty native
comments/dependencies, hidden/stale-input refusal, changed-source consent,
withdrawal during I/O, and native pending-approval restart/reactivation without
replay. Permission to post one exact disposable fixture comment is pending; no
comment was posted. No model ran. Q08–Q30, installed variants and all 13 security
gates remain open. Original six dirty edit sets, profiles/keyrings and failed
attempts are preserved.

### Q07 exact consent recovery increment

`c55b33619b141e2598c5c9f84c4079467cab6b8d` captures the complete normalized
setup request digest in its existing immutable recipe. Known receipt recovery
checks exact payload, actor and native owner before returning observations;
it acquires no source bundle and starts no preparation. Older recipes keep their
prior admission path; missing provenance is not inferred. Setup readiness is
shown as current only for the displayed source/selection and native session.

`/home/vince/Projects/q07-recover2/run.log` passed real setup with its completed
reply dropped and retained across production Quit. `reopen.log.phase2` passed
recovery after source change, changed-payload conflict and stale-session refusal
before its next source review failed: the owned fixture B accidentally omitted
README. The corrected, fast-forward-only source fixture is
`fe292f53ed53ed1bf3887d3338b9c3a73bc0c09e`; `reopen.log.phase4` passed fresh consent,
both actual ready preparations and source-context readback for it. The final
result deliberately does not relabel the reused recovery phase as a fresh pass.
`q07-recovery-journals.log` confirms all six original journal files byte-identical,
exactly two new preparations, and confirmed retirement of all four identities.
Existing native PostgreSQL requirement/approval-chain checks passed in
`q07-recipe-pg.log`; TypeScript/Lit/UI, native-check, backend/helper and regular
native-ui builds passed in the `q07-recovery-*` logs. All original dirty hunks
were compared and preserved. No model ran.

**Q07 remains INCOMPLETE:** acceptance with nonempty native comments/dependencies,
hidden/stale acceptance inputs and withdrawal during I/O still need their owning
checks. Automatic cross-session recovery of legacy pending entries lacking full
command provenance is unqualified; their prior behavior/data remains intact.
The exact fixture-comment permission remains pending, and no comment was posted.
Q08–Q30 and all 13 security gates remain open.

### Q07 preview and source-I/O withdrawal checks

`e91315300058cb4c42acdc0a5e8a3a6f668cf064` passed the real native setup ->
shared work-detail observation -> acceptance-preview path with consent unchecked
and acceptance disabled. Evidence: `q07-recover2/reopen.log.phase5`, its
`github-ui-write-result.json.phase5` (`connected_acceptance_preview=true`,
`q07_complete=false`), and `q07-acceptance-journals.log`; original journals stayed
byte-identical and all six retained preparation identities retired.

`e095d91a9bf7185a4f7645787ab9fa5ec7c01d08` extends the existing native setup
check with a source-response barrier. The final account/controller/approval
cases passed in `/home/vince/Projects/q07-withdraw-{account2,placement2,approval2}/result.log`:
actual credential/controller withdrawal leaves zero consent/preparation rows;
a separate explicit replacement approval leaves exactly one requirement/approval
and only its two ready preparations, with all 14 selected files read back through
the production material reader. Original single-context bounds remain enforced.
The no-forwarding peer uses declared identity/source responses and the retained
14-file input from `fsetup-validation24.uMf49B`; its narrowly extended source is
`/home/vince/Projects/q07-source-provider.go`, driven by
`q07-native-withdrawal.sh`. Current native/helper builds passed. Temporary units
were removed; profiles/keyrings and all evidence remain. These controlled
negative cases do not claim live-provider or installed qualification.

**INCOMPLETE:** native acceptance with nonempty comments/dependencies, hidden/stale
acceptance inputs, the positive 64-file maximum and legacy pending provenance
qualification remain open. The pending exact fixture-comment permission has not
been granted; no comment or other provider message was posted. Q08–Q30 and all
13 security gates remain open. The verified increments above do not finish Q07
or Factory.

### Q07 maximum selection and complete native setup envelope

`4bed2b1967b6e63a5b10cdc249dbe473c8a204da` fixes the private native Go server's
missing setup-specific 1 MiB envelope while retaining other route limits. Source
reads use bounded groups under the existing deadline; source identity/tip checks,
decoding and byte bounds remain. Approval refuses selected files plus scripts
over the existing 128 KiB planning bound before recording consent. Its response
reuses checked bytes only when repository, latest tip, target and selection match;
it still rereads authority, decision heads and actual preparations.

`/home/vince/Projects/q07-recover2/reopen.log` passed real GitHub/WebKit/Go/PG/native
recovery of the exact retained 64-file approval after restart, fresh consent,
both ready preparations and all 64 files read back from both actual workspaces.
The selected files total 130560 bytes, leaving script space inside the unchanged
planning bound; the owned source fixture is
`f617de773e9e178d290ad1e4ebc4bc9d491c1799`. Earlier 128 KiB aggregate preview and
oversize refusals remain in their retained scope. `reopen.log.phase6` retains the
serial-reader failure; `phase7` retains its lost large-selection reply. The latter
was recovered without replay. Its old result's combined restart/source-change
flag is not evidence of a new head change during this same-source recovery; the
future check label now distinguishes restart alone.

`q07-planning-bound3/result.log` passed the established 422 no-consent refusal
with zero decision/preparation rows. `q07-large-scripts/result.log` passed valid
requests above 64 KiB with two 32 KiB scripts through actual preparation and
14-file material readback. `q07-parallel-{account,placement,approval}/result.log`
passed the affected controlled source-I/O races after parallelization. TypeScript,
Lit, UI, Go API/runtime compilation, and native-check/regular desktop builds passed
in `q07-maximum-*`; the Go packages report no test files, not extra test coverage.
`q07-maximum-journals.log` confirms original source-A bytes unchanged, eight local
retirement markers and two interrupted roots independently broker-confirmed absent
in `q07-interrupted-retirement-confirmed.log`. Their missing historical local
markers remain untouched; `removed:false` is the broker's documented already-absent
success. Temporary units were removed and all original dirty hunks preserved.

**Q07 remains INCOMPLETE:** nonempty comment/dependency acceptance and hidden/stale
acceptance-input checks await the exact fixture-comment permission. Legacy pending
entries without complete captured command provenance remain unqualified. No
comment or model call ran. Q08–Q30 and all 13 security gates remain open.

### Q07 legacy pending recovery refusal

`a312951` refuses known legacy setup retries before source acquisition, consent
admission or native execution when their immutable requirement lacks the full
command digest. The API/UI explicitly report automatic recovery unavailable and
retain the pending command. No provenance is inferred or old record migrated;
modern exact receipts keep their existing recovery path and qualification above.

The existing native WebKit/IPC/Go/PostgreSQL check passed against a copy of the
actual retained `q07-ui3` profile: `q07-legacy/reopen.log` and
`github-ui-write-result.json`. It confirmed the legacy decisions still validate,
no new preparation admission, unchanged historical observations and exact pending
record bytes (`q07-legacy-preservation.log`). The first attempt stopped before
the journey because its retained-account fixture flag was missing; that log is
preserved. Cleanup shared the old fixture's isolated keyring and withdrew its
test entry; the same authorized grant was restored under the original
profile/connection with a fresh credential revision, without printing the token
(`q07-legacy/restore-fixture-keyring.log`). The copied profile remains withdrawn;
personal login was untouched. Future retained-profile copies need isolated
keyring copies. TypeScript/Lit, UI, backend/helper and both desktop configurations
passed (`q07-legacy-*.log`); all six original dirty edit sets remain exact.

**INCOMPLETE:** Q07's nonempty comment/dependency acceptance and hidden/stale
acceptance-input journey still await the pending exact fixture-comment permission.
No comment or model call ran. Q08–Q30 and all 13 security gates remain open.

### Q07 blocked on the real comment fixture

Read-only GitHub revalidation on 2026-11-08 confirms the owned fixture's ordinary
issues 3 and 5 both have zero comments. The fallback search found only unrelated
Soda OS issues. The same exact fixture-comment permission remains unanswered
across successive goal turns; developer instructions prohibit posting a message
without explicit authorization. Q07 is **BLOCKED** on that permission or a
user-designated existing commented fixture. Nonempty comment/dependency acceptance
and hidden/stale acceptance-input qualification remain unfinished. No provider
message was posted, no model ran, Q08–Q30 remain pending and all 13 security gates
remain open. Verified implementation and the six original dirty edit sets are
unchanged by this status update.

### Q07 completion: connected accepted inputs (2026-11-08)

The owner approved posting the exact [fixture comment](https://github.com/LevitateOS/factory-team-public-check-20261106/issues/3#issuecomment-6720378010).
`9ecb2989a03b3ab03a132d94e7e0f8b4423c74a8` passed the existing native
WebKit/IPC/Go/PostgreSQL journey in `q07-acceptance2/run.log` and
`github-ui-write-result.json`: current source review, both actual ready roles,
real selected comment and prerequisite, refusal while that comment was minimized,
refusal after removing/re-adding its prerequisite occurrence, refusal to invent
an unaccepted code prerequisite, and fresh explicit acceptance (`201`). Exactly
one decision persisted. Native dispatch-input readback matched its title/body/
comment revisions; the production source assembler read the approved file bytes
for a fixture draft derived from those inputs. No model, draft row or run was
seeded; actual Muse planning/execution remains Q08.

The first `q07-acceptance/run.log` preserves its successful acceptance receipt
and the check's incorrect expectation of HTTP 200; only that affected journey
was repeated after correcting the assertion. The final run used its own keyring
copy, removed its local test account, confirmed both native retirement markers
and production Quit, restored comment visibility and retained the prerequisite.
Native-check/regular desktop and helper builds passed (`q07-acceptance-*build.log`);
`q07-acceptance-original-edits.log` confirms all six original edit sets preserved.

Combined with the preceding Q07 setup, bounds, changed-source/restart/receipt
recovery and controlled authority-withdrawal evidence, Q07 is **COMPLETE within
ordinary native onboarding scope**. The single final artifact deliberately does
not claim all reused cases itself. D1–D8/Q01–Q06 references remain ancestors;
Fountain still retains D6 `e88b50dadb91b5ea4bb39dcf80307939d3270fed` and its gateway
source. Relevant current owners and retained checks were reused. Q08 is Active;
Q09–Q30, installed variants and all 13 security gates remain open. Factory is
unfinished; historical status conflicts and failed profiles remain preserved.

### Q08 active: retained typed-finish trace

Run `518ab9643f2a381dd81b4814df003979` under
`/home/vince/tmp/fmuse-ready-admission8.z4gCPL/` retains the actual CLI transcript
at `execution-profile/native-muse-runs/29067804fda7a7e439da7af2e5a4ccde/bb84c3a320682e22786aaba2104018e6/terminal.log`.
Both failed plan tasks report `payload` as a string where the RPC schema requires
an object. Parsing the retained prompt confirms `payload` and `tasks` were
objects, with an explicit instruction against JSON-encoded strings. The retained
result remains `needs-human` / `harness_no_report`; CLI final text is no product
completion evidence. Current native forwarding uses JSON values/RawMessage;
the responsible conversion is not yet attributed. The existing strict
`TestFactoryCatalogTransportPerHarness` passed (`q08-catalog-transport.log`).

**INCOMPLETE:** a current live reproduction is prepared in
`/home/vince/Projects/q08-native-plan.sh` with the original source bundle and
private copies of the verified tool/login. Automatic approval review rejected
the command before execution: explicit authorization of this live Meta model
execution and payload was required because it sends test inputs externally and
may consume quota or incur cost. That approval is pending; the command was not
retried or bypassed and no new model call started. Encoder correction, accepted
plan/Builder/task/final identities, candidate/accounting/restart and the listed
live regression cases remain pending. Q09–Q30 and all 13 security gates stay open.

### Q08 current native journey and remaining historical attribution (2026-11-08)

The owner approved the live test described above. Implementation
`5bd4c825a2bb076984d56d850684608b110d6b00` separates user-manager control from
the credential bus for launch/stop/recovery, clarifies noninteractive Builder
completion and shares a bounded 32-model-step ceiling. The successful Builder
used 23 steps; earlier 16-step runs stopped with work pending. Existing time,
cancellation, output/source allowances and `max_children: 0` remain enforced.
The existing check requires both dependent tasks and matches receipt task/plan/
output/check identities; outer/inner fixture approach fields also match.

The existing live journey passed at `/home/vince/tmp/q08-native-plan8/result.log`
using `/home/vince/Projects/q08-native-sequential.sh`: strict typed planning,
public Builder advancement, both task checkpoints, final receipt, candidate
export, original-source stop-based accounting and reopen without inference
replay. Candidate: `225606838c722d5812172a5ba97d9c39430f6615`; source bundle/
commit compare exactly with the original fixture. Retained CLI bytes summarized
in `/home/vince/Projects/q08-live-qualification-summary.json` prove missing-file
read and reused-ID refusal; `q08-native-plan5/` additionally proves live
output-ready and early-finish refusal/recovery. That earlier one-task pass does
not qualify sequential completion.

Production Go/helper builds passed (`q08-server-build.log`,
`q08-sequential-helper-build.log`); regular/native-check desktop and native
helper builds passed (`q08-step-{regular,check,native}-build.log`). The existing
non-model finish/cancel check passed with an isolated credential-bus address
(`q08-manager-final.log`), confirming process/tree/service retirement. Fixture
storage moved outside the preserved `Projects/.git` marker after its temp-path
refusal. Failed `q08-native-plan6/7` runs remain retained for Q09 recovery
observations, without invented finishes or replay. All six original edit sets
still match the saved baseline.

**INCOMPLETE pending clarification:** original run
`518ab9643f2a381dd81b4814df003979` proves a string reached strict RPC validation,
but retains no raw model/client argument bytes identifying the producer's
internal conversion. Factory's JSON value/RawMessage forwarding does not
stringify it; current live typed planning passes without schema weakening or
coercion. The historical cause is not claimed fixed. The owner has been asked
whether current complete journey evidence may close Q08 while retaining that
historical cause as unknown. Until answered, Q08 remains Active and Q09–Q30
pending. D1–D8/Q01–Q07 and canonical Fountain references reuse their preceding
verified evidence. Installed qualifications and all 13 security gates remain
open; Factory is unfinished.

## Verification and completion

Use existing affected checks: production Go build and dependency boundaries,
actual disposable PostgreSQL/native product checks, affected Cargo/native paths,
TypeScript/Lit/browser journeys and build staging as appropriate. Source checks
support their actual scope only. The final independent pass must revisit every
section and resolve worthwhile remaining findings. Completion requires preserved
intended behavior; missing features and original provider/installed/security
qualification remain explicitly unfinished in their owning product plan.
