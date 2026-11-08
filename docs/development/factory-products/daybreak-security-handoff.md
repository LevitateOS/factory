# Open security gates and Daybreak handoff

Updated 2026-11-08. All broad security gates below remain open. This is the
current evidence register; the [implementation queue](implementation-tasks.md)
is the single queue. Follow the [language allocation](language-allocation.md)
and [owner decisions](standalone-assumptions-audit.md). Completed redesign
D1–D8 does not close historical security gates D1/D2/D3/D5 or finish Factory.
Routine security implementation/checks belong to normal coding agents. Use a
user-started Daybreak session only when work or a check looks like hacking.
A build, fixture, source review or component probe cannot qualify another scope.

All remaining implementation, connected/installed qualification and security
closure tasks are consolidated in the
[central task list](implementation-tasks.md#unfinished-integration-and-qualification),
including a [mapping of all 13 gates](implementation-tasks.md#security-closure-coverage).
Keep this handoff as the evidence register; update task status/order there.

The complete prior handoff, including all original requirements, attempts,
uncertain outcomes and evidence paths, is retained in exact Factory Git object
`fe40c5ef51d1f6be0b541efa1a9d8f723d7092da:docs/development/factory-products/daybreak-security-handoff.md`.
Use `git show` with that object to retrieve it. Historical locators: standalone
corrections at lines 14–1462; retained recovery and connected evidence at
1463–2485; the earlier gate register at 2486–2502; subsequent producer/write
history at 3010–5661. Earlier statements that native coordinator/MCP ports or the
Fountain connector are absent are historical, superseded by the current source
and evidence below. Original outcome records are not rewritten by this summary.
The pre-existing T06-CONNECT Active row/note remain verbatim as historical
conflicting status. On 2026-11-08 the owner authorized the ordered Q01–Q30 goal;
Q01 is Complete within its source/native scope; Q02 is Complete within its
source/component scope; Q03 is Complete within configuration/admission scope;
Q04 is Complete within local placement scope; Q05 is Complete within ordinary
boundary admission scope; Q06 is Complete within ordinary preparation scope and
Q07 is Active.
The owner accepted current native diagnostics plus the complete
connected lifecycle as its closure evidence, retaining the unavailable historical
crash cause as unknown. No current lifecycle requirement is waived.
Q01 implementation/evidence: Factory `d5b4506`, `67b6b16`, `5544082` and the
[decision log](../untangle-codebase.md#q01-completion-current-native-session-2026-11-08).
Actual current pre-consent, approved connected session, native minimize/restore,
keyring outages/recovery, scoped drafts, explicit Disconnect, refresh, execution
retry and production Quit passed. These source/native checks close no installed
or security gate. The observed canonical protection snapshot was unavailable;
the original aggregate consent flag was incorrect and does not qualify it.
Q02 implementation: `cf44374`, `699106a`, `031b03d`, `5fa71ea`; native login/reopen,
real refresh cadence, tool races and current source checks are recorded in its
[completion evidence](../untangle-codebase.md#q02-completion-current-inputs-and-owning-update-paths-2026-11-08).
Remote dependency-job activation is still pending publication/finalization;
installed updater activation remains Q28. All 13 security gates remain open.
Q03 configuration/admission: `d4b05f0`; actual native disabled-policy/funding,
explicit role/judgment choices, prerequisites, conflicts and withdrawal passed
with declared read-only provider facts and the retained isolated Muse home.
[Evidence and limits](../untangle-codebase.md#q03-completion-owner-configuration-and-explicit-funding-2026-11-08).
Paid execution, connected permission matrices and installed/security gates remain open.

Q04 local placement: `23508db`; the full native journey passed current generation
consent, placement conflicts, explicit enrollment/disable and account/repository
retirement. Existing PostgreSQL controller-race and disabled/unfunded admission
checks passed. [Evidence and limits](../untangle-codebase.md#q04-completion-canonical-local-placement-2026-11-08).
Only the owned read-only fixture was enrolled; no model call or real repository
intake ran. Remote placement is Q19; all security gates remain open.

Q05 ordinary boundary admission: current canonical source at `6c4dc40` passed
actual isolated reviewer finite/cancel/Quit and admitted coder MCP/artifact/result/
restart checks. [Evidence, exact failed-run reference and limits](../untangle-codebase.md#q05-completion-ordinary-native-boundary-admission-2026-11-08).
This adds ordinary admission evidence to gates 7–12; no whole security gate closes.
The timed-out startup fixture remains unresolved for Q09; real provider-task and
installed recovery evidence is still required.

Q06 ordinary preparation scope is complete in `82e68b3`, `cb841bd`, `def29b7`.
Actual larger UI build/worker/restart, public/offline/DNS/aggregate-budget behavior,
capacity/waiter checks, interrupted overlapping transfers/reopen and snapshot
cancellation/reuse passed; [exact evidence and limits](../untangle-codebase.md#q06-completion-ordinary-preparation-and-resource-recovery-2026-11-08).
The earlier failed workload runs remain fenced, including candidate capture run
`b8affa6b0bdfe1dc88bf55af010a4c7d` for Q09; no result is inferred. All gates stay open.

## Current gates, owners and closure evidence

1. **Workflow jobs / branch protection (T06-GITHUB).** Native authenticated reads
   and canonical Go projections exist in Rust `github_provider/{evidence,protection}.rs`
   and `pkg/factory/runtime/native_product_{jobs,protection,evidence}.go`.
   T06-GITHUB-EVIDENCE is a completed producer, not passing merge authority.
   Connected/private reads, current head/protection, continuation, refresh and
   shared UI passed; declared native/Go consistency/refusal cases passed under
   `/home/vince/tmp/factory-github-declared-1105/attempt9/` (62 cases).
   Q10 reverified actual provider-side revocation/refusal and historical
   observer-consumer migration, and completed the supported connected permission
   cases. Remaining: Q11 rules, full delivery and installed admission.
   Positive classic policy mutations were declared; a live public classic read
   was unavailable. No atomic complete provider history is claimed.

2. **Desktop authentication / IPC admission.** Production startup, native
   PostgreSQL/Go readiness, shared UI, private IPC refusals, streams/windows,
   second-launch activation and production Quit passed the existing real
   Tauri/WebKit lifecycle check at
   `/home/vince/Projects/d7-lifecycle-verified/run.log`. Both regular/native-check
   builds and supported flag/refusal cases passed (redesign D7).
   Owners: `cmd/soda-desktop/src/{main,check}.rs`, native runtime/supervisor and
   `pkg/factory/runtime/native_{application,server}.go`.
   Remaining: real installed startup/sign-in/Quit, authenticated admission across
   restart, provider withdrawal and OS sleep/wake. The retained pre-consent
   heap/segmentation failure has unavailable original artifacts and its cause
   remains unknown; preserve retained profiles/evidence. The owner's Q01
   clarification accepts current diagnostics rather than reconstruction of the
   lost crash. Neither the human CONNECT note nor a current pass diagnoses it.

3. **GitHub credential custody / revocation (T06-GITHUB and consumers).**
   Rust `credentials/` and `github_provider/` own authenticated connectors;
   Go/UI consume bounded observations. Actual consent, keyring, private pages,
   refresh, local account withdrawal, shared UI and Quit have source evidence
   under `/home/vince/tmp/factory-consent-keyring-1105/`.
   Local withdrawal is not remote provider revocation. Q10 reverified the actual
   remote revocation evidence and completed the read-only reviewer case below.
   Installed custody/admission and later product journeys remain required.

4. **Security D1: standing GitHub automation (T11-INTAKE/T12-GITHUB-PRS).**
   Local-owner policy, saved connection, accepted-input/source and background
   observations exist in `pkg/factory/control/` and `runtime/native_*` with Rust
   current credential leases. Redesign D5 normalizes requests once and enforces
   an explicit background-operation allowlist before credential access or write
   claims. Existing operation/receipt/replay/uncertainty/cancellation/withdrawal/
   restart checks passed at `/home/vince/Projects/d5-operation-verified/`.
   This does not qualify unattended intake. Closure needs eligible own-issue
   intake, polling/cursors/wake, no recursion or unauthorized spending, admitted
   placement, real isolated execution and the full issue-to-PR/review/CI/merge
   pipeline. Existing controls must not auto-enable automation or charge accounts.

5. **Security D2: Fountain desktop authentication / grants (T10-GATEWAY).**
   Actual gateway source is retained on canonical Fountain main in
   `2e57559c0e4844baba861a86632d083192537ff3`, merging original commits
   `778e08aa98329848ddca1c07295120cf5fd082a8` and
   `1a29a5e86e06e370486afeee689eaaf54ea04b7b` while preserving ownership commit
   `e88b50dadb91b5ea4bb39dcf80307939d3270fed`.
   [Direct source and history references](fountain-gateway-review/README.md)
   replace Factory's patch copies. The existing PostgreSQL OAuth enrollment
   check passed on this combined source at
   `/home/vince/Projects/d8-gateway-verified/run.log`: S256 exchange, exact replay,
   repository read, withdrawal/inactive/403 and bound-proof refusals.
   Reused `/home/vince/tmp/fbound23.CWdTev/result.log` additionally proves native
   TLS/private fixture CA, real isolated keyring, profile-root reopen, replay,
   withdrawal and subsequent read refusal. Factory's existing
   `lib/desktop-runtime/src/{fountain_provider,mcp_signing}.rs` derives a separate
   domain proof and uses current credential-revision leases; credentials/proof
   do not cross Go/UI. Scope is repository read only. Closure needs actual
   configured-host desktop login/refresh, old-install migrations, concurrent
   withdrawal/expiry, installed profile/worker custody, central runtime placement
   and separately admitted operation grants/journals. Candidate transfer and
   publication remain later work. Fixture accounts are not personal/deployed
   qualification. A handled concurrent reservation initializer duplicate remains
   unqualified for startup concurrency; no reservation/locking redesign was made.

6. **Native recovery intent (N3/N4/security D3).**
   `pkg/factory/execution/{native_intent,launch,native_record}.go`, Store native
   recovery/observation/retirement and Rust `factory_provider/{intent,custody,recovery}.rs`
   retain exact immutable launch intent and require complete sealed evidence.
   Existing actual registry/ELF/private-keyring/Go checks preserved intent across
   process/custody recording and restart, refused changed/partial/missing seals,
   and fenced historical intent-only starts (full original evidence in frozen
   handoff, “Current post-ledger resource recovery evidence”).
   Native admitted source/worker/artifact/restart checks at
   `/home/vince/tmp/fwrk6.DHbN7l/run.log` cover declared authority only.
   Q09 ordinary standalone launch/write/lost-reply and local correction recovery
   now pass in `0b289f6`, reusing `e91534b`/`feaa292`; exact commands, retained
   journals, stop-based accounting and scope are in the
   [completion decision](../untangle-codebase.md#q09-completion-standalone-launch-recovery-2026-11-08).
   Closure still needs connected candidate/outcome settlement, actual provider handling and worker/owner
   crash/reboot/installed Quit retirement. Never synthesize historical intents
   or infer authority from a receipt/custody record alone.

7. **Security D3: isolated worker / execution (T09-NATIVE-EXECUTOR/EXECUTION).**
   Owners: `cmd/soda-factory-{broker,worker}/`, `lib/native-process/`, Rust
   `factory_provider/{broker,run}.rs`, Go `pkg/factory/{execution,control,runtime}/`.
   Source now composes the single native coordinator and semantic execution
   ports. Redesign D1 binds issue/run/intervention services to the same account,
   Store and coordinator; missing authority, inspection or recovery adapters
   explicitly hold the affected action. Recorded issue/run/pause receipt and
   refusal checks passed at `/home/vince/Projects/d12-native-verified/work-detail-result.json`.
   Distinct-UID/file/keyring denials, process/tree/deadline and broker/relay/agent
   component probes are retained in the frozen handoff. Degraded browsing with
   unavailable isolation passed at `/home/vince/tmp/fdeg6.cXjzVR/result.log`.
   These do not qualify the production worker lifecycle. Closure requires exact
   real admitted UID/mount/role/worktree bindings, file/process/keyring/session
   denial, network/quota/cross-run isolation, scoped credential withdrawal,
   candidate/artifact capture, bounded retirement and transaction reconciliation
   over interrupted launch, crash/reboot and production Quit, with a successful
   finite real provider task. No same-user fallback, dummy token or normal-path
   fixture closes this gate. Dependent publication/intake/packaging stay pending.

8. **Reviewer / coder tool admission within security D3.**
   The agent, Rust `factory_provider/{owner,response}.rs` and Go execution
   admission own trusted roles and policy. Actual Muse advertised workflow/memory
   functions despite reviewer flags; production policy reduced the retained
   88,434-byte body from 11 functions/128,000 output tokens to two reads/4,096.
   Scripted responses refused unoffered functions (original evidence retained
   in frozen handoff). Later real source runs do not establish a complete
   successful review. Closure requires actual admitted reviewer/coder allowed
   and denied model/tool events through production custody, execution and
   retirement. Do not promote scripted output or a rejected dummy key to proof.

9. **Provider credential proxy within security D3.**
   Rust `factory_provider/{run,owner,response}.rs`, `credentials/` and the worker
   relay own exact-run endpoint/custody lifetimes. Reusable owner keys must not
   enter the worker. Actual local Muse source withdrawal passed at
   `/home/vince/tmp/fmuse-withdraw7.1jegf3/result.log`: another source remained
   usable while withdrawn capability/MCP/process/root use was refused/retired;
   re-enable did not revive it. No provider request/login mutation ran there.
   Closure needs final authenticated endpoint/network confinement, successful
   owner-held credential use, real response admission, expiry/withdrawal/owner
   death/restart denial and Go settlement. Token-lane source without a composed
   task owner remains unavailable and lacks an early-stop stamp. Dummy-key
   rejection is not custody qualification. Reused CLI sessions do not require a
   separately verified billing identity under the recorded owner decision.

10. **Worker MCP transport / policy within N4/security D3.**
    `pkg/factory/mcpserver/{bound,catalog}.go`, native listener/current-run binding,
    Rust `factory_provider/{mcp,owner,run,response}.rs` and worker agent/relay now
    compose the trusted catalog and signer. The earlier “missing port” blocker
    is superseded. Existing managed MCP/recovery checks passed startup/restart,
    role/peer/key/instance refusals and joined retirement at
    `/home/vince/Projects/d5-mcp-verified/run.log`. Cancellation and joined shutdown
    are preserved. Closure still needs a real provider Factory-tool task under
    canonical admitted capability/current-work/role/expiry/revocation checks,
    exact backend/instance binding, no IPC/root/provider secret in the worker,
    transactional outcome settlement and installed recovery. Declared MCP facts
    cannot close that complete journey.

11. **MCP signing-key custody within N4/security D3.**
    Rust `mcp_signing.rs` owns native derivation/lifetime; Go's native client in
    `runtime/native_mcp_signing.go` uses the current supervisor socket. Separate
    appliance key handling remains in Go. The existing managed checks above
    prove stable fixture-key reopening, exact key/instance/scope admission and
    retired-socket refusal on two incarnations. Production binding is present;
    its existence is not installed custody proof. Closure needs actual admitted
    worker delivery/current-state/expiry/revocation and rotation/restart, with
    root key absent from Go/UI/logs and the isolated worker unable to acquire
    same-UID shared-IPC authority. Keep uncertainty fenced after cancellation.

12. **Authentication / admission at other native boundaries.**
    Broker, worker, desktop, native product HTTP/IPC and grant owners retain
    their existing peer/bounded-contract checks. Each activated production path
    still needs authenticated exact instance/profile/run/resource binding,
    foreign/stale refusal, cancellation/retirement and no credential leakage
    through UI, logs or observation DTOs. Source validation is not admission.
    Redesign D3 preserves exact PostgreSQL known-prior/current/unknown constraint
    comparison and locking (upgrade/reopen/data checks in
    `/home/vince/Projects/d3-pg-verified.log`); this is schema compatibility only.
    Redesign D4 expired-waiter/no-effect, later-valid-request and revision/head
    checks passed at `/home/vince/Projects/d4-progress-verified.log`; it changes
    cancellation-aware bounded serialization, not the isolation/security claim.

13. **Security D5: issue-only Fountain collaborator parity.**
    Fountain extension/adapters and standalone `runtime/native_product_reads.go`
    still require code-unit read. Existing code-reader/shared UI evidence is not
    issue-only parity. Closure needs a separately authorized Fountain capability
    change and actual host issue-only reads/writes plus out-of-scope denial.

## Current product blockers and scoped evidence

The full pipeline remains required; independent personal-account AI inspection
publishes COMMENT, never an inferred provider approval. An optional designated
automation identity may vote only under actual provider rules. Existing
local-owner execution/spending controls do not authorize account autocharging.
Owner choices and exact requirements remain in the assumptions audit.

The latest real Muse planning run `518ab9643f2a381dd81b4814df003979` at
`/home/vince/tmp/fmuse-ready-admission8.z4gCPL/` returned a string where the typed
finish requires an object; no accepted terminal proposal was produced. Recovery
recorded needs-human with exact usage/process/root retirement and no replay.
The origin of the encoding mismatch is still unverified. Preserve all earlier
failed attempts in the frozen handoff. Do not weaken the schema, change the
selected source or infer success from final text. Closure needs accepted typed
plan -> public Builder -> accepted implementation -> final checks/candidate,
accounting and retained restart, then the rest of the full pipeline.

Workspace setup A21 composes native pinned fetch/read, editable source/setup
review, role preparations and durable pending approval. Approval alone retains
`Verified=false`, not installed readiness. Actual setup/readiness/withdrawal/
retirement passed `/home/vince/tmp/fsetup-onboarding18-final.BfmxW9/result.log`;
selected source digest/refusal/readback passed
`/home/vince/tmp/fsetup-source19.hljasE/result.log`; reload/exact retry/account
separation/storage refusal passed `/home/vince/tmp/fsetup-retained24.OndeuO/result.log`;
pre-effect invalid tools with unchanged heads/no preparations passed
`/home/vince/tmp/fsetup-validation24.uMf49B/result.log`.
Source pack/wire/preparer limit remains 64 MiB; broker memory 256 MiB. Connected/
installed onboarding, withdrawal during I/O, old-profile restart/reactivation,
maximum bounds and dependency-download isolation remain pending. A05/A19 source
reads and A11 role/source spending controls have their recorded scoped evidence
in the audit; they do not qualify successful model review or the full pipeline.

Q07 scoped update (2026-11-08): real connected native source review, both role
preparations, exact pending-consent recovery and the shared acceptance preview
now have current evidence, including a 64-file material readback. Controlled
native account/controller/approval withdrawal during source I/O also passed.
`4bed2b1967b6e63a5b10cdc249dbe473c8a204da` completes the setup envelope and bounded
read/planning-limit fixes. With owner-approved fixture comment
`6720378010`, `9ecb2989a03b3ab03a132d94e7e0f8b4423c74a8` completes the ordinary
Q07 scope: real UI comment/dependency acceptance, native hidden/stale refusals,
exact accepted-input readback and approved planning-source assembly passed in
`/home/vince/Projects/q07-acceptance2/run.log`. Actual Muse planning/execution is
still Q08; installed variants remain pending.
`a312951` qualifies explicit legacy recovery refusal without inventing missing
provenance or replaying setup; automatic legacy recovery remains unavailable.
No model call or security-gate closure
is claimed. Preserve the historical scope/status text above; current references
and limitations are in the [Q07 decision evidence](../untangle-codebase.md#q07-connected-setup-increment-2026-11-08).

Q08 current native evidence: `5bd4c825a2bb076984d56d850684608b110d6b00`
passes the owner-approved live Architect/two-task Builder journey in
`/home/vince/tmp/q08-native-plan8/result.log`, including exact receipts,
candidate export, original-source usage and restart without inference replay.
Combined live regression evidence and limits are in the decision log below.
The owner accepted current checks while retaining the historical encoding cause
as unknown. Q08–Q10 are Complete within their recorded ordinary scopes;
Q11–Q30 remain pending. All 13 security gates stay open. See the
[current evidence and limits](../untangle-codebase.md#q08-current-native-journey-and-remaining-historical-attribution-2026-11-08).

The public protected-branch pre-capture case passed real native/WebKit/Go/PG at
`/home/vince/tmp/factory-branch-rule-admitted-1106.XTR8go/`: disabled UI, POST 403,
exact new-command GET 404, unchanged normal open-PR/head readback and owned local
account withdrawal. It closes only that case, not the complete rule/permission
matrix. Original command `3023a7d539af47ac9a7253d51165c186` under
`/home/vince/tmp/factory-branch-rule-coherent-1106.3M3UEV/` remains reserved,
indeterminate/needs_intervention, `invalid_github_response`, no causal receipt.
Private Free-plan team commands `3563790745944bdba7991a168752a014` under
`/home/vince/tmp/factory-team-oauth-1106.iWTrMb/` and
`a7ed6e3aa27448539b46b515477e8546` under
`/home/vince/tmp/factory-team-author-1106.yhdQSW/` likewise retain original
reserved/indeterminate journals and null receipts. Current PR/target absence or
presence cannot settle these commands. Preserve their stopped profiles and
causal evidence; no command was replayed or rewritten by this redesign.

Tool updater/source evidence and role/merge/review component receipts remain in
the frozen handoff and audit. Empty-HOME install/update source reuse does not
qualify active-run update races, network confinement or installed activation.
Actual agent inspection remains separate from provider approvals and merge
methods/result provenance. Do not infer a full merge gate from fixture SQL.

## Preserved pending reviewer-permission work

Historical pending record (2026-11-06; superseded by Q10 completion below): staged changes in
`pkg/factory/runtime/native_product_review_requests.go`,
`lib/desktop-runtime/src/github_provider/review_request_effect.rs`,
`frontend/factory/{api/capability-response.ts,pulls/reviewers.ts}` and
`cmd/soda-desktop/src/{github_writes_check.js,check_credentials.rs}` make
reviewer requests require current repository write permission before capture,
native dispatch and UI presentation. TypeScript, Go, desktop and native builds
passed. The existing no-forwarding native reviewer check passed 61 declared
cases under `/home/vince/tmp/factory-reviewer-role-declared-v2-1106.MvieLj/`,
but it does not prove a real read-only role denial. A matching private desktop
fixture is staged at `/home/vince/tmp/factory-readonly-role-1106.fuKsMs/`; no
device challenge has started and the authorized public test team's permission
is still write. Close this scoped case only after fresh sodaos Factory consent,
temporarily changing that team's permission to read, proving the effective
read-only role, real WebKit merge/reviewer UI and POST 403 plus exact-command
GET 404 with normal PR readback and local account withdrawal, then restoring
the original write permission. Keep the staged code unverified until that run.

Q10 completion (2026-11-08): `8f8ed8a` verifies those six restored paths through
fresh `sodaos` Factory OAuth, actual effective Read role, real WebKit COMMENT
writing/readback and disabled merge/reviewer controls, POST 403 and exact-command
GET 404, normal PR readback and local withdrawal. Team write permission and the
original team review request were restored. The changed native owner also passed
63 declared reviewer cases including denied/unobserved push permission.
Exact supported action/token/role coverage, retained revocation/observer commits
and matching artifacts are in the
[Q10 completion decision](../untangle-codebase.md#q10-completion-connected-permissions-and-retained-provider-evidence-2026-11-08).
All broad security gates remain open; no installed, Q11 rule matrix or historical
unknown-send completion is inferred. The stash and unrelated README work remain.
