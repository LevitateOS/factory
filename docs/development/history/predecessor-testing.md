# Testing and native validation

The [mandatory freshness policy](freshness.md) applies to validation baselines. Check current upstream inputs before qualification; historical pinned results do not establish current compatibility.

How to validate Soda changes. Product journeys and native proof requirements live
here; tool transport lives in [Native support](native-support.md).

## Layers

| Layer | What it proves |
| --- | --- |
| Source checks | Go tests, TypeScript typecheck/lint, focused frontend suites |
| Authored browser fixtures | UI contracts against local/fixture servers |
| Native stage checks | `scripts/check-native.sh` against a prepared matching-architecture stage |
| Installed journeys | Real appliance/project behavior on an authorized target |

Cross-compilation or emulation is not native proof. Native validation follows the
[platform scope](../architecture/release.md#architectures).

## Source checks

Use the installed tools and choose checks for the changed behavior. Tool version
numbers are not admission criteria. Ordinary source checks work in a dirty
checkout and require no release preparation or provider enrollment. Unit
tests are forbidden by [policy](no-unit-tests.md); only integration and
end-to-end suites run. Examples:

```sh
go test ./internal/store ./internal/web/...
bun run typecheck
bun run test:frontend
bun run check:source
```

`check:source` runs all source checks by default. Select `bun run check:source go`
for Go tests and repository source guards, or `bun run check:source browser` for
TypeScript and prepared browser suites. The browser selection needs Bun and Rust
for asset tooling, but does not require Go or PostgreSQL.

### GitHub CI

The required [push workflow](../../.github/workflows/ci.yml) runs on pull requests,
main pushes, merge groups, manual dispatch and immutable
`ci/soda-main-FULL_COMMIT_SHA` tags. After committing a clean candidate, run:

```sh
bash scripts/push.sh
```

The command qualifies that exact SHA, verifies its workflow and GitHub Actions
app, and ordinarily fast-forwards protected main after success. The complete
command, including network waits and publication, has a hard **300-second limit**.
A shorter `SODA_PUSH_TIMEOUT_SECONDS` is allowed; values above 300 are refused.
If a timeout interrupts publication, inspect origin before retrying. Never move
a qualification tag to different bytes or bypass protection after a failure.

The required `CI` aggregate rejects missing, failed, cancelled or unexpected
skipped jobs and invalid reuse decisions. Passing checks do not expire because
of queue delays or elapsed time. Each check job retains its execution timeout.
Main admission reuses only its identical full SHA's latest successful push-tag
run of this workflow; tag runs, PRs and reruns execute fresh checks. No path filter
exempts source. Compiler/download caches do not replace `go test -count=1`.

| Push lane | Blocking scope |
| --- | --- |
| Go | Parallel core, Store and Web suites: Go command/internal/tool packages except native factory-control, identity-client and installed-probe integrations; focused CI/source-driver contracts, module integrity, SQL locality, no-npm, no-Python and unit-test-ban guards. All three jobs must pass. |
| PostgreSQL | Every Go job receives PostgreSQL 17 and a required restricted fixture password file; Store/Web tests execute against real disposable databases. |
| Browser source | Full strict TypeScript/Lit analysis and fixtures; native form, personal/repository settings and presentation-inventory source contracts. |
| Rust | Wire-time, Unix HTTP, release-input foundation and release-delivery libraries with the committed lock (`cargo test --lib` runs clean with no unit-test targets by policy). |
| Packaging | Checksum-pinned Hadolint, Compose deployment and readiness contracts. |

The protected main branch still requires `CI` from GitHub Actions, an up-to-date
candidate and administrator enforcement; force pushes and deletion stay disabled.
No additional reviewer approval is required by this policy.

The separate [deep workflow](../../.github/workflows/deep-ci.yml), required for
full regression/release evidence, preserves the complete previous Go, real
Chromium, Rust workspace and packaging suites. It runs on main pushes, PRs,
merge groups, dispatch and `ci/soda-full-*` tags under `Deep CI`. **It does not
block routine pushes.** It always runs its suites independently of push-receipt
reuse. A fast push pass does not prove native integration or release qualification.
It checks out the exact Fountain source pin and retains mandatory PostgreSQL and
Chromium sandbox setup. Source CI does not rebuild the OS; native builds and
publication remain the [release workflow](release.md).

## Native stage

```sh
bash scripts/check-native.sh ARCH /ABS/PATH/TO/stage
```

Stage preparation and candidate production: [Native support](native-support.md).

## Product journeys to cover when touching those areas

- Project foundation: join, terminal, files, Git, shared tools, nested service
- Spaces: create/key/join, native extension page and persistent panel across
  Forgejo navigation and account changes
- Managed terminal: reserve/create/exact attach/End, reload survival
- Operator runners: retained observation/cleanup and refusal of deferred local
  creation; native CI runs on separately managed capacity
- Operator Tailnet: host settings and project enrollment policy
- Installation media and activate flows when those owners change

Exact script names and fixtures live under `tests/` and `scripts/`. Prefer extending
existing drivers over inventing new harnesses.

## Factory acceptance

This section owns how to demonstrate the finished factory, not an implementation
sequence or a checklist required for every documentation or code change. Behavior
and limits belong to the [product contract](../product/overview.md), environment
ownership to [Projects](../product/projects.md), session behavior to
[Spaces](../product/spaces.md), and authority to [Trust](../architecture/trust.md).
The [architecture decisions](../architecture/overview.md#factory-architecture-decisions)
select the boundaries these checks exercise.

### Finished product demonstration

On an authorized installed appliance with the selected Soda, Fountain and Project
OS versions, enable a repository under its separate repository, capacity and
provider grants. Use a supported subscription CLI, a small real codebase with an
approved toolchain/service requirement, and native CI on separately managed
capacity. Keep a joined human's dirty checkout, terminal and service data present.

Demonstrate an issue and a dependent issue progressing through the actual product:

1. Readiness exposes both a declared dependency and a material unanswered
   requirement in the fixture. An authorized answer resolves the question;
   merely closing an unmerged code prerequisite does not satisfy its outcome.
2. Ready work automatically acquires the environment and runs the selected coding
   CLI through the broker, without an operator `admit` command. Spaces displays
   that process's live activity and issue/attempt identity.
3. Coding publishes the exact native PR candidate. Another agent reviews it in the
   same Project with fresh Git state. Demonstrate a blocking finding or actionable
   CI failure returning to the coder, a changed candidate, fresh review and new
   required checks. If the ordinary candidate needs no correction, use a separate
   bounded case to demonstrate this path; do not fabricate an agent finding.
4. The exact reviewed head and verified base pass the complete configured check
   set and the conditional native merge. Soda confirms the merge and required
   issue outcome, then automatically reconsiders and starts the eligible dependent.
   No person performs the routine merge or manually admits the dependent issue.
5. The recorded container identity stays the same through coding/review/fix.
   Human dirty files and service data remain intact; ordinary factory run
   termination leaves the human terminal and service running. Ending a browser
   attachment does not stop factory work.

The demonstration must include real provider executions, native collaboration
records, the real broker and rendered Spaces routes. Mock transport, synthetic
credentials, a terminal marker or an agent's success narrative cannot substitute
for this composed result. Short deterministic fixtures remain appropriate for
the controllable rejection and race cases below; do not spend provider quota
merely to exhaust timers or simulate duplicate events.

### Acceptance cases

| Area | Observable pass and refusal criteria | Required evidence |
| --- | --- | --- |
| Authorization and accepted input | Separately recorded grants permit unattended accepted work even after browser logout. Missing/withdrawn grants or unadopted issue changes deny affected execution and writes. Agent instructions cannot adopt objectives, alter policy or enlarge authority. | Focused policy checks plus native unattended writes and invalidation ordering under the [mutation contract](../architecture/trust.md#conditional-native-mutations). |
| Readiness and observations | Native issue/dependency/comment evidence produces a specific blocker and accepted resolution causes reassessment. Cycles and inaccessible evidence stay blocked. A missed notification is recovered by bounded reconciliation; duplicates and unchanged blockers cause no duplicate agent or repeated model assessment. | Native issue/answer/dependency journey; deterministic duplicate, missed-event and cycle checks against current native rereads. |
| Assignment and preparation | Actual runs identify accepted inputs, approved base, template, role, checkout, harness/model/connection and required outputs. Authorized absent-Project creation and existing-Project reuse follow the [preparation contract](../product/projects.md#preparing-the-environment). Missing shared prerequisites wait for native administration; private setup and service checks precede a provider lease. Unsupported ownership/profile or incomplete preparation prevents launch without destructive replacement. | Inspect native assignments and the selected full Project OS with a representative repository tool and service, using the focused preparation cases below. |
| Role and credential boundary | Each selectable factory harness runs as its assigned non-login role with fresh home/config/auth and a current process binding. Role-private Git state and human data remain protected; publisher secrets and privileged sockets are absent. Capture and stop cover descendants while unrelated workloads continue. Failed return/termination prevents connection reuse; stale container/run bindings are rejected. | Bounded real CLI/broker execution for every harness exposed as selectable, plus targeted process, permission and stale-binding checks. Enrollment or an old disposable-worker receipt alone does not qualify a factory harness. |
| Candidate, review and correction | Native issue-linked publication identifies the exact head; a distinct reviewer receives accepted requirements and candidate evidence without the coding conversation. Independent writable Git metadata prevents shared local configuration. Reviewer edits cannot publish or approve themselves; correction changes the candidate and invalidates earlier review/checks. Disputed/non-progressing work reaches intervention. | Real coding/review/correction evidence in the same Project, native commit-bound reviews and focused role/revision enforcement checks. |
| CI and conditional mutation | The nonempty required-check set and native protections remain effective. Missing, skipped, pending, cancelled or failed evidence, unauthorized changes to approval definitions, or stale head/base/accepted-input/policy/authority cannot produce an automatic merge. A lost response is reconciled before any retry. | Native selected-Fountain checks for head/base changes after preflight/preparation and authority invalidation racing the effective write. Record both orderings: invalidation first rejects; a write committed first is reported accurately. Pending invalidation must not appear as completed cancellation/revocation. Also exercise publication/review authority loss, native protection refusal and ambiguous outcome lookup. |
| Capacity and spending | The [configured limits](../product/overview.md#concurrency-and-usage-limits) bound appliance/repository concurrency, processes, active time, correction cycles and aggregate leased connection time. Queues identify their resource wait and select from a deterministic complete-eligible tournament with bounded judgment input; oldest-first applies only as the fairness fallback within its group (including starvation age), and uncertain preference falls back to oldest within its group. Blocked work holds no execution slot. Duplicate observations, input edits and resume do not replenish allowances; explicit retry preserves history and rolling use. No account/provider or paid fallback occurs. | Deterministic accounting and reservation checks, including multiple repositories sharing a connection, plus native resource/lease enforcement. Use short test limits or controlled clocks instead of hours of provider execution. |
| Spaces and human access | Authorized viewers see actual factory output with correct issue/run identity; input and human-terminal End cannot control it. Navigation, splits, hide/show and closing/reopening views preserve the execution binding. Session/permission loss disconnects views and cached pages cannot reveal another actor's private state. Human Join, member-only terminals/End, explicit-key SSH/SCP/SFTP and ordinary Git remain usable. | Actual factory and human browser journeys through the native host, including forged input/control requests and access loss. Mocked components and native tmux attachment alone do not pass. |
| Intervention and persistence | Block/pause/cancel stop affected agents, preserve work and deny further writes. Cancellation survives issue reopening and new events; only an authorized explicit retry can create another attempt. Takeover follows confirmed stop and credential return/revocation into a member-owned checkout. Project Stop holds admission; Start preserves roots without restoring old runs/leases or independent grants. Controller interruption never silently resumes or duplicates work; failed cleanup remains distinct from outcome. | Coordinated native pause/resume, cancel, takeover, Stop/Start and interruption checks with dirty work and service data present; inspect remaining allowances, process identities and lifecycle authority refusals. |
| Retained appliance support | LAN routes and service endpoints work without purchased-domain or Tailnet prerequisites; optional Tailnet preserves operator/opt-in policy. Private HTTPS/native origin, bootstrap/helper bounds, stock Cockpit, branding/attribution and separately managed native CI remain usable. Local runner creation remains deferred. | Reuse applicable existing operator, connectivity, human-development and UI checks; verify changed installed paths without building parallel support services. |
| Delivery and preservation | The selected candidate is exercised on native x86_64. Installation/update/recovery preserve protected existing state. Obsolete auth/factory callers and fixtures are replaced together; no parallel legacy runtime is needed. | Existing [release qualification](release.md) and [native support](native-support.md) evidence for the shipping candidate. Cross-compilation or emulation does not qualify the native candidate. |

Every advertised coding/review harness must pass its actual launch, result,
credential and stop contract. Do not infer primary Muse factory support from
enrollment or optional tool-worker support. Qualify each selectable harness's
boundary with a small native case; one representative complete product journey
need not be repeated for every combination of otherwise unchanged components.

### Project preparation acceptance

Use the selected full Project OS on an affected native architecture, with an
existing human checkout and service data. The earlier minimal terminal image only
establishes process/filesystem primitives; it does not qualify this path. Use one
representative real toolchain and native development service, not a new generic
provisioning harness:

- Show absent-Project creation and retained-Project reuse, bounded non-login role
  provisioning, missing shared prerequisites reported without launching an agent,
  native administrator preparation, and automatic role-private setup afterwards.
  Confirm a working build/test from both coder and fresh
  reviewer checkouts; exercise an ordinary candidate dependency lockfile change.
- Demonstrate that repository mise hooks, changed setup/service definitions and
  referenced input edits cannot execute as root or silently change approved tool
  selection. Verify both roles lack wheel, shared engine access and write access
  to shared tool/configuration stores. Test the actual launcher environment;
  checking the account record alone is insufficient.
- Check endpoint health and native data permissions as each role. A destructive
  test in assigned disposable data must succeed without access to human data;
  native shared services and the human terminal must survive run termination.
- Interrupt private setup and inspect partial effects before a bounded safe retry.
  Exercise shared maintenance and Stop/Start: holds deny admission/writes, protected
  state survives, and stale readiness/process bindings cannot pass. No preparation
  failure may borrow root, lease a provider merely to wait, or recreate the Project.

These checks qualify the preparation boundary; a small real broker/CLI execution
under the separate role/credential acceptance case still proves agent consumption.
Do not run a production build merely to repeat source or minimal-image evidence.

### Conditional operation acceptance

The [conditional operation contracts](../architecture/trust.md#operation-identity-and-authorization)
share immutable identity, cancellation and receipt rules. Focused merge acceptance
additionally exercises:

- Identical-ID replay without a second execution, changed-intent refusal, and
  cancellation arriving before submission or after a lost submission response.
- Spoofed actor/installation/target refusal, expiry at guarded commit admission, and
  cancellation by the owning installation after its native actor loses write rights.
- Both cancellation/write orderings and restart with an in-flight write, preserving
  operation identity and denial of replay rather than inferring failure from timeout.
- Ref success followed by native bookkeeping failure: retain the committed result,
  withhold factory completion/dependent pickup, and reconcile without another merge.
  Where attribution cannot be recovered, report indeterminate and require intervention.

The [enforcement span](../architecture/trust.md#native-enforcement-placement) needs
controlled pauses after prepared-hook approval but before ref publication. Race
cancellation, a source-branch update and a relevant native policy/input change
against that paused writer; also exercise controller death while the receiver
survives. Prove coverage for selected HTTP/SSH and direct native writers. Do not
infer this from a preparation check, PR mutex, reflog entry or missing completion
callback. Under the [selected reservation protocol](../architecture/trust.md#selected-coordination-mechanism),
also demonstrate:

- Atomic idle/revision observations bracket authoritative input reads; changes before
  claim, including issue/dependency changes before Soda receives an event, reject
  the old revision. Unrelated participating changes may conservatively reject too.
- Competing native ref and policy/input mutations join the gate before taking
  native locks. Required synchronous callbacks reenter without deadlock; forged
  bindings and deferred jobs cannot borrow the owner's authority.
- Controller restart and elapsed deadlines cannot clear an occupied reservation.
  Recovery distinguishes writer quiescence from effect attribution; an unresolved
  outcome retains the fence and does not launch replacement work.
- Expiry before guarded admission rejects. Admission before `not_after` may publish
  afterward; a paused admitted writer keeps cancellation pending until resolved.
  This tests the explicit admission deadline, not a physical-publication deadline.

First prove one real native merge with a competing input/ref mutation, cancellation
and controller loss around the prepared checkpoint. Do not build broad factory
orchestration to test this primitive. That focused proof does not replace the
required inventory and coverage checks for every participating writer family.

The [selected integration boundaries](../architecture/trust.md#background-authentication)
add these focused cases during implementation:

- Runtime and external-service bootstrap work without a browser. Verify both Unix
  peers in the actual container/user namespaces; reject an unbound/ambiguous peer,
  spoofed installation/actor, stale admission and native PAT scope/resource loss.
  Owned cancellation remains available after actor-token or binding withdrawal.
- Replacement/restart retain installation identity; remove/reinstall cannot adopt
  old operations. Rotate service admissions without resubmitting accepted work.
  A duplicate cannot replace its recorded native credential with a fresh token;
  regeneration under the same token ID also invalidates the old credential binding.
  Initial submission requires full current-secret verification even after a cache hit.
- Native hooks require the execution capability as well as internal-channel
  authentication. Reject forged generation, phase, ref effects and policy push
  options; preserve only the existing unresolved execution's bounded callbacks
  across restart. Check credential redaction and retirement after reconciliation.
- Exercise a transaction-plus-direct-Git writer such as `DeleteBranch`, an Actions
  task/status update and a deferred push worker. Missing ownership refuses before
  effects; busy work remains queued and no callback waits on its parent's gate.
  Authority revoked between route authentication and claim cannot survive as a
  cached permission. Complete the participating-writer inventory before enablement.
  Native post-publication Actions notifications retain their bounded completion
  ownership without deadlock or dropped effects; runner updates claim fresh ownership.
- Qualify offline reconciliation with the whole native writer domain stopped and
  restart inhibited. Wrong owner/generation and uncertain attribution refuse
  release. Native admin cancellation alone never provides a force-unlock path.
  Include an interrupted ordinary writer; quiescence or a merge-tip comparison
  cannot substitute for accounting for its own native effects and consistency.
- The first conditional method is `fast-forward-only`: require the exact reviewed
  result, native permission for that method and a real target change. Other methods,
  divergence and no-op updates refuse without policy changes or fallback.

The [other factory writes](../architecture/trust.md#candidate-ref-publication)
require native checks at their own selected boundaries:

- Publish an absent branch and a correction by native smart-HTTP using registered
  installation/actor/operation binding. Refuse wrong or stale admissions, token
  generation, old tip, comparison base, correction PR, extra refs and non-fast-forward
  changes. Native protection remains effective; ordinary PAT-only pushes cannot
  consume conditional registrations. Prove no duplicate receiver after lost push
  replies or restart, and no caller admission reaches Git children or diagnostics.
- Race publication cancellation on both sides of prepared admission, including
  registration before receive and a paused admitted writer. Check the exact ref
  effect, retained fence and native completion independently.
- Create a native PR with effect and receipt in one database commit. Race
  cancellation against that transaction and lose its response across restart;
  recover one attributable PR/issue identity, never a duplicate or adopted match.
  Fail derived-ref/completion work after commit: creation remains committed,
  downstream work waits, and no SQL transaction spans Git.
- Submit both final review dispositions against an exact head/base. Refuse stale
  refs, self-review, insufficient native authority and any existing pending draft;
  draft comments remain untouched. Review/comment, official-state/request changes
  and receipt commit together. Cancellation first leaves those effects absent;
  commit first returns the same review after lost response, with no second POST.
- Exercise native PR/review notifications and internal PR-ref callbacks under
  bounded completion ownership. Preserve Actions behavior without inline gate
  reacquisition or dropped busy jobs; uncertain completion is not blindly replayed.
- Verify stage sequencing with a committed branch followed by failed/cancelled PR
  creation, and an old-head review followed by correction. Earlier effects remain
  attributable, old evidence cannot authorize the new candidate, and each next
  operation obtains fresh native observations and application authority.

The [accepted-input representation](../product/overview.md#accepted-requirements-and-native-records)
also requires focused native checks:

- A verified authorized human creation accepts its exact original issue without
  per-issue administrator admission. Outsider, factory/imported attribution and
  missing creation evidence wait for adoption. Replayed creation cannot overwrite
  a later acceptance or create another attempt.
- Acceptance derives the current native human and code-write permission; an
  issue-write-only editor, forged approver or command-like comment cannot approve.
  Conflicting predecessor revisions and source changes while the action is open
  refuse. Repeated decision IDs return one receipt.
- Edit and revert issue body/title and selected question/answer comments under a different editor;
  versions/title-event identities invalidate the old snapshot despite unchanged
  poster or restored text. Delete selected comments and remove/readd an edge;
  old acceptance cannot revive. Unselected discussion and excluded metadata do not
  change approved inputs. Generic snapshot reads enforce native visibility and
  expose the required versions/occurrence IDs without direct Soda database access.
- Demonstrate accepted code completion unblocking automatically, and refusal for
  closure alone, wrong target/result or changed prerequisite acceptance. Confirm
  a non-code resolution with selected native evidence. Reopen/reclose cannot reuse
  the old resolution occurrence. Longer dependency cycles and hidden closed
  placeholders remain blocked without leaking their private details.
- Distinguish a verifiable factual answer from a product choice or waiver requiring
  human acceptance. An agent assertion alone cannot clear a material question.
- Delay an input-change notification and race a native write against invalidation;
  the native revision/guard still rejects stale authority. Observe dispatch hold,
  outstanding-operation cancellation, preservation of committed effects, new
  prompts and unchanged remaining allowances before replacement work starts.

### Recording completion

Report what was exercised, the result and any concrete remaining failure in chat.
Use existing test output and product records when useful; do not create a separate
completion receipt, evidence packet or status document. Keep credentials and
private content out of the report.

Focused development proof precedes production qualification when the native
boundary is uncertain. Reuse valid unchanged evidence at its stated scope. A failed
release run stays failed; its artifacts may inform a separately identified
development check, not a relabeled or resumed successful qualification. Never
claim the finished factory from source checks, the narrow
[synthetic experiment](../research/factory-capability-map.md#focused-native-findings),
or Jev agreement. Do not build the release merely to approve architecture prose.

## Release component regression boundaries

The accepted RCX/RCX-2 source repairs retain these failure criteria. Exercise the
changed join with the existing component driver; do not replay an obsolete task
schedule. Current uncovered native consumers belong to Factory R03–R06/R45/R46/
R50–R53 (removed from the task list; see git history).

| Existing repair | Boundary that must remain true |
| --- | --- |
| FX01 activation | Register backend cleanup before final activation; failed readiness keeps ordinary HTTP closed, cleanup reachable and stale tokens fenced. |
| FX02/FX05 offline disable and survivors | A fresh manager and missing receipts cannot lose custody of surviving effects. Distinguish two Project incarnations, preserve current identities, retire only obsolete owned effects and avoid optional acquisition while disabling. |
| FX03 native daemon | A task-owned tailscaled failure, systemd recreation and live-daemon recovery preserve lifetime ownership. Maintenance exclusivity excludes Start/Ensure as well as another maintainer. |
| FX04 package recovery | Interrupted package swaps recover offline from validated retained bytes during startup. |
| FX06 current intent | After a completed release, changed administrator intent reconciles current readiness without rewriting rollback evidence. |
| FX07 component relationships | Missing catalog/controller references refuse rather than guessing ownership. |
| FX08 acquisition | Deadlines and cancellation retain their actual failure cause through acquisition and cleanup. |

Current packaged enabled/disabled restart, failed cleanup and survivor cases are
native component evidence. The earlier Soda `730fc205` / Fountain `a4fd2221`
development results keep their exact scope. R51 still needs actual immutable
inputs; R52 qualifies the installed candidate and R53 its public download bytes.

## Native frontend composition

The completed NFX source/browser/installed work remains accepted at its recorded
scope. R03/R43/R52/R54 own changed authority, new feed composition and the exact
new candidate. Preserve these criteria when touching their boundary:

- Both entry orders across global/repository pages, panel views and new/saved
  routes preserve exact URL, query, fragment, Back/Forward and reload behavior.
  Use canonical host types/helpers and version refusal, with an independent
  generic consumer proving one workspace; no iframe, copied DOM or inferred host DOM.
- Native controls and every feature class use the native handler and appropriate
  global/page lifetime, teardown and async fencing. Stale configuration cannot
  resurrect a retired view. Suspension, revalidation, expiry, logout, account or
  permission changes hide private cached state and revoke affected streams.
- Ordinary navigation preserves the actual terminal element, xterm and socket.
  Reload creates a fresh browser instance bound to the exact locator, account,
  Project and process incarnation/PID start identity. Browser retirement is not
  End of the human process. An absent renderer cannot satisfy positive acceptance.
- Saved reviews render their stored presentation; missing historical material is
  reported honestly. GET/render does not call a model or search global receipts.
- Geometry, focus, keyboard, zoom, compact/light/dark presentation, emitted colors,
  fonts and asset attribution remain correct; the native host owns host geometry.
  Preserve intentional all-page design coverage (GUIDANCE-08).
- Installed bytes/schema and retained-data checks bind actual identities. Earlier
  frontend installation proof does not qualify a different complete release.

The [Spaces UX](../design/spaces-ux.md) and feed design
own the intended presentation; these are regression boundaries, not a second backlog.

## Canlang qualification boundaries

The [runtime contract](canlang-soda-os-implementation-plan/production-workerd.md)
owns provider/process/image details. Keep these evidence layers distinct:

| Gate | Required evidence | Failure disposition |
| --- | --- | --- |
| G1 native boundary | Exact locked binaries on native Rocky/Podman; the real compiled app reaches D1 and a callable interface. | Resolve CPU/library/bundle/storage/lifetime incompatibility before orchestration. Historical Miniflare G1 does not qualify direct workerd. |
| G2 complete runtime | Two users, team access, browser/auth/MCP, allowed and denied mutations, Files and durable scheduled work; concurrency and acknowledged state across abrupt restart/replacement. | Repair the specific provider/runtime boundary. A container start is insufficient; use explicit DO/named-queue fixtures when claimed. |
| G3 operations | Production independent of Project stop, host reboot, actual claimed private access, complete stopped backup/fresh restore and failed-upgrade writer fencing. | Keep the particular lifecycle/client criterion open; local ingress is not physical LAN/Tailnet proof. |
| G4 installed release | Exact packaged tools, runtime and app in R51's candidate, with P30's complete UI/API/native journey. | R52/P31 remains unqualified until actual installed observations pass; development artifacts may diagnose without becoming release proof. |

Historical compiled-app, domain/UI, Miniflare/native image, offline tools and
component results retain their scopes. They neither reopen accepted work nor
prove the replacement runtime or composed current release. No broad release build
is needed to answer G1/G2. P32/R55 remains open after the duplicate parent plan's
removal: closure requires G4 and reconciliation of the qualified release's actual
contracts, procedures and attribution. R54 freezes shipping documentation earlier.

## Evidence rules

- Distinguish authored checks, local tests, native builds and installed evidence.
- Protect credentials, unrelated work and explicitly retained state. Keep useful
  failure diagnostics; exact task-owned disposable fixtures and artifacts may be
  cleaned within task authorization.
- A passing receipt supports its stated scope only.
