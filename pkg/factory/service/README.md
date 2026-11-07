# Factory application services

This package owns application behavior over the canonical Factory store. It has
no HTTP request, native session, provider SDK or host-client dependency. Adapters
must supply an authenticated canonical actor and current authority; a value from
a browser request body is not admission.

`HumanEdits` accepts an optional original-content condition (external subject ID,
original title and UTF-8 body digest). The writer projects the exact native
preparation into the captured observed base; the command journal binds both.
After capture and admitted reconciliation, an unmet condition cancels that same
provider operation instead of dispatching the edit. This produces a durable
no-effect tombstone and keeps reply-loss recovery under the original command.
It does not synthesize no-effect from refusal or absence. A matched preparation
still needs the provider's native revision/content CAS at dispatch. The adapter
also refuses direct submit with an unmet condition, and shared outcome validation
rejects committed observations contradicting that captured condition. Lookup/
cancel recover original conditions; changing them requires a new command.

`HumanIssueCreateWriter` is the provider boundary for plain human issue creation.
Preparation returns credential-free intent to commit in the existing command
journal before dispatch. Submit/lookup/cancel must reconcile that exact saved
identity and namespace; uncertain delivery cannot be replaced by a new request.
The Fountain adapter implements this port with current native human admission
and exact native intent/receipt checks. `Application.HumanIssueCreates` now owns
submit/lookup/cancel over that adapter and the existing capture journal. It saves
preparation before dispatch, reconciles under current authority on every request,
and preserves original receipts and cancellation tombstones through the shared
outcome rules. Lost replies remain unknown until lookup; committed completion
needing intervention remains unfinished. Scoped command lookup/cancel recovers
server-retained writing within the admitted host/actor/repository. The shared
HTTP owner now consumes this service through Fountain's admitted composition;
installed UI and desktop gateway journeys remain subsequent work. These records
and ports grant no write authority.

`CreatedIssue` resolves the same saved command within its admitted scope and
asks the provider port for a currently readable canonical issue reference. The
port must authenticate the original committed receipt and confirm the exact
external identity/index through normal issue reads. A failed destination read
does not change the separately retained mutation outcome; HTTP can preserve a
committed result with no currently available destination.

`HumanLockWriter` is the neutral provider boundary for exact captured human
issue/PR lock/unlock requests. Preparation grants no authority; callers must save
the returned capture before dispatch and reconcile transport uncertainty using
that same capture. Fountain's `forgejo.HumanLocks` implements prepare/submit/
lookup/cancel with live admission, exact native intent and retained receipts.
`Application.HumanLocks` now saves the capture before delivery, performs live
lookup before dispatch or replay, and retains validated outcomes in the existing
command row. Scoped lookup/cancel recovers the original server-retained lock
state/reason. Lost replies stay unknown until authenticated reconciliation;
terminal receipts/tombstones remain immutable under concurrent observations.
A cached terminal receipt still requires live admission before being returned.
The shared HTTP owner now consumes this service through Fountain’s admitted
Product and frontend bridge, with strict boolean/reason input and safe scoped
results. Installed UI and desktop gateway qualification remain subsequent work.

`HumanStateWriter` is the neutral provider port for captured direct issue/PR
close/reopen. Its prepare/submit/lookup/cancel contract requires committing the
exact capture before dispatch and retaining its identity when delivery is
uncertain. `forgejo.HumanStates` implements it with current human admission and
strict saved-intent/native-receipt binding. Partial native completion stays
explicit, including committed state needing intervention. `Application.HumanStates`
now saves preparation before dispatch, reconciles under current provider admission
and retains validated observations in the existing immutable command journal.
Scoped lookup/cancel resolves server-retained desired state within the admitted
host/actor/repository/parent. Lost replies stay unknown until authenticated
reconciliation; original receipts and tombstones survive stale observations and
concurrent retries. Completion needing intervention stays open across Store
reopen and replay. The shared HTTP owner now consumes this service through
Fountain's admitted Product/frontend bridge, using strict boolean input and safe
scoped results. Installed UI and desktop gateway qualification remain subsequent
work. These values and ports grant no authority.

`HumanReviewWriter` is the neutral port for captured PR draft/final reviews.
Its prepare/submit/lookup/cancel contract requires committing exact preparation
before dispatch and retaining the original capture through uncertain delivery.
`forgejo.HumanReviews` implements it with current human admission and strict
canonical/native scope, intent and receipt binding. Namespace replacement and
native completion needing intervention stay explicit; cancelled-before-submit
tombstones invent no submitted metadata. `Application.HumanReviews` now saves
preparation before dispatch, reconciles under current admission and retains
validated outcomes monotonically in the same journal. Scoped lookup/cancel uses
server-retained event/prose within admitted host/actor/repository/PR. Lost replies
remain unknown until authenticated reconciliation; original receipts survive
stale observations, concurrent submissions and Store reopen. Completion needing
intervention stays unfinished. The shared HTTP owner now consumes this service
through Fountain Product admission and the frontend bridge, with strict required
event/body fields, safe scoped cleanup, bounded prose envelopes and private
provider evidence. Actual HTTP requests qualify both reply losses and current
session refusal. Installed review UI and desktop gateway remain subsequent
consumers. The port grants no authority.

`HumanInlineWriter` is the neutral port for captured draft/immediate PR inline
comments. `Application.HumanInlines` commits exact mode, body, full immutable
anchor and opaque preparation before dispatch. It reconciles every request
through current provider admission, retains outcomes under the existing command
row lock and preserves the original capture across reply loss and Store reopen.
Scoped command lookup/cancel accepts only admitted host/actor/repository/PR and
command identity; it recovers writing and anchor from the server journal. Stale
observations cannot erase committed receipts or completion intervention, and
contradictory effects or changed receipt bytes refuse. Only admitted not-observed
dispatches saved intent; known records are never resubmitted. The shared HTTP
owner now consumes this service through Fountain's admitted Product and frontend
bridge, with explicit full-anchor input and safe scoped results. Actual HTTP
checks qualify both reply losses, retained intervention and withheld post-commit
session results. Installed inline UI and desktop HTTPS remain subsequent work.

`HumanThreadWriter` is the neutral port for captured thread resolve/unresolve.
`forgejo.HumanThreads` implements preparation, submit, lookup and cancellation
with current human admission, canonical/native identity mapping, exact thread and
full/unknown original context, and installation-bound provider receipts. The
application must save the returned winning capture before dispatch; uncertainty
is reconciled against that capture. Lookup/cancel remain available after native
repository permission loss while current human/installation admission holds.
Canonical binding and provider intent digests remain separate.
`Application.HumanThreads` now commits the exact winning capture before dispatch,
reconciles every request under current provider admission, and saves monotonic
observations in the existing command row. A known record is never submitted
again. Scoped lookup/cancel recovers original thread/state/context from the
server journal using only admitted host/actor/repository/PR and command identity.
Lost replies stay unknown; exact pending/terminal receipts survive Store reopen.
Only proved complete outcomes finish. Real SQL/Git/Unix/PostgreSQL checks cover
full/unknown context, large receipts, concurrency, interrupted completion and
live admission/installation refusal. The shared HTTP/Product consumer now joins
the same service through Fountain's admitted frontend/private wrappers, with
explicit thread/state/full-or-unknown context input and safe scoped results.
Real HTTP checks qualify both reply losses, pending completion without resubmit,
Store reopen and withheld post-commit session results. `HumanThreadReader` is a
separate neutral port for safe selection facts, explicitly supplied in the
Fountain composition. `HumanThreads.Selection` validates canonical scope, stored
original context (including explicit unknown) and resolution/action facts. It
allocates no command and returns no private preparation. Context can remain
available when ordinary Git placement is unproved. Submit always prepares and
admits the write afresh. Shared installed UI and desktop access remain subsequent
work.
These values grant no authority.

`Application.HumanReviewRequests` composes the admitted reviewer writer with the
application's single Store. Submit commits the winning canonical user/team capture
before dispatch and reconciles it under current provider admission. Known pending
or terminal records are never submitted again; transport errors retain uncertainty
under the saved namespace, operation and proof. Lookup always checks current human
admission before exposing a saved receipt. Scoped lookup/cancel takes only the
host/operator/repository/PR and command ID, loading the original target and desired
state from the journal. Foreign scopes and client-supplied cleanup targets refuse.
Cancel can create a native tombstone before submit; it leaves known primary effects
and their separate completion intact. Actual PostgreSQL/manager/Unix/SQL/Git checks
qualify commit-before-provider calls, exact replay without dispatch, lost replies
and Store reopen, permission-loss cleanup, session and installation refusal, and
pending/intervention completion without resubmit. The shared HTTP/Product reviewer
consumer now exposes strict user/team submit and scoped lookup/cancel through the
admitted Fountain frontend/private wrappers, with safe result fields and final
current-session checks. Installed UI activation follows.

`HumanMergeWriter` defines prepare/submit/lookup/cancel for canonical ordinary
merge and manual-mark captures, with typed query/admission/conflict/namespace/
unavailable errors. Callers must persist preparation before dispatch and reconcile
lost replies against the original capture. The Fountain adapter checks native
preparation/receipt identity and current human admission.

`Application.HumanMerges` composes that writer with its single Store. Submit commits
the winning preparation before provider dispatch and checks current admission
before returning any saved result. Known pending/terminal effects never resubmit;
failed replies retain uncertainty under the original capture. Scoped lookup/cancel
loads the original method/message or selected commit using only admitted host,
operator, repository, PR and command ID. Client-supplied replacement intent refuses.
Cancellation may commit preparation before creating a native tombstone; a known
primary retains its separate completion without another cancel. Real native/PG
checks qualify concurrent capture winners, rollback before dispatch, scoped
recovery, lost replies and unfinished completion. Shared HTTP/Product now exposes
strict ordinary/manual submit and scoped cleanup through admitted Fountain
composition; original provider evidence stays private. Installed UI follows.

`CollaborationIssues.Read` consumes a neutral provider read port and validates
canonical repository/actor scope, stable remote issue IDs, explicit issue kind,
observed versions, page limits and continuation completeness. Open/closed/all issue pages
and any-state issue detail are available through the shared HTTP owner. Missing
sources, malformed scope and unavailable observations refuse; no provider login
or action permission is inferred. PRs and action facts remain later T06
consumers. Read revisions describe returned resources/pages, rather than
guarded-mutation preconditions or global atomic snapshots.

`CollaborationDiscussions` consumes the same human/provider boundary for ordinary
issue and PR comments, preserving the separate `IssueComment` and `PullComment`
records. Every item must retain the requested repository, kind, number and stable
parent ID, with unique comment IDs and observed resource/page versions. Missing
sources or current access remain errors. The service neither stores a copy of
the provider conversation nor grants comment/review permission from readability.

`RepositoryAuthority` consumes fresh `access.AdmittedActor` evidence from a
trusted current-authority port. It binds host, canonical actor, source kind and
connection/runtime identity; checks exact repository, permission and expiry
after the authority read; and returns no metadata after error/cancellation.
`Visibility` supplies catalogue/feed admission and `Configure` supplies current
administration for request/policy mutations. Only explicit confirmed invisibility
is hidden; unknown access, missing ports and invalid evidence remain errors.
Every call re-queries the adapter. An unexpired envelope is never a permission
cache, login or execution/publication/spending grant. Adapters still own current
provider/grant truth and canonical identity binding, and mutations retain their
existing authority rechecks. Native accounts supply their real connection;
Fountain sessions have neither an invented connection nor a runtime identity.

`NewApplication(ApplicationPorts)` composes the canonical coordinator, priority/
planning judgments and its background workflow callback from explicit runtime,
custody, collaboration, evidence, effect and judgment ports. It opens no store,
loads no configuration/key, acquires no ledger lock and starts no reconciliation
or work. `Application.Start` explicitly starts the existing coordinator under its
ledger lock; hosts first supply admitted runtime ports and bind worker channels.
Before starting the coordinator it calls `Application.Recover`, which traverses
all bounded pages of interrupted-assessment recovery. Hosts without admitted
execution ports can explicitly run this same recovery before readiness, without
starting the coordinator or modifying retained execution facts. Recovery uses
the host's startup context; it neither replays models nor advances workflows.
`StopAdmission` cancels background progress and refuses coordinator-gated work;
`Close` joins background progress and admitted requests before releasing ledger
ownership. Hosts retire listeners/upgraded peers and close the store only after
the application drains, including when forced HTTP closure leaves handlers alive.
Draining also applies before background startup. Transport uses the same
coordinator admission gate; there is no second lifetime or journal owner.
Missing optional ports preserve unavailable/wait
behavior; constructing a coordinator grants no runtime or provider authority.

`Application.Background` and `Application.IssueMaker` supply one shared assembly
for continuation and request-side collaborator readback. Lightweight application
views reference the same store/coordinator. They create no extra workflow journal
or progress loop. Background provider availability stays live through the host's
chooser resolver. Appliance web composition supplies its native adapters here;
standalone runtime/account admission and Rust child supervision remain consumers.
Fountain and the native application now consume the same `httpapi.Product` router
over these services. Construction configures no native connector or background
execution. Native server startup explicitly recovers store-owned assessments;
actual provider/runtime admission and execution reconciliation retain their later
consumer slices.

`WorkerResults` fills server-owned plan/review/correction/task/delivery bindings
for an admitted worker. `CheckNewFinish` uses the canonical terminal mapper and
approved catalogue before a new proposal can freeze work; it does not record or
settle that proposal. `Status` distinguishes consumed work from actual recorded
settlement. `CheckpointContext` reads bounded task/output/integration facts
through `WorkerCheckpointSource`, also used for task-output binding. Canonical
sources and typed settlement still own truth; missing/malformed bindings remain
validation gaps. Binding/readback/checks create no journal, grant, checkpoint or
runtime operation. Transport retains capability admission, payload/envelope
checks, required-input gates, rate slots and the existing submission store.

`WorkerInputs.Read` supplies required-current facts and joins Test Pilot's
existing verification port for an admitted worker. Missing sources/verification
remain explicit gaps. Successful evidence must match the delivery and worker;
source/scope/cancellation errors return no partial current view. Joining valid
verification removes only its own missing reason: absent allowance or other
required inputs keep work incomplete. Reads perform no judgment, grant, write
or runtime operation. The source remains canonical, and transport admission is
still required. MCP current/status reads and mutation gates use this same reader;
bounded presentation belongs to `mcpwire.RenderCurrent`.

`WorkerConsultations.Evaluate` validates admitted single/batch System One
questions and dispatches through `WorkerSystemOne`. Production supplies the
existing work-discussion journal. Failed request recording prevents dispatch;
result recording uses a bounded context independent of worker cancellation, so
actual answers and uncertain/failed/unavailable calls retain their real status.
Failed result recording returns no successful response. Explicit repeats call
the provider again under the same question/discussion identity; the journal is
not a funding or replay receipt. Optional journal-less advice keeps its existing
behavior. The service introduces no credential/provider SDK, workflow grant,
automatic retry or second ledger. MCP authentication, rate slots, envelope size
and schema framing remain in transport, which supplies an admitted capability.
Native transport binds its selected-source provider from that capability after
admission. It preserves the three primitive contracts and one-call batches,
uses one existing command per explicit invocation, and checks the worker again
before adopting a result. Its usage envelope reports the actual elapsed process
interval; no provider token counts are inferred. Completed native raw output is
accounted before typed-answer validation and grants no workflow authority.

`WorkerContext` loads already admitted worker context from canonical terminal,
assignment, run, priority-receipt and required-current sources. Those shared
interfaces also serve protocol mutation/current-input checks. `Load` retains
same-actor/project/repository/attempt parent visibility; missing or foreign parents
stay incomplete. `Foreground` consumes recorded choice/current-only resolution
without judging, and `WorkerContextRead.Projection` preserves the existing worker
metadata/eligible-history contract without raw prompts or native provenance.
`Evidence` returns bounded own-attempt IDs/roles/outcomes, keeps future collections
explicitly incomplete and returns no successful partial page for unknown rows or
sources. These reads create no work/judgment/journal or runtime operation.
Transport owns capability admission, rate slots, MCP route/schema and error framing.
Callers must supply an already authenticated/admitted worker; this reader neither
grants nor refreshes authority.

`WorkerAdmission.Prepare` consumes an authenticated worker capability and a typed
runtime observation port. It reloads the recorded capability, assignment, run and
current stop/preparation admission before and after I/O, checking exact captured
scope and process identity. Native process/tool/worktree/custody facts require no
container/unit/image; appliance observations retain their actual boundary facts
and cannot replace a known invocation. Missing, uncertain, exited or sealed
workers refuse. Existing store transactions own observed activation and stop
ordering; the service neither launches nor retains a new process observation.
Runtime kind never follows from a request or an equal ID. The HTTP layer now
only supplies its appliance observation adapter; native composition supplies its
own observer. Declared native records exercise this seam but do not qualify an
installed executor.

`WorkerVerification.ApprovedCatalog` and `Evidence` read planning check bindings
and Test Pilot evidence through explicit protected-source ports. They reload
recorded active capability/work/run/preparation admission around source I/O.
Planning also requires the same draft and current requirement/approval heads;
successful results must match the preparation. Review evidence must match the
exact delivery, reviewer workspace and run, with current accepted-scope/task/
merge/closure prerequisites read again from the existing store. Native run and
preparation ownership must agree and needs no appliance Project lookup. Missing
checks retain unavailable guidance; admission, cancellation and malformed or
foreign successful observations return no view. These independent observations
are not an atomic snapshot or permission to execute, adopt, settle or publish.
The controller still owns protected check reads and repeats verification during
adoption. HTTP supplies those existing readers and preserves the MCP wire.

`WorkerTasks.Execute` owns Builder/Fixer task-command admission over the recorded
capability, assignment, preparation, admitted Muse run and retained current plan.
Typed observation and task-command ports carry the captured runtime variant.
The service rechecks current work, terminal proposal, process and manifest after
observation and again after the command, validates the owner's receipt and
returns its result unchanged. Missing/uncertain ports, foreign process/task/
receipt and changed authority return no view; an uncertain command is never
retried implicitly. A command may already have acted when its reply or later
admission is lost: recovery belongs to that same task owner under fresh admission.
The service creates no submission/task history, grants no placement or credential
and launches no process. Native work requires captured launch/tool/worktree and
process evidence; no appliance identity is synthesized. The appliance adapter
rejects native requests. Installed native task execution remains later work.
`Run.SupportsTaskCommands` gives discovery and admission one captured-tool rule,
including absent appliance pins on native runs.

`Feed` reads subscriptions, saves preferences under CAS, selects feed windows and
projects post/record/parent facts. `FeedVisibility` checks each canonical repository
against the current actor and returns its display name. Every served entry is
checked, including repeated entries in one repository. Filtered rows still advance
the durable continuation cursor. Project-scoped reads require current visibility
and bypass only personal subscription narrowing.

`SaveSubscription` derives the subscriber from the admitted actor, replacing any
subscriber/revision on the input with that actor and the explicit expected
revision. Preferences never grant visibility. Repository and kind filters narrow
the requested window; an empty kind intersection cannot become an all-kinds read.
Store ownership, transactions and journals remain in `internal/store`.

The Fountain HTTP feed/subscription routes now call this service. Their adapters
retain request parsing, actor discovery, native permission checks and HTTP error
projection.

`Artifacts.Read` returns complete retained artifact bytes behind current canonical
actor/repository visibility before and after I/O. The exact ID, canonical link,
metadata, recorded size and SHA-256 digest must match; incomplete/missing uploads
remain distinct from unknown access/source failures. It owns the verified byte
buffer so an adapter's reuse cannot change a returned download. Existing name/
media rules and link formatting have one domain owner shared by upload and read.
HTTP retains attachment, sandbox/nosniff/no-store and range response policy. These
reads never fetch runtime files, finalize uploads, publish posts or dispatch work;
the existing upload/store journal retains those responsibilities.

`Library` owns admitted human knowledge/decision search and revision reads over
the existing projection/revision journal. Current canonical actor/repository
visibility is mandatory before and after I/O. Unknown access/source reads and
foreign item/history payloads return no partial response. Existing text/kind/
cursor validation has one owner in `factory.LibraryQuery`, shared with the store.
Current-only, removed-history, contested evidence, bounded paging and exact
revision provenance retain their existing meaning. Browser item/history scope is
a string; retained domain JSON and the worker MCP wire keep their existing
encoding. Human reads neither mutate evidence nor consult a model or dispatch
work. The native adapter supplies current session/visibility, while worker MCP
retains its own capability admission over the same `LibrarySource` interface.

`Feed.Page` assembles the complete host-facing feed, including human post detail,
private action readback and existing system/record projections. It uses the same
ledger for feed and post reads and the canonical `RepositoryVisibility` port for
their current admission. Only confirmed hidden/missing rows are omitted; unknown
authority/detail reads return no partial page. After all detail assembly, every
returned kind is checked again and gets its current repository display name.
Late revocation filters earlier human/system/record rows while preserving the
source cursor/truncation. Independent provider/store reads remain observations,
not an atomic snapshot. This assembly neither drives workflows nor changes their
journals. The HTTP handler now parses/adapts scope and returns this shared page.

`Background` is the coordinator's shared lifetime callback for post and
contribution continuation. Hosts supply admitted stage ports and a current Choice
availability resolver. Construction and collaborator readback start no work.
Each drive uses the existing bounded post page, then at most one contribution
claim; cancellation stops continuation. Availability is resolved again before a
contribution claim, so a provider connected or withdrawn during post I/O needs no
restart. The runner retains no HTTP object, session or credential bytes. Native
application composition binds it after graph/effect ports are ready, while
readback uses the same collaborator assembly. `ChoicePostJudge` owns the existing route criteria
and result projection over the shared Choice port; transport/model/credential
selection remains host-supplied. Journals, finite funding, current admission and
restart recovery keep their existing owners. Contribution claims now hold a
dedicated PostgreSQL producer session; the store exposes bounded recovery for
proved released ownership. Both host startup paths now run this recovery through
`Application.Recover`; ongoing admitted runtime progress remains a separate port.

`DrivePostIntake` and `DrivePendingPosts` own durable post routing, authoring,
feedback, issue effects and bounded background continuation. Their explicit
`PostCollaborators` ports provide current context, routing judgment, funded
drafting, captured Issue Maker processing and native effects. The service owns
the routing policy and pending/error contracts; model/network and native grant
adapters remain outside it. A missing stage waits without spending. A captured
request cannot fall into legacy authoring when its Issue Maker port is absent,
and unknown intake-path reads fail instead of selecting another workflow.

An issue lookup returning zero with no error confirms absence. Any lookup error
is unknown and cannot trigger creation. A recovered issue settles the existing
effect; retry and restart use the same store stage, operation identity and CAS.
There is no competing workflow journal or automatic work on construction.

`IntentPostDrafter` owns funded post authoring behind `PostDraftStore` and
`PostPlanTransport`. It derives one intent/revision from the recorded post,
reuses its durable planning attempt before consulting launch errors, and reports
the recorded queued/running/unknown/settling state through `PostDraftPending`.
The existing coordinator retains funding, launch admission and run accounting.
Only a single assessed candidate with one task becomes an issue draft; no
fallback text or new planning attempt substitutes for an unresolved answer.
`PostDraftTitle` also labels the captured Issue Maker's initial request.

`IssueMaker` owns capture-first orchestration through `IssueMakerEffects` and
the same funded planning transport. It publishes the initial request receipt
before planning, waits when effects are unavailable, checks request-scoped
authority before refinement and keeps uncertain final writes eligible for
background recovery. The provider adapter reconciles the same conditional
operation through the existing journal; no second run or issue is needed.
Progress/authoring persistence failures propagate to the caller. `PostIssueBody`
is the shared draft/receipt formatter consumed by the native adapters too.

`PostIssueEffects` owns ordinary post issue creation, receipt lookup and discussion
feedback. It reads the actual canonical source and recorded draft, requires current
human/publishing admission through `PostEffectGraph`, and rechecks source/draft
after permission I/O. Deleted, revoked, changed, cancelled or foreign inputs cannot
reach a write. Semantic graph payloads and receipt facts use the existing codec;
provider bytes stay adapter-owned. Historical `commentissue-<post>` operation IDs,
source revision guards, payloads and feedback markers remain unchanged. Only an
explicit not-observed lookup permits creation. Unknown, unavailable, contradictory
or uncommitted outcomes remain pending; committed receipts must match the exact
operation, canonical repository/publishing actor and sole source key. Current
admission is checked again after lookup before returning. Lost replies recover
through the provider's existing operation journal and the existing intake CAS,
including after store reopen; no competing journal is introduced. The HTTP adapter
now only composes this collaborator. Independent local/provider reads are not an
atomic transaction, and ordinary issue/feedback delivery grants no execution
authority.

`IssueIntakeCapture.Capture` owns source-author/revision admission and retained
proof capture/reuse. Hosts supply current post visibility, a trusted issuer and
the existing proof decoder. Issuance receives canonical source/actor/repository/
digest fields; the returned scope and decoded opaque proof must match that exact
request and remain unexpired. Source and permission are checked again after
issuance, before the existing store journal accepts the proof. Valid retained
proofs are reused; known absence/expiry permits issuance, while unknown reads or
decoding stop without replacement. Missing ports, cancellation, withdrawal or
changed sources cannot capture authority. The store also checks current body
digest and deletion/revocation at persistence. Proof bytes stay server-side;
capture launches no issue mutation, model, worker or spending. Fountain owns its
native issuance/encoding, and a desktop connector must supply its own admitted
issuer rather than borrowing a Fountain session or its atomic guarantees.

`CapturedIssueEffects` owns server-retained proof/source validation, the initial
captured issue, scoped conditional mutations and admitted Issue Maker tools.
Native issuance/authentication and live provider admission remain host ports;
no SDK, session or provider bearer enters semantic commands or the slot/step
journal. Expiry, current source digest/revision/author/repository and the exact
active role scope are checked before dispatch, including after journal allocation.
Initial creation also requires the recorded draft. Receipt repository, subject,
slot, title and body digest must match before a reply is published. Lost replies
reconcile the same proof/slot/step through the provider, preserving the existing
mutation journal and intake CAS after reopen. A zero initial lookup asks that
conditional writer to reconcile its same captured slot; it does not prove external
absence or permit a new grant. Tool calls derive scope from admitted assignment
facts and recheck active work and terminal-proposal fences before effects. Proof
metadata alone is never authentication, and these separate local/provider reads
are not an atomic transaction or permission to execute agent work.

`Requests` owns owner-private overview and saved-review reads. It separates
recorded work delivery from adopted product-outcome evidence, preserves exact
mapping/effective-scope facts and never invokes a judge or dispatches work.
`RequestSetupReader` supplies runtime setup facts after request ownership is
checked. Unbound requests need no runtime port; a missing port explicitly reports
unavailable setup without hiding the saved request. Invalid observations and
reader failures still refuse rather than fabricating readiness. Native workspace
observations belong to the desktop runtime; appliance Project/preparation queries
remain in the Fountain adapter. `WorkItemState`
also supplies the existing admitted revision-history projection.

`SaveRevision` binds the admitted owner and server timestamp, retains the
original timestamp on exact head replay and requires current `RequestWriteAccess`
for every repository-bound save, including replay. Unknown head reads propagate
before any save. The existing store transaction owns immutable identity conflicts,
head CAS, private department membership and the committed repository binding.
`CreateDepartment` also replaces supplied ownership. `Revision`, `Catalogue`,
`History` and `Departments` retain owner-private reads and bounded pagination.
Unbound drafts require no repository grant; saving a request starts no work.

`SavedRevision` reads a single immutable save under its intent and admitted
owner, even when later revisions have moved the head beyond bounded history.
It preserves the captured record for exact command confirmation, without write
authority or replay. Missing/foreign identities remain private not-found results.
An absent record does not settle a still-running save.

`RequestPlanning` owns draft planning and accepted recorded-plan generation.
It requires current request ownership/repository authority before invoking its
planning or graph ports. Recorded planning attempts are recovered before source
assembly, retaining their exact owner/head/scope/operation binding and allowance
even when a fresh coordinator sees the repository's protected session as busy.
Missing or unreadable attempts cannot invent a recovered launch. Generation
retains the coordinator's reviewed-plan adoption, currency and effect journals.
`Generate` requires the selected accepted revision, checks it against the private
head before repository authority, and carries it through the generator port.
The coordinator checks the same identity again after authority and before each
generation entry. A moved head returns stale without selecting newer work;
replay retains the original operation and requires current authority.
The service respects caller cancellation; only the HTTP adapter supplies the
existing bounded context that survives a disconnected browser response.

`RequestExploration` owns product design, clarification identity, assumption
settlement and atomic draft/review persistence. Commands use the existing
`ProductJudgment` Choice port and coordinator receipts; no model transport or
credentials are selected inside the service. Explicit approaches and human
answers survive subsequent rounds. Private exploration grants no repository
effects or execution authority. `ReviewWalkthrough` requires current repository
authority and the exact completed planning revision, preserving the draft head.
`Status` ignores command choosers and replays recorded advice with a chooser-free
coordinator. `Generation` also remains an owner-private read without dispatch.
`SavedGeneration` resolves the exact immutable accepted revision through the
current private request owner, then reads its existing generation independently
of the head. A missing operation stays absent; unknown/foreign/nonaccepted or
mismatched revision identities remain private not-found outcomes. It wires no
chooser, write grant or graph port and never resumes an operation.

`SavedPlanning` reads a selected immutable draft's recorded planning attempt
independently of the current head. Its `PlanningReceipt` includes the captured
operation, assignment/run identities and reported outcome/walkthroughs. Full
assignment validation and exact owner/intent/revision/scope-digest binding precede
disclosure. Scoped `none` means no recorded attempt; unknown/foreign/non-draft or
misbound inputs remain private not-found. It reuses the status projection but
never wires a chooser or replays review advice, and never reports a confirmed
native start. The current-head status/review APIs keep their existing behavior.

`ScopeHistory` owns bounded structural comparisons, generated lineage,
submitted-graph facts and mapped-work authority. Each page reads one extra
predecessor so its oldest displayed entry still has a real comparison. Work
states and generation receipts remain separate: a submitted graph does not
prove delivery, and older mappings lose head authority when the request changes.
Unknown authority stays unknown. Reads neither mutate captured rows nor invoke
model, native effect or execution ports.

`Posts` owns human/worker/record views, reply paging and the product-review
handoff. `PostVisibility` is mandatory and is checked for each served entry;
reply failure returns no partial private page. Author-only intent details are
redacted for other actors. `ReviewProduct` requires current write authority and
private request ownership, using the existing source-bound store CAS. An
accepted product action only opens the owner's private request and retains that
capability with repository read access. Other review actions require current
write authority. `WorkerPostResponse` also supplies the admitted MCP writer's
wire projection. None of these reads advances authoring, funding or execution.

`PostCommands` owns submission/retry using the admitted canonical actor, current
visibility and `PostCapture` ports for provider proof/context evidence. Submission
recovers an existing author/repository/nonce before resolving a live parent, so
deleted or changed parents cannot strand the saved command or rewrite its source
snapshot. Retry retains the existing author/revision CAS and refreshes capture
for its committed revision. Missing context is a durable wait. Proof capture is
best effort, while the captured Issue Maker path still requires its valid proof
before effects and cannot fall into legacy authoring. Commands start no work.

`Contributions` owns one-source background contribution review with an explicit
Choice port. Unwired instances claim nothing. Store discovery/claims admit only
eligible running roles; all independent requests are fixed before consultation.
The existing assessment post retains answers and the decision, without a second
judgment journal. Cancellation/deadline blocks a late invitation, while a bounded
detached settlement records the unavailable outcome. Source/reply/role currency
is checked again by the store at settlement. `Drive` returns whether a source was
claimed and any error; the persisted assessment remains authoritative. Hosts
with repository-selected funding may supply `ContributionJudgments`: readiness
runs before claiming, then that host returns the same per-question call records.
Native composition batches the three original formulations per role and visits
roles sequentially, preserving the existing deadline, thresholds and settlement.
Preflight checks enough remaining allowance for all role batches; atomic source
admission still controls each actual call. Fixed-provider hosts keep their
existing independent-call implementation.

Each claimed assessment carries a private owner identity backed by a dedicated
PostgreSQL advisory-lock session through settlement. The producer claims/settles
on that same connection, then closes it; a lost session cannot reconnect and
publish a late invitation. `Store.RecoverContributionReviews` traverses bounded
pages and only marks a pending receipt unavailable after acquiring the released
owner's lock. It preserves that receipt without replay or new model calls. Live
owners and unowned historical claims remain untouched regardless of age. Both
host startup paths consume this traversal before readiness/coordinator startup;
this continuation does not guess that another producer stopped from a timestamp
or retry unknown consultations. Native startup does not activate model/workflow
progress; native execution admission remains a later consumer.

`Repositories.Catalogue` lists existing canonical repository observations in
bounded windows with mandatory current `RepositoryVisibility`. The port receives
the admitted canonical actor and repository and supplies a current display name.
Only confirmed `ErrRepositoryHidden` is omitted; unknown access, cancellation or
store failure returns an error with no partial page. Denied rows still advance
the continuation. Repository IDs and cursors remain exact decimal strings.
`Complete` means the registered ledger traversal ended, not that all remote
repositories were discovered. No native Project inventory, provider discovery,
enrollment, model consultation or work dispatch is involved.

The admitted Fountain adapter exposes `GET /api/factory/repositories`, including
through its frontend proxy, with host/actor scope and current session checks.
Other hosts' observations remain hidden there. The global feed now reads this
catalogue instead of Spaces inventory. Its Fountain adapter checks host/actor
scope, canonical IDs, whole-page labels and forward continuation before exposing
each page. Earlier valid pages remain usable after a later failure, with a visible
incomplete-list notice. Manual Refresh reloads the catalogue and activity;
provider repository picker/discovery and host navigation remain in the adapter.

`Requests.VerificationAllowance` and `IncreaseVerificationAllowance` own private
verification-limit reads and durable increases. Bound reads and every command
replay require current `WriteAccess`; missing authority is unavailable, and
private allowance data is not returned after denial. Unbound reads remain
owner-private without a repository port. Canonical ownership and the host's
admitted audit principal are separate. The existing store journal retains the
original principal/command while replay returns current limits and charged usage.
The store compares the admitted repository under the request head lock before
recording or replaying a grant, so a binding changed during authorization cannot
receive that grant. Such a race returns stale revision without a command row.
Neither reads nor increases dispatch verification; existing execution admission
still owns spending and launch. Fountain HTTP handles parsing/session checks and
supplies current native administration through the request authority port.

`PostContext.Capture` owns bounded human issue-context capture through
`PostIssueReader`, using canonical actor/repository identities and the existing
collaboration page/issue records. The Fountain adapter only supplies current
admitted native reads and normalizes their used fields; unobserved external IDs
are not fabricated. Capture requires the exact author/revision, current
visibility before each page and again before persistence, available observations
and matching issue scope. Unknown/partial pages or a missing selected issue do
not save partial context. Open pages deduplicate identities while retaining order,
and closed bound/source/parent issues are read explicitly with their known origin
and worker status. Repeated cursors and more than 250 open-page reads are refused;
the existing 250-open-issue bound remains. The store retains the revision CAS.
`Prepare` reads only that saved revision for background routing, without provider
access; missing evidence waits before a routing judgment. Capture never enrolls
execution or consults a model. Native HTTP post/retry and background composition
now use these shared owners directly.

`Policies.Apply` owns repository policy assembly and admission through explicit
current `WriteAccess`, `PolicyBindingAccess` and `PolicyWriter` ports. The command
contains existing token/actor references; their operation kinds come from the
policy slots. Repository and human attribution come from admitted arguments.
Current administration is checked before binding reads and again before writing,
so a binding lookup cannot carry an expired admission into a grant. Missing ports
are unavailable. The resolver must use its separately enrolled binding on the
independent reviewer account. Existing attempt/reassessment defaults are applied
once; zero intent minutes stays unfunded. The existing coordinator/store retain
policy CAS, dispatch withdrawal and pending native cancellation ownership.
The Fountain HTTP adapter supplies current session/actor scope checks and parses
the same command; the former API-owned actor parser/policy assembly were removed.
This service neither enrolls credentials nor starts execution.

`Activity.Read` and `Update` own current activity visibility, host navigation and
display-setting admission. The store supplies confirmed bounded activity facts
without native repository paths or issue/PR URLs. `ActivityVisibility` supplies
current navigation after checking the admitted canonical actor/repository;
adapters validate their URLs and choose their own route shapes. Read refreshes
visibility/navigation after fact assembly and returns no facts after denial or
failure. Update requires current administration through `WriteAccess`, binds
settings attribution to the admitted actor and retains the store's revision CAS.
The returned read rechecks visibility; a later read failure does not undo the
already committed settings change. Settings remain independent of execution
policy and neither operation dispatches work. Fountain owns its existing native
links and current session checks in the HTTP adapter.

`WorkJourneys.Read` owns the full recorded issue-work journey and its projection
helpers: lineage/current phases, tasks, commit/review/correction evidence,
judgments/waits, reassessment gates, adoption, delivered work and verified apps.
It resolves the recorded owner/publication from the store instead of accepting
caller-selected evidence, and rejects conflicting repository/issue/assignment
scope. `WorkVisibility` receives the admitted canonical actor/repository before
reading and again before returning; denied/unknown reads expose no partial view.
Existing bounds, incomplete-reason codes and exact evidence joins remain
authoritative. No-attempt is explicit, and no judgment, grant, provisioning or
execution is driven. Fountain HTTP uses this service through its current session
and visibility adapter. Projection definitions have one owner in this package;
the service exports only the journey view needed by its real HTTP consumer.

`WorkControls.Read` owns watch/intervention capabilities and outstanding explicit
retry projection. It resolves recorded assignment/run/readiness/dispatch facts
and takes explicit current visibility, execution-authority and runtime-membership
ports. Canonical actor/repository scope is preserved; the Fountain adapter alone
uses the native user key for native Project membership. Missing ports are
unavailable and late visibility denial returns no control view. Watching requires
current read/session admission, not write authority. Stop/retry/takeover and
pause/resume retain their independent mutation, settlement, membership and dispatch
gates. A queued retry is a requested control, never confirmed execution, and
disables another retry. Capabilities never authorize a command by themselves;
actual lifecycle admission still revalidates when the human acts. The same
`WorkCapability` wire shape also serves the existing run-status response.

`Issues.Read` assembles the complete issue-work response from current acceptance
evidence and recorded readiness/check/merge/assignment/publication facts. Its
status and navigation ports receive admitted canonical scope. It rejects
conflicting record scope, and visibility is rechecked before returning any view.
Journey and control projections use the same selected owner/publication rather
than independently resolving another root. These remain bounded observations,
not an atomic snapshot of provider and store state. Checks join the recorded
PR number/identity, never an equal issue number, and missing PR checks stay absent.
Existing candidate-history staleness, original closure evidence and bounded
review/correction tails retain their meaning. Navigation stays host-owned and
optional. `PublicationOperationsView` also supplies the existing assignment HTTP
response, keeping one effect/link projection. HTTP now parses scope and supplies
current evidence/authority/navigation adapters; it owns no issue assembly.

`WorkReads.Assignment` and `Priority` own the remaining recorded work panels.
Both require current canonical visibility before reading and again before
returning. Assignment projects the recorded prompt/result, held reservation and
publication through one shared definition, rejecting mismatched related scope.
Priority pages existing journal receipts with bounded cursor/limit validation;
it does not rebuild candidates or make a judgment. Cross-repository candidates
are counted without enumerating identities, including every individual call's
selected option and the final probability distribution. Visible choices remain
exact. Missing assignments and empty comparison windows remain distinct. Their
HTTP adapters now only parse/admit scope and project responses/errors.

`RepositoryStatus.Read` owns policy/grant/capacity/sponsor and queue assembly with
explicit canonical visibility, binding, preparation and effective-authority
ports. Missing runtime ports are unavailable; a nil preparation returned by its
port is known absence, not an inferred ready environment. The existing native
adapter alone resolves Fountain bindings and Project preparation. Sponsor
allowance amounts remain private to their admitted canonical sponsor. Optional
diagnostics require their own current administration check; unknown diagnostics
are omitted. Visibility is checked again before returning any assembled view.
Queue counts come from exact repository-scoped recorded states: queued controls
and assigned dispatch work. Active is not a claim about live host processes.
Status reads neither provision nor launch work. `RepositoryPreparation` also
supplies the existing access/request setup projections without a duplicate DTO.

`RuntimeGrants` owns capacity, repository operator permission and environment
permission commands. It derives attribution from the admitted canonical actor,
keeps the authenticated host principal separate, validates the existing records
and requires explicit current operator/visibility/environment-owner ports.
Repository visibility is read before the final operator check. Missing admission
or writer ports are unavailable; denied or cancelled commands never reach the
writer. The existing coordinator remains the single transactional CAS/withdrawal
and publication/merge cancellation owner. Saving permission does not provision,
enroll or launch work. The native adapter alone interprets the configured native
operator and current Fountain repository ownership. Environment profile semantics
remain with the existing domain; this extraction does not qualify native execution.

`Sponsorships.Apply` owns connection sponsorship selection, validation and
canonical sponsor attribution behind current visibility and custody ports. It
retains a still-live recorded broker grant, otherwise selects the newest live
grant for the exact connection/runtime scope, and records the current credential
generation. An unreadable prior sponsorship is unknown and stops before custody
selection. The host verifies connection ownership and supplies nonsecret custody
facts; the shared service reuses the existing identity grant record, whose actor
belongs to the broker rather than the canonical sponsor namespace. Missing ports,
unknown custody, late visibility denial and cancellation cannot save a grant.
The coordinator remains the CAS/withdrawal owner, including the last-active-
sponsorship rule. Native Project binding and broker requests stay in the adapter.
These separate reads and Factory writes do not form an atomic broker transaction;
execution must still obtain current broker admission. No credential is copied,
enrolled or acquired by a sponsorship command.

`RepositoryAccess.Read` owns settings editability, required-authority actions,
preparation and per-connection usage behind current visibility, viewer, owned-
connection and runtime ports. Sponsor attribution never implies connection
ownership. Unknown ownership hides every budget/usage value; unknown SQL usage
is withheld rather than rendered as zero. A successful empty usage query remains
a confirmed zero. Environment permission points specifically to the repository
owner, matching its save admission. Viewer powers stay independent, and a stale
session disables edits. Missing runtime ports and unknown viewer/preparation
facts return no partial view. Visibility is checked again before returning.
Host adapters alone resolve native membership, operator identity and broker
ownership; authority and preparation reuse the status reader's runtime ports.
These separate reads are a read model, not an atomic provider/store snapshot,
and they do not replace current admission on writes or start work.

`Interventions` owns repository pause/resume/completion retry and run stop/retry,
current-context selection, delivery repair and takeover commands. It validates
their exact action/payload/command identities, requires current canonical write
authority, and delegates unchanged receipts to the existing coordinator. Run scope
comes from a required runtime port; a conflicting recorded display repository
refuses the command. Unbound manual runs remain usable in their admitted runtime.
Takeover uses the existing typed `execution.RecoveryTarget`: the native Forge
adapter admits its member login, while a desktop host can admit a registered native
checkout. No request body chooses the destination. Authority is checked again after
run-scope/binding/destination reads, before effects. The coordinator still owns
settlement, pending/uncertain retirement, current broker authority, retry journals,
context receipts and exact recovery replay. Missing ports, unknown scope and
cancelled/revoked admission cannot reach the writer. A saved grant, receipt or
settled run never bypasses current admission.

`RunStatus.Read` owns recorded run/binding views, saved context selection and
private delivery-retry/repair projections. It requires current write authority,
an explicit canonical runtime scope and a typed `execution.RunObservation` port.
Missing observation is distinct from unknown runtime state and never proves
retirement. Observations must match the requested run/workspace/runtime kind;
native observations must also advance the captured process/tool/custody identity.
Only display facts leave the service: bounded output, phase, invocation and
appliance fields when actually applicable. Native executable, process-start,
worktree and custody metadata stay private. Confirmed retirement retains the
existing display value; other typed retirement states remain explicit. A terminal
phase alone is not retirement proof. Context and repair evidence keep their
existing owner/scope/bounds; missing verification admission cannot invent action
availability. Current authority is checked again before returning any result.
The native adapter alone handles Project visibility, scoped registry joins and
host transport, including refusal of unmapped equal-number bindings. Reads neither
launch/retire work nor mutate captured observations, custody or usage.

`RepairFunding` owns repository repair allowance/history reads and durable grant
commands. Its current-administration port receives canonical actor/repository
scope; the authenticated host principal stays separate from the recorded sponsor.
Exact replay retains the original timestamp, predecessor and revision even after
the head advances. Increases require an explicit effect and never refill prior
charge history. The original sponsor remains immutable: other current repository
administrators can read the shared history but cannot inherit funding decisions.
Known missing grants return an explicit unfunded view with empty lists; unknown
reads return no partial result. Administration is rechecked after journal reads
and before saving/returning, including replay. The existing store transaction and
immutable command/charge journal retain final CAS, exact replay and finite spend
admission; no new ledger, Project, broker authority or dispatched work is created.

`RunOutput.Open` creates a server-owned read-only attachment over the existing
bounded output record. The host supplies canonical runtime scope, current output
authority and a source reader; no HTTP, WebSocket or appliance client enters the
service. Every slice rechecks recorded run/workspace/runtime/display scope and
authority before and after I/O, pins the process invocation and validates byte
counts, bounded cursors and metadata before any frame is returned. Native sources
need no container/unit or native Project. Binary output is canonical base64 so
ignored producer CR/LF cannot expand a frame beyond the proxy bound. Gaps and
truncated attaches keep source-selected cursors; terminal output drains before
EOF. Known missing output, changed incarnation, invalid data, lost authority and
transient failure remain distinct. The Fountain adapter retains its native
handshake/session/contribution/origin checks, peer cancellation, read-only socket
policy, pacing and heartbeats; both host reads and frame projection use the shared
attachment. Reconnection only reads retained bytes and never starts/resumes work.

`Acceptances` owns exact-input human acceptance preview, admission and withdrawal
behind canonical actor/repository scope, current write authority and evidence
ports. Preview retains source revisions, digests and dependency validation,
rechecking authority after evidence before returning. Approver and withdrawer
come from the admitted canonical actor. The existing coordinator requires a
current-authorization callback and rechecks it after evidence before persistence
or replay return; missing authority never admits. Its existing CAS, immutable
decision/replay journal, prerequisite checks, withdrawal latch and settlement
remain authoritative. Internal generated-plan adoption supplies its current
accepted-intent and owner checks. The native HTTP adapter alone interprets
sessions, origin/query rules and native Project joins; native IDs never become
canonical attribution by numeric coincidence. These separate authority/evidence
reads do not form an atomic provider/store transaction or qualify another host's
runtime execution.

Provider proof/effect/authority adapters,
remaining project-work views and full host-neutral admission
remain in T03-APP.
These extracted services do not complete the standalone application or admit
another provider/runtime's agent execution.
