# Collaboration read contract

`records.go` is the defining Go wire owner. The Lit client mirrors it in
`frontend/factory/api/collaboration.ts`. These records contain no provider SDK,
credential, browser session or execution authority. T06-FOUNTAIN-ISSUES now
normalizes human-admitted native issue pages and complete issue detail;
T06-FOUNTAIN-DISCUSSION adds closed/all filters and distinct issue/PR discussion
comments through the shared service/HTTP owner. T06-FOUNTAIN-PULLS adds PR
metadata paging/detail with native draft/merged state and visible branch
repositories. PR metadata leaves commit IDs unobserved. T06-FOUNTAIN-FILES
now supplies an explicit immutable diff pair and bounded changed-file pages in
the Go `PullFiles` envelope, including observed empty diffs. Closed PR lists
include merged PRs; their resource state remains `merged`.
T06-FOUNTAIN-REVIEWS supplies review paging/detail with current native draft
visibility. T06-FOUNTAIN-THREADS adds bounded thread/detail/reply reads and
nullable original/current anchors with separate recorded native coordinates.
T06-FOUNTAIN-CHECKS adds current-head commit statuses and branch-protection
requirements. T06-FOUNTAIN-ACTIONS adds separately admitted native job reads;
T06-FOUNTAIN-ISSUE-CAPABILITIES supplies repository/issue action facts.
T06-FOUNTAIN-CAPABILITIES supplies typed PR action facts and native normal-merge
preflight. Shared issue screens are T07.
Record definitions alone confer no read permission.

Repository discovery returns the existing `Page[RepositoryRef]` from
`/api/factory/collaboration/repositories`. It uses the current human's native
code access, including owned, shared, team and public repositories, independently
of Factory enrollment or cached ledger membership. Fountain's older owned-only
composer search keeps its distinct purpose. Search accepts repository names or
owner/name; continuations bind the native instance, actor and exact query.
Native repository-ID keyset order avoids offset shifts. Each bounded candidate
window is checked with fresh native permissions, and selected records are
rechecked after assembly. Lost access or changed metadata during that read
conflicts without publishing a partial page. A sparse window may have no items
and a continuation; clients follow it rather than treating it as complete.

This is a live catalogue, not a globally atomic repository snapshot. A new or
newly accessible repository behind the cursor appears after reloading. Observed
empty search results have an explicit available observation and empty list.
Canonical IDs remain stable across native renames; display names refresh on the
adopted host. Discovery returns no mutation/action grants, credentials, counts
of hidden repositories or automation enrollment facts. Per-action capabilities
are a separate producer and every mutation still requires fresh admission.

Repository and issue capability reads use `/repositories/{repository}/capabilities`
and `/repositories/{repository}/issues/{number}/capabilities` under the same
collaboration API. `Capabilities.issue`/`pull` make resource scope explicit:
both null means repository scope, and an issue fact carries the actual typed
native record ID separately from its display number and canonical repository.
Fountain's request-scoped appliance read has no desktop connection ID; its
empty connection field does not invent one. Current human and adopted host
identity remain enforced by the adapter and host composition.

An available capability observation can contain denied, unsupported, unavailable
or unknown action facts. Disabled/external units are unsupported; present units
without current access are denied. Repository issue creation follows native
internal-issue read access, with archives denying mutation. Issue title/body
editing follows poster-or-issue-writer permission. Locked comments require an
issue writer or current site administrator; closed issues still accept comments.
Archive denial applies independently of author/admin status. Current code and
issue visibility is required before disclosing an issue capability resource.

Repository facts leave issue-dependent actions unknown until a subject is read.
PR actions remain unknown when comparison/PR prerequisites are missing; issue
resources mark PR-specific operations unsupported for that scope. Separate
`checks.jobs.read` facts retain Actions enablement and unit access. This producer
does not infer merge, review or thread eligibility from a repository read token.
The typed PR capability endpoint supplies those facts from their native owners.
Pre/post native recomputation conflicts if returned scope/action facts change.
The observation revision is not a native write precondition. Allowed is a
presentation permission fact, not approval of a submitted payload, quotas,
credentials, automation, execution or spending; mutation admission stays fresh.

The installed Fountain bridge forwards queries on its already-admitted
collaboration GET routes to these defining handlers. Their strict query parsers
remain authoritative for cursors, limits and unknown selectors. A separate
bridge allowlist must not silently drop real paging/search before those
handlers. The live issue journey verifies this full native-session path with
populated private repositories, complete bodies and actual access revocation;
component callback checks alone do not establish an installed route works.

`/repositories/{repository}/pulls/{number}/capabilities` returns an exclusive
typed PullRef. Its nullable `head` is the actual hidden PR ref commit; `base` is
the current target branch tip, **not** the diff merge base. Missing refs stay
null. Changed commits or projected permissions across the native read conflict.
Missing head makes current-head checks/reviews unavailable while known comment
and thread permissions can still be observed. Status and Actions read facts keep
their separate unit/global permissions.

`pull.edit` describes title/body edits for the poster or PR writer. Locked
ordinary PR discussion requires a writer or current administrator; the native
review paths have different rules and remain separate. `pull.review` asks the
client to choose a kind; `pull.review.comment`, `pull.review.approve` and
`pull.review.request_changes` carry the corresponding facts. A PR author can
comment but cannot approve/request changes on their own PR. Native closed/merged
review behavior is retained. `thread.resolve` reuses CanMarkConversation for the
PR poster, writer or official reviewer; it does not establish that an arbitrary
thread/draft is readable or a supplied thread belongs to this PR.

`pull.merge` reuses CheckPullMergeable in its ordinary immediate mode with
protection bypass disabled. Enabled normal merge policy, current native
permission/allowlists, draft/closed/merged/checking/conflict state, required
checks/approvals, protections, signing and dependencies retain their native
owners. Both commit refs must resolve, and the source head must match the hidden
head. Manual-only policy is unsupported by this normal-merge fact. Technical
read failures remain unavailable, distinct from denial. This is a native
preflight observation, not an execution receipt or proof that a selected method
will complete. Method/payload/quota checks, fresh authority and exact commit
admission remain required at mutation time. No forced or queued merge is implied.

Repository/actor IDs, issue/PR display numbers and opaque revisions travel as
strings. Issue and PR references have explicit `issue`/`pull` discriminators as
well as separate Go types. Remote record IDs remain distinct from display
numbers. Existing identity records retain their canonical owner in `identity.go`.

The distinct resources are issues, PRs, ordinary issue comments, PR discussion
comments, submitted/draft reviews, inline review comments, threads and checks.
Adapters normalize state spellings to the documented values; unknown state stays
unknown. These types do not introduce a second workflow or persistence journal.

Review requests are distinct from submitted/pending reviews and are excluded
from these pages. Fountain follows native draft visibility: author or an actual
current site administrator. Its visibility filter runs before paging and is
rechecked after I/O. Pending reviews have no submitted timestamp. SubmittedAt
follows the native API CreatedUnix convention. Missing/malformed historical
commit IDs remain an empty, unobserved CommitID; the current PR head never fills
that gap. Nullable stale/official flags preserve observed native metadata, not
merge or mutation authority. A deleted reviewer can have an unobserved author.

Each observation records availability, opaque revision, time and an optional
reason. Page completeness concerns that bounded resource/page, not a complete
history or global atomic snapshot. Null lists/facts are unobserved, distinct from
an observed empty list or false fact. Populate observation states explicitly:
an uninitialized Go struct is not an admitted, valid provider observation.
PR head repository/commit, draft status, check requiredness, authors and dates
can remain unobserved. Commit IDs support SHA-1 and SHA-256.

Capabilities are actor/connection/resource-scoped presentation observations.
Missing actions and null capability sets mean unknown. `allowed`, `denied`,
`unsupported`, `unavailable` and `unknown` remain distinct. Application services
must freshly admit every mutation; no capability here authorizes credentials,
execution, automatic intake or spending. Runtime/admission contracts are a later
producer, separate from these read shapes.

Diffs bind a specific PR and immutable base/head commits. Anchors also retain
old/new paths, side, file-versus-line scope and inclusive start/end lines. The
original anchor survives force-push; a provider-observed current relocation is a
separate optional field. Outdated/unknown state is explicit. Review drafts must
not turn a display relocation into a replacement original anchor.

Fountain file reads resolve its native PR merge base and hidden head ref to
real Git commits. Continuations carry that pair and an opaque scoped cursor;
a moved pair returns conflict. Current base code/PR permissions bracket Git
I/O. File-list completeness and patch completeness are separate: a complete
list can contain binary or truncated patches. The later PR UI consumes these
records; this producer does not claim a connected screen or historical-diff
retrieval after a force-push. Missing native history is unavailable.

Fountain now persists `models/issues.CodeCommentAnchor` with each ordinary or
conditional inline write: full original diff base/head, selected path and side,
scope and inclusive range. Blame commit/line remain separate display facts.
Replies copy captured context; edits omit that immutable column. Startup adds
the nullable JSON/TEXT column through existing native schema sync. Retained
comments stay nil when no original pair was recorded. The thread reader must
preserve that unknown state and derive rename paths only from captured trees,
without substituting today’s references or blame location.

Thread and per-comment original anchors can be null: unknown history is not
an empty diff or a fabricated current anchor. RecordedLocation carries native
blame coordinates without a missing diff pair. CurrentAnchor only comes from
a successful native relocation; errors/missing refs leave it unobserved. Native
invalidation proves outdated=true; a successful placement can prove false.
Thread roots follow native grouping and have independent reply cursors. The
first embedded reply window is bounded; clients follow the comments endpoint
for the rest. Original/current context never grants resolution or merge rights.

Changed files preserve added/deleted/renamed/copied metadata and distinguish
complete, truncated, binary, unavailable and unknown patch content. A renderer
cannot interpret absent content as an empty complete diff. Check state and
requiredness are observations, not a merge permission or guarded-merge receipt.

`PullChecks` binds a typed PR and resolved current hidden head to a bounded
`Page[Check]`, optional `BranchProtection`, missing/invalid required patterns and
the native status-gate class. Fountain reads the latest native status per context
from the base repository at that exact commit. A check's `kind=status` identifies
this projection; `provider_state` and description preserve information lost by
generic normalization. Pending stays pending, error remains a raw error with a
failure conclusion, warning has an unknown conclusion, and future states remain
unknown. An Actions status projection is not a rich Actions job record.

Protection follows the native selected rule, including exact-name priority over
glob rules. It projects configured requirements without administrative identity
allowlists. Per-check requiredness uses the selected enabled context patterns;
missing contexts and invalid legacy patterns are explicit. Observed unprotected
branches differ from unknown protection. These facts are not actor-specific merge
verdicts. They do not replace fresh native merge admission.

Checks cursors bind the native repository/PR, head and complete status/protection
fingerprint. Changed head, latest statuses or projected requirements return
conflict instead of mixing pages. Current code/PR permissions bracket the native
read; statuses do not require the separate Actions unit. Missing hidden refs are
unavailable, rather than a guessed empty page. Details links accept only safe
HTTP(S) URLs, resolving native root-relative links against the adopted host.

The `/checks/jobs` read returns `PullChecks.kind=job` with its own coherent
cursor/fingerprint; `/checks` returns `kind=status`. Cursors cannot cross those
views. Job reads require current code, PR and Actions permissions, enabled global
Actions and an enabled native unit, before and after I/O. Withdrawing Actions
does not withdraw status access. A denied job read is not an observed empty page.

`CheckJob` retains native run ID and display number, workflow path/source commit,
ref/event/trigger, native run state, approval requirement, workflow job ID,
latest attempt and nullable start/stop times. IDs, run numbers and attempts travel
as strings. Different runs with identical job names stay separate. Jobs and
their owning runs must match the base repository/current head; PR-specific runs
must belong to this PR. Commit-wide runs at that head remain relevant. This is
a bounded catalogue of jobs and latest attempts, not an execution/task history
or a latest-run merge verdict. Workflow/event payloads, logs, credentials and
runner labels are excluded.

Waiting/blocked jobs normalize to pending, running remains running, and terminal
skipped/cancelled conclusions remain distinct from success/failure. Raw unknown
numeric states remain available while generic state/conclusion stay unknown.
Job requiredness is null: configured status contexts do not by themselves prove
the requiredness of a named job. Job reads leave protection null and the native
status gate unknown; callers read `/checks` for its separately observed summary.

## Client handoff and verification

`collaboration-response.ts` provides the production decoders consumed by the next
diff slice. They retain int64 IDs as strings, require immutable commit/file/side/
range context, and preserve binary/truncated/unavailable patch states. Transport
uses the existing bounded `readSodaJSON` reader. Markdown remains untrusted text;
its renderer/sanitizer belongs to the later issue/editor slice.

T01-RECORDS checks: Go compilation, full TypeScript/Lit/source guards, formatting,
Oxlint, and actual Go JSON serialization through Bun's bounded production reader
and the diff/anchor decoders. The wire probe retained IDs above `2^53`, an original
old-side multiline anchor, both Git hash formats, renamed paths, four patch states
and null unobserved PR facts. These are cross-runtime contract checks, not real
Fountain/GitHub responses, native rendering or connected journeys.

Logs: `/home/vince/factory-read-contract-go.log`,
`/home/vince/factory-read-contract-ts.log`,
`/home/vince/factory-read-wire-check.log` and
`/home/vince/factory-read-contract-source-guard.log`. The diagnostic producer and
consumer are under `/home/vince/factory-read-wire-probe/`; no unit tests or new
test-runner files were added. The [effect/graph contract](effects.md) is now
complete as T01-EFFECTS. The [runtime/admission contracts](../factory/access/README.md)
are now complete as T01-RUNTIME; production consumer cutovers remain later slices.
