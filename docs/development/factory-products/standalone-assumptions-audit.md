# Standalone Factory assumptions audit

2026-11-06. Source baseline: `aa6b6d9`; the working tree was clean when this
audit started. Scope: requirements, authority, supported workflows, runtime,
UI and delivery assumptions across standalone Factory and its shared consumers.
The old implementation queue was paused for this audit. Corrections now follow
the owner decisions below. Audit recommendations alone do not adopt requirements
or qualify any security gate.

## Owner decisions after the audit

The user started the correction goal and selected these requirements on
2026-11-06:

- The local computer's owner controls local execution and spending. Provider
  permissions are checked separately for each remote action (A02/A03).
- Factory always requires the full automated pipeline. The proposed planning,
  local-change and PR-only delivery modes were not selected (A07/A10). Preserve
  required review, CI and merge stages while correcting incompatible contracts.
- Independent agent inspection works with one GitHub account. A second provider
  account or bot is optional; submitted approvals and merges must still obey
  the provider's actual rules (A04). A second personal account is not required
  for Factory and cannot substitute for independent human approval. The earlier
  second account was an optional two-user integration fixture.
- Existing Muse session reuse and a browser link only when authentication is
  needed remain the earlier explicit instructions (A01).
- Hiding/minimizing Factory retains the account/repository choices and reconnects
  after fresh status on return. Explicit Disconnect keeps it disconnected (A12).
- Support ordinary Go/Rust/Node/Bun project setup, including installing declared
  dependencies inside isolated workspaces (A17). The current system-tools-only,
  offline preparer does not meet this requirement. Approved provisioning, network
  policy and meaningful resource limits must be implemented and qualified.

The original audit and its recommendations below retain their baseline scope.
They are not all selected requirements. In particular, A07's proposed simpler
delivery modes are superseded by the full-pipeline decision above. Execution
corrections are now active; the old feature queue waits for their resolution.

### Verified corrections

- **A23 — active guidance corrected.** README now describes implemented binding
  and human GitHub journeys separately from pending agent execution/installed
  delivery. The milestone summary matches the fifteen pending table rows.
  Freshness guidance names Factory's actual scope and missing updater automation;
  stale OS design links and the nonexistent Dependabot claim are removed.
  Desktop lifecycle guidance reflects A12/A15. Documentation review only; no
  qualification gate or implementation row is closed by these edits.
- **A15 — native degraded startup and healthy execution verified.** Product
  health is now separate from execution availability. Missing worker installation,
  an installed missing socket or failed coordinator startup keeps browsing alive;
  new preparation/launch effects refuse while inspection and retained recovery
  remain available. Desktop reports execution unavailable and offers explicit
  retry. Actual native product reads and preparation refusal passed with isolation
  absent in `/home/vince/tmp/fdeg6.cXjzVR/result.log`, an installed missing socket
  in `/home/vince/tmp/fdeg-owned6.KIZ1xh/missing-socket/result.log`, and owned
  controller lock refusal in `/home/vince/tmp/fdeg-coordinator6.6gkvTX/result.log`.
  The healthy admitted worker/MCP/artifact/retirement/restart journey also passed
  in `/home/vince/tmp/fdeg-execution6.4F2QOX/run.log`. Provider facts were declared
  and credentials dummy. Native UI compile, typecheck and UI build passed;
  physical UI retry and unresolved-root recovery failures remain unqualified.
- **A12 — selected behavior implemented; UI build verified.** Hidden windows
  retire their obsolete UI binding while retaining connection intent. Fresh
  runtime/account status on return restores the selected account/repository;
  unknown status waits, removed accounts and changed profiles clear the intent,
  and explicit Disconnect prevents automatic reconnection. Runtime-generation
  reset no longer reports an empty account list as observed removal. Typecheck
  and desktop UI build passed. Physical minimize/restore and connected readiness
  races remain part of native UI qualification; no auth/IPC gate is closed.
- **A13 — source correction and UI build verified.** Unknown account/connection
  failures no longer prescribe signing in or unlocking the keyring. Native error
  messages retain their reported cause; observed locked/expired/unconfirmed-refresh
  states receive distinct guidance. A failed status read retains the last observed
  saved account choices rather than reporting them removed. Every command still
  rechecks native admission. Typecheck and desktop UI build passed; a connected
  status-failure journey was not run and no authentication gate is qualified.
- **A14 — native capability projection verified with declared Read access.**
  Authenticated comment/review presentation no longer requires push permission;
  merge still does. Self-approval, locked/closed subjects, draft verdict limits
  and archived/disabled repositories retain their restrictions. Existing native
  Rust/Go/PostgreSQL readback passed in
  `/home/vince/tmp/fcap6.HXgTKE/result.log`, with no provider mutations or retained
  owned grant. This follows the linked GitHub role contract; actual token grants,
  interaction restrictions, required-approval eligibility and the connected
  permission matrix remain subject to action admission and qualification.
- **A04 — Go/storage contract verified; native automation pending.** Independent
  review remains bound to its separate reviewer assignment and exact cycle.
  When its provider account also authored the PR, the coordinator records an
  explicit agent verdict and submits a COMMENT. Correction routing consumes the
  agent verdict; shared UI distinguishes that inspection from provider approval.
  Merge intents bind the comment event, require the exact adopted review receipt
  and a canonical clear, and wait for explicit complete/eligible provider rules.
  Another run cannot manufacture a missing provider approval. Existing PostgreSQL
  review/correction/merge integration checks passed in
  `/home/vince/tmp/frev6.u0FQ5M/result.log`; typecheck, builds and boundaries passed.
  This is not connected automation or branch-protection qualification. Native
  automated review/merge adapters still need to supply and enforce these facts.
- **A05 — native source integration verified; workflow qualification pending.**
  Planning assembly and source recheck now use the enrolled execution workspace.
  Completion source/material and priority capture share a transactional recorded
  workspace resolver; delivery/verification/repair use the execution resolver.
  Pause/resume use recorded ownership and retry uses the canonical run view,
  rather than an appliance Project join. The existing actual native integration
  passed planning's approved README bytes, source-qualified completion capture,
  overview and admitted worker/MCP/artifact/restart settlement in
  `/home/vince/tmp/fwrk6.DHbN7l/run.log`. Go build and boundaries passed.
  Provider authority/policy were declared fixture inputs; no model call ran.
  Native planning dispatch/choice, delivery/repair and pause/resume/retry journeys
  remain unqualified, as do connected/installed and broad security gates.
- **A02/A03 — local native integration verified.** Desktop now exposes explicit
  run-limit and repository-run consent alongside controller/workspace controls.
  Local-owner admission replaces remote-admin admission for local resource and
  policy settings; native settings readback and typed policy writes are wired.
  The existing composed product check passed in
  `/home/vince/tmp/floc6.1PARrb/result.log`: actual Rust/Go/PostgreSQL settings,
  disabled full-pipeline policy and remote-write refusal with declared
  `push=false` and no admin observation. Typecheck and desktop UI build passed.
  No actual GitHub permission matrix, physical UI or broad security closure is
  claimed. Same-account review/merge semantics (A04) remain pending.
- **A06 — local native integration verified.** Saved bound-request overview now
  reports explicit unavailable setup when a host has no reader. Desktop supplies
  a native workspace reader; ready requires both approved role preparations in
  the current runtime. Shared presentation no longer labels unknown setup ready.
  Actual coder/reviewer preparation, HTTP overview, stale instance/profile,
  retirement and unchanged draft passed in
  `/home/vince/tmp/fsov6.BHDkXg/run.log`, followed by the existing admitted local
  worker/restart journey. Provider authority was declared and credentials dummy;
  connected UI/installed behavior and broad security gates remain unqualified.

## Conclusion

The problem is broader than Muse authentication. Some appliance assumptions
remain active in shared workflow contracts; some native adapters cannot satisfy
those contracts; other restrictions were introduced during extraction without a
clear standalone user requirement. Structural cleanup did not resolve these
semantic mismatches.

The most consequential findings are:

- Native dispatch requires operator/capacity grants that the connected desktop
  cannot grant through its current product routes (A02).
- Native policy binds author and reviewer to one account, while automatic merge
  validation requires different accounts (A04).
- Draft planning still resolves an adopted Fountain appliance Project rather
  than the native workspace (A05).
- Repository-bound request overview needs a setup reader absent from native
  composition (A06).
- Existing Muse session reuse is missing; that does not establish a need for a
  fresh sign-in, new credential store or personal keyring interaction (A01).

The supported first journey needs to be specified before more adapters are built.
These findings support targeted contract corrections, not a full engine rewrite.

## What establishes a requirement

1. Current user instructions control: standalone Factory, existing Muse session
   reuse, a browser link if sign-in is actually needed, preservation of existing
   work, and the selected targeted Go/Rust allocation.
2. The plan records earlier owner choices: Linux first, Tauri, shared UI and
   workflows across desktop/Forge, PostgreSQL initially, native worker isolation
   and permission for privileged installation. Those are documented owner
   decisions, not independently recovered original conversations. Revisit them
   explicitly where necessary; do not silently discard them as imported residue.
3. Code, tests and agent-written implementation notes establish current behavior.
   They do not by themselves establish that the behavior is wanted.
4. Provider contracts constrain remote operations. A local machine permission,
   repository permission, provider login and spending authorization are distinct.

The isolation choice is recorded in [the plan](ideal-filetree-plan.md#d3-native-agent-isolation-decision).
The earlier [reuse table](reuse.md#existing-source-is-the-first-donor) says to keep
appliance systemd/cgroup/root assumptions out; later D3 deliberately selects
some of those mechanisms for Linux isolation. The later choice needs an explicit
platform rationale, not an inference that every appliance prerequisite survived.

## Findings

**Mismatch** means source establishes inconsistent or missing composition.
**Restriction** means an actual constraint needs correction or justification.
**Decision** means the desired behavior is unresolved; this report does not call
the existing choice a demonstrated runtime defect.

### A01 — Existing provider login is confused with Factory credential enrollment

**Mismatch; desired reuse is already selected by the user.** Native custody
searches only Factory application/profile Secret Service entries, and execution
enrollment requires that owner's Muse capability. The worker receives a private
HOME and a run placeholder rather than the user's normal login state.

Evidence: [credential lookup](../../../lib/desktop-runtime/src/credentials.rs#L304),
[execution account](../../../lib/desktop-runtime/src/factory_provider/launch.rs#L19),
[worker launch](../../../cmd/soda-factory-worker/src/bin/soda-factory-agent.rs#L672).
There is no supported existing-session reuse path in these owners. The fresh
Muse sign-in/unlock additions attempted before this audit were removed and are
not part of this baseline.

**Correction:** define supported reuse through the native custody boundary. Ask
for browser sign-in only after an actual authentication requirement is observed.
Do not mount the whole user HOME, pass login files into the worker, scrape
undocumented secrets or assume that reuse is already implemented. Muse documents
stored browser sessions and API-key precedence; subscription onboarding keys and
additional billed API keys are different lanes. Preserve the selected lane.
Sources: [Muse authentication](https://dev.meta.ai/docs/muse-code/auth),
[Muse subscriptions](https://dev.meta.ai/docs/muse-code/subscriptions).

Follow-up inspection: the installed CLI exposes `muse serve` and an offline
stable MSP schema export; `auth --help` exposes credential storage, not a token
export interface. Public MSP provides session/turn control and model/usage reads.
These establish a supported integration surface, not authenticated inference,
account/billing proof or an implemented isolated delegation path. No sign-in,
personal keyring operation or model call ran during this inspection.

### A02 — Desktop authority requires grants its product routes cannot issue

**Mismatch.** Effective authority always requires an operator grant and capacity.
Native product composition omits the callbacks needed to authorize their writes.
The exported coordinator/store writers exist; they are not a reachable desktop
setup journey. Checks can seed those rows directly.

Evidence: [authority](../../../pkg/factory/effective_authority.go#L21),
[native wiring](../../../pkg/factory/runtime/native_product.go#L114),
[grant service](../../../pkg/factory/service/runtime_grants.go#L59),
[fixture seeding](../../../tools/native-checks/native_mcp_signing_check.go#L1372).

**Correction:** define the local owner's concurrency/resource consent and expose
its actual setup path. Decide whether separate shared-appliance operator grants
have a standalone purpose. Do not bypass consent by inventing grant rows.

### A03 — Owning this computer is conflated with administering a remote repository

**Restriction; owner decision.** Placement, environment grants and policy writes
all use current repository `Admin` access. A normal contributor cannot configure
their own Factory execution environment through those routes.

Evidence: [shared native admission](../../../pkg/factory/runtime/native_product.go#L111),
[admin check](../../../pkg/factory/runtime/native_product_environment.go#L12).

**Correction:** separate local resource ownership, spending consent, automation
policy and permission for each remote effect. Decide which read/write/fork
contributors Factory supports. This does not authorize weakening provider access
checks or enrolling somebody else's machine/account.

### A04 — Single-account native policy cannot satisfy automatic merge identity

**Mismatch.** Every native operation actor must be the currently admitted account.
Policy therefore binds PR creation and review to the same actor. Automatic merge
construction uses those bindings, but its validator rejects equal author/reviewer
identities. A successful policy save does not prove a usable delivery policy.

Evidence: [native binding](../../../pkg/factory/runtime/native_policies.go#L14),
[policy actors](../../../pkg/factory/service/policies.go#L93),
[merge assembly](../../../pkg/factory/control/merge.go#L151),
[identity validation](../../../pkg/factory/merge.go#L169).

**Correction:** distinguish independent agent inspection, submitted provider
review, human approval and branch protection. Decide the supported single-account
journey and whether separate accounts/bots are optional or required for a
particular automatic delivery mode. Human GitHub writes are a separate path;
this finding does not invalidate their completed checks.

### A05 — Draft planning still requires the appliance's Fountain Project mapping

**Mismatch in the pending native planning path.** The planner resolves
`NativeProjectForCollaborationRepository`, whose SQL joins only an adopted
Fountain host and appliance `projects`. A GitHub native workspace cannot satisfy
that lookup. Other dispatch paths already use the separate workspace contract.

Evidence: [planning lookup](../../../pkg/factory/control/intent_plan_assembly.go#L136),
[Fountain-only join](../../../pkg/store/collaboration_resolution.go#L74),
[native workspaces](../../../pkg/store/factory_native_workspaces.go#L82),
[ported dispatch](../../../pkg/factory/control/dispatch_timeonly.go#L240).
Related completion/priority duties still use appliance joins in
[completion material](../../../pkg/store/factory_completion_material.go#L40),
[completion source](../../../pkg/store/factory_completion_source.go#L133) and
[priority inventory](../../../pkg/store/factory_priority_inventory.go#L479).

**Correction:** carry native workspace/source identity through the complete
supported workflow; keep appliance Project translation in its host integration.
Do not manufacture appliance Projects for desktop. This planner is not currently
wired into normal desktop operation, so no connected planning failure is claimed.

### A06 — Saving a bound request and reading its overview have different availability

**Mismatch.** Native request composition supplies Store and write admission but
no setup reader. Repository-bound overview refuses when that reader is absent.

Evidence: [native requests](../../../pkg/factory/runtime/native_product.go#L78),
[overview requirement](../../../pkg/factory/service/requests.go#L331).

**Correction:** provide native setup readback, with unavailable execution shown
as a capability/status within a readable request. Missing execution should not
invalidate the retained request overview. This needs no new product policy.

### A07 — One automation policy mandates the entire delivery pipeline

**Restriction; owner decision.** Every `RepositoryPolicy` requires exactly coder
and reviewer roles, nonempty CI checks, fast-forward merge and all publication/
review/merge actor bindings. The contract cannot express a simpler qualifying
automation policy for planning, local work or PR-only delivery.

Evidence: [policy validation](../../../pkg/factory/grants.go#L201),
[actor binding](../../../pkg/factory/service/policies.go#L93),
[planning authority](../../../pkg/factory/control/dispatch_timeonly.go#L229).

**Correction:** decide supported delivery modes and apply their actual
prerequisites. Preserve candidate integrity and provider branch protections.
These restrictions apply to shared automation policy, not all Factory actions:
human GitHub merge separately supports merge/squash/rebase. Do not infer an
observed native automatic merge failure from an unwired executor.

### A08 — Original-creation intake assumes human and automation identities differ

**Restriction in automatic initial intake; owner decision.** Initial acceptance
requires corroborated original, unedited creation and rejects a creator matching
any configured automation actor. With automation acting as the desktop user,
that user's own new issue is excluded by this path.

Evidence: [initial acceptance](../../../pkg/factory/control/acceptance_initial.go#L80),
[creator exclusion](../../../pkg/factory/control/acceptance_initial.go#L108).
Explicit maintainer acceptance is a different existing path:
[acceptance](../../../pkg/factory/control/acceptance.go#L119).

**Correction:** define current-state adoption and prevent recursive automation by
causal operation/origin evidence, not merely shared account identity. Decide
whether executable work must originate in a remote issue. Do not claim that all
self-authored work is rejected or that explicit adoption is already reachable
through the desktop; D1 remains unresolved.

### A09 — Local product ownership depends on live collaboration-provider admission

**Decision.** Product binding verifies the provider account and selected
repository before constructing even local product handlers. Private drafts are
owned by that provider actor. Local planning/history access therefore inherits
network/account availability and account switching semantics.

Evidence: [binding before routes](../../../pkg/factory/runtime/native_product.go#L27),
[draft ownership](../../../pkg/factory/runtime/native_product.go#L68).

**Correction:** decide whether local drafts/history/planning should work before
GitHub connection, offline and across account changes. If so, define local profile
ownership and attach collaboration identities at the appropriate boundary.
Continue rechecking provider authority for private remote data and remote effects.
Offline operation is a proposed requirement, not an established promise.

### A10 — Understanding/planning work requires an approved execution environment

**Restriction; owner decision.** Planning selects a ready coder preparation and
approved source paths. Standing authority also expects requirement/approval heads
and ready role preparations. Some approval language describes privileged setup
inspection inherited from the appliance.

Evidence: [planning preparation](../../../pkg/factory/control/intent_plan_assembly.go#L85),
[standing prerequisites](../../../pkg/store/factory_grant_commands.go#L82),
[approval contract](../../../pkg/project/grants.go#L57).

**Correction:** distinguish read-only source understanding from setup and code
execution. Decide which planning operations genuinely require installed project
tools. Preserve explicit consent before setup scripts run and exact source inputs.

### A11 — Multiple funding records do not provide usable role-specific selection

**Mismatch when multiple sponsorships are intended.** Authority selects the
first active sponsorship; dispatch waits if that grant does not permit the role,
without selecting a later suitable grant. A funded reviewer can remain unused.

Evidence: [selection](../../../pkg/store/factory_grant_commands.go#L60),
[role refusal](../../../pkg/factory/control/dispatch_selection.go#L15).

**Correction:** choose an explicit account/allowance per supported role, or offer
one understandable local allowance if collaborative sponsorship is outside the
initial scope. Preserve account and spending consent; do not silently switch
funding accounts as a fallback.

### A12 — Window visibility is treated as UI-session disconnection

**Restriction.** `document.hidden` retires the active product binding and native
UI session. Returning visible refreshes readiness but does not reconnect.
Account/repository selectors survive; this is not logout, draft deletion,
ordinary blur or evidence that background execution is cancelled.

Evidence: [visibility handler](../../../frontend/desktop/connections.ts#L298),
[binding retirement](../../../frontend/desktop/connections.ts#L90),
[visible refresh](../../../frontend/desktop/main.ts#L65).

**Correction:** preserve the user's connection intent, suspend obsolete reads,
then revalidate on return. Confirm whether manual reconnection on every actual
visibility change is intended. Do not equate hiding a window with revocation.

### A13 — Unknown errors prescribe credential changes

**Restriction.** Generic status/connection failures suggest unlocking the keyring
or signing in, even when the cause could be transport, runtime or validation.

Evidence: [account error](../../../frontend/desktop/accounts.ts#L121),
[connection error](../../../frontend/desktop/connections.ts#L208),
[credential-state handling](../../../frontend/desktop/connections.ts#L320).

**Correction:** retain distinct failure classes and recommend the remedy proved
necessary. A login/keyring action is not a generic retry mechanism. These prompts
reinforce the unsupported assumption that an unknown failure requires new
authentication; source does not establish what caused the earlier agent decision.

### A14 — Human review/comment UI capability is reduced to push permission

**Restriction.** Rust maps WRITE/MAINTAIN/ADMIN to `Writer`; Go uses it to expose
ordinary comment/review capabilities. The UI permits only `Allowed`. Backend
effect admission is separate and does not universally require this Writer flag.

Evidence: [permission mapping](../../../lib/desktop-runtime/src/github_provider/capabilities.rs#L86),
[capability projection](../../../pkg/factory/runtime/native_product_capabilities.go#L88),
[UI gate](../../../frontend/factory/api/capability-response.ts#L30).
GitHub's [role table](https://docs.github.com/en/organizations/managing-user-access-to-your-organizations-repositories/managing-repository-roles/repository-roles-for-an-organization)
allows Read users to submit PR reviews; actual token grants, subject restrictions
and branch-protection approval eligibility remain separate.

**Correction:** derive action-specific capabilities from the actual provider
contract. Preserve unknown/refused states when the required facts are missing.
No broader connected permission matrix was exercised by this audit.

### A15 — Execution recovery/readiness can take browsing down

**Restriction; recovery policy decision.** Supervisor startup fails if an
installed broker unit exists but its socket is absent. Desktop readiness also
requires coordinator startup. A completely absent broker installation is allowed;
missing optional provider ports do not inherently fail coordinator startup.

Evidence: [broker inspection](../../../lib/desktop-runtime/src/supervisor.rs#L579),
[desktop ready transition](../../../cmd/soda-desktop/src/main.rs#L91),
[D3 scope](ideal-filetree-plan.md#decisions-that-gate-specific-work).

**Correction:** specify a degraded browsing/recovery state for execution failures
where safe. Preserve uncertain-run fences and retained evidence; do not resume or
settle work from absence alone. D3 was explicitly scoped to execution, not browsing.

### A16 — Executable discovery assumes an enrolled directory layout

**Restriction.** Selection resolves official current metadata, then expects
`muse-bin-VERSION` in a native-enrolled directory. It does not discover supported
installations through PATH. An existing compatible directory can satisfy the
contract; a duplicate installation is not demonstrably required.

Evidence: [tool selection](../../../lib/desktop-runtime/src/factory_provider/tool.rs#L125),
[expected layout](../../../lib/desktop-runtime/src/factory_provider/tool.rs#L157),
[native enrollment](../../../lib/desktop-runtime/src/collaboration.rs#L165).

**Correction:** discover/reuse a supported installed executable, verify and capture
its bytes, and install only when needed. Keep mandatory current-channel checking,
integrity and immutable active-run executable identity. Installation/update
activation remains unfinished; fresh executable identity does not imply new login.

### A17 — The supported project environment is implicit and unusually constrained

**Restriction; workload decision.** Preparation clears the environment, limits
tool lookup to system paths, hides HOME and denies network. This excludes direct
use of common user-managed toolchains and cold dependency installation unless
Factory explicitly provisions them. Setup/check limits have not been justified
against a declared supported project workload.

Evidence: [setup environment/limits](../../../cmd/soda-factory-broker/src/prepared_setup.rs#L33),
[tool lookup](../../../cmd/soda-factory-broker/src/prepared_setup.rs#L109),
[preparer service](../../../cmd/soda-factory-broker/systemd/soda-factory-preparer@.service#L12),
[worker environment](../../../cmd/soda-factory-worker/src/agent_environment.rs#L56).

| Actual material/phase | Current bound | Defining source |
| --- | --- | --- |
| Staged source Git bundle, not raw repository size | 512 KiB | `lib/execution-protocol/src/lib.rs:7` |
| Candidate/result bundle | 4 MiB | `lib/execution-protocol/src/lib.rs:13` |
| Each copied prepared tree: coder repository or HOME | 64 MiB / 20,000 entries independently | `cmd/soda-factory-worker/src/agent_environment.rs:56` |
| Each approved setup/check script | 10 seconds, then kill delay | `cmd/soda-factory-broker/src/prepared_setup.rs:33` |
| Regular file written by setup descendants (`RLIMIT_FSIZE`) | 256 KiB, using the output constant | `cmd/soda-factory-broker/src/prepared_setup.rs:59` |
| Retained preparation material | 512 MiB | `cmd/soda-factory-broker/src/preparation_quota.rs:9` |
| Agent tmpfs | 768 MiB | `cmd/soda-factory-worker/systemd/soda-factory-agent@.service.in:17` |

**Selected correction:** support ordinary Go/Rust/Node/Bun setup and declared
dependency installation inside isolated workspaces. Implement approved toolchain
and dependency provisioning, setup network policy and meaningful bounds, then
qualify representative cold builds and cleanup. These remain dependencies of
native executor qualification, not features supplied by the current preparer.
Keep enforced isolation
and quotas. These constants are observed restrictions, not measured failures on
all normal projects; packed bundles, expanded trees and build output differ.

### A18 — “Linux desktop” conceals a narrower installed support model

**Decision.** Execution requires systemd/private namespaces, cgroup v2 controllers
and a conventional runtime directory. Global worker/relay templates embed one
desktop user/UID. Multiuser installation of the same global units is not supplied.

Evidence: [scope requirements](../../../lib/native-process/src/scope.rs#L15),
[runtime path](../../../lib/desktop-runtime/src/factory_provider/owner.rs#L102),
[broker socket](../../../cmd/soda-factory-broker/systemd/soda-factory-broker.socket.in#L5),
[relay template](../../../cmd/soda-factory-worker/systemd/soda-factory-provider-relay@.service.in#L7).

**Correction:** declare supported distributions/session/service capabilities and
single-user versus multiuser installation. Keep the documented isolated worker
choice pending explicit reconsideration. Do not introduce a weaker execution
fallback or a login remedy for unsupported platform facilities.

### A19 — CLI integration includes a second provider wire-policy implementation

**Decision.** Factory's relay validates/restricts request fields, model, tool
catalog and output/cache choices, then validates bounded SSE responses. A new CLI
release can require corresponding relay changes even when the CLI itself works.
This is more responsibility than merely launching a supported CLI.

Evidence: [request policy](../../../lib/desktop-runtime/src/factory_provider.rs#L247),
[response policy](../../../lib/desktop-runtime/src/factory_provider/response.rs#L125).

**Correction:** choose the smallest supported provider/CLI integration that
preserves role authority, spending limits and secret custody. Justify retained
wire rewriting explicitly. CLI flags alone were previously insufficient for role
enforcement; removing the relay without an equivalent boundary is not a correction.

### A20 — Shared database initialization still includes appliance application state

**Coupling; migration decision.** Native startup initializes the shared schema,
including appliance users/keys/Projects/memberships and application hosting
records. Native workspace support adds dual-owner relationships. This creates
startup/migration responsibility; it does not mean desktop users must configure
SSH, containers or appliance hosting.

Evidence: [store initialization](../../../pkg/store/store.go#L90),
[schema](../../../pkg/store/schema.go#L23),
[native workspace relationships](../../../pkg/store/factory_native_workspaces.go#L33).

**Correction:** separate retained compatibility obligations from new standalone
requirements and initialization duties. Preserve data and the defining schema
owner. PostgreSQL is a selected engineering choice; this audit provides no basis
for an SQLite rewrite or deleting imported tables.

### A21 — Check fixtures can satisfy prerequisites that a user cannot reach

**Evidence boundary.** The joined native check explicitly seeds policy, capacity,
operator and sponsorship facts before exercising real packet/worker/MCP/storage
behavior. This is legitimate component evidence and is documented as such, but
it cannot prove normal desktop onboarding or remove A02/A04–A06.

Evidence: [declared scope](../../../tools/native-checks/native_mcp_signing_check.go#L1261),
[seeded prerequisites](../../../tools/native-checks/native_mcp_signing_check.go#L1372),
[shell execution availability](../../../frontend/desktop/index.html#L61).

**Correction:** define the user journey first and finish its reachable composition.
Use retained authorized sessions for checks when they prove the changed behavior.
Require fresh authentication when authentication itself is being tested, or an
observed session limitation requires it. Do not build another fixture framework.

### A22 — Whole-product completion is broader than an independently usable release

**Release decision.** Packaging and installed GitHub work have independent paths.
However, upgrade/parity and the definition of complete Linux work require Forge,
GitHub desktop and Fountain desktop journeys. Fountain does not block implementing
or installing GitHub Factory through the existing dependency graph.

Evidence: [release dependencies](implementation-tasks.md#sequential-implementation-order),
specifically rows T14-PACKAGE, T13-GITHUB, T14-UPGRADE and T14-PARITY.

**Correction:** explicitly choose the first independently shippable host/provider
milestone and its upgrade requirements. Preserve shared-consumer compatibility
and later full parity; do not silently abandon Fountain scope or call the entire
project finished after one host works.

### A23 — Current guidance mixes historical status, active requirements and OS policy

**Documentation defect.** README says GitHub login/writes and canonical binding
are unfinished, while current code and table record their implementation. The
remaining-milestone summary still lists completed T07/T08 work. The active
freshness document additionally refers to an absent OS updater design and a
Dependabot configuration not present in this tracked checkout, and discusses OS
images/Canlang beyond standalone Factory's ownership.

Evidence: [README](../../../README.md#factory),
[milestone summary](implementation-tasks.md#remaining-product-milestones-2026-11-06),
[current completed rows](implementation-tasks.md#sequential-implementation-order),
[freshness ownership](../freshness.md#mandatory-software-freshness).

**Correction:** make active requirement/status summaries current and keep history
as history. Bound Factory's updater responsibilities to what it owns, and name
actual external owners. The mandatory freshness rule remains in force; missing
updater machinery is not permission to run known stale tools. Neither source
refactoring nor repeated historical evidence validates a requirement.

### A24 — Route scope and selected workspace are treated as the same UI fact

**Restriction; navigation decision.** Shared tabs depend on the current route
containing a repository, so New request removes repository navigation despite
desktop retaining its selected repository. Desktop also deliberately provides a
selected-repository feed/catalogue rather than the shared global-feed experience.

Evidence: [shared frame](../../../frontend/factory/app/factory-app.ts#L445),
[desktop route scope](../../../frontend/desktop/product.ts#L50),
[catalogue](../../../frontend/desktop/product.ts#L169).

**Correction:** define selected workspace/navigation independently of destination
routes. Decide repository-local versus aggregate Feed behavior. Do not broaden
the current admitted repository scope implicitly to implement aggregation.

## What remains justified

- Go owns canonical decisions, durable workflows and SQL; Rust owns native
  execution, resource lifetime and credential-bearing desktop operations; Lit
  owns shared presentation. The audit does not establish a reason to rewrite Go.
- Stable canonical identity must survive rename, account refresh and reconnect;
  a provider locator, credential connection and execution workspace differ.
- Preserve human work, current remote authority, explicit spend/resource limits,
  exact accepted input/candidate identity and causal command outcomes. Unknown
  remote effects must not be replayed as though they failed.
- Isolated native execution, revocable scoped capabilities, actual task/checkpoint
  lifecycle and honest retirement remain requirements until explicitly changed.
  Missing native task ports cannot be replaced with stubs or discarded manifests.
- Native PostgreSQL and shared-host compatibility remain selected obligations.
  Sharing a workflow does not imply sharing appliance administration/onboarding.
- Source, connected-provider and installed/security qualification remain different
  evidence. All broad security gates remain open.

## Decisions before dependent implementation resumes

| Question | Recommended requirement to review | Affected findings |
| --- | --- | --- |
| Who controls local execution? | The local owner controls this computer and spending; provider authority governs each remote operation. | A02–A03, A09, A11 |
| What is the first supported work journey? | Specify whether Factory supports planning/local work/PR-only delivery before mandatory CI and automatic merge. | A05–A08, A10 |
| What does independent review mean? | Separate agent inspection from provider account identity and human/branch-protection approval. | A04, A07–A08 |
| Which projects and Linux installations must work? | Name representative toolchains, dependency provisioning, meaningful limits and installed user/platform support. | A17–A18 |
| What is the first shippable product milestone? | Give standalone GitHub desktop its own explicit release/upgrade criteria while retaining the other host commitments. | A22 |

Existing Muse reuse and a browser link when necessary are already instructed;
do not ask the user to select that again. The engineering mechanism still needs
supported-provider evidence and preservation of the selected custody boundary.

## Correction order

1. Settle the local-owner/account/delivery requirements above. Record decisions
   in the existing plan; recommendations here are not adopted automatically.
2. Correct the authority bootstrap and workspace/setup contracts (A02–A06) and
   existing-provider integration (A01/A16) as complete reachable user journeys.
3. Correct UI failure/visibility/capability projection and readiness boundaries
   (A12–A15); finish the selected project environment contract.
4. Resume the dependency-ready product queue using actual user/setup paths.
   Qualify changed connected/installed boundaries once, at their required scope.

## Review coverage and limits

The 1,227 tracked paths were inventoried at root/package level. Review traced
requirements and established workflow boundaries across the following domains;
it does not claim every source line was reviewed for correctness.

| Domain | Review | Independent challenge |
| --- | --- | --- |
| Product scope, plan, language ownership, provenance and status | Root | Findings reconciled against the three specialist reviews |
| Native custody, execution, source/setup, artifacts, tooling and platform | Native reviewer | Product reviewer checked reuse, startup, layout and exact bounds |
| Go identity, policy, grants, planning, intake, review/merge and storage | Engine reviewer | Native reviewer checked grant reachability, acceptance alternatives, policy scope and Project lookup |
| Human collaboration, shell/shared UI, navigation and distribution | Product reviewer | Engine reviewer checked visibility semantics, UI versus effect authority, release dependencies and status |
| Manifests, source-import record, assets/licenses and check tooling | Root inventory and specialist consumer review | Ownership/consumer scope only; no license, build or exhaustive test audit |

Methods used: the requirements/workflow review from `ideal-filetree-audit` and
the existing `untangle-codebase` guidance, scoped to this requested assumptions
audit. Independent challenge narrowed several findings; those qualifications are
included above. No full filetree redesign, source refactor or test cleanup ran.

Evidence is source inspection, current user instructions and the linked public
provider documentation. No builds, tests, services, model calls, sign-ins,
keyring operations or security probes ran during this audit. No credentials or
private profiles were inspected. No security gate was implemented, bypassed or
marked complete. Future changes must retain the exact remaining closure evidence
in [the security handoff](daybreak-security-handoff.md).
