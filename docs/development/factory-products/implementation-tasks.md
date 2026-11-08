# Implementation slices and dependencies

Current product owner: [LevitateOS/factory](https://github.com/LevitateOS/factory).
This is the single current product queue, reduced on 2026-11-08 after redesign
D1–D8. The old implementation goal remains cleared; listing pending work does
not authorize resuming it. Factory is not finished. Source/fixture completion
never substitutes for connected, installed or security qualification.

Read the [owner decisions and findings](standalone-assumptions-audit.md),
[selected language allocation](language-allocation.md),
[current security handoff](daybreak-security-handoff.md) and
[redesign decisions/evidence](../untangle-codebase.md) before dependent work.
The assumptions audit is a findings register, not another queue. Normal coding
agents may do routine security engineering; a user-started Daybreak session is
required only for work or checks that look like hacking.

## Frozen requirements and evidence

The complete prior plan is retained exactly in Factory Git object
`fe40c5ef51d1f6be0b541efa1a9d8f723d7092da:docs/development/factory-products/implementation-tasks.md`.
Retrieve it with `git show` followed by that exact object name. Its original
slice IDs and headings are the locators for all prior requirements, completed
slices, acceptance boundaries and evidence; they remain binding where a current
row depends on them. This is a local Git reference, not a claim of publication.

Historical locators in that object: owner corrections and latest pipeline facts
at lines 1–440; complete ordered table and execution rule at lines 441–1215;
T00/T01 at 1216–1405; T02 at 1406–3734; T03 at 3735 onward; remaining T04–T14
requirements under their original headings. Completed rows are historical,
not an invitation to replay their journeys. All 15 Pending rows below retain
their original IDs, dependencies, outcomes and statuses verbatim.

The committed snapshot records T06-CONNECT Complete. The pre-existing human
Active edit and appended note are preserved below; neither is overwritten by
this history cut. The later instruction to clear the old queue is still in
effect. These statuses conflict and remain unresolved; no new Active task is
selected here.

<a id="sequential-implementation-order"></a>
<a id="remaining-product-milestones-2026-11-06"></a>

## Pending product queue

Existing milestone/order links land here. The full completed-row history and
earlier milestone narrative are in the exact frozen plan referenced above.

| Order | Slice | Depends on | Required outcome | Status |
| --- | --- | --- | --- | --- |
| 331 | T12-PLACEMENT | T01-RUNTIME, T03-APP, T06-CONNECT | Persist/enforce runtime placement, generation and explicit policy enrollment before dispatch. Prove local GitHub single-controller behavior; preserve appliance admission. Remote Fountain placement is qualified in T10-GATEWAY. Automation stays inactive. | Pending |
| 333 | T09-NATIVE-EXECUTOR | T09-NATIVE-PROCESS, T05-CONTROL, T01-RUNTIME, T02-ENGINE, T05-CREDENTIALS | N3: Implement a worker with a host identity isolated from the desktop user's files and keyring, plus headless Rust process/tree/PTY/deadline/workspace/Git/artifact/custody operations behind admitted execution contracts. Broker only scoped credentials; qualify actual isolation, finite execution, cancellation and retirement before the execution consumer. | Pending |
| 334 | T09-EXECUTION | T09-NATIVE-EXECUTOR, T02-ENGINE, T05-LIFECYCLE, T05-CREDENTIALS, T06-CONNECT, T12-PLACEMENT | N4: Supply the admitted Rust execution ports to the single Go coordinator; start it only when dependencies are admitted. Preserve native worktrees, provider custody, provenance, cancellation, candidate capture and ledger recovery. Integrate executor retirement into Quit/crash/recovery before unattended intake; qualify D3 and a real finite provider run. | Pending |
| 335 | T10-GATEWAY | T05-CREDENTIALS, T06-FOUNTAIN, T10-WRITES, T12-PLACEMENT | Supported Fountain desktop login/enrollment, revocable grants, authenticated HTTPS reads/operations and centrally coordinated runtime placement; prove D2 and connect the existing desktop views. Candidate transfer is the next slice. | Pending |
| 336 | T10-PUBLISH | T09-EXECUTION, T10-GATEWAY | Digest-bound candidate transfer/staging and conditional Fountain publication with retained operation lookup; prove native execution to PR and lost-upload/write recovery. | Pending |
| 337 | T10-INTAKE | T10-PUBLISH | Admit eligible Fountain issue changes on the selected runtime; preserve current native authority and prevent duplicate appliance/desktop dispatch. Verify an automatic issue-to-PR journey. | Pending |
| 338 | T11-INTAKE | T09-EXECUTION, T11-WRITES, T12-PLACEMENT | Explicit D1 policy, authenticated polling/cursors, wake reconciliation and eligible GitHub issue-to-PR automation; verify no recursive/self-triggered or unauthorized spending. No Fountain gateway dependency. | Pending |
| 339 | T12-GITHUB-PRS | T08-WRITE, T11-INTAKE | Adopt eligible external GitHub PR updates/reviews/checks with exact head/base and fork-execution authority; replace ignored-PR behavior for this provider. | Pending |
| 340 | T12-FOUNTAIN-PRS | T08-WRITE, T10-INTAKE | Adopt eligible external Fountain PR updates/reviews/checks with exact head/base and fork-execution authority; replace ignored-PR behavior for this provider. | Pending |
| 341 | T14-PACKAGE | T05-LIFECYCLE, T08-WRITE, T09-EXECUTION | Build a locally installable Linux app containing architecture-matching Go/PG/native assets, production UI and notices. Exclude Storybook; installation needs no developer tools or container engine. | Pending |
| 342 | T13-FORGE | T08-WRITE, T12-FOUNTAIN-PRS, T04-REPO-UI, T04-REPO-NATIVE | Build/install with the existing appliance tooling and verify the actual Fountain extension/appliance issue-to-PR/review/correction journey with shared UI and retained execution behavior. No Linux desktop package prerequisite. | Pending |
| 343 | T13-GITHUB | T14-PACKAGE, T12-GITHUB-PRS | Install the Linux app and verify GitHub connect/issue/automatic work/PR/review/correction, external PR adoption, real keyboard input, Quit/restart/wake and permissions. | Pending |
| 344 | T13-FOUNTAIN | T14-PACKAGE, T12-FOUNTAIN-PRS | Install the Linux app and verify Fountain enrollment/issue/local work/upload/PR/review/correction, external PR adoption, placement and revoked-grant behavior. | Pending |
| 345 | T14-UPGRADE | T13-FORGE, T13-GITHUB, T13-FOUNTAIN | Upgrade the installed app/backend/store with retained data, credentials, pending effects, browser draft/command recovery and captured active-run executables. Explicitly qualify recovery of historical unscoped writing without granting unproven commands authority. | Pending |
| 346 | T14-PARITY | T13-FORGE, T13-GITHUB, T13-FOUNTAIN | Change one shared component, rebuild/install both consumers and demonstrate the same visible result in Fountain and desktop. | Pending |

Named dependencies absent from this table are retained completed slices in the
frozen plan; their recorded scope and exceptions still apply. Pending outcomes
may already have partial source implementations (including native execution
ports and the Fountain read gateway); their remaining qualification and product
blockers are stated in the handoff. Historical Complete statuses do not close
those broader gates. Security gates
D1/D2/D3/D5 are the historical gate IDs in the handoff, distinct from the
completed redesign decisions D1–D8. The Linux completion boundary still includes
all remaining rows; issue-only collaborator parity additionally requires security
D5. An unavailable credential, host capability or authorized real fixture is a
blocker, never permission to substitute a simulation. Keep unperformed live
checks, the Muse typed-result failure and the read-only reviewer check pending.

## Preserved human status conflict

The following pre-existing row and note are verbatim, including their status.
They are evidence of the unresolved conflict, not a restart of the product queue.

| Order | Slice | Depends on | Required outcome | Status |
| --- | --- | --- | --- | --- |
| 182 | T06-CONNECT | T06-CONNECT-BRIDGE | N2: Bind production desktop selection/shared UI and Go product services to the current account/repository and real GitHub reads; expose execution as unavailable until its ports are admitted; verify switches/reconnect/drafts and preserve empty filtered issue pages with continuations. Fountain desktop connects later in T10-GATEWAY; its absence must not block the GitHub path. | Active |


T06-CONNECT resumed after the coherent native process producer 2e4bfd5. The
previous turn was progress: actual kernel/process checks passed and the checkpoint
is pushed. Current main is clean; CONNECT is again the sole Active row. Diagnose
the retained pre-consent heap/segmentation failure using the actual native runtime
before another browser challenge. No connector ownership, product policy or
credential boundary is being reopened. Preserve the failed runs and credentials.
