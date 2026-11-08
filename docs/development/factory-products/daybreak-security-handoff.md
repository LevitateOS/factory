# Open security gates and Daybreak handoff

Updated 2026-11-08. All broad security gates below remain open. This is the
current evidence register; the [implementation queue](implementation-tasks.md)
is the single queue. Follow the [language allocation](language-allocation.md)
and [owner decisions](standalone-assumptions-audit.md). Completed redesign
D1–D8 does not close historical security gates D1/D2/D3/D5 or finish Factory.
Routine security implementation/checks belong to normal coding agents. Use a
user-started Daybreak session only when work or a check looks like hacking.
A build, fixture, source review or component probe cannot qualify another scope.

The complete prior handoff, including all original requirements, attempts,
uncertain outcomes and evidence paths, is retained in exact Factory Git object
`fe40c5ef51d1f6be0b541efa1a9d8f723d7092da:docs/development/factory-products/daybreak-security-handoff.md`.
Use `git show` with that object to retrieve it. Historical locators: standalone
corrections at lines 14–1462; retained recovery and connected evidence at
1463–2485; the earlier gate register at 2486–2502; subsequent producer/write
history at 3010–5661. Earlier statements that native coordinator/MCP ports or the
Fountain connector are absent are historical, superseded by the current source
and evidence below. Original outcome records are not rewritten by this summary.
The pre-existing T06-CONNECT Active row/note remain verbatim in the queue with
its conflict unresolved. The old product goal stays cleared.

## Current gates, owners and closure evidence

1. **Workflow jobs / branch protection (T06-GITHUB).** Native authenticated reads
   and canonical Go projections exist in Rust `github_provider/{evidence,protection}.rs`
   and `pkg/factory/runtime/native_product_{jobs,protection,evidence}.go`.
   T06-GITHUB-EVIDENCE is a completed producer, not passing merge authority.
   Connected/private reads, current head/protection, continuation, refresh and
   shared UI passed; declared native/Go consistency/refusal cases passed under
   `/home/vince/tmp/factory-github-declared-1105/attempt9/` (62 cases).
   Remaining: real provider-side revocation/refusal, historical observer-consumer
   migration, installed admission and the complete rule/permission matrix.
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
   heap/segmentation failure still needs diagnosis; preserve its failed profiles
   and credentials. The human CONNECT note does not establish its resolution.

3. **GitHub credential custody / revocation (T06-GITHUB and consumers).**
   Rust `credentials/` and `github_provider/` own authenticated connectors;
   Go/UI consume bounded observations. Actual consent, keyring, private pages,
   refresh, local account withdrawal, shared UI and Quit have source evidence
   under `/home/vince/tmp/factory-consent-keyring-1105/`.
   Local withdrawal is not remote provider revocation. Closure still needs real
   provider-side revocation and refusal, installed custody/admission and pending
   permission cases. The read-only reviewer case below remains unverified.

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
   Closure needs interrupted launch/write/lost-reply reconciliation, atomic
   candidate/outcome settlement, actual provider handling and worker/owner
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

Read-only role check pending (2026-11-06): staged changes in
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
